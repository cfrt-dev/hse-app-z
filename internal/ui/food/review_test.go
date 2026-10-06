package food

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"hse-app-z/internal/api"
	"hse-app-z/internal/ui"
	"hse-app-z/internal/ui/uitest"
)

func key(k string) tea.KeyMsg { return uitest.KeyMsgs(k)[0] }

// Switching days must not show the previous day's dishes under the new
// day's title while the request is on its way (slow network, 429 retry).
func TestMenuDaySwitchHidesPreviousDay(t *testing.T) {
	mp := newMenuPage(uitest.Ctx(t), api.Cafe{ID: "x", Name: "C"}, nil)
	uitest.New(t, mp)
	if v := ansi.Strip(mp.View(100, 12)); !strings.Contains(v, "Tuesday · today") || !strings.Contains(v, "Baked beetroot") {
		t.Fatalf("today's menu:\n%s", v)
	}
	mp.Update(key("l")) // request in flight, result not delivered yet
	v := ansi.Strip(mp.View(100, 12))
	if !strings.Contains(v, "Wednesday") || strings.Contains(v, "Baked beetroot") || !strings.Contains(v, "Loading") {
		t.Fatalf("Tuesday's dishes shown as Wednesday's:\n%s", v)
	}
	mp.Update(ui.Result[api.Menu]{ID: mp.id, Seq: mp.load.Seq, Data: api.Menu{CurrentDay: "wednesday", AvailableDays: mp.menu.AvailableDays,
		Sections: []api.MenuSection{{SectionName: "Wed", Items: []api.MenuItem{{ItemName: "Wednesday soup"}}}}}})
	if v := ansi.Strip(mp.View(100, 12)); !strings.Contains(v, "Wednesday soup") {
		t.Fatalf("Wednesday's menu:\n%s", v)
	}
	// A refresh of the same day keeps the rows (no flicker).
	mp.Update(key("r"))
	if v := ansi.Strip(mp.View(100, 12)); !strings.Contains(v, "Wednesday soup") {
		t.Fatalf("refresh should keep the rows:\n%s", v)
	}
	mp.Update(ui.Result[api.Menu]{ID: mp.id, Seq: mp.load.Seq, Err: &api.RateLimitError{}})
	if v := ansi.Strip(mp.View(100, 12)); !strings.Contains(v, "Wednesday soup") || !strings.Contains(v, "Rate limited") {
		t.Fatalf("failed refresh keeps the data with a note:\n%s", v)
	}
	// Back to today fails: Wednesday's dishes must not stay under it.
	mp.Update(key("t"))
	mp.Update(ui.Result[api.Menu]{ID: mp.id, Seq: mp.load.Seq, Err: &api.RateLimitError{}})
	v = ansi.Strip(mp.View(100, 12))
	if strings.Contains(v, "Wednesday soup") || !strings.Contains(v, "Tuesday · today") || !strings.Contains(v, "Rate limited") {
		t.Fatalf("after t failed:\n%s", v)
	}
	// Unordered / Russian / mixed-case available days still step in week order.
	mp.menu.AvailableDays = []string{"FRIDAY", "понедельник", "Wed", "bogus", ""}
	mp.Update(key("l"))
	if mp.day != "wednesday" {
		t.Fatalf("l from Tuesday → %q", mp.day)
	}
	mp.Update(ui.Result[api.Menu]{ID: mp.id, Seq: mp.load.Seq, Err: &api.APIError{Status: 404}})
	if v := ansi.Strip(mp.View(100, 12)); !strings.Contains(v, "Wednesday") || !strings.Contains(v, "Menu not available") {
		t.Fatalf("404:\n%s", v)
	}
	mp.Update(key("l"))
	if mp.day != "friday" {
		t.Fatalf("l from Wednesday → %q", mp.day)
	}
	mp.Update(key("h"))
	mp.Update(key("h"))
	if mp.day != "monday" {
		t.Fatalf("h,h from Friday → %q", mp.day)
	}
}

