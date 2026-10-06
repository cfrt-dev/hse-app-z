package search

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"hse-app-z/internal/api"
	"hse-app-z/internal/config"
	"hse-app-z/internal/ui"
	"hse-app-z/internal/ui/uitest"
)

// newPage opens the tab the way a user who wants to type does: with "/"
// (the root sends FocusSearchMsg).
func newPage(t *testing.T) (*Page, *uitest.Harness, *ui.Ctx) {
	t.Helper()
	ctx := uitest.Ctx(t)
	p := New(ctx).(*Page)
	h := uitest.New(t, p)
	h.Send(ui.FocusSearchMsg{})
	return p, h, ctx
}

// Reaching the tab with 1-8/tab must not focus the field: otherwise the
// next digit would be typed instead of switching tabs.
func TestTabSwitchDoesNotFocusField(t *testing.T) {
	p := New(uitest.Ctx(t)).(*Page)
	h := uitest.New(t, p)
	if p.Capturing() {
		t.Fatal("opening the tab must not capture keys")
	}
	h.Key("i")
	if !p.Capturing() {
		t.Fatal("i focuses the field")
	}
	h.Key("esc")
	h.Send(ui.FocusSearchMsg{})
	if !p.Capturing() {
		t.Fatal("/ (FocusSearchMsg) focuses the field")
	}
}

// checkSize asserts the raw (styled) view fits w×h.
func checkSize(t *testing.T, p ui.Page, w, h int) string {
	t.Helper()
	raw := p.View(w, h)
	lines := strings.Split(raw, "\n")
	if len(lines) > h {
		t.Errorf("%dx%d: %d lines", w, h, len(lines))
	}
	for i, l := range lines {
		if lw := ansi.StringWidth(l); lw > w {
			t.Errorf("%dx%d: line %d is %d wide: %q", w, h, i, lw, ansi.Strip(l))
		}
	}
	return ansi.Strip(raw)
}

func checkSizes(t *testing.T, p ui.Page) {
	t.Helper()
	for _, sz := range [][2]int{{100, 30}, {60, 12}, {60, 10}, {120, 30}, {200, 50}, {30, 5}, {1, 1}} {
		checkSize(t, p, sz[0], sz[1])
	}
}

func TestHomeShowsMeAndFavourites(t *testing.T) {
	p, h, _ := newPage(t)
	if !p.Capturing() {
		t.Fatal("input should be focused when the tab first opens")
	}
	v := h.View(100, 30)
	for _, want := range []string{"Me · Demo Student", "Favourites", "Star people, groups or rooms with f", "name, email, group"} {
		if !strings.Contains(v, want) {
			t.Errorf("home view lacks %q:\n%s", want, v)
		}
	}
	checkSizes(t, p)
	t.Logf("home 100x30:\n%s", v)
}

func TestTypingSearchesAndOpens(t *testing.T) {
	p, h, _ := newPage(t)
	h.Key("Rezn")
	v := h.View(100, 30)
	if !strings.Contains(v, "Reznichenko Margarita Vasilevna") || !strings.Contains(v, "12 results") {
		t.Fatalf("no results:\n%s", v)
	}
	if !p.Capturing() {
		t.Fatal("typing should keep the input focused")
	}
	h.Key("enter")
	if p.Capturing() {
		t.Fatal("enter should move focus to the list")
	}
	h.Key("enter")
	tg, ok := h.LastTarget()
	if !ok || tg.Kind != config.KindPerson || tg.Key != "mreznichenko@hse.ru" {
		t.Fatalf("enter should open the first hit, got %+v %v", tg, ok)
	}
	checkSizes(t, p)
	t.Logf("results 120x30:\n%s", h.View(120, 30))
}

