package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"hse-app-z/internal/api"
)

var fixedNow = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

func TestExchange(t *testing.T) {
	exp := fixedNow.Add(3 * time.Hour)
	access := accessJWT(t, "user-1", exp)
	kc := newKeycloak(t, func(w http.ResponseWriter, form url.Values, n int) {
		writeTokens(w, access, "refresh-1", 999, 1800)
	})
	c := &Client{TokenEndpoint: kc.URL, Now: func() time.Time { return fixedNow }}
	tok, err := c.Exchange(t.Context(), "the-code", "http://localhost:8001/callback", "the-verifier")
	if err != nil {
		t.Fatal(err)
	}

	r := kc.req(0)
	if r.Method != http.MethodPost {
		t.Errorf("method %s", r.Method)
	}
	if ua := r.Header.Get("User-Agent"); ua != "okhttp/4.12.0" {
		t.Errorf("User-Agent = %q", ua)
	}
	if ct := r.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
		t.Errorf("Content-Type = %q", ct)
	}
	if a := r.Header.Get("Accept"); a != "application/json" {
		t.Errorf("Accept = %q", a)
	}
	want := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {"the-code"},
		"client_id":     {"app-x-android"},
		"redirect_uri":  {"http://localhost:8001/callback"},
		"code_verifier": {"the-verifier"},
	}
	if got := kc.form(0); got.Encode() != want.Encode() {
		t.Errorf("form = %v\nwant   %v", got, want)
	}

	if tok.AccessToken != access || tok.RefreshToken != "refresh-1" || tok.IDToken != "id-token" || tok.TokenType != "Bearer" || tok.Scope != "openid email profile" {
		t.Errorf("tokens = %+v", tok)
	}
	// exp claim wins over expires_in.
	if !tok.ExpiresAt.Equal(time.Unix(exp.Unix(), 0)) {
		t.Errorf("ExpiresAt = %v, want %v (from exp)", tok.ExpiresAt, exp)
	}
	if !tok.RefreshExpiresAt.Equal(fixedNow.Add(1800 * time.Second)) {
		t.Errorf("RefreshExpiresAt = %v", tok.RefreshExpiresAt)
	}

	// No verifier: the field is omitted. Custom client id is honoured.
	c.ClientID = "other-client"
	_, err = c.Exchange(t.Context(), "code-2", "http://localhost:9/callback", "")
	if err != nil {
		t.Fatal(err)
	}
	if f := kc.form(1); f.Has("code_verifier") || f.Get("client_id") != "other-client" {
		t.Errorf("form = %v", f)
	}
}

func TestRefresh(t *testing.T) {
	access := accessJWT(t, "u", fixedNow.Add(3*time.Hour))
	rotate := true
	kc := newKeycloak(t, func(w http.ResponseWriter, form url.Values, n int) {
		if rotate {
			writeTokens(w, access, "refresh-2", 10800, 0)
		} else {
			writeTokens(w, access, "", 10800, 0)
		}
	})
	c := &Client{TokenEndpoint: kc.URL, Now: func() time.Time { return fixedNow }}
	tok, err := c.Refresh(t.Context(), "refresh-1")
	if err != nil {
		t.Fatal(err)
	}
	want := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {"refresh-1"}, "client_id": {"app-x-android"}}
	if got := kc.form(0); got.Encode() != want.Encode() {
		t.Errorf("form = %v", got)
	}
	if ua := kc.req(0).Header.Get("User-Agent"); ua != UserAgent {
		t.Errorf("User-Agent = %q", ua)
	}
	if tok.RefreshToken != "refresh-2" {
		t.Errorf("rotated refresh token = %q", tok.RefreshToken)
	}
	// refresh_expires_in 0 (offline token / unknown): zero RefreshExpiresAt.
	if !tok.RefreshExpiresAt.IsZero() {
		t.Errorf("RefreshExpiresAt = %v, want zero", tok.RefreshExpiresAt)
	}
	if !tok.CanRefresh(fixedNow.Add(1000 * time.Hour)) {
		t.Error("a refresh token without expiry should stay refreshable")
	}

	// Keycloak may omit the refresh token: keep the old one.
	rotate = false
	tok, err = c.Refresh(t.Context(), "refresh-1")
	if err != nil {
		t.Fatal(err)
	}
	if tok.RefreshToken != "refresh-1" {
		t.Errorf("refresh token without rotation = %q, want the old one", tok.RefreshToken)
	}
}

