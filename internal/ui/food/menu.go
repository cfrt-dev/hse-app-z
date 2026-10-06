package food

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"

	"hse-app-z/internal/api"
	"hse-app-z/internal/ui"
)

// mrow is a menu row: a section header (item < 0) or an item.
type mrow struct {
	sec  int
	item int
}

func (r mrow) header() bool { return r.item < 0 }

// menuPage shows one cafe's menu for a day (pushed from the Food tab).
type menuPage struct {
	ctx  *ui.Ctx
	id   int
	cafe api.Cafe
	zone *time.Location

	day  string // requested weekday key; "" = today (server's choice)
	menu api.Menu
	// menuDay is the day value menu was fetched for: while another day
	// loads, menu is not what the title says.
	menuDay string
	load    ui.Load

	rows   []mrow
	list   ui.List
	scroll ui.Scroll
	selKey string
}

func newMenuPage(ctx *ui.Ctx, c api.Cafe, zone *time.Location) *menuPage {
	if zone == nil {
		zone = api.Moscow
	}
	p := &menuPage{ctx: ctx, id: ui.NewID(), cafe: c, zone: zone}
	p.list.Skip = func(i int) bool { return i >= 0 && i < len(p.rows) && p.rows[i].header() }
	return p
}

func (p *menuPage) Title() string {
	if n := ui.Clean(p.cafe.Name); n != "" {
		return ui.Tr("Menu · ", "Меню · ") + n
	}
	return ui.Tr("Menu", "Меню")
}

func (p *menuPage) Capturing() bool { return false }

func (p *menuPage) Hints() []ui.Hint {
	return []ui.Hint{
		{Key: "h/l", Desc: ui.Tr("previous / next day", "пред. / след. день")},
		{Key: "t", Desc: ui.Tr("today", "сегодня")},
		{Key: "J/K", Desc: ui.Tr("scroll details", "прокрутка деталей")},
		{Key: "r", Desc: ui.Tr("refresh", "обновить")},
	}
}

func (p *menuPage) Init() tea.Cmd { return p.fetch() }

func (p *menuPage) fetch() tea.Cmd {
	if p.ctx == nil || p.ctx.API == nil {
		return nil
	}
	client, id, day := p.ctx.API, string(p.cafe.ID), p.day
	return ui.Fetch(p.id, p.load.Begin(), func(ctx context.Context) (api.Menu, api.Meta, error) {
		return client.CafeMenu(ctx, id, day)
	})
}

func (p *menuPage) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case ui.ReloadMsg:
		return p.fetch()
	case ui.Result[api.Menu]:
		if msg.ID != p.id || !p.load.Accept(msg.Seq) {
			return nil
		}
		cmd := p.load.Done(msg.Meta, msg.Err)
		switch {
		case msg.Err == nil:
			p.menu = msg.Data
			p.menuDay = p.day
		case p.menuDay != p.day:
			// The kept data is another day's (also after t from another
			// day): don't show it under this day's title.
			p.menu.Sections = nil
			p.menu.CurrentDay = p.day
			p.menuDay = p.day
		}
		p.rebuild()
		return cmd
	case tea.KeyMsg:
		switch msg.String() {
		case "h", "left":
			return p.shift(-1)
		case "l", "right":
			return p.shift(1)
		case "t":
			if p.day == "" && !p.load.Loading && p.load.Err == nil {
				return nil
			}
			p.day = ""
			return p.fetch()
		case "r":
			return p.fetch()
		}
		if p.list.HandleKey(msg) {
			p.syncSel()
			return nil
		}
		p.scroll.HandleKey(msg)
	}
	return nil
}

func (p *menuPage) today() time.Weekday { return p.ctx.Time().In(p.zone).Weekday() }

// current is the day being shown (or requested).
func (p *menuPage) current() time.Weekday {
	for _, s := range []string{p.day, p.menu.CurrentDay} {
		if d, ok := api.ParseWeekday(s); ok {
			return d
		}
	}
	return p.today()
}

// monIndex numbers weekdays from Monday (0) to Sunday (6).
func monIndex(d time.Weekday) int { return (int(d) + 6) % 7 }

// shift moves to the previous/next day that has a menu.
func (p *menuPage) shift(delta int) tea.Cmd {
	var avail []time.Weekday
	for _, s := range p.menu.AvailableDays {
		if d, ok := api.ParseWeekday(s); ok {
			avail = append(avail, d)
		}
	}
	if len(avail) == 0 {
		avail = week
	}
	cur := monIndex(p.current())
	best := -1
	for _, d := range avail {
		i := monIndex(d)
		if delta > 0 && i > cur && (best < 0 || i < best) {
			best = i
		}
		if delta < 0 && i < cur && (best < 0 || i > best) {
			best = i
		}
	}
	if best < 0 {
		if delta > 0 {
			return ui.Info(ui.Tr("No menu after %s", "Позже %s меню нет"), dayGen(p.current()))
		}
		return ui.Info(ui.Tr("No menu before %s", "Раньше %s меню нет"), dayGen(p.current()))
	}
	p.day = api.WeekdayKey(week[best])
	return p.fetch()
}

