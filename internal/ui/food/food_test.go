package food

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"hse-app-z/internal/api"
	"hse-app-z/internal/ui"
	"hse-app-z/internal/ui/uitest"
)

func checkSize(t *testing.T, p ui.Page, w, h int) string {
	t.Helper()
	raw := p.View(w, h)
	lines := strings.Split(raw, "\n")
	if len(lines) > h {
		t.Errorf("%dx%d: %d lines", w, h, len(lines))
	}
	for i, l := range lines {
		if lw := ansi.StringWidth(l); lw > w {
			t.Errorf("%dx%d: line %d is %d wide: %q", w, h, i, lw, ansi.Strip(l))
		}
	}
	return ansi.Strip(raw)
}

func checkSizes(t *testing.T, p ui.Page) {
	t.Helper()
	for _, sz := range [][2]int{{100, 30}, {60, 12}, {60, 10}, {120, 30}, {200, 50}, {30, 5}, {1, 1}} {
		checkSize(t, p, sz[0], sz[1])
	}
}

func newFood(t *testing.T) (*Page, *uitest.Harness) {
	t.Helper()
	p := New(uitest.Ctx(t)).(*Page)
	return p, uitest.New(t, p)
}

func TestFoodRenders(t *testing.T) {
	p, h := newFood(t)
	v := h.View(100, 30)
	for _, want := range []string{"Cafés · All cities", "1st Saratovsky proezd", "Столовая на Саратовском", "open · until", "Hours", "Tue  "} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	checkSizes(t, p)
	t.Logf("food 120x30:\n%s", h.View(120, 30))
	t.Logf("food 60x12:\n%s", h.View(60, 12))
}

func TestMoscowFirstAndCityFilter(t *testing.T) {
	p, h := newFood(t)
	if p.places[0].city != cityMoscow || p.places[len(p.places)-1].city != cityPerm {
		t.Fatalf("expected Moscow first, Perm last")
	}
	h.Key("c")
	v := h.View(100, 30)
	if !strings.Contains(v, "Cafés · Moscow") || strings.Contains(v, "Kantemirovskaya") {
		t.Fatalf("Moscow filter:\n%s", v)
	}
	h.Key("c")
	if v := h.View(100, 30); !strings.Contains(v, "Cafés · St. Petersburg") || !strings.Contains(v, "Kantemirovskaya") {
		t.Fatalf("SPb filter:\n%s", v)
	}
	h.Key("c")
	if v := h.View(100, 30); !strings.Contains(v, "Cafés · Nizhny Novgorod") || !strings.Contains(v, " B. Pecherskaya, 25/12") {
		t.Fatalf("NN filter:\n%s", v)
	}
	h.Key("c")
	v = h.View(100, 30)
	if !strings.Contains(v, " Studencheskaya, 38") {
		t.Fatalf("Perm filter:\n%s", v)
	}
	// Perm is UTC+5: the detail shows the local time.
	if !strings.Contains(v, "14:30 UTC+5") {
		t.Errorf("Perm local time missing:\n%s", v)
	}
	h.Key("c")
	if !strings.Contains(h.View(100, 30), "All cities") {
		t.Fatal("c should wrap to All")
	}
}

func TestOpenOnly(t *testing.T) {
	p, h := newFood(t)
	all := len(p.rows)
	h.Key("o")
	if len(p.rows) >= all || len(p.rows) == 0 {
		t.Fatalf("open-only should filter (%d → %d)", all, len(p.rows))
	}
	for _, r := range p.rows {
		if r.header() {
			continue
		}
		c, g := p.cafeAt(r)
		if !p.status(c, g).Open {
			t.Fatalf("%s is closed but listed", c.Name)
		}
	}
	if !strings.Contains(h.View(100, 30), "open now") {
		t.Error("filter not shown in the title")
	}
	// Nothing open in a city → explanatory empty state.
	p.places = p.places[:1]
	p.places[0].group.Cafes = []api.Cafe{{Name: "Shut", OpeningHours: []api.OpeningHours{{DayOfWeek: "tuesday"}}}}
	p.rebuild()
	if v := h.View(100, 30); !strings.Contains(v, "Nothing open right now") {
		t.Fatalf("empty state:\n%s", v)
	}
	checkSizes(t, p)
}