func TestTokenExpiryFallbacks(t *testing.T) {
	cases := []struct {
		name      string
		access    string
		expiresIn int
		want      time.Time
	}{
		{"opaque token uses expires_in", "opaque-token", 600, fixedNow.Add(600 * time.Second)},
		{"jwt without exp uses expires_in", makeJWT(t, map[string]any{"sub": "x"}), 300, fixedNow.Add(300 * time.Second)},
		{"nothing known: 5 minutes", "opaque-token", 0, fixedNow.Add(5 * time.Minute)},
		{"exp claim", accessJWT(t, "x", fixedNow.Add(time.Hour)), 0, time.Unix(fixedNow.Add(time.Hour).Unix(), 0)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kc := newKeycloak(t, func(w http.ResponseWriter, form url.Values, n int) {
				writeTokens(w, tc.access, "r", tc.expiresIn, 0)
			})
			c := &Client{TokenEndpoint: kc.URL, Now: func() time.Time { return fixedNow }}
			tok, err := c.Refresh(t.Context(), "r")
			if err != nil {
				t.Fatal(err)
			}
			if !tok.ExpiresAt.Equal(tc.want) {
				t.Errorf("ExpiresAt = %v, want %v", tok.ExpiresAt, tc.want)
			}
		})
	}
}

func TestTokenEndpointErrors(t *testing.T) {
	cases := []struct {
		name        string
		status      int
		body        string
		ctype       string
		login       bool
		network     bool
		oauthCode   string
		errContains string
	}{
		{"invalid_grant", 400, `{"error":"invalid_grant","error_description":"Token is not active"}`, "application/json", true, false, "", "Token is not active"},
		{"invalid_grant on 401", 401, `{"error":"invalid_grant","error_description":"Session not active"}`, "application/json", true, false, "", "Session not active"},
		{"invalid_token", 400, `{"error":"invalid_token"}`, "application/json", true, false, "", "invalid_token"},
		{"unauthorized_client", 401, `{"error":"unauthorized_client"}`, "application/json", true, false, "", "unauthorized_client"},
		{"invalid_client", 400, `{"error":"invalid_client","error_description":"bad client"}`, "application/json", false, false, "invalid_client", "sign-in failed: invalid_client: bad client"},
		{"5xx json", 500, `{"error":"unknown_error"}`, "application/json", false, true, "unknown_error", "unknown_error"},
		{"5xx html", 502, `<html><body>Bad Gateway</body></html>`, "text/html", false, true, "", "unexpected response from saml.hse.ru"},
		{"503 empty", 503, ``, "text/plain", false, true, "", "HTTP 503"},
		{"WAF html 403", 403, `<html><title>Access denied</title></html>`, "text/html", false, false, "", "sign-in failed: HTTP 403: unexpected response from saml.hse.ru"},
		{"html with 200", 200, `<!doctype html><p>maintenance</p>`, "text/html", false, false, "", "HTTP 200: unexpected response"},
		{"200 without access token", 200, `{"token_type":"Bearer"}`, "application/json", false, false, "", "HTTP 200"},
		{"200 with error field", 200, `{"error":"temporarily_unavailable"}`, "application/json", false, false, "temporarily_unavailable", "temporarily_unavailable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kc := newKeycloak(t, func(w http.ResponseWriter, form url.Values, n int) {
				w.Header().Set("Content-Type", tc.ctype)
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			})
			_, err := kc.client().Refresh(t.Context(), "r")
			if err == nil {
				t.Fatal("expected an error")
			}
			if got := errors.Is(err, api.ErrLoginRequired); got != tc.login {
				t.Errorf("login required = %v, want %v (err %v)", got, tc.login, err)
			}
			if tc.login && (!errors.Is(err, ErrInvalidGrant) || !IsLoginRequired(err)) {
				t.Errorf("err should wrap ErrInvalidGrant: %v", err)
			}
			var ne *api.NetworkError
			if got := errors.As(err, &ne); got != tc.network {
				t.Errorf("network = %v, want %v (err %v)", got, tc.network, err)
			}
			if !tc.login {
				var oe *OAuthError
				if !errors.As(err, &oe) {
					t.Fatalf("err %v should carry *OAuthError", err)
				}
				if oe.Status != tc.status || oe.Code != tc.oauthCode {
					t.Errorf("OAuthError = %+v", oe)
				}
			}
			if !strings.Contains(err.Error(), tc.errContains) {
				t.Errorf("error %q should contain %q", err.Error(), tc.errContains)
			}
		})
	}
}

