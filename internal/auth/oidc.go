// Package auth signs in to HSE's Keycloak (saml.hse.ru) with the same
// public OIDC client the Android app uses, and keeps the tokens fresh.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"hse-app-z/internal/api"
)

const (
	Issuer        = "https://saml.hse.ru/realms/hse"
	AuthEndpoint  = Issuer + "/protocol/openid-connect/auth"
	TokenEndpoint = Issuer + "/protocol/openid-connect/token"
	ClientID      = "app-x-android"
	// DefaultPort and CallbackPath form the redirect URI registered for the
	// client: http://localhost:8001/callback.
	DefaultPort  = 8001
	CallbackPath = "/callback"
	// UserAgent: the WAF in front of saml.hse.ru blocks default library agents.
	UserAgent = "okhttp/4.12.0"
)

// ErrInvalidGrant means the refresh token (or auth code) was rejected:
// expired, revoked, or the session was ended. Wraps api.ErrLoginRequired.
var ErrInvalidGrant = fmt.Errorf("%w: refresh token rejected", api.ErrLoginRequired)

// Tokens is the persisted session.
type Tokens struct {
	AccessToken      string    `json:"access_token"`
	RefreshToken     string    `json:"refresh_token"`
	IDToken          string    `json:"id_token,omitempty"`
	TokenType        string    `json:"token_type,omitempty"`
	Scope            string    `json:"scope,omitempty"`
	ExpiresAt        time.Time `json:"expires_at"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at,omitempty"` // zero: unknown or offline token
}

// Valid reports whether the access token is usable for at least margin.
func (t Tokens) Valid(now time.Time, margin time.Duration) bool {
	return t.AccessToken != "" && !t.ExpiresAt.IsZero() && now.Add(margin).Before(t.ExpiresAt)
}

// CanRefresh reports whether a refresh is worth attempting.
func (t Tokens) CanRefresh(now time.Time) bool {
	if t.RefreshToken == "" {
		return false
	}
	return t.RefreshExpiresAt.IsZero() || now.Before(t.RefreshExpiresAt)
}

type tokenResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	IDToken          string `json:"id_token"`
	TokenType        string `json:"token_type"`
	Scope            string `json:"scope"`
	ExpiresIn        int64  `json:"expires_in"`
	RefreshExpiresIn int64  `json:"refresh_expires_in"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// OAuthError is an error response from the token endpoint.
type OAuthError struct {
	Status      int
	Code        string
	Description string
}

func (e *OAuthError) Error() string {
	s := e.Code
	if s == "" {
		s = fmt.Sprintf("HTTP %d", e.Status)
	}
	if e.Description != "" {
		s += ": " + e.Description
	}
	return "sign-in failed: " + s
}

// Client performs token requests. The zero value uses http.DefaultClient
// semantics with a timeout.
type Client struct {
	HTTP          *http.Client
	TokenEndpoint string
	ClientID      string
	Now           func() time.Time
}

func (c *Client) httpClient() *http.Client {
	if c != nil && c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *Client) endpoint() string {
	if c != nil && c.TokenEndpoint != "" {
		return c.TokenEndpoint
	}
	return TokenEndpoint
}

func (c *Client) clientID() string {
	if c != nil && c.ClientID != "" {
		return c.ClientID
	}
	return ClientID
}

func (c *Client) now() time.Time {
	if c != nil && c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Exchange trades an authorization code for tokens.
func (c *Client) Exchange(ctx context.Context, code, redirectURI, codeVerifier string) (Tokens, error) {
	form := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"client_id":    {c.clientID()},
		"redirect_uri": {redirectURI},
	}
	if codeVerifier != "" {
		form.Set("code_verifier", codeVerifier)
	}
	return c.tokenRequest(ctx, form)
}

// Refresh redeems a refresh token. Keycloak rotates refresh tokens, so the
// returned Tokens must replace the stored ones.
func (c *Client) Refresh(ctx context.Context, refreshToken string) (Tokens, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {c.clientID()},
	}
	t, err := c.tokenRequest(ctx, form)
	if err != nil {
		return Tokens{}, err
	}
	if t.RefreshToken == "" {
		t.RefreshToken = refreshToken
	}
	return t, nil
}

func (c *Client) tokenRequest(ctx context.Context, form url.Values) (Tokens, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(), strings.NewReader(form.Encode()))
	if err != nil {
		return Tokens{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", UserAgent)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return Tokens{}, &api.NetworkError{Err: err}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Tokens{}, &api.NetworkError{Err: err}
	}
	var tr tokenResponse
	jerr := json.Unmarshal(body, &tr)
	if resp.StatusCode != http.StatusOK || tr.Error != "" || tr.AccessToken == "" {
		oe := &OAuthError{Status: resp.StatusCode, Code: tr.Error, Description: tr.ErrorDescription}
		if jerr != nil && oe.Code == "" {
			// HTML from a WAF/proxy, etc.
			oe.Description = "unexpected response from saml.hse.ru"
		}
		if tr.Error == "invalid_grant" || tr.Error == "invalid_token" || tr.Error == "unauthorized_client" {
			return Tokens{}, fmt.Errorf("%w (%s)", ErrInvalidGrant, oe.Error())
		}
		if resp.StatusCode >= 500 {
			return Tokens{}, &api.NetworkError{Err: oe}
		}
		return Tokens{}, oe
	}
	now := c.now()
	t := Tokens{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		IDToken:      tr.IDToken,
		TokenType:    tr.TokenType,
		Scope:        tr.Scope,
	}
	// Prefer the token's own exp claim; fall back to expires_in.
	if cl, err := ParseClaims(tr.AccessToken); err == nil && cl.Exp > 0 {
		t.ExpiresAt = time.Unix(cl.Exp, 0)
	} else if tr.ExpiresIn > 0 {
		t.ExpiresAt = now.Add(time.Duration(tr.ExpiresIn) * time.Second)
	} else {
		t.ExpiresAt = now.Add(5 * time.Minute)
	}
	if tr.RefreshExpiresIn > 0 {
		t.RefreshExpiresAt = now.Add(time.Duration(tr.RefreshExpiresIn) * time.Second)
	}
	return t, nil
}

// IsLoginRequired reports whether err means the user must sign in again.
func IsLoginRequired(err error) bool { return errors.Is(err, api.ErrLoginRequired) }
