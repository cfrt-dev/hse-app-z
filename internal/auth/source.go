package auth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"hse-app-z/internal/api"
)

// refreshMargin: refresh this long before the access token expires so a
// request never goes out with a token that dies in flight.
const refreshMargin = 60 * time.Second

// refreshTimeout bounds a refresh, which runs to completion even if the
// caller's context ends (see AccessToken).
const refreshTimeout = 30 * time.Second

// Source is a concurrency-safe api.TokenProvider. It refreshes the access
// token when needed (one refresh at a time; concurrent callers wait and
// reuse the result) and persists rotated tokens.
type Source struct {
	mu     sync.Mutex
	tokens Tokens
	store  *Store
	client *Client
	now    func() time.Time

	// OnChange, if set, is called (outside the lock) after tokens change.
	OnChange func(Tokens)
}

// NewSource returns a source seeded with t.
func NewSource(t Tokens, store *Store, client *Client) *Source {
	if client == nil {
		client = &Client{}
	}
	return &Source{tokens: t, store: store, client: client, now: time.Now}
}

// Tokens returns a copy of the current tokens.
func (s *Source) Tokens() Tokens {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tokens
}

// Claims parses the current access token.
func (s *Source) Claims() Claims {
	c, _ := ParseClaims(s.Tokens().AccessToken)
	return c
}

// SignedIn reports whether there is anything to authenticate with.
func (s *Source) SignedIn() bool {
	t := s.Tokens()
	return t.Valid(s.now(), 0) || t.CanRefresh(s.now())
}

// Set replaces the session (after an interactive login) and persists it.
func (s *Source) Set(t Tokens) error {
	s.mu.Lock()
	s.tokens = t
	s.mu.Unlock()
	err := s.store.Save(t)
	if s.OnChange != nil {
		s.OnChange(t)
	}
	return err
}

// Clear forgets the session and deletes it from disk.
func (s *Source) Clear() error {
	s.mu.Lock()
	s.tokens = Tokens{}
	s.mu.Unlock()
	return s.store.Delete()
}

// AccessToken implements api.TokenProvider.
func (s *Source) AccessToken(ctx context.Context, force bool) (string, error) {
	s.mu.Lock()
	t := s.tokens
	now := s.now()
	if !force && t.Valid(now, refreshMargin) {
		s.mu.Unlock()
		return t.AccessToken, nil
	}
	if !t.CanRefresh(now) {
		s.mu.Unlock()
		if !force && t.Valid(now, 0) {
			return t.AccessToken, nil
		}
		return "", fmt.Errorf("%w: no valid session", api.ErrLoginRequired)
	}
	// Keep the lock during the refresh: Keycloak rotates refresh tokens, so
	// two parallel refreshes with the same token would log the user out.
	// For the same reason the refresh must not be abandoned when the caller
	// gives up: Keycloak may already have rotated the token we hold.
	defer s.mu.Unlock()
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), refreshTimeout)
	defer cancel()
	nt, err := s.client.Refresh(rctx, t.RefreshToken)
	if err != nil {
		if errors.Is(err, api.ErrLoginRequired) {
			s.tokens = Tokens{}
			_ = s.store.Delete()
			return "", err
		}
		// Transient failure (offline, 5xx): keep using the old access token
		// while it is still technically valid.
		if !force && t.Valid(now, 0) {
			return t.AccessToken, nil
		}
		return "", err
	}
	s.tokens = nt
	_ = s.store.Save(nt)
	if s.OnChange != nil {
		go s.OnChange(nt)
	}
	return nt.AccessToken, nil
}
