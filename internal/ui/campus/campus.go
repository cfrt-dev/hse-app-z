// Package campus is the Campus tab: HSE buildings grouped by campus, with
// their cafés and libraries (opening hours, phones, sanitary days).
package campus

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"hse-app-z/internal/api"
	"hse-app-z/internal/ui"
)

// row is one line of the building list: a campus header (b == nil) or a
// building.
type row struct {
	header string // the campus's English name (shown via code)
	code   string // header's campus code
	b      *api.Building
}

// campusGroup is the merged, sorted set of buildings of one campus.
type campusGroup struct {
	code      string
	name      string // English name (sort key); display() localises it
	buildings []api.Building
}

// display is the campus name in the interface language.
func (g campusGroup) display() string { return campusName(g.code) }

// campusName localises a campus code ("Other" when it is blank).
func campusName(code string) string {
	if n := ui.Clean(ui.CampusName(code)); n != "" {
		return n
	}
	return ui.Tr("Other", "Другое")
}

// Page is the Campus tab.
type Page struct {
	ctx  *ui.Ctx
	id   int
	load ui.Load

	groups []campusGroup
	filter int // 0 = all campuses, i = groups[i-1]
	rows   []row

	list   ui.List
	scroll ui.Scroll
}

// New returns the tab page.
func New(ctx *ui.Ctx) ui.Page {
	p := &Page{ctx: ctx, id: ui.NewID()}
	p.list.Skip = func(i int) bool { return i >= 0 && i < len(p.rows) && p.rows[i].b == nil }
	return p
}

func (p *Page) Title() string   { return ui.Tr("Campus", "Кампус") }
func (p *Page) Capturing() bool { return false }

func (p *Page) Hints() []ui.Hint {
	return []ui.Hint{
		{Key: "enter", Desc: ui.Tr("actions", "действия")},
		{Key: "m", Desc: ui.Tr("map", "карта")},
		{Key: "c", Desc: ui.Tr("campus filter", "выбор кампуса")},
		{Key: "y", Desc: ui.Tr("copy address", "копировать адрес")},
		{Key: "J/K", Desc: ui.Tr("scroll details", "прокрутка деталей")},
		{Key: "r", Desc: ui.Tr("refresh", "обновить")},
	}
}

func (p *Page) Init() tea.Cmd { return p.fetch() }

func (p *Page) fetch() tea.Cmd {
	if p.ctx == nil || p.ctx.API == nil {
		return nil
	}
	client := p.ctx.API
	return ui.Fetch(p.id, p.load.Begin(), func(c context.Context) ([]api.CampusGroup, api.Meta, error) {
		return client.Buildings(c)
	})
}

func (p *Page) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case ui.Result[[]api.CampusGroup]:
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
		return p.fetch()
	case "c":
		p.cycleFilter()
		return nil
	case "m":
		if b := p.selected(); b != nil {
			return openMap(*b)
		}
	case "y":
		if b := p.selected(); b != nil {
			return copyAddress(*b)
		}
	case "enter":
		if b := p.selected(); b != nil {
			return p.menu(*b)
		}
	}
	return nil
}

// ------------------------------------------------------------------ data

func (p *Page) setData(data []api.CampusGroup) {
	prevCode := p.filterCode()
	prevKey := p.selectedKey()

	byCode := map[string]*campusGroup{}
	var order []*campusGroup
	for _, g := range data {
		for _, b := range g.Buildings {
			code := strings.TrimSpace(g.Campus)
			if code == "" {
				code = strings.TrimSpace(b.Campus)
			}
			if strings.TrimSpace(b.Campus) == "" {
				b.Campus = code
			}
			key := strings.ToUpper(code)
			cg := byCode[key]
			if cg == nil {
				cg = &campusGroup{code: code, name: ui.Clean(api.CampusName(code))}
				if cg.name == "" {
					cg.name = "Other"
				}
				byCode[key] = cg
				order = append(order, cg)
			}
			cg.buildings = append(cg.buildings, b)
		}
	}
	sort.SliceStable(order, func(i, j int) bool {
		mi, mj := isMoscow(order[i].code), isMoscow(order[j].code)
		if mi != mj {
			return mi
		}
		return strings.ToLower(order[i].name) < strings.ToLower(order[j].name)
	})
	p.groups = p.groups[:0]
	for _, g := range order {
		bs := g.buildings
		sort.SliceStable(bs, func(i, j int) bool {
			ni, nj := strings.ToLower(buildingName(bs[i])), strings.ToLower(buildingName(bs[j]))
			if ni != nj {
				return ni < nj
			}
			return bs[i].ID < bs[j].ID
		})
		p.groups = append(p.groups, *g)
	}

	p.filter = 0
	for i, g := range p.groups {
		if prevCode != "" && strings.EqualFold(g.code, prevCode) {
			p.filter = i + 1
		}
	}
	p.rebuild(prevKey)
}

