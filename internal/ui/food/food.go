// Package food is the Food tab: campus cafes grouped by building with
// live open/closed status, weekly hours and the daily menu.
package food

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"hse-app-z/internal/api"
	"hse-app-z/internal/ui"
)

// frow is a list row: a building header (cafe < 0) or a cafe.
type frow struct {
	place int
	cafe  int
}

func (r frow) header() bool { return r.cafe < 0 }

// Page is the cafes tab.
type Page struct {
	ctx *ui.Ctx
	id  int

	load   ui.Load
	places []place

	filter   int // index into filterCycle
	openOnly bool

	rows   []frow
	list   ui.List
	scroll ui.Scroll
	selKey string
}

// New returns the tab page.
func New(ctx *ui.Ctx) ui.Page {
	p := &Page{ctx: ctx, id: ui.NewID()}
	p.list.Skip = func(i int) bool { return i >= 0 && i < len(p.rows) && p.rows[i].header() }
	return p
}

func (p *Page) Title() string   { return ui.Tr("Food", "Еда") }
func (p *Page) Capturing() bool { return false }

func (p *Page) Hints() []ui.Hint {
	return []ui.Hint{
		{Key: "enter", Desc: ui.Tr("menu / actions", "меню / действия")},
		{Key: "c", Desc: ui.Tr("city", "город")},
		{Key: "o", Desc: ui.Tr("open now only", "только открытые")},
		{Key: "m", Desc: ui.Tr("map", "карта")},
		{Key: "p", Desc: ui.Tr("photo", "фото")},
		{Key: "y", Desc: ui.Tr("copy address", "копировать адрес")},
		{Key: ".", Desc: ui.Tr("actions", "действия")},
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
	return ui.Fetch(p.id, p.load.Begin(), func(ctx context.Context) ([]api.CafeGroup, api.Meta, error) {
		return client.Cafes(ctx)
	})
}

func (p *Page) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case ui.ReloadMsg:
		return p.fetch()
	case ui.Result[[]api.CafeGroup]:
		if msg.ID != p.id || !p.load.Accept(msg.Seq) {
			return nil
		}
		cmd := p.load.Done(msg.Meta, msg.Err)
		if msg.Err == nil {
			p.places = sortPlaces(msg.Data)
			p.rebuild()
		}
		return cmd
	case tea.KeyMsg:
		return p.key(msg)
	}
	return nil
}

func (p *Page) key(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "r":
		return p.fetch()
	case "c":
		p.filter = (p.filter + 1) % len(filterCycle)
		p.rebuild()
		return nil
	case "o":
		p.openOnly = !p.openOnly
		p.rebuild()
		return nil
	}
	c, g, ok := p.selected()
	switch msg.String() {
	case "enter":
		if !ok {
			return nil
		}
		if c.HasMenu {
			return p.openMenu(c, g)
		}
		return p.actions(c, g)
	case ".":
		if !ok {
			return nil
		}
		return p.actions(c, g)
	case "M":
		if !ok {
			return nil
		}
		if !c.HasMenu {
			return ui.Warn("%s", noMenu())
		}
		return p.openMenu(c, g)
	case "m":
		if !ok {
			return nil
		}
		return openMap(c, g)
	case "p":
		if !ok {
			return nil
		}
		return openPhoto(c)
	case "y":
		if !ok {
			return nil
		}
		return ui.Copy(address(c, g), addressWord())
	}
	if p.list.HandleKey(msg) {
		p.syncSel()
		return nil
	}
	p.scroll.HandleKey(msg)
	return nil
}

func (p *Page) openMenu(c api.Cafe, g api.CafeGroup) tea.Cmd {
	return ui.Push(newMenuPage(p.ctx, c, zoneOf(c, g)))
}

func openMap(c api.Cafe, g api.CafeGroup) tea.Cmd {
	lat, lng, ok := location(c, g)
	if !ok {
		return ui.Warn("%s", ui.Tr("No location for this cafe", "Местоположение кафе неизвестно"))
	}
	return ui.OpenURL(ui.MapURL(lat, lng))
}

func openPhoto(c api.Cafe) tea.Cmd {
	u := photo(c)
	if u == "" {
		return ui.Warn("%s", ui.Tr("No photos of this cafe", "У этого кафе нет фото"))
	}
	return ui.OpenURL(u)
}

func (p *Page) actions(c api.Cafe, g api.CafeGroup) tea.Cmd {
	var acts []ui.Action
	if c.HasMenu {
		acts = append(acts, ui.Action{Key: "M", Label: ui.Tr("Menu", "Меню"), Run: func() tea.Cmd { return p.openMenu(c, g) }})
	}
	if _, _, ok := location(c, g); ok {
		acts = append(acts, ui.Action{Key: "m", Label: ui.Tr("Open map", "Открыть карту"), Run: func() tea.Cmd { return openMap(c, g) }})
	}
	if photo(c) != "" {
		acts = append(acts, ui.Action{Key: "p", Label: ui.Tr("Open photo", "Открыть фото"), Run: func() tea.Cmd { return openPhoto(c) }})
	}
	if a := address(c, g); a != "" {
		what := addressWord()
		acts = append(acts, ui.Action{Key: "y", Label: ui.Tr("Copy address", "Копировать адрес"), Run: func() tea.Cmd { return ui.Copy(a, what) }})
	}
	if len(acts) == 0 {
		return ui.Warn("%s", noMenu())
	}
	return ui.ShowMenu(ui.Clean(c.Name), acts...)
}

