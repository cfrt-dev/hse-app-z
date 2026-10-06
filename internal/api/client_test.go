package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeTokens is a scripted TokenProvider that records the force flags.
type fakeTokens struct {
	mu    sync.Mutex
	calls []bool
	fn    func(force bool) (string, error)
}

func (f *fakeTokens) AccessToken(_ context.Context, force bool) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, force)
	f.mu.Unlock()
	return f.fn(force)
}

func (f *fakeTokens) Calls() []bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]bool(nil), f.calls...)
}

func newTestServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func newTestClient(base string, tokens TokenProvider) *Client {
	c := NewClient(base, nil, NewCache(""))
	if tokens != nil {
		c.Tokens = tokens
	}
	return c
}

const storiesBody = `[{"id":"s1","title":"One","publisher":{"name":"HSE"},"pages":[]}]`

func TestClientSendsStandardHeaders(t *testing.T) {
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != "okhttp/4.12.0" {
			t.Errorf("User-Agent = %q", got)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization sent without a token provider: %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "" {
			t.Errorf("Content-Type on a GET: %q", got)
		}
		fmt.Fprint(w, storiesBody)
	})
	c := newTestClient(srv.URL+"/", nil) // trailing slash is trimmed
	if c.BaseURL != srv.URL {
		t.Errorf("BaseURL = %q", c.BaseURL)
	}
	st, _, err := c.Stories(t.Context())
	mustOK(t, err)
	if len(st) != 1 || st[0].Title != "One" {
		t.Errorf("stories = %+v", st)
	}
	if NewClient("", nil, nil).BaseURL != DefaultBaseURL {
		t.Error("empty base should select DefaultBaseURL")
	}
}

func TestClientETagAndNotModified(t *testing.T) {
	var hits atomic.Int32
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		inm := r.Header.Get("If-None-Match")
		if n == 1 {
			if inm != "" {
				t.Errorf("first request sent If-None-Match %q", inm)
			}
			w.Header().Set("ETag", `W/"v1"`)
			fmt.Fprint(w, storiesBody)
			return
		}
		if inm != `W/"v1"` {
			t.Errorf("request %d If-None-Match = %q, want W/\"v1\"", n, inm)
		}
		w.WriteHeader(http.StatusNotModified)
	})
	dir := t.TempDir()
	c := NewClient(srv.URL, nil, NewCache(dir))

	st, meta, err := c.Stories(t.Context())
	mustOK(t, err)
	if meta.FromCache || meta.Stale || len(st) != 1 {
		t.Fatalf("first: meta=%+v stories=%+v", meta, st)
	}
	before := time.Now()
	st, meta, err = c.Stories(t.Context())
	mustOK(t, err)
	if !meta.FromCache || meta.Stale || meta.StaleReason != "" || meta.Fetched.Before(before) {
		t.Errorf("304: meta=%+v", meta)
	}
	if len(st) != 1 || st[0].ID != "s1" {
		t.Errorf("304 body from cache = %+v", st)
	}

	// The cache survives a restart: a new client on the same dir revalidates.
	c2 := NewClient(srv.URL, nil, NewCache(dir))
	st, meta, err = c2.Stories(t.Context())
	mustOK(t, err)
	if !meta.FromCache || len(st) != 1 {
		t.Errorf("after restart: meta=%+v stories=%+v", meta, st)
	}
	if hits.Load() != 3 {
		t.Errorf("server hits = %d", hits.Load())
	}

	// Cache files are private.
	entries, err := os.ReadDir(dir)
	mustOK(t, err)
	if len(entries) != 1 {
		t.Fatalf("cache dir has %d entries", len(entries))
	}
	info, err := entries[0].Info()
	mustOK(t, err)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("cache file mode %v", info.Mode().Perm())
	}
}

func TestClient304WithoutCacheIsAnError(t *testing.T) {
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	})
	_, _, err := newTestClient(srv.URL, nil).Stories(t.Context())
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != 304 {
		t.Errorf("err = %v", err)
	}
}

func TestClientUnauthorizedRefreshesOnceAndRetries(t *testing.T) {
	var hits atomic.Int32
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.Header.Get("Authorization") {
		case "Bearer fresh":
			fmt.Fprint(w, storiesBody)
		default:
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":{"name":"TOKEN_INVALID","message":"Bearer token is invalid."},"trace_id":"t"}`)
		}
	})
	tok := &fakeTokens{fn: func(force bool) (string, error) {
		if force {
			return "fresh", nil
		}
		return "stale", nil
	}}
	st, _, err := newTestClient(srv.URL, tok).Stories(t.Context())
	mustOK(t, err)
	if len(st) != 1 {
		t.Errorf("stories = %+v", st)
	}
	if got := tok.Calls(); len(got) != 2 || got[0] || !got[1] {
		t.Errorf("token calls (force flags) = %v, want [false true]", got)
	}
	if hits.Load() != 2 {
		t.Errorf("server hits = %d, want 2", hits.Load())
	}
}