func isMoscow(code string) bool { return strings.EqualFold(code, "CAMPUS_MOS") }

// rebuild regenerates the visible rows for the current filter and selects
// the building with key (or the first building).
func (p *Page) rebuild(key string) {
	p.rows = p.rows[:0]
	for i := range p.groups {
		g := &p.groups[i]
		if p.filter != 0 && p.filter != i+1 {
			continue
		}
		if p.filter == 0 {
			p.rows = append(p.rows, row{header: g.name, code: g.code})
		}
		for j := range g.buildings {
			p.rows = append(p.rows, row{b: &g.buildings[j]})
		}
	}
	p.list.SetLen(len(p.rows))
	sel := 0
	for i, r := range p.rows {
		if r.b != nil && key != "" && buildingKey(*r.b) == key {
			sel = i
			break
		}
	}
	if sel != p.list.Cursor {
		p.scroll.Reset()
	}
	p.list.Select(sel)
}

func (p *Page) cycleFilter() {
	if len(p.groups) == 0 {
		return
	}
	p.filter = (p.filter + 1) % (len(p.groups) + 1)
	p.rebuild("")
	p.list.Select(0)
	p.scroll.Reset()
}

func (p *Page) filterCode() string {
	if p.filter > 0 && p.filter <= len(p.groups) {
		return p.groups[p.filter-1].code
	}
	return ""
}

func (p *Page) filterName() string {
	if p.filter > 0 && p.filter <= len(p.groups) {
		return p.groups[p.filter-1].display()
	}
	return ui.Tr("All", "Все")
}

func (p *Page) selected() *api.Building {
	if !p.list.HasSelection() || p.list.Cursor >= len(p.rows) {
		return nil
	}
	return p.rows[p.list.Cursor].b
}

func (p *Page) selectedKey() string {
	if b := p.selected(); b != nil {
		return buildingKey(*b)
	}
	return ""
}

func (p *Page) buildingCount() int {
	n := 0
	for _, g := range p.groups {
		n += len(g.buildings)
	}
	return n
}

func buildingKey(b api.Building) string { return string(b.ID) + "\x00" + b.Name }

func buildingName(b api.Building) string {
	for _, s := range []string{b.Name, b.Address} {
		if c := ui.Clean(s); c != "" {
			return c
		}
	}
	if id := ui.Clean(string(b.ID)); id != "" {
		return ui.Tr("Building ", "Здание ") + id
	}
	return ui.Tr("Building", "Здание")
}

// location returns the building's coordinates when they look real.
func location(b api.Building) (lat, lng float64, ok bool) {
	if !b.Location.Valid() {
		return 0, 0, false
	}
	lat, lng = b.Location.Lat(), b.Location.Lng()
	if math.IsNaN(lat) || math.IsNaN(lng) || lat < -90 || lat > 90 || lng < -180 || lng > 180 || (lat == 0 && lng == 0) {
		return 0, 0, false
	}
	return lat, lng, true
}

func countsText(b api.Building) string {
	var parts []string
	if n := len(b.Cafes); n > 0 {
		parts = append(parts, ui.Count(n, "café", "cafés", "кафе", "кафе", "кафе"))
	}
	switch n := len(b.LibrariesV3); {
	case n == 1:
		parts = append(parts, ui.Tr("library", "библиотека"))
	case n > 1:
		parts = append(parts, ui.Count(n, "library", "libraries", "библиотека", "библиотеки", "библиотек"))
	}
	return strings.Join(parts, " · ")
}

// --------------------------------------------------------------- actions

func openMap(b api.Building) tea.Cmd {
	lat, lng, ok := location(b)
	if !ok {
		return ui.Info(ui.Tr("No location for %s", "Нет координат: %s"), buildingName(b))
	}
	return ui.OpenURL(ui.MapURL(lat, lng))
}

