package search

import (
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"hse-app-z/internal/ui"
)

// input is a minimal single-line text field with readline-style keys.
//
// It mirrors the subset of bubbles/textinput this page needs. textinput
// itself can't be imported yet: its clipboard dependency
// (github.com/atotto/clipboard) has no go.sum entry in this module.
type input struct {
	Prompt      string
	Placeholder string
	Limit       int // max runes; 0 = unlimited

	value   []rune
	pos     int
	focused bool
}

var cursorStyle = lipgloss.NewStyle().Reverse(true)

func (in *input) Value() string { return string(in.value) }

// SetValue replaces the text and moves the cursor to the end.
func (in *input) SetValue(s string) {
	in.value, in.pos = nil, 0
	in.insert([]rune(s))
	in.pos = len(in.value)
}

func (in *input) Focused() bool { return in.focused }
func (in *input) Focus()        { in.focused = true }
func (in *input) Blur()         { in.focused = false }
func (in *input) CursorEnd()    { in.pos = len(in.value) }

// insert adds user text at the cursor, dropping control characters and
// whole escape sequences (a paste of styled text must not leave "[31m"
// behind); newlines and tabs from pastes become spaces.
func (in *input) insert(rs []rune) {
	s := string(rs)
	if strings.ContainsRune(s, '\x1b') {
		s = ansi.Strip(s)
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	var clean []rune
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t' || r == '\r':
			clean = append(clean, ' ')
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r):
		default:
			clean = append(clean, r)
		}
	}
	if in.Limit > 0 && len(in.value)+len(clean) > in.Limit {
		clean = clean[:max(0, in.Limit-len(in.value))]
	}
	if len(clean) == 0 {
		return
	}
	v := make([]rune, 0, len(in.value)+len(clean))
	v = append(v, in.value[:in.pos]...)
	v = append(v, clean...)
	v = append(v, in.value[in.pos:]...)
	in.value = v
	in.pos += len(clean)
}

func (in *input) wordLeft() int {
	i := in.pos
	for i > 0 && unicode.IsSpace(in.value[i-1]) {
		i--
	}
	for i > 0 && !unicode.IsSpace(in.value[i-1]) {
		i--
	}
	return i
}

func (in *input) wordRight() int {
	i := in.pos
	for i < len(in.value) && unicode.IsSpace(in.value[i]) {
		i++
	}
	for i < len(in.value) && !unicode.IsSpace(in.value[i]) {
		i++
	}
	return i
}

func (in *input) cut(from, to int) {
	if from < 0 || to > len(in.value) || from >= to {
		return
	}
	in.value = append(in.value[:from:from], in.value[to:]...)
	in.pos = from
}

// Update applies an editing key. It reports whether the key was used.
func (in *input) Update(msg tea.KeyMsg) bool {
	if !in.focused {
		return false
	}
	switch msg.Type {
	case tea.KeyRunes:
		if msg.Alt && !msg.Paste {
			break
		}
		in.insert(msg.Runes)
		return true
	case tea.KeySpace:
		in.insert([]rune{' '})
		return true
	}
	switch msg.String() {
	case "backspace", "ctrl+h":
		in.cut(in.pos-1, in.pos)
	case "delete", "ctrl+d":
		in.cut(in.pos, in.pos+1)
		in.pos = min(in.pos, len(in.value))
	case "left", "ctrl+b":
		in.pos = max(0, in.pos-1)
	case "right", "ctrl+f":
		in.pos = min(len(in.value), in.pos+1)
	case "home", "ctrl+a":
		in.pos = 0
	case "end", "ctrl+e":
		in.pos = len(in.value)
	case "ctrl+u":
		in.cut(0, in.pos)
	case "ctrl+k":
		in.value = in.value[:in.pos]
	case "ctrl+w", "alt+backspace":
		in.cut(in.wordLeft(), in.pos)
	case "alt+d", "alt+delete":
		p := in.pos
		in.cut(p, in.wordRight())
		in.pos = p
	case "alt+left", "ctrl+left", "alt+b":
		in.pos = in.wordLeft()
	case "alt+right", "ctrl+right", "alt+f":
		in.pos = in.wordRight()
	default:
		return false
	}
	return true
}

// View renders prompt and text into at most width cells, scrolling
// horizontally so the cursor stays visible.
func (in *input) View(width int, promptStyle lipgloss.Style) string {
	if width <= 0 {
		return ""
	}
	prompt := ui.Trunc(in.Prompt, width)
	out := promptStyle.Render(prompt)
	avail := width - ui.Width(prompt)
	if avail <= 0 {
		return out
	}
	if len(in.value) == 0 {
		ph := []rune(in.Placeholder)
		if !in.focused || len(ph) == 0 {
			return out + ui.StyleDim.Render(ui.Trunc(in.Placeholder, avail))
		}
		return out + cursorStyle.Render(string(ph[0])) + ui.StyleDim.Render(ui.Trunc(string(ph[1:]), avail-ui.Width(string(ph[0]))))
	}
	// Leave room for the cursor cell (two cells on a wide rune, or it
	// would be cut off at the right edge); drop runes from the left until
	// the text before the cursor fits.
	curW := 1
	if in.pos < len(in.value) {
		curW = max(1, ui.Width(string(in.value[in.pos])))
	}
	start := 0
	for start < in.pos && ui.Width(string(in.value[start:in.pos])) > avail-curW {
		start++
	}
	var b strings.Builder
	used := 0
	for i := start; i <= len(in.value); i++ {
		ch := " "
		if i < len(in.value) {
			ch = string(in.value[i])
		} else if !in.focused || i != in.pos {
			break
		}
		w := ui.Width(ch)
		if used+w > avail {
			break
		}
		used += w
		if in.focused && i == in.pos {
			b.WriteString(cursorStyle.Render(ch))
		} else {
			b.WriteString(ch)
		}
	}
	return out + b.String()
}