// A Perm building without coordinates (only its "P …" name tells the
// city) is evaluated in Perm time, not Moscow time.
func TestPermCafeWithoutCoordinatesUsesPermTime(t *testing.T) {
	p, h := newFood(t)
	hours := []api.OpeningHours{{DayOfWeek: "tuesday", IsOpen: true, StartTime: "09:00", EndTime: "14:00"}}
	h.Send(ui.Result[[]api.CafeGroup]{ID: p.id, Seq: p.load.Begin(), Data: []api.CafeGroup{
		{CampusName: "P Lebedeva, 27", Cafes: []api.Cafe{{ID: "p1", Name: "Perm cafe", OpeningHours: hours}}},
	}})
	c, g, ok := p.selected()
	if !ok || p.places[0].city != cityPerm {
		t.Fatal("expected the Perm cafe")
	}
	// 12:30 Moscow = 14:30 Perm: closed since 14:00.
	if st := p.status(c, g); st.Open {
		t.Fatalf("Perm cafe judged by Moscow time: %+v", st)
	}
	if v := h.View(100, 30); !strings.Contains(v, "14:30 UTC+5") {
		t.Fatalf("local time not shown:\n%s", v)
	}
}

// 0,0 placeholder coordinates are not a location: no St. Petersburg
// filing by longitude, no map at null island.
func TestPlaceholderCoordinates(t *testing.T) {
	p, h := newFood(t)
	zero := &api.LatLng{Lat: api.NewNum(0), Lng: api.NewNum(0)}
	h.Send(ui.Result[[]api.CafeGroup]{ID: p.id, Seq: p.load.Begin(), Data: []api.CafeGroup{
		{CampusName: "Pokrovsky bulvar, 11, Moscow", Coordinates: zero, Cafes: []api.Cafe{{ID: "m", Name: "Zero", Coordinates: zero}}},
	}})
	if p.places[0].city != cityMoscow {
		t.Fatalf("city from 0,0: %v", p.places[0].city)
	}
	h.Key("m")
	if st := h.Statuses(); len(st) == 0 || !strings.Contains(st[len(st)-1], "No location") {
		t.Fatalf("map at 0,0: %v", st)
	}
}

// Photo links go through ui.OpenURL; unsafe ones are skipped in favour of
// a usable one, and not advertised when none is usable.
func TestUnsafePhotoLinks(t *testing.T) {
	c := api.Cafe{Photos: []string{"javascript:alert(1)", " file:///etc/passwd", "mailto:x@y", "https://hse.ru/a.jpg"}, Photo: "https://hse.ru/logo.jpg"}
	if photo(c) != "https://hse.ru/a.jpg" || photoCount(c) != 1 {
		t.Fatalf("photo %q count %d", photo(c), photoCount(c))
	}
	c = api.Cafe{Photos: []string{"file:///etc/passwd"}, Photo: "https://hse.ru/logo.jpg"}
	if photo(c) != "https://hse.ru/logo.jpg" {
		t.Fatalf("fallback photo %q", photo(c))
	}
	p, h := newFood(t)
	h.Send(ui.Result[[]api.CafeGroup]{ID: p.id, Seq: p.load.Begin(), Data: []api.CafeGroup{
		{CampusName: "X", Cafes: []api.Cafe{{ID: "a", Name: "Evil", Photos: []string{"file:///etc/passwd"}, Photo: "javascript:x", Address: "Somewhere"}}},
	}})
	if v := h.View(100, 30); strings.Contains(v, "photo") {
		t.Fatalf("unusable photo advertised:\n%s", v)
	}
	h.Key(".")
	m, _ := h.LastMenu()
	for _, a := range m.Actions {
		if strings.Contains(a.Label, "photo") {
			t.Fatal("unusable photo offered in the menu")
		}
	}
	h.Key("p")
	if st := h.Statuses(); !strings.Contains(st[len(st)-1], "No photos") {
		t.Fatalf("p: %v", st)
	}
}