func TestEnterOpensMenu(t *testing.T) {
	p, h := newFood(t)
	c, _, _ := p.selected()
	if !c.HasMenu {
		t.Fatalf("first cafe %q should have a menu", c.Name)
	}
	h.Key("enter")
	mp := h.LastPushed()
	if mp == nil {
		t.Fatal("enter should push the menu page")
	}
	if !strings.HasPrefix(mp.Title(), "Menu · Столовая на Саратовском") {
		t.Fatalf("title %q", mp.Title())
	}
	mh := uitest.New(t, mp)
	v := mh.View(100, 30)
	for _, want := range []string{"Tuesday · today", "“FIX” set — 280 ₽", "Baked beetroot salad", "Composition"} {
		if !strings.Contains(v, want) {
			t.Errorf("menu lacks %q:\n%s", want, v)
		}
	}
	mh.Key("l")
	if v := mh.View(100, 30); !strings.Contains(v, "Wednesday") || strings.Contains(v, "today") {
		t.Fatalf("l should go to Wednesday:\n%s", v)
	}
	mh.Key("h", "h")
	if v := mh.View(100, 30); !strings.Contains(v, "Monday") {
		t.Fatalf("h should go back to Monday:\n%s", v)
	}
	mh.Key("h")
	if st := mh.Statuses(); len(st) == 0 || !strings.Contains(st[len(st)-1], "No menu before Monday") {
		t.Fatalf("expected a note at the first day: %v", st)
	}
	mh.Key("t")
	if v := mh.View(100, 30); !strings.Contains(v, "Tuesday · today") {
		t.Fatalf("t should return to today:\n%s", v)
	}
	// Navigation and detail: breakfast items carry their own price and a RU;EN composition.
	mh.Key("G")
	checkSizes(t, mp)
	t.Logf("menu 120x30:\n%s", mh.View(120, 30))
}

func TestMenuDetailAndEdgeCases(t *testing.T) {
	ctx := uitest.Ctx(t)
	mp := newMenuPage(ctx, api.Cafe{ID: "x", Name: "Test \"Cafe\" 🍕"}, nil)
	mh := uitest.New(t, mp)
	// Jump to the breakfast porridge (RU;EN composition, own price).
	for i, r := range mp.rows {
		if !r.header() && mp.menu.Sections[r.sec].Items[r.item].ItemName == "Herculean milk porridge with butter" {
			mp.list.Select(i)
			break
		}
	}
	v := mh.View(100, 30)
	if !strings.Contains(v, "54 ₽") || !strings.Contains(v, "Milk, oat flakes") || !strings.Contains(v, "Каша молочная") {
		t.Fatalf("detail:\n%s", v)
	}
	// Empty sections are skipped; an empty menu says so.
	mh.Send(ui.Result[api.Menu]{ID: mp.id, Seq: mp.load.Begin(), Data: api.Menu{CurrentDay: "tuesday", Sections: []api.MenuSection{{SectionName: "Empty"}}}})
	if v := mh.View(100, 30); !strings.Contains(v, "No menu for Tuesday") || strings.Contains(v, "Empty") {
		t.Fatalf("empty menu:\n%s", v)
	}
	// Items without a price, a set price, weird chips.
	mh.Send(ui.Result[api.Menu]{ID: mp.id, Seq: mp.load.Begin(), Data: api.Menu{CurrentDay: "tuesday", Sections: []api.MenuSection{
		{SectionName: "Set", Price: api.NewNum(199.5), Items: []api.MenuItem{{ItemName: "Soup", Chips: []string{"vegetarian", "\x1b[2Jodd"}}}},
		{SectionName: "", Items: []api.MenuItem{{ItemNameOpt: "Только по-русски"}}},
	}}})
	v = mh.View(100, 30)
	if !strings.Contains(v, "Set — 199.50 ₽") || !strings.Contains(v, "veg") || !strings.Contains(v, "199.50 ₽ for the Set") || !strings.Contains(v, "Только по-русски") {
		t.Fatalf("hand-made menu:\n%s", v)
	}
	checkSizes(t, mp)
	// 404 → not available.
	mh.Send(ui.Result[api.Menu]{ID: mp.id, Seq: mp.load.Begin(), Err: &api.APIError{Status: 404}})
	mp.menu.Sections = nil
	mp.rebuild()
	if v := mh.View(100, 30); !strings.Contains(v, "Menu not available") {
		t.Fatalf("404:\n%s", v)
	}
	checkSizes(t, mp)
}

func TestNoMenuShowsActions(t *testing.T) {
	p, h := newFood(t)
	for i, r := range p.rows {
		if r.header() {
			continue
		}
		if c, _ := p.cafeAt(r); c.Name == "Кофейня Jeffrey's на Мясницкой 20" {
			p.list.Select(i)
		}
	}
	h.Key("enter")
	m, ok := h.LastMenu()
	if !ok || len(m.Actions) != 3 {
		t.Fatalf("expected map/photo/copy actions, got %+v", m)
	}
	if !h.RunAction("map") {
		t.Fatal("map action")
	}
	if st := h.Statuses(); !strings.Contains(st[len(st)-1], "yandex.ru/maps") {
		t.Fatalf("map: %v", st)
	}
	h.Key("p")
	if st := h.Statuses(); !strings.Contains(st[len(st)-1], "Opened https://www.hse.ru/pubs/share/direct/") {
		t.Fatalf("photo: %v", st)
	}
	h.Key("y")
	if st := h.Statuses(); !strings.Contains(st[len(st)-1], "address") {
		t.Fatalf("copy: %v", st)
	}
	// A cafe with nothing at all.
	h.Send(ui.Result[[]api.CafeGroup]{ID: p.id, Seq: p.load.Begin(), Data: []api.CafeGroup{{Cafes: []api.Cafe{{Name: "Ghost"}}}}})
	h.Key("enter")
	if st := h.Statuses(); !strings.Contains(st[len(st)-1], "No menu for this cafe") {
		t.Fatalf("expected warning: %v", st)
	}
	if v := h.View(100, 30); !strings.Contains(v, "hours n/a") {
		t.Fatalf("unknown hours:\n%s", v)
	}
	checkSizes(t, p)
}

