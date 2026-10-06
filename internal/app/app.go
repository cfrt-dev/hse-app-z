// Package app is the root Bubble Tea model: tabs, per-tab page stacks,
// overlays (action menu, help), the footer, and the sign-in screen.
package app

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"

	"hse-app-z/internal/auth"
	"hse-app-z/internal/ui"
	"hse-app-z/internal/ui/avatar"
	"hse-app-z/internal/ui/campus"
	"hse-app-z/internal/ui/food"
	"hse-app-z/internal/ui/grades"
	"hse-app-z/internal/ui/news"
	"hse-app-z/internal/ui/rating"
	"hse-app-z/internal/ui/search"
	"hse-app-z/internal/ui/services"
	"hse-app-z/internal/ui/timetable"
)

const (
	minWidth  = 60
	minHeight = 14
	searchTab = 3
)

// Options configure the root model.
type Options struct {
	Ctx        *ui.Ctx
	Source     *auth.Source // nil in demo mode
	AuthClient *auth.Client
	Port       int // OAuth callback port
	// Notice is shown once in the footer at startup (e.g. why avatars
	// are off).
	Notice string
}

// Model is the root model.
type Model struct {
	ctx  *ui.Ctx
	opts Options

	tabs   []ui.Page
	stacks [][]ui.Page
	inited map[ui.Page]bool
	active int

	width, height int

	menu   *ui.Menu
	help   bool
	status ui.StatusMsg
	// statusSeq invalidates pending clear timers when a newer status arrives.
	statusSeq int

	login *loginModel

	// uploadPending is set by View when an avatar upload is in the frame;
	// Update then schedules one redraw so it can be dropped again.
	uploadPending, settleScheduled bool
}

type clearStatusMsg struct{ seq int }

// clockMsg re-renders periodically so relative times ("starts in 5 min",
// "open · until 21:00") stay current without any data refetch.
type clockMsg struct{}

const clockInterval = 30 * time.Second

func clockTick() tea.Cmd {
	return tea.Tick(clockInterval, func(time.Time) tea.Msg { return clockMsg{} })
}

// New builds the root model.
func New(opts Options) *Model {
	ctx := opts.Ctx
	m := &Model{
		ctx:  ctx,
		opts: opts,
		tabs: []ui.Page{
			timetable.NewTab(ctx),
			grades.New(ctx),
			rating.New(ctx),
			search.New(ctx),
			food.New(ctx),
			campus.New(ctx),
			services.New(ctx),
			news.New(ctx),
		},
		inited: map[ui.Page]bool{},
	}
	m.stacks = make([][]ui.Page, len(m.tabs))
	return m
}

func (m *Model) Init() tea.Cmd {
	title := tea.SetWindowTitle("HSE App Z")
	if m.opts.Notice != "" {
		title = tea.Batch(title, m.setStatus(ui.StatusMsg{Text: m.opts.Notice, Kind: ui.StatusWarn}))
	}
	if m.needsLogin() {
		return tea.Batch(title, clockTick(), m.startLogin())
	}
	return tea.Batch(title, clockTick(), m.initPage(m.top()))
}

func (m *Model) needsLogin() bool {
	return !m.ctx.Demo && (m.opts.Source == nil || !m.opts.Source.SignedIn())
}

func (m *Model) top() ui.Page {
	if s := m.stacks[m.active]; len(s) > 0 {
		return s[len(s)-1]
	}
	return m.tabs[m.active]
}

func (m *Model) initPage(p ui.Page) tea.Cmd {
	if p == nil || m.inited[p] {
		return nil
	}
	m.inited[p] = true
	return p.Init()
}

func (m *Model) livePages() []ui.Page {
	var out []ui.Page
	for i, t := range m.tabs {
		if m.inited[t] {
			out = append(out, t)
		}
		out = append(out, m.stacks[i]...)
	}
	return out
}

func (m *Model) broadcast(msg tea.Msg) tea.Cmd {
	var cmds []tea.Cmd
	for _, p := range m.livePages() {
		cmds = append(cmds, p.Update(msg))
	}
	return tea.Batch(cmds...)
}

func (m *Model) setStatus(s ui.StatusMsg) tea.Cmd {
	m.status = s
	m.statusSeq++
	seq := m.statusSeq
	d := 4 * time.Second
	if s.Kind == ui.StatusError {
		d = 8 * time.Second
	}
	return tea.Tick(d, func(time.Time) tea.Msg { return clearStatusMsg{seq: seq} })
}

