package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"hse-app-z/internal/api"
)

// fits fails when s has more than h lines or a line wider than w cells.
func fits(t *testing.T, what, s string, w, h int) {
	t.Helper()
	lines := strings.Split(s, "\n")
	if h > 0 && len(lines) > h {
		t.Errorf("%s: %d lines, want ≤ %d\n%s", what, len(lines), h, ansi.Strip(s))
	}
	for i, l := range lines {
		if n := ansi.StringWidth(l); n > w {
			t.Errorf("%s: line %d is %d cells (max %d): %q", what, i, n, w, ansi.Strip(l))
		}
	}
}

func TestSafeURL(t *testing.T) {
	ok := []string{"https://zoom.us/j/1?pwd=x", "http://hse.ru", " HTTPS://EX.COM/a ", "mailto:a@hse.ru", "tel:+74957729590", "https://ex.com/a%20b"}
	bad := []string{
		"javascript:alert(1)", "JaVaScRiPt:alert(1)", "file:///etc/passwd", "data:text/html,x", "myapp://x",
		"http:///x", "//evil.com/x", "https:evil.com", "mailto:", "tel:", "", "   ",
		"https://ex.com/\x07x", "https://ex.com/\x1b[31m", "https://ex.com/\u0085x", "https://ex.com/\u009b31m",
		"https://ex.com/‮x", "https://‮example.com", "https://ex.com/⁦x", "https://ex.com/​x",
		"https://ex.com/ x", "https://ex.com/\xff",
	}
	for _, u := range ok {
		if !SafeURL(u) {
			t.Errorf("SafeURL(%q) = false, want true", u)
		}
	}
	for _, u := range bad {
		if SafeURL(u) {
			t.Errorf("SafeURL(%q) = true, want false", u)
		}
	}
}

