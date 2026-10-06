package search

import (
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"hse-app-z/internal/api"
	"hse-app-z/internal/ui"
	"hse-app-z/internal/ui/uitest"
)

// The harness runs debounce ticks synchronously: keep them short so the
// behaviour tests don't sleep. Pacing tests set their own values.
func TestMain(m *testing.M) {
	debounce, minGap = time.Millisecond, 0
	os.Exit(m.Run())
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

// pacing installs a controllable clock and the production pacing values.
func pacing(t *testing.T) *fakeClock {
	t.Helper()
	c := &fakeClock{t: time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)}
	oldClock, oldDeb, oldGap := clock, debounce, minGap
	clock = func() time.Time { return c.t }
	debounce, minGap = 300*time.Millisecond, time.Second
	t.Cleanup(func() { clock, debounce, minGap = oldClock, oldDeb, oldGap })
	return c
}

func runes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

// typeSlowly types s one rune at a time, gap apart, firing each
// keystroke's debounce tick on time (as the runtime would). It returns
// the number of search requests sent.
func typeSlowly(p *Page, c *fakeClock, s string, gap time.Duration) int {
	before := p.load.Seq
	for _, r := range s {
		p.Update(runes(string(r)))
		if gap > debounce {
			c.advance(debounce)
			p.Update(debounceMsg{id: p.id, seq: p.debSeq})
			c.advance(gap - debounce)
		} else {
			c.advance(gap)
		}
	}
	// The user stops typing: the pending tick (possibly pushed back by
	// the pacing) eventually fires.
	for i := 0; i < 3 && p.dirty; i++ {
		c.advance(max(debounce, p.holdOff()))
		p.Update(debounceMsg{id: p.id, seq: p.debSeq})
	}
	return p.load.Seq - before
}

func TestSlowTypingDoesNotSearchPerKeystroke(t *testing.T) {
	c := pacing(t)
	name := "Константинопольский"

	// Fast typing: one request once the user pauses.
	p := New(uitest.Ctx(t)).(*Page)
	p.Init()
	p.Update(ui.FocusSearchMsg{})
	if n := typeSlowly(p, c, name, 120*time.Millisecond); n != 1 || p.inflight != name {
		t.Fatalf("fast typing: %d requests, last %q", n, p.inflight)
	}

	// Slow typing (a pause longer than the debounce after every key):
	// requests are spaced at least minGap apart instead of one per key.
	p = New(uitest.Ctx(t)).(*Page)
	p.Init()
	p.Update(ui.FocusSearchMsg{})
	c.advance(time.Minute)
	n := typeSlowly(p, c, name, 450*time.Millisecond)
	if n > 10 {
		t.Errorf("slow typing of %d runes sent %d requests", len([]rune(name)), n)
	}
	if p.inflight != name {
		t.Errorf("the full name must still be searched, last request %q", p.inflight)
	}
	t.Logf("slow typing: %d requests for %d keystrokes", n, len([]rune(name)))
}

func TestRateLimitPausesTypingAndRetriesOnce(t *testing.T) {
	c := pacing(t)
	p := New(uitest.Ctx(t)).(*Page)
	p.Init()
	p.Update(ui.FocusSearchMsg{})
	typeSlowly(p, c, "Ivanov", 50*time.Millisecond)
	if !p.load.Loading || p.inflight != "Ivanov" {
		t.Fatalf("expected a search in flight, got %q", p.inflight)
	}
	retry := p.Update(ui.Result[hits]{ID: p.id, Seq: p.load.Seq, Err: &api.RateLimitError{RetryAfter: 30 * time.Second}})
	if retry == nil || !p.held() {
		t.Fatal("a 429 should schedule a retry after the pause")
	}
	if v := ansi.Strip(p.View(100, 20)); !strings.Contains(v, "Rate limited · will retry") || !strings.Contains(v, "Too many requests") {
		t.Fatalf("the pause should be visible:\n%s", v)
	}

	// Typing during the pause doesn't hit the server.
	seq := p.load.Seq
	p.Update(runes("a"))
	for i := 0; i < 5; i++ {
		c.advance(time.Second)
		p.Update(debounceMsg{id: p.id, seq: p.debSeq})
	}
	if p.load.Seq != seq {
		t.Fatalf("searched during the rate-limit pause")
	}
	// After the pause the pending query goes out by itself.
	c.advance(26 * time.Second)
	p.Update(debounceMsg{id: p.id, seq: p.debSeq})
	if p.load.Seq != seq+1 || p.inflight != "Ivanova" {
		t.Fatalf("expected the held search after the pause: seq %d→%d %q", seq, p.load.Seq, p.inflight)
	}

	// Still limited: one automatic retry, then it's up to the user.
	if p.Update(ui.Result[hits]{ID: p.id, Seq: p.load.Seq, Err: &api.RateLimitError{}}) == nil {
		t.Fatal("first failure of a user search should be retried")
	}
	c.advance(rateLimitWait)
	p.Update(debounceMsg{id: p.id, seq: p.debSeq})
	if p.load.Seq != seq+2 {
		t.Fatal("retry not sent")
	}
	if cmd := p.Update(ui.Result[hits]{ID: p.id, Seq: p.load.Seq, Err: &api.RateLimitError{}}); cmd != nil || p.dirty {
		t.Fatal("the retry failing must not loop")
	}
	v := ansi.Strip(p.View(100, 20))
	if !strings.Contains(v, "press enter to retry") || strings.Contains(v, "press r to retry") {
		t.Fatalf("while typing, r would be typed into the field:\n%s", v)
	}
	// enter retries at once, pause or not.
	p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if p.load.Seq != seq+3 {
		t.Fatal("enter should retry immediately")
	}
	// A cached fallback while limited also pauses typing.
	p.Update(ui.Result[hits]{ID: p.id, Seq: p.load.Seq, Data: hits{q: "Ivanova"}, Meta: api.Meta{Stale: true, StaleReason: "rate limited"}})
	if p.holdOff() <= minGap {
		t.Fatal("stale data served because of a 429 should pause typing too")
	}
}