func (m *Model) switchTab(i int) tea.Cmd {
	if i < 0 || i >= len(m.tabs) {
		return nil
	}
	m.active = i
	return m.initPage(m.top())
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	model, cmd := m.update(msg)
	if m.uploadPending && !m.settleScheduled {
		m.settleScheduled = true
		cmd = tea.Batch(cmd, tea.Tick(avatar.SettleAfter, func(time.Time) tea.Msg { return avatar.SettleMsg{} }))
	}
	return model, cmd
}

func (m *Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case avatar.SettleMsg:
		m.settleScheduled = false
		return m, nil

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		// tmux may have been reattached from another terminal window,
		// which doesn't have our images: upload visible ones again.
		avatar.Default.Reupload()
		return m, nil

	case tea.KeyMsg:
		return m, m.handleKey(msg)

	case clockMsg:
		return m, clockTick()

	case clearStatusMsg:
		if msg.seq == m.statusSeq {
			m.status = ui.StatusMsg{}
		}
		return m, nil

	case ui.StatusMsg:
		return m, m.setStatus(msg)

	case ui.PushMsg:
		if msg.Page == nil {
			return m, nil
		}
		m.menu = nil
		m.stacks[m.active] = append(m.stacks[m.active], msg.Page)
		return m, m.initPage(msg.Page)

	case ui.PopMsg:
		m.pop()
		return m, nil

	case ui.OpenTargetMsg:
		m.menu = nil
		p := timetable.NewTargetPage(m.ctx, msg.Target)
		m.stacks[m.active] = append(m.stacks[m.active], p)
		return m, m.initPage(p)

	case ui.MenuMsg:
		if len(msg.Actions) > 0 {
			m.menu = ui.NewMenu(msg.Title, msg.Actions)
			if len(m.menu.Actions) == 0 {
				m.menu = nil
			}
		}
		return m, nil

	case ui.LoginRequiredMsg:
		if m.ctx.Demo || m.login != nil {
			return m, nil
		}
		return m, m.startLogin()

	case loginResultMsg:
		return m, m.handleLoginResult(msg)

	case loginOpenedMsg:
		if m.login != nil {
			m.login.note = msg.note
		}
		return m, nil
	}

	if seq, what, ok := ui.OSC52(msg); ok {
		if !writeTerminal(seq) {
			return m, m.setStatus(ui.StatusMsg{Text: ui.Tr("Couldn't copy ", "Не удалось скопировать: ") + what + ui.Tr(": no clipboard tool (pbcopy, wl-copy, xclip, xsel) and no terminal to ask", " — нет pbcopy, wl-copy, xclip или xsel и нет доступа к терминалу"), Kind: ui.StatusWarn})
		}
		return m, m.setStatus(ui.StatusMsg{Text: ui.Tr("Copied ", "Скопировано: ") + what + ui.Tr(" (via terminal)", " (через терминал)"), Kind: ui.StatusOK})
	}

	return m, m.broadcast(msg)
}

// writeTerminal sends an escape sequence (OSC 52 clipboard) straight to
// the terminal without disturbing the renderer's stdout stream: through
// stderr when that is the terminal, else through /dev/tty (stderr may be
// redirected to a log, and then the copy would silently go nowhere).
var writeTerminal = func(seq string) bool {
	if term.IsTerminal(os.Stderr.Fd()) {
		_, err := os.Stderr.WriteString(seq)
		return err == nil
	}
	f, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		return false
	}
	defer f.Close()
	_, err = f.WriteString(seq)
	return err == nil
}

func (m *Model) pop() {
	s := m.stacks[m.active]
	if len(s) == 0 {
		return
	}
	p := s[len(s)-1]
	m.stacks[m.active] = s[:len(s)-1]
	delete(m.inited, p)
}