func TestClientSecondUnauthorizedIsLoginRequired(t *testing.T) {
	var hits atomic.Int32
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"name":"TOKEN_INVALID","message":"Bearer token is invalid."}}`)
	})
	tok := &fakeTokens{fn: func(bool) (string, error) { return "tok", nil }}
	c := newTestClient(srv.URL, tok)
	_, _, err := c.Stories(t.Context())
	if !errors.Is(err, ErrLoginRequired) {
		t.Fatalf("err = %v, want ErrLoginRequired", err)
	}
	if !strings.Contains(err.Error(), "Bearer token is invalid") {
		t.Errorf("error should carry the server message: %v", err)
	}
	if Friendly(err) != "Session expired — please sign in again" {
		t.Errorf("Friendly = %q", Friendly(err))
	}
	if hits.Load() != 2 {
		t.Errorf("server hits = %d, want exactly 2 (one retry)", hits.Load())
	}
	if got := tok.Calls(); len(got) != 2 || !got[1] {
		t.Errorf("token calls = %v", got)
	}

	// No token provider: no retry, straight to login required.
	hits.Store(0)
	_, _, err = newTestClient(srv.URL, nil).Stories(t.Context())
	if !errors.Is(err, ErrLoginRequired) || hits.Load() != 1 {
		t.Errorf("without provider: err=%v hits=%d", err, hits.Load())
	}
}

func TestClientTokenProviderErrors(t *testing.T) {
	var hits atomic.Int32
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, storiesBody)
	})

	// Login required: no request, no stale fallback.
	login := &fakeTokens{fn: func(bool) (string, error) {
		return "", fmt.Errorf("%w: no valid session", ErrLoginRequired)
	}}
	c := newTestClient(srv.URL, &fakeTokens{fn: func(bool) (string, error) { return "ok", nil }})
	_, _, err := c.Stories(t.Context()) // fill the cache
	mustOK(t, err)
	c.Tokens = login
	hits.Store(0)
	_, meta, err := c.Stories(t.Context())
	if !errors.Is(err, ErrLoginRequired) || meta.Stale || hits.Load() != 0 {
		t.Errorf("login required: err=%v meta=%+v hits=%d", err, meta, hits.Load())
	}

	// Token endpoint unreachable: offline. Falls back to the cache...
	offline := &fakeTokens{fn: func(bool) (string, error) {
		return "", &NetworkError{Err: errors.New("dial tcp: connection refused")}
	}}
	c.Tokens = offline
	st, meta, err := c.Stories(t.Context())
	mustOK(t, err)
	if !meta.Stale || !meta.FromCache || meta.StaleReason != "offline" || len(st) != 1 {
		t.Errorf("offline token with cache: meta=%+v", meta)
	}
	// ...and without a cache reports a (single-wrapped) network error.
	c2 := newTestClient(srv.URL, offline)
	_, _, err = c2.Stories(t.Context())
	var ne *NetworkError
	if !errors.As(err, &ne) || !IsNetwork(err) {
		t.Fatalf("err = %v, want NetworkError", err)
	}
	if strings.Count(err.Error(), "network error") != 1 {
		t.Errorf("network error wrapped twice: %q", err.Error())
	}
	// Non-network provider errors (e.g. a WAF page from saml.hse.ru) are
	// passed through unchanged, not mislabelled as "offline".
	c3 := newTestClient(srv.URL, &fakeTokens{fn: func(bool) (string, error) { return "", errors.New("boom") }})
	_, _, err = c3.Stories(t.Context())
	if errors.As(err, &ne) || err.Error() != "boom" {
		t.Errorf("err = %v", err)
	}
}

func TestClientRateLimit(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
		want    time.Duration
		msg     string
	}{
		{"x-ratelimit-reset", map[string]string{"X-Ratelimit-Reset": "8"}, 8 * time.Second, "too many requests, try again in 8s"},
		{"retry-after wins", map[string]string{"Retry-After": "3", "X-Ratelimit-Reset": "60"}, 3 * time.Second, "too many requests, try again in 3s"},
		{"none", nil, 0, "too many requests, try again shortly"},
		{"garbage", map[string]string{"Retry-After": "soon"}, 0, "too many requests, try again shortly"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				for k, v := range tc.headers {
					w.Header().Set(k, v)
				}
				w.WriteHeader(http.StatusTooManyRequests)
				fmt.Fprint(w, `{"error":{"name":"TooManyRequests"}}`)
			})
			_, _, err := newTestClient(srv.URL, nil).Stories(t.Context())
			var re *RateLimitError
			if !errors.As(err, &re) {
				t.Fatalf("err = %v, want *RateLimitError", err)
			}
			if re.RetryAfter != tc.want || re.Error() != tc.msg {
				t.Errorf("RetryAfter=%v Error()=%q", re.RetryAfter, re.Error())
			}
			if Friendly(err) != "Rate limited: "+tc.msg {
				t.Errorf("Friendly = %q", Friendly(err))
			}
		})
	}
}

func TestRetryAfterParsing(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", time.Now().Add(90*time.Second).UTC().Format(http.TimeFormat))
	if d := retryAfter(h); d < 85*time.Second || d > 91*time.Second {
		t.Errorf("HTTP-date Retry-After = %v", d)
	}
	h = http.Header{}
	h.Set("X-Ratelimit-Reset", fmt.Sprint(time.Now().Add(30*time.Second).Unix()))
	if d := retryAfter(h); d < 25*time.Second || d > 31*time.Second {
		t.Errorf("epoch X-Ratelimit-Reset = %v", d)
	}
	h = http.Header{}
	h.Set("Retry-After", "-5")
	if d := retryAfter(h); d != 0 {
		t.Errorf("negative Retry-After = %v", d)
	}
}

// statusSequence serves a 200 with an ETag first, then the given status.
func statusSequence(t *testing.T, then int, extra http.Header) (*httptest.Server, *atomic.Int32) {
	var hits atomic.Int32
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.Header().Set("ETag", `"e1"`)
			fmt.Fprint(w, storiesBody)
			return
		}
		for k, v := range extra {
			w.Header()[k] = v
		}
		w.WriteHeader(then)
		fmt.Fprint(w, `{"error":{"name":"X","message":"nope"}}`)
	})
	return srv, &hits
}

func TestClientStaleFallbacks(t *testing.T) {
	cases := []struct {
		status int
		reason string
	}{
		{http.StatusTooManyRequests, "rate limited"},
		{http.StatusInternalServerError, "server error"},
		{http.StatusBadGateway, "server error"},
		{http.StatusServiceUnavailable, "server error"},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			srv, _ := statusSequence(t, tc.status, http.Header{"X-Ratelimit-Reset": {"8"}})
			c := newTestClient(srv.URL, nil)
			_, first, err := c.Stories(t.Context())
			mustOK(t, err)
			st, meta, err := c.Stories(t.Context())
			if err != nil {
				t.Fatalf("expected stale data, got %v", err)
			}
			if !meta.Stale || !meta.FromCache || meta.StaleReason != tc.reason {
				t.Errorf("meta = %+v", meta)
			}
			if meta.Fetched.IsZero() || meta.Fetched.After(first.Fetched.Add(time.Second)) {
				t.Errorf("stale Fetched %v should be the original fetch time %v", meta.Fetched, first.Fetched)
			}
			if len(st) != 1 || st[0].ID != "s1" {
				t.Errorf("stale body = %+v", st)
			}
		})
	}
}

func TestClientServerErrorWithoutCache(t *testing.T) {
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, "<html><body>502 Bad Gateway</body></html>")
	})
	_, meta, err := newTestClient(srv.URL, nil).Stories(t.Context())
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != 502 || ae.Message != "" || meta.Stale {
		t.Fatalf("err=%v (%+v) meta=%+v", err, ae, meta)
	}
	if got := Friendly(err); got != "HSE server error (502) — try again later" {
		t.Errorf("Friendly = %q", got)
	}
	if ae.Error() != "HTTP 502" {
		t.Errorf("Error() = %q", ae.Error())
	}
}

func TestClientNetworkFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, storiesBody)
	}))
	base := srv.URL
	cached := newTestClient(base, nil)
	_, _, err := cached.Stories(t.Context())
	mustOK(t, err)
	srv.Close()

	// No cache: NetworkError.
	_, _, err = newTestClient(base, nil).Stories(t.Context())
	var ne *NetworkError
	if !errors.As(err, &ne) || !IsNetwork(err) {
		t.Fatalf("err = %v, want *NetworkError", err)
	}
	if got := Friendly(err); got != "Can't reach HSE servers — check your connection" {
		t.Errorf("Friendly = %q", got)
	}

	// Cached: stale data.
	st, meta, err := cached.Stories(t.Context())
	mustOK(t, err)
	if !meta.Stale || !meta.FromCache || meta.StaleReason != "offline" || len(st) != 1 {
		t.Errorf("meta=%+v stories=%+v", meta, st)
	}
}

func TestClientContextCancellation(t *testing.T) {
	release := make(chan struct{})
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
		fmt.Fprint(w, storiesBody)
	})
	defer close(release)
	c := newTestClient(srv.URL, nil)
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	_, meta, err := c.Stories(ctx)
	if !errors.Is(err, context.DeadlineExceeded) || meta.Stale {
		t.Errorf("err=%v meta=%+v", err, meta)
	}
	if got := Friendly(err); got != "Request timed out" {
		t.Errorf("Friendly(deadline) = %q", got)
	}
}

func TestClientAPIErrorEnvelope(t *testing.T) {
	notFound, err := os.ReadFile("testdata/error_404.json")
	mustOK(t, err)
	cases := []struct {
		name, body          string
		status              int
		wantName, wantMsg   string
		wantTrace, friendly string
	}{
		{"404 envelope", string(notFound), 404, "NotFound", "users.get: User not found", "8f6d1f84-ea73-4a59-a41c-1fac0c86fd22", "Not found (users.get: User not found)"},
		{"404 bare", ``, 404, "", "", "", "Not found"},
		{"400 string error", `{"error":"bad day","trace_id":"x1"}`, 400, "", "bad day", "x1", "HTTP 400: bad day"},
		{"400 top-level message", `{"message":"invalid program"}`, 400, "", "invalid program", "", "HTTP 400: invalid program"},
		{"400 name only", `{"error":{"name":"ValidationError"}}`, 400, "ValidationError", "", "", "HTTP 400: ValidationError"},
		{"403 plain text", `Forbidden by WAF`, 403, "", "Forbidden by WAF", "", "HTTP 403: Forbidden by WAF"},
		{"403 html", `<html>blocked</html>`, 403, "", "", "", "HTTP 403"},
		// A JSON body without a message is shown raw (useful for bug reports).
		{"400 null error", `{"error":null}`, 400, "", `{"error":null}`, "", `HTTP 400: {"error":null}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			})
			_, _, err := newTestClient(srv.URL, nil).Person(t.Context(), "nobody@hse.ru")
			var ae *APIError
			if !errors.As(err, &ae) {
				t.Fatalf("err = %v, want *APIError", err)
			}
			if ae.Status != tc.status || ae.Name != tc.wantName || ae.Message != tc.wantMsg || ae.TraceID != tc.wantTrace {
				t.Errorf("APIError = %+v", ae)
			}
			if ae.NotFound() != (tc.status == 404) {
				t.Errorf("NotFound() = %v", ae.NotFound())
			}
			if got := Friendly(err); got != tc.friendly {
				t.Errorf("Friendly = %q, want %q", got, tc.friendly)
			}
		})
	}

	// Long plain-text bodies are truncated.
	long := strings.Repeat("x", 500)
	e := parseAPIError(400, []byte(long))
	if len([]rune(e.Message)) != 201 || !strings.HasSuffix(e.Message, "…") {
		t.Errorf("long body message has %d runes", len([]rune(e.Message)))
	}
}

