// Package news is the News tab: HSE stories (GET /v2/stories) with their
// pages' action links.
package news

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"hse-app-z/internal/api"
	"hse-app-z/internal/ui"
)

// Page is the News tab.
type Page struct {
	ctx     *ui.Ctx
	id      int
	load    ui.Load
	stories []api.Story

	list   ui.List
	scroll ui.Scroll
}

// New returns the tab page.
func New(ctx *ui.Ctx) ui.Page { return &Page{ctx: ctx, id: ui.NewID()} }

func (p *Page) Title() string   { return ui.Tr("News", "Новости") }
func (p *Page) Capturing() bool { return false }

func (p *Page) Hints() []ui.Hint {
	return []ui.Hint{
		{Key: "o", Desc: ui.Tr("open link", "открыть ссылку")},
		{Key: "enter", Desc: ui.Tr("actions", "действия")},
		{Key: "i", Desc: ui.Tr("open image", "открыть обложку")},
		{Key: "y", Desc: ui.Tr("copy link", "копировать ссылку")},
		{Key: "r", Desc: ui.Tr("refresh", "обновить")},
		{Key: "J/K", Desc: ui.Tr("scroll details", "прокрутка деталей")},
	}
}

func (p *Page) Init() tea.Cmd { return p.fetch() }

func (p *Page) fetch() tea.Cmd {
	if p.ctx == nil || p.ctx.API == nil {
		return nil
	}
	client := p.ctx.API
	return ui.Fetch(p.id, p.load.Begin(), func(c context.Context) ([]api.Story, api.Meta, error) {
		return client.Stories(c)
	})
}

func (p *Page) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case ui.Result[[]api.Story]:
		if msg.ID != p.id || !p.load.Accept(msg.Seq) {
			return nil
		}
		cmd := p.load.Done(msg.Meta, msg.Err)
		if msg.Err == nil {
			p.setData(msg.Data)
		}
		return cmd
	case ui.ReloadMsg:
		return p.fetch()
	case tea.KeyMsg:
		return p.handleKey(msg)
	}
	return nil
}

func (p *Page) setData(data []api.Story) {
	key := ""
	if s, ok := p.selected(); ok {
		key = storyKey(s)
	}
	stories := append([]api.Story(nil), data...)
	sort.SliceStable(stories, func(i, j int) bool {
		ti, tj := stories[i].Published(), stories[j].Published()
		if ti.IsZero() != tj.IsZero() {
			return !ti.IsZero()
		}
		return ti.After(tj)
	})
	p.stories = stories
	p.list.SetLen(len(stories))
	sel, found := 0, false
	for i, s := range stories {
		if key != "" && storyKey(s) == key {
			sel, found = i, true
			break
		}
	}
	// Keep the detail scroll only while the same story stays selected.
	if !found {
		p.scroll.Reset()
	}
	p.list.Select(sel)
}

func storyKey(s api.Story) string { return string(s.ID) + "\x00" + s.PreviewImage }

func (p *Page) selected() (api.Story, bool) {
	if !p.list.HasSelection() || p.list.Cursor >= len(p.stories) {
		return api.Story{}, false
	}
	return p.stories[p.list.Cursor], true
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
	if msg.String() == "r" {
		return p.fetch()
	}
	s, ok := p.selected()
	if !ok {
		return nil
	}
	switch msg.String() {
	case "o":
		if l := links(s); len(l) > 0 {
			return ui.OpenURL(l[0].url)
		}
		return noLink()
	case "y":
		if l := links(s); len(l) > 0 {
			return ui.Copy(l[0].url, linkWord())
		}
		return noLink()
	case "i":
		if img := strings.TrimSpace(s.PreviewImage); img != "" {
			return ui.OpenURL(img)
		}
		return ui.Info("%s", ui.Tr("This story has no cover image", "У этой новости нет обложки"))
	case "enter":
		return p.menu(s)
	}
	return nil
}

func noLink() tea.Cmd {
	return ui.Info("%s", ui.Tr("This story has no link", "У этой новости нет ссылки"))
}

// linkWord names a copied link in the status line.
func linkWord() string { return ui.Tr("link", "ссылка") }