func (m *Model) handleKey(msg tea.KeyMsg) tea.Cmd {
	key := msg.String()
	if key == "ctrl+c" {
		return m.quit()
	}
	if m.login != nil {
		return m.login.update(m, msg)
	}
	if m.help {
		m.help = false
		return nil
	}
	if m.menu != nil {
		cmd, done := m.menu.Update(msg)
		if done {
			m.menu = nil
		}
		return cmd
	}
	top := m.top()
	if top.Capturing() {
		return top.Update(msg)
	}
	switch key {
	case "q":
		if len(m.stacks[m.active]) > 0 {
			m.pop()
			return nil
		}
		return m.quit()
	case "esc", "backspace":
		if len(m.stacks[m.active]) > 0 {
			m.pop()
			return nil
		}
	case "tab":
		return m.switchTab((m.active + 1) % len(m.tabs))
	case "shift+tab":
		return m.switchTab((m.active + len(m.tabs) - 1) % len(m.tabs))
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		// Digits belong to the root even without a tab behind them.
		return m.switchTab(int(key[0] - '1'))
	case "?":
		m.help = true
		return nil
	case "/":
		for _, p := range m.stacks[searchTab] {
			delete(m.inited, p)
		}
		m.stacks[searchTab] = nil
		cmd := m.switchTab(searchTab)
		return tea.Batch(cmd, m.tabs[searchTab].Update(ui.FocusSearchMsg{}))
	case "L":
		lang := "ru"
		if m.ctx.Settings.GetLang() == "ru" {
			lang = "en"
		}
		m.ctx.Settings.SetLang(lang)
		m.ctx.API.SetLang(lang)
		ui.SetLang(lang)
		text := map[string]string{"en": "Language: English", "ru": "Язык: русский"}[lang]
		return tea.Batch(m.setStatus(ui.StatusMsg{Text: text, Kind: ui.StatusOK}), m.broadcast(ui.ReloadMsg{}))
	}
	return top.Update(msg)
}

func (m *Model) quit() tea.Cmd {
	if m.login != nil {
		m.login.close()
	}
	return tea.Quit
}

// ------------------------------------------------------------------ view

func (m *Model) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	if m.width < minWidth || m.height < minHeight {
		return ui.Placeholder(m.width, m.height, ui.Trf("Terminal too small (%d×%d).\nResize to at least %d×%d.", "Окно терминала слишком маленькое (%d×%d).\nНужно хотя бы %d×%d.", m.width, m.height, minWidth, minHeight))
	}
	if m.login != nil {
		return m.login.view(m, m.width, m.height)
	}
	header := m.headerView()
	footer := m.footerView()
	bodyH := m.height - lipgloss.Height(header) - lipgloss.Height(footer)
	var crumb string
	if len(m.stacks[m.active]) > 0 {
		crumb = m.crumbView()
		bodyH--
	}
	body := ui.Fit(m.top().View(m.width, bodyH), m.width, bodyH)
	// Avatar uploads (zero-width graphics commands) ride on the header line:
	// it rarely changes, so the renderer writes them once. They must be
	// collected after the body rendered (that's when images are requested).
	tx, pending := avatar.Default.Transmissions()
	m.uploadPending = pending
	header = tx + header
	if m.menu != nil {
		body = ui.Overlay(body, m.menu.View(m.width, bodyH), m.width, bodyH)
	} else if m.help {
		body = ui.Overlay(body, ui.HelpView(m.helpSections(), m.width, bodyH), m.width, bodyH)
	}
	parts := []string{header}
	if crumb != "" {
		parts = append(parts, crumb)
	}
	parts = append(parts, body, footer)
	return strings.Join(parts, "\n")
}

func (m *Model) headerView() string {
	brand := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(lipgloss.Color("#1F4FD8")).Padding(0, 1).Render("HSE")
	user := ""
	if m.ctx.Me.Name != "" || m.ctx.Me.Email != "" {
		user = m.ctx.Me.Name
		if user == "" {
			user = m.ctx.Me.Email
		}
	}
	if m.ctx.Demo {
		user = ui.JoinNonEmpty(" ", ui.StyleWarn.Render(ui.Tr("demo", "демо")), user)
	}
	user = ui.StyleDim.Render(ui.Clean(user))

	render := func(compact bool) string {
		var parts []string
		for i, t := range m.tabs {
			label := t.Title()
			if compact && i != m.active {
				label = ""
			}
			s := fmt.Sprintf("%d", i+1)
			if label != "" {
				s += " " + label
			}
			if i == m.active {
				parts = append(parts, lipgloss.NewStyle().Bold(true).Foreground(ui.ColorAccent).Underline(true).Render(s))
			} else {
				parts = append(parts, ui.StyleDim.Render(s))
			}
		}
		return brand + " " + strings.Join(parts, "  ")
	}
	tabs := render(false)
	if ui.Width(tabs)+ui.Width(user)+2 > m.width {
		tabs = render(true)
	}
	if ui.Width(tabs)+ui.Width(user)+2 > m.width {
		user = ""
	}
	gap := m.width - ui.Width(tabs) - ui.Width(user)
	line := tabs + strings.Repeat(" ", max(1, gap)) + user
	return ui.Trunc(line, m.width) + "\n" + ui.StyleSep.Render(strings.Repeat("─", m.width))
}