func TestStarAndHome(t *testing.T) {
	p, h, ctx := newPage(t)
	h.Key("Rezn", "enter", "j", "f")
	if !ctx.Favs.Has(config.KindPerson, "mdreznichenko@hse.ru") {
		t.Fatalf("f should star the selected hit; statuses %v", h.Statuses())
	}
	if !strings.Contains(h.View(100, 30), "★") {
		t.Error("starred hit should show ★")
	}
	// Back to the field, clear it: the favourite shows up on the home list.
	h.Key("i", "ctrl+u")
	if p.input.Value() != "" {
		t.Fatalf("ctrl+u should clear, got %q", p.input.Value())
	}
	v := h.View(100, 30)
	if !strings.Contains(v, "Reznichenko Mikhail Dmitrievich") || strings.Contains(v, "Star people") {
		t.Fatalf("favourite not on the home list:\n%s", v)
	}
	// Unstar from the home list.
	h.Key("esc", "j", "f")
	if ctx.Favs.Has(config.KindPerson, "mdreznichenko@hse.ru") {
		t.Fatal("f on the favourite should unstar it")
	}
	if !strings.Contains(h.View(100, 30), "Star people") {
		t.Error("hint row should come back")
	}
}

func TestFocusSwitching(t *testing.T) {
	p, h, _ := newPage(t)
	h.Key("esc")
	if p.Capturing() {
		t.Fatal("esc should blur the input even when empty")
	}
	h.Key("i")
	if !p.Capturing() {
		t.Fatal("i should focus the input")
	}
	h.Key("Re", "esc")
	h.Send(ui.FocusSearchMsg{})
	if !p.Capturing() || p.input.Value() != "Re" || p.input.pos != 2 {
		t.Fatalf("FocusSearchMsg should focus with the text kept, cursor at end: %q %d", p.input.Value(), p.input.pos)
	}
	// up does nothing; down moves to the list.
	h.Key("up")
	if !p.Capturing() {
		t.Fatal("up should keep focus")
	}
	h.Key("down")
	if p.Capturing() {
		t.Fatal("down should move to the list")
	}
}

func TestShortQueryDoesNotSearch(t *testing.T) {
	p, h, _ := newPage(t)
	h.Key("R")
	if p.load.Seq != 0 {
		t.Fatalf("1-char query must not search (seq %d)", p.load.Seq)
	}
	h.Key("space", "space")
	if p.load.Seq != 0 {
		t.Fatal("whitespace must not search")
	}
	if !strings.Contains(h.View(100, 30), "type 2+ characters") {
		t.Error("expected a hint for short queries")
	}
}

func TestDebounceAndStaleResults(t *testing.T) {
	p, _, _ := newPage(t)
	// Type without running the debounce tick, then clear quickly.
	for _, k := range uitest.KeyMsgs("Re") {
		p.Update(k)
	}
	stale := debounceMsg{id: p.id, seq: p.debSeq}
	p.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	if cmd := p.Update(stale); cmd != nil {
		t.Fatal("stale debounce tick should not search")
	}
	if p.load.Seq != 0 {
		t.Fatal("no request expected")
	}

	// A request in flight, then the user clears the field: ignore the result.
	for _, k := range uitest.KeyMsgs("Rezn") {
		p.Update(k)
	}
	p.Update(debounceMsg{id: p.id, seq: p.debSeq})
	if !p.load.Loading {
		t.Fatal("debounce should start a search")
	}
	seq := p.load.Seq
	p.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	p.Update(ui.Result[hits]{ID: p.id, Seq: seq, Data: hits{q: "Rezn", items: []api.Person{{FullName: "Late"}}}})
	if len(p.results) != 0 {
		t.Fatal("results after clearing must be ignored")
	}
	if strings.Contains(ansi.Strip(p.View(100, 30)), "Late") {
		t.Fatal("late results rendered")
	}
}