func TestClean(t *testing.T) {
	cases := map[string]string{
		"a‮b":                                "ab", // right-to-left override
		"a⁦b⁩c":                              "abc",
		"x\x1b[31my\x1b[0m":                  "xy",
		"x\x1b]8;;http://e\x07y\x1b]8;;\x07": "xy",
		"\x1b[?1049h":                        "",
		"a b\tc\r\nd":                        "a b c d",
		"⠀":                                  "",
		"ㅤ":                                  "", // Hangul filler (invisible)
		"a b​c":                              "a bc",
		"Очень длинное":                      "Очень длинное",
	}
	for in, want := range cases {
		if got := Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
	if got := CleanMulti("a\x1b[1m\n\n\n‮b\n"); got != "a\n\nb" {
		t.Errorf("CleanMulti = %q", got)
	}
}

func TestKindShort(t *testing.T) {
	for in, want := range map[string]string{
		"Lecture": "LEC", "Семинар": "SEM", "": "   ", "  ": "   ", "Мероприятие": "МЕР",
		"Курсовая работа": "КУР", "ab": "AB ", "\x1b[31mfoo": "FOO", "\x1b]0;x\x07": "   ", "講義": "講 ",
	} {
		got := KindShort(in)
		if got != want {
			t.Errorf("KindShort(%q) = %q, want %q", in, got, want)
		}
		if Width(got) != 3 {
			t.Errorf("KindShort(%q) is %d cells wide, want 3", in, Width(got))
		}
	}
}

func TestOverlayWideChars(t *testing.T) {
	row := strings.Repeat("中文", 30)
	base := strings.Join([]string{row, "a" + row, row, "a" + row}, "\n")
	box := Box("T", "hello", 10) // 12 cells wide
	for _, w := range []int{40, 41, 42, 43} {
		out := Overlay(base, box, w, 4)
		fits(t, fmt.Sprintf("overlay %d", w), out, w, 4)
		left := (w - 12) / 2
		for i, l := range strings.Split(out, "\n") {
			plain := ansi.Strip(l)
			if got := ansi.StringWidth(plain); got != w {
				t.Errorf("w=%d line %d: %d cells, want %d: %q", w, i, got, w, plain)
			}
			// The box must start at the same column on every row.
			if i < 3 {
				prefix := ansi.Truncate(plain, left, "")
				rest := ansi.TruncateLeft(plain, left, "")
				if ansi.StringWidth(prefix) != left || !strings.ContainsAny(firstRune(rest), "╭│╰") {
					t.Errorf("w=%d line %d: box not at column %d: %q", w, i, left, plain)
				}
			}
		}
	}
}

func firstRune(s string) string {
	for _, r := range s {
		return string(r)
	}
	return ""
}

func helpSections(n int) []HelpSection {
	var page, global []Hint
	for i := 0; i < n; i++ {
		page = append(page, Hint{Key: fmt.Sprintf("k%d", i), Desc: "switch language (English / Русский) and more words"})
	}
	global = []Hint{
		{Key: "1-8 / tab", Desc: "switch tab (shift+tab back)"},
		{Key: "ctrl+d/ctrl+u", Desc: "half page down / up"},
		{Key: "L", Desc: "switch language (English / Русский)"},
	}
	return []HelpSection{{Title: "Schedule", Hints: page}, {Title: "Everywhere", Hints: global}}
}

func TestHelpViewFits(t *testing.T) {
	for _, sz := range [][2]int{{60, 10}, {60, 9}, {80, 20}, {80, 19}, {100, 28}, {200, 60}, {66, 12}} {
		for _, n := range []int{2, 11, 30} {
			v := HelpView(helpSections(n), sz[0], sz[1])
			fits(t, fmt.Sprintf("help %dx%d n=%d", sz[0], sz[1], n), v, sz[0], sz[1])
			if !strings.Contains(v, "press any key") {
				t.Errorf("help %dx%d n=%d lost its footer", sz[0], sz[1], n)
			}
		}
	}
	// Keys are never cut: they are the point of the overlay.
	v := ansi.Strip(HelpView(helpSections(11), 80, 20))
	if !strings.Contains(v, "ctrl+d/ctrl+u") {
		t.Errorf("key truncated in the help overlay:\n%s", v)
	}
}

func TestMenuView(t *testing.T) {
	var acts []Action
	for i := 0; i < 12; i++ {
		acts = append(acts, Action{Label: fmt.Sprintf("Subordinate %d", i), Run: func() tea.Cmd { return nil }})
	}
	m := NewMenu("Title \x1b[31mred\x1b[0m "+strings.Repeat("long ", 30), acts)
	for _, sz := range [][2]int{{60, 10}, {60, 9}, {110, 30}} {
		v := m.View(sz[0], sz[1])
		fits(t, fmt.Sprintf("menu %dx%d", sz[0], sz[1]), v, sz[0]-2, sz[1]-2)
		if strings.Contains(v, "\x1b[31m") {
			t.Errorf("menu title not sanitised")
		}
	}
	// A clipped menu says there is more.
	if v := ansi.Strip(m.View(60, 10)); !strings.Contains(v, "1/12") {
		t.Errorf("clipped menu gives no hint that more actions exist:\n%s", v)
	}
}

func TestListAllHeaders(t *testing.T) {
	var l List
	l.Skip = func(int) bool { return true }
	l.SetLen(3)
	for _, k := range []string{"j", "k", "G", "g", "ctrl+d", "pgup"} {
		l.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
		if l.HasSelection() {
			t.Fatalf("%s: header row selectable", k)
		}
	}
	out := l.Render(10, 2, func(i int, sel bool, w int) string {
		if sel {
			t.Errorf("header rendered as selected")
		}
		return HeaderRow("h", w)
	})
	fits(t, "list", out, 10, 2)
}

func TestScrollClamp(t *testing.T) {
	var s Scroll
	content := strings.Repeat("line\n", 20)
	s.Render(content, 10, 5)
	for i := 0; i < 50; i++ {
		s.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("J")})
	}
	out := s.Render(content, 10, 5)
	fits(t, "scroll", out, 10, 5)
	if s.Offset != 15 {
		t.Errorf("offset %d, want 15", s.Offset)
	}
	// Content shrinks (selection changed without a reset): still in range.
	out = s.Render("short", 10, 5)
	if s.Offset != 0 || !strings.HasPrefix(out, "short") {
		t.Errorf("offset %d after shrinking: %q", s.Offset, out)
	}
	s.Render(content, 0, 0)
	s.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("K")})
	if s.Offset < 0 {
		t.Errorf("negative offset")
	}
}

// Error text can carry the server's message: it is sanitised and kept
// on one line in pane headers and placeholders.
func TestErrorTextSanitised(t *testing.T) {
	err := &api.APIError{Status: 404, Message: "no\x1b[2J such\nperson‮"}
	l := Load{Err: err, Loaded: true}
	note := LoadNote(l)
	if strings.Contains(note, "\x1b[2J") || strings.Contains(note, "\n") || strings.Contains(note, "‮") {
		t.Errorf("LoadNote leaks server text: %q", note)
	}
	fits(t, "pane title", PaneTitle("Week", note, 40), 40, 1)
	v := StateView(40, 6, Load{Err: err}, 0, "empty")
	if strings.Contains(v, "\x1b[2J") || strings.Contains(v, "‮") {
		t.Errorf("StateView leaks server text: %q", v)
	}
	fits(t, "state view", v, 40, 6)
}
