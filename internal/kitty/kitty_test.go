package kitty

import (
	"encoding/base64"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func stubTmux(t *testing.T, out string, err error) *[][]string {
	t.Helper()
	var calls [][]string
	old := TmuxQuery
	TmuxQuery = func(args ...string) (string, error) {
		calls = append(calls, args)
		return out, err
	}
	t.Cleanup(func() { TmuxQuery = old })
	return &calls
}

func TestDetect(t *testing.T) {
	stubTmux(t, "", errors.New("tmux must not be queried outside tmux"))
	cases := []struct {
		name string
		mode string
		env  map[string]string
		on   bool
		tmux bool
	}{
		{"kitty term", "auto", map[string]string{"TERM": "xterm-kitty"}, true, false},
		{"kitty window", "", map[string]string{"KITTY_WINDOW_ID": "3", "TERM": "xterm-256color"}, true, false},
		{"ghostty", "auto", map[string]string{"TERM_PROGRAM": "ghostty"}, true, false},
		{"ghostty term", "auto", map[string]string{"TERM": "xterm-ghostty"}, true, false},
		{"wezterm", "auto", map[string]string{"TERM_PROGRAM": "WezTerm", "TERM": "xterm-256color"}, false, false},
		{"apple terminal", "auto", map[string]string{"TERM_PROGRAM": "Apple_Terminal"}, false, false},
		{"forced on in tmux", "on", map[string]string{"TMUX": "/tmp/x"}, true, true},
		{"forced off", "off", map[string]string{"TERM": "xterm-kitty"}, false, false},
		{"garbage mode = auto", "maybe", map[string]string{"TERM": "xterm-kitty"}, true, false},
	}
	for _, c := range cases {
		s := Detect(c.mode, env(c.env))
		if s.Enabled != c.on || s.Tmux != c.tmux || s.Reason == "" {
			t.Errorf("%s: %+v", c.name, s)
		}
	}
}

func TestDetectTmux(t *testing.T) {
	inTmux := env(map[string]string{"TMUX": "/tmp/tmux-501/default,1,0", "TERM": "tmux-256color", "TERM_PROGRAM": "tmux"})
	cases := []struct {
		name, out string
		err       error
		on        bool
		hint      bool
	}{
		{"ghostty with passthrough", "xterm-ghostty\tghostty 1.1.3\ton", nil, true, false},
		{"kitty with passthrough all", "xterm-kitty\tkitty 0.39.1\tall", nil, true, false},
		{"ghostty via xterm-256color name", "xterm-256color\tghostty 1.2.0\ton", nil, true, false},
		{"passthrough off", "xterm-ghostty\tghostty 1.1.3\toff", nil, false, true},
		{"old tmux without the option", "xterm-kitty\t\t", nil, false, true},
		{"iterm", "xterm-256color\tiTerm2 3.5\ton", nil, false, false},
		{"query fails", "", errors.New("no server"), false, false},
	}
	for _, c := range cases {
		calls := stubTmux(t, c.out, c.err)
		s := Detect("auto", inTmux)
		if s.Enabled != c.on || (s.Hint != "") != c.hint || s.Reason == "" {
			t.Errorf("%s: %+v", c.name, s)
		}
		if s.Enabled && !s.Tmux {
			t.Errorf("%s: commands must be wrapped for tmux", c.name)
		}
		if len(*calls) != 1 || (*calls)[0][0] != "display-message" {
			t.Errorf("%s: unexpected tmux calls %v", c.name, *calls)
		}
	}
	// The inherited GHOSTTY_RESOURCES_DIR must not override what tmux says
	// about the attached client.
	stubTmux(t, "xterm-256color\tApple_Terminal\ton", nil)
	if s := Detect("auto", env(map[string]string{"TMUX": "x", "GHOSTTY_RESOURCES_DIR": "/Applications/Ghostty.app"})); s.Enabled {
		t.Errorf("client is Terminal.app: %+v", s)
	}
}

var apc = regexp.MustCompile("\x1b_G([^;\x1b]*)(?:;([^\x1b]*))?\x1b\\\\")

func TestTransmitChunks(t *testing.T) {
	png := make([]byte, 10000) // → 13336 base64 chars → 4 chunks
	for i := range png {
		png[i] = byte(i)
	}
	out := Support{Enabled: true}.Transmit(42, png, 12, 6)
	cmds := apc.FindAllStringSubmatch(out, -1)
	if len(cmds) != 5 {
		t.Fatalf("got %d commands", len(cmds))
	}
	if !strings.Contains(cmds[0][1], "a=t") || !strings.Contains(cmds[0][1], "i=42") || !strings.Contains(cmds[0][1], "f=100") || !strings.Contains(cmds[0][1], "q=2") || !strings.Contains(cmds[0][1], "m=1") {
		t.Errorf("first: %q", cmds[0][1])
	}
	if cmds[3][1] != "m=0" {
		t.Errorf("last data chunk: %q", cmds[3][1])
	}
	var data string
	for _, c := range cmds[:4] {
		if len(c[2]) > chunkSize {
			t.Errorf("chunk too big: %d", len(c[2]))
		}
		data += c[2]
	}
	got, err := base64.StdEncoding.DecodeString(data)
	if err != nil || len(got) != len(png) {
		t.Errorf("payload roundtrip failed: %v %d", err, len(got))
	}
	if p := cmds[4][1]; p != "a=p,U=1,i=42,c=12,r=6,q=2" {
		t.Errorf("placement: %q", p)
	}
	if ansi.StringWidth(out) != 0 {
		t.Errorf("transmission must have zero display width")
	}
}

func TestTransmitSmallAndTmux(t *testing.T) {
	out := Support{Enabled: true}.Transmit(1, []byte{1, 2, 3}, 2, 1)
	if n := len(apc.FindAllString(out, -1)); n != 2 {
		t.Errorf("small image: %d commands", n)
	}
	tm := Support{Enabled: true, Tmux: true}.Transmit(1, []byte{1}, 2, 1)
	if !strings.HasPrefix(tm, "\x1bPtmux;\x1b\x1b_G") || !strings.HasSuffix(tm, "\x1b\x1b\\\x1b\\") {
		t.Errorf("tmux wrapping: %q", tm)
	}
	if d := (Support{}).Delete(7); d != "\x1b_Ga=d,d=I,i=7,q=2\x1b\\" {
		t.Errorf("delete: %q", d)
	}
}

func TestPlaceholder(t *testing.T) {
	id := uint32(0x4A0102)
	lines := Placeholder(id, 12, 6)
	if len(lines) != 6 {
		t.Fatalf("rows: %d", len(lines))
	}
	for r, l := range lines {
		if w := ansi.StringWidth(l); w != 12 {
			t.Errorf("row %d width %d", r, w)
		}
		if !strings.HasPrefix(l, "\x1b[38;2;74;1;2m") || !strings.HasSuffix(l, "\x1b[39m") {
			t.Errorf("row %d color: %q", r, l[:20])
		}
		runes := []rune(ansi.Strip(l))
		if len(runes) != 36 {
			t.Fatalf("row %d: %d runes", r, len(runes))
		}
		for c := 0; c < 12; c++ {
			if runes[3*c] != placeholder || runes[3*c+1] != diacritics[r] || runes[3*c+2] != diacritics[c] {
				t.Fatalf("cell %d,%d wrong", r, c)
			}
		}
	}
	// Cutting a line keeps every remaining cell addressable.
	cut := ansi.TruncateLeft(lines[2], 5, "")
	if ansi.StringWidth(cut) != 7 || []rune(ansi.Strip(cut))[2] != diacritics[5] {
		t.Errorf("cut line lost its column info")
	}
	if Placeholder(1, 0, 3) != nil || Placeholder(1, 3, 0) != nil {
		t.Error("empty sizes should give nil")
	}
	if l := Placeholder(1, 1000, 1); ansi.StringWidth(l[0]) != MaxCells {
		t.Error("columns should be clamped to the diacritics table")
	}
}