func noMenu() string      { return ui.Tr("No menu for this cafe", "У этого кафе нет меню") }
func addressWord() string { return ui.Tr("address", "адрес") }

// cafeName is the cafe's name, or a generic one.
func cafeName(c api.Cafe) string {
	if name := ui.Clean(c.Name); name != "" {
		return name
	}
	return ui.Tr("Cafe", "Кафе")
}

// --------------------------------------------------------------- rows

func (p *Page) city() city { return filterCycle[p.filter%len(filterCycle)] }

func (p *Page) cafeAt(r frow) (api.Cafe, api.CafeGroup) {
	g := p.places[r.place].group
	return g.Cafes[r.cafe], g
}

func (p *Page) status(c api.Cafe, g api.CafeGroup) api.OpenStatus {
	return api.Status(c.OpeningHours, c.ClosedDates, p.ctx.Time().In(zoneOf(c, g)))
}

func (p *Page) selected() (api.Cafe, api.CafeGroup, bool) {
	if !p.list.HasSelection() || p.list.Cursor >= len(p.rows) {
		return api.Cafe{}, api.CafeGroup{}, false
	}
	c, g := p.cafeAt(p.rows[p.list.Cursor])
	return c, g, true
}

func cafeKey(c api.Cafe, g api.CafeGroup) string {
	return string(g.CampusID) + "/" + string(c.ID) + "/" + c.Name
}

// rebuild applies the filters, keeping the selected cafe when possible.
func (p *Page) rebuild() {
	// selKey, not selected(): rows may point into replaced data.
	prev := p.selKey
	p.rows = p.rows[:0]
	want := p.city()
	for pi, pl := range p.places {
		if want != cityAll && pl.city != want {
			continue
		}
		var cafes []frow
		for ci, c := range pl.group.Cafes {
			if p.openOnly && !p.status(c, pl.group).Open {
				continue
			}
			cafes = append(cafes, frow{place: pi, cafe: ci})
		}
		if len(cafes) == 0 {
			continue
		}
		p.rows = append(p.rows, frow{place: pi, cafe: -1})
		p.rows = append(p.rows, cafes...)
	}
	p.list.SetLen(len(p.rows))
	idx := 0
	for i, r := range p.rows {
		if r.header() {
			continue
		}
		if c, g := p.cafeAt(r); cafeKey(c, g) == prev {
			idx = i
			break
		}
	}
	p.list.Select(idx)
	p.syncSel()
}

func (p *Page) syncSel() {
	k := ""
	if c, g, ok := p.selected(); ok {
		k = cafeKey(c, g)
	}
	if k != p.selKey {
		p.selKey = k
		p.scroll.Reset()
	}
}

// --------------------------------------------------------------- view

func (p *Page) View(w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	if p.openOnly {
		// Cafés open and close while the page is shown (the root redraws
		// every 30s): keep "open now" true to its name.
		p.rebuild()
	}
	lw := ui.LeftWidth(w)
	return ui.Split(w, h, lw, p.listView(lw, h), p.detail(ui.RightWidth(w, lw), h))
}

// title names the active filters; city names are shortened when the
// pane is narrow.
func (p *Page) title(w, noteW int) string {
	mk := func(name string) string {
		t := ui.Tr("Cafés · ", "Кафе · ") + name
		if p.openOnly {
			t += ui.Tr(" · open now", " · открыто сейчас")
		}
		return t
	}
	if t := mk(p.city().String()); ui.Width(t)+noteW+2 <= w {
		return t
	}
	return mk(p.city().short())
}

func (p *Page) listView(w, h int) string {
	note := ui.LoadNote(p.load)
	if note == "" && len(p.rows) > 0 {
		open, total := 0, 0
		for _, r := range p.rows {
			if r.header() {
				continue
			}
			total++
			if c, g := p.cafeAt(r); p.status(c, g).Open {
				open++
			}
		}
		note = ui.StyleDim.Render(ui.Trf("%d/%d open", "%d/%d открыто", open, total))
	}
	title := p.title(w, ui.Width(note))
	head := ui.PaneTitle(title, ui.Trunc(note, max(0, w-ui.Width(title)-2)), w)
	if h <= 1 {
		return head
	}
	if len(p.rows) == 0 {
		return head + "\n" + p.emptyView(w, h-1)
	}
	return head + "\n" + p.list.Render(w, h-1, p.renderRow)
}

