package food

import (
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"hse-app-z/internal/api"
	"hse-app-z/internal/ui"
)

// city is a campus city; cityAll is the "no filter" value.
type city int

const (
	cityAll city = iota
	cityMoscow
	citySPb
	cityNN
	cityPerm
	cityOther
)

// filterCycle is the order c steps through.
var filterCycle = []city{cityAll, cityMoscow, citySPb, cityNN, cityPerm}

// names are the city's English and Russian names.
func (c city) names() (en, ru string) {
	switch c {
	case cityAll:
		return "All cities", "Все города"
	case cityMoscow:
		return "Moscow", "Москва"
	case citySPb:
		return "St. Petersburg", "Санкт-Петербург"
	case cityNN:
		return "Nizhny Novgorod", "Нижний Новгород"
	case cityPerm:
		return "Perm", "Пермь"
	}
	return "Other", "Другое"
}

// String is the city name in the interface language.
func (c city) String() string { return ui.Tr(c.names()) }

// short is the city name for narrow pane titles.
func (c city) short() string {
	switch c {
	case cityAll:
		return ui.Tr("All", "Все")
	case citySPb:
		return ui.Tr("SPb", "СПб")
	case cityNN:
		return ui.Tr("N. Novgorod", "Н. Новгород")
	}
	return c.String()
}

// in is " in Moscow" / " в Москве" for empty-state messages.
func (c city) in() string {
	switch c {
	case cityAll:
		return ""
	case cityMoscow:
		return ui.Tr(" in Moscow", " в Москве")
	case citySPb:
		return ui.Tr(" in St. Petersburg", " в Санкт-Петербурге")
	case cityNN:
		return ui.Tr(" in Nizhny Novgorod", " в Нижнем Новгороде")
	case cityPerm:
		return ui.Tr(" in Perm", " в Перми")
	}
	return ui.Tr(" in other cities", " в других городах")
}

// trimCity drops a leading "City, " (in either language) from a building
// name: the pane title names the city already.
func trimCity(name string, c city) string {
	en, ru := c.names()
	for _, n := range []string{en, ru} {
		if rest, ok := strings.CutPrefix(name, n+", "); ok && rest != "" {
			return rest
		}
	}
	return name
}

func cityForLng(lng float64) city {
	switch {
	case lng < 34:
		return citySPb
	case lng < 42:
		return cityMoscow
	case lng < 50:
		return cityNN
	}
	return cityPerm
}

// cityOf derives a building's city from its coordinates (or its cafes'),
// falling back to the name ("St. Petersburg, …", "N.N., …", "P …"). It
// works on the raw API data, whatever the interface language.
func cityOf(g api.CafeGroup) city {
	if validLL(g.Coordinates) {
		return cityForLng(g.Coordinates.Lng.V)
	}
	for _, c := range g.Cafes {
		if validLL(c.Coordinates) {
			return cityForLng(c.Coordinates.Lng.V)
		}
	}
	n := strings.ToLower(ui.Clean(g.CampusName))
	switch {
	case strings.Contains(n, "st. petersburg") || strings.Contains(n, "спб") || strings.Contains(n, "петербург"):
		return citySPb
	case strings.HasPrefix(n, "n.n.") || strings.Contains(n, "nizhny") || strings.Contains(n, "нижн") || strings.Contains(n, "н.н."):
		return cityNN
	case strings.Contains(n, "пермь") || strings.Contains(n, "perm") || strings.HasPrefix(n, "p "):
		return cityPerm
	case strings.Contains(n, "moscow") || strings.Contains(n, "москва"):
		return cityMoscow
	}
	return cityOther
}

// buildingName is the readable header for a building: the fixture uses
// "N.N., …" and "P …" prefixes for Nizhny Novgorod and Perm.
func buildingName(g api.CafeGroup, c city) string {
	n := ui.Clean(g.CampusName)
	switch {
	case c == cityNN && strings.HasPrefix(n, "N.N., "):
		n = cityNN.String() + ", " + strings.TrimPrefix(n, "N.N., ")
	case c == cityPerm && strings.HasPrefix(n, "P "):
		n = cityPerm.String() + ", " + strings.TrimPrefix(n, "P ")
	}
	if n == "" {
		n = strings.TrimSpace(ui.Tr("Building ", "Здание ") + ui.Clean(string(g.CampusID)))
	}
	return n
}

