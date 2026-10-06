// Package kitty draws images with the kitty terminal graphics protocol using
// Unicode placeholders: the image is transmitted once, attached to a virtual
// placement, and then displayed by printing ordinary text cells
// (U+10EEEE plus row/column diacritics, colored with the image id). Because
// the image lives in text cells, it works inside a TUI framework that only
// writes lines of text: it scrolls, clips and redraws like text.
//
// Supported by kitty ≥ 0.28 and Ghostty. Spec:
// https://sw.kovidgoyal.net/kitty/graphics-protocol/#unicode-placeholders
package kitty

import (
	"context"
	"encoding/base64"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// placeholder is the Unicode placeholder character.
const placeholder = '\U0010EEEE'

// MaxCells is the largest row/column index a placeholder can address.
var MaxCells = len(diacritics)

// Support says whether, and how, images can be shown.
type Support struct {
	Enabled bool
	// Tmux wraps graphics commands in tmux passthrough sequences (requires
	// `set -g allow-passthrough on`).
	Tmux bool
	// Reason explains the decision (shown by `hse-app-z doctor`).
	Reason string
	// Hint is set when images are off only because of a fixable setting
	// (e.g. tmux passthrough); the app shows it once at startup.
	Hint string
}

// TmuxQuery runs a tmux command and returns its trimmed output. Replaced
// in tests.
var TmuxQuery = func(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", args...).Output()
	return strings.TrimSpace(string(out)), err
}

// placeholderTerminal names a terminal that supports Unicode placeholders,
// judging by a terminal name/version string ("" if none).
func placeholderTerminal(s string) string {
	s = strings.ToLower(s)
	switch {
	case strings.Contains(s, "ghostty"):
		return "Ghostty"
	case strings.Contains(s, "kitty"):
		return "kitty"
	}
	return ""
}

// Detect decides from the environment whether the terminal supports
// Unicode placeholders. mode is "auto", "on" or "off" (anything else =
// auto). getenv is os.Getenv in production. Inside tmux, auto mode asks
// tmux which terminal the client is attached from and whether
// allow-passthrough is enabled for this pane.
func Detect(mode string, getenv func(string) string) Support {
	inTmux := getenv("TMUX") != ""
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "off", "no", "false", "0":
		return Support{Reason: "disabled by option"}
	case "on", "yes", "true", "1":
		return Support{Enabled: true, Tmux: inTmux, Reason: "forced on"}
	}
	if inTmux {
		return detectTmux()
	}
	var who string
	switch {
	case getenv("KITTY_WINDOW_ID") != "" || strings.ToLower(getenv("TERM")) == "xterm-kitty":
		who = "kitty"
	default:
		who = placeholderTerminal(getenv("TERM_PROGRAM") + " " + getenv("TERM"))
		if who == "" && getenv("GHOSTTY_RESOURCES_DIR") != "" {
			who = "Ghostty"
		}
	}
	if who == "" {
		// WezTerm, Konsole, iTerm2… implement parts of the protocol but not
		// Unicode placeholders.
		return Support{Reason: "terminal doesn't support kitty image placeholders"}
	}
	return Support{Enabled: true, Reason: who}
}

func detectTmux() Support {
	out, err := TmuxQuery("display-message", "-p", "#{client_termname}\t#{client_termtype}\t#{allow-passthrough}")
	if err != nil {
		return Support{Reason: "inside tmux, but couldn't ask tmux about the terminal (force with --images on)"}
	}
	parts := strings.Split(out, "\t")
	for len(parts) < 3 {
		parts = append(parts, "")
	}
	term, ver, pass := parts[0], parts[1], strings.ToLower(strings.TrimSpace(parts[2]))
	who := placeholderTerminal(term + " " + ver)
	if who == "" {
		name := strings.TrimSpace(ver)
		if name == "" {
			name = term
		}
		if name == "" {
			name = "an unknown terminal"
		}
		return Support{Reason: "tmux is attached from " + name + ", which doesn't support kitty image placeholders"}
	}
	if pass != "on" && pass != "all" {
		hint := "Avatars are off: tmux blocks image passthrough. Add `set -g allow-passthrough on` to tmux.conf (or run hse-app-z --images off)."
		return Support{Reason: who + " inside tmux, but tmux allow-passthrough is off", Hint: hint}
	}
	return Support{Enabled: true, Tmux: true, Reason: who + " via tmux (allow-passthrough " + pass + ")"}
}

func (s Support) wrap(cmd string) string {
	if !s.Tmux {
		return cmd
	}
	return "\x1bPtmux;" + strings.ReplaceAll(cmd, "\x1b", "\x1b\x1b") + "\x1b\\"
}

// chunkSize is the max base64 payload per command (protocol limit 4096).
const chunkSize = 4096

// Transmit returns the commands that upload a PNG as image id and create a
// virtual placement of cols×rows cells for it. Responses are suppressed
// (q=2) so nothing is ever written back to the application's stdin.
func (s Support) Transmit(id uint32, png []byte, cols, rows int) string {
	data := base64.StdEncoding.EncodeToString(png)
	var b strings.Builder
	for first := true; first || len(data) > 0; first = false {
		n := min(chunkSize, len(data))
		chunk := data[:n]
		data = data[n:]
		more := 0
		if len(data) > 0 {
			more = 1
		}
		var cmd string
		if first {
			cmd = fmt.Sprintf("\x1b_Ga=t,f=100,t=d,i=%d,q=2,m=%d;%s\x1b\\", id, more, chunk)
		} else {
			cmd = fmt.Sprintf("\x1b_Gm=%d;%s\x1b\\", more, chunk)
		}
		b.WriteString(s.wrap(cmd))
	}
	b.WriteString(s.wrap(fmt.Sprintf("\x1b_Ga=p,U=1,i=%d,c=%d,r=%d,q=2\x1b\\", id, cols, rows)))
	return b.String()
}

// Delete frees an image and its placements in the terminal.
func (s Support) Delete(id uint32) string {
	return s.wrap(fmt.Sprintf("\x1b_Ga=d,d=I,i=%d,q=2\x1b\\", id))
}

// Placeholder returns rows lines of cols placeholder cells showing image id.
// Every cell carries its row and column, so lines may be cut anywhere (by
// overlays or truncation) and still show the right part of the image. ids
// must fit in 24 bits (they are encoded as a true-color foreground).
func Placeholder(id uint32, cols, rows int) []string {
	cols = min(cols, MaxCells)
	rows = min(rows, MaxCells)
	if cols <= 0 || rows <= 0 {
		return nil
	}
	fg := fmt.Sprintf("\x1b[38;2;%d;%d;%dm", (id>>16)&0xff, (id>>8)&0xff, id&0xff)
	lines := make([]string, rows)
	for r := 0; r < rows; r++ {
		var b strings.Builder
		b.WriteString(fg)
		for c := 0; c < cols; c++ {
			b.WriteRune(placeholder)
			b.WriteRune(diacritics[r])
			b.WriteRune(diacritics[c])
		}
		b.WriteString("\x1b[39m")
		lines[r] = b.String()
	}
	return lines
}
