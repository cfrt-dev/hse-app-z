package app

import (
	"context"
	"errors"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"hse-app-z/internal/api"
	"hse-app-z/internal/auth"
	"hse-app-z/internal/ui"
)

// loginModel is the sign-in screen: it runs the loopback OAuth flow and
// waits for the browser to come back.
type loginModel struct {
	session *auth.LoginSession
	err     error
	busy    bool
	note    string
	// gen identifies the current attempt so results of abandoned attempts
	// are ignored.
	gen int
}

type loginResultMsg struct {
	gen    int
	tokens auth.Tokens
	err    error
}

type loginOpenedMsg struct{ note string }

func (m *Model) startLogin() tea.Cmd {
	if m.login == nil {
		m.login = &loginModel{}
	}
	m.menu, m.help = nil, false
	return m.login.restart(m)
}

func (l *loginModel) close() {
	if l.session != nil {
		l.session.Close()
		l.session = nil
	}
}

func (l *loginModel) restart(m *Model) tea.Cmd {
	l.close()
	l.gen++
	l.err, l.note = nil, ""
	s, err := auth.StartLogin(m.opts.AuthClient, m.opts.Port)
	if err != nil {
		l.err = err
		return nil
	}
	l.session = s
	l.busy = true
	gen := l.gen
	wait := func() tea.Msg {
		t, err := s.Wait(context.Background())
		return loginResultMsg{gen: gen, tokens: t, err: err}
	}
	return tea.Batch(wait, openBrowser(s.AuthURL))
}

func openBrowser(u string) tea.Cmd {
	open := ui.OpenURL(u)
	return func() tea.Msg {
		if st, ok := open().(ui.StatusMsg); ok && st.Kind != ui.StatusOK {
			return loginOpenedMsg{note: ui.Tr("Couldn't open a browser automatically — open the link below manually.", "Не удалось открыть браузер — откройте ссылку ниже вручную.")}
		}
		return loginOpenedMsg{note: ui.Tr("Opened the sign-in page in your browser.", "Страница входа открыта в браузере.")}
	}
}

func (m *Model) handleLoginResult(msg loginResultMsg) tea.Cmd {
	l := m.login
	if l == nil || msg.gen != l.gen {
		return nil
	}
	l.busy = false
	if msg.err != nil {
		if errors.Is(msg.err, context.Canceled) {
			return nil
		}
		l.err = msg.err
		l.close()
		return nil
	}
	status := ui.StatusMsg{Kind: ui.StatusOK}
	if err := m.opts.Source.Set(msg.tokens); err != nil {
		// Signed in for this run, but it won't survive a restart.
		status = ui.StatusMsg{Text: ui.Tr("Signed in, but couldn't save the session: ", "Вход выполнен, но сессию не удалось сохранить: ") + err.Error(), Kind: ui.StatusWarn}
	}
	ApplyIdentity(m.ctx, m.opts.Source)
	if status.Text == "" {
		status.Text = ui.Tr("Signed in as ", "Вы вошли как ") + ui.Clean(m.ctx.Me.Email)
	}
	l.close()
	m.login = nil
	// Pages that were already showing reload (the account may have changed);
	// the current page is initialised if it never was.
	reload := m.broadcast(ui.ReloadMsg{})
	return tea.Batch(m.setStatus(status), reload, m.initPage(m.top()))
}

// ApplyIdentity copies the signed-in user from the token into ctx and
// scopes the HTTP cache to that account.
func ApplyIdentity(ctx *ui.Ctx, src *auth.Source) {
	if src == nil {
		return
	}
	c := src.Claims()
	ctx.Me = ui.Me{Email: c.UserEmail(), Name: c.DisplayName()}
	ctx.API.SetCacheScope(c.Sub)
}

func (l *loginModel) update(m *Model, msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "q":
		// Not esc: this screen can pop up mid-session, and esc (back
		// everywhere else) must not quit the app by accident.
		return m.quit()
	case "enter", "o":
		if l.session != nil {
			return openBrowser(l.session.AuthURL)
		}
		return l.restart(m)
	case "c", "y":
		if l.session != nil {
			return ui.Copy(l.session.AuthURL, ui.Tr("sign-in link", "ссылку для входа"))
		}
	case "r":
		return l.restart(m)
	}
	return nil
}