// place is a building with its derived city, in display order.
type place struct {
	group api.CafeGroup
	city  city
}

// name is the building's header in the interface language.
func (pl place) name() string { return buildingName(pl.group, pl.city) }

// sortPlaces orders buildings Moscow first (then the other cities), by
// name within a city. Cafes keep the server order.
func sortPlaces(groups []api.CafeGroup) []place {
	out := make([]place, 0, len(groups))
	for _, g := range groups {
		out = append(out, place{group: g, city: cityOf(g)})
	}
	// By the raw name: the order must not change with the language.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].city != out[j].city {
			return out[i].city < out[j].city
		}
		return strings.ToLower(ui.Clean(out[i].group.CampusName)) < strings.ToLower(ui.Clean(out[j].group.CampusName))
	})
	return out
}

// validLL reports whether coordinates look real: present, in range, and
// not the 0,0 placeholder (which would put the cafe in St. Petersburg's
// filter and open the map in the Gulf of Guinea).
func validLL(ll *api.LatLng) bool {
	if !ll.Valid() {
		return false
	}
	lat, lng := ll.Lat.V, ll.Lng.V
	return lat >= -90 && lat <= 90 && lng >= -180 && lng <= 180 && (lat != 0 || lng != 0)
}

// zoneOf is the cafe's local zone: from its own coordinates, else the
// building's, else the city its name gives (Perm is UTC+5), else Moscow.
func zoneOf(c api.Cafe, g api.CafeGroup) *time.Location {
	switch {
	case validLL(c.Coordinates):
		return api.ZoneForLng(c.Coordinates.Lng.V)
	case validLL(g.Coordinates):
		return api.ZoneForLng(g.Coordinates.Lng.V)
	case cityOf(g) == cityPerm:
		return api.ZoneForCampus("CAMPUS_PERM")
	}
	return api.Moscow
}

// location is where m opens the map.
func location(c api.Cafe, g api.CafeGroup) (lat, lng float64, ok bool) {
	switch {
	case validLL(c.Coordinates):
		return c.Coordinates.Lat.V, c.Coordinates.Lng.V, true
	case validLL(g.Coordinates):
		return g.Coordinates.Lat.V, g.Coordinates.Lng.V, true
	}
	return 0, 0, false
}

// photos are the cafe's openable photo links: web links only (ui.OpenURL
// would refuse anything else, and mailto:/tel: aren't photos), the
// gallery first, else the single photo.
func photos(c api.Cafe) []string {
	ok := func(u string) bool {
		u = strings.TrimSpace(u)
		l := strings.ToLower(u)
		return ui.SafeURL(u) && (strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "http://"))
	}
	var out []string
	for _, p := range c.Photos {
		if ok(p) {
			out = append(out, strings.TrimSpace(p))
		}
	}
	if len(out) == 0 && ok(c.Photo) {
		out = append(out, strings.TrimSpace(c.Photo))
	}
	return out
}

// photo is the first usable photo link ("" when there is none).
func photo(c api.Cafe) string {
	if ps := photos(c); len(ps) > 0 {
		return ps[0]
	}
	return ""
}

func photoCount(c api.Cafe) int { return len(photos(c)) }

func address(c api.Cafe, g api.CafeGroup) string {
	if a := ui.Clean(c.Address); a != "" {
		return a
	}
	return ui.Clean(g.CampusName)
}

// navigation renders "floor 1, room 104".
func navigation(n *api.CafeNavigation) string {
	if n == nil {
		return ""
	}
	var parts []string
	if f := ui.Clean(string(n.Floor)); f != "" {
		parts = append(parts, ui.Tr("floor ", "этаж ")+f)
	}
	if r := ui.Clean(string(n.Room)); r != "" {
		parts = append(parts, ui.Tr("room ", "пом. ")+r)
	}
	return strings.Join(parts, ", ")
}

// statusText is the full label and its style.
func statusText(st api.OpenStatus) (string, lipgloss.Style) {
	label := ui.OpenLabel(st)
	switch {
	case !st.Known:
		return label, ui.StyleDim
	case st.Open:
		return label, ui.StyleOK
	case label == "":
		return ui.Tr("closed", "закрыто"), ui.StyleDim
	}
	return label, ui.StyleDim
}

