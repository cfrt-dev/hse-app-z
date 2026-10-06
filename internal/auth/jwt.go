package auth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Claims are the parts of the Keycloak access token the app uses. The token
// is not verified locally — the API does that; we only read it.
type Claims struct {
	Exp               int64  `json:"exp"`
	Iat               int64  `json:"iat"`
	Sub               string `json:"sub"`
	Azp               string `json:"azp"`
	Email             string `json:"email"`
	PreferredUsername string `json:"preferred_username"`
	Name              string `json:"name"`
	GivenName         string `json:"given_name"`
	FamilyName        string `json:"family_name"`
}

// ParseClaims decodes the payload of a JWT without verifying it.
func ParseClaims(token string) (Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return Claims{}, errors.New("not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return Claims{}, err
	}
	var c Claims
	if err := json.Unmarshal(payload, &c); err != nil {
		return Claims{}, err
	}
	return c, nil
}

// UserEmail is the email the timetable is keyed by.
func (c Claims) UserEmail() string {
	if c.Email != "" {
		return strings.ToLower(strings.TrimSpace(c.Email))
	}
	if strings.Contains(c.PreferredUsername, "@") {
		return strings.ToLower(strings.TrimSpace(c.PreferredUsername))
	}
	return ""
}

// DisplayName is the user's name for the header.
func (c Claims) DisplayName() string {
	if n := strings.TrimSpace(c.Name); n != "" {
		return n
	}
	if n := strings.TrimSpace(c.GivenName + " " + c.FamilyName); n != "" {
		return n
	}
	return c.UserEmail()
}

// Expiry is the access token expiry (zero if unknown).
func (c Claims) Expiry() time.Time {
	if c.Exp == 0 {
		return time.Time{}
	}
	return time.Unix(c.Exp, 0)
}