func (l *loginModel) view(m *Model, width, height int) string {
	status := m.status
	w := min(76, width-4) // box width without the border
	tw := max(10, w-2)    // text width inside the box padding
	room := height - 3    // lines left after the border and the box title
	wrap := func(s string) []string { return strings.Split(ui.Wrap(s, tw), "\n") }
	styled := func(st lipgloss.Style, lines []string) []string {
		out := make([]string, len(lines))
		for i, s := range lines {
			out[i] = st.Render(s)
		}
		return out
	}
	key := ui.StyleKey.Render

	head := []string{ui.StyleSection.Render(ui.Tr("Sign in to HSE", "Вход в аккаунт ВШЭ"))}
	intro := wrap(ui.Tr("HSE App Z signs in with your HSE account in the browser (saml.hse.ru), the same way the official app does. Your password never touches this program.", "HSE App Z входит через ваш аккаунт ВШЭ в браузере (saml.hse.ru), так же как официальное приложение. Пароль в эту программу не попадает."))
	var note, main, link, keys []string
	switch {
	case l.err != nil:
		msg := l.err.Error()
		if errors.Is(l.err, auth.ErrPortInUse) {
			port := m.opts.Port
			if port == 0 {
				port = auth.DefaultPort
			}
			msg = ui.Trf("Port %d is busy — is another copy of hse-app-z (or getRefreshToken.py) running? Close it and press r.", "Порт %d занят — возможно, запущена другая копия hse-app-z (или getRefreshToken.py). Закройте её и нажмите r.", port)
		} else if errors.Is(l.err, api.ErrLoginRequired) || strings.Contains(msg, "invalid_grant") {
			msg = ui.Tr("The sign-in code was rejected (it may have expired). Press r to try again.", "Код входа отклонён (возможно, истёк). Нажмите r, чтобы попробовать снова.")
		}
		main = styled(ui.StyleErr, wrap(ui.Clean(msg)))
		keys = wrap(key("r") + ui.Tr(" try again   ", " повторить   ") + key("q") + ui.Tr(" quit", " выход"))
	case l.session != nil:
		if l.note != "" {
			note = styled(ui.StyleDim, wrap(l.note))
		}
		main = []string{ui.StyleDim.Render(ui.Tr("Waiting for the browser…", "Ожидание браузера…"))}
		// Hard-wrapped: a URL has no words to break at ("app-x-android").
		url := strings.Split(ansi.Hardwrap(l.session.AuthURL, tw, false), "\n")
		link = append([]string{ui.StyleDim.Render(ui.Tr("Sign-in link:", "Ссылка для входа:"))}, styled(ui.StyleLink, url)...)
		keys = wrap(key("enter") + ui.Tr(" open browser  ", " открыть браузер  ") + key("c") + ui.Tr(" copy link  ", " копировать  ") + key("r") + ui.Tr(" restart  ", " заново  ") + key("q") + ui.Tr(" quit", " выход"))
	default:
		main = []string{ui.StyleDim.Render(ui.Tr("Starting…", "Запуск…"))}
	}
	var stat []string
	if t := ui.Clean(status.Text); t != "" {
		st := ui.StyleDim
		switch status.Kind {
		case ui.StatusOK:
			st = ui.StyleOK
		case ui.StatusWarn:
			st = ui.StyleWarn
		case ui.StatusError:
			st = ui.StyleErr
		}
		stat = []string{st.Render(ui.Trunc(t, tw))}
	}

	// Drop the least important parts until everything fits: the intro,
	// the blank lines between parts, the note; finally shorten the link
	// (c copies it whole).
	build := func(withIntro, withNote, gaps bool, linkLines int) []string {
		var out []string
		for _, b := range [][]string{head, pick(withIntro, intro), pick(withNote, note), main, clip(link, linkLines, tw), keys, stat} {
			if len(b) == 0 {
				continue
			}
			if gaps && len(out) > 0 {
				out = append(out, "")
			}
			out = append(out, b...)
		}
		return out
	}
	var lines []string
	for _, v := range [][3]bool{{true, true, true}, {false, true, true}, {false, false, true}, {false, true, false}, {false, false, false}} {
		if lines = build(v[0], v[1], v[2], len(link)); len(lines) <= room {
			break
		}
	}
	if len(lines) > room && len(link) > 0 {
		rest := len(build(false, false, false, 0))
		lines = build(false, false, false, max(0, room-rest))
	}
	return ui.Overlay("", ui.Box("HSE App Z", strings.Join(lines, "\n"), w), width, height)
}

func pick(ok bool, lines []string) []string {
	if ok {
		return lines
	}
	return nil
}

// clip keeps the first n lines of a block, marking the cut with "…"; a
// block of a heading plus less than one line of content is dropped.
func clip(lines []string, n, width int) []string {
	if n >= len(lines) {
		return lines
	}
	if n < 2 {
		return nil
	}
	out := append([]string(nil), lines[:n]...)
	out[n-1] = ui.Trunc(out[n-1], width-1) + ui.StyleDim.Render("…")
	return out
}