// Overnight hours, garbage closed dates and missing days in a cafe's own
// zone while the machine runs in another one.
func TestHoursAcrossZones(t *testing.T) {
	ctx := uitest.Ctx(t)
	// A laptop in Berlin: 01:30 local on Wednesday = 03:30 Moscow = 05:30 Perm.
	berlin, _ := time.LoadLocation("Europe/Berlin")
	ctx.Now = func() time.Time { return time.Date(2026, 10, 14, 1, 30, 0, 0, berlin) }
	p := New(ctx).(*Page)
	h := uitest.New(t, p)
	hours := []api.OpeningHours{
		{DayOfWeek: "tuesday", IsOpen: true, StartTime: "20:00", EndTime: "04:00"},
		{DayOfWeek: "wednesday", IsOpen: true, StartTime: "08:00", EndTime: "20:00"},
	}
	perm := &api.LatLng{Lat: api.NewNum(58), Lng: api.NewNum(56.2)}
	h.Send(ui.Result[[]api.CafeGroup]{ID: p.id, Seq: p.load.Begin(), Data: []api.CafeGroup{
		{CampusName: "Moscow", Cafes: []api.Cafe{{ID: "m", Name: "Night owl", OpeningHours: hours, ClosedDates: []string{"", "garbage", "2026-13-45"}}}},
		{CampusName: "P Perm", Coordinates: perm, Cafes: []api.Cafe{{ID: "p", Name: "Perm owl", OpeningHours: hours}}},
	}})
	for _, r := range p.rows {
		if r.header() {
			continue
		}
		c, g := p.cafeAt(r)
		st := p.status(c, g)
		switch c.Name {
		case "Night owl": // 03:30 Moscow: still open from Tuesday night
			if !st.Open {
				t.Errorf("Moscow overnight: %+v", st)
			}
		case "Perm owl": // 05:30 Perm: closed, opens 08:00
			if st.Open || !strings.Contains(st.Label, "08:00") {
				t.Errorf("Perm: %+v", st)
			}
		}
	}
	checkSizes(t, p)
}

// "open now" stays true while the page is shown: a café closing at 14:00
// drops out on the next redraw instead of lingering until a key press.
func TestOpenNowFilterFollowsTheClock(t *testing.T) {
	ctx := uitest.Ctx(t)
	now := time.Date(2026, 10, 13, 13, 55, 0, 0, api.Moscow)
	ctx.Now = func() time.Time { return now }
	p := New(ctx).(*Page)
	h := uitest.New(t, p)
	hours := []api.OpeningHours{{DayOfWeek: "tuesday", IsOpen: true, StartTime: "09:00", EndTime: "14:00"}}
	h.Send(ui.Result[[]api.CafeGroup]{ID: p.id, Seq: p.load.Begin(), Data: []api.CafeGroup{{CampusName: "Moscow", Cafes: []api.Cafe{
		{ID: "a", Name: "Lunch place", OpeningHours: hours},
		{ID: "b", Name: "All day", OpeningHours: []api.OpeningHours{{DayOfWeek: "tuesday", IsOpen: true}}},
	}}}})
	h.Key("o", "j")
	if c, _, _ := p.selected(); c.Name != "All day" {
		t.Fatalf("selected %q", c.Name)
	}
	if v := h.View(100, 12); !strings.Contains(v, "Lunch place") {
		t.Fatalf("open at 13:55:\n%s", v)
	}
	now = now.Add(10 * time.Minute)
	v := h.View(100, 12)
	if strings.Contains(v, "Lunch place") || !strings.Contains(v, "1/1 open") {
		t.Fatalf("closed café still listed under open now:\n%s", v)
	}
	if c, _, _ := p.selected(); c.Name != "All day" {
		t.Fatalf("selection lost: %q", c.Name)
	}
}