func TestErrorHintMatchesFocus(t *testing.T) {
	p, h, _ := newPage(t)
	h.Key("zzzz")
	p.Update(ui.Result[hits]{ID: p.id, Seq: p.load.Begin(), Err: &api.NetworkError{Err: errString("offline")}})
	if v := h.View(100, 30); !strings.Contains(v, "press enter to retry") {
		t.Fatalf("focused: \n%s", v)
	}
	h.Key("esc")
	if v := h.View(100, 30); !strings.Contains(v, "press r to retry") {
		t.Fatalf("list: \n%s", v)
	}
	h.Key("r")
	if p.load.Err != nil || !strings.Contains(h.View(100, 30), "No matches") {
		t.Fatalf("r should retry:\n%s", h.View(100, 30))
	}
}

func TestFocusedPreviewHidesListKeys(t *testing.T) {
	p, h, _ := newPage(t)
	h.Key("Rezn")
	if v := h.View(120, 30); strings.Contains(v, "f star") || strings.Contains(v, "enter open timetable") {
		t.Fatalf("list keys advertised while typing (f would be typed):\n%s", v)
	}
	h.Key("enter")
	if p.Capturing() {
		t.Fatal("enter should leave the field")
	}
	if v := h.View(120, 30); !strings.Contains(v, "f star") {
		t.Fatalf("list keys should be shown in the list:\n%s", v)
	}
}

func TestDismissHintOnlyWithDismissibleBanner(t *testing.T) {
	p, h, _ := newPage(t)
	h.Key("esc")
	has := func() bool {
		for _, hn := range p.Hints() {
			if hn.Key == "x" {
				return true
			}
		}
		return false
	}
	if !has() {
		t.Fatal("dismissible banner shown: x should be hinted")
	}
	h.Key("x")
	if has() {
		t.Fatal("x hinted with nothing to dismiss")
	}
	h.Send(ui.Result[bannerList]{ID: p.id, Seq: p.banLoad.Begin(), Data: bannerList{{ID: "fixed", Title: "Read me"}}})
	if !strings.Contains(h.View(100, 30), "Read me") || has() {
		t.Fatal("non-dismissible banner: shown, but x not hinted")
	}
}

// rootTakes mirrors app.Model.handleKey on a tab root: the keys the root
// handles itself unless the page captures input.
func rootTakes(p ui.Page, key string) bool {
	if key == "ctrl+c" {
		return true
	}
	if p.Capturing() {
		return false
	}
	switch key {
	case "q", "tab", "shift+tab", "?", "L", "1", "2", "3", "4", "5", "6", "7", "8", "9":
		return true
	}
	return false // esc/backspace reach a tab root; / refocuses search
}

func TestEscAlwaysLeavesTheField(t *testing.T) {
	p, h, _ := newPage(t)
	states := []func(){
		func() {},
		func() { h.Key("R") },
		func() { h.Key("zzzz") },
		func() { p.Update(runes("Ivan")) }, // debounce pending
		func() {
			p.Update(ui.Result[hits]{ID: p.id, Seq: p.load.Begin(), Err: &api.RateLimitError{}})
		},
	}
	for i, setup := range states {
		h.Send(ui.FocusSearchMsg{})
		setup()
		if !p.Capturing() {
			t.Fatalf("state %d: expected focus", i)
		}
		for _, hn := range p.Hints() {
			if rootTakes(p, hn.Key) {
				t.Errorf("state %d: hint %q is taken by the root", i, hn.Key)
			}
		}
		h.Key("esc")
		if p.Capturing() {
			t.Fatalf("state %d: esc must leave the field", i)
		}
		for _, hn := range p.Hints() {
			for _, k := range strings.Fields(hn.Key) {
				if k != "/" && rootTakes(p, k) {
					t.Errorf("state %d: list hint %q is taken by the root", i, k)
				}
			}
		}
	}
}