func TestEdgeCafes(t *testing.T) {
	p, h := newFood(t)
	now := uitest.Now
	hours := []api.OpeningHours{
		{DayOfWeek: "monday", IsOpen: true, StartTime: "09:00", EndTime: "14:00"},
		{DayOfWeek: "tuesday", IsOpen: true, StartTime: "09:00", EndTime: "14:00"},
	}
	perm := &api.LatLng{Lat: api.NewNum(58), Lng: api.NewNum(56.2)}
	groups := []api.CafeGroup{
		{CampusName: "P Lebedeva, 27", Coordinates: perm, Cafes: []api.Cafe{{ID: "p1", Name: "Perm cafe", OpeningHours: hours}}},
		{CampusName: "Pokrovsky", Cafes: []api.Cafe{
			{ID: "m1", Name: "Moscow \"cafe\" 🍕 " + strings.Repeat("очень длинное название ", 5), OpeningHours: hours,
				ClosedDates: []string{"", "2025-01-01", "2026-10-14", "2026-10-15", "2026-10-16", "2026-10-20", "2026-12-01", "bad"},
				Banner:      "Закрыто\n\nна ремонт \x1b[31m!", Navigation: &api.CafeNavigation{Room: "104", Floor: "1"}, CurrentLoad: "normal"},
		}},
	}
	h.Send(ui.Result[[]api.CafeGroup]{ID: p.id, Seq: p.load.Begin(), Data: groups})
	// The name-only group (no coordinates) has no city: it sorts after Perm.
	if p.places[0].city != cityPerm || p.places[1].city != cityOther {
		t.Fatalf("cities: %v %v", p.places[0].city, p.places[1].city)
	}
	pc, pg, _ := p.selected()
	if p.status(pc, pg).Open {
		t.Fatal("Perm cafe is closed at 14:30 local")
	}
	h.Key("j")
	mc, mg, _ := p.selected()
	if !p.status(mc, mg).Open {
		t.Fatal("Moscow cafe is open at 12:30")
	}
	v := h.View(100, 30)
	for _, want := range []string{"14 Oct – 16 Oct, 20 Oct", "floor 1, room 104", "! Закрыто", "normal", "!"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	if strings.Contains(p.View(100, 30), "\x1b[31m!") {
		t.Error("banner escape sequence not stripped")
	}
	h.Key("m")
	if st := h.Statuses(); !strings.Contains(st[len(st)-1], "No location") {
		t.Fatalf("nil coordinates: %v", st)
	}
	_ = now
	checkSizes(t, p)
}

func TestUpcomingClosed(t *testing.T) {
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, api.Moscow)
	got := upcomingClosed([]string{"2026-09-14", "", "2026-08-27", "2026-08-28", "2026-08-29", "2026-08-31", "2026-09-01", "2025-07-01", "2026-08-27"}, now)
	if got != "27 Aug – 29 Aug, 31 Aug – 1 Sep, 14 Sep" {
		t.Fatalf("got %q", got)
	}
	if upcomingClosed([]string{"2026-10-01"}, now) != "" {
		t.Fatal("dates beyond 30 days should be hidden")
	}
}

func TestCityOf(t *testing.T) {
	cases := map[string]city{
		"St. Petersburg, ul.Sedova": citySPb, "СПб, Седова": citySPb, "N.N., Rodionova": cityNN,
		"Пермь, Лебедева": cityPerm, "Perm, Lebedeva": cityPerm, "Myasnitskaya": cityOther,
	}
	for name, want := range cases {
		if got := cityOf(api.CafeGroup{CampusName: name}); got != want {
			t.Errorf("%q: got %v want %v", name, got, want)
		}
	}
	for lng, want := range map[float64]city{30.3: citySPb, 37.6: cityMoscow, 44: cityNN, 56.2: cityPerm} {
		g := api.CafeGroup{Coordinates: &api.LatLng{Lat: api.NewNum(55), Lng: api.NewNum(lng)}}
		if got := cityOf(g); got != want {
			t.Errorf("lng %v: got %v", lng, got)
		}
	}
}

func TestReloadRefetches(t *testing.T) {
	p, h := newFood(t)
	seq := p.load.Seq
	h.Send(ui.ReloadMsg{})
	h.Key("r")
	if p.load.Seq != seq+2 || len(p.places) == 0 {
		t.Fatal("reload / r should refetch")
	}
}
