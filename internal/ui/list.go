package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// List is a keyboard-driven cursor over n rows, some of which may be
// non-selectable headers. It does not own the data: the page renders rows
// through a callback.
//
// Keys: j/k ↓/↑ move, g/G home/end jump, ctrl+d/ctrl+u half page,
// pgdown/pgup (ctrl+f/ctrl+b) full page.
type List struct {
	Cursor int
	// Skip marks non-selectable rows (headers). nil = all selectable.
	Skip func(i int) bool

	n      int
	offset int
	height int
}

func (l *List) skip(i int) bool { return l.Skip != nil && l.Skip(i) }

// Len is the number of rows.
func (l *List) Len() int { return l.n }

// SetLen updates the row count and keeps the cursor valid.
func (l *List) SetLen(n int) {
	l.n = n
	if l.Cursor >= n {
		l.Cursor = n - 1
	}
	if l.Cursor < 0 {
		l.Cursor = 0
	}
	l.settle(1)
}

// settle moves the cursor off header rows, preferring direction dir.
func (l *List) settle(dir int) {
	if l.n == 0 {
		l.Cursor = 0
		return
	}
	if !l.skip(l.Cursor) {
		return
	}
	for i := l.Cursor; i >= 0 && i < l.n; i += dir {
		if !l.skip(i) {
			l.Cursor = i
			return
		}
	}
	for i := l.Cursor; i >= 0 && i < l.n; i -= dir {
		if !l.skip(i) {
			l.Cursor = i
			return
		}
	}
}

// HasSelection reports whether the cursor is on a selectable row.
func (l *List) HasSelection() bool {
	return l.n > 0 && l.Cursor >= 0 && l.Cursor < l.n && !l.skip(l.Cursor)
}

// Select moves the cursor to row i (or the nearest selectable row).
func (l *List) Select(i int) {
	if l.n == 0 {
		return
	}
	if i < 0 {
		i = 0
	}
	if i >= l.n {
		i = l.n - 1
	}
	l.Cursor = i
	l.settle(1)
}

func (l *List) move(delta int) {
	if l.n == 0 {
		return
	}
	dir := 1
	if delta < 0 {
		dir = -1
	}
	target := l.Cursor + delta
	if target < 0 {
		target = 0
	}
	if target >= l.n {
		target = l.n - 1
	}
	// Step past headers in the direction of travel; stay put if there's
	// nothing selectable that way.
	for i := target; i >= 0 && i < l.n; i += dir {
		if !l.skip(i) {
			l.Cursor = i
			return
		}
	}
	for i := target; i >= 0 && i < l.n; i -= dir {
		if !l.skip(i) {
			if (dir > 0 && i > l.Cursor) || (dir < 0 && i < l.Cursor) {
				l.Cursor = i
			}
			return
		}
	}
}

func (l *List) page() int {
	if l.height > 1 {
		return l.height - 1
	}
	return 10
}

// HandleKey applies navigation keys; it reports whether the key was used.
func (l *List) HandleKey(msg tea.KeyMsg) bool {
	switch msg.String() {
	case "down", "j":
		l.move(1)
	case "up", "k":
		l.move(-1)
	case "home", "g":
		l.Cursor = 0
		l.settle(1)
	case "end", "G":
		l.Cursor = l.n - 1
		l.settle(-1)
	case "ctrl+d":
		l.move(max(1, l.page()/2))
	case "ctrl+u":
		l.move(-max(1, l.page()/2))
	case "pgdown", "ctrl+f":
		l.move(l.page())
	case "pgup", "ctrl+b":
		l.move(-l.page())
	default:
		return false
	}
	return true
}

