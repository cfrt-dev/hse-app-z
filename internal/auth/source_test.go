package auth

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"hse-app-z/internal/api"
)

func newStore(t *testing.T) *Store {
	return &Store{Path: filepath.Join(t.TempDir(), "tokens.json")}
}

// rotatingKeycloak behaves like Keycloak with refresh token rotation and
// reuse detection: only the latest refresh token is accepted.
type rotatingKeycloak struct {
	*keycloak
	mu      sync.Mutex
	current string
	gen     int
	delay   time.Duration
}

func newRotatingKeycloak(t *testing.T, first string, delay time.Duration) *rotatingKeycloak {
	rk := &rotatingKeycloak{current: first, delay: delay}
	rk.keycloak = newKeycloak(t, func(w http.ResponseWriter, form url.Values, n int) {
		rk.mu.Lock()
		if form.Get("grant_type") != "refresh_token" || form.Get("refresh_token") != rk.current {
			rk.mu.Unlock()
			writeOAuthError(w, 400, "invalid_grant", "Token is not active")
			return
		}
		rk.gen++
		rk.current = "refresh-" + string(rune('a'+rk.gen))
		next := rk.current
		rk.mu.Unlock()
		// The server has rotated the token before the response is delivered.
		time.Sleep(rk.delay)
		writeTokens(w, accessJWT(t, "user-1", time.Now().Add(3*time.Hour)), next, 10800, 1800)
	})
	return rk
}

func (rk *rotatingKeycloak) latest() string {
	rk.mu.Lock()
	defer rk.mu.Unlock()
	return rk.current
}

func TestSourceValidTokenNoRefresh(t *testing.T) {
	kc := newRotatingKeycloak(t, "r1", 0)
	tok := Tokens{AccessToken: "valid", RefreshToken: "r1", ExpiresAt: time.Now().Add(time.Hour)}
	src := NewSource(tok, newStore(t), kc.client())
	got, err := src.AccessToken(t.Context(), false)
	if err != nil || got != "valid" {
		t.Fatalf("AccessToken = %q, %v", got, err)
	}
	if kc.hits.Load() != 0 {
		t.Errorf("refreshed a valid token (%d requests)", kc.hits.Load())
	}
	if !src.SignedIn() {
		t.Error("SignedIn")
	}
}

func TestSourceNearExpiryRefreshesAndPersists(t *testing.T) {
	kc := newRotatingKeycloak(t, "r1", 0)
	store := newStore(t)
	old := Tokens{AccessToken: "old", RefreshToken: "r1", ExpiresAt: time.Now().Add(30 * time.Second)}
	if err := store.Save(old); err != nil {
		t.Fatal(err)
	}
	src := NewSource(old, store, kc.client())
	changed := make(chan Tokens, 1)
	src.OnChange = func(t Tokens) { changed <- t }

	got, err := src.AccessToken(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	if got == "old" || got == "" {
		t.Fatalf("token not refreshed: %q", got)
	}
	if f := kc.form(0); f.Get("refresh_token") != "r1" || f.Get("grant_type") != "refresh_token" {
		t.Errorf("refresh form = %v", f)
	}
	cur := src.Tokens()
	if cur.AccessToken != got || cur.RefreshToken != kc.latest() || cur.RefreshToken == "r1" {
		t.Errorf("source tokens = %+v", cur)
	}
	saved, ok, err := store.Load()
	if err != nil || !ok || saved.RefreshToken != kc.latest() || saved.AccessToken != got {
		t.Errorf("rotated token not persisted: %+v ok=%v err=%v", saved, ok, err)
	}
	select {
	case ct := <-changed:
		if ct.AccessToken != got {
			t.Errorf("OnChange got %q", ct.AccessToken)
		}
	case <-time.After(2 * time.Second):
		t.Error("OnChange not called")
	}
	if c := src.Claims(); c.Sub != "user-1" || c.UserEmail() != "student@edu.hse.ru" {
		t.Errorf("claims = %+v", c)
	}

	// The fresh token is reused.
	again, err := src.AccessToken(t.Context(), false)
	if err != nil || again != got || kc.hits.Load() != 1 {
		t.Errorf("second call: %q %v hits=%d", again, err, kc.hits.Load())
	}
}

func TestSourceConcurrentCallsRefreshOnce(t *testing.T) {
	kc := newRotatingKeycloak(t, "r1", 100*time.Millisecond)
	store := newStore(t)
	src := NewSource(Tokens{AccessToken: "expired", RefreshToken: "r1", ExpiresAt: time.Now().Add(-time.Minute)}, store, kc.client())

	const n = 32
	var wg sync.WaitGroup
	results := make([]string, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = src.AccessToken(context.Background(), false)
		}(i)
	}
	wg.Wait()
	if h := kc.hits.Load(); h != 1 {
		t.Fatalf("token endpoint hit %d times, want exactly 1", h)
	}
	for i := range results {
		if errs[i] != nil || results[i] != results[0] || results[i] == "expired" {
			t.Errorf("caller %d: %q %v", i, results[i], errs[i])
		}
	}
	if saved, _, _ := store.Load(); saved.RefreshToken != kc.latest() {
		t.Errorf("persisted refresh token %q, server expects %q", saved.RefreshToken, kc.latest())
	}
}