func copyAddress(b api.Building) tea.Cmd {
	addr := ui.Clean(b.Address)
	if addr == "" {
		return ui.Info(ui.Tr("No address for %s", "Нет адреса: %s"), buildingName(b))
	}
	return ui.Copy(addr, ui.Tr("address", "адрес"))
}

func (p *Page) menu(b api.Building) tea.Cmd {
	var actions []ui.Action
	if _, _, ok := location(b); ok {
		actions = append(actions, ui.Action{Key: "m", Label: ui.Tr("Open in map", "Открыть на карте"), Run: func() tea.Cmd { return openMap(b) }})
	}
	if ui.Clean(b.Address) != "" {
		actions = append(actions, ui.Action{Key: "y", Label: ui.Tr("Copy address", "Копировать адрес"), Run: func() tea.Cmd { return copyAddress(b) }})
	}
	if len(actions) == 0 {
		return ui.Info(ui.Tr("No actions for %s", "Нет действий: %s"), buildingName(b))
	}
	return ui.ShowMenu(buildingName(b), actions...)
}

// ------------------------------------------------------------------ view

func (p *Page) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	if sv := ui.StateView(width, height, p.load, p.buildingCount(), ui.Tr("No buildings", "Нет зданий")); sv != "" {
		return sv
	}
	lw := ui.LeftWidth(width)
	rw := ui.RightWidth(width, lw)

	left := []string{paneTitle(ui.Tr("Buildings", "Здания"), ui.LoadNote(p.load), lw)}
	if height > 1 {
		left = append(left, p.filterLine(lw))
	}
	if lh := height - len(left); lh > 0 {
		left = append(left, p.list.Render(lw, lh, p.renderRow))
	}

	right := ""
	if b := p.selected(); b != nil && rw > 0 {
		right = p.scroll.Render(balanceSGR(p.detail(*b, rw)), rw, height)
	}
	return ui.Split(width, height, lw, strings.Join(left, "\n"), right)
}

func (p *Page) filterLine(w int) string {
	values := []string{ui.Tr("All", "Все")}
	for _, g := range p.groups {
		values = append(values, g.display())
	}
	if line := ui.Tabs(values, p.filter); ui.Width(line) <= w {
		return line
	}
	compact := ui.StyleDim.Render(ui.Tr("campus: ", "кампус: ")) + ui.StyleKey.Render(p.filterName()) +
		ui.StyleDim.Render(ui.Tr(" · c to switch", " · c — сменить"))
	return ui.Trunc(compact, w)
}

func (p *Page) renderRow(i int, sel bool, w int) string {
	if i < 0 || i >= len(p.rows) {
		return ""
	}
	r := p.rows[i]
	if r.b == nil {
		return ui.HeaderRow(campusName(r.code), w)
	}
	name := buildingName(*r.b)
	inner := w - 1
	content := name
	if counts := countsText(*r.b); counts != "" {
		if nw := inner - ui.Width(counts) - 1; nw >= 12 {
			content = ui.PadRight(name, nw) + " " + ui.StyleDim.Render(counts)
		}
	}
	return ui.Row(sel, content, w)
}

// detail renders the right pane for a building, wrapped to w.
func (p *Page) detail(b api.Building, w int) string {
	now := p.ctx.Time()
	var blocks []string
	blocks = append(blocks, styleLines(ui.Wrap(buildingName(b), w), ui.StyleTitle))
	if addr := ui.Clean(b.Address); addr != "" && addr != buildingName(b) {
		blocks = append(blocks, ui.KV(ui.Tr("Address", "Адрес"), addr, w))
	}
	blocks = append(blocks, ui.KV(ui.Tr("Campus", "Кампус"), ui.Clean(ui.CampusName(b.Campus)), w))
	if lat, lng, ok := location(b); ok {
		blocks = append(blocks, ui.KV(ui.Tr("Location", "Координаты"), fmt.Sprintf("%.5f, %.5f", lat, lng)+ui.StyleDim.Render(" · ")+
			ui.StyleKey.Render("m")+ui.StyleDim.Render(ui.Tr(" open map", " открыть карту")), w))
	}

	zone := buildingZone(b)
	var cafes []string
	for _, c := range b.Cafes {
		cafes = append(cafes, cafeBlock(c, now.In(cafeZone(c, zone)), w))
	}
	blocks = append(blocks, ui.Section(ui.Tr("Cafés", "Кафе"), strings.Join(cafes, "\n")))

	var libs []string
	for _, l := range b.LibrariesV3 {
		libs = append(libs, libraryBlock(l, now.In(zone), w))
	}
	blocks = append(blocks, ui.Section(ui.Tr("Libraries", "Библиотеки"), strings.Join(libs, "\n\n")))

	if len(b.Cafes) == 0 && len(b.LibrariesV3) == 0 {
		blocks = append(blocks, "\n"+ui.StyleDim.Render(ui.Trunc(ui.Tr("No cafés or libraries here", "Здесь нет кафе и библиотек"), w)))
	}
	return ui.Lines(blocks...)
}

