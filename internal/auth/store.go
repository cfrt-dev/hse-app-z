package auth

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// Store persists tokens as JSON readable only by the current user.
type Store struct {
	Path string
}

// Load returns the saved tokens; ok is false when nothing is saved.
func (s *Store) Load() (t Tokens, ok bool, err error) {
	if s == nil || s.Path == "" {
		return Tokens{}, false, nil
	}
	b, err := os.ReadFile(s.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return Tokens{}, false, nil
	}
	if err != nil {
		return Tokens{}, false, err
	}
	if err := json.Unmarshal(b, &t); err != nil {
		// Corrupt file: treat as signed out rather than crash.
		return Tokens{}, false, nil
	}
	if t.AccessToken == "" && t.RefreshToken == "" {
		return Tokens{}, false, nil
	}
	return t, true, nil
}

// Save writes tokens atomically with 0600 permissions.
func (s *Store) Save(t Tokens) error {
	if s == nil || s.Path == "" {
		return nil
	}
	dir := filepath.Dir(s.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tokens-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.Path)
}

// Delete removes saved tokens.
func (s *Store) Delete() error {
	if s == nil || s.Path == "" {
		return nil
	}
	err := os.Remove(s.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