func TestErrorsKeepResultsAndSelection(t *testing.T) {
	p, h, _ := newPage(t)
	h.Key("Rezn", "enter", "j", "j")
	sel, _ := p.selected()
	// Refresh with reordered data: selection follows the person.
	items := append([]api.Person(nil), p.results...)
	items[0], items[2] = items[2], items[0]
	h.Send(ui.Result[hits]{ID: p.id, Seq: p.load.Begin(), Data: hits{q: "Rezn", items: items}})
	if now, _ := p.selected(); now.key() != sel.key() {
		t.Fatalf("selection moved: %s → %s", sel.key(), now.key())
	}
	// Rate limited: previous results stay, error is shown inline. (Update,
	// not Send: the harness would run the automatic retry.)
	p.Update(ui.Result[hits]{ID: p.id, Seq: p.load.Begin(), Err: &api.RateLimitError{}})
	v := h.View(100, 30)
	if !strings.Contains(v, "Reznichenko") || !strings.Contains(v, "Rate limited") {
		t.Fatalf("error should be inline without wiping results:\n%s", v)
	}
	checkSizes(t, p)
}

func TestNoMatchesAndFirstError(t *testing.T) {
	p, h, _ := newPage(t)
	h.Key("zzzz")
	v := h.View(100, 30)
	if !strings.Contains(v, "No matches for “zzzz”") || !strings.Contains(v, "0 results") {
		t.Fatalf("expected no-matches state:\n%s", v)
	}
	h.Send(ui.Result[hits]{ID: p.id, Seq: p.load.Begin(), Err: &api.NetworkError{Err: errString("offline")}})
	v = h.View(100, 30)
	if !strings.Contains(v, "Can't reach") {
		t.Fatalf("expected error state:\n%s", v)
	}
	checkSizes(t, p)
}

type errString string

func (e errString) Error() string { return string(e) }

func TestCyrillicGroupsAndRooms(t *testing.T) {
	p, h, _ := newPage(t)
	h.Key("БИБ")
	v := h.View(100, 30)
	if !strings.Contains(v, "БИБ255") || !strings.Contains(v, "GRP") {
		t.Fatalf("group search failed:\n%s", v)
	}
	h.Key("enter")
	for i, r := range p.rows {
		if r.person.IsGroup() {
			p.list.Select(i)
			break
		}
	}
	h.Key("y")
	if st := h.Statuses(); len(st) == 0 || !strings.Contains(st[len(st)-1], "group id") {
		t.Fatalf("y on a group should copy its id: %v", st)
	}
	h.Key("i", "ctrl+u", "R6", "enter")
	v = h.View(100, 30)
	if !strings.Contains(v, "R615") || !strings.Contains(v, "open map") {
		t.Fatalf("room search failed:\n%s", v)
	}
	h.Key("m")
	if st := h.Statuses(); !strings.Contains(st[len(st)-1], "yandex.ru/maps") {
		t.Fatalf("m should open the map: %v", st)
	}
	h.Key(".")
	m, ok := h.LastMenu()
	if !ok || len(m.Actions) != 4 {
		t.Fatalf("room menu should have open/star/copy/map: %+v", m)
	}
	if !h.RunAction("star") || !p.starred(p.rows[p.list.Cursor]) {
		t.Fatal("star action failed")
	}
	h.Key("enter")
	if tg, ok := h.LastTarget(); !ok || tg.Kind != config.KindAuditorium || tg.Key != "ruz1033" {
		t.Fatalf("enter on room: %+v", tg)
	}
	checkSizes(t, p)
}

func TestPersonWithoutEmail(t *testing.T) {
	p, h, _ := newPage(t)
	h.Key("ab", "esc")
	long := strings.Repeat("Очень-длинное-имя ", 20)
	h.Send(ui.Result[hits]{ID: p.id, Seq: p.load.Begin(), Data: hits{q: "ab", items: []api.Person{
		{FullName: long + "\x1b[31mred", Type: "STUDENT"},
		{Type: "MYSTERY", ID: "x1"},
		{Type: "AUDITORIUM", ID: "r1", Room: "101"},
	}}})
	h.Key("enter")
	if st := h.Statuses(); len(st) == 0 || !strings.Contains(st[len(st)-1], "no email") {
		t.Fatalf("expected a warning: %v", st)
	}
	h.Key("f")
	h.Key("j", "j", "m")
	if st := h.Statuses(); !strings.Contains(st[len(st)-1], "No location") {
		t.Fatalf("expected no-location warning: %v", st)
	}
	v := checkSize(t, p, 100, 30)
	if strings.Contains(v, "\x1b[31m") {
		t.Fatal("escape sequences must be stripped")
	}
	checkSizes(t, p)
}

