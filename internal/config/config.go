// Package config owns on-disk state: where files live, user settings, and
// locally stored favourites.
package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const appDir = "hse-app-z"

// Paths are the directories the app writes to.
type Paths struct {
	ConfigDir string // tokens, settings, favourites
	CacheDir  string // HTTP response cache
}

// DefaultPaths uses the OS conventions, overridable with HSE_APP_Z_HOME
// (both dirs then live under that directory).
func DefaultPaths() (Paths, error) {
	if home := os.Getenv("HSE_APP_Z_HOME"); home != "" {
		return Paths{ConfigDir: home, CacheDir: filepath.Join(home, "cache")}, nil
	}
	cfg, err := os.UserConfigDir()
	if err != nil {
		return Paths{}, err
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		cache = filepath.Join(cfg, appDir, "cache")
	} else {
		cache = filepath.Join(cache, appDir)
	}
	return Paths{ConfigDir: filepath.Join(cfg, appDir), CacheDir: cache}, nil
}

func (p Paths) TokensFile() string     { return filepath.Join(p.ConfigDir, "tokens.json") }
func (p Paths) SettingsFile() string   { return filepath.Join(p.ConfigDir, "settings.json") }
func (p Paths) FavouritesFile() string { return filepath.Join(p.ConfigDir, "favourites.json") }
func (p Paths) HTTPCacheDir() string   { return filepath.Join(p.CacheDir, "http") }

// ------------------------------------------------------------------ files

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func writeJSON(path string, v any) error {
	if path == "" {
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	_ = os.Chmod(tmp.Name(), 0o600)
	return os.Rename(tmp.Name(), path)
}

// --------------------------------------------------------------- settings

// Settings are user preferences.
type Settings struct {
	Lang             string   `json:"lang"` // "en" or "ru"
	DismissedBanners []string `json:"dismissed_banners,omitempty"`

	path string
	mu   sync.Mutex
}

// LoadSettings reads settings, returning defaults when missing or corrupt.
func LoadSettings(path string) *Settings {
	s := &Settings{Lang: "en"}
	_ = readJSON(path, s)
	s.path = path
	if s.Lang != "ru" {
		s.Lang = "en"
	}
	return s
}

func (s *Settings) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeJSON(s.path, s)
}

func (s *Settings) GetLang() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Lang
}

func (s *Settings) SetLang(lang string) {
	s.mu.Lock()
	if lang != "ru" {
		lang = "en"
	}
	s.Lang = lang
	s.mu.Unlock()
	_ = s.Save()
}

func (s *Settings) BannerDismissed(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.DismissedBanners {
		if d == id {
			return true
		}
	}
	return false
}

func (s *Settings) DismissBanner(id string) {
	if id == "" {
		return
	}
	s.mu.Lock()
	for _, d := range s.DismissedBanners {
		if d == id {
			s.mu.Unlock()
			return
		}
	}
	s.DismissedBanners = append(s.DismissedBanners, id)
	s.mu.Unlock()
	_ = s.Save()
}

// ------------------------------------------------------------- favourites

// Favourite kinds.
const (
	KindPerson     = "person"
	KindGroup      = "group"
	KindAuditorium = "auditorium"
)

// Favourite is a starred timetable target, kept locally.
type Favourite struct {
	Kind     string    `json:"kind"`
	Key      string    `json:"key"` // email for people, RUZ id for groups/rooms
	Title    string    `json:"title"`
	Subtitle string    `json:"subtitle,omitempty"`
	AddedAt  time.Time `json:"added_at"`
}

// Favourites is a small persisted list, safe for concurrent use.
type Favourites struct {
	path  string
	mu    sync.Mutex
	items []Favourite
}

func LoadFavourites(path string) *Favourites {
	f := &Favourites{path: path}
	var items []Favourite
	if err := readJSON(path, &items); err == nil {
		for _, it := range items {
			it.Key = normKey(it.Kind, it.Key)
			if it.Key != "" && it.Kind != "" {
				f.items = append(f.items, it)
			}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		// Corrupt file: start empty but don't overwrite it until changed.
		f.items = nil
	}
	return f
}

func normKey(kind, key string) string {
	key = strings.TrimSpace(key)
	switch kind {
	case KindPerson:
		key = strings.ToLower(key)
	case KindGroup, KindAuditorium:
		// Search returns "ruz1033", lessons carry "1033": same room. The
		// timetable API accepts both forms.
		if rest, ok := strings.CutPrefix(key, "ruz"); ok && rest != "" && strings.Trim(rest, "0123456789") == "" {
			key = rest
		}
	}
	return key
}

// List returns favourites, most recently added first.
func (f *Favourites) List() []Favourite {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Favourite, len(f.items))
	for i := range f.items {
		out[i] = f.items[len(f.items)-1-i]
	}
	return out
}

func (f *Favourites) Has(kind, key string) bool {
	key = normKey(kind, key)
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, it := range f.items {
		if it.Kind == kind && it.Key == key {
			return true
		}
	}
	return false
}

// Toggle adds or removes a favourite and reports whether it is now starred.
func (f *Favourites) Toggle(fav Favourite) (bool, error) {
	fav.Key = normKey(fav.Kind, fav.Key)
	if fav.Key == "" || fav.Kind == "" {
		return false, errors.New("nothing to star")
	}
	// Hold the lock while writing so concurrent toggles can't persist an
	// older snapshot over a newer one.
	f.mu.Lock()
	defer f.mu.Unlock()
	idx := -1
	for i, it := range f.items {
		if it.Kind == fav.Kind && it.Key == fav.Key {
			idx = i
			break
		}
	}
	starred := idx < 0
	if starred {
		if fav.AddedAt.IsZero() {
			fav.AddedAt = time.Now()
		}
		f.items = append(f.items, fav)
	} else {
		f.items = append(f.items[:idx], f.items[idx+1:]...)
	}
	return starred, writeJSON(f.path, f.items)
}
