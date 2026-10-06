package campus

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"hse-app-z/internal/api"
	"hse-app-z/internal/ui"
	"hse-app-z/internal/ui/uitest"
)

func checkSize(t *testing.T, view string, w, h int) {
	t.Helper()
	lines := strings.Split(view, "\n")
	if len(lines) > h {
		t.Errorf("view has %d lines, want ≤ %d", len(lines), h)
	}
	for i, l := range lines {
		if n := ansi.StringWidth(l); n > w {
			t.Errorf("line %d is %d cells wide (max %d): %q", i, n, w, l)
		}
	}
}

func newPage(t *testing.T) (*Page, *uitest.Harness) {
	t.Helper()
	p := New(uitest.Ctx(t)).(*Page)
	h := uitest.New(t, p)
	return p, h
}

func selectBuilding(t *testing.T, p *Page, name string) {
	t.Helper()
	for i, r := range p.rows {
		if r.b != nil && r.b.Name == name {
			p.list.Select(i)
			return
		}
	}
	t.Fatalf("building %q not in list", name)
}

func TestRender(t *testing.T) {
	p, h := newPage(t)
	selectBuilding(t, p, "Pokrovsky B., h.11")
	for _, sz := range [][2]int{{100, 30}, {60, 12}, {120, 30}, {60, 10}} {
		v := h.View(sz[0], sz[1])
		checkSize(t, v, sz[0], sz[1])
	}
	v := h.View(120, 30)
	t.Logf("120×30:\n%s", v)
	for _, want := range []string{"Buildings", "Moscow", "Pokrovsky B., h.11", "7 cafés · library", "Cafés", "m open map"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q", want)
		}
	}
	// Scroll the detail down to the libraries.
	for i := 0; i < 40; i++ {
		h.Key("J")
	}
	v = h.View(100, 30)
	checkSize(t, v, 100, 30)
	if !strings.Contains(v, "Libraries") && !strings.Contains(v, "Читальный зал") {
		t.Errorf("scrolling didn't reach the libraries:\n%s", v)
	}
	t.Logf("100×30 scrolled:\n%s", v)
	// Selection change resets the detail scroll.
	h.Key("j")
	if p.scroll.Offset != 0 {
		t.Errorf("scroll not reset on selection change: %d", p.scroll.Offset)
	}
}

func TestOrderingAndFilter(t *testing.T) {
	p, h := newPage(t)
	if len(p.groups) != 4 {
		t.Fatalf("want 4 campuses, got %d", len(p.groups))
	}
	var names []string
	for _, g := range p.groups {
		names = append(names, g.name)
	}
	if got := strings.Join(names, ","); got != "Moscow,Nizhny Novgorod,Perm,St. Petersburg" {
		t.Errorf("campus order: %s", got)
	}
	if p.rows[0].b != nil || p.rows[0].header != "Moscow" {
		t.Errorf("first row should be the Moscow header: %+v", p.rows[0])
	}
	if !p.list.HasSelection() || p.list.Cursor != 1 {
		t.Errorf("cursor should start on the first building, got %d", p.list.Cursor)
	}
	// Buildings sorted by name inside a campus.
	prev := ""
	for _, r := range p.rows[1:] {
		if r.b == nil {
			break
		}
		n := strings.ToLower(buildingName(*r.b))
		if n < prev {
			t.Errorf("buildings not sorted: %q after %q", n, prev)
		}
		prev = n
	}
	total := p.buildingCount()

	wantFilters := []string{"Moscow", "Nizhny Novgorod", "Perm", "St. Petersburg", "All"}
	for _, want := range wantFilters {
		h.Key("c")
		if p.filterName() != want {
			t.Fatalf("filter = %q, want %q", p.filterName(), want)
		}
		v := h.View(100, 30)
		checkSize(t, v, 100, 30)
		if want == "All" {
			if len(p.rows) != total+len(p.groups) {
				t.Errorf("All: %d rows, want %d", len(p.rows), total+len(p.groups))
			}
			continue
		}
		for _, r := range p.rows {
			if r.b == nil {
				t.Errorf("filter %s: unexpected header row", want)
				continue
			}
			if api.CampusName(r.b.Campus) != want {
				t.Errorf("filter %s: building %s from %s", want, r.b.Name, r.b.Campus)
			}
		}
		if !p.list.HasSelection() || p.list.Cursor != 0 {
			t.Errorf("filter %s: cursor %d", want, p.list.Cursor)
		}
		if want == "Perm" {
			if !strings.Contains(v, "P B. Gagarina 37") {
				t.Errorf("Perm filter view lacks Gagarina:\n%s", v)
			}
			t.Logf("Perm filter:\n%s", v)
		}
	}
	// Narrow: the compact filter line still fits.
	h.Key("c", "c")
	checkSize(t, h.View(60, 12), 60, 12)
	if !strings.Contains(h.View(60, 12), "Nizhny") {
		t.Errorf("compact filter line lacks campus name:\n%s", h.View(60, 12))
	}
}

