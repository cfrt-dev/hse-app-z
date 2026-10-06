package ui

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"hse-app-z/internal/api"
	"hse-app-z/internal/config"
)

// Cmd wraps a message as a command.
func Cmd(msg tea.Msg) tea.Cmd { return func() tea.Msg { return msg } }

// PushMsg asks the root to push a page on the active tab's stack.
type PushMsg struct{ Page Page }

// PopMsg asks the root to pop the top page (no-op on a tab root).
type PopMsg struct{}

// OpenTargetMsg asks the root to open the timetable/profile page of a
// person, group or room.
type OpenTargetMsg struct{ Target Target }

// LoginRequiredMsg makes the root show the sign-in screen.
type LoginRequiredMsg struct{}

// ReloadMsg is broadcast when data must be re-fetched (language switch,
// new sign-in). Pages that loaded data should reload.
type ReloadMsg struct{}

// FocusSearchMsg is sent to the search tab when the user presses / anywhere.
type FocusSearchMsg struct{}

// MenuMsg opens the action menu overlay.
type MenuMsg struct {
	Title   string
	Actions []Action
}

// StatusKind classifies footer messages.
type StatusKind int

const (
	StatusInfo StatusKind = iota
	StatusOK
	StatusWarn
	StatusError
)

// StatusMsg shows a transient message in the footer.
type StatusMsg struct {
	Text string
	Kind StatusKind
}

func Push(p Page) tea.Cmd         { return Cmd(PushMsg{Page: p}) }
func Pop() tea.Cmd                { return Cmd(PopMsg{}) }
func OpenTarget(t Target) tea.Cmd { return Cmd(OpenTargetMsg{Target: t}) }
func ShowMenu(title string, actions ...Action) tea.Cmd {
	return Cmd(MenuMsg{Title: title, Actions: actions})
}

func Info(format string, a ...any) tea.Cmd {
	return Cmd(StatusMsg{Text: fmt.Sprintf(format, a...), Kind: StatusInfo})
}
func OK(format string, a ...any) tea.Cmd {
	return Cmd(StatusMsg{Text: fmt.Sprintf(format, a...), Kind: StatusOK})
}
func Warn(format string, a ...any) tea.Cmd {
	return Cmd(StatusMsg{Text: fmt.Sprintf(format, a...), Kind: StatusWarn})
}

// ErrCmd reports an error: sign-in prompt for auth failures, footer message
// otherwise; nil for cancellations.
func ErrCmd(err error) tea.Cmd {
	switch {
	case err == nil, errors.Is(err, context.Canceled):
		return nil
	case errors.Is(err, api.ErrLoginRequired):
		return Cmd(LoginRequiredMsg{})
	}
	return Cmd(StatusMsg{Text: Clean(api.Friendly(err)), Kind: StatusError})
}

// SafeURL reports whether u is something we are willing to hand to the OS
// opener: only web, mail and phone links (API data must never make us open
// local files or custom schemes).
func SafeURL(u string) bool {
	u = strings.TrimSpace(u)
	for _, r := range u {
		// Control characters (C0 and C1), bidi overrides, zero-width and
		// other format characters, line separators and invalid UTF-8 have
		// no place in a link: they can disguise where it really goes.
		if r == utf8.RuneError || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) ||
			unicode.In(r, unicode.Zl, unicode.Zp) {
			return false
		}
	}
	p, err := url.Parse(u)
	if err != nil {
		return false
	}
	switch strings.ToLower(p.Scheme) {
	case "http", "https":
		return p.Host != ""
	case "mailto", "tel":
		return p.Opaque != "" || p.Path != ""
	}
	return false
}

// DryRun disables OS side effects (browser, clipboard); tests set it.
var DryRun bool

// OpenURL opens a link in the default browser.
func OpenURL(u string) tea.Cmd {
	u = strings.TrimSpace(u)
	return func() tea.Msg {
		if !SafeURL(u) {
			return StatusMsg{Text: Tr("Refusing to open link: ", "Небезопасная ссылка не открыта: ") + Clean(u), Kind: StatusError}
		}
		if DryRun {
			return StatusMsg{Text: Tr("Opened ", "Открыто: ") + u, Kind: StatusOK}
		}
		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "darwin":
			cmd = exec.Command("open", u)
		case "windows":
			cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
		default:
			cmd = exec.Command("xdg-open", u)
		}
		if err := cmd.Start(); err != nil {
			return StatusMsg{Text: Tr("Couldn't open browser — link: ", "Не удалось открыть браузер — ссылка: ") + u, Kind: StatusWarn}
		}
		go func() { _ = cmd.Wait() }()
		return StatusMsg{Text: Tr("Opened ", "Открыто: ") + Trunc(u, 60), Kind: StatusOK}
	}
}

