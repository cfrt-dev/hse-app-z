package app

import (
	"cmp"
	"encoding/base64"
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"hse-app-z/internal/auth"
	"hse-app-z/internal/ui"
	"hse-app-z/internal/ui/uitest"
)

// driver runs the root model synchronously like the Bubble Tea runtime.
type driver struct {
	// timeout drops slow commands (timers); 0 means 700ms.
	timeout time.Duration
	t       *testing.T
	m       *Model
	quit    bool
	msgs    []tea.Msg
	depth   int
}

func newDriver(t *testing.T, opts Options) *driver {
	t.Helper()
	d := &driver{t: t, m: New(opts)}
	d.send(tea.WindowSizeMsg{Width: 110, Height: 32})
	d.run(d.m.Init())
	return d
}

func (d *driver) run(cmd tea.Cmd) {
	if cmd == nil || d.depth > 200 {
		return
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-ch:
	case <-time.After(cmp.Or(d.timeout, 700*time.Millisecond)):
		return // timers (status clear, debounce) — drop
	}
	switch m := msg.(type) {
	case nil:
		return
	case tea.BatchMsg:
		for _, c := range m {
			d.run(c)
		}
		return
	case tea.QuitMsg:
		d.quit = true
		return
	}
	d.send(msg)
}

func (d *driver) send(msg tea.Msg) {
	d.msgs = append(d.msgs, msg)
	d.depth++
	defer func() { d.depth-- }()
	_, cmd := d.m.Update(msg)
	d.run(cmd)
}

func (d *driver) keys(keys ...string) {
	for _, k := range keys {
		for _, km := range uitest.KeyMsgs(k) {
			d.send(km)
		}
	}
}

func (d *driver) view() string { return ansi.Strip(d.m.View()) }

func checkFits(t *testing.T, view string, w, h int) {
	t.Helper()
	lines := strings.Split(view, "\n")
	if len(lines) > h {
		t.Errorf("view has %d lines, want ≤ %d", len(lines), h)
	}
	for i, l := range lines {
		if ansi.StringWidth(l) > w {
			t.Errorf("line %d is %d cells wide (max %d): %q", i, ansi.StringWidth(l), w, l)
		}
	}
}

func demoDriver(t *testing.T) *driver {
	ctx := uitest.Ctx(t)
	return newDriver(t, Options{Ctx: ctx})
}

func TestTabsAndLayout(t *testing.T) {
	d := demoDriver(t)
	v := d.view()
	for _, want := range []string{"1 Schedule", "2 Grades", "3 Rating", "4 Search", "8 News", "? help"} {
		if !strings.Contains(v, want) {
			t.Errorf("header/footer missing %q\n%s", want, v)
		}
	}
	checkFits(t, d.m.View(), 110, 32)

	for i, title := range []string{"Schedule", "Grades", "Rating", "Search", "Food", "Campus", "Services", "News"} {
		d.keys(string(rune('1' + i)))
		if d.m.active != i {
			t.Fatalf("key %d: active tab %d", i+1, d.m.active)
		}
		if got := d.m.top().Title(); got != title {
			t.Errorf("tab %d title %q, want %q", i+1, got, title)
		}
		// Search captures keys while its input is focused; leave it.
		if d.m.top().Capturing() {
			d.keys("esc")
		}
		checkFits(t, d.m.View(), 110, 32)
	}
	d.keys("1", "tab")
	if d.m.active != 1 {
		t.Errorf("tab: active %d, want 1", d.m.active)
	}
	d.keys("shift+tab", "shift+tab")
	if d.m.active != 7 {
		t.Errorf("shift+tab wraps: active %d, want 7", d.m.active)
	}
}

func TestSmallTerminal(t *testing.T) {
	d := demoDriver(t)
	d.send(tea.WindowSizeMsg{Width: 40, Height: 10})
	if v := d.view(); !strings.Contains(v, "Terminal too small") {
		t.Errorf("expected size warning, got\n%s", v)
	}
	checkFits(t, d.m.View(), 40, 10)
	d.send(tea.WindowSizeMsg{Width: 60, Height: 14})
	checkFits(t, d.m.View(), 60, 14)
	if v := d.view(); strings.Contains(v, "Terminal too small") {
		t.Errorf("60×14 should be usable")
	}
}

type fakePage struct {
	title string
	keys  []string
}

func (p *fakePage) Init() tea.Cmd { return nil }
func (p *fakePage) Update(msg tea.Msg) tea.Cmd {
	if k, ok := msg.(tea.KeyMsg); ok {
		p.keys = append(p.keys, k.String())
	}
	return nil
}
func (p *fakePage) View(w, h int) string { return "fake page " + p.title }
func (p *fakePage) Title() string        { return p.title }
func (p *fakePage) Hints() []ui.Hint     { return []ui.Hint{{Key: "x", Desc: "does x"}} }
func (p *fakePage) Capturing() bool      { return false }