// shortStatus drops the "open · " / "closed · " prefixes for narrow rows.
func shortStatus(label string) string {
	for _, pre := range []string{"open · ", "closed today · ", "closed · ", "открыто · ", "сегодня закрыто · ", "закрыто · "} {
		if strings.HasPrefix(label, pre) {
			return strings.TrimPrefix(label, pre)
		}
	}
	return label
}

// upcomingClosed lists the closed dates within the next 30 days (from
// today in the cafe's zone), compressed into ranges: "27 Aug – 14 Sep, 1 Oct"
// ("27 авг – 14 сен, 1 окт").
// Blank and malformed entries and past dates are ignored.
func upcomingClosed(dates []string, now time.Time) string {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	end := today.AddDate(0, 0, 30)
	seen := map[time.Time]bool{}
	var days []time.Time
	for _, s := range dates {
		d, err := time.Parse("2006-01-02", strings.TrimSpace(s))
		if err != nil || d.Before(today) || !d.Before(end) || seen[d] {
			continue
		}
		seen[d] = true
		days = append(days, d)
	}
	if len(days) == 0 {
		return ""
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Before(days[j]) })
	var parts []string
	start, prev := days[0], days[0]
	flush := func() {
		if start.Equal(prev) {
			parts = append(parts, ui.FmtDayMonth(start))
		} else {
			parts = append(parts, ui.FmtDayMonth(start)+" – "+ui.FmtDayMonth(prev))
		}
	}
	for _, d := range days[1:] {
		if d.Equal(prev.AddDate(0, 0, 1)) {
			prev = d
			continue
		}
		flush()
		start, prev = d, d
	}
	flush()
	return strings.Join(parts, ", ")
}

var week = []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday, time.Sunday}

// schedule renders the weekly hours, one row per day, today highlighted.
func schedule(hours []api.OpeningHours, today time.Weekday, w int) string {
	if len(hours) == 0 {
		return ui.StyleDim.Render(ui.Trunc(ui.Tr("Opening hours aren't published", "Часы работы не опубликованы"), w))
	}
	todayNote := ui.Tr("  today", "  сегодня")
	var lines []string
	for _, d := range week {
		txt := "—"
		h, ok := api.HoursFor(hours, d)
		if ok {
			txt = hoursRange(h)
		}
		line := ui.PadRight(dayShort(d), 5) + txt
		switch {
		case d == today && ui.Width(line)+ui.Width(todayNote) <= w:
			lines = append(lines, ui.StyleKey.Render(line)+ui.StyleDim.Render(todayNote))
		case d == today:
			lines = append(lines, ui.StyleKey.Render(ui.Trunc(line, w)))
		case !ok || !h.IsOpen:
			lines = append(lines, ui.StyleDim.Render(ui.Trunc(line, w)))
		default:
			lines = append(lines, ui.Trunc(line, w))
		}
	}
	return strings.Join(lines, "\n")
}

// dayShort is "Mon" / "Пн".
func dayShort(d time.Weekday) string { return capFirst(ui.WeekdayShort(d)) }

func capFirst(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	return strings.ToUpper(string(r[:1])) + string(r[1:])
}

// hoursRange is a day's hours: "09:00–21:00", "closed", "open".
func hoursRange(h api.OpeningHours) string {
	switch {
	case !h.IsOpen:
		return ui.Tr("closed", "закрыто")
	case strings.TrimSpace(h.StartTime) == "" && strings.TrimSpace(h.EndTime) == "":
		return ui.Tr("open", "открыто")
	}
	return ui.Clean(h.Range())
}

// todayText localises api.OpenStatus.Today ("09:00–14:00, 15:00–21:00",
// "closed", "open").
func todayText(s string) string {
	parts := strings.Split(ui.Clean(s), ", ")
	for i, p := range parts {
		switch p {
		case "closed":
			parts[i] = ui.Tr("closed", "закрыто")
		case "open":
			parts[i] = ui.Tr("open", "открыто")
		}
	}
	return strings.Join(parts, ", ")
}