func (p *Page) emptyView(w, h int) string {
	if sv := ui.StateView(w, h, p.load, len(p.places), ui.Tr("No cafes listed", "Список кафе пуст")); sv != "" {
		return sv
	}
	where := p.city().in()
	if p.openOnly {
		return ui.Placeholder(w, h, ui.Trf("Nothing open%s right now", "Сейчас%s ничего не открыто", where)+"\n\n"+
			ui.StyleDim.Render(ui.Tr("o shows all cafes · c changes the city", "o — все кафе · c — сменить город")))
	}
	return ui.Placeholder(w, h, ui.Trf("No cafes%s", "Нет кафе%s", where)+"\n\n"+ui.StyleDim.Render(ui.Tr("c changes the city", "c — сменить город")))
}

func (p *Page) renderRow(i int, sel bool, w int) string {
	r := p.rows[i]
	if r.header() {
		name := p.places[r.place].name()
		// The city is in the pane title already when filtering by it.
		if c := p.city(); c != cityAll {
			name = trimCity(name, c)
		}
		return ui.HeaderRow(name, w)
	}
	c, g := p.cafeAt(r)
	label, style := statusText(p.status(c, g))
	warn := ""
	if ui.Clean(c.Banner) != "" {
		warn = ui.StyleWarn.Render("!") + " "
	}
	name := cafeName(c)
	cw := w - 1 // Row's marker column
	// Names come first: drop the "open · " prefix when the row is tight
	// (the colour and "until …" still say it), and keep ≥ 14 cells for
	// the name.
	if cw-ui.Width(warn)-ui.Width(label)-1 < 24 {
		label = shortStatus(label)
	}
	status := ui.Trunc(warn+style.Render(label), max(0, cw/2, cw-15))
	content := ui.PadRight(name, max(0, cw-ui.Width(status)-1)) + " " + status
	return ui.Row(sel, content, w)
}

func (p *Page) detail(w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	c, g, ok := p.selected()
	if !ok {
		return ""
	}
	return p.scroll.Render(p.detailBody(c, g, w), w, h)
}

func (p *Page) detailBody(c api.Cafe, g api.CafeGroup, w int) string {
	zone := zoneOf(c, g)
	now := p.ctx.Time().In(zone)
	st := api.Status(c.OpeningHours, c.ClosedDates, now)
	var out []string
	add := func(s string) {
		if s != "" {
			out = append(out, s)
		}
	}
	add(ui.StyleTitle.Render(ui.Wrap(cafeName(c), w)))
	label, style := statusText(st)
	if st.Known && !st.Open {
		style = ui.StyleWarn
	}
	add(style.Render(ui.Wrap(label, w)))
	if b := ui.CleanMulti(c.Banner); b != "" {
		out = append(out, "", ui.StyleWarn.Render(ui.Wrap("! "+b, w)))
	}
	out = append(out, "")
	if st.Known {
		add(ui.KV(ui.Tr("Today", "Сегодня"), todayText(st.Today), w))
	}
	_, off := now.Zone()
	if _, homeOff := p.ctx.Time().Zone(); off != homeOff {
		add(ui.KV(ui.Tr("Local time", "Время"), now.Format("15:04")+" "+zoneLabel(now), w))
	}
	add(ui.KV(ui.Tr("Closed", "Закрыто"), upcomingClosed(c.ClosedDates, now), w))
	add(ui.Section(ui.Tr("Hours", "Часы работы"), schedule(c.OpeningHours, now.Weekday(), w)))
	out = append(out, "")
	add(ui.KV(ui.Tr("Address", "Адрес"), address(c, g), w))
	add(ui.KV(ui.Tr("Directions", "Как пройти"), ui.Clean(c.Description), w))
	add(ui.KV(ui.Tr("Where", "Где"), navigation(c.Navigation), w))
	add(ui.KV(ui.Tr("Load", "Загрузка"), ui.Clean(c.CurrentLoad), w))
	if c.HasMenu {
		add(ui.KV(ui.Tr("Menu", "Меню"), ui.Trf("available — %s to view", "есть — %s, чтобы открыть", ui.StyleKey.Render("enter")), w))
	}
	if n := photoCount(c); n > 0 {
		add(ui.KV(ui.Tr("Photos", "Фото"), ui.Count(n, "photo", "photos", "фото", "фото", "фото")+
			ui.Trf(" — %s to open", " — %s, чтобы открыть", ui.StyleKey.Render("p")), w))
	}
	if _, _, ok := location(c, g); ok {
		add(ui.KV(ui.Tr("Map", "Карта"), ui.Trf("%s to open", "%s, чтобы открыть", ui.StyleKey.Render("m")), w))
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}

// zoneLabel is "UTC+5" for a time's offset.
func zoneLabel(t time.Time) string {
	_, off := t.Zone()
	sign := "+"
	if off < 0 {
		sign, off = "-", -off
	}
	if off%3600 == 0 {
		return fmt.Sprintf("UTC%s%d", sign, off/3600)
	}
	return fmt.Sprintf("UTC%s%d:%02d", sign, off/3600, off%3600/60)
}
