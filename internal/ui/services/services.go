// Package services is the Services tab: the SmartPoint service catalogue
// (folders and links) and the user's own requests.
package services

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"hse-app-z/internal/api"
	"hse-app-z/internal/ui"
)

// Page lists the services of one category ("" = the root catalogue). The
// root also offers the user's requests.
type Page struct {
	ctx      *ui.Ctx
	id       int
	category string
	// title is the page title from the catalogue ("" = the root, or a
	// folder without a name: see Title).
	title string
	// svc is the folder this page browses (nil for the root); it names the
	// page when title is empty.
	svc *api.Service
	// path holds the categories of this page and all its ancestors, so a
	// folder pointing back at one of them is not followed (no loops).
	path map[string]bool

	svcLoad   ui.Load
	tasksLoad ui.Load
	services  []api.Service
	tasks     *api.TasksPage // root only; nil until loaded

	list   ui.List
	scroll ui.Scroll

	// tasksWho is the account the requests were last fetched for.
	tasksWho     string
	tasksFetched bool
}

// New returns the tab page (the root of the catalogue).
func New(ctx *ui.Ctx) ui.Page { return newPage(ctx, "", "", nil) }

func newPage(ctx *ui.Ctx, category, title string, parent map[string]bool) *Page {
	path := map[string]bool{}
	for k := range parent {
		path[k] = true
	}
	if c := normCat(category); c != "" {
		path[c] = true
	}
	return &Page{ctx: ctx, id: ui.NewID(), category: category, title: ui.Clean(title), path: path}
}

func normCat(c string) string { return strings.TrimRight(strings.TrimSpace(c), "/") }

func (p *Page) root() bool      { return p.category == "" }
func (p *Page) Capturing() bool { return false }

// Title is the folder's name, or "Services" (built at call time, so it
// follows the interface language).
func (p *Page) Title() string {
	if p.title != "" {
		return p.title
	}
	if p.svc != nil {
		return serviceName(*p.svc)
	}
	return ui.Tr("Services", "Сервисы")
}

func (p *Page) Hints() []ui.Hint { return hints() }

// hints are shared by the catalogue and the requests page.
func hints() []ui.Hint {
	return []ui.Hint{
		{Key: "enter", Desc: ui.Tr("open", "открыть")},
		{Key: "y", Desc: ui.Tr("copy link", "копировать ссылку")},
		{Key: "r", Desc: ui.Tr("refresh", "обновить")},
		{Key: "o", Desc: ui.Tr("open link", "открыть ссылку")},
		{Key: "J/K", Desc: ui.Tr("scroll details", "прокрутка деталей")},
	}
}

func (p *Page) Init() tea.Cmd { return p.fetchAll() }

func (p *Page) fetchAll() tea.Cmd { return tea.Batch(p.fetchServices(), p.fetchTasks()) }

func (p *Page) fetchServices() tea.Cmd {
	if p.ctx == nil || p.ctx.API == nil {
		return nil
	}
	client, cat := p.ctx.API, p.category
	return ui.Fetch(p.id, p.svcLoad.Begin(), func(c context.Context) ([]api.Service, api.Meta, error) {
		return client.Services(c, cat)
	})
}

func (p *Page) fetchTasks() tea.Cmd {
	if !p.root() || p.ctx == nil || p.ctx.API == nil {
		return nil
	}
	client := p.ctx.API
	p.tasksWho, p.tasksFetched = p.ctx.Me.Email, true
	return ui.Fetch(p.id, p.tasksLoad.Begin(), func(c context.Context) (api.TasksPage, api.Meta, error) {
		return client.Tasks(c, "")
	})
}

func (p *Page) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case ui.Result[[]api.Service]:
		if msg.ID != p.id || !p.svcLoad.Accept(msg.Seq) {
			return nil
		}
		cmd := p.svcLoad.Done(msg.Meta, msg.Err)
		if msg.Err == nil {
			key := p.selectedKey()
			p.services = msg.Data
			p.relist(key)
		}
		return cmd
	case ui.Result[api.TasksPage]:
		if msg.ID != p.id || !p.tasksLoad.Accept(msg.Seq) {
			return nil
		}
		cmd := p.tasksLoad.Done(msg.Meta, msg.Err)
		if msg.Err == nil {
			key := p.selectedKey()
			tp := msg.Data
			p.tasks = &tp
			p.relist(key)
		}
		return cmd
	case ui.ReloadMsg:
		if p.tasksFetched && p.ctx != nil && p.tasksWho != p.ctx.Me.Email {
			// Another account signed in: its requests aren't this one's.
			key := p.selectedKey()
			p.tasks = nil
			p.tasksLoad.Loaded, p.tasksLoad.Err = false, nil
			p.relist(key)
		}
		return p.fetchAll()
	case tea.KeyMsg:
		return p.handleKey(msg)
	}
	return nil
}