func TestSourceForcedRefreshesKeepRotationConsistent(t *testing.T) {
	// Several requests hitting 401 at once each force a refresh; they must
	// run one after another, each with the latest rotated token.
	kc := newRotatingKeycloak(t, "r1", 10*time.Millisecond)
	src := NewSource(Tokens{AccessToken: "a", RefreshToken: "r1", ExpiresAt: time.Now().Add(time.Hour)}, newStore(t), kc.client())
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := src.AccessToken(context.Background(), true); err != nil {
				t.Errorf("forced refresh failed: %v", err)
			}
		}()
	}
	wg.Wait()
	if src.Tokens().RefreshToken != kc.latest() {
		t.Errorf("source holds %q, server expects %q", src.Tokens().RefreshToken, kc.latest())
	}
}

func TestSourceForceAlwaysRefreshes(t *testing.T) {
	kc := newRotatingKeycloak(t, "r1", 0)
	src := NewSource(Tokens{AccessToken: "valid", RefreshToken: "r1", ExpiresAt: time.Now().Add(time.Hour)}, newStore(t), kc.client())
	got, err := src.AccessToken(t.Context(), true)
	if err != nil || got == "valid" || kc.hits.Load() != 1 {
		t.Errorf("force: %q %v hits=%d", got, err, kc.hits.Load())
	}
	if _, err := src.AccessToken(t.Context(), true); err != nil || kc.hits.Load() != 2 {
		t.Errorf("second force: %v hits=%d", err, kc.hits.Load())
	}
}

func TestSourceInvalidGrantClearsSession(t *testing.T) {
	kc := newKeycloak(t, func(w http.ResponseWriter, form url.Values, n int) {
		writeOAuthError(w, 400, "invalid_grant", "Session not active")
	})
	store := newStore(t)
	tok := Tokens{AccessToken: "old", RefreshToken: "r1", ExpiresAt: time.Now().Add(-time.Minute)}
	if err := store.Save(tok); err != nil {
		t.Fatal(err)
	}
	src := NewSource(tok, store, kc.client())
	_, err := src.AccessToken(t.Context(), false)
	if !errors.Is(err, api.ErrLoginRequired) {
		t.Fatalf("err = %v, want ErrLoginRequired", err)
	}
	if src.Tokens() != (Tokens{}) {
		t.Errorf("tokens not cleared: %+v", src.Tokens())
	}
	if _, ok, _ := store.Load(); ok {
		t.Error("token file not deleted")
	}
	if src.SignedIn() {
		t.Error("still signed in")
	}
	// Later calls fail fast without contacting Keycloak.
	if _, err := src.AccessToken(t.Context(), true); !errors.Is(err, api.ErrLoginRequired) || kc.hits.Load() != 1 {
		t.Errorf("after logout: %v hits=%d", err, kc.hits.Load())
	}
}