func TestClientDecodeErrorOn2xx(t *testing.T) {
	for _, body := range []string{"<html>maintenance</html>", "", "   ", `{"items": 5}`} {
		var hits atomic.Int32
		srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			if r.Header.Get("If-None-Match") != "" {
				t.Errorf("an undecodable body must not be cached (got If-None-Match)")
			}
			w.Header().Set("ETag", `"bad"`)
			fmt.Fprint(w, body)
		})
		c := newTestClient(srv.URL, nil)
		_, _, err := c.Stories(t.Context())
		var de *DecodeError
		if !errors.As(err, &de) || de.Path != "/v2/stories" || de.Unwrap() == nil {
			t.Fatalf("body %q: err = %v, want *DecodeError", body, err)
		}
		if got := Friendly(err); got != "Unexpected response format from /v2/stories" {
			t.Errorf("Friendly = %q", got)
		}
		_, _, _ = c.Stories(t.Context())
		if hits.Load() != 2 {
			t.Errorf("hits = %d", hits.Load())
		}
	}
}

func TestClientAcceptLanguage(t *testing.T) {
	var mu sync.Mutex
	var langs, inms []string
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		langs = append(langs, r.Header.Get("Accept-Language"))
		inms = append(inms, r.Header.Get("If-None-Match"))
		mu.Unlock()
		if r.Header.Get("If-None-Match") != "" {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"`+r.Header.Get("Accept-Language")[:2]+`"`)
		fmt.Fprint(w, storiesBody)
	})
	c := newTestClient(srv.URL, nil)
	if c.Lang() != "en" {
		t.Errorf("default lang %q", c.Lang())
	}
	_, _, err := c.Stories(t.Context())
	mustOK(t, err)
	c.SetLang("ru")
	if c.Lang() != "ru" {
		t.Errorf("lang %q", c.Lang())
	}
	_, meta, err := c.Stories(t.Context())
	mustOK(t, err)
	if meta.FromCache {
		t.Error("Russian response served from the English cache entry")
	}
	c.SetLang("de") // unsupported → en
	if c.Lang() != "en" {
		t.Errorf("SetLang(de) → %q", c.Lang())
	}
	_, meta, err = c.Stories(t.Context())
	mustOK(t, err)
	if !meta.FromCache {
		t.Error("English entry should be revalidated")
	}
	want := []string{"en-RU;q=1.0, ru-RU;q=0.9", "ru-RU;q=1.0, en-RU;q=0.9", "en-RU;q=1.0, ru-RU;q=0.9"}
	wantINM := []string{"", "", `"en"`}
	for i := range want {
		if langs[i] != want[i] || inms[i] != wantINM[i] {
			t.Errorf("request %d: Accept-Language=%q If-None-Match=%q", i, langs[i], inms[i])
		}
	}
}

func TestClientCacheScope(t *testing.T) {
	var fail atomic.Bool
	var mu sync.Mutex
	var inms []string
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		inms = append(inms, r.Header.Get("If-None-Match"))
		mu.Unlock()
		if fail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if r.Header.Get("If-None-Match") != "" {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"x"`)
		fmt.Fprint(w, storiesBody)
	})
	c := newTestClient(srv.URL, nil)
	c.SetCacheScope("alice")
	_, _, err := c.Stories(t.Context())
	mustOK(t, err)
	c.SetCacheScope("bob")
	_, meta, err := c.Stories(t.Context())
	mustOK(t, err)
	if meta.FromCache {
		t.Error("bob was served alice's cache entry")
	}

	// Bob's stale fallback must not show alice's data either.
	c.SetCacheScope("carol")
	fail.Store(true)
	_, meta, err = c.Stories(t.Context())
	if err == nil || meta.Stale {
		t.Errorf("carol got stale data from another scope: meta=%+v err=%v", meta, err)
	}
	c.SetCacheScope("alice")
	_, meta, err = c.Stories(t.Context())
	mustOK(t, err)
	if !meta.Stale {
		t.Errorf("alice should get her own stale entry: %+v", meta)
	}
	if inms[0] != "" || inms[1] != "" || inms[2] != "" || inms[3] != `"x"` {
		t.Errorf("If-None-Match sequence %q", inms)
	}
}