func (p *Page) menu(s api.Story) tea.Cmd {
	var actions []ui.Action
	for i, l := range links(s) {
		u := l.url
		a := ui.Action{Label: ui.Tr("Open: ", "Открыть: ") + l.title + " (" + ShortURL(u) + ")", Run: func() tea.Cmd { return ui.OpenURL(u) }}
		if i == 0 {
			a.Key = "o"
		}
		actions = append(actions, a)
	}
	if l := links(s); len(l) > 0 {
		u := l[0].url
		actions = append(actions, ui.Action{Key: "y", Label: ui.Tr("Copy link", "Копировать ссылку"), Run: func() tea.Cmd { return ui.Copy(u, linkWord()) }})
	}
	if img := strings.TrimSpace(s.PreviewImage); img != "" {
		actions = append(actions, ui.Action{Key: "i", Label: ui.Tr("Open cover image", "Открыть обложку"), Run: func() tea.Cmd { return ui.OpenURL(img) }})
	}
	if img := strings.TrimSpace(s.Publisher.Image); img != "" {
		actions = append(actions, ui.Action{Label: ui.Tr("Open publisher image", "Открыть логотип источника"), Run: func() tea.Cmd { return ui.OpenURL(img) }})
	}
	if len(actions) == 0 {
		return ui.Info("%s", ui.Tr("Nothing to open in this story", "В этой новости нечего открыть"))
	}
	return ui.ShowMenu(StoryTitle(s), actions...)
}

// ----------------------------------------------------------------- model

type link struct{ title, url string }

// links are the distinct action links of a story's pages, in page order
// (stories often repeat the same link on every page). Links we won't open
// (javascript:, file:, app schemes…) are left out, so o/y/the menu always
// act on a usable one.
func links(s api.Story) []link {
	var out []link
	seen := map[string]bool{}
	for _, pg := range s.Pages {
		if pg.Action == nil {
			continue
		}
		u := strings.TrimSpace(pg.Action.Link)
		if u == "" || seen[u] || !ui.SafeURL(u) {
			continue
		}
		seen[u] = true
		t := ui.Clean(pg.Action.Title)
		if t == "" {
			t = linkWord()
		}
		out = append(out, link{title: t, url: u})
	}
	return out
}

// StoryTitle is the visible title: story titles are often just the
// invisible U+2800, so fall back to the first page's action title, then the
// publisher name.
func StoryTitle(s api.Story) string {
	if t := ui.Clean(s.Title); t != "" {
		return t
	}
	for _, pg := range s.Pages {
		if pg.Action != nil {
			if t := ui.Clean(pg.Action.Title); t != "" {
				return t
			}
		}
	}
	if t := ui.Clean(s.Publisher.Name); t != "" {
		return t
	}
	return ui.Tr("Story", "Новость")
}

// ShortURL is host+path without scheme, query or trailing slash
// ("max.ru/hse_official").
func ShortURL(raw string) string {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ui.Trunc(ui.Clean(raw), 40)
	}
	s := strings.TrimPrefix(u.Host, "www.") + strings.TrimRight(u.EscapedPath(), "/")
	return ui.Trunc(ui.Clean(s), 40)
}

func dayLabel(t time.Time, now time.Time) string {
	t = t.In(api.Moscow)
	if t.Year() == now.In(api.Moscow).Year() {
		return ui.FmtDay(t)
	}
	return ui.FmtDate(t)
}

// ------------------------------------------------------------------ view

func (p *Page) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	if sv := ui.StateView(width, height, p.load, len(p.stories), ui.Tr("No news right now", "Новостей пока нет")); sv != "" {
		return sv
	}
	lw := ui.LeftWidth(width)
	rw := ui.RightWidth(width, lw)
	left := paneTitle(p.Title(), ui.LoadNote(p.load), lw)
	if height > 1 {
		left += "\n" + p.list.Render(lw, height-1, p.renderRow)
	}
	right := ""
	if s, ok := p.selected(); ok && rw > 0 {
		right = p.scroll.Render(balanceSGR(p.detail(s, rw)), rw, height)
	}
	return ui.Split(width, height, lw, left, right)
}

