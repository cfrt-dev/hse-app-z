package app

import (
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"hse-app-z/internal/auth"
	"hse-app-z/internal/ui"
	"hse-app-z/internal/ui/uitest"
)

// The help overlay must fit (with its border and footer) at the common
// 80×24 and at the minimum size, also on a pushed page (breadcrumb row).
func TestHelpOverlayFitsCommonSizes(t *testing.T) {
	d := demoDriver(t)
	for _, push := range []bool{false, true} {
		if push {
			d.send(ui.OpenTargetMsg{Target: ui.Target{Kind: "person", Key: "yakinderknecht@hse.ru", Title: "Kinderkneht Yana Anatolevna"}})
		}
		for _, sz := range [][2]int{{80, 24}, {60, 14}, {100, 30}, {66, 16}} {
			d.send(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
			d.keys("?")
			v := d.view()
			checkFits(t, d.m.View(), sz[0], sz[1])
			if !strings.Contains(v, "press any key") || !strings.Contains(v, "╰") {
				t.Errorf("%dx%d push=%v: help box clipped:\n%s", sz[0], sz[1], push, v)
			}
			if sz[0] >= 80 && !strings.Contains(v, "ctrl+d/ctrl+u") {
				t.Errorf("%dx%d: key cut short:\n%s", sz[0], sz[1], v)
			}
			d.keys("x")
		}
	}
}

func loginDriver(t *testing.T) *driver {
	ctx := uitest.Ctx(t)
	ctx.Demo = false
	store := &auth.Store{Path: filepath.Join(t.TempDir(), "tokens.json")}
	src := auth.NewSource(auth.Tokens{}, store, nil)
	d := newDriver(t, Options{Ctx: ctx, Source: src, Port: freePort(t)})
	t.Cleanup(func() {
		if d.m.login != nil {
			d.m.login.close()
		}
	})
	if d.m.login == nil || d.m.login.session == nil {
		t.Fatal("sign-in screen not started")
	}
	return d
}

// At the minimum size the sign-in screen still shows the keys and the
// link; text is wrapped once, to the box's real width.
func TestLoginScreenSmall(t *testing.T) {
	d := loginDriver(t)
	url := d.m.login.session.AuthURL
	for _, sz := range [][2]int{{60, 14}, {80, 24}, {110, 32}} {
		d.send(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
		v := d.view()
		checkFits(t, d.m.View(), sz[0], sz[1])
		for _, want := range []string{"Sign in to HSE", "q quit", "c copy link", "https://", "╰"} {
			if !strings.Contains(v, want) {
				t.Errorf("%dx%d: sign-in screen lacks %q:\n%s", sz[0], sz[1], want, v)
			}
		}
		// No line of box content may be a stray fragment produced by
		// wrapping twice (a long line followed by a 1–2 word remainder).
		for _, l := range strings.Split(v, "\n") {
			inner := strings.TrimSpace(strings.Trim(strings.TrimSpace(l), "│"))
			if inner == "browser" || inner == "Your" || strings.HasPrefix(inner, "(saml") {
				t.Errorf("%dx%d: text wrapped twice: %q\n%s", sz[0], sz[1], inner, v)
			}
		}
	}
	// With enough room the whole link is shown, in one piece per line.
	v := d.view()
	var link strings.Builder
	for _, l := range strings.Split(v, "\n") {
		inner := strings.TrimSpace(strings.Trim(strings.TrimSpace(l), "│"))
		if strings.HasPrefix(inner, "https://") || (link.Len() > 0 && !strings.Contains(inner, " ") && inner != "") {
			link.WriteString(inner)
		} else if link.Len() > 0 {
			break
		}
	}
	if link.String() != url {
		t.Errorf("link shown as %q\nwant %q", link.String(), url)
	}
}

// Copy feedback (and errors) must be visible on the sign-in screen,
// which replaces the footer.
func TestLoginScreenShowsStatus(t *testing.T) {
	d := loginDriver(t)
	d.send(tea.WindowSizeMsg{Width: 60, Height: 14})
	d.keys("c")
	v := d.view()
	if !strings.Contains(v, "Copied sign-in link") {
		t.Errorf("copy gave no visible feedback:\n%s", v)
	}
	t.Log("\n" + v)
	checkFits(t, d.m.View(), 60, 14)
	d.send(tea.WindowSizeMsg{Width: 110, Height: 32})
	t.Log("\n" + d.view())
}

// reloadPage records ReloadMsg deliveries.
type reloadPage struct {
	fakePage
	reloads int
}

func (p *reloadPage) Update(msg tea.Msg) tea.Cmd {
	if _, ok := msg.(ui.ReloadMsg); ok {
		p.reloads++
	}
	return p.fakePage.Update(msg)
}

// Session expiry mid-session: pushed pages survive the sign-in and reload.
func TestReloginKeepsPushedPages(t *testing.T) {
	d := loginDriver(t)
	tok := fakeJWT(map[string]any{"exp": time.Now().Add(time.Hour).Unix(), "sub": "u1", "email": "a@edu.hse.ru", "name": "A"})
	d.send(loginResultMsg{gen: d.m.login.gen, tokens: auth.Tokens{AccessToken: tok, RefreshToken: "r1", ExpiresAt: time.Now().Add(time.Hour)}})
	p := &reloadPage{fakePage: fakePage{title: "Pushed"}}
	d.send(ui.PushMsg{Page: p})
	d.send(ui.LoginRequiredMsg{})
	if d.m.login == nil {
		t.Fatal("no sign-in screen")
	}
	// Keys are inert while signing in; esc (back everywhere else) must
	// not quit the app from this screen that popped up by itself.
	d.keys("j", "2", "esc")
	if d.quit || d.m.login == nil || d.m.active != 0 {
		t.Fatalf("keys acted during sign-in: quit=%v login=%v tab=%d", d.quit, d.m.login != nil, d.m.active)
	}
	tok2 := fakeJWT(map[string]any{"exp": time.Now().Add(time.Hour).Unix(), "sub": "u2", "email": "b@edu.hse.ru", "name": "B"})
	d.send(loginResultMsg{gen: d.m.login.gen, tokens: auth.Tokens{AccessToken: tok2, RefreshToken: "r2", ExpiresAt: time.Now().Add(time.Hour)}})
	if d.m.login != nil {
		t.Fatal("sign-in screen still shown")
	}
	if d.m.top() != p || d.m.active != 0 {
		t.Errorf("pushed page lost after re-login")
	}
	if p.reloads == 0 {
		t.Errorf("pushed page not reloaded after re-login")
	}
	if d.m.ctx.Me.Email != "b@edu.hse.ru" {
		t.Errorf("identity: %+v", d.m.ctx.Me)
	}
	if len(p.keys) != 0 {
		t.Errorf("keys leaked to the page during sign-in: %v", p.keys)
	}
}

// 1-9 belong to the root: a digit without a tab doesn't reach the page.
func TestDigitWithoutTab(t *testing.T) {
	d := demoDriver(t)
	p := &fakePage{title: "x"}
	d.send(ui.PushMsg{Page: p})
	d.keys("9")
	if len(p.keys) != 0 {
		t.Errorf("9 reached the page: %v", p.keys)
	}
}

// Port busy: a clear message naming the port, r retries once it's free.
func TestLoginPortBusy(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ctx := uitest.Ctx(t)
	ctx.Demo = false
	src := auth.NewSource(auth.Tokens{}, &auth.Store{Path: filepath.Join(t.TempDir(), "tokens.json")}, nil)
	d := newDriver(t, Options{Ctx: ctx, Source: src, Port: port})
	t.Cleanup(func() {
		if d.m.login != nil {
			d.m.login.close()
		}
	})
	d.send(tea.WindowSizeMsg{Width: 60, Height: 14})
	v := d.view()
	if !strings.Contains(v, fmt.Sprintf("Port %d is busy", port)) || !strings.Contains(v, "r try again") {
		t.Errorf("port-busy screen:\n%s", v)
	}
	checkFits(t, d.m.View(), 60, 14)
	d.keys("r")
	if d.m.login.session != nil {
		t.Error("session started on a busy port")
	}
	ln.Close()
	d.keys("r")
	if d.m.login.session == nil || d.m.login.err != nil {
		t.Errorf("r didn't retry once the port was free: %v", d.m.login.err)
	}
}