// buildingZone is the building's local time zone: by campus code (Perm
// is UTC+5), or by location when the code is missing.
func buildingZone(b api.Building) *time.Location {
	if strings.TrimSpace(b.Campus) == "" {
		if _, lng, ok := location(b); ok {
			return api.ZoneForLng(lng)
		}
	}
	return api.ZoneForCampus(b.Campus)
}

// cafeZone is a café's zone: from its own coordinates when it has real
// ones, else its building's (cafés without coordinates in Perm must not
// be judged by Moscow time).
func cafeZone(c api.Cafe, building *time.Location) *time.Location {
	if c.Coordinates.Valid() && (c.Coordinates.Lat.V != 0 || c.Coordinates.Lng.V != 0) {
		return c.Zone()
	}
	return building
}

// cafeBlock renders a café; now must be in the café's zone.
func cafeBlock(c api.Cafe, now time.Time, w int) string {
	name := ui.Clean(c.Name)
	if name == "" {
		name = ui.Tr("Café", "Кафе")
	}
	lines := []string{styleLines(ui.Wrap(name, w), ui.StyleBold)}
	st := api.Status(c.OpeningHours, c.ClosedDates, now)
	lines = append(lines, para(statusLine(st), 2, w))
	if banner := ui.CleanMulti(c.Banner); banner != "" {
		lines = append(lines, styleLines(para(banner, 2, w), ui.StyleWarn))
	}
	return strings.Join(lines, "\n")
}

func libraryBlock(l api.Library, now time.Time, w int) string {
	name := ui.Clean(l.Name)
	if name == "" {
		name = ui.Tr("Library", "Библиотека")
	}
	head := ui.StyleBold.Render(name)
	if hint := ui.Clean(l.AddressHint); hint != "" {
		head += ui.StyleDim.Render(" · " + hint)
	}
	lines := []string{ui.Wrap(head, w)}
	for _, o := range l.Offices {
		lines = append(lines, officeBlock(o, now, w))
	}
	return strings.Join(lines, "\n")
}

func officeBlock(o api.LibraryOffice, now time.Time, w int) string {
	name := ui.Clean(o.Name)
	if name == "" {
		name = ui.Tr("Office", "Отдел")
	}
	if hint := ui.Clean(o.AddressHint); hint != "" {
		name += ui.StyleDim.Render(" · " + hint)
	}
	lines := []string{para(name, 2, w)}
	var sanitary []string
	seen := map[string]bool{}
	for _, r := range o.Rules {
		label := ui.Clean(r.Name)
		if label != "" {
			label = ui.StyleDim.Render(label + ": ")
		}
		if r.Schedule == nil || len(r.Schedule.OpeningHours) == 0 {
			lines = append(lines, para(label+ui.StyleDim.Render(noSchedule()), 4, w))
		} else {
			// The monthly cleaning day closes the office whatever the
			// weekly hours say.
			st := api.Status(r.Schedule.OpeningHours, SanitaryDates(r.Schedule.SanitaryDay, now), now)
			lines = append(lines, para(label+statusLine(st), 4, w))
			lines = append(lines, styleLines(para(Weekly(r.Schedule.OpeningHours), 4, w), ui.StyleDim))
		}
		if r.Schedule != nil {
			if t := ui.Clean(ui.SanitaryDayText(r.Schedule.SanitaryDay)); t != "" && !seen[t] {
				seen[t] = true
				sanitary = append(sanitary, t)
			}
		}
	}
	var phones []string
	for _, ph := range o.PhoneNumbers {
		if f := FormatPhone(ph); f != "" {
			phones = append(phones, f)
		}
	}
	if len(phones) > 0 {
		lines = append(lines, para(ui.StyleDim.Render(ui.Tr("phone ", "тел. "))+strings.Join(phones, ", "), 4, w))
	}
	for _, s := range sanitary {
		lines = append(lines, styleLines(para(sanitaryLine(s), 4, w), ui.StyleDim))
	}
	return strings.Join(lines, "\n")
}