func (p *Page) renderRow(i int, sel bool, w int) string {
	if i < 0 || i >= len(p.stories) {
		return ""
	}
	s := p.stories[i]
	date := "      "
	if t := s.Published(); !t.IsZero() {
		t = t.In(api.Moscow)
		date = ui.PadRight(fmt.Sprintf("%02d %s", t.Day(), ui.MonthShort(t.Month())), 6)
	}
	title := StoryTitle(s)
	content := ui.StyleDim.Render(date) + " " + title
	if pub := ui.Clean(s.Publisher.Name); pub != "" && !strings.EqualFold(pub, title) {
		content += ui.StyleDim.Render(" · " + pub)
	}
	return ui.Row(sel, content, w)
}

func (p *Page) detail(s api.Story, w int) string {
	now := p.ctx.Time()
	title := StoryTitle(s)
	blocks := []string{styleLines(ui.Wrap(title, w), ui.StyleTitle)}
	if pub := ui.Clean(s.Publisher.Name); pub != "" {
		blocks = append(blocks, ui.KV(ui.Tr("Publisher", "Источник"), pub, w))
	}
	published := s.Published()
	if !published.IsZero() {
		blocks = append(blocks, ui.KV(ui.Tr("Published", "Публикация"), dayLabel(published, now), w))
	}
	pagesLabel := ui.Tr("Pages", "Страницы")
	blocks = append(blocks, ui.KV(pagesLabel, fmt.Sprint(len(s.Pages)), w))

	var pages []string
	for i, pg := range s.Pages {
		pages = append(pages, pageBlock(i, pg, published, now, w))
	}
	blocks = append(blocks, ui.Section(pagesLabel, strings.Join(pages, "\n")))
	// Only advertise the keys that do something for this story.
	var keys []string
	if len(links(s)) > 0 {
		keys = append(keys, ui.Tr("o open link", "o открыть ссылку"), ui.Tr("enter all actions", "enter все действия"))
	}
	if strings.TrimSpace(s.PreviewImage) != "" {
		keys = append(keys, ui.Tr("i cover image", "i обложка"))
	}
	if len(keys) > 0 {
		blocks = append(blocks, "\n"+styleLines(ui.Wrap(strings.Join(keys, " · "), w), ui.StyleDim))
	}
	return ui.Lines(blocks...)
}

func pageBlock(i int, pg api.StoryPage, published, now time.Time, w int) string {
	num := fmt.Sprintf("%d. ", i+1)
	indent := strings.Repeat(" ", len(num))
	iw := max(4, w-len(num))
	var lines []string
	if pg.Action != nil && strings.TrimSpace(pg.Action.Link) != "" {
		t := ui.Clean(pg.Action.Title)
		if t == "" {
			t = linkWord()
		}
		u := ui.Clean(pg.Action.Link)
		st := ui.StyleLink
		if !ui.SafeURL(pg.Action.Link) {
			st = ui.StyleDim // shown for reference, but never opened
		}
		head := num + t + " → "
		if ui.Width(head)+ui.Width(u) <= w {
			lines = append(lines, head+st.Render(u))
		} else {
			head := ui.Indent(ui.Wrap(t+" →", iw), indent)
			lines = append(lines, num+strings.TrimPrefix(head, indent))
			lines = append(lines, ui.Indent(styleLines(ui.Wrap(u, iw), st), indent))
		}
	} else {
		lines = append(lines, num+ui.StyleDim.Render(ui.Tr("no link", "нет ссылки")))
	}
	if t := pg.DatePublished.Time; !t.IsZero() && !sameDay(t, published) {
		lines = append(lines, indent+ui.StyleDim.Render(dayLabel(t, now)))
	}
	if img := ui.Clean(pg.Image); img != "" {
		lines = append(lines, indent+ui.StyleDim.Render(ui.Trunc(img, iw)))
	}
	return strings.Join(lines, "\n")
}

func sameDay(a, b time.Time) bool {
	a, b = a.In(api.Moscow), b.In(api.Moscow)
	return a.Year() == b.Year() && a.YearDay() == b.YearDay()
}

// ----------------------------------------------------------------- utils

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
