package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

// ErrLoginRequired means there is no usable session: the user must sign in.
// The auth package returns errors wrapping this from token providers.
var ErrLoginRequired = errors.New("login required")

// APIError is a non-2xx response. The backend wraps errors as
// {"error":{"name":…,"message":…},"trace_id":…}.
type APIError struct {
	Status  int
	Name    string
	Message string
	TraceID string
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = e.Name
	}
	if msg == "" {
		return fmt.Sprintf("HTTP %d", e.Status)
	}
	return fmt.Sprintf("HTTP %d: %s", e.Status, msg)
}

// NotFound reports a 404 (unknown person, missing menu, …).
func (e *APIError) NotFound() bool { return e.Status == 404 }

func parseAPIError(status int, body []byte) *APIError {
	e := &APIError{Status: status}
	var env struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
		TraceID string          `json:"trace_id"`
	}
	if json.Unmarshal(body, &env) == nil {
		e.TraceID = env.TraceID
		var inner struct {
			Name    string `json:"name"`
			Message string `json:"message"`
		}
		var s string
		switch {
		case json.Unmarshal(env.Error, &inner) == nil && (inner.Name != "" || inner.Message != ""):
			e.Name, e.Message = inner.Name, inner.Message
		case json.Unmarshal(env.Error, &s) == nil:
			e.Message = s
		}
		if e.Message == "" {
			e.Message = env.Message
		}
	}
	if e.Message == "" && e.Name == "" {
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > 200 {
			snippet = snippet[:200] + "…"
		}
		if snippet != "" && !strings.HasPrefix(snippet, "<") {
			e.Message = snippet
		}
	}
	return e
}

// RateLimitError is a 429 without cached data to fall back to.
type RateLimitError struct {
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("too many requests, try again in %ds", int(e.RetryAfter.Round(time.Second)/time.Second))
	}
	return "too many requests, try again shortly"
}

// DecodeError means the server answered 2xx with an unexpected body shape.
type DecodeError struct {
	Path string
	Err  error
}

func (e *DecodeError) Error() string {
	return fmt.Sprintf("unexpected response from %s: %v", e.Path, e.Err)
}

func (e *DecodeError) Unwrap() error { return e.Err }

// NetworkError wraps transport failures (DNS, TLS, timeouts, offline).
type NetworkError struct{ Err error }

func (e *NetworkError) Error() string { return "network error: " + e.Err.Error() }
func (e *NetworkError) Unwrap() error { return e.Err }

// IsNetwork reports whether err is a transport failure.
func IsNetwork(err error) bool {
	var ne *NetworkError
	if errors.As(err, &ne) {
		return true
	}
	var ue *url.Error
	var oe *net.OpError
	return errors.As(err, &ue) || errors.As(err, &oe)
}

// Friendly renders any error from this package (or auth) as one short line
// suitable for the UI.
func Friendly(err error) string {
	if err == nil {
		return ""
	}
	var (
		ae *APIError
		re *RateLimitError
		de *DecodeError
		ne *NetworkError
	)
	switch {
	case errors.Is(err, ErrLoginRequired):
		return "Session expired — please sign in again"
	case errors.As(err, &re):
		return "Rate limited: " + re.Error()
	case errors.As(err, &ae):
		switch {
		case ae.Status == 404:
			if ae.Message != "" {
				return "Not found (" + ae.Message + ")"
			}
			return "Not found"
		case ae.Status >= 500:
			return fmt.Sprintf("HSE server error (%d) — try again later", ae.Status)
		}
		return ae.Error()
	case errors.As(err, &de):
		return "Unexpected response format from " + de.Path
	case errors.As(err, &ne):
		if errors.Is(ne.Err, context.DeadlineExceeded) || strings.Contains(ne.Err.Error(), "Client.Timeout") || strings.Contains(ne.Err.Error(), "deadline exceeded") {
			return "Request timed out — check your connection"
		}
		return "Can't reach HSE servers — check your connection"
	case errors.Is(err, context.DeadlineExceeded):
		return "Request timed out"
	}
	return err.Error()
}
