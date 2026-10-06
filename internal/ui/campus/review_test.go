package campus

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"hse-app-z/internal/api"
	"hse-app-z/internal/ui"
	"hse-app-z/internal/ui/uitest"
)

// A café without its own coordinates in a Perm building is judged by
// Perm time: at 12:30 Moscow (14:30 Perm) a 09:00–14:00 café is closed.
func TestCafeWithoutCoordinatesUsesBuildingZone(t *testing.T) {
	hours := []api.OpeningHours{{DayOfWeek: "tuesday", IsOpen: true, StartTime: "09:00", EndTime: "14:00"}}
	p := New(uitest.Ctx(t)).(*Page)
	for _, b := range []api.Building{
		{ID: "x", Name: "Perm", Campus: "CAMPUS_PERM", Cafes: []api.Cafe{{Name: "No coords", OpeningHours: hours}}},
		// No campus code either: the building's location says Perm.
		{ID: "y", Name: "Perm?", Location: &api.GeoPoint{Coordinates: []float64{56.28, 58.01}},
			Cafes: []api.Cafe{{Name: "No coords", OpeningHours: hours, Coordinates: &api.LatLng{Lat: api.NewNum(0), Lng: api.NewNum(0)}}}},
	} {
		d := ansi.Strip(p.detail(b, 80))
		if strings.Contains(d, "open · until 14:00") || !strings.Contains(d, "closed · opens") {
			t.Errorf("%s: café judged by Moscow time:\n%s", b.Name, d)
		}
	}
	// A Moscow café is still open then.
	d := ansi.Strip(p.detail(api.Building{ID: "z", Name: "Msk", Campus: "CAMPUS_MOS", Cafes: []api.Cafe{{Name: "C", OpeningHours: hours}}}, 80))
	if !strings.Contains(d, "open · until 14:00") {
		t.Errorf("Moscow café:\n%s", d)
	}
}

// On its monthly cleaning day the library is closed whatever the weekly
// hours say.
func TestSanitaryDayClosesLibrary(t *testing.T) {
	ctx := uitest.Ctx(t)
	hours := []api.OpeningHours{
		{DayOfWeek: "monday", IsOpen: true, StartTime: "10:00", EndTime: "21:00"},
		{DayOfWeek: "friday", IsOpen: true, StartTime: "10:00", EndTime: "21:00"},
	}
	b := api.Building{ID: "x", Name: "Lib", Campus: "CAMPUS_MOS", LibrariesV3: []api.Library{{Name: "L", Offices: []api.LibraryOffice{{Name: "Hall",
		Rules: []api.LibraryRule{{Schedule: &api.LibrarySchedule{OpeningHours: hours, SanitaryDay: "SANITARY_DAY_FIRST_FRIDAY"}}}}}}}}
	p := New(ctx).(*Page)
	// Friday 2 Oct 2026 is the first Friday; Friday 9 Oct isn't.
	ctx.Now = func() time.Time { return time.Date(2026, 10, 2, 12, 0, 0, 0, api.Moscow) }
	if d := ansi.Strip(p.detail(b, 80)); !strings.Contains(d, "closed today · opens Mon 10:00") {
		t.Errorf("sanitary day shown as open:\n%s", d)
	}
	ctx.Now = func() time.Time { return time.Date(2026, 10, 9, 12, 0, 0, 0, api.Moscow) }
	if d := ansi.Strip(p.detail(b, 80)); !strings.Contains(d, "open · until 21:00") {
		t.Errorf("ordinary Friday:\n%s", d)
	}
	// Thursday 1 Oct: "opens tomorrow" would be wrong.
	ctx.Now = func() time.Time { return time.Date(2026, 10, 1, 22, 0, 0, 0, api.Moscow) }
	if d := ansi.Strip(p.detail(b, 80)); !strings.Contains(d, "opens Mon 10:00") {
		t.Errorf("next opening must skip the sanitary day:\n%s", d)
	}
}

func TestSanitaryDates(t *testing.T) {
	now := time.Date(2026, 12, 15, 10, 0, 0, 0, api.Moscow)
	for code, want := range map[string]string{
		"SANITARY_DAY_FIRST_FRIDAY":  "2026-12-04 2027-01-01",
		"SANITARY_DAY_LAST_MONDAY":   "2026-12-28 2027-01-25",
		"sanitary_day_third_tuesday": "2026-12-15 2027-01-19",
		"SANITARY_DAY_FOURTH_SUNDAY": "2026-12-27 2027-01-24",
		"SANITARY_DAY_WEIRD":         "",
		"SANITARY_DAY_FIRST_FUNDAY":  "",
		"":                           "",
	} {
		if got := strings.Join(SanitaryDates(code, now), " "); got != want {
			t.Errorf("%s: %q, want %q", code, got, want)
		}
	}
}

func TestFormatPhoneEdgeCases(t *testing.T) {
	for in, want := range map[string]string{
		"+84957729590":           "+84957729590", // Vietnam, not Moscow
		"84957729590":            "+7 495 772-95-90",
		"8 (495) 772-95-90":      "+7 495 772-95-90",
		",15241":                 "ext. 15241",
		"+7 495 772 95 90 , 123": "+7 495 772-95-90 ext. 123",
		",":                      "",
	} {
		if got := FormatPhone(in); got != want {
			t.Errorf("FormatPhone(%q) = %q, want %q", in, got, want)
		}
	}
}

// A campus filter whose campus disappears on refresh falls back to All
// instead of showing an empty list with no way to tell why.
func TestFilterSurvivesRefreshWithoutItsCampus(t *testing.T) {
	p, h := newPage(t)
	for p.filterName() != "Perm" {
		h.Key("c")
	}
	h.Send(ui.Result[[]api.CampusGroup]{ID: p.id, Seq: p.load.Begin(), Data: []api.CampusGroup{
		{Campus: "CAMPUS_MOS", Buildings: []api.Building{{ID: "1", Name: "Only Moscow", Campus: "CAMPUS_MOS"}}},
	}})
	if p.filterName() != "All" || !p.list.HasSelection() {
		t.Fatalf("filter %q, selection %v", p.filterName(), p.list.HasSelection())
	}
	if v := h.View(60, 10); !strings.Contains(v, "Only Moscow") {
		t.Fatalf("view:\n%s", v)
	}
}