func TestClientBuildingsPOST(t *testing.T) {
	fixture, err := os.ReadFile("testdata/buildings.json")
	mustOK(t, err)
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v3/dump/buildings/groups" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q", ct)
		}
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(b, &body); err != nil || body["is_free"] != true || len(body) != 1 {
			t.Errorf("body = %s", b)
		}
		w.WriteHeader(http.StatusCreated)
		w.Write(fixture)
	})
	groups, _, err := newTestClient(srv.URL, nil).Buildings(t.Context())
	mustOK(t, err)
	if len(groups) != 4 {
		t.Errorf("groups = %d", len(groups))
	}
}

func TestClientRetryResendsPOSTBody(t *testing.T) {
	var hits atomic.Int32
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		b, _ := io.ReadAll(r.Body)
		if string(b) != `{"is_free":true}` {
			t.Errorf("attempt %d body = %q", hits.Load(), b)
		}
		if r.Header.Get("Authorization") != "Bearer forced" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `[]`)
	})
	tok := &fakeTokens{fn: func(force bool) (string, error) {
		if force {
			return "forced", nil
		}
		return "old", nil
	}}
	if _, _, err := newTestClient(srv.URL, tok).Buildings(t.Context()); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 2 {
		t.Errorf("hits = %d", hits.Load())
	}
}