func (p *Page) handleKey(msg tea.KeyMsg) tea.Cmd {
	before := p.list.Cursor
	if p.list.HandleKey(msg) {
		if p.list.Cursor != before {
			p.scroll.Reset()
		}
		return nil
	}
	if p.scroll.HandleKey(msg) {
		return nil
	}
	switch msg.String() {
	case "r":
		return p.fetchAll()
	case "enter":
		return p.activate()
	case "o":
		if s, ok := p.selectedService(); ok {
			return openLink(s)
		}
	case "y":
		if s, ok := p.selectedService(); ok {
			if u := strings.TrimSpace(s.URL); u != "" {
				return ui.Copy(u, linkWord())
			}
			return noLinkToCopy()
		}
	}
	return nil
}

// activate is enter: browse a folder, open a link, or open the requests.
func (p *Page) activate() tea.Cmd {
	if p.onRequests() {
		return ui.Push(newRequests(p.ctx, p.tasks))
	}
	s, ok := p.selectedService()
	if !ok {
		return nil
	}
	switch p.kind(s) {
	case kindFolder:
		q := newPage(p.ctx, s.Category, "", p.path)
		q.svc = &s
		return ui.Push(q)
	case kindLink:
		return openLink(s)
	}
	return ui.Info(ui.Tr("%s has no link", "У «%s» нет ссылки"), serviceName(s))
}

func openLink(s api.Service) tea.Cmd {
	if u := strings.TrimSpace(s.URL); u != "" {
		return ui.OpenURL(u)
	}
	return ui.Info("%s", ui.Tr("No link to open", "Нет ссылки"))
}

// linkWord names a copied link in the status line.
func linkWord() string { return ui.Tr("link", "ссылка") }

func noLinkToCopy() tea.Cmd {
	return ui.Info("%s", ui.Tr("No link to copy", "Нет ссылки для копирования"))
}

// ------------------------------------------------------------------ rows

type kind int

const (
	kindPlain kind = iota
	kindFolder
	kindLink
)

func (p *Page) kind(s api.Service) kind {
	switch {
	case strings.TrimSpace(s.URL) != "":
		return kindLink
	case s.IsFolder() && !p.path[normCat(s.Category)]:
		return kindFolder
	}
	return kindPlain
}

func serviceName(s api.Service) string {
	for _, v := range []string{s.Name, s.Description} {
		if c := ui.Clean(v); c != "" {
			return c
		}
	}
	if id := ui.Clean(string(s.ID)); id != "" {
		return ui.Tr("Service ", "Сервис ") + id
	}
	return ui.Tr("Untitled service", "Сервис без названия")
}

func (p *Page) hasRequestsRow() bool {
	return p.root() && p.tasks != nil && len(p.tasks.Items) > 0
}

// requestsCount is the number of loaded requests, "12+" when the server
// has more pages (only the first one is loaded here).
func (p *Page) requestsCount() string {
	s := fmt.Sprint(len(p.tasks.Items))
	if moreTasks(*p.tasks) {
		s += "+"
	}
	return s
}

// moreTasks reports whether a tasks page points at a further page.
func moreTasks(tp api.TasksPage) bool {
	next := strings.TrimSpace(string(tp.NextCursor))
	return next != "" && next != "0" && next != strings.TrimSpace(string(tp.Cursor))
}

func (p *Page) offset() int {
	if p.hasRequestsRow() {
		return 1
	}
	return 0
}

func (p *Page) rowCount() int { return p.offset() + len(p.services) }

func (p *Page) onRequests() bool {
	return p.hasRequestsRow() && p.list.HasSelection() && p.list.Cursor == 0
}

func (p *Page) selectedService() (api.Service, bool) {
	if !p.list.HasSelection() {
		return api.Service{}, false
	}
	i := p.list.Cursor - p.offset()
	if i < 0 || i >= len(p.services) {
		return api.Service{}, false
	}
	return p.services[i], true
}

func (p *Page) selectedKey() string {
	if p.onRequests() {
		return "requests"
	}
	if s, ok := p.selectedService(); ok {
		return serviceKey(s)
	}
	return ""
}

func serviceKey(s api.Service) string { return "svc:" + string(s.ID) + "\x00" + s.Name }

// relist updates the list length after new data, keeping the selection.
func (p *Page) relist(key string) {
	p.list.SetLen(p.rowCount())
	if key == "" {
		p.list.Select(0)
		return
	}
	for i := 0; i < p.rowCount(); i++ {
		if p.rowKey(i) == key {
			p.list.Select(i)
			return
		}
	}
	p.scroll.Reset()
	p.list.Select(p.list.Cursor)
}

func (p *Page) rowKey(i int) string {
	if p.hasRequestsRow() && i == 0 {
		return "requests"
	}
	if j := i - p.offset(); j >= 0 && j < len(p.services) {
		return serviceKey(p.services[j])
	}
	return ""
}

// ------------------------------------------------------------------ view

func (p *Page) note() string {
	if n := ui.LoadNote(p.svcLoad); n != "" {
		return n
	}
	if p.svcLoad.Err != nil {
		return ui.StyleErr.Render("⚠ " + ui.Clean(ui.ErrText(p.svcLoad.Err)))
	}
	if p.root() && p.tasksLoad.Err != nil {
		return ui.StyleErr.Render(ui.Tr("⚠ requests: ", "⚠ заявки: ") + ui.Clean(ui.ErrText(p.tasksLoad.Err)))
	}
	return ""
}

