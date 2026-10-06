package auth

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// makeJWT builds an unsigned token with the given claims.
func makeJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	b, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding
	return enc.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`)) + "." + enc.EncodeToString(b) + ".c2lnbmF0dXJl"
}

// accessJWT is a Keycloak-like access token expiring at exp.
func accessJWT(t *testing.T, sub string, exp time.Time) string {
	return makeJWT(t, map[string]any{
		"exp":   exp.Unix(),
		"iat":   exp.Add(-3 * time.Hour).Unix(),
		"sub":   sub,
		"azp":   ClientID,
		"email": "Student@edu.hse.ru",
		"name":  "Test Student",
	})
}

// keycloak is a mock token endpoint that records every request.
type keycloak struct {
	*httptest.Server
	hits atomic.Int32

	mu    sync.Mutex
	forms []url.Values
	reqs  []*http.Request

	// respond writes the response for request n (1-based).
	respond func(w http.ResponseWriter, form url.Values, n int)
}

func newKeycloak(t *testing.T, respond func(w http.ResponseWriter, form url.Values, n int)) *keycloak {
	t.Helper()
	kc := &keycloak{respond: respond}
	kc.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(kc.hits.Add(1))
		body, _ := io.ReadAll(r.Body)
		form, err := url.ParseQuery(string(body))
		if err != nil {
			t.Errorf("token request body is not form-encoded: %q", body)
		}
		kc.mu.Lock()
		kc.forms = append(kc.forms, form)
		kc.reqs = append(kc.reqs, r.Clone(r.Context()))
		kc.mu.Unlock()
		kc.respond(w, form, n)
	}))
	t.Cleanup(kc.Close)
	return kc
}

func (k *keycloak) form(i int) url.Values {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.forms[i]
}

func (k *keycloak) req(i int) *http.Request {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.reqs[i]
}

func (k *keycloak) client() *Client { return &Client{TokenEndpoint: k.URL} }

func writeTokens(w http.ResponseWriter, access, refresh string, expiresIn, refreshExpiresIn int) {
	resp := map[string]any{
		"access_token":       access,
		"expires_in":         expiresIn,
		"refresh_expires_in": refreshExpiresIn,
		"token_type":         "Bearer",
		"id_token":           "id-token",
		"scope":              "openid email profile",
	}
	if refresh != "" {
		resp["refresh_token"] = refresh
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func writeOAuthError(w http.ResponseWriter, status int, code, desc string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"error":%q,"error_description":%q}`, code, desc)
}