// sanitaryLine is "Sanitary day: closed for cleaning on the first Friday
// of each month" ("Санитарный день — в первую пятницу месяца").
func sanitaryLine(s string) string {
	if !ui.RU() {
		return "Sanitary day: " + s
	}
	if rest, ok := strings.CutPrefix(s, "санитарный день"); ok {
		return "Санитарный день" + rest
	}
	return "Санитарный день: " + s
}

func noSchedule() string { return ui.Tr("no schedule", "нет расписания") }

// statusLine is "open · until 21:00 · today 10:00–21:00" (the open part
// green), or a dim "no schedule".
func statusLine(st api.OpenStatus) string {
	if !st.Known {
		return ui.StyleDim.Render(noSchedule())
	}
	label := ui.Clean(ui.OpenLabel(st))
	today := todayHours(st.Today)
	if st.Open && today == "24h" {
		return ui.StyleOK.Render(ui.Tr("open 24h", "открыто круглосуточно"))
	}
	var s string
	if st.Open {
		s = ui.StyleOK.Render(label)
	} else {
		s = ui.StyleDim.Render(label)
	}
	if today != "" && today != "closed" {
		s += ui.StyleDim.Render(ui.Tr(" · today ", " · сегодня ") + hoursWord(today))
	}
	return s
}

// todayHours normalises api.OpenStatus.Today (English): split shifts
// keep their ranges, "open"/whole-day ranges become "24h".
func todayHours(s string) string {
	parts := strings.Split(ui.Clean(s), ", ")
	for i, p := range parts {
		parts[i] = normRange(p)
	}
	return strings.Join(parts, ", ")
}

// ------------------------------------------------------------- schedules

var weekOrder = []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday, time.Sunday}

// HoursText renders one day: "10:00–21:00", "24h", "closed" (in the
// interface language).
func HoursText(h api.OpeningHours) string { return hoursWord(hoursToken(h)) }

// hoursToken is a day's hours in canonical English form.
func hoursToken(h api.OpeningHours) string {
	if !h.IsOpen {
		return "closed"
	}
	s, e := ui.Clean(h.StartTime), ui.Clean(h.EndTime)
	switch {
	case s == "" && e == "":
		return "24h"
	case s == "":
		return "until " + e
	case e == "":
		return "from " + s
	}
	return normRange(s + "–" + e)
}

// hoursWord localises an hours token (or a list of them).
func hoursWord(t string) string {
	if !ui.RU() {
		return t
	}
	parts := strings.Split(t, ", ")
	for i, p := range parts {
		switch {
		case p == "closed":
			parts[i] = "закрыто"
		case p == "24h":
			parts[i] = "круглосуточно"
		case strings.HasPrefix(p, "until "):
			parts[i] = "до " + strings.TrimPrefix(p, "until ")
		case strings.HasPrefix(p, "from "):
			parts[i] = "с " + strings.TrimPrefix(p, "from ")
		}
	}
	return strings.Join(parts, ", ")
}

func normRange(r string) string {
	switch r {
	case "open", "00:00–23:59", "00:00–24:00", "0:00–24:00":
		return "24h"
	}
	return r
}