func TestPushPopAndQuit(t *testing.T) {
	d := demoDriver(t)
	p := &fakePage{title: "Ivchenko"}
	d.send(ui.PushMsg{Page: p})
	if d.m.top() != p {
		t.Fatal("page not pushed")
	}
	if v := d.view(); !strings.Contains(v, "Schedule › Ivchenko") || !strings.Contains(v, "fake page") {
		t.Errorf("breadcrumb/page missing:\n%s", v)
	}
	if !strings.Contains(d.view(), "q back") {
		t.Errorf("footer should say q back")
	}
	d.keys("z")
	if len(p.keys) != 1 || p.keys[0] != "z" {
		t.Errorf("page keys = %v", p.keys)
	}
	d.keys("esc")
	if d.m.top() == p {
		t.Fatal("esc should pop")
	}
	d.send(ui.PushMsg{Page: p})
	d.keys("q")
	if d.m.top() == p || d.quit {
		t.Fatal("q should pop, not quit")
	}
	// Stacks are per tab.
	d.send(ui.PushMsg{Page: p})
	d.keys("2")
	if d.m.top() == p {
		t.Error("other tab should not show the pushed page")
	}
	d.keys("1")
	if d.m.top() != p {
		t.Error("returning to the tab should restore its stack")
	}
	d.keys("esc", "q")
	if !d.quit {
		t.Error("q on a tab root should quit")
	}
}

func TestOpenTargetPushesPage(t *testing.T) {
	d := demoDriver(t)
	d.send(ui.OpenTargetMsg{Target: ui.Target{Kind: "person", Key: "yakinderknecht@hse.ru", Title: "Kinderkneht Yana Anatolevna"}})
	if len(d.m.stacks[d.m.active]) != 1 {
		t.Fatal("target page not pushed")
	}
	checkFits(t, d.m.View(), 110, 32)
}

func TestMenuOverlay(t *testing.T) {
	d := demoDriver(t)
	ran := ""
	d.send(ui.MenuMsg{Title: "Lesson", Actions: []ui.Action{
		{Key: "o", Label: "Open link", Run: func() tea.Cmd { ran = "o"; return nil }},
		{Key: "m", Label: "Open map", Run: func() tea.Cmd { ran = "m"; return nil }},
		{Label: "No run"},
	}})
	if d.m.menu == nil || len(d.m.menu.Actions) != 2 {
		t.Fatal("menu not shown or nil actions kept")
	}
	if v := d.view(); !strings.Contains(v, "Open link") || !strings.Contains(v, "Lesson") {
		t.Errorf("menu not rendered:\n%s", v)
	}
	checkFits(t, d.m.View(), 110, 32)
	d.keys("m")
	if ran != "m" || d.m.menu != nil {
		t.Errorf("shortcut: ran=%q menu=%v", ran, d.m.menu != nil)
	}
	d.send(ui.MenuMsg{Title: "x", Actions: []ui.Action{{Label: "A", Run: func() tea.Cmd { ran = "a"; return nil }}}})
	d.keys("enter")
	if ran != "a" {
		t.Error("enter should run the selected action")
	}
	d.send(ui.MenuMsg{Title: "x", Actions: []ui.Action{{Label: "A", Run: func() tea.Cmd { ran = "b"; return nil }}}})
	d.keys("esc")
	if d.m.menu != nil || ran == "b" {
		t.Error("esc should close without running")
	}
	// Empty menus are not shown.
	d.send(ui.MenuMsg{Title: "empty"})
	if d.m.menu != nil {
		t.Error("empty menu shown")
	}
}

func TestHelpOverlay(t *testing.T) {
	d := demoDriver(t)
	d.keys("?")
	if !d.m.help || !strings.Contains(d.view(), "Everywhere") {
		t.Fatalf("help not shown:\n%s", d.view())
	}
	checkFits(t, d.m.View(), 110, 32)
	d.send(tea.WindowSizeMsg{Width: 60, Height: 14})
	checkFits(t, d.m.View(), 60, 14)
	d.keys("j")
	if d.m.help {
		t.Error("any key should close help")
	}
}

func TestSlashOpensSearch(t *testing.T) {
	d := demoDriver(t)
	d.send(ui.PushMsg{Page: &fakePage{title: "stale"}})
	d.keys("2", "/")
	if d.m.active != searchTab {
		t.Fatalf("active %d, want search", d.m.active)
	}
	if len(d.m.stacks[searchTab]) != 0 {
		t.Error("search stack should be reset")
	}
}