// switching reports whether another day is loading: the rows still
// belong to the previous day.
func (p *menuPage) switching() bool { return p.load.Loading && p.menuDay != p.day }

func (p *menuPage) dayTitle() string {
	d := p.current()
	name := capFirst(ui.WeekdayLong(d))
	if d == p.today() {
		return name + ui.Tr(" · today", " · сегодня")
	}
	return name
}

// dayGen is "Monday" / "понедельника" (after "раньше/позже").
func dayGen(d time.Weekday) string {
	if !ui.RU() {
		return d.String()
	}
	return [7]string{"воскресенья", "понедельника", "вторника", "среды", "четверга", "пятницы", "субботы"}[d]
}

// dayAcc is "Monday" / "понедельник" (after "на": "на среду").
func dayAcc(d time.Weekday) string {
	if !ui.RU() {
		return d.String()
	}
	return [7]string{"воскресенье", "понедельник", "вторник", "среду", "четверг", "пятницу", "субботу"}[d]
}

func (p *menuPage) rebuild() {
	prev := p.selKey
	p.rows = p.rows[:0]
	for si, s := range p.menu.Sections {
		if len(s.Items) == 0 {
			continue
		}
		p.rows = append(p.rows, mrow{sec: si, item: -1})
		for ii := range s.Items {
			p.rows = append(p.rows, mrow{sec: si, item: ii})
		}
	}
	p.list.SetLen(len(p.rows))
	idx := 0
	for i, r := range p.rows {
		if !r.header() && p.rowKey(r) == prev {
			idx = i
			break
		}
	}
	p.list.Select(idx)
	p.syncSel()
}

func (p *menuPage) rowKey(r mrow) string {
	s := p.menu.Sections[r.sec]
	it := s.Items[r.item]
	return s.Section + "/" + s.SectionName + "/" + string(it.ItemID) + "/" + it.ItemName
}

func (p *menuPage) selected() (api.MenuSection, api.MenuItem, bool) {
	if !p.list.HasSelection() || p.list.Cursor >= len(p.rows) {
		return api.MenuSection{}, api.MenuItem{}, false
	}
	r := p.rows[p.list.Cursor]
	s := p.menu.Sections[r.sec]
	return s, s.Items[r.item], true
}

func (p *menuPage) syncSel() {
	k := ""
	if p.list.HasSelection() && p.list.Cursor < len(p.rows) {
		k = p.rowKey(p.rows[p.list.Cursor])
	}
	if k != p.selKey {
		p.selKey = k
		p.scroll.Reset()
	}
}

// ------------------------------------------------------------------ view

func (p *menuPage) View(w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	lw := ui.LeftWidth(w)
	return ui.Split(w, h, lw, p.listView(lw, h), p.detail(ui.RightWidth(w, lw), h))
}

func (p *menuPage) listView(w, h int) string {
	title := p.dayTitle()
	head := ui.PaneTitle(title, ui.Trunc(ui.LoadNote(p.load), max(0, w-ui.Width(title)-2)), w)
	if h <= 1 {
		return head
	}
	if p.switching() {
		return head + "\n" + ui.Placeholder(w, h-1, ui.StyleDim.Render(ui.Tr("Loading…", "Загрузка…")))
	}
	if len(p.rows) > 0 {
		return head + "\n" + p.list.Render(w, h-1, p.renderRow)
	}
	var ae *api.APIError
	switch {
	case p.load.Err != nil && errors.As(p.load.Err, &ae) && ae.NotFound():
		return head + "\n" + ui.Placeholder(w, h-1, ui.Tr("Menu not available", "Меню недоступно")+"\n\n"+
			ui.StyleDim.Render(ui.Tr("t today · h/l other days", "t сегодня · h/l другие дни")))
	case p.load.Err != nil || p.load.Loading:
		return head + "\n" + ui.StateView(w, h-1, p.load, 0, "")
	}
	return head + "\n" + ui.Placeholder(w, h-1, ui.Trf("No menu for %s", "Нет меню на %s", dayAcc(p.current()))+"\n\n"+
		ui.StyleDim.Render(ui.Tr("h/l other days", "h/l другие дни")))
}

