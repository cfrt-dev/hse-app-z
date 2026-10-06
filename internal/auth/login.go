package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// LoginSession is one interactive sign-in: a loopback HTTP server waiting for
// Keycloak to redirect back with an authorization code.
type LoginSession struct {
	// AuthURL is the page the user must open in a browser.
	AuthURL     string
	RedirectURI string

	client   *Client
	state    string
	verifier string
	servers  []*http.Server
	result   chan loginResult
	once     sync.Once
	closeMu  sync.Once
	// claimed is set by the first valid callback: the code is redeemed once.
	claimed atomic.Bool
}

type loginResult struct {
	tokens Tokens
	err    error
}

// ErrPortInUse means the fixed callback port is taken (another instance, or
// the Python helper script, is probably running).
var ErrPortInUse = errors.New("callback port is already in use")

// StartLogin binds the callback listener and prepares the authorization URL.
// port 0 means DefaultPort. The redirect URI must match the one registered
// for the client, so the port can't be chosen freely.
func StartLogin(client *Client, port int) (*LoginSession, error) {
	if port == 0 {
		port = DefaultPort
	}
	if client == nil {
		client = &Client{}
	}
	s := &LoginSession{
		client:      client,
		RedirectURI: fmt.Sprintf("http://localhost:%d%s", port, CallbackPath),
		result:      make(chan loginResult, 1),
	}
	var err error
	if s.state, err = randomString(24); err != nil {
		return nil, err
	}
	if s.verifier, err = randomString(48); err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(s.verifier))
	q := url.Values{
		"client_id":             {client.clientID()},
		"response_type":         {"code"},
		"redirect_uri":          {s.RedirectURI},
		"state":                 {s.state},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
	}
	s.AuthURL = AuthEndpoint + "?" + q.Encode()

	mux := http.NewServeMux()
	mux.HandleFunc(CallbackPath, s.handleCallback)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })

	// "localhost" may resolve to either loopback address depending on the
	// browser, so listen on both (IPv6 is optional).
	var listeners []net.Listener
	for _, host := range []string{"127.0.0.1", "::1"} {
		ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
		if err != nil {
			// IPv6 loopback may simply be unavailable; that's fine. But if
			// another process holds it, a browser resolving localhost to ::1
			// would deliver our code to that process — refuse.
			if host == "127.0.0.1" || isAddrInUse(err) {
				for _, l := range listeners {
					l.Close()
				}
				if isAddrInUse(err) {
					return nil, fmt.Errorf("%w (port %d)", ErrPortInUse, port)
				}
				return nil, err
			}
			continue
		}
		listeners = append(listeners, ln)
	}
	for _, ln := range listeners {
		srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
		s.servers = append(s.servers, srv)
		go func(ln net.Listener) { _ = srv.Serve(ln) }(ln)
	}
	return s, nil
}

func isAddrInUse(err error) bool {
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	// Windows reports WSAEADDRINUSE (10048), which isn't syscall.EADDRINUSE.
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "address already in use") || strings.Contains(msg, "only one usage of each socket address")
}

func (s *LoginSession) handleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(s.state)) != 1 {
			// Not a response to our request: don't let it cancel the sign-in.
			writePage(w, http.StatusBadRequest, "Sign-in link expired", "This page is from an old sign-in attempt. Return to the terminal.")
			return
		}
		desc := q.Get("error_description")
		s.finish(Tokens{}, &OAuthError{Code: e, Description: desc})
		writePage(w, http.StatusBadRequest, "Sign-in failed", e+": "+desc)
		return
	}
	code := q.Get("code")
	if code == "" {
		http.NotFound(w, r)
		return
	}
	if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(s.state)) != 1 {
		// A stale tab from an earlier attempt, or a forged request.
		writePage(w, http.StatusBadRequest, "Sign-in link expired", "This sign-in link is from an old attempt. Return to the terminal and try again.")
		return
	}
	if !s.claimed.CompareAndSwap(false, true) {
		// A reload of the redirect (or a duplicate request). Redeeming the
		// code again would make Keycloak revoke the session issued for it.
		writePage(w, http.StatusOK, "Sign-in already handled", "You can close this tab and return to the terminal.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	t, err := s.client.Exchange(ctx, code, s.RedirectURI, s.verifier)
	if err != nil {
		s.finish(Tokens{}, err)
		writePage(w, http.StatusBadGateway, "Sign-in failed", err.Error())
		return
	}
	s.finish(t, nil)
	writePage(w, http.StatusOK, "Signed in", "You're signed in to HSE App Z. You can close this tab and return to the terminal.")
}

func (s *LoginSession) finish(t Tokens, err error) {
	s.once.Do(func() { s.result <- loginResult{tokens: t, err: err} })
}

// Wait blocks until the browser redirect completes, ctx ends, or Close.
func (s *LoginSession) Wait(ctx context.Context) (Tokens, error) {
	select {
	case r := <-s.result:
		return r.tokens, r.err
	case <-ctx.Done():
		return Tokens{}, ctx.Err()
	}
}

// Close stops the callback servers. Safe to call more than once.
func (s *LoginSession) Close() {
	s.closeMu.Do(func() {
		s.finish(Tokens{}, context.Canceled)
		for _, srv := range s.servers {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = srv.Shutdown(ctx)
			cancel()
		}
	})
}

func randomString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func writePage(w http.ResponseWriter, status int, title, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>%s</title>
<body style="font-family:system-ui,sans-serif;max-width:32rem;margin:4rem auto;padding:0 1rem">
<h2>%s</h2><p>%s</p></body>`, html.EscapeString(title), html.EscapeString(title), html.EscapeString(msg))
}