func TestBannerDismiss(t *testing.T) {
	p, h, ctx := newPage(t)
	if !strings.Contains(h.View(100, 30), "HSE SmartPoint services got better") {
		t.Fatalf("banner missing:\n%s", h.View(100, 30))
	}
	h.Key("x") // typed into the field while focused
	if p.input.Value() != "x" {
		t.Fatalf("x should type while the field is focused, got %q", p.input.Value())
	}
	h.Key("ctrl+u", "esc", "x")
	if strings.Contains(h.View(100, 30), "SmartPoint") {
		t.Fatal("x should dismiss the banner")
	}
	if !ctx.Settings.BannerDismissed("6ab3395b85ef4e9464628df4") {
		t.Fatal("dismissal not stored")
	}
}

func TestServerFavouritesDeduped(t *testing.T) {
	p, h, ctx := newPage(t)
	ctx.Favs.Toggle(config.Favourite{Kind: config.KindPerson, Key: "a@hse.ru", Title: "A Local"})
	h.Send(ui.Result[serverFavs]{ID: p.id, Seq: p.favLoad.Begin(), Data: serverFavs{
		{FullName: "A Server", Email: "A@hse.ru", Type: "STAFF"},
		{FullName: "B Server", Email: "b@edu.hse.ru", Type: "STUDENT"},
		{FullName: "No Email", Type: "STAFF"},
	}})
	v := h.View(100, 30)
	if !strings.Contains(v, "A Local") || strings.Contains(v, "A Server") || !strings.Contains(v, "B Server") || strings.Contains(v, "No Email") {
		t.Fatalf("server favourites not merged:\n%s", v)
	}
	h.Key("esc", "j", "j", "j", "enter")
	if tg, ok := h.LastTarget(); !ok || tg.Key != "b@edu.hse.ru" {
		t.Fatalf("open server favourite: %+v", tg)
	}
	h.Key("g", "enter")
	if tg, _ := h.LastTarget(); tg.Key != api.DemoEmail || !strings.HasPrefix(tg.Title, "Me · ") {
		t.Fatalf("Me row should open my timetable: %+v", tg)
	}
}

func TestReloadAndRefresh(t *testing.T) {
	p, h, _ := newPage(t)
	h.Key("Rezn")
	seq := p.load.Seq
	h.Send(ui.ReloadMsg{})
	if p.load.Seq == seq || len(p.results) != 12 {
		t.Fatal("reload should refetch the results")
	}
	h.Key("esc", "r")
	if p.load.Seq != seq+2 {
		t.Fatal("r should refetch")
	}
}

func TestInputEditing(t *testing.T) {
	in := input{Prompt: "/ "}
	in.Focus()
	for _, k := range uitest.KeyMsgs("hello world") {
		in.Update(k)
	}
	in.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	if in.Value() != "hello " {
		t.Fatalf("ctrl+w: %q", in.Value())
	}
	in.Update(tea.KeyMsg{Type: tea.KeyLeft})
	in.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if in.Value() != "hell " {
		t.Fatalf("backspace: %q", in.Value())
	}
	in.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a\nb\x1b"), Paste: true})
	if in.Value() != "hella b " {
		t.Fatalf("paste: %q", in.Value())
	}
	in.SetValue(strings.Repeat("Ж", 100))
	for _, w := range []int{1, 2, 3, 10, 40} {
		if got := ansi.StringWidth(in.View(w, ui.StyleKey)); got > w {
			t.Fatalf("view width %d > %d", got, w)
		}
	}
	if !strings.HasSuffix(ansi.Strip(in.View(10, ui.StyleKey)), "Ж ") {
		t.Fatalf("cursor at the end should stay visible: %q", ansi.Strip(in.View(10, ui.StyleKey)))
	}
}