func TestLibraryHoursAtFixedClock(t *testing.T) {
	p, h := newPage(t)
	selectBuilding(t, p, "Pokrovsky B., h.11")
	d := ansi.Strip(p.detail(*p.selected(), 80))
	t.Logf("detail:\n%s", d)
	for _, want := range []string{
		"Центральная библиотека · корпус N",
		"3, 4 этаж: open · until 21:00 · today 10:00–21:00",
		"Mon–Fri 10:00–21:00, Sat 10:00–18:00, Sun closed",
		"2 этаж: open 24h",
		"daily 24h",
		"open · until 20:00 · today 12:00–20:00",
		"phone +7 495 916-89-27",
		"Sanitary day: closed for cleaning on the first Friday of each month",
		"Столовая на Покровке",
		"open · until 21:00 · today 08:30–21:00",
		"временно закрыта", // a café banner
	} {
		if !strings.Contains(d, want) {
			t.Errorf("detail lacks %q", want)
		}
	}
	_ = h

	// Myasnitskaya: a rule open only Tue/Thu, phone with an extension.
	selectBuilding(t, p, "Myasnitskaya ul., h.20")
	d = ansi.Strip(p.detail(*p.selected(), 90))
	for _, want := range []string{
		"Необходимо предварительное бронирование книг: open · until 20:00",
		"Mon closed, Tue 12:00–20:00, Wed closed, Thu 12:00–20:00, Fri–Sun closed",
		"+7 495 772-95-90 ext. 15571",
	} {
		if !strings.Contains(d, want) {
			t.Errorf("Myasnitskaya detail lacks %q:\n%s", want, d)
		}
	}
}

func TestLibraryZone(t *testing.T) {
	// 12:30 in Moscow is 14:30 in Perm: a 14:00–15:00 office is open in
	// Perm but would still be closed by Moscow time.
	hours := []api.OpeningHours{{DayOfWeek: "tuesday", IsOpen: true, StartTime: "14:00", EndTime: "15:00"}}
	b := api.Building{ID: "x", Name: "Perm test", Campus: "CAMPUS_PERM", LibrariesV3: []api.Library{{
		Name:    "Lib",
		Offices: []api.LibraryOffice{{Name: "Hall", Rules: []api.LibraryRule{{Schedule: &api.LibrarySchedule{OpeningHours: hours}}}}},
	}}}
	p := New(uitest.Ctx(t)).(*Page)
	d := ansi.Strip(p.detail(b, 80))
	if !strings.Contains(d, "open · until 15:00") {
		t.Errorf("Perm office should be open at 14:30 local:\n%s", d)
	}
	b.Campus = "CAMPUS_MOS"
	d = ansi.Strip(p.detail(b, 80))
	if !strings.Contains(d, "closed · opens 14:00") {
		t.Errorf("Moscow office should open at 14:00:\n%s", d)
	}
}

func TestWeekly(t *testing.T) {
	oh := func(day string, open bool, s, e string) api.OpeningHours {
		return api.OpeningHours{DayOfWeek: day, IsOpen: open, StartTime: s, EndTime: e}
	}
	cases := []struct {
		hours []api.OpeningHours
		want  string
	}{
		{[]api.OpeningHours{
			oh("monday", true, "10:00", "21:00"), oh("tuesday", true, "10:00", "21:00"), oh("wednesday", true, "10:00", "21:00"),
			oh("thursday", true, "10:00", "21:00"), oh("friday", true, "10:00", "21:00"), oh("saturday", true, "10:00", "18:00"),
		}, "Mon–Fri 10:00–21:00, Sat 10:00–18:00, Sun closed"},
		// Out of order, a gap mid-week, explicit closed day, unknown day name.
		{[]api.OpeningHours{
			oh("friday", true, "09:00", "17:00"), oh("monday", true, "09:00", "17:00"), oh("tuesday", true, "09:00", "17:00"),
			oh("thursday", true, "09:00", "17:00"), oh("sunday", false, "", ""), oh("funday", true, "01:00", "02:00"),
		}, "Mon–Tue 09:00–17:00, Wed closed, Thu–Fri 09:00–17:00, Sat–Sun closed"},
		{nil, "closed all week"},
		{[]api.OpeningHours{
			oh("mon", true, "", ""), oh("tue", true, "", ""), oh("wed", true, "", ""), oh("thu", true, "", ""),
			oh("fri", true, "", ""), oh("sat", true, "", ""), oh("sun", true, "", ""),
		}, "daily 24h"},
		{[]api.OpeningHours{oh("saturday", true, "11:00", ""), oh("sunday", true, "00:00", "23:59")},
			"Mon–Fri closed, Sat from 11:00, Sun 24h"},
	}
	for _, c := range cases {
		if got := Weekly(c.hours); got != c.want {
			t.Errorf("Weekly = %q, want %q", got, c.want)
		}
	}
}