func TestClientQueryParams(t *testing.T) {
	var mu sync.Mutex
	var last *url.URL
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		u := *r.URL
		last = &u
		mu.Unlock()
		switch {
		case strings.HasPrefix(r.URL.Path, "/education/"), r.URL.Path == "/tasks",
			strings.HasSuffix(r.URL.Path, "/menu"), strings.HasSuffix(r.URL.Path, "/link"):
			fmt.Fprint(w, `{}`)
		case strings.HasPrefix(r.URL.Path, "/v3/dump/email/") && !strings.HasSuffix(r.URL.Path, "/subordinates"):
			fmt.Fprint(w, `{}`)
		case strings.HasPrefix(r.URL.Path, "/food/cafes/"):
			fmt.Fprint(w, `{}`)
		default:
			fmt.Fprint(w, `[]`)
		}
	})
	c := newTestClient(srv.URL, nil)
	ctx := t.Context()
	d := func(y int, m time.Month, day int) time.Time { return time.Date(y, m, day, 15, 0, 0, 0, Moscow) }

	cases := []struct {
		name  string
		call  func() error
		path  string
		query url.Values
	}{
		{"lessons email", func() error {
			_, _, err := c.Lessons(ctx, LessonQuery{Email: "a@hse.ru", Group: "ignored", Start: d(2026, 10, 5), End: d(2026, 10, 11)})
			return err
		}, "/v3/ruz/lessons", url.Values{"email": {"a@hse.ru"}, "start": {"2026-10-05"}, "end": {"2026-10-11"}}},
		{"lessons group", func() error {
			_, _, err := c.Lessons(ctx, LessonQuery{Group: "ruz47990", Start: d(2026, 10, 5)})
			return err
		}, "/v3/ruz/lessons", url.Values{"group": {"ruz47990"}, "start": {"2026-10-05"}}},
		{"lessons auditorium", func() error {
			_, _, err := c.Lessons(ctx, LessonQuery{Auditorium: "ruz1033", End: d(2026, 10, 11)})
			return err
		}, "/v3/ruz/lessons", url.Values{"auditorium": {"ruz1033"}, "end": {"2026-10-11"}}},
		{"lessons own", func() error {
			_, _, err := c.Lessons(ctx, LessonQuery{})
			return err
		}, "/v3/ruz/lessons", url.Values{}},
		{"search", func() error {
			_, _, err := c.Search(ctx, "Иванов & co")
			return err
		}, "/v3/dump/search", url.Values{"q": {"Иванов & co"}}},
		{"ratings all", func() error {
			_, _, err := c.Ratings(ctx, RatingQuery{AcademicYear: "2025/2026", ProgramID: "1306560", Type: "current", ModuleNumber: 4})
			return err
		}, "/education/ratings", url.Values{"academic_year": {"2025/2026"}, "program_id": {"1306560"}, "type": {"current"}, "module_number": {"4"}}},
		{"ratings none", func() error {
			_, _, err := c.Ratings(ctx, RatingQuery{})
			return err
		}, "/education/ratings", url.Values{}},
		{"grades", func() error {
			_, _, err := c.Grades(ctx, "2025/2026", "000275111")
			return err
		}, "/education/grades", url.Values{"academic_year": {"2025/2026"}, "program_id": {"000275111"}}},
		{"grades default", func() error {
			_, _, err := c.Grades(ctx, "", "")
			return err
		}, "/education/grades", url.Values{}},
		{"menu day", func() error {
			_, _, err := c.CafeMenu(ctx, "abc", "Monday")
			return err
		}, "/food/cafes/abc/menu", url.Values{"day": {"monday"}}},
		{"menu today", func() error {
			_, _, err := c.CafeMenu(ctx, "abc", "")
			return err
		}, "/food/cafes/abc/menu", url.Values{}},
		{"services root", func() error {
			_, _, err := c.Services(ctx, "")
			return err
		}, "/tasks/services", url.Values{}},
		{"services category", func() error {
			_, _, err := c.Services(ctx, "/ELK_YA-STUDENT_DOOR/ELK_Skidki&Budget_DOOR")
			return err
		}, "/tasks/services", url.Values{"category": {"/ELK_YA-STUDENT_DOOR/ELK_Skidki&Budget_DOOR"}}},
		{"tasks first", func() error {
			_, _, err := c.Tasks(ctx, "")
			return err
		}, "/tasks", url.Values{}},
		{"tasks cursor 0", func() error {
			_, _, err := c.Tasks(ctx, "0")
			return err
		}, "/tasks", url.Values{}},
		{"tasks cursor", func() error {
			_, _, err := c.Tasks(ctx, "20")
			return err
		}, "/tasks", url.Values{"cursor": {"20"}}},
		{"banners", func() error {
			_, _, err := c.Banners(ctx, "ratings")
			return err
		}, "/banners", url.Values{"section": {"ratings"}}},
		{"banners all", func() error {
			_, _, err := c.Banners(ctx, "")
			return err
		}, "/banners", url.Values{}},
		{"favourites", func() error {
			_, _, err := c.Favourites(ctx)
			return err
		}, "/v3/dump/favourites/me", url.Values{}},
		{"cafes", func() error {
			_, _, err := c.Cafes(ctx)
			return err
		}, "/food/cafes", url.Values{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); err != nil {
				t.Fatalf("call failed: %v", err)
			}
			mu.Lock()
			defer mu.Unlock()
			if last.Path != tc.path {
				t.Errorf("path = %q, want %q", last.Path, tc.path)
			}
			if got := last.Query(); got.Encode() != tc.query.Encode() {
				t.Errorf("query = %q, want %q", got.Encode(), tc.query.Encode())
			}
		})
	}
}