func TestLanguageToggle(t *testing.T) {
	t.Cleanup(func() { ui.SetLang("en") })
	d := demoDriver(t)
	if d.m.ctx.API.Lang() != "en" {
		t.Fatal("default lang should be en")
	}
	d.keys("L")
	if d.m.ctx.API.Lang() != "ru" || d.m.ctx.Settings.GetLang() != "ru" {
		t.Error("L should switch to ru")
	}
	if !strings.Contains(d.view(), "Язык: русский") {
		t.Errorf("status should announce the language:\n%s", d.view())
	}
	d.keys("L")
	if d.m.ctx.API.Lang() != "en" {
		t.Error("L should switch back")
	}
}

func TestStatusMessages(t *testing.T) {
	d := demoDriver(t)
	d.send(ui.StatusMsg{Text: "Opened \x1b[31mhttps://x\x1b[0m", Kind: ui.StatusOK})
	v := d.view()
	if !strings.Contains(v, "Opened https://x") {
		t.Errorf("status not shown/sanitised:\n%s", v)
	}
	d.send(clearStatusMsg{seq: d.m.statusSeq - 1})
	if d.m.status.Text == "" {
		t.Error("stale clear should be ignored")
	}
	d.send(clearStatusMsg{seq: d.m.statusSeq})
	if d.m.status.Text != "" {
		t.Error("clear should remove the status")
	}
}

func TestDemoIgnoresLoginRequired(t *testing.T) {
	d := demoDriver(t)
	d.send(ui.LoginRequiredMsg{})
	if d.m.login != nil {
		t.Error("demo mode must never show sign-in")
	}
}

func fakeJWT(claims map[string]any) string {
	b, _ := json.Marshal(claims)
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"none"}`)) + "." + enc(b) + ".sig"
}

func freePort(t *testing.T) int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestLoginFlow(t *testing.T) {
	ctx := uitest.Ctx(t)
	ctx.Demo = false
	store := &auth.Store{Path: filepath.Join(t.TempDir(), "tokens.json")}
	src := auth.NewSource(auth.Tokens{}, store, nil)
	d := newDriver(t, Options{Ctx: ctx, Source: src, Port: freePort(t)})
	defer func() {
		if d.m.login != nil {
			d.m.login.close()
		}
	}()
	if d.m.login == nil {
		t.Fatal("signed-out start should show the sign-in screen")
	}
	v := d.view()
	if !strings.Contains(v, "Sign in to HSE") || !strings.Contains(v, "saml.hse.ru") {
		t.Fatalf("sign-in screen:\n%s", v)
	}
	checkFits(t, d.m.View(), 110, 32)

	// Global keys are inert on the sign-in screen.
	d.keys("2")
	if d.m.active != 0 {
		t.Error("tabs must not switch while signing in")
	}

	// A failed attempt shows the error; r restarts.
	d.send(loginResultMsg{gen: d.m.login.gen, err: &auth.OAuthError{Code: "access_denied"}})
	if !strings.Contains(d.view(), "access_denied") {
		t.Errorf("error not shown:\n%s", d.view())
	}
	d.keys("r")
	if d.m.login == nil || d.m.login.session == nil {
		t.Fatal("r should restart the session")
	}
	// Results of abandoned attempts are ignored.
	d.send(loginResultMsg{gen: d.m.login.gen - 1, err: &auth.OAuthError{Code: "stale"}})
	if strings.Contains(d.view(), "stale") {
		t.Error("stale attempt result should be ignored")
	}

	tok := fakeJWT(map[string]any{"exp": time.Now().Add(time.Hour).Unix(), "sub": "u1", "email": "Student@edu.hse.ru", "name": "Иван Петров"})
	d.send(loginResultMsg{gen: d.m.login.gen, tokens: auth.Tokens{AccessToken: tok, RefreshToken: "r1", ExpiresAt: time.Now().Add(time.Hour)}})
	if d.m.login != nil {
		t.Fatal("successful sign-in should close the screen")
	}
	if d.m.ctx.Me.Email != "student@edu.hse.ru" || d.m.ctx.Me.Name != "Иван Петров" {
		t.Errorf("identity not applied: %+v", d.m.ctx.Me)
	}
	if _, ok, _ := store.Load(); !ok {
		t.Error("tokens not persisted")
	}
	if v := d.view(); !strings.Contains(v, "Иван Петров") {
		t.Errorf("header should show the user:\n%s", v)
	}

	// An auth failure later brings the screen back.
	d.send(ui.LoginRequiredMsg{})
	if d.m.login == nil {
		t.Error("LoginRequiredMsg should show sign-in again")
	}
	d.keys("q")
	if !d.quit {
		t.Error("q on the sign-in screen quits")
	}
}
