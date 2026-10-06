package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Action is one entry of the action menu (opened with enter on most rows).
type Action struct {
	// Key is an optional one-character shortcut, also usable directly on
	// the page if the page binds it.
	Key   string
	Label string
	Run   func() tea.Cmd
}

// Menu is the modal action picker drawn by the root over the page.
type Menu struct {
	Title   string
	Actions []Action
	list    List
}

// NewMenu builds a menu; nil Run entries are dropped.
func NewMenu(title string, actions []Action) *Menu {
	m := &Menu{Title: title}
	for _, a := range actions {
		if a.Run != nil {
			m.Actions = append(m.Actions, a)
		}
	}
	m.list.SetLen(len(m.Actions))
	return m
}

// Update handles a key. done is true when the menu should close; cmd is
// the chosen action's command.
func (m *Menu) Update(msg tea.KeyMsg) (cmd tea.Cmd, done bool) {
	switch msg.String() {
	case "esc", "q", "backspace", "left", "h":
		return nil, true
	case "enter", "right", "l", " ":
		if m.list.HasSelection() {
			return m.Actions[m.list.Cursor].Run(), true
		}
		return nil, true
	}
	if m.list.HandleKey(msg) {
		return nil, false
	}
	k := msg.String()
	for _, a := range m.Actions {
		if a.Key != "" && a.Key == k {
			return a.Run(), true
		}
	}
	return nil, false
}

// View renders the menu box for a screen of the given width.
func (m *Menu) View(width, height int) string {
	title := Clean(m.Title)
	w := 20
	for _, a := range m.Actions {
		w = max(w, Width(a.Label)+6)
	}
	w = max(w, Width(title)+2)
	if w > width-6 {
		w = width - 6
	}
	h := min(len(m.Actions), max(1, height-6))
	body := m.list.Render(w, h, func(i int, sel bool, w int) string {
		a := m.Actions[i]
		key := "  "
		if a.Key != "" {
			key = StyleKey.Render(a.Key) + " "
		}
		return Row(sel, key+Clean(a.Label), w)
	})
	hint := Tr("enter select · esc close", "enter выбрать · esc закрыть")
	if pos := m.list.Position(); pos != "" {
		// Not every action fits: say so.
		hint = pos + " · " + hint
	}
	body += "\n" + StyleDim.Render(Trunc(hint, w))
	return Box(Trunc(title, w), body, w+2)
}

// HelpView renders the key reference for the ? overlay: one column when
// it fits, otherwise sections side by side.
func HelpView(sections []HelpSection, width, height int) string {
	w := min(76, width-6) // box width; the text area is w-2 (padding)
	tw := w - 2
	// keyWidth is the widest key of secs, capped so descriptions keep
	// some room (keys are what the overlay is for: never cut them short
	// when they fit).
	keyWidth := func(secs []HelpSection, limit int) int {
		kw := 1
		for _, s := range secs {
			for _, h := range s.Hints {
				kw = max(kw, Width(h.Key))
			}
		}
		return min(kw, limit)
	}
	render := func(secs []HelpSection, cw, keyW int) []string {
		var out []string
		for _, s := range secs {
			if len(s.Hints) == 0 {
				continue
			}
			if len(out) > 0 {
				out = append(out, "")
			}
			out = append(out, StyleSection.Render(Trunc(s.Title, cw)))
			for _, h := range s.Hints {
				line := " " + StyleKey.Render(PadRight(h.Key, keyW)) + " " + Trunc(h.Desc, max(1, cw-keyW-2))
				out = append(out, Trunc(line, cw))
			}
		}
		return out
	}
	// The box adds its border, the title and the footer line.
	avail := max(3, height-4)
	lines := render(sections, tw, keyWidth(sections, 14))
	if len(lines) > avail && w >= 60 && len(sections) > 1 {
		cw := (tw - 3) / 2
		left := render(sections[:1], cw, keyWidth(sections[:1], cw/2))
		right := render(sections[1:], cw, keyWidth(sections[1:], cw/2))
		lines = nil
		for i := 0; i < max(len(left), len(right)); i++ {
			l, r := "", ""
			if i < len(left) {
				l = left[i]
			}
			if i < len(right) {
				r = right[i]
			}
			lines = append(lines, PadRight(l, cw)+"   "+r)
		}
	}
	if len(lines) > avail {
		lines = append(lines[:max(1, avail-1)], StyleDim.Render(Trunc(Tr(" … (enlarge the terminal to see more)", " … (увеличьте окно, чтобы увидеть всё)"), tw)))
	}
	return Box(Tr("Keys", "Клавиши"), strings.Join(lines, "\n")+"\n"+StyleDim.Render(Trunc(Tr("press any key to close", "нажмите любую клавишу, чтобы закрыть"), tw)), w)
}

// HelpSection groups hints in the help overlay.
type HelpSection struct {
	Title string
	Hints []Hint
}
