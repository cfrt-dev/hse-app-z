// Package ui holds the shared building blocks of the TUI: the Page contract
// every screen implements, cross-page messages, widgets (List, Scroll,
// Menu), layout helpers and styles. Feature screens live in sub-packages.
package ui

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"hse-app-z/internal/api"
	"hse-app-z/internal/config"
)

// Page is implemented by every tab and every pushed screen. Pages are
// pointers and mutate themselves in Update.
//
// Message routing (done by the root model):
//   - tea.KeyMsg goes only to the page on top of the active tab's stack, and
//     only after global keys (1-8, tab, q, ?, /, L, esc-to-pop) are handled —
//     unless Capturing() is true, in which case the page gets every key
//     except ctrl+c.
//   - Every other message is broadcast to all pages, so async results must
//     carry the page's ID (see NewID) and be filtered.
type Page interface {
	// Init runs once, the first time the page is shown. Start loading here.
	Init() tea.Cmd
	// Update handles a message and returns a follow-up command.
	Update(msg tea.Msg) tea.Cmd
	// View renders the page into width×height cells. The root clips and
	// pads the result, but pages should respect the size.
	View(width, height int) string
	// Title is used for the tab label and breadcrumbs.
	Title() string
	// Hints are shown in the footer (first few) and in the ? overlay (all).
	Hints() []Hint
	// Capturing is true while a text input has focus.
	Capturing() bool
}

// Hint documents one key binding.
type Hint struct {
	Key  string
	Desc string
}

var idCounter atomic.Int64

// NewID returns a process-unique id for tagging async messages.
func NewID() int { return int(idCounter.Add(1)) }

// Me is the signed-in user.
type Me struct {
	Email string
	Name  string
}

// Ctx is shared by all pages.
type Ctx struct {
	API      *api.Client
	Settings *config.Settings
	Favs     *config.Favourites
	Me       Me
	// Now returns the current time (fixed in demo mode).
	Now func() time.Time
	// Demo is true when running against recorded fixtures.
	Demo bool
}

// Time returns Now() (time.Now if unset).
func (c *Ctx) Time() time.Time {
	if c != nil && c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Today is midnight of the current local day.
func (c *Ctx) Today() time.Time {
	t := c.Time()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// RequestTimeout bounds every API call made by pages.
const RequestTimeout = 30 * time.Second

// Result is the message produced by Fetch.
type Result[T any] struct {
	ID   int
	Seq  int
	Data T
	Meta api.Meta
	Err  error
}

// Fetch runs fn off the UI goroutine and delivers a Result[T] tagged with
// id and seq. Use seq to drop responses that were superseded.
func Fetch[T any](id, seq int, fn func(ctx context.Context) (T, api.Meta, error)) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), RequestTimeout)
		defer cancel()
		data, meta, err := fn(ctx)
		if err != nil && errors.Is(err, context.DeadlineExceeded) {
			err = &api.NetworkError{Err: err}
		}
		return Result[T]{ID: id, Seq: seq, Data: data, Meta: meta, Err: err}
	}
}

// Load tracks one async data source of a page.
type Load struct {
	Seq     int
	Loading bool
	Loaded  bool // at least one successful load
	Err     error
	Meta    api.Meta
}

// Begin starts a new request and returns its sequence number. Previous data
// and errors are kept until the result arrives (no flicker on refresh).
func (l *Load) Begin() int {
	l.Seq++
	l.Loading = true
	return l.Seq
}

// Accept reports whether a result with seq is the latest one.
func (l *Load) Accept(seq int) bool { return seq == l.Seq }

// Done records the outcome of the latest request and returns a command
// for global side effects (sign-in prompt on auth failure).
func (l *Load) Done(meta api.Meta, err error) tea.Cmd {
	l.Loading = false
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil
		}
		l.Err = err
		if errors.Is(err, api.ErrLoginRequired) {
			return Cmd(LoginRequiredMsg{})
		}
		return nil
	}
	l.Err = nil
	l.Loaded = true
	l.Meta = meta
	return nil
}