func TestSourceTransientFailure(t *testing.T) {
	kc := newKeycloak(t, func(w http.ResponseWriter, form url.Values, n int) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	store := newStore(t)
	almost := Tokens{AccessToken: "still-valid", RefreshToken: "r1", ExpiresAt: time.Now().Add(30 * time.Second)}
	if err := store.Save(almost); err != nil {
		t.Fatal(err)
	}
	src := NewSource(almost, store, kc.client())

	got, err := src.AccessToken(t.Context(), false)
	if err != nil || got != "still-valid" {
		t.Errorf("transient failure with a valid token: %q %v", got, err)
	}
	// force means the server rejected that token: don't hand it out again.
	if _, err := src.AccessToken(t.Context(), true); err == nil || api.IsNetwork(err) == false {
		t.Errorf("forced refresh during outage: %v", err)
	}
	if src.Tokens().RefreshToken != "r1" {
		t.Error("transient failure must keep the session")
	}
	if _, ok, _ := store.Load(); !ok {
		t.Error("transient failure must keep the token file")
	}

	expired := NewSource(Tokens{AccessToken: "dead", RefreshToken: "r1", ExpiresAt: time.Now().Add(-time.Second)}, newStore(t), kc.client())
	_, err = expired.AccessToken(t.Context(), false)
	var ne *api.NetworkError
	if !errors.As(err, &ne) || errors.Is(err, api.ErrLoginRequired) {
		t.Errorf("expired token during outage: %v", err)
	}
	if !expired.SignedIn() {
		t.Error("an outage must not sign the user out")
	}
}

func TestSourceWithoutRefreshToken(t *testing.T) {
	kc := newKeycloak(t, func(w http.ResponseWriter, form url.Values, n int) {
		t.Error("token endpoint must not be called")
	})
	src := NewSource(Tokens{AccessToken: "short", ExpiresAt: time.Now().Add(30 * time.Second)}, nil, kc.client())
	if got, err := src.AccessToken(t.Context(), false); err != nil || got != "short" {
		t.Errorf("valid token without refresh token: %q %v", got, err)
	}
	if _, err := src.AccessToken(t.Context(), true); !errors.Is(err, api.ErrLoginRequired) {
		t.Errorf("forced without refresh token: %v", err)
	}
	dead := NewSource(Tokens{AccessToken: "dead", ExpiresAt: time.Now().Add(-time.Second)}, nil, kc.client())
	if _, err := dead.AccessToken(t.Context(), false); !errors.Is(err, api.ErrLoginRequired) {
		t.Errorf("expired without refresh token: %v", err)
	}
	if dead.SignedIn() {
		t.Error("SignedIn with nothing usable")
	}
	refreshExpired := NewSource(Tokens{AccessToken: "dead", RefreshToken: "r", ExpiresAt: time.Now().Add(-time.Second), RefreshExpiresAt: time.Now().Add(-time.Second)}, nil, kc.client())
	if _, err := refreshExpired.AccessToken(t.Context(), false); !errors.Is(err, api.ErrLoginRequired) {
		t.Errorf("expired refresh token: %v", err)
	}
	if NewSource(Tokens{}, nil, nil).SignedIn() {
		t.Error("empty source signed in")
	}
}

func TestSourceSetAndClear(t *testing.T) {
	store := newStore(t)
	src := NewSource(Tokens{}, store, nil)
	var seen []Tokens
	src.OnChange = func(t Tokens) { seen = append(seen, t) }
	tok := Tokens{AccessToken: accessJWT(t, "u2", time.Now().Add(time.Hour)), RefreshToken: "r", ExpiresAt: time.Now().Add(time.Hour)}
	if err := src.Set(tok); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || seen[0].RefreshToken != "r" {
		t.Errorf("OnChange after Set: %+v", seen)
	}
	if saved, ok, _ := store.Load(); !ok || saved.RefreshToken != "r" {
		t.Error("Set did not persist")
	}
	if !src.SignedIn() || src.Claims().Sub != "u2" {
		t.Error("after Set")
	}
	if err := src.Clear(); err != nil {
		t.Fatal(err)
	}
	if src.SignedIn() || src.Tokens() != (Tokens{}) {
		t.Error("after Clear")
	}
	if _, ok, _ := store.Load(); ok {
		t.Error("Clear did not delete the file")
	}
}

// A caller that gives up mid-refresh must not lose the rotated token: Keycloak
// has already invalidated the old one.
func TestSourceRefreshSurvivesCallerCancellation(t *testing.T) {
	kc := newRotatingKeycloak(t, "r1", 300*time.Millisecond)
	store := newStore(t)
	src := NewSource(Tokens{AccessToken: "expired", RefreshToken: "r1", ExpiresAt: time.Now().Add(-time.Minute)}, store, kc.client())

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	_, _ = src.AccessToken(ctx, false)

	if src.Tokens().RefreshToken != kc.latest() {
		t.Fatalf("source kept %q but Keycloak rotated to %q: the session is lost", src.Tokens().RefreshToken, kc.latest())
	}
	if saved, _, _ := store.Load(); saved.RefreshToken != kc.latest() {
		t.Errorf("persisted %q, want %q", saved.RefreshToken, kc.latest())
	}
	if _, err := src.AccessToken(t.Context(), true); err != nil {
		t.Errorf("next refresh failed: %v", err)
	}
}