// price formats a rouble amount ("" when unknown).
func price(n api.Num) string {
	if !n.OK || n.V <= 0 {
		return ""
	}
	if n.V == float64(int64(n.V)) {
		return fmt.Sprintf("%d ₽", int64(n.V))
	}
	return fmt.Sprintf("%.2f ₽", n.V)
}

func sectionName(s api.MenuSection) string {
	if n := ui.Clean(s.SectionName); n != "" {
		return n
	}
	if n := ui.Clean(s.Section); n != "" {
		return n
	}
	return ui.Tr("Menu", "Меню")
}

func itemName(it api.MenuItem) string {
	for _, s := range []string{it.ItemName, it.ItemNameOpt} {
		if n := ui.Clean(s); n != "" {
			return n
		}
	}
	return ui.Tr("Unnamed dish", "Блюдо без названия")
}

// chipLabel abbreviates a dish tag for list rows.
func chipLabel(c string) string {
	k := strings.ToLower(ui.Clean(c))
	switch {
	case k == "":
		return ""
	case strings.Contains(k, "vegan") || strings.Contains(k, "веган"):
		return ui.Tr("vegan", "веган")
	case strings.Contains(k, "veg") || strings.Contains(k, "вегет"):
		return ui.Tr("veg", "вегет")
	case strings.Contains(k, "diet") || strings.Contains(k, "диет"):
		return ui.Tr("diet", "диет")
	case strings.Contains(k, "spic") || strings.Contains(k, "остр"):
		return ui.Tr("spicy", "остр")
	case strings.Contains(k, "lent") || strings.Contains(k, "пост"):
		return ui.Tr("lent", "пост")
	}
	return ui.Trunc(k, 10)
}

func chips(it api.MenuItem, short bool) string {
	var out []string
	for _, c := range it.Chips {
		l := ui.Clean(c)
		if short {
			l = chipLabel(c)
		}
		if l != "" {
			out = append(out, l)
		}
	}
	if short {
		return strings.Join(out, " ")
	}
	return strings.Join(out, ", ")
}

func (p *menuPage) renderRow(i int, sel bool, w int) string {
	r := p.rows[i]
	s := p.menu.Sections[r.sec]
	if r.header() {
		return ui.HeaderRow(ui.JoinNonEmpty(" — ", sectionName(s), price(s.Price)), w)
	}
	it := s.Items[r.item]
	cw := w - 1
	pr := ui.Trunc(price(it.Price), max(0, cw/3))
	left := itemName(it)
	if c := chips(it, true); c != "" {
		left += " " + ui.StyleDim.Render(c)
	}
	content := ui.PadRight(left, max(0, cw-ui.Width(pr)-1)) + " " + pr
	return ui.Row(sel, content, w)
}

func (p *menuPage) detail(w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	s, it, ok := p.selected()
	if !ok || p.switching() {
		return ""
	}
	var out []string
	add := func(v string) {
		if v != "" {
			out = append(out, v)
		}
	}
	name := itemName(it)
	add(ui.StyleTitle.Render(ui.Wrap(name, w)))
	if alt := ui.Clean(it.ItemNameOpt); alt != "" && alt != name {
		add(ui.StyleDim.Render(ui.Wrap(alt, w)))
	}
	out = append(out, "")
	pr, section := price(it.Price), sectionName(s)
	if sp := price(s.Price); pr == "" && sp != "" {
		pr, section = ui.Trf("%s for the %s", "%s за «%s»", sp, section), ""
	}
	add(ui.KV(ui.Tr("Price", "Цена"), pr, w))
	add(ui.KV(ui.Tr("Weight", "Вес"), ui.Clean(it.Weight), w))
	add(ui.KV(ui.Tr("Calories", "Калории"), ui.Clean(it.Calories), w))
	add(ui.KV(ui.Tr("Tags", "Метки"), chips(it, false), w))
	add(ui.KV(ui.Tr("Section", "Раздел"), section, w))
	add(ui.Section(ui.Tr("Composition", "Состав"), ui.Wrap(composition(it.Composition), w)))
	return p.scroll.Render(strings.Join(out, "\n"), w, h)
}

// composition cleans the ingredients text; "RU;EN" pairs become two
// paragraphs.
func composition(s string) string {
	s = ui.CleanMulti(s)
	if strings.Count(s, ";") == 1 {
		a, b, _ := strings.Cut(s, ";")
		a, b = strings.TrimSpace(a), strings.TrimSpace(b)
		if a != "" && b != "" && hasCyrillic(a) != hasCyrillic(b) {
			return a + "\n\n" + b
		}
	}
	return s
}

func hasCyrillic(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Cyrillic, r) {
			return true
		}
	}
	return false
}
