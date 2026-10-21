package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"hse-app-z/internal/auth"
	"hse-app-z/internal/kitty"
	"hse-app-z/internal/ui"
	"hse-app-z/internal/ui/avatar"
)

// These tests never start the TUI, open a browser or reach the network:
// every case returns before runTUI / the browser flow, and token requests go
// to a local httptest server.

type cli struct {
	t    *testing.T
	home string
}

func newCLI(t *testing.T) *cli {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HSE_APP_Z_HOME", home)
	// Any token request that isn't redirected by a test must fail loudly
	// instead of reaching saml.hse.ru.
	old := newAuthClient
	newAuthClient = func() *auth.Client {
		return &auth.Client{TokenEndpoint: "http://127.0.0.1:1/unreachable", HTTP: &http.Client{Timeout: time.Second}}
	}
	oldStart, oldProgram := startLogin, runProgram
	startLogin = func(*auth.Client, int) (*auth.LoginSession, error) {
		t.Error("the browser sign-in was started")
		return nil, errors.New("browser sign-in is disabled in tests")
	}
	runProgram = func(tea.Model) error {
		t.Error("the TUI was started")
		return errors.New("the TUI is disabled in tests")
	}
	ui.DryRun = true
	t.Cleanup(func() { newAuthClient, startLogin, runProgram = old, oldStart, oldProgram })
	return &cli{t: t, home: home}
}

// run runs the CLI and returns what it printed to stdout (errors are
// printed by main, so run never writes to stderr).
func (c *cli) run(args ...string) (string, error) {
	c.t.Helper()
	var out bytes.Buffer
	err := run(args, &out)
	return out.String(), err
}

func (c *cli) tokensFile() string { return filepath.Join(c.home, "tokens.json") }

func (c *cli) saveTokens(t auth.Tokens) {
	c.t.Helper()
	if err := (&auth.Store{Path: c.tokensFile()}).Save(t); err != nil {
		c.t.Fatal(err)
	}
}

// jwt builds an unsigned token with the given claims (the app never
// verifies signatures locally).
func jwt(claims map[string]any) string {
	b, _ := json.Marshal(claims)
	enc := base64.RawURLEncoding
	return enc.EncodeToString([]byte(`{"alg":"none"}`)) + "." + enc.EncodeToString(b) + ".sig"
}

func validTokens() auth.Tokens {
	exp := time.Now().Add(2 * time.Hour)
	return auth.Tokens{
		AccessToken:      jwt(map[string]any{"exp": exp.Unix(), "email": "Student@Edu.HSE.ru", "name": "Иван Петров", "sub": "u1"}),
		RefreshToken:     "refresh-1",
		ExpiresAt:        exp,
		RefreshExpiresAt: time.Now().Add(24 * time.Hour),
	}
}

func usageCode(err error) bool {
	var ue usageError
	return errors.As(err, &ue)
}

func TestHelp(t *testing.T) {
	c := newCLI(t)
	for _, args := range [][]string{{"--help"}, {"-h"}, {"help"}, {"login", "--help"}, {"help", "login"}} {
		out, err := c.run(args...)
		if err != nil {
			t.Errorf("%v: err %v", args, err)
		}
		if !strings.Contains(out, "Usage:") {
			t.Errorf("%v: usage must go to stdout; got %q", args, out)
		}
	}
}

func TestUsageErrors(t *testing.T) {
	c := newCLI(t)
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"frobnicate"}, `unknown command "frobnicate"`},
		{[]string{"--nope"}, "-nope"},
		{[]string{"--lang", "de"}, "--lang must be en or ru"},
		{[]string{"--lang"}, "lang"},
		{[]string{"--port", "0"}, "--port"},
		{[]string{"--port", "abc"}, "port"},
		{[]string{"whoami", "extra"}, `unexpected argument "extra"`},
		{[]string{"logout", "now"}, `unexpected argument "now"`},
		// Without "login" the token would be silently ignored and the
		// app would start.
		{[]string{"--refresh-token", "abc"}, "--refresh-token"},
		{[]string{"token", "--refresh-token", "abc"}, "--refresh-token"},
		{[]string{"--fixtures", "x"}, "--fixtures"},
		{[]string{"whoami", "--demo"}, "--demo"},
		{[]string{"login", "--refresh-token", "  "}, "empty"},
		// An empty command (e.g. an unset "$CMD") must not start the app.
		{[]string{""}, "empty command"},
		{[]string{"--lang", "en", ""}, "empty command"},
	}
	for _, tc := range cases {
		out, err := c.run(tc.args...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: err %v, want it to mention %q", tc.args, err, tc.want)
			continue
		}
		if !usageCode(err) {
			t.Errorf("%v: %v should be a usage error (exit 2)", tc.args, err)
		}
		// The error is printed once by main; run itself must not dump
		// the whole usage on top of it.
		if strings.Contains(out, "Usage:") {
			t.Errorf("%v: usage dumped alongside the error:\n%s", tc.args, out)
		}
	}
}