// Render draws exactly height lines. row renders row i at the given width;
// selected is true for the cursor row (use Row to get a consistent marker).
func (l *List) Render(width, height int, row func(i int, selected bool, width int) string) string {
	if height <= 0 {
		return ""
	}
	l.height = height
	if l.n == 0 {
		return strings.Repeat("\n", height-1)
	}
	// Keep a little context around the cursor, and show the header above
	// the first selectable row when scrolled to the top of a group.
	margin := 2
	if height < 8 {
		margin = 0
	}
	if l.Cursor-margin < l.offset {
		l.offset = l.Cursor - margin
		if l.offset > 0 && l.skip(l.offset-1) && l.Cursor-l.offset < height-1 {
			l.offset--
		}
	}
	if l.Cursor+margin >= l.offset+height {
		l.offset = l.Cursor + margin - height + 1
	}
	if l.offset > l.n-height {
		l.offset = l.n - height
	}
	if l.offset < 0 {
		l.offset = 0
	}
	lines := make([]string, 0, height)
	for i := l.offset; i < l.n && len(lines) < height; i++ {
		lines = append(lines, Trunc(row(i, i == l.Cursor && !l.skip(i), width), width))
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// Position is a "12/40" indicator ("" when everything fits).
func (l *List) Position() string {
	if l.n == 0 || l.n <= l.height {
		return ""
	}
	return fmt.Sprintf("%d/%d", l.Cursor+1, l.n)
}

// Row renders a list row with the standard cursor marker, padded to width.
func Row(selected bool, content string, width int) string {
	if selected {
		return StyleCursor.Render("▌") + StyleSelected.Render(PadRight(content, width-1))
	}
	return " " + PadRight(content, width-1)
}

// HeaderRow renders a non-selectable group header row.
func HeaderRow(title string, width int) string {
	return StyleGroupHeader.Render(Trunc(" "+title, width))
}

// Scroll is a vertically scrollable text pane (the detail pane next to
// lists). Keys: J/K or shift+↓/shift+↑ scroll a line, ctrl+e/ctrl+y too.
type Scroll struct {
	Offset int
	height int
	total  int
}

// Reset scrolls back to the top (call when the selection changes).
func (s *Scroll) Reset() { s.Offset = 0 }

// HandleKey applies scroll keys; it reports whether the key was used.
func (s *Scroll) HandleKey(msg tea.KeyMsg) bool {
	switch msg.String() {
	case "J", "shift+down", "ctrl+e":
		s.Offset++
	case "K", "shift+up", "ctrl+y":
		s.Offset--
	default:
		return false
	}
	s.clamp()
	return true
}

func (s *Scroll) clamp() {
	if maxOff := s.total - s.height; s.Offset > maxOff {
		s.Offset = maxOff
	}
	if s.Offset < 0 {
		s.Offset = 0
	}
}

// Render shows the visible window of content (already wrapped to width),
// with ↑/↓ markers when there is more.
func (s *Scroll) Render(content string, width, height int) string {
	if height <= 0 {
		return ""
	}
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	s.height, s.total = height, len(lines)
	s.clamp()
	end := s.Offset + height
	if end > len(lines) {
		end = len(lines)
	}
	view := append([]string(nil), lines[s.Offset:end]...)
	for i := range view {
		view[i] = Trunc(view[i], width)
	}
	if s.Offset > 0 && len(view) > 0 {
		view[0] = withMarker(view[0], "↑", width)
	}
	if end < len(lines) && len(view) > 0 {
		view[len(view)-1] = withMarker(view[len(view)-1], "↓", width)
	}
	for len(view) < height {
		view = append(view, "")
	}
	return strings.Join(view, "\n")
}

// withMarker puts a scroll marker at the right edge, overwriting at most
// one cell of content.
func withMarker(line, marker string, width int) string {
	if width < 2 {
		return line
	}
	if Width(line) <= width-2 {
		return PadRight(line, width-2) + " " + StyleDim.Render(marker)
	}
	return Trunc(line, width-1) + "\x1b[0m" + StyleDim.Render(marker)
}

// Overflows reports whether the content didn't fit on the last render.
func (s *Scroll) Overflows() bool { return s.total > s.height }