func TestTabLeavesFieldEvenWithoutResults(t *testing.T) {
	p, h, _ := newPage(t)
	h.Key("zzzz")
	if p.list.HasSelection() {
		t.Fatal("expected no matches")
	}
	h.Key("tab")
	if p.Capturing() {
		t.Fatal("tab should leave the field so the next tab switches tabs")
	}
}

func TestNoMeRowWithoutEmail(t *testing.T) {
	ctx := uitest.Ctx(t)
	ctx.Me = ui.Me{Name: "Nameless"}
	p := New(ctx).(*Page)
	h := uitest.New(t, p)
	h.Send(ui.FocusSearchMsg{})
	v := h.View(100, 30)
	if strings.Contains(v, "Me ·") || !strings.Contains(v, "Star people") {
		t.Fatalf("no Me row without an email:\n%s", v)
	}
	h.Key("enter") // nothing selectable: stays in the field, no panic
	h.Key("esc", "enter", "f", "y", "m", ".", "j", "G")
	if _, ok := h.LastTarget(); ok {
		t.Fatal("nothing to open")
	}
	checkSizes(t, p)
}

func TestInputWideCursorStaysVisible(t *testing.T) {
	in := input{Prompt: "/ "}
	in.Focus()
	in.SetValue("abcdefghi世")
	in.pos = 9 // on 世, which ends exactly at the right edge
	v := in.View(12, ui.StyleKey)
	if !strings.Contains(ansi.Strip(v), "世") || ansi.StringWidth(v) > 12 {
		t.Fatalf("cursor rune cut off: %q", ansi.Strip(v))
	}
	// Distinct runes, so finding the rune under the cursor in the output
	// proves the cursor cell is drawn (styles aren't rendered in tests).
	for _, s := range []string{"一二三四五六七八九十百千", "🍕🍔🍟🌭🍿🥓🥚🍳🧇", "АБВГДЕЁЖЗИЙКЛМНОПР", "a一b二c三d四"} {
		in.SetValue(s)
		for pos := 0; pos <= len(in.value); pos++ {
			in.pos = pos
			for w := 5; w <= 14; w++ {
				v := in.View(w, ui.StyleKey)
				if ansi.StringWidth(v) > w {
					t.Fatalf("%q pos %d: width %d > %d", s, pos, ansi.StringWidth(v), w)
				}
				plain := ansi.Strip(v)
				if pos < len(in.value) && !strings.Contains(plain, string(in.value[pos])) {
					t.Fatalf("%q pos %d w %d: cursor rune missing in %q", s, pos, w, plain)
				}
				if pos == len(in.value) && !strings.HasSuffix(plain, " ") {
					t.Fatalf("%q end w %d: cursor cell missing in %q", s, w, plain)
				}
			}
		}
	}
}

func TestInputPasteDropsEscapeSequences(t *testing.T) {
	in := input{}
	in.Focus()
	in.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("\x1b[31mIvanov\x1b[0m\r\nPetr\x1b"), Paste: true})
	if in.Value() != "Ivanov Petr" {
		t.Fatalf("paste: %q", in.Value())
	}
	// ctrl+w / alt+backspace at the start, word moves with Cyrillic.
	in.SetValue("Иванов Пётр")
	in.Update(tea.KeyMsg{Type: tea.KeyCtrlLeft})
	in.Update(tea.KeyMsg{Type: tea.KeyBackspace, Alt: true})
	if in.Value() != "Пётр" || in.pos != 0 {
		t.Fatalf("alt+backspace: %q %d", in.Value(), in.pos)
	}
	in.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	if in.Value() != "Пётр" {
		t.Fatalf("ctrl+w at start: %q", in.Value())
	}
}

func TestRoomWithPlaceholderLocationHasNoMap(t *testing.T) {
	p, h, _ := newPage(t)
	h.Key("ab", "esc")
	h.Send(ui.Result[hits]{ID: p.id, Seq: p.load.Begin(), Data: hits{q: "ab", items: []api.Person{
		{Type: "AUDITORIUM", ID: "r1", Room: "101", AuditoriumLocation: &api.GeoPoint{Coordinates: []float64{0, 0}}},
	}}})
	if v := h.View(100, 30); strings.Contains(v, "open map") || strings.Contains(v, "m map") {
		t.Fatalf("map advertised for 0,0:\n%s", v)
	}
	h.Key("m")
	if st := h.Statuses(); len(st) == 0 || !strings.Contains(st[len(st)-1], "No location") {
		t.Fatalf("m: %v", st)
	}
}