func TestClientEmailPathEscaping(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.EscapedPath())
		mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/subordinates") {
			fmt.Fprint(w, `[]`)
			return
		}
		fmt.Fprint(w, `{"url":"  https://www.hse.ru/staff/x/  "}`)
	})
	c := newTestClient(srv.URL, nil)
	ctx := t.Context()
	_, _, err := c.Person(ctx, " First.Last+tag@hse.ru ")
	mustOK(t, err)
	_, _, err = c.Person(ctx, "a/b?c#d e@hse.ru")
	mustOK(t, err)
	link, _, err := c.PersonLink(ctx, "x@hse.ru")
	mustOK(t, err)
	if link != "https://www.hse.ru/staff/x/" {
		t.Errorf("link not trimmed: %q", link)
	}
	_, _, err = c.Subordinates(ctx, "boss@hse.ru")
	mustOK(t, err)
	_, _, err = c.Cafe(ctx, "id/../x")
	mustOK(t, err)

	want := []string{
		"/v3/dump/email/First.Last+tag@hse.ru",
		"/v3/dump/email/a%2Fb%3Fc%23d%20e@hse.ru",
		"/v3/dump/email/x@hse.ru/link",
		"/v3/dump/email/boss@hse.ru/subordinates",
		"/food/cafes/id%2F..%2Fx",
	}
	for i, w := range want {
		if paths[i] != w {
			t.Errorf("path %d = %q, want %q", i, paths[i], w)
		}
	}
}

