package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// LeftWidth is the list pane width for a split layout of total width w.
func LeftWidth(w int) int {
	lw := w * 2 / 5
	if lw < 30 {
		lw = 30
	}
	if lw > 64 {
		lw = 64
	}
	if lw > w-20 {
		lw = w / 2
	}
	return lw
}

// Fit clips/pads s to exactly width×height cells.
func Fit(s string, width, height int) string {
	if height <= 0 || width <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for i, l := range lines {
		l = PadRight(l, width)
		if strings.Contains(l, "\x1b[") {
			// Truncation can cut off a closing sequence; never leak styles.
			l += "\x1b[0m"
		}
		lines[i] = l
	}
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", width))
	}
	return strings.Join(lines, "\n")
}

// Split draws two panes side by side with a vertical rule between them.
// The right pane gets width-leftWidth-3 cells (rule plus padding).
func Split(width, height, leftWidth int, left, right string) string {
	if leftWidth >= width-4 {
		return Fit(left, width, height)
	}
	rw := width - leftWidth - 3
	l := strings.Split(Fit(left, leftWidth, height), "\n")
	r := strings.Split(Fit(right, rw, height), "\n")
	sep := StyleSep.Render("│")
	var b strings.Builder
	for i := 0; i < height; i++ {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(l[i])
		b.WriteString(" ")
		b.WriteString(sep)
		b.WriteString(" ")
		b.WriteString(r[i])
	}
	return b.String()
}

// RightWidth is the usable width of the right pane for Split.
func RightWidth(width, leftWidth int) int { return max(0, width-leftWidth-3) }

// Placeholder centers a message in an empty pane.
func Placeholder(width, height int, msg string) string {
	lines := strings.Split(Wrap(msg, max(10, width-4)), "\n")
	top := (height - len(lines)) / 3
	var out []string
	for i := 0; i < top; i++ {
		out = append(out, "")
	}
	for _, l := range lines {
		out = append(out, Center(l, width))
	}
	return Fit(strings.Join(out, "\n"), width, height)
}

// StateView renders the standard loading/error/empty states for a pane,
// or "" when there is data to show (len > 0).
func StateView(width, height int, l Load, n int, empty string) string {
	switch {
	case n > 0:
		return ""
	case l.Loading:
		// A new request supersedes an old error (retry, other year…).
		return Placeholder(width, height, StyleDim.Render(Tr("Loading…", "Загрузка…")))
	case l.Err != nil:
		return Placeholder(width, height, StyleErr.Render(ErrText(l.Err))+"\n\n"+StyleDim.Render(Tr("press r to retry", "нажмите r, чтобы повторить")))
	}
	return Placeholder(width, height, StyleDim.Render(empty))
}

// LoadNote is a one-line status for a pane header: loading, stale, error.
func LoadNote(l Load) string {
	switch {
	case l.Loading && l.Loaded:
		return StyleDim.Render(Tr("updating…", "обновление…"))
	case l.Loading:
		return StyleDim.Render(Tr("loading…", "загрузка…"))
	case l.Err != nil && l.Loaded:
		return StyleErr.Render("⚠ " + ErrText(l.Err))
	case l.Meta.Stale:
		when := ""
		if !l.Meta.Fetched.IsZero() {
			when = Tr(" from ", " от ") + AgoString(time.Since(l.Meta.Fetched))
		}
		return StyleWarn.Render(fmt.Sprintf("%s · %s%s", staleReason(l.Meta.StaleReason), Tr("cached", "из кэша"), when))
	}
	return ""
}

func staleReason(r string) string {
	if !RU() {
		return r
	}
	switch r {
	case "offline":
		return "нет сети"
	case "rate limited":
		return "лимит запросов"
	case "server error":
		return "ошибка сервера"
	case "sign-in server error":
		return "ошибка сервера входа"
	}
	return r
}

// AgoString renders a duration as "5m ago", "2h ago", "3d ago".
func AgoString(d time.Duration) string {
	switch {
	case d < time.Minute:
		return Tr("just now", "только что")
	case d < time.Hour:
		return Trf("%dm ago", "%d мин назад", int(d.Minutes()))
	case d < 48*time.Hour:
		return Trf("%dh ago", "%d ч назад", int(d.Hours()))
	}
	return Trf("%dd ago", "%d дн назад", int(d.Hours()/24))
}

// PaneTitle renders a pane header line: bold title, dim extra on the right.
// The title keeps at least half the width; the note is shortened first.
func PaneTitle(title, right string, width int) string {
	if width <= 0 {
		return ""
	}
	tw := min(Width(title), max(width/2, width-Width(right)-1))
	if room := width - tw - 1; room < 2 {
		right = ""
	} else {
		right = Trunc(right, room)
	}
	t := StyleTitle.Render(Trunc(title, width-Width(right)-1))
	if right == "" {
		return Trunc(StyleTitle.Render(title), width)
	}
	gap := width - Width(t) - Width(right)
	return t + strings.Repeat(" ", max(1, gap)) + right
}

// Box draws a rounded border box with a title; inner width w.
func Box(title, body string, w int) string {
	st := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(ColorAccent).Padding(0, 1).Width(w)
	if title != "" {
		body = StyleTitle.Render(title) + "\n" + body
	}
	return st.Render(body)
}

// Overlay draws box centered on top of base (both multi-line strings,
// base is width×height).
func Overlay(base, box string, width, height int) string {
	bl := strings.Split(Fit(base, width, height), "\n")
	ol := strings.Split(box, "\n")
	bw := 0
	for _, l := range ol {
		bw = max(bw, ansi.StringWidth(l))
	}
	if bw > width {
		bw = width
	}
	top := (height - len(ol)) / 2
	if top < 0 {
		top = 0
	}
	left := (width - bw) / 2
	for i, l := range ol {
		y := top + i
		if y >= len(bl) {
			break
		}
		row := bl[y]
		// A wide character (CJK, emoji) straddling either edge of the box
		// is replaced by a space so the box starts at the same column on
		// every row and the line keeps its width.
		prefix := ansi.Truncate(row, left, "")
		if pw := ansi.StringWidth(prefix); pw < left {
			prefix += "\x1b[0m" + strings.Repeat(" ", left-pw)
		}
		rest := max(0, width-left-bw)
		suffix := ansi.TruncateLeft(row, left+bw, "")
		if ansi.StringWidth(suffix) > rest {
			suffix = " " + ansi.TruncateLeft(row, left+bw+1, "")
		}
		bl[y] = prefix + "\x1b[0m" + PadRight(l, bw) + "\x1b[0m" + suffix
	}
	return strings.Join(bl, "\n")
}

// Tabs renders a one-line selector: "‹ a │ b │ c ›" with the active value
// highlighted. Use for year/program/type filters.
func Tabs(values []string, active int) string {
	var parts []string
	for i, v := range values {
		if i == active {
			parts = append(parts, StyleKey.Render(v))
		} else {
			parts = append(parts, StyleDim.Render(v))
		}
	}
	return strings.Join(parts, StyleSep.Render(" │ "))
}
