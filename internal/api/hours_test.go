package api

import (
	"testing"
	"time"
)

// at returns a Moscow time in the week of Mon 2026-10-05.
func at(day, hour, min int) time.Time {
	return time.Date(2026, 10, day, hour, min, 0, 0, Moscow)
}

// weekdays is Mon–Sat open start–end, Sunday closed (like the canteens).
func weekdays(start, end string) []OpeningHours {
	var hs []OpeningHours
	for _, d := range []string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday"} {
		hs = append(hs, OpeningHours{DayOfWeek: d, IsOpen: true, StartTime: start, EndTime: end})
	}
	return append(hs, OpeningHours{DayOfWeek: "sunday", IsOpen: false})
}

func TestStatus(t *testing.T) {
	std := weekdays("09:00", "21:00")
	overnight := []OpeningHours{
		{DayOfWeek: "friday", IsOpen: true, StartTime: "22:00", EndTime: "02:00"},
		{DayOfWeek: "saturday", IsOpen: true, StartTime: "22:00", EndTime: "02:00"},
	}
	cases := []struct {
		name   string
		hours  []OpeningHours
		closed []string
		now    time.Time
		open   bool
		label  string
		today  string
	}{
		{"open", std, nil, at(5, 12, 0), true, "open · until 21:00", "09:00–21:00"},
		{"opening minute", std, nil, at(5, 9, 0), true, "open · until 21:00", "09:00–21:00"},
		{"before opening", std, nil, at(5, 8, 59), false, "closed · opens 09:00", "09:00–21:00"},
		{"closing minute", std, nil, at(5, 21, 0), false, "closed · opens tomorrow 09:00", "09:00–21:00"},
		{"after closing", std, nil, at(5, 23, 30), false, "closed · opens tomorrow 09:00", "09:00–21:00"},
		{"closed day", std, nil, at(11, 12, 0), false, "closed · opens tomorrow 09:00", "closed"},
		{"before a closed day", std, nil, at(10, 22, 0), false, "closed · opens Mon 09:00", "09:00–21:00"},

		{"overnight evening", overnight, nil, at(9, 23, 0), true, "open · until 02:00", "22:00–02:00"},
		{"overnight after midnight via yesterday", overnight, nil, at(10, 1, 30), true, "open · until 02:00", "22:00–02:00"},
		{"overnight after midnight, today unlisted", overnight, nil, at(11, 1, 59), true, "open · until 02:00", "closed"},
		{"overnight ended", overnight, nil, at(11, 2, 0), false, "closed · opens Fri 22:00", "closed"},
		{"overnight before start", overnight, nil, at(9, 21, 0), false, "closed · opens 22:00", "22:00–02:00"},
		{"overnight, yesterday was a closed date", overnight, []string{"2026-10-09"}, at(10, 1, 0), false, "closed · opens 22:00", "22:00–02:00"},

		{"until 24:00", weekdays("08:00", "24:00"), nil, at(5, 23, 59), true, "open · until 24:00", "08:00–24:00"},
		{"24:00 not open after midnight", weekdays("08:00", "24:00"), nil, at(6, 0, 30), false, "closed · opens 08:00", "08:00–24:00"},
		{"round the clock", weekdays("00:00", "24:00"), nil, at(5, 3, 0), true, "open · until 24:00", "00:00–24:00"},
		{"compact clock format", weekdays("0900", "2100"), nil, at(5, 20, 0), true, "open · until 2100", "0900–2100"},

		{"missing times with is_open", []OpeningHours{{DayOfWeek: "monday", IsOpen: true}}, nil, at(5, 3, 0), true, "open today", "open"},
		{"missing times next day", []OpeningHours{{DayOfWeek: "tuesday", IsOpen: true}}, nil, at(5, 12, 0), false, "closed · opens tomorrow", "closed"},

		{"closed date today", std, []string{"2026-10-05"}, at(5, 12, 0), false, "closed today · opens tomorrow 09:00", "closed"},
		{"closed dates skipped", std, []string{"", " 2026-10-05 ", "2026-10-06", "2026-10-07"}, at(5, 12, 0), false, "closed today · opens Thu 09:00", "closed"},
		{"closed date tomorrow", std, []string{"2026-10-06"}, at(5, 22, 0), false, "closed · opens Wed 09:00", "09:00–21:00"},
		{"closed for weeks", std, closedRange(at(5, 0, 0), 20), at(5, 12, 0), false, "closed", "closed"},
		{"never open", []OpeningHours{{DayOfWeek: "monday"}, {DayOfWeek: "tuesday"}}, nil, at(5, 12, 0), false, "closed", "closed"},

		{"russian day names", []OpeningHours{{DayOfWeek: "Понедельник", IsOpen: true, StartTime: "10:00", EndTime: "18:00"}}, nil, at(5, 11, 0), true, "open · until 18:00", "10:00–18:00"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := Status(c.hours, c.closed, c.now)
			if !st.Known || st.Open != c.open || st.Label != c.label || st.Today != c.today {
				t.Errorf("Status = %+v, want open=%v label=%q today=%q", st, c.open, c.label, c.today)
			}
		})
	}

	if st := Status(nil, []string{"2026-10-05"}, at(5, 12, 0)); st.Known || st.Open || st.Label != "" || st.Today != "" {
		t.Errorf("no schedule: %+v", st)
	}
}