func TestTokenEndpointUnreachable(t *testing.T) {
	kc := newKeycloak(t, func(w http.ResponseWriter, form url.Values, n int) {})
	c := kc.client()
	kc.Close()
	_, err := c.Exchange(t.Context(), "code", "http://localhost:8001/callback", "v")
	var ne *api.NetworkError
	if !errors.As(err, &ne) || IsLoginRequired(err) {
		t.Errorf("err = %v, want *api.NetworkError", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := (&Client{TokenEndpoint: "http://127.0.0.1:1/token"}).Refresh(ctx, "r"); err == nil {
		t.Error("cancelled context should fail")
	}
}

func TestClientDefaults(t *testing.T) {
	var c *Client
	if c.endpoint() != TokenEndpoint || c.clientID() != ClientID || c.httpClient() == nil || c.now().IsZero() {
		t.Error("nil client defaults")
	}
	if (&Client{}).endpoint() != "https://saml.hse.ru/realms/hse/protocol/openid-connect/token" {
		t.Error("token endpoint")
	}
}

func TestParseClaims(t *testing.T) {
	// Payload lengths that need 0, 1 and 2 padding characters.
	paddings := map[int]bool{}
	for _, name := range []string{"Al", "Bob", "Carl", "Dmitry", "Eve Smith", "Fedor"} {
		payload := fmt.Sprintf(`{"exp":1760000000,"sub":"s","email":"x@hse.ru","name":%q}`, name)
		padded := base64.URLEncoding.EncodeToString([]byte(payload))
		paddings[strings.Count(padded, "=")] = true
		for _, enc := range []string{padded, strings.TrimRight(padded, "=")} {
			c, err := ParseClaims("header." + enc + ".sig")
			if err != nil {
				t.Fatalf("ParseClaims(%q): %v", enc, err)
			}
			if c.Name != name || c.Exp != 1760000000 || c.Sub != "s" {
				t.Errorf("claims = %+v", c)
			}
		}
	}
	for _, p := range []int{0, 1, 2} {
		if !paddings[p] {
			t.Errorf("test did not exercise %d padding chars", p)
		}
	}
	// Two segments are enough.
	if c, err := ParseClaims("h." + base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"x"}`))); err != nil || c.Sub != "x" {
		t.Errorf("two-part token: %+v %v", c, err)
	}

	garbage := []string{
		"",
		"not-a-jwt",
		"a.!!!.c",
		"a." + base64.RawURLEncoding.EncodeToString([]byte("not json")) + ".c",
		"a." + base64.RawURLEncoding.EncodeToString([]byte(`{"exp":"soon"}`)) + ".c",
		"a." + base64.RawURLEncoding.EncodeToString([]byte(`[1,2]`)) + ".c",
	}
	for _, g := range garbage {
		if _, err := ParseClaims(g); err == nil {
			t.Errorf("ParseClaims(%q) succeeded", g)
		}
	}
}

func TestClaimsHelpers(t *testing.T) {
	c := Claims{Email: " Student@EDU.hse.ru ", Name: " Ivan Petrov ", Exp: 1760000000}
	if c.UserEmail() != "student@edu.hse.ru" || c.DisplayName() != "Ivan Petrov" || !c.Expiry().Equal(time.Unix(1760000000, 0)) {
		t.Errorf("claims helpers: %q %q %v", c.UserEmail(), c.DisplayName(), c.Expiry())
	}
	c = Claims{PreferredUsername: "IPetrov@hse.ru", GivenName: "Ivan", FamilyName: "Petrov"}
	if c.UserEmail() != "ipetrov@hse.ru" || c.DisplayName() != "Ivan Petrov" {
		t.Errorf("fallbacks: %q %q", c.UserEmail(), c.DisplayName())
	}
	c = Claims{PreferredUsername: "ipetrov", GivenName: "Ivan"}
	if c.UserEmail() != "" || c.DisplayName() != "Ivan" {
		t.Errorf("username without @: %q %q", c.UserEmail(), c.DisplayName())
	}
	c = Claims{Email: "x@hse.ru"}
	if c.DisplayName() != "x@hse.ru" || !c.Expiry().IsZero() {
		t.Errorf("email-only: %q %v", c.DisplayName(), c.Expiry())
	}
}

func TestTokensValidity(t *testing.T) {
	now := fixedNow
	tok := Tokens{AccessToken: "a", ExpiresAt: now.Add(90 * time.Second)}
	if !tok.Valid(now, time.Minute) || tok.Valid(now, 2*time.Minute) || tok.Valid(now.Add(90*time.Second), 0) {
		t.Error("Valid margins")
	}
	if (Tokens{AccessToken: "a"}).Valid(now, 0) || (Tokens{ExpiresAt: now.Add(time.Hour)}).Valid(now, 0) {
		t.Error("Valid without expiry/token")
	}
	if (Tokens{}).CanRefresh(now) || !(Tokens{RefreshToken: "r"}).CanRefresh(now) {
		t.Error("CanRefresh basic")
	}
	if (Tokens{RefreshToken: "r", RefreshExpiresAt: now}).CanRefresh(now) || !(Tokens{RefreshToken: "r", RefreshExpiresAt: now.Add(time.Second)}).CanRefresh(now) {
		t.Error("CanRefresh expiry")
	}
	e := &OAuthError{Status: 400, Code: "access_denied", Description: "User cancelled"}
	if e.Error() != "sign-in failed: access_denied: User cancelled" {
		t.Errorf("OAuthError.Error() = %q", e.Error())
	}
	if (&OAuthError{Status: 403}).Error() != "sign-in failed: HTTP 403" {
		t.Error("OAuthError without code")
	}
}
