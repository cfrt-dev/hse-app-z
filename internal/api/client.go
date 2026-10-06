package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultBaseURL = "https://api.hseapp.ru"
	// UserAgent matches the Android client the tokens are issued for; the
	// WAF in front of HSE services rejects unknown agents.
	UserAgent       = "okhttp/4.12.0"
	maxBodyBytes    = 32 << 20
	defaultTimeout  = 25 * time.Second
	maxParallelReqs = 4
)

// TokenProvider supplies bearer tokens. force asks for a refreshed token
// (used after a 401). Implementations return an error wrapping
// ErrLoginRequired when the user must sign in again.
type TokenProvider interface {
	AccessToken(ctx context.Context, force bool) (string, error)
}

// Meta describes where a response came from.
type Meta struct {
	// FromCache is true when the body was served from the local cache
	// (after a 304 or as a fallback).
	FromCache bool
	// Stale is true when the server could not be reached (or rate limited
	// us) and the body is the last cached copy.
	Stale bool
	// StaleReason explains why stale data is shown.
	StaleReason string
	// Fetched is when the body was last confirmed by the server.
	Fetched time.Time
}

// Client talks to api.hseapp.ru. It is safe for concurrent use.
type Client struct {
	BaseURL string
	HTTP    *http.Client
	Tokens  TokenProvider // nil: send no Authorization header
	Cache   *Cache

	mu         sync.RWMutex
	lang       string // "en" or "ru"
	cacheScope string // separates cached data of different accounts

	sem     chan struct{}
	semOnce sync.Once
}

// NewClient returns a client for base (DefaultBaseURL when empty).
func NewClient(base string, tokens TokenProvider, cache *Cache) *Client {
	if base == "" {
		base = DefaultBaseURL
	}
	return &Client{
		BaseURL: strings.TrimRight(base, "/"),
		HTTP:    &http.Client{Timeout: defaultTimeout},
		Tokens:  tokens,
		Cache:   cache,
		lang:    "en",
		sem:     make(chan struct{}, maxParallelReqs),
	}
}

// SetLang selects the response language ("en" or "ru").
func (c *Client) SetLang(lang string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if lang != "ru" {
		lang = "en"
	}
	c.lang = lang
}

func (c *Client) Lang() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lang
}

// SetCacheScope isolates cached responses per account (use the token subject).
func (c *Client) SetCacheScope(scope string) {
	c.mu.Lock()
	c.cacheScope = scope
	c.mu.Unlock()
}

func acceptLanguage(lang string) string {
	if lang == "ru" {
		return "ru-RU;q=1.0, en-RU;q=0.9"
	}
	return "en-RU;q=1.0, ru-RU;q=0.9"
}

func (c *Client) get(ctx context.Context, path string, q url.Values, out any) (Meta, error) {
	return c.do(ctx, http.MethodGet, path, q, nil, out)
}

func (c *Client) do(ctx context.Context, method, path string, q url.Values, body any, out any) (Meta, error) {
	u := c.BaseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return Meta{}, err
		}
	}
	c.mu.RLock()
	scope := c.cacheScope
	c.mu.RUnlock()
	// Read the language once: the cache key and the header must agree even
	// if SetLang runs concurrently.
	lang := c.Lang()
	key := cacheKey(method, u, string(payload), lang, scope)
	cached := c.Cache.get(key)

	// Bound concurrency so a burst of UI activity can't trip the rate limit.
	c.semOnce.Do(func() {
		if c.sem == nil {
			c.sem = make(chan struct{}, maxParallelReqs)
		}
	})
	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		return Meta{}, ctx.Err()
	}

	fallback := func(reason string, cause error) (Meta, error) {
		if cached == nil {
			return Meta{}, cause
		}
		if err := decode(cached.Body, out, path); err != nil {
			return Meta{}, cause
		}
		return Meta{FromCache: true, Stale: true, StaleReason: reason, Fetched: cached.Stored}, nil
	}

	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(payload))
		if err != nil {
			return Meta{}, err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Accept-Language", acceptLanguage(lang))
		req.Header.Set("User-Agent", UserAgent)
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if cached != nil && cached.ETag != "" {
			req.Header.Set("If-None-Match", cached.ETag)
		}
		if c.Tokens != nil {
			tok, err := c.Tokens.AccessToken(ctx, attempt > 0)
			if err != nil {
				if errors.Is(err, ErrLoginRequired) || ctx.Err() != nil {
					return Meta{}, err
				}
				// Couldn't refresh the session: offline, or saml.hse.ru is
				// misbehaving. Either way, cached data beats an empty screen.
				var ne *NetworkError
				if errors.As(err, &ne) {
					return fallback("offline", err)
				}
				return fallback("sign-in server error", err)
			}
			req.Header.Set("Authorization", "Bearer "+tok)
		}

		resp, err := c.HTTP.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return Meta{}, ctx.Err()
			}
			return fallback("offline", &NetworkError{Err: err})
		}
		data, rerr := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
		resp.Body.Close()
		if rerr != nil {
			if ctx.Err() != nil {
				return Meta{}, ctx.Err()
			}
			return fallback("offline", &NetworkError{Err: rerr})
		}

		switch {
		case resp.StatusCode == http.StatusNotModified:
			if cached == nil {
				// Shouldn't happen (we only send If-None-Match with a cached
				// body), but never leave the caller without data.
				return Meta{}, &APIError{Status: 304, Message: "not modified but no cached copy"}
			}
			if err := decode(cached.Body, out, path); err != nil {
				return Meta{}, err
			}
			c.Cache.touch(key)
			return Meta{FromCache: true, Fetched: time.Now()}, nil

		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			if err := decode(data, out, path); err != nil {
				return Meta{}, err
			}
			c.Cache.put(key, resp.Header.Get("Etag"), data)
			return Meta{Fetched: time.Now()}, nil

		case resp.StatusCode == http.StatusUnauthorized && c.Tokens != nil && attempt == 0:
			continue // refresh the token once and retry

		case resp.StatusCode == http.StatusUnauthorized:
			return Meta{}, fmt.Errorf("%w (%s)", ErrLoginRequired, parseAPIError(resp.StatusCode, data).Error())

		case resp.StatusCode == http.StatusTooManyRequests:
			rl := &RateLimitError{RetryAfter: retryAfter(resp.Header)}
			return fallback("rate limited", rl)

		case resp.StatusCode >= 500:
			return fallback("server error", parseAPIError(resp.StatusCode, data))

		default:
			return Meta{}, parseAPIError(resp.StatusCode, data)
		}
	}
}

func decode(data []byte, out any, path string) error {
	if out == nil {
		return nil
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return &DecodeError{Path: path, Err: errors.New("empty body")}
	}
	if err := json.Unmarshal(data, out); err != nil {
		return &DecodeError{Path: path, Err: err}
	}
	return nil
}

func retryAfter(h http.Header) time.Duration {
	for _, k := range []string{"Retry-After", "X-Ratelimit-Reset"} {
		v := strings.TrimSpace(h.Get(k))
		if v == "" {
			continue
		}
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			// X-Ratelimit-Reset is seconds until reset here (values like 8, 60);
			// guard against an epoch timestamp just in case.
			if n > 1_000_000_000 {
				return time.Until(time.Unix(int64(n), 0))
			}
			return time.Duration(n) * time.Second
		}
		if t, err := http.ParseTime(v); err == nil {
			return time.Until(t)
		}
	}
	return 0
}