func closedRange(from time.Time, days int) []string {
	var out []string
	for i := 0; i < days; i++ {
		out = append(out, from.AddDate(0, 0, i).Format("2006-01-02"))
	}
	return out
}

func TestCafeZoneAndStatus(t *testing.T) {
	perm := Cafe{
		OpeningHours: weekdays("09:00", "17:00"),
		Coordinates:  &LatLng{Lat: NewNum(58.01), Lng: NewNum(56.28)},
	}
	if z := perm.Zone().String(); z != "Asia/Yekaterinburg" {
		t.Errorf("Perm zone = %s", z)
	}
	// 12:30 in Moscow is 14:30 in Perm.
	if st := perm.Status(at(5, 12, 30)); !st.Open || st.Label != "open · until 17:00" {
		t.Errorf("Perm at 14:30 local: %+v", st)
	}
	// 15:30 in Moscow is 17:30 in Perm: closed, although Moscow time is in hours.
	if st := perm.Status(at(5, 15, 30)); st.Open {
		t.Errorf("Perm at 17:30 local: %+v", st)
	}
	// Status converts any input zone.
	if st := perm.Status(at(5, 12, 30).UTC()); !st.Open {
		t.Errorf("UTC input: %+v", st)
	}

	moscow := Cafe{OpeningHours: weekdays("09:00", "17:00"), Coordinates: &LatLng{Lat: NewNum(55.8), Lng: NewNum(37.4)}}
	if moscow.Zone() != Moscow || !moscow.Status(at(5, 16, 0)).Open {
		t.Error("Moscow cafe")
	}
	if (Cafe{}).Zone() != Moscow || (Cafe{Coordinates: &LatLng{Lng: NewNum(56)}}).Zone() != Moscow {
		t.Error("missing/invalid coordinates should default to Moscow")
	}
	if (Cafe{}).Status(at(5, 12, 0)).Known {
		t.Error("cafe without hours should be unknown")
	}

	if z := ZoneForCampus("campus_perm").String(); z != "Asia/Yekaterinburg" {
		t.Errorf("ZoneForCampus(perm) = %s", z)
	}
	if ZoneForCampus("CAMPUS_SPB") != Moscow || ZoneForLng(30.3) != Moscow {
		t.Error("SPb should be Moscow time")
	}
	if LoadZone("") != Moscow || LoadZone("Nowhere/Nothing") != Moscow || LoadZone("Asia/Yekaterinburg").String() != "Asia/Yekaterinburg" {
		t.Error("LoadZone")
	}
}

func TestParseWeekday(t *testing.T) {
	ok := map[string]time.Weekday{
		"monday": time.Monday, "Monday": time.Monday, " SUNDAY ": time.Sunday,
		"mon": time.Monday, "Tue": time.Tuesday, "wed": time.Wednesday, "thu": time.Thursday,
		"thurs": time.Thursday, "fri": time.Friday, "sat": time.Saturday, "sun": time.Sunday,
		"понедельник": time.Monday, "Вторник": time.Tuesday, "среда": time.Wednesday,
		"четверг": time.Thursday, "пятница": time.Friday, "суббота": time.Saturday,
		"воскресенье": time.Sunday, "пн": time.Monday, "Вт": time.Tuesday, "вс": time.Sunday,
	}
	for in, want := range ok {
		if got, found := ParseWeekday(in); !found || got != want {
			t.Errorf("ParseWeekday(%q) = %v, %v; want %v", in, got, found, want)
		}
	}
	for _, in := range []string{"", "mo", "t", "s", "xyz", "mondays", "funday", "пон"} {
		if _, found := ParseWeekday(in); found {
			t.Errorf("ParseWeekday(%q) matched", in)
		}
	}
	for d := time.Sunday; d <= time.Saturday; d++ {
		if got, _ := ParseWeekday(WeekdayKey(d)); got != d {
			t.Errorf("WeekdayKey(%v) does not roundtrip", d)
		}
	}
	if _, ok := HoursFor(weekdays("09:00", "17:00"), time.Sunday); !ok {
		t.Error("HoursFor should return the closed Sunday entry")
	}
	if _, ok := HoursFor(nil, time.Monday); ok {
		t.Error("HoursFor(nil)")
	}
}

