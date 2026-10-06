package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFavouritesToggle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "favourites.json")
	f := LoadFavourites(path)
	if len(f.List()) != 0 {
		t.Fatal("new favourites not empty")
	}

	on, err := f.Toggle(Favourite{Kind: KindPerson, Key: "  YAKinderknecht@HSE.ru ", Title: "Kinderknecht"})
	if err != nil || !on {
		t.Fatalf("Toggle on: %v %v", on, err)
	}
	for _, k := range []string{"yakinderknecht@hse.ru", "YAKINDERKNECHT@HSE.RU", " yakinderknecht@hse.ru"} {
		if !f.Has(KindPerson, k) {
			t.Errorf("Has(%q) = false", k)
		}
	}
	if f.Has(KindGroup, "yakinderknecht@hse.ru") {
		t.Error("kinds must not mix")
	}
	list := f.List()
	if len(list) != 1 || list[0].Key != "yakinderknecht@hse.ru" || list[0].AddedAt.IsZero() {
		t.Errorf("list = %+v", list)
	}

	// Toggling with different case removes it.
	on, err = f.Toggle(Favourite{Kind: KindPerson, Key: "yakinderknecht@hse.ru"})
	if err != nil || on {
		t.Fatalf("Toggle off: %v %v", on, err)
	}
	if f.Has(KindPerson, "yakinderknecht@hse.ru") || len(f.List()) != 0 {
		t.Error("not removed")
	}

	// Group and room ids are case-sensitive.
	if _, err := f.Toggle(Favourite{Kind: KindGroup, Key: "ruz47990", Title: "БИБ255"}); err != nil {
		t.Fatal(err)
	}
	if !f.Has(KindGroup, " ruz47990 ") || f.Has(KindGroup, "RUZ47990") {
		t.Error("group key normalisation")
	}

	for _, bad := range []Favourite{{Kind: KindPerson, Key: "  "}, {Key: "x"}, {}} {
		if on, err := f.Toggle(bad); err == nil || on {
			t.Errorf("Toggle(%+v) = %v, %v", bad, on, err)
		}
	}
}

func TestFavouritesPersistenceAndOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "favourites.json")
	f := LoadFavourites(path)
	added := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	items := []Favourite{
		{Kind: KindPerson, Key: "a@hse.ru", Title: "A", Subtitle: "Professor", AddedAt: added},
		{Kind: KindGroup, Key: "ruz1", Title: "Group 1"},
		{Kind: KindAuditorium, Key: "ruz1033", Title: "R615"},
		{Kind: KindPerson, Key: "b@edu.hse.ru", Title: "B"},
	}
	for _, it := range items {
		if on, err := f.Toggle(it); err != nil || !on {
			t.Fatalf("Toggle(%+v): %v %v", it, on, err)
		}
	}
	// Remove one in the middle.
	if on, _ := f.Toggle(Favourite{Kind: KindGroup, Key: "ruz1"}); on {
		t.Fatal("expected removal")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("file mode %v", info.Mode().Perm())
	}

	g := LoadFavourites(path)
	got := g.List()
	want := []string{"b@edu.hse.ru", "1033", "a@hse.ru"} // newest first; ruz prefix normalised
	if len(got) != len(want) {
		t.Fatalf("reloaded %d items: %+v", len(got), got)
	}
	for i, w := range want {
		if got[i].Key != w {
			t.Errorf("List()[%d] = %q, want %q", i, got[i].Key, w)
		}
	}
	if a := got[2]; a.Title != "A" || a.Subtitle != "Professor" || !a.AddedAt.Equal(added) || a.Kind != KindPerson {
		t.Errorf("reloaded item = %+v", a)
	}
	if !g.Has(KindAuditorium, "ruz1033") || g.Has(KindGroup, "ruz1") {
		t.Error("Has after reload")
	}

	// List returns a copy.
	got[0].Title = "mutated"
	if g.List()[0].Title == "mutated" {
		t.Error("List exposes internal storage")
	}
}

