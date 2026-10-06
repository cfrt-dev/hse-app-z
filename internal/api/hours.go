package api

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

var weekdayKeys = [7]string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"}

// WeekdayKey is the API day name for a weekday ("monday").
func WeekdayKey(d time.Weekday) string { return weekdayKeys[d] }

// ParseWeekday maps "monday"/"Mon"/"понедельник" to a weekday.
func ParseWeekday(s string) (time.Weekday, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return 0, false
	}
	for i, k := range weekdayKeys {
		if s == k || (len(s) >= 3 && strings.HasPrefix(k, s)) {
			return time.Weekday(i), true
		}
	}
	ru := map[string]time.Weekday{"пн": time.Monday, "вт": time.Tuesday, "ср": time.Wednesday, "чт": time.Thursday, "пт": time.Friday, "сб": time.Saturday, "вс": time.Sunday,
		"понедельник": time.Monday, "вторник": time.Tuesday, "среда": time.Wednesday, "четверг": time.Thursday, "пятница": time.Friday, "суббота": time.Saturday, "воскресенье": time.Sunday}
	if d, ok := ru[s]; ok {
		return d, true
	}
	return 0, false
}

// HoursFor returns the entry for a weekday.
func HoursFor(hours []OpeningHours, d time.Weekday) (OpeningHours, bool) {
	for _, h := range hours {
		if wd, ok := ParseWeekday(h.DayOfWeek); ok && wd == d {
			return h, true
		}
	}
	return OpeningHours{}, false
}