func TestLangIsCaseInsensitive(t *testing.T) {
	c := newCLI(t)
	// Valid --lang, then a demo with a missing fixtures dir: fails before
	// the TUI starts, and only because of the fixtures.
	_, err := c.run("--lang", "RU", "--demo", "--fixtures", filepath.Join(c.home, "missing"))
	if err == nil || strings.Contains(err.Error(), "--lang") {
		t.Fatalf("err %v", err)
	}
}

func TestDemoMissingFixtures(t *testing.T) {
	c := newCLI(t)
	missing := filepath.Join(c.home, "nope")
	_, err := c.run("--demo", "--fixtures", missing)
	if err == nil || !strings.Contains(err.Error(), "demo data not found") || !strings.Contains(err.Error(), missing) {
		t.Fatalf("err %v", err)
	}
	if usageCode(err) {
		t.Errorf("a missing directory is not a usage error")
	}
	// A file instead of a directory.
	f := filepath.Join(c.home, "file")
	_ = os.WriteFile(f, []byte("x"), 0o600)
	if _, err := c.run("--demo", "--fixtures", f); err == nil {
		t.Fatal("a file passed as --fixtures was accepted")
	}
}

func TestSignedOut(t *testing.T) {
	c := newCLI(t)
	for _, cmd := range []string{"token", "whoami"} {
		out, err := c.run(cmd)
		if err == nil || !strings.Contains(err.Error(), "not signed in") || !strings.Contains(err.Error(), "hse-app-z login") {
			t.Errorf("%s: err %v", cmd, err)
		}
		if out != "" {
			t.Errorf("%s printed %q to stdout", cmd, out)
		}
	}
	// A corrupt tokens file counts as signed out (no panic, no JSON error).
	if err := os.WriteFile(c.tokensFile(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{"token", "whoami"} {
		if _, err := c.run(cmd); err == nil || !strings.Contains(err.Error(), "not signed in") {
			t.Errorf("%s with corrupt tokens: %v", cmd, err)
		}
	}
}

func TestLogout(t *testing.T) {
	c := newCLI(t)
	// Nothing saved: succeeds, and says so.
	out, err := c.run("logout")
	if err != nil || !strings.Contains(out, "Not signed in") {
		t.Fatalf("logout when signed out: %q %v", out, err)
	}
	c.saveTokens(validTokens())
	cache := filepath.Join(c.home, "cache", "http")
	_ = os.MkdirAll(cache, 0o700)
	_ = os.WriteFile(filepath.Join(cache, "entry"), []byte("x"), 0o600)
	out, err = c.run("logout")
	if err != nil || !strings.Contains(out, "Signed out") {
		t.Fatalf("logout: %q %v", out, err)
	}
	if _, err := os.Stat(c.tokensFile()); !os.IsNotExist(err) {
		t.Errorf("tokens file still there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cache, "entry")); !os.IsNotExist(err) {
		t.Errorf("cache not cleared: %v", err)
	}
}

func TestTokenAndWhoami(t *testing.T) {
	c := newCLI(t)
	tok := validTokens()
	c.saveTokens(tok)
	out, err := c.run("token")
	if err != nil || strings.TrimSpace(out) != tok.AccessToken {
		t.Fatalf("token: %q %v", out, err)
	}
	out, err = c.run("whoami")
	if err != nil || !strings.Contains(out, "Иван Петров <student@edu.hse.ru>") || !strings.Contains(out, "access token expires") {
		t.Fatalf("whoami: %q %v", out, err)
	}
}

func TestWhoamiExpiredSession(t *testing.T) {
	c := newCLI(t)
	past := time.Now().Add(-time.Hour)
	c.saveTokens(auth.Tokens{
		AccessToken:      jwt(map[string]any{"exp": past.Unix(), "email": "a@edu.hse.ru"}),
		RefreshToken:     "r",
		ExpiresAt:        past,
		RefreshExpiresAt: past,
	})
	out, err := c.run("whoami")
	if err == nil || !strings.Contains(err.Error(), "expired") || !strings.Contains(err.Error(), "hse-app-z login") {
		t.Fatalf("whoami with a dead session: out %q err %v", out, err)
	}
	if _, err := c.run("token"); err == nil || !strings.Contains(err.Error(), "hse-app-z login") {
		t.Fatalf("token with a dead session: %v", err)
	}
}

func TestWhoamiOpaqueToken(t *testing.T) {
	c := newCLI(t)
	c.saveTokens(auth.Tokens{AccessToken: "opaque", RefreshToken: "r", ExpiresAt: time.Now().Add(time.Hour)})
	out, err := c.run("whoami")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "<>") || strings.HasPrefix(out, " ") {
		t.Errorf("whoami printed an empty identity: %q", out)
	}
}

// tokenServer is a fake Keycloak token endpoint.
func tokenServer(t *testing.T, c *cli, handler http.HandlerFunc) *atomic.Int32 {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	newAuthClient = func() *auth.Client { return &auth.Client{TokenEndpoint: srv.URL, HTTP: srv.Client()} }
	return &hits
}

func TestLoginRefreshTokenInvalid(t *testing.T) {
	c := newCLI(t)
	hits := tokenServer(t, c, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"Invalid refresh token"}`))
	})
	_, err := c.run("login", "--refresh-token", "bogus")
	if err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("err %v", err)
	}
	if hits.Load() != 1 {
		t.Errorf("token endpoint hit %d times", hits.Load())
	}
	if _, err := os.Stat(c.tokensFile()); !os.IsNotExist(err) {
		t.Errorf("a rejected token must not be saved")
	}
}

func TestLoginRefreshTokenFlagPositions(t *testing.T) {
	for _, args := range [][]string{
		{"login", "--refresh-token", "good"},
		{"login", "--refresh-token=good", "--port", "9000"},
		// Flags before the subcommand, and more after it: everything after
		// "login" used to be ignored, so this opened the browser instead.
		{"--port", "9000", "login", "--refresh-token", "good"},
	} {
		c := newCLI(t)
		var got string
		access := jwt(map[string]any{"exp": time.Now().Add(time.Hour).Unix(), "email": "x@edu.hse.ru", "name": "X Y"})
		tokenServer(t, c, func(w http.ResponseWriter, r *http.Request) {
			_ = r.ParseForm()
			got = r.PostForm.Get("refresh_token")
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": access, "refresh_token": "rotated", "expires_in": 3600})
		})
		out, err := c.run(args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if got != "good" || !strings.Contains(out, "Signed in as X Y <x@edu.hse.ru>") {
			t.Errorf("%v: sent %q, out %q", args, got, out)
		}
		saved, ok, _ := (&auth.Store{Path: c.tokensFile()}).Load()
		if !ok || saved.RefreshToken != "rotated" {
			t.Errorf("%v: saved %+v", args, saved)
		}
	}
}

func TestImagesFlag(t *testing.T) {
	c := newCLI(t)
	for _, args := range [][]string{{"--images", "maybe"}, {"whoami", "--images", "on"}} {
		if _, err := c.run(args...); !usageCode(err) {
			t.Errorf("%v: want a usage error, got %v", args, err)
		}
	}
	t.Cleanup(func() {
		avatar.Default = &avatar.Registry{}
		ui.SetLang("en")
	})
	var enabled bool
	var lang string
	runProgram = func(tea.Model) error {
		enabled, lang = avatar.Default.Enabled(), ui.Lang()
		return nil
	}
	if _, err := c.run("--demo", "--fixtures", "internal/api/testdata", "--images", "on", "--lang", "ru"); err != nil {
		t.Fatal(err)
	}
	if !enabled || lang != "ru" {
		t.Errorf("images=%v lang=%q; want images on and Russian interface", enabled, lang)
	}
	avatar.Default = &avatar.Registry{}
	t.Setenv("HSE_APP_Z_IMAGES", "garbage") // a bad env value must not stop the app
	t.Setenv("TERM", "dumb")
	t.Setenv("TMUX", "") // tests may run inside a real tmux
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("GHOSTTY_RESOURCES_DIR", "")
	if _, err := c.run("--demo", "--fixtures", "internal/api/testdata"); err != nil {
		t.Fatal(err)
	}
	if enabled {
		t.Error("auto mode on a dumb terminal must keep images off")
	}
}

func TestDoctor(t *testing.T) {
	c := newCLI(t)
	old := kitty.TmuxQuery
	kitty.TmuxQuery = func(...string) (string, error) { return "xterm-ghostty\tghostty 1.1.3\toff", nil }
	t.Cleanup(func() { kitty.TmuxQuery = old })
	t.Setenv("TMUX", "/tmp/tmux-1/default,1,0")
	out, err := c.run("doctor")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"config", c.home, "not signed in", "language   en", "images     off (--images auto)", "allow-passthrough on"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor output lacks %q:\n%s", want, out)
		}
	}
	c.saveTokens(validTokens())
	if out, _ := c.run("doctor", "--images", "on"); !strings.Contains(out, "images     on (--images on)") || !strings.Contains(out, "token valid until") {
		t.Errorf("doctor with tokens and forced images:\n%s", out)
	}
}

func TestQueryCommandsDemo(t *testing.T) {
	c := newCLI(t)
	fx := filepath.Join("internal", "api", "testdata")
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"schedule", "--demo", "--fixtures", fx, "--week"}, "Probability Theory"},
		{[]string{"--demo", "--fixtures", fx, "schedule", "--from", "2026-10-12"}, "Mon 12 Oct 2026"},
		{[]string{"grades", "--demo", "--fixtures", fx, "--year", "2025/2026"}, "Algebra and Geometry"},
		{[]string{"rating", "--demo", "--fixtures", fx, "--type", "current"}, "GPA"},
		{[]string{"search", "ivan", "--demo", "--fixtures", fx}, "Reznichenko"},
		{[]string{"person", "--demo", "--fixtures", fx}, "makvalnikitin@edu.hse.ru"},
		{[]string{"cafes", "--demo", "--fixtures", fx}, "has menu"},
		{[]string{"menu", "64ed04c9411dc0b2e4890e42", "--demo", "--fixtures", fx}, "₽"},
		{[]string{"news", "--demo", "--fixtures", fx}, "https://"},
	}
	for _, tc := range cases {
		out, err := c.run(tc.args...)
		if err != nil || !strings.Contains(out, tc.want) {
			t.Errorf("%v: err %v, want %q in:\n%s", tc.args, err, tc.want, out)
		}
	}
}

func TestQueryJSON(t *testing.T) {
	c := newCLI(t)
	fx := filepath.Join("internal", "api", "testdata")
	out, err := c.run("schedule", "--demo", "--fixtures", fx, "--week", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var lessons []map[string]any
	if err := json.Unmarshal([]byte(out), &lessons); err != nil || len(lessons) == 0 {
		t.Fatalf("schedule --json: %v, %d lessons:\n%s", err, len(lessons), out)
	}
	// An empty day is [] rather than null.
	out, err = c.run("schedule", "--demo", "--fixtures", fx, "--from", "2020-01-01", "--json")
	if err != nil || strings.TrimSpace(out) != "[]" {
		t.Fatalf("empty schedule: err %v, %q", err, out)
	}
	out, err = c.run("grades", "--demo", "--fixtures", fx, "--json")
	var g map[string]any
	if err != nil || json.Unmarshal([]byte(out), &g) != nil || g["items"] == nil {
		t.Fatalf("grades --json: err %v:\n%s", err, out)
	}
}

func TestQueryUsageErrors(t *testing.T) {
	c := newCLI(t)
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"grades", "extra"}, `unexpected argument "extra"`},
		{[]string{"search"}, "needs an argument"},
		{[]string{"menu"}, "needs an argument"},
		{[]string{"schedule", "--group", "1", "--room", "2"}, "only one of"},
		{[]string{"schedule", "--days", "0"}, "--days"},
		{[]string{"schedule", "--week", "--days", "3"}, "--week"},
		{[]string{"schedule", "--nope"}, "-nope"},
		{[]string{"--port", "9000", "grades"}, "--port"},
		{[]string{"grades", "--lang", "de"}, "--lang"},
		{[]string{"grades", "--fixtures", "x"}, "--fixtures needs --demo"},
	}
	for _, tc := range cases {
		_, err := c.run(tc.args...)
		if err == nil || !strings.Contains(err.Error(), tc.want) || !usageCode(err) {
			t.Errorf("%v: err %v, want a usage error mentioning %q", tc.args, err, tc.want)
		}
	}
	// A bad date is caught before anything is requested.
	if _, err := c.run("schedule", "--demo", "--from", "next week"); err == nil || !usageCode(err) {
		t.Errorf("bad --from: %v", err)
	}
}

func TestQuerySignedOut(t *testing.T) {
	c := newCLI(t)
	out, err := c.run("grades")
	if err == nil || !strings.Contains(err.Error(), "hse-app-z login") || out != "" {
		t.Fatalf("err %v, out %q", err, out)
	}
}