func TestFavouritesInMemory(t *testing.T) {
	f := LoadFavourites("")
	if on, err := f.Toggle(Favourite{Kind: KindPerson, Key: "X@hse.ru"}); err != nil || !on {
		t.Fatalf("Toggle: %v %v", on, err)
	}
	if !f.Has(KindPerson, "x@hse.ru") || len(f.List()) != 1 {
		t.Error("in-memory favourite missing")
	}
	if on, err := f.Toggle(Favourite{Kind: KindPerson, Key: "x@hse.ru"}); err != nil || on {
		t.Errorf("Toggle off: %v %v", on, err)
	}
}

func TestFavouritesCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "favourites.json")
	if err := os.WriteFile(path, []byte("{definitely not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := LoadFavourites(path)
	if len(f.List()) != 0 {
		t.Errorf("corrupt file produced %+v", f.List())
	}
	// Not overwritten just by loading.
	if b, _ := os.ReadFile(path); string(b) != "{definitely not json" {
		t.Error("corrupt file rewritten on load")
	}
	if _, err := f.Toggle(Favourite{Kind: KindGroup, Key: "ruz1"}); err != nil {
		t.Fatal(err)
	}
	if g := LoadFavourites(path); !g.Has(KindGroup, "ruz1") || len(g.List()) != 1 {
		t.Error("file not repaired by the next change")
	}
}

func TestFavouritesLoadSanitises(t *testing.T) {
	path := filepath.Join(t.TempDir(), "favourites.json")
	raw := `[
	 {"kind":"person","key":"Mixed.Case@HSE.ru","title":"Hand-edited"},
	 {"kind":"","key":"orphan"},
	 {"kind":"group","key":""},
	 {"kind":"group","key":"ruz5","title":"ok"}
	]`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	f := LoadFavourites(path)
	if len(f.List()) != 2 {
		t.Errorf("invalid entries kept: %+v", f.List())
	}
	if !f.Has(KindPerson, "mixed.case@hse.ru") {
		t.Error("person key from disk not normalised")
	}
	if on, _ := f.Toggle(Favourite{Kind: KindPerson, Key: "mixed.case@hse.ru"}); on {
		t.Error("toggle should remove the hand-edited entry, not add a duplicate")
	}
}

func TestFavouritesConcurrentToggles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "favourites.json")
	f := LoadFavourites(path)
	const n = 40
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := f.Toggle(Favourite{Kind: KindGroup, Key: fmt.Sprintf("ruz%d", i)}); err != nil {
				t.Error(err)
			}
			_ = f.List()
			_ = f.Has(KindGroup, "ruz0")
		}(i)
	}
	wg.Wait()
	if len(f.List()) != n {
		t.Errorf("in memory: %d items", len(f.List()))
	}
	// The file must reflect the final state, not an older snapshot.
	if got := len(LoadFavourites(path).List()); got != n {
		t.Errorf("on disk: %d items, want %d", got, n)
	}
}

func TestSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	s := LoadSettings(path)
	if s.GetLang() != "en" || s.BannerDismissed("b1") {
		t.Errorf("defaults: lang=%q", s.GetLang())
	}
	s.SetLang("ru")
	if s.GetLang() != "ru" {
		t.Error("SetLang(ru)")
	}
	if LoadSettings(path).GetLang() != "ru" {
		t.Error("lang not persisted")
	}
	s.SetLang("de")
	if s.GetLang() != "en" || LoadSettings(path).GetLang() != "en" {
		t.Error("unsupported lang should normalise to en")
	}

	s.DismissBanner("6ab3395b85ef4e9464628df4")
	s.DismissBanner("6ab3395b85ef4e9464628df4")
	s.DismissBanner("")
	r := LoadSettings(path)
	if !r.BannerDismissed("6ab3395b85ef4e9464628df4") || r.BannerDismissed("") || len(r.DismissedBanners) != 1 {
		t.Errorf("dismissed banners after reload: %v", r.DismissedBanners)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var onDisk map[string]any
	if err := json.Unmarshal(b, &onDisk); err != nil || onDisk["lang"] != "en" {
		t.Errorf("settings file = %s", b)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("settings mode %v", info.Mode().Perm())
	}
}

