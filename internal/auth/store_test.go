package auth

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreRoundtrip(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Path: filepath.Join(dir, "nested", "deeper", "tokens.json")}

	if _, ok, err := s.Load(); ok || err != nil {
		t.Fatalf("missing file: ok=%v err=%v", ok, err)
	}
	in := Tokens{
		AccessToken:      "access",
		RefreshToken:     "refresh",
		IDToken:          "id",
		TokenType:        "Bearer",
		Scope:            "openid",
		ExpiresAt:        time.Date(2026, 10, 6, 15, 0, 0, 0, time.UTC),
		RefreshExpiresAt: time.Date(2026, 10, 6, 15, 30, 0, 0, time.UTC),
	}
	if err := s.Save(in); err != nil {
		t.Fatal(err)
	}
	out, ok, err := s.Load()
	if err != nil || !ok {
		t.Fatalf("Load: ok=%v err=%v", ok, err)
	}
	if out.AccessToken != in.AccessToken || out.RefreshToken != in.RefreshToken || out.IDToken != in.IDToken ||
		out.TokenType != in.TokenType || out.Scope != in.Scope ||
		!out.ExpiresAt.Equal(in.ExpiresAt) || !out.RefreshExpiresAt.Equal(in.RefreshExpiresAt) {
		t.Errorf("roundtrip:\n got %+v\nwant %+v", out, in)
	}

	info, err := os.Stat(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("token file mode = %v, want 0600", info.Mode().Perm())
	}
	dinfo, err := os.Stat(filepath.Dir(s.Path))
	if err != nil {
		t.Fatal(err)
	}
	if dinfo.Mode().Perm() != 0o700 {
		t.Errorf("token dir mode = %v, want 0700", dinfo.Mode().Perm())
	}

	// Overwrite keeps the mode and leaves no temp files behind.
	in.RefreshToken = "rotated"
	if err := s.Save(in); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Dir(s.Path))
	if len(entries) != 1 {
		t.Errorf("token dir has %d entries, want 1", len(entries))
	}
	if out, _, _ := s.Load(); out.RefreshToken != "rotated" {
		t.Errorf("after overwrite: %q", out.RefreshToken)
	}
	if info, _ := os.Stat(s.Path); info.Mode().Perm() != 0o600 {
		t.Errorf("mode after overwrite = %v", info.Mode().Perm())
	}
}

func TestStoreCorruptAndEmpty(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Path: filepath.Join(dir, "tokens.json")}
	for _, content := range []string{"{not json", "", "null", `{"access_token":"","refresh_token":""}`, `[]`} {
		if err := os.WriteFile(s.Path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		tok, ok, err := s.Load()
		if ok || err != nil || tok.AccessToken != "" {
			t.Errorf("content %q: ok=%v err=%v tok=%+v", content, ok, err, tok)
		}
	}
	// Refresh-only sessions count as signed in.
	if err := os.WriteFile(s.Path, []byte(`{"refresh_token":"r"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Load(); !ok {
		t.Error("refresh-only file should load")
	}
}

func TestStoreDelete(t *testing.T) {
	s := &Store{Path: filepath.Join(t.TempDir(), "tokens.json")}
	if err := s.Delete(); err != nil {
		t.Errorf("Delete of a missing file: %v", err)
	}
	if err := s.Save(Tokens{AccessToken: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(); err != nil {
		t.Errorf("second Delete: %v", err)
	}
	if _, err := os.Stat(s.Path); !os.IsNotExist(err) {
		t.Errorf("file still exists: %v", err)
	}
	if _, ok, _ := s.Load(); ok {
		t.Error("deleted session loads")
	}
}

func TestStoreNilAndEmptyPath(t *testing.T) {
	for _, s := range []*Store{nil, {}} {
		if err := s.Save(Tokens{AccessToken: "a"}); err != nil {
			t.Errorf("Save: %v", err)
		}
		if _, ok, err := s.Load(); ok || err != nil {
			t.Errorf("Load: ok=%v err=%v", ok, err)
		}
		if err := s.Delete(); err != nil {
			t.Errorf("Delete: %v", err)
		}
	}
}

func TestStoreLoadError(t *testing.T) {
	// A directory where the file should be is a real error, not "signed out".
	dir := t.TempDir()
	s := &Store{Path: dir}
	if _, ok, err := s.Load(); ok || err == nil {
		t.Errorf("Load(dir): ok=%v err=%v", ok, err)
	}
}