// Copy puts text on the system clipboard (falls back to OSC 52, which most
// modern terminals support, including over SSH).
func Copy(text, what string) tea.Cmd {
	return func() tea.Msg {
		if strings.TrimSpace(text) == "" {
			return StatusMsg{Text: Tr("Nothing to copy", "Нечего копировать"), Kind: StatusWarn}
		}
		if DryRun {
			return StatusMsg{Text: Tr("Copied ", "Скопировано: ") + what + ": " + text, Kind: StatusOK}
		}
		for _, c := range clipboardCommands() {
			if _, err := exec.LookPath(c[0]); err != nil {
				continue
			}
			cmd := exec.Command(c[0], c[1:]...)
			cmd.Stdin = strings.NewReader(text)
			if cmd.Run() == nil {
				return StatusMsg{Text: Tr("Copied ", "Скопировано: ") + what, Kind: StatusOK}
			}
		}
		return osc52Msg{text: text, what: what}
	}
}

func clipboardCommands() [][]string {
	switch runtime.GOOS {
	case "darwin":
		return [][]string{{"pbcopy"}}
	case "windows":
		return [][]string{{"clip"}}
	}
	return [][]string{{"wl-copy"}, {"xclip", "-selection", "clipboard"}, {"xsel", "--clipboard", "--input"}}
}

// osc52Msg is handled by the root, which writes the escape sequence.
type osc52Msg struct{ text, what string }

// OSC52 returns the sequence and label for an osc52Msg (root helper).
func OSC52(msg tea.Msg) (seq, what string, ok bool) {
	m, ok := msg.(osc52Msg)
	if !ok {
		return "", "", false
	}
	return ansi.SetSystemClipboard(m.text), m.what, true
}

// MapURL links to a point on Yandex Maps (works in RU without extra apps).
func MapURL(lat, lng float64) string {
	return fmt.Sprintf("https://yandex.ru/maps/?pt=%.6f,%.6f&z=17&l=map", lng, lat)
}

// ------------------------------------------------------------- targets

// Target is something with a timetable: a person, a group or a room.
type Target struct {
	Kind     string // config.KindPerson, KindGroup, KindAuditorium
	Key      string // email (person) or RUZ id (group/room)
	Title    string
	Subtitle string
	// Person is the search hit / lecturer profile we came from, if any.
	Person *api.Person
}

// TargetFromPerson converts a search hit or lecturer profile. ok is false
// when the hit can't be opened (e.g. a person without an email).
func TargetFromPerson(p api.Person) (Target, bool) {
	pp := p
	switch {
	case p.IsGroup():
		if p.ID == "" {
			return Target{}, false
		}
		sub := Clean(p.ProgramName)
		if p.Course.OK {
			sub = JoinNonEmpty(" · ", Trf("course %d", "%d курс", p.Course.Int()), sub)
		}
		return Target{Kind: config.KindGroup, Key: string(p.ID), Title: Clean(p.DisplayName()), Subtitle: sub, Person: &pp}, true
	case p.IsAuditorium():
		if p.ID == "" {
			return Target{}, false
		}
		return Target{Kind: config.KindAuditorium, Key: string(p.ID), Title: Tr("Room ", "Ауд. ") + Clean(p.DisplayName()),
			Subtitle: JoinNonEmpty(" · ", Clean(p.AuditoriumType), Clean(p.Description)), Person: &pp}, true
	default:
		if strings.TrimSpace(p.Email) == "" {
			return Target{}, false
		}
		return Target{Kind: config.KindPerson, Key: strings.ToLower(strings.TrimSpace(p.Email)), Title: Clean(p.DisplayName()),
			Subtitle: Clean(p.Description), Person: &pp}, true
	}
}

// TargetFromFavourite restores a starred target.
func TargetFromFavourite(f config.Favourite) Target {
	return Target{Kind: f.Kind, Key: f.Key, Title: f.Title, Subtitle: f.Subtitle}
}

// Favourite converts the target for storage.
func (t Target) Favourite() config.Favourite {
	return config.Favourite{Kind: t.Kind, Key: t.Key, Title: t.Title, Subtitle: t.Subtitle}
}

// Query builds the lessons query for a date range.
func (t Target) Query() api.LessonQuery {
	switch t.Kind {
	case config.KindGroup:
		return api.LessonQuery{Group: t.Key}
	case config.KindAuditorium:
		return api.LessonQuery{Auditorium: t.Key}
	}
	return api.LessonQuery{Email: t.Key}
}

// ToggleFavourite stars/unstars t and reports the result in the footer.
func ToggleFavourite(ctx *Ctx, t Target) tea.Cmd {
	if ctx == nil || ctx.Favs == nil {
		return nil
	}
	on, err := ctx.Favs.Toggle(t.Favourite())
	if err != nil {
		return Cmd(StatusMsg{Text: Tr("Couldn't save favourites: ", "Не удалось сохранить избранное: ") + err.Error(), Kind: StatusError})
	}
	if on {
		return OK(Tr("★ Added %s to favourites", "★ %s добавлено в избранное"), t.Title)
	}
	return Info(Tr("Removed %s from favourites", "%s удалено из избранного"), t.Title)
}