func TestSettingsInvalidAndCorrupt(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		`{"lang":"fr"}`:     "en",
		`{"lang":"RU"}`:     "en",
		`{"lang":5}`:        "en",
		`{"lang":"ru"}`:     "ru",
		`{broken`:           "en",
		``:                  "en",
		`{"lang":null}`:     "en",
		`["not","a","map"]`: "en",
	}
	i := 0
	for content, want := range cases {
		i++
		path := filepath.Join(dir, fmt.Sprintf("s%d.json", i))
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := LoadSettings(path).GetLang(); got != want {
			t.Errorf("%s: lang = %q, want %q", content, got, want)
		}
	}
	if s := LoadSettings(filepath.Join(dir, "missing.json")); s.GetLang() != "en" {
		t.Error("missing file")
	}

	// In-memory settings work and never touch disk.
	mem := LoadSettings("")
	mem.SetLang("ru")
	mem.DismissBanner("x")
	if mem.GetLang() != "ru" || !mem.BannerDismissed("x") || mem.Save() != nil {
		t.Error("in-memory settings")
	}
}

func TestSettingsConcurrentDismiss(t *testing.T) {
	s := LoadSettings(filepath.Join(t.TempDir(), "settings.json"))
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s.DismissBanner("same")
			s.DismissBanner(fmt.Sprint(i))
			_ = s.GetLang()
		}(i)
	}
	wg.Wait()
	n := 0
	for _, id := range s.DismissedBanners {
		if id == "same" {
			n++
		}
	}
	if n != 1 || len(s.DismissedBanners) != 21 {
		t.Errorf("dismissed = %v", s.DismissedBanners)
	}
}

func TestDefaultPaths(t *testing.T) {
	home := filepath.Join(t.TempDir(), "custom home")
	t.Setenv("HSE_APP_Z_HOME", home)
	p, err := DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if p.ConfigDir != home || p.CacheDir != filepath.Join(home, "cache") {
		t.Errorf("paths = %+v", p)
	}
	if p.TokensFile() != filepath.Join(home, "tokens.json") ||
		p.SettingsFile() != filepath.Join(home, "settings.json") ||
		p.FavouritesFile() != filepath.Join(home, "favourites.json") ||
		p.HTTPCacheDir() != filepath.Join(home, "cache", "http") {
		t.Errorf("file paths: %s %s %s %s", p.TokensFile(), p.SettingsFile(), p.FavouritesFile(), p.HTTPCacheDir())
	}

	t.Setenv("HSE_APP_Z_HOME", "")
	p, err = DefaultPaths()
	if err != nil {
		t.Skipf("no user config dir on this system: %v", err)
	}
	if filepath.Base(p.ConfigDir) != "hse-app-z" || !strings.Contains(p.CacheDir, "hse-app-z") || p.ConfigDir == p.CacheDir {
		t.Errorf("OS default paths = %+v", p)
	}
}

func TestFavouriteRUZPrefixNormalised(t *testing.T) {
	f := LoadFavourites("")
	if on, err := f.Toggle(Favourite{Kind: KindAuditorium, Key: "ruz1033", Title: "Room R615"}); !on || err != nil {
		t.Fatal("toggle on")
	}
	if !f.Has(KindAuditorium, "1033") || !f.Has(KindAuditorium, "ruz1033") {
		t.Error("ruz-prefixed and bare room ids must be the same favourite")
	}
	if on, _ := f.Toggle(Favourite{Kind: KindAuditorium, Key: "1033"}); on {
		t.Error("toggling the bare id must remove the ruz one")
	}
	f.Toggle(Favourite{Kind: KindGroup, Key: "ruzabc"})
	if !f.Has(KindGroup, "ruzabc") || f.Has(KindGroup, "abc") {
		t.Error("non-numeric ids keep their prefix")
	}
}
