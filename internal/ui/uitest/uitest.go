// Package uitest drives pages headlessly against the recorded fixtures:
// it runs commands synchronously, feeds results back, and renders views
// without ANSI codes so tests (and humans) can inspect them.
package uitest

import (
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"hse-app-z/internal/api"
	"hse-app-z/internal/config"
	"hse-app-z/internal/ui"
)

// Now is the fixed demo clock: Tuesday 13 Oct 2026, 12:30 Moscow — inside
// the recorded timetable range.
var Now = time.Date(2026, 10, 13, 12, 30, 0, 0, api.Moscow)

// FixturesDir is internal/api/testdata.
func FixturesDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "api", "testdata")
}

// Ctx returns a demo context backed by fixtures, with an in-memory
// favourites store and side effects (browser, clipboard) disabled.
func Ctx(t testing.TB) *ui.Ctx {
	ui.DryRun = true
	client := api.NewClient("http://fixtures.invalid", nil, api.NewCache(""))
	client.HTTP = &http.Client{Transport: &api.FixtureTransport{Dir: FixturesDir()}}
	return &ui.Ctx{
		API:      client,
		Settings: config.LoadSettings(""),
		Favs:     config.LoadFavourites(""),
		Me:       ui.Me{Email: api.DemoEmail, Name: "Demo Student"},
		Now:      func() time.Time { return Now },
		Demo:     true,
	}
}

// Harness wraps a page.
type Harness struct {
	T    testing.TB
	Page ui.Page
	// Emitted collects messages addressed to the root (status, push,
	// open-target, menu, login) plus everything else the page produced.
	Emitted []tea.Msg
}

// New initialises p and settles all resulting commands.
func New(t testing.TB, p ui.Page) *Harness {
	h := &Harness{T: t, Page: p}
	h.run(p.Init(), 0)
	return h
}

// Send delivers msg and settles.
func (h *Harness) Send(msg tea.Msg) {
	h.run(h.Page.Update(msg), 0)
}

// Key sends key presses, e.g. Key("j", "j", "enter", "ctrl+d", "esc").
// Plain strings longer than one rune that aren't key names are typed
// rune by rune (Key("Rezni") types five characters).
func (h *Harness) Key(keys ...string) {
	for _, k := range keys {
		for _, km := range KeyMsgs(k) {
			h.Send(km)
		}
	}
}

var named = map[string]tea.KeyType{
	"enter": tea.KeyEnter, "esc": tea.KeyEsc, "tab": tea.KeyTab, "shift+tab": tea.KeyShiftTab,
	"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight,
	"shift+up": tea.KeyShiftUp, "shift+down": tea.KeyShiftDown,
	"home": tea.KeyHome, "end": tea.KeyEnd, "pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown,
	"backspace": tea.KeyBackspace, "delete": tea.KeyDelete, "space": tea.KeySpace,
	"ctrl+d": tea.KeyCtrlD, "ctrl+u": tea.KeyCtrlU, "ctrl+f": tea.KeyCtrlF, "ctrl+b": tea.KeyCtrlB,
	"ctrl+e": tea.KeyCtrlE, "ctrl+y": tea.KeyCtrlY, "ctrl+n": tea.KeyCtrlN, "ctrl+p": tea.KeyCtrlP,
	"ctrl+w": tea.KeyCtrlW, "ctrl+a": tea.KeyCtrlA, "ctrl+k": tea.KeyCtrlK, "ctrl+l": tea.KeyCtrlL,
}

// KeyMsgs converts a key name or literal text into key messages.
func KeyMsgs(k string) []tea.KeyMsg {
	if t, ok := named[k]; ok {
		return []tea.KeyMsg{{Type: t}}
	}
	var out []tea.KeyMsg
	for _, r := range k {
		out = append(out, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return out
}

func rootMsg(msg tea.Msg) bool {
	switch msg.(type) {
	case ui.StatusMsg, ui.PushMsg, ui.PopMsg, ui.OpenTargetMsg, ui.MenuMsg, ui.LoginRequiredMsg:
		return true
	}
	return false
}

func (h *Harness) run(cmd tea.Cmd, depth int) {
	if cmd == nil || depth > 64 {
		return
	}
	msg := runWithTimeout(cmd, 3*time.Second)
	if msg == nil {
		return
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			h.run(c, depth+1)
		}
		return
	}
	h.Emitted = append(h.Emitted, msg)
	if rootMsg(msg) {
		return
	}
	h.run(h.Page.Update(msg), depth+1)
}

// runWithTimeout executes cmd, giving up on long timers (tea.Tick etc.).
func runWithTimeout(cmd tea.Cmd, d time.Duration) tea.Msg {
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	select {
	case m := <-ch:
		return m
	case <-time.After(d):
		return nil
	}
}

// View renders the page and strips ANSI codes.
func (h *Harness) View(w, ht int) string {
	return ansi.Strip(h.Page.View(w, ht))
}

// Statuses returns the footer messages emitted so far.
func (h *Harness) Statuses() []string {
	var out []string
	for _, m := range h.Emitted {
		if s, ok := m.(ui.StatusMsg); ok {
			out = append(out, s.Text)
		}
	}
	return out
}

// LastMenu returns the most recent action menu, if any.
func (h *Harness) LastMenu() (ui.MenuMsg, bool) {
	for i := len(h.Emitted) - 1; i >= 0; i-- {
		if m, ok := h.Emitted[i].(ui.MenuMsg); ok {
			return m, true
		}
	}
	return ui.MenuMsg{}, false
}

// RunAction runs the action of the last menu whose label contains label.
func (h *Harness) RunAction(label string) bool {
	m, ok := h.LastMenu()
	if !ok {
		return false
	}
	for _, a := range m.Actions {
		if strings.Contains(strings.ToLower(a.Label), strings.ToLower(label)) && a.Run != nil {
			h.run(a.Run(), 0)
			return true
		}
	}
	return false
}

// LastPushed returns the most recently pushed page, if any.
func (h *Harness) LastPushed() ui.Page {
	for i := len(h.Emitted) - 1; i >= 0; i-- {
		if m, ok := h.Emitted[i].(ui.PushMsg); ok {
			return m.Page
		}
	}
	return nil
}

// LastTarget returns the most recent open-target request, if any.
func (h *Harness) LastTarget() (ui.Target, bool) {
	for i := len(h.Emitted) - 1; i >= 0; i-- {
		if m, ok := h.Emitted[i].(ui.OpenTargetMsg); ok {
			return m.Target, true
		}
	}
	return ui.Target{}, false
}