func (m *Model) crumbView() string {
	parts := []string{m.tabs[m.active].Title()}
	for _, p := range m.stacks[m.active] {
		parts = append(parts, ui.Clean(p.Title()))
	}
	return ui.Trunc(ui.StyleDim.Render("‹ esc  ")+ui.StyleDim.Render(strings.Join(parts[:len(parts)-1], " › ")+" › ")+ui.StyleTitle.Render(parts[len(parts)-1]), m.width)
}

func (m *Model) footerView() string {
	sep := ui.StyleSep.Render(strings.Repeat("─", m.width))
	if m.status.Text != "" {
		st := ui.StyleDim
		switch m.status.Kind {
		case ui.StatusOK:
			st = ui.StyleOK
		case ui.StatusWarn:
			st = ui.StyleWarn
		case ui.StatusError:
			st = ui.StyleErr
		}
		return sep + "\n" + st.Render(ui.Trunc(ui.Clean(m.status.Text), m.width))
	}
	var hints []string
	for _, h := range m.top().Hints() {
		hints = append(hints, ui.StyleKey.Render(h.Key)+" "+ui.StyleDim.Render(h.Desc))
	}
	right := ui.StyleKey.Render("?") + ui.StyleDim.Render(ui.Tr(" help  ", " справка  ")) + ui.StyleKey.Render("q") + ui.StyleDim.Render(" ")
	if len(m.stacks[m.active]) > 0 {
		right += ui.StyleDim.Render(ui.Tr("back", "назад"))
	} else {
		right += ui.StyleDim.Render(ui.Tr("quit", "выход"))
	}
	if m.top().Capturing() {
		// A text field gets every key: ? and q would be typed, not run.
		right = ui.StyleKey.Render("ctrl+c") + ui.StyleDim.Render(ui.Tr(" quit", " выход"))
	}
	left := ""
	avail := m.width - ui.Width(right) - 2
	for _, h := range hints {
		next := left
		if next != "" {
			next += ui.StyleDim.Render(" · ")
		}
		next += h
		if ui.Width(next) > avail {
			break
		}
		left = next
	}
	gap := m.width - ui.Width(left) - ui.Width(right)
	return sep + "\n" + left + strings.Repeat(" ", max(1, gap)) + right
}

func (m *Model) helpSections() []ui.HelpSection {
	global := []ui.Hint{
		{Key: "1-8 / tab", Desc: ui.Tr("switch tab (shift+tab back)", "вкладки (shift+tab — назад)")},
		{Key: "j/k ↓/↑", Desc: ui.Tr("move", "вверх / вниз")},
		{Key: "g/G", Desc: ui.Tr("first / last", "в начало / в конец")},
		{Key: "ctrl+d/ctrl+u", Desc: ui.Tr("half page down / up", "полстраницы вниз / вверх")},
		{Key: "J/K", Desc: ui.Tr("scroll the detail pane", "прокрутка правой панели")},
		{Key: "enter", Desc: ui.Tr("actions for the selected item", "действия с выбранным")},
		{Key: "esc / q", Desc: ui.Tr("back (q quits on a tab)", "назад (q на вкладке — выход)")},
		{Key: "/", Desc: ui.Tr("search people, groups, rooms", "поиск людей, групп, аудиторий")},
		{Key: "r", Desc: ui.Tr("refresh", "обновить")},
		{Key: "L", Desc: ui.Tr("switch language (English / Русский)", "сменить язык (English / Русский)")},
		{Key: "ctrl+c", Desc: ui.Tr("quit", "выход")},
	}
	return []ui.HelpSection{
		{Title: ui.Clean(m.top().Title()), Hints: m.top().Hints()},
		{Title: ui.Tr("Everywhere", "Везде"), Hints: global},
	}
}