// Weekly is a compact weekly schedule grouping consecutive days with the
// same hours: "Mon–Fri 10:00–21:00, Sat 10:00–18:00, Sun closed". Days
// missing from hours count as closed.
func Weekly(hours []api.OpeningHours) string {
	type span struct {
		from, to int
		text     string
	}
	var spans []span
	for i, d := range weekOrder {
		text := "closed"
		if h, ok := api.HoursFor(hours, d); ok {
			text = hoursToken(h)
		}
		if n := len(spans); n > 0 && spans[n-1].text == text && spans[n-1].to == i-1 {
			spans[n-1].to = i
			continue
		}
		spans = append(spans, span{from: i, to: i, text: text})
	}
	if len(spans) == 1 {
		if spans[0].text == "closed" {
			return ui.Tr("closed all week", "закрыто всю неделю")
		}
		return ui.Tr("daily ", "ежедневно ") + hoursWord(spans[0].text)
	}
	parts := make([]string, 0, len(spans))
	for _, s := range spans {
		label := dayShort(weekOrder[s.from])
		if s.to > s.from {
			label += "–" + dayShort(weekOrder[s.to])
		}
		parts = append(parts, label+" "+hoursWord(s.text))
	}
	return strings.Join(parts, ", ")
}

// dayShort is "Mon" / "Пн".
func dayShort(d time.Weekday) string {
	s := []rune(ui.WeekdayShort(d))
	if len(s) == 0 {
		return ""
	}
	return strings.ToUpper(string(s[:1])) + string(s[1:])
}

// SanitaryDates lists the dates (YYYY-MM-DD) of a monthly cleaning day
// such as SANITARY_DAY_FIRST_FRIDAY or SANITARY_DAY_LAST_MONDAY in the
// month of now and the next one; nil for codes it doesn't understand.
func SanitaryDates(code string, now time.Time) []string {
	ord, day, ok := strings.Cut(strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(code)), "SANITARY_DAY_"), "_")
	if !ok {
		return nil
	}
	n := map[string]int{"FIRST": 1, "SECOND": 2, "THIRD": 3, "FOURTH": 4, "LAST": -1}[ord]
	wd, okDay := api.ParseWeekday(day)
	if n == 0 || !okDay || strings.Contains(day, "_") {
		return nil
	}
	var out []string
	for i := 0; i < 2; i++ {
		first := time.Date(now.Year(), now.Month()+time.Month(i), 1, 0, 0, 0, 0, now.Location())
		var d time.Time
		if n > 0 {
			d = first.AddDate(0, 0, (int(wd)-int(first.Weekday())+7)%7+7*(n-1))
		} else {
			last := first.AddDate(0, 1, -1)
			d = last.AddDate(0, 0, -((int(last.Weekday()) - int(wd) + 7) % 7))
		}
		if d.Month() == first.Month() {
			out = append(out, d.Format("2006-01-02"))
		}
	}
	return out
}

// FormatPhone renders "+74957729590,15241" as "+7 495 772-95-90 ext. 15241"
// ("доб." in Russian).
// Numbers it doesn't recognise are shown cleaned but otherwise unchanged.
func FormatPhone(raw string) string {
	raw = ui.Clean(raw)
	if raw == "" {
		return ""
	}
	num, ext, hasExt := strings.Cut(raw, ",")
	num, ext = strings.TrimSpace(num), strings.TrimSpace(ext)
	if hasExt && (ext == "" || !allDigits(ext)) {
		if ext == "" {
			hasExt = false
		} else {
			return raw
		}
	}
	if num == "" {
		if hasExt {
			return ui.Tr("ext. ", "доб. ") + ext
		}
		return "" // just separators
	}
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, num)
	out := num
	onlyPhoneChars := strings.Trim(num, "+0123456789 -()") == ""
	// 8 is the domestic prefix for +7; "+8…" is another country.
	russian := digits != "" && (digits[0] == '7' || (digits[0] == '8' && !strings.HasPrefix(num, "+")))
	if onlyPhoneChars && len(digits) == 11 && russian {
		d := digits[1:]
		out = "+7 " + d[0:3] + " " + d[3:6] + "-" + d[6:8] + "-" + d[8:10]
	}
	if hasExt {
		out += ui.Tr(" ext. ", " доб. ") + ext
	}
	return out
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// ----------------------------------------------------------------- utils

// para wraps s to w-indent cells and indents every line.
func para(s string, indent, w int) string {
	iw := w - indent
	if iw < 4 {
		indent, iw = 0, w
	}
	return ui.Indent(ui.Wrap(s, iw), strings.Repeat(" ", indent))
}

// styleLines styles each line separately (lipgloss would pad a multi-line
// block to its widest line).
func styleLines(s string, st lipgloss.Style) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l == "" {
			continue
		}
		lead := len(l) - len(strings.TrimLeft(l, " "))
		lines[i] = l[:lead] + st.Render(l[lead:])
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
