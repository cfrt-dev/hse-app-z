package ui

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Clean makes API text safe and single-line: strips ANSI/control
// characters (no terminal injection from server data), invisible fillers
// like U+2800 (used as "empty" story titles) and collapses whitespace.
func Clean(s string) string {
	return strings.Join(strings.Fields(sanitize(s, false)), " ")
}

// CleanMulti is Clean but keeps line breaks (paragraphs are preserved,
// runs of blank lines collapsed).
func CleanMulti(s string) string {
	s = sanitize(s, true)
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blank := false
	for _, l := range lines {
		l = strings.Join(strings.Fields(l), " ")
		if l == "" {
			if !blank && len(out) > 0 {
				out = append(out, "")
			}
			blank = true
			continue
		}
		blank = false
		out = append(out, l)
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}

func sanitize(s string, keepNewlines bool) string {
	s = ansi.Strip(s)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' && keepNewlines:
			b.WriteRune('\n')
		case r == '\r':
			// drop; \r\n becomes \n
		case r == '\t' || r == '\n':
			b.WriteRune(' ')
		case r == '\u2800' || r == '\u200b' || r == '\u200c' || r == '\u200d' || r == '\ufeff' || r == '\u00ad' ||
			r == '\u3164' || r == '\uffa0' || r == '\u115f' || r == '\u1160':
			// braille blank, zero-width chars, BOM, soft hyphen, Hangul
			// fillers (render as blanks, used for "empty" names)
		case r == '\u00a0':
			b.WriteRune(' ')
		case unicode.IsControl(r) || (unicode.Is(unicode.Cf, r) && r != '\u200e' && r != '\u200f'):
			// other control/format characters
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Width is the display width of s (ANSI-aware).
func Width(s string) int { return ansi.StringWidth(s) }

// Trunc shortens s to w cells with an ellipsis (ANSI-aware).
func Trunc(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= w {
		return s
	}
	return ansi.Truncate(s, w, "…")
}

// PadRight pads (or truncates) s to exactly w cells.
func PadRight(s string, w int) string {
	s = Trunc(s, w)
	if n := w - ansi.StringWidth(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// PadLeft right-aligns s in w cells.
func PadLeft(s string, w int) string {
	s = Trunc(s, w)
	if n := w - ansi.StringWidth(s); n > 0 {
		return strings.Repeat(" ", n) + s
	}
	return s
}

// Wrap word-wraps s to w cells, hard-breaking words longer than w (URLs).
// Styles that span a break are closed and reopened per line.
func Wrap(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return BalanceSGR(ansi.Wrap(s, w, ""))
}

var sgrRe = regexp.MustCompile(`\x1b\[[0-9;:]*m`)

// BalanceSGR makes every line self-contained: a style left open at the end
// of a line is closed there and reopened on the next line, so it can't bleed
// into a neighbouring pane.
func BalanceSGR(s string) string {
	if !strings.Contains(s, "\x1b[") {
		return s
	}
	lines := strings.Split(s, "\n")
	active := ""
	for i, l := range lines {
		out := active + l
		for _, seq := range sgrRe.FindAllString(l, -1) {
			if seq == "\x1b[0m" || seq == "\x1b[m" {
				active = ""
			} else {
				active += seq
			}
		}
		if active != "" {
			out += "\x1b[0m"
		}
		lines[i] = out
	}
	return strings.Join(lines, "\n")
}

// Indent prefixes every line of s.
func Indent(s, prefix string) string {
	if s == "" {
		return s
	}
	return prefix + strings.ReplaceAll(s, "\n", "\n"+prefix)
}

// JoinNonEmpty joins the non-blank parts with sep.
func JoinNonEmpty(sep string, parts ...string) string {
	var out []string
	for _, p := range parts {
		if strings.TrimSpace(ansi.Strip(p)) != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}

// LabelWidth is the label column width used by KV.
const LabelWidth = 12

// KV renders "label  value" with the value wrapped under itself. Returns ""
// when value is blank so callers can append unconditionally.
func KV(label, value string, width int) string {
	if strings.TrimSpace(ansi.Strip(value)) == "" {
		return ""
	}
	lw := LabelWidth
	if width < 30 {
		lw = 10
	}
	vw := width - lw - 1
	if vw < 10 {
		vw = 10
	}
	lines := strings.Split(Wrap(value, vw), "\n")
	var b strings.Builder
	b.WriteString(StyleDim.Render(PadRight(label, lw)))
	b.WriteString(" ")
	b.WriteString(lines[0])
	for _, l := range lines[1:] {
		b.WriteString("\n")
		b.WriteString(strings.Repeat(" ", lw+1))
		b.WriteString(l)
	}
	return b.String()
}

// Lines joins the non-empty blocks with newlines.
func Lines(blocks ...string) string {
	var out []string
	for _, b := range blocks {
		if b != "" {
			out = append(out, b)
		}
	}
	return strings.Join(out, "\n")
}

// Section renders a heading followed by the body (empty body → "").
func Section(title, body string) string {
	if strings.TrimSpace(body) == "" {
		return ""
	}
	return "\n" + StyleSection.Render(title) + "\n" + body
}

// Plural picks a word form by count ("1 lesson", "3 lessons").
func Plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// Center centers s within w cells.
func Center(s string, w int) string {
	return lipgloss.PlaceHorizontal(w, lipgloss.Center, s)
}