func TestParseClock(t *testing.T) {
	ok := map[string]int{"09:00": 540, "9:00": 540, "0900": 540, "24:00": 1440, "00:00": 0, " 21:30 ": 1290}
	for in, want := range ok {
		if got, found := parseClock(in); !found || got != want {
			t.Errorf("parseClock(%q) = %d, %v", in, got, found)
		}
	}
	for _, in := range []string{"", "25:00", "12:60", "ab:cd", "900", "-1:00", "9"} {
		if _, found := parseClock(in); found {
			t.Errorf("parseClock(%q) accepted", in)
		}
	}
}

func TestOpeningHoursRange(t *testing.T) {
	cases := map[string]OpeningHours{
		"closed":      {IsOpen: false, StartTime: "09:00", EndTime: "21:00"},
		"open":        {IsOpen: true},
		"09:00–21:00": {IsOpen: true, StartTime: " 09:00", EndTime: "21:00 "},
	}
	for want, h := range cases {
		if got := h.Range(); got != want {
			t.Errorf("%+v.Range() = %q, want %q", h, got, want)
		}
	}
}

func TestSanitaryDayText(t *testing.T) {
	cases := map[string]string{
		"SANITARY_DAY_FIRST_FRIDAY":      "closed for cleaning on the first Friday of each month",
		"sanitary_day_last_monday":       "closed for cleaning on the last Monday of each month",
		" SANITARY_DAY_THIRD_WEDNESDAY ": "closed for cleaning on the third Wednesday of each month",
		"":                               "",
		"SANITARY_DAY_NONE":              "none",
		"SANITARY_DAY_EVERY_DAY":         "every day",
		"SOMETHING_ELSE":                 "something else",
	}
	for in, want := range cases {
		if got := SanitaryDayText(in); got != want {
			t.Errorf("SanitaryDayText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStatusPartialHours(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 10, 13, h, m, 0, 0, Moscow) } // Tuesday
	onlyStart := []OpeningHours{{DayOfWeek: "tuesday", IsOpen: true, StartTime: "10:00"}}
	if st := Status(onlyStart, nil, at(9, 0)); st.Open || st.Label != "closed · opens 10:00" {
		t.Errorf("only start, before: %+v", st)
	}
	if st := Status(onlyStart, nil, at(12, 0)); !st.Open || st.Label != "open · since 10:00" {
		t.Errorf("only start, after: %+v", st)
	}
	onlyEnd := []OpeningHours{{DayOfWeek: "tuesday", IsOpen: true, EndTime: "18:00"}}
	if st := Status(onlyEnd, nil, at(12, 0)); !st.Open || st.Label != "open · until 18:00" {
		t.Errorf("only end, before: %+v", st)
	}
	if st := Status(onlyEnd, nil, at(19, 0)); st.Open {
		t.Errorf("only end, after: %+v", st)
	}
}

func TestStatusStructuredAndQuirks(t *testing.T) {
	at := func(d, h, m int) time.Time { return time.Date(2026, 10, d, h, m, 0, 0, Moscow) } // 5 Oct = Monday
	split := []OpeningHours{
		{DayOfWeek: "monday", IsOpen: true, StartTime: "09:00", EndTime: "12:00"},
		{DayOfWeek: "monday", IsOpen: true, StartTime: "13:00", EndTime: "18:00"},
	}
	if st := Status(split, nil, at(5, 14, 0)); !st.Open || st.Kind != OpenUntil || st.Time != "18:00" || st.Today != "09:00–12:00, 13:00–18:00" {
		t.Errorf("split shift afternoon: %+v", st)
	}
	if st := Status(split, nil, at(5, 12, 30)); st.Open || st.Kind != ClosedOpens || st.OpensAt != "13:00" || st.OpensInDays != 0 {
		t.Errorf("lunch break: %+v", st)
	}
	dotted := []OpeningHours{{DayOfWeek: "monday", IsOpen: true, StartTime: "09.00", EndTime: "21.00"}}
	if st := Status(dotted, nil, at(5, 23, 0)); st.Open {
		t.Errorf("09.00–21.00 must be closed at 23:00: %+v", st)
	}
	std := []OpeningHours{{DayOfWeek: "monday", IsOpen: true, StartTime: "09:00", EndTime: "21:00"}}
	st := Status(std, nil, at(5, 22, 0))
	if st.Kind != ClosedOpens || st.OpensInDays != 7 || st.Label != "closed · opens Mon 12 Oct 09:00" {
		t.Errorf("same weekday next week must name the date: %+v", st)
	}
	if st := Status(std, []string{"2026-10-05T00:00:00Z"}, at(5, 12, 0)); st.Open || !st.ClosedToday {
		t.Errorf("ISO datetime closed date: %+v", st)
	}
	if st := Status([]OpeningHours{{DayOfWeek: "monday"}}, nil, at(5, 12, 0)); st.Kind != Closed {
		t.Errorf("never open: %+v", st)
	}
}