// parseClock parses "09:00" / "9:00" / "09.00" / "9-00" / "0900" / "24:00"
// into minutes.
func parseClock(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	s = strings.NewReplacer(".", ":", "-", ":", "h", ":").Replace(s)
	hh, mm, found := strings.Cut(s, ":")
	if !found {
		if len(s) != 4 {
			return 0, false
		}
		hh, mm = s[:2], s[2:]
	}
	h, err1 := strconv.Atoi(hh)
	m, err2 := strconv.Atoi(mm)
	if err1 != nil || err2 != nil || h < 0 || h > 24 || m < 0 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

// Range is the human form of a day's hours: "09:00–21:00", "closed", or "".
func (h OpeningHours) Range() string {
	if !h.IsOpen {
		return "closed"
	}
	if h.StartTime == "" && h.EndTime == "" {
		return "open"
	}
	return strings.TrimSpace(h.StartTime) + "–" + strings.TrimSpace(h.EndTime)
}

// OpenKind classifies an OpenStatus so the UI can phrase it in any language.
type OpenKind int

const (
	KindUnknown OpenKind = iota // no schedule
	OpenUntil                   // open, closes at Time
	OpenSince                   // open since Time, closing time unknown
	OpenAllDay                  // open today, times unknown
	ClosedOpens                 // closed, opens at OpensAt in OpensInDays days
	Closed                      // closed, nothing within two weeks
)

// OpenStatus summarises opening hours at a moment.
type OpenStatus struct {
	Known bool // false when there is no schedule at all
	Open  bool
	// Label is a short English summary: "open · until 21:00",
	// "closed · opens 08:45", "closed · opens Mon 09:00", "closed".
	Label string
	// Today is today's hours ("09:00–21:00", "closed").
	Today string

	Kind        OpenKind
	Time        string       // OpenUntil: closing time; OpenSince: opening time
	OpensAt     string       // ClosedOpens: opening time ("" if unknown)
	OpensInDays int          // ClosedOpens: 0 today, 1 tomorrow, …
	OpensDay    time.Weekday // ClosedOpens: weekday of the opening
	OpensDate   time.Time    // ClosedOpens: date of the opening
	ClosedToday bool         // today is an explicit closed date
}

// hoursAll returns every entry for a weekday (split shifts are listed as
// several entries).
func hoursAll(hours []OpeningHours, d time.Weekday) []OpeningHours {
	var out []OpeningHours
	for _, h := range hours {
		if wd, ok := ParseWeekday(h.DayOfWeek); ok && wd == d {
			out = append(out, h)
		}
	}
	return out
}

func normDate(d string) string {
	d = strings.TrimSpace(d)
	if len(d) >= 10 && d[4] == '-' && d[7] == '-' {
		return d[:10] // also accepts "2026-10-05T00:00:00Z"
	}
	return d
}

// Status evaluates weekly hours plus explicit closed dates (YYYY-MM-DD) at
// now. now must already be in the place's time zone.
func Status(hours []OpeningHours, closedDates []string, now time.Time) OpenStatus {
	if len(hours) == 0 {
		return OpenStatus{}
	}
	closed := map[string]bool{}
	for _, d := range closedDates {
		if d = normDate(d); d != "" {
			closed[d] = true
		}
	}
	isClosedDate := func(t time.Time) bool { return closed[t.Format("2006-01-02")] }
	openEntries := func(t time.Time) []OpeningHours {
		if isClosedDate(t) {
			return nil
		}
		var out []OpeningHours
		for _, h := range hoursAll(hours, t.Weekday()) {
			if h.IsOpen {
				out = append(out, h)
			}
		}
		return out
	}

	st := OpenStatus{Known: true, ClosedToday: isClosedDate(now)}
	today := openEntries(now)
	if len(today) == 0 {
		st.Today = "closed"
	} else {
		var parts []string
		for _, h := range today {
			parts = append(parts, h.Range())
		}
		st.Today = strings.Join(parts, ", ")
	}
	mins := now.Hour()*60 + now.Minute()
	openUntil := func(end string) OpenStatus {
		st.Open, st.Kind, st.Time, st.Label = true, OpenUntil, end, "open · until "+end
		return st
	}

	// Still open from yesterday's overnight shift?
	for _, y := range openEntries(now.AddDate(0, 0, -1)) {
		s, ok1 := parseClock(y.StartTime)
		e, ok2 := parseClock(y.EndTime)
		if ok1 && ok2 && e < s && mins < e {
			return openUntil(y.EndTime)
		}
	}

	nextToday, nextTodayAt := -1, ""
	for _, h := range today {
		s, ok1 := parseClock(h.StartTime)
		e, ok2 := parseClock(h.EndTime)
		switch {
		case !ok1 && !ok2:
			st.Open, st.Kind, st.Label = true, OpenAllDay, "open today"
			return st
		case ok1 && !ok2:
			if mins >= s {
				st.Open, st.Kind, st.Time, st.Label = true, OpenSince, h.StartTime, "open · since "+h.StartTime
				return st
			}
		case !ok1 && ok2:
			if mins < e {
				return openUntil(h.EndTime)
			}
			continue
		default:
			if mins >= s && (e <= s || mins < e) {
				return openUntil(h.EndTime)
			}
		}
		if ok1 && mins < s && (nextToday < 0 || s < nextToday) {
			nextToday, nextTodayAt = s, h.StartTime
		}
	}
	if nextToday >= 0 {
		st.Kind, st.OpensAt, st.OpensDay, st.OpensDate = ClosedOpens, nextTodayAt, now.Weekday(), now
		st.Label = "closed · opens " + nextTodayAt
		return st
	}

	// Find the next opening within two weeks.
	for i := 1; i <= 14; i++ {
		d := now.AddDate(0, 0, i)
		entries := openEntries(d)
		if len(entries) == 0 {
			continue
		}
		start, best := "", -1
		for _, h := range entries {
			if s, ok := parseClock(h.StartTime); ok && (best < 0 || s < best) {
				best, start = s, h.StartTime
			}
		}
		when := d.Format("Mon")
		switch {
		case i == 1:
			when = "tomorrow"
		case i >= 7:
			when = d.Format("Mon 2 Jan")
		}
		if start != "" {
			when += " " + start
		}
		st.Kind, st.OpensAt, st.OpensInDays, st.OpensDay, st.OpensDate = ClosedOpens, start, i, d.Weekday(), d
		if st.ClosedToday {
			st.Label = "closed today · opens " + when
		} else {
			st.Label = "closed · opens " + when
		}
		return st
	}
	st.Kind, st.Label = Closed, "closed"
	return st
}

// Zone is the cafe's local time zone (by longitude; Moscow by default).
func (c Cafe) Zone() *time.Location {
	if c.Coordinates.Valid() {
		return ZoneForLng(c.Coordinates.Lng.V)
	}
	return Moscow
}

// Status reports whether the cafe is open at now.
func (c Cafe) Status(now time.Time) OpenStatus {
	return Status(c.OpeningHours, c.ClosedDates, now.In(c.Zone()))
}

// SanitaryDayText explains library cleaning-day codes such as
// SANITARY_DAY_FIRST_FRIDAY.
func SanitaryDayText(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return ""
	}
	rest := strings.TrimPrefix(code, "SANITARY_DAY_")
	parts := strings.Split(rest, "_")
	if len(parts) == 2 {
		ord := map[string]string{"FIRST": "first", "SECOND": "second", "THIRD": "third", "FOURTH": "fourth", "LAST": "last"}[parts[0]]
		day := strings.ToLower(parts[1])
		if ord != "" && day != "" {
			return fmt.Sprintf("closed for cleaning on the %s %s%s of each month", ord, strings.ToUpper(day[:1]), day[1:])
		}
	}
	return strings.ToLower(strings.ReplaceAll(rest, "_", " "))
}