func TestClientLessonsFilterAndSort(t *testing.T) {
	// The server may return lessons outside the requested range, unsorted.
	body := `[
	 {"id":"after","discipline":"A","date_start":"2026-10-11T21:30:00Z","date_end":"2026-10-11T22:30:00Z","time_zone":"Europe/Moscow"},
	 {"id":"late","discipline":"B","date_start":"2026-10-11T15:00:00Z","date_end":"2026-10-11T16:00:00Z","time_zone":"Europe/Moscow"},
	 {"id":"same-b","discipline":"Beta","date_start":"2026-10-06T07:00:00Z","date_end":"2026-10-06T08:00:00Z"},
	 {"id":"same-a","discipline":"Alpha","date_start":"2026-10-06T07:00:00Z","date_end":"2026-10-06T08:00:00Z"},
	 {"id":"before","discipline":"C","date_start":"2026-10-04T20:30:00Z","date_end":"2026-10-04T20:50:00Z","time_zone":"Europe/Moscow"},
	 {"id":"first","discipline":"D","date_start":"2026-10-04T21:30:00Z","date_end":"2026-10-04T22:30:00Z","time_zone":"Europe/Moscow"},
	 {"id":"perm","discipline":"E","date_start":"2026-10-11T19:30:00Z","date_end":"2026-10-11T20:30:00Z","time_zone":"Asia/Yekaterinburg"},
	 {"id":"nodate","discipline":"F"}
	]`
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) })
	c := newTestClient(srv.URL, nil)
	q := LessonQuery{
		Email: "a@hse.ru",
		Start: time.Date(2026, 10, 5, 0, 0, 0, 0, Moscow),
		End:   time.Date(2026, 10, 11, 0, 0, 0, 0, Moscow),
	}
	ls, _, err := c.Lessons(t.Context(), q)
	mustOK(t, err)
	var ids []string
	for _, l := range ls {
		ids = append(ids, string(l.ID))
	}
	// "first" is 00:30 MSK on 5 Oct (in range); "before" is 23:30 MSK on 4 Oct;
	// "after" is 00:30 MSK on 12 Oct; "perm" is 00:30 on 12 Oct in Perm.
	want := "first,same-a,same-b,late"
	if strings.Join(ids, ",") != want {
		t.Errorf("lessons = %s, want %s", strings.Join(ids, ","), want)
	}

	// Open-ended query keeps everything with a date.
	ls, _, err = c.Lessons(t.Context(), LessonQuery{})
	mustOK(t, err)
	if len(ls) != 7 {
		t.Errorf("unfiltered lessons = %d, want 7 (only the undated one dropped)", len(ls))
	}
}

