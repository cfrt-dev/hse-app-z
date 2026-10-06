package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"hse-app-z/internal/api"
)

// freePort returns a loopback port that was free a moment ago.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

func startSession(t *testing.T, kc *keycloak) (*LoginSession, int) {
	t.Helper()
	var c *Client
	if kc != nil {
		c = kc.client()
	}
	for attempt := 0; ; attempt++ {
		port := freePort(t)
		s, err := StartLogin(c, port)
		if errors.Is(err, ErrPortInUse) && attempt < 5 {
			continue // raced with another process for the port
		}
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(s.Close)
		return s, port
	}
}

func authParams(t *testing.T, s *LoginSession) url.Values {
	t.Helper()
	u, err := url.Parse(s.AuthURL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query()
}

func callback(t *testing.T, port int, q url.Values) (int, string) {
	t.Helper()
	u := fmt.Sprintf("http://127.0.0.1:%d%s?%s", port, CallbackPath, q.Encode())
	resp, err := http.Get(u)
	if err != nil {
		t.Fatalf("GET callback: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func waitShort(s *LoginSession, d time.Duration) (Tokens, error) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	return s.Wait(ctx)
}

func TestLoginAuthURL(t *testing.T) {
	s, port := startSession(t, nil)
	u, err := url.Parse(s.AuthURL)
	if err != nil {
		t.Fatal(err)
	}
	if base := u.Scheme + "://" + u.Host + u.Path; base != AuthEndpoint {
		t.Errorf("auth endpoint = %s", base)
	}
	q := u.Query()
	wantRedirect := fmt.Sprintf("http://localhost:%d/callback", port)
	if s.RedirectURI != wantRedirect || q.Get("redirect_uri") != wantRedirect {
		t.Errorf("redirect_uri = %q / %q, want %q", s.RedirectURI, q.Get("redirect_uri"), wantRedirect)
	}
	if q.Get("client_id") != "app-x-android" || q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" {
		t.Errorf("auth params = %v", q)
	}
	b64url := regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	if st := q.Get("state"); len(st) < 22 || !b64url.MatchString(st) {
		t.Errorf("state %q is too weak", st)
	}
	if ch := q.Get("code_challenge"); len(ch) != 43 || !b64url.MatchString(ch) {
		t.Errorf("code_challenge %q is not an unpadded base64url SHA-256", ch)
	}

	other, _ := startSession(t, nil)
	oq := authParams(t, other)
	if oq.Get("state") == q.Get("state") || oq.Get("code_challenge") == q.Get("code_challenge") {
		t.Error("state/PKCE reused across sessions")
	}
}

func TestLoginSuccess(t *testing.T) {
	access := accessJWT(t, "user-1", time.Now().Add(3*time.Hour))
	kc := newKeycloak(t, func(w http.ResponseWriter, form url.Values, n int) {
		writeTokens(w, access, "refresh-1", 10800, 1800)
	})
	s, port := startSession(t, kc)
	q := authParams(t, s)

	status, body := callback(t, port, url.Values{"code": {"auth-code"}, "state": {q.Get("state")}, "session_state": {"x"}})
	if status != http.StatusOK || !strings.Contains(body, "Signed in") {
		t.Errorf("callback page: %d %s", status, body)
	}
	tok, err := waitShort(s, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != access || tok.RefreshToken != "refresh-1" || tok.ExpiresAt.IsZero() {
		t.Errorf("tokens = %+v", tok)
	}

	form := kc.form(0)
	if form.Get("grant_type") != "authorization_code" || form.Get("code") != "auth-code" ||
		form.Get("client_id") != ClientID || form.Get("redirect_uri") != s.RedirectURI {
		t.Errorf("exchange form = %v", form)
	}
	verifier := form.Get("code_verifier")
	if len(verifier) < 43 || len(verifier) > 128 || !regexp.MustCompile(`^[A-Za-z0-9._~-]+$`).MatchString(verifier) {
		t.Errorf("code_verifier %q violates RFC 7636", verifier)
	}
	sum := sha256.Sum256([]byte(verifier))
	if got := base64.RawURLEncoding.EncodeToString(sum[:]); got != q.Get("code_challenge") {
		t.Errorf("S256(code_verifier) = %s, but the auth URL had code_challenge %s", got, q.Get("code_challenge"))
	}
	if ua := kc.req(0).Header.Get("User-Agent"); ua != UserAgent {
		t.Errorf("User-Agent = %q", ua)
	}

	// Reloading the tab must not redeem the code again (Keycloak treats code
	// reuse as an attack and revokes the session issued for it).
	status, body = callback(t, port, url.Values{"code": {"auth-code"}, "state": {q.Get("state")}})
	if status != http.StatusOK || strings.Contains(body, "failed") {
		t.Errorf("second callback page: %d %s", status, body)
	}
	if kc.hits.Load() != 1 {
		t.Errorf("code exchanged %d times", kc.hits.Load())
	}
}

func TestLoginConcurrentCallbacksExchangeOnce(t *testing.T) {
	kc := newKeycloak(t, func(w http.ResponseWriter, form url.Values, n int) {
		time.Sleep(50 * time.Millisecond)
		writeTokens(w, accessJWT(t, "u", time.Now().Add(time.Hour)), "r", 3600, 0)
	})
	s, port := startSession(t, kc)
	q := url.Values{"code": {"c"}, "state": {authParams(t, s).Get("state")}}
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			callback(t, port, q)
		}()
	}
	wg.Wait()
	if _, err := waitShort(s, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if kc.hits.Load() != 1 {
		t.Errorf("code exchanged %d times, want 1", kc.hits.Load())
	}
}

func TestLoginWrongStateIgnored(t *testing.T) {
	kc := newKeycloak(t, func(w http.ResponseWriter, form url.Values, n int) {
		writeTokens(w, accessJWT(t, "u", time.Now().Add(time.Hour)), "r", 3600, 0)
	})
	s, port := startSession(t, kc)

	status, body := callback(t, port, url.Values{"code": {"stolen"}, "state": {"forged"}})
	if status != http.StatusBadRequest || !strings.Contains(body, "expired") {
		t.Errorf("wrong state page: %d %s", status, body)
	}
	status, _ = callback(t, port, url.Values{"code": {"x"}}) // no state at all
	if status != http.StatusBadRequest {
		t.Errorf("missing state: %d", status)
	}
	status, _ = callback(t, port, url.Values{"state": {authParams(t, s).Get("state")}}) // no code
	if status != http.StatusNotFound {
		t.Errorf("missing code: %d", status)
	}
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/favicon.ico", port))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("other path: %d", resp.StatusCode)
	}

	if _, err := waitShort(s, 150*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("session completed after a forged callback: %v", err)
	}
	if kc.hits.Load() != 0 {
		t.Errorf("forged code was exchanged")
	}

	// The real redirect still works afterwards.
	callback(t, port, url.Values{"code": {"real"}, "state": {authParams(t, s).Get("state")}})
	if _, err := waitShort(s, 5*time.Second); err != nil {
		t.Errorf("real callback after a forged one: %v", err)
	}
	if kc.form(0).Get("code") != "real" {
		t.Errorf("exchanged code %q", kc.form(0).Get("code"))
	}
}

func TestLoginAccessDenied(t *testing.T) {
	s, port := startSession(t, nil)
	status, body := callback(t, port, url.Values{
		"error":             {"access_denied"},
		"error_description": {"User <cancelled> login"},
		"state":             {authParams(t, s).Get("state")},
	})
	if status != http.StatusBadRequest || !strings.Contains(body, "access_denied") || strings.Contains(body, "<cancelled>") {
		t.Errorf("error page (must be HTML-escaped): %d %s", status, body)
	}
	_, err := waitShort(s, 5*time.Second)
	var oe *OAuthError
	if !errors.As(err, &oe) || oe.Code != "access_denied" || oe.Description != "User <cancelled> login" {
		t.Errorf("Wait err = %v", err)
	}
	if IsLoginRequired(err) {
		t.Error("access_denied is not a stale-session error")
	}
}

func TestLoginExchangeFailure(t *testing.T) {
	kc := newKeycloak(t, func(w http.ResponseWriter, form url.Values, n int) {
		writeOAuthError(w, 400, "invalid_grant", "Code not valid")
	})
	s, port := startSession(t, kc)
	status, body := callback(t, port, url.Values{"code": {"old"}, "state": {authParams(t, s).Get("state")}})
	if status != http.StatusBadGateway || !strings.Contains(body, "Code not valid") {
		t.Errorf("failure page: %d %s", status, body)
	}
	_, err := waitShort(s, 5*time.Second)
	if !errors.Is(err, api.ErrLoginRequired) {
		t.Errorf("Wait err = %v", err)
	}
}

func TestLoginPortInUse(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	s, err := StartLogin(nil, port)
	if err == nil {
		s.Close()
		t.Fatal("StartLogin succeeded on a busy port")
	}
	if !errors.Is(err, ErrPortInUse) || !strings.Contains(err.Error(), fmt.Sprint(port)) {
		t.Errorf("err = %v, want ErrPortInUse naming the port", err)
	}
}

func TestLoginCloseAndWait(t *testing.T) {
	s, port := startSession(t, nil)

	// Wait honours its own context.
	if _, err := waitShort(s, 20*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Wait before Close: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := s.Wait(context.Background())
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	s.Close()
	s.Close() // idempotent
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Wait after Close = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Wait did not return after Close")
	}

	// The port is released.
	s2, err := StartLogin(nil, port)
	if err != nil {
		t.Fatalf("port not released after Close: %v", err)
	}
	s2.Close()

	// A callback after Close can't complete anything.
	if _, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/callback?code=x", port)); err == nil {
		t.Error("callback server still listening after Close")
	}
}

func TestLoginCloseAfterSuccessKeepsResult(t *testing.T) {
	kc := newKeycloak(t, func(w http.ResponseWriter, form url.Values, n int) {
		writeTokens(w, accessJWT(t, "u", time.Now().Add(time.Hour)), "r", 3600, 0)
	})
	s, port := startSession(t, kc)
	callback(t, port, url.Values{"code": {"c"}, "state": {authParams(t, s).Get("state")}})
	s.Close()
	tok, err := waitShort(s, time.Second)
	if err != nil || tok.RefreshToken != "r" {
		t.Errorf("result lost by Close: %+v %v", tok, err)
	}
}

func TestLoginErrorCallbackNeedsState(t *testing.T) {
	port := freePort(t)
	s, err := StartLogin(&Client{TokenEndpoint: "http://127.0.0.1:1/token"}, port)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/callback?error=access_denied&state=forged", port))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := s.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("forged error callback must not finish the session, got %v", err)
	}
}