func (p *Page) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	empty := ui.Tr("Nothing in this folder", "В этой папке пусто")
	if p.root() {
		empty = ui.Tr("No services available", "Нет доступных сервисов")
	}
	if sv := ui.StateView(width, height, p.svcLoad, p.rowCount(), empty); sv != "" {
		return sv
	}
	lw := ui.LeftWidth(width)
	rw := ui.RightWidth(width, lw)
	left := paneTitle(p.Title(), p.note(), lw)
	if height > 1 {
		left += "\n" + p.list.Render(lw, height-1, p.renderRow)
	}
	right := ""
	if rw > 0 {
		right = p.scroll.Render(balanceSGR(p.detail(rw)), rw, height)
	}
	return ui.Split(width, height, lw, left, right)
}

func (p *Page) renderRow(i int, sel bool, w int) string {
	if p.hasRequestsRow() && i == 0 {
		return ui.Row(sel, ui.StyleAccent.Render(myRequests()+" ("+p.requestsCount()+")"), w)
	}
	j := i - p.offset()
	if j < 0 || j >= len(p.services) {
		return ""
	}
	s := p.services[j]
	suffix := ""
	switch p.kind(s) {
	case kindFolder:
		suffix = "›"
	case kindLink:
		suffix = "↗"
	}
	return ui.Row(sel, withSuffix(serviceName(s), suffix, w-1), w)
}

// withSuffix right-aligns a dim marker after name within w cells.
func withSuffix(name, suffix string, w int) string {
	if suffix == "" {
		return name
	}
	nw := w - ui.Width(suffix) - 1
	if nw < 1 {
		return name
	}
	return ui.PadRight(name, nw) + " " + ui.StyleDim.Render(suffix)
}

func (p *Page) detail(w int) string {
	if p.onRequests() {
		n := len(p.tasks.Items)
		var count string
		if ui.RU() {
			// "12 заявок SmartPoint"; "12+" reads as many.
			word := ui.PluralRU(n, "заявка", "заявки", "заявок")
			if moreTasks(*p.tasks) {
				word = "заявок"
			}
			count = fmt.Sprintf("%s %s SmartPoint", p.requestsCount(), word)
		} else {
			word := ui.Plural(n, "request", "requests")
			if moreTasks(*p.tasks) {
				word = "requests"
			}
			count = fmt.Sprintf("%s SmartPoint %s", p.requestsCount(), word)
		}
		return ui.Lines(
			styleLines(ui.Wrap(myRequests(), w), ui.StyleTitle),
			ui.Wrap(count, w),
			"\n"+styleLines(ui.Wrap(ui.Tr("press enter to view", "нажмите enter, чтобы открыть"), w), ui.StyleDim),
		)
	}
	s, ok := p.selectedService()
	if !ok {
		return ""
	}
	blocks := []string{styleLines(ui.Wrap(serviceName(s), w), ui.StyleTitle)}
	if d := ui.CleanMulti(s.Description); d != "" && d != serviceName(s) {
		blocks = append(blocks, ui.Wrap(d, w))
	}
	switch p.kind(s) {
	case kindLink:
		blocks = append(blocks,
			"\n"+styleLines(ui.Wrap(ui.Clean(s.URL), w), ui.StyleLink),
			"\n"+styleLines(ui.Wrap(ui.Tr("enter open · y copy link", "enter открыть · y копировать ссылку"), w), ui.StyleDim))
	case kindFolder:
		blocks = append(blocks, "\n"+styleLines(ui.Wrap(ui.Tr("Folder · press enter to browse", "Папка · нажмите enter, чтобы открыть"), w), ui.StyleDim))
	default:
		blocks = append(blocks, "\n"+styleLines(ui.Wrap(ui.Tr("No link", "Нет ссылки"), w), ui.StyleDim))
	}
	return ui.Lines(blocks...)
}

// myRequests is the name of the requests entry and page.
func myRequests() string { return ui.Tr("My requests", "Мои заявки") }

// styleLines styles each line separately (lipgloss would pad a multi-line
// block to its widest line).
func styleLines(s string, st lipgloss.Style) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = st.Render(l)
		}
	}
	return strings.Join(lines, "\n")
}

// paneTitle is ui.PaneTitle that shortens a long note instead of letting it
// push the title out of the line.
func paneTitle(title, note string, w int) string {
	tw := min(ui.Width(title), max(w/2, w-ui.Width(note)-1))
	if room := w - tw - 1; room < 2 {
		note = ""
	} else {
		note = ui.Trunc(note, room)
	}
	return ui.PaneTitle(title, note, w)
}

var sgrRe = regexp.MustCompile(`\x1b\[[0-9;:]*m`)

// balanceSGR makes every line self-contained: a style left open at the end
// of a line (ansi.Wrap breaks inside styled spans) is closed there and
// reopened on the next line, so it can't bleed into the neighbouring pane.
func balanceSGR(s string) string {
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