func TestClientConcurrencyLimit(t *testing.T) {
	var inFlight, maxInFlight, hits atomic.Int32
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		n := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			m := maxInFlight.Load()
			if n <= m || maxInFlight.CompareAndSwap(m, n) {
				break
			}
		}
		// Every third request needs a token refresh.
		if hits.Add(1)%3 == 0 && r.Header.Get("Authorization") != "Bearer forced" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		time.Sleep(5 * time.Millisecond)
		w.Header().Set("ETag", `"e"`)
		fmt.Fprint(w, storiesBody)
	})
	tok := &fakeTokens{fn: func(force bool) (string, error) {
		if force {
			return "forced", nil
		}
		return "normal", nil
	}}
	c := newTestClient(srv.URL, tok)

	const n = 64
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var err error
			switch i % 4 {
			case 0:
				_, _, err = c.Stories(t.Context())
			case 1:
				c.SetLang([]string{"en", "ru"}[i%2])
				_, _, err = c.Search(t.Context(), fmt.Sprint(i%5))
			case 2:
				c.SetCacheScope(fmt.Sprint(i % 3))
				_, _, err = c.Banners(t.Context(), "")
			default:
				_, _, err = c.Favourites(t.Context())
			}
			errs <- err
		}(i)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("deadlock: concurrent requests did not finish")
	}
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("request failed: %v", err)
		}
	}
	if m := maxInFlight.Load(); m > maxParallelReqs || m < 1 {
		t.Errorf("max in-flight requests = %d, limit %d", m, maxParallelReqs)
	}
}

func TestClientSemaphoreRespectsContext(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, maxParallelReqs)
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-release
		fmt.Fprint(w, storiesBody)
	})
	c := newTestClient(srv.URL, nil)
	var wg sync.WaitGroup
	for i := 0; i < maxParallelReqs; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _ = c.Stories(context.Background())
		}()
	}
	for i := 0; i < maxParallelReqs; i++ {
		<-started
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, err := c.Stories(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("queued request err = %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Error("queued request ignored its context")
	}
	close(release)
	wg.Wait()
}

// Concurrent revalidations (304 → touch) and failures (5xx → stale
// fallback reading the entry) on the same key must not race.
func TestClientConcurrentRevalidationAndFallback(t *testing.T) {
	var hits atomic.Int32
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		switch {
		case n == 1:
			w.Header().Set("ETag", `"e"`)
			fmt.Fprint(w, storiesBody)
		case n%2 == 0:
			w.WriteHeader(http.StatusNotModified)
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	})
	c := newTestClient(srv.URL, nil)
	_, _, err := c.Stories(t.Context())
	mustOK(t, err)
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st, meta, err := c.Stories(context.Background())
			if err != nil || len(st) != 1 || !meta.FromCache || meta.Fetched.IsZero() {
				t.Errorf("err=%v meta=%+v", err, meta)
			}
		}()
	}
	wg.Wait()
}

func TestCacheBasics(t *testing.T) {
	var nilCache *Cache
	nilCache.put("k", "e", []byte("x"))
	nilCache.touch("k")
	nilCache.Clear()
	if nilCache.get("k") != nil {
		t.Error("nil cache returned an entry")
	}

	dir := t.TempDir()
	c := NewCache(dir)
	c.put("k1", `"e1"`, []byte(`[1]`))
	if e := c.get("k1"); e == nil || e.ETag != `"e1"` || string(e.Body) != "[1]" {
		t.Fatalf("get = %+v", e)
	}
	// Persisted.
	if e := NewCache(dir).get("k1"); e == nil || string(e.Body) != "[1]" {
		t.Errorf("reloaded = %+v", e)
	}
	// Corrupt and empty-body files are ignored.
	mustOK(t, os.WriteFile(dir+"/k2.json", []byte("{not json"), 0o600))
	mustOK(t, os.WriteFile(dir+"/k3.json", []byte(`{"etag":"x"}`), 0o600))
	fresh := NewCache(dir)
	if fresh.get("k2") != nil || fresh.get("k3") != nil {
		t.Error("corrupt cache file returned an entry")
	}

	// touch refreshes Stored without mutating entries handed out earlier.
	e := c.get("k1")
	stored := e.Stored
	time.Sleep(2 * time.Millisecond)
	c.touch("k1")
	if !e.Stored.Equal(stored) {
		t.Error("touch mutated an entry already returned to a caller")
	}
	if e2 := c.get("k1"); !e2.Stored.After(stored) || e2.ETag != e.ETag || string(e2.Body) != string(e.Body) {
		t.Errorf("touched entry = %+v", e2)
	}
	// ...and the revalidation time survives a restart.
	if e3 := NewCache(dir).get("k1"); e3 == nil || !e3.Stored.After(stored) {
		t.Errorf("touch not persisted: %+v", e3)
	}

	c.Clear()
	if c.get("k1") != nil || NewCache(dir).get("k1") != nil {
		t.Error("Clear left entries behind")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("Clear left %d files", len(entries))
	}

	mem := NewCache("")
	mem.put("k", "", []byte("{}"))
	if mem.get("k") == nil {
		t.Error("memory cache lost the entry")
	}
	if cacheKey("a", "bc") == cacheKey("ab", "c") {
		t.Error("cache key parts are not separated")
	}
}