func TestFormatPhone(t *testing.T) {
	for in, want := range map[string]string{
		"+74957729590,15241":   "+7 495 772-95-90 ext. 15241",
		"+74959168927":         "+7 495 916-89-27",
		"+76635911,61291":      "+76635911 ext. 61291",
		"84957729590":          "+7 495 772-95-90",
		"+74957729590,":        "+7 495 772-95-90",
		"+7495,доб. 1":         "+7495,доб. 1",
		"":                     "",
		"\x1b[31m+7495\x1b[0m": "+7495",
	} {
		if got := FormatPhone(in); got != want {
			t.Errorf("FormatPhone(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestKeysAndMenu(t *testing.T) {
	p, h := newPage(t)
	selectBuilding(t, p, "MIEM")
	h.Key("m")
	st := h.Statuses()
	if len(st) == 0 || !strings.HasPrefix(st[len(st)-1], "Opened https://yandex.ru/maps/?pt=37.409980,55.803430") {
		t.Errorf("m: statuses %v", st)
	}
	h.Key("y")
	st = h.Statuses()
	if !strings.HasPrefix(st[len(st)-1], "Copied address: ") {
		t.Errorf("y: statuses %v", st)
	}
	h.Key("enter")
	m, ok := h.LastMenu()
	if !ok || len(m.Actions) != 2 || m.Title != "MIEM" {
		t.Fatalf("menu: %+v", m)
	}
	if !h.RunAction("copy address") {
		t.Fatal("no copy action")
	}
	if !h.RunAction("map") {
		t.Fatal("no map action")
	}
}

func TestEdgeCases(t *testing.T) {
	p, h := newPage(t)
	nan := []float64{}
	weird := []api.CampusGroup{
		{Campus: "", Buildings: []api.Building{
			{ID: "1", Name: "\x1b[31mEvil\x1b[0m\u2800 building", Location: &api.GeoPoint{Coordinates: nan}},
			{ID: "2", Location: nil, Cafes: []api.Cafe{{}}, LibrariesV3: []api.Library{{Offices: []api.LibraryOffice{{
				Rules:        []api.LibraryRule{{Name: "x", Schedule: nil}, {Schedule: &api.LibrarySchedule{SanitaryDay: "SANITARY_DAY_WEIRD"}}},
				PhoneNumbers: []string{"", "+7"},
			}}}}},
			{ID: "3", Name: strings.Repeat("Очень длинное название корпуса ", 10), Address: strings.Repeat("ул. ", 50),
				Location: &api.GeoPoint{Coordinates: []float64{0, 0}}},
		}},
		{Campus: "CAMPUS_XYZ", Buildings: []api.Building{{ID: "4", Name: "Other campus", Campus: "CAMPUS_XYZ",
			Location: &api.GeoPoint{Coordinates: []float64{500, 500}}}}},
		{Campus: "CAMPUS_EMPTY"},
	}
	h.Send(ui.Result[[]api.CampusGroup]{ID: p.id, Seq: p.load.Seq, Data: weird})
	if len(p.groups) != 2 {
		t.Fatalf("groups: %d", len(p.groups))
	}
	for i := 0; i < len(p.rows); i++ {
		p.list.Select(i)
		for _, sz := range [][2]int{{100, 30}, {60, 12}, {60, 10}} {
			v := h.View(sz[0], sz[1])
			checkSize(t, v, sz[0], sz[1])
			if strings.Contains(v, "\x1b[31m") || strings.Contains(v, "\u2800") {
				t.Errorf("unsanitised text in view")
			}
		}
		h.Key("m", "y", "enter")
	}
	t.Logf("edge 100×30:\n%s", h.View(100, 30))

	// Empty response.
	h.Send(ui.Result[[]api.CampusGroup]{ID: p.id, Seq: p.load.Seq, Data: nil})
	v := h.View(60, 12)
	checkSize(t, v, 60, 12)
	if !strings.Contains(v, "No buildings") {
		t.Errorf("empty view:\n%s", v)
	}
	h.Key("j", "c", "m", "y", "enter", "J")

	// Results for other pages or stale sequences are ignored.
	h.Send(ui.Result[[]api.CampusGroup]{ID: p.id + 1000, Seq: p.load.Seq, Data: weird})
	if p.buildingCount() != 0 {
		t.Errorf("foreign result accepted")
	}
}

func TestReload(t *testing.T) {
	p, h := newPage(t)
	selectBuilding(t, p, "MIEM")
	h.Key("c") // Moscow
	before := p.load.Seq
	h.Send(ui.ReloadMsg{})
	if p.load.Seq != before+1 {
		t.Errorf("ReloadMsg didn't refetch")
	}
	if p.filterName() != "Moscow" {
		t.Errorf("filter lost on reload: %s", p.filterName())
	}
	h.Key("r")
	if p.load.Seq != before+2 || p.load.Loading {
		t.Errorf("r didn't refetch")
	}
}
