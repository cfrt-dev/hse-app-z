package timetable

import (
	"io"
	"strings"
	"testing"
	"time"

	"hse-app-z/internal/api"
	"hse-app-z/internal/config"
	"hse-app-z/internal/ui"
	"hse-app-z/internal/ui/uitest"
)

// zoneWeek shows Mon 12 – Wed 14 Oct lessons (Moscow times) to a viewer
// whose clock is now (in their own zone) and selects the "now" lesson.
func zoneWeek(t *testing.T, now time.Time) (*tabPage, *uitest.Harness) {
	t.Helper()
	ctx := uitest.Ctx(t)
	ctx.Now = func() time.Time { return now }
	p := NewTab(ctx).(*tabPage)
	h := uitest.New(t, p)
	at := func(day, hh, mm int) api.FlexTime {
		return api.FlexTime{Time: time.Date(2026, 10, day, hh, mm, 0, 0, api.Moscow)}
	}
	ls := []api.Lesson{
		{ID: "1", Discipline: "Monday late", KindOfWork: "Lecture", TimeZone: "Europe/Moscow", DateStart: at(12, 18, 10), DateEnd: at(12, 19, 30)},
		{ID: "2", Discipline: "Tuesday morning", KindOfWork: "Seminar", TimeZone: "Europe/Moscow", DateStart: at(13, 9, 30), DateEnd: at(13, 10, 50)},
		{ID: "3", Discipline: "Wednesday", KindOfWork: "Seminar", TimeZone: "Europe/Moscow", DateStart: at(14, 9, 30), DateEnd: at(14, 10, 50)},
	}
	h.Send(ui.Result[weekResult]{ID: p.id, Seq: p.wk.load.Seq, Data: weekResult{Key: "2026-10-12", Gen: p.wk.gen, Lessons: ls}})
	if dateKey(p.wk.mon) != "2026-10-12" {
		t.Fatalf("shown week %s", dateKey(p.wk.mon))
	}
	h.Key("t")
	return p, h
}

func rowWith(v, text string) string {
	for _, l := range strings.Split(v, "\n") {
		if strings.Contains(l, text) {
			return l
		}
	}
	return ""
}

// The "today" header, the ○ next-lesson-today marker and the relative
// status ("starts in", "tomorrow") must agree when the viewer's zone is
// not Moscow.
func TestTodayInViewerZone(t *testing.T) {
	tokyo, _ := time.LoadLocation("Asia/Tokyo")
	ny, _ := time.LoadLocation("America/New_York")

	// Tue 03:00 in Tokyo = Mon 21:00 in Moscow.
	p, h := zoneWeek(t, time.Date(2026, 10, 13, 3, 0, 0, 0, tokyo))
	v := h.View(120, 20)
	t.Log("\n" + v)
	if !strings.Contains(v, "Tue 13 Oct · today") {
		t.Errorf("Tokyo: Tuesday should be today:\n%s", v)
	}
	if l := selectedLesson(t, p); l.Discipline != "Tuesday morning" {
		t.Fatalf("Tokyo: selected %s", l.Discipline)
	}
	if !strings.Contains(rowWith(v, "SEM Tuesday morning"), "○") {
		t.Errorf("Tokyo: today's next lesson not marked:\n%s", v)
	}
	if !strings.Contains(v, "starts in 12 h 30 min") || strings.Contains(v, "tomorrow") {
		t.Errorf("Tokyo: today's lesson described as tomorrow:\n%s", v)
	}

	// Mon 20:00 in New York = Tue 03:00 in Moscow.
	p, h = zoneWeek(t, time.Date(2026, 10, 12, 20, 0, 0, 0, ny))
	v = h.View(120, 20)
	t.Log("\n" + v)
	if !strings.Contains(v, "Mon 12 Oct · today") {
		t.Errorf("New York: Monday should be today:\n%s", v)
	}
	if strings.Contains(v, "○") {
		t.Errorf("New York: a lesson under tomorrow's header is marked as next today:\n%s", v)
	}
	if l := selectedLesson(t, p); l.Discipline != "Tuesday morning" {
		t.Fatalf("New York: selected %s", l.Discipline)
	}
	if !strings.Contains(v, "tomorrow") {
		t.Errorf("New York: tomorrow's lesson not described as tomorrow:\n%s", v)
	}
	h.Key("j")
	if v := h.View(120, 20); !strings.Contains(v, "in 2 days") {
		t.Errorf("New York: Wednesday is in 2 days:\n%s", v)
	}
}

// Around New Year the week spans two years, both in Moscow and abroad.
func TestYearBoundaryWeek(t *testing.T) {
	ny, _ := time.LoadLocation("America/New_York")
	for _, now := range []time.Time{
		time.Date(2026, 12, 31, 23, 30, 0, 0, api.Moscow),
		time.Date(2027, 1, 3, 23, 59, 0, 0, ny), // Sunday night (Mon 07:59 in Moscow)
	} {
		ctx := uitest.Ctx(t)
		ctx.Now = func() time.Time { return now }
		p := NewTab(ctx).(*tabPage)
		h := uitest.New(t, p)
		if v := h.View(100, 20); !strings.Contains(v, "28 Dec 2026 – 3 Jan 2027") || !strings.Contains(v, "this week") {
			t.Errorf("%s: week title:\n%s", now, v)
		}
		h.Key("l")
		if v := h.View(100, 20); !strings.Contains(v, "4–10 Jan 2027") || !strings.Contains(v, "next week") {
			t.Errorf("%s: next week:\n%s", now, v)
		}
		h.Key("h", "h")
		if v := h.View(100, 20); !strings.Contains(v, "21–27 Dec 2026") {
			t.Errorf("%s: previous week:\n%s", now, v)
		}
	}
}

// Rooms found by search have "ruz"-prefixed ids while lessons carry the
// bare number: the room's own page must not offer to open itself again.
func TestRoomFromSearchIsSelf(t *testing.T) {
	ctx := uitest.Ctx(t)
	hit := api.Person{Type: "AUDITORIUM", ID: "ruz526", Room: "303", Description: "34 Tallinskaya st."}
	tg, ok := ui.TargetFromPerson(hit)
	if !ok {
		t.Fatal("room hit not openable")
	}
	p := NewTargetPage(ctx, tg).(*targetPage)
	h := uitest.New(t, p)
	h.Key("h")
	if _, ok := p.wk.selected(); !ok {
		t.Fatal("no lesson in the room's previous week")
	}
	before := len(h.Emitted)
	h.Key("a")
	for _, m := range h.Emitted[before:] {
		if m, ok := m.(ui.OpenTargetMsg); ok {
			t.Errorf("a reopened the same room: %+v", m.Target)
		}
	}
	h.Key("enter")
	m, _ := h.LastMenu()
	for _, a := range m.Actions {
		if strings.HasPrefix(a.Label, "Room timetable") {
			t.Errorf("menu offers the room's own timetable: %q", a.Label)
		}
	}
	// A group page opened by bare id is the same group as "ruz…".
	if !sameTarget(ui.Target{Kind: config.KindGroup, Key: "ruz75091"}, ui.Target{Kind: config.KindGroup, Key: "75091"}) {
		t.Errorf("group ids with and without the ruz prefix differ")
	}
	if sameTarget(ui.Target{Kind: config.KindPerson, Key: "ruzx@hse.ru"}, ui.Target{Kind: config.KindPerson, Key: "x@hse.ru"}) {
		t.Errorf("emails must compare as they are")
	}
}

// Weeks cached long ago are refetched (kept on screen meanwhile) when
// shown again, and the shown week is refreshed periodically: the app
// stays open for days while rooms and times change.
func TestStaleWeeksRefetched(t *testing.T) {
	now := uitest.Now
	ctx := uitest.Ctx(t)
	ctx.Now = func() time.Time { return now }
	p := NewTab(ctx).(*tabPage)
	h := uitest.New(t, p)
	h.Key("l", "h")
	seq := p.wk.load.Seq
	h.Key("l", "h")
	if p.wk.load.Seq != seq {
		t.Fatalf("fresh cached weeks were refetched")
	}
	h.Send(tickMsg{id: p.id})
	if p.wk.load.Seq != seq {
		t.Fatalf("tick refetched fresh data")
	}

	now = now.Add(11 * time.Minute)
	sel := lessonKey(selectedLesson(t, p))
	h.Send(tickMsg{id: p.id})
	if p.wk.load.Seq != seq+1 {
		t.Errorf("tick didn't refresh a stale week (seq %d → %d)", seq, p.wk.load.Seq)
	}
	if l := selectedLesson(t, p); lessonKey(l) != sel {
		t.Errorf("background refresh moved the selection to %s", l.Discipline)
	}
	seq = p.wk.load.Seq
	h.Key("l")
	if p.wk.load.Seq != seq+1 {
		t.Errorf("stale cached week not refetched on navigation")
	}
	if v := h.View(100, 30); !strings.Contains(v, "19–25 Oct 2026") {
		t.Errorf("navigation:\n%s", v)
	}
	// Ticks for other pages are ignored.
	seq = p.wk.load.Seq
	now = now.Add(time.Hour)
	h.Send(tickMsg{id: p.id + 1000})
	if p.wk.load.Seq != seq {
		t.Errorf("foreign tick refreshed")
	}
}

// After a reload (language switch, re-login) the refetched week must
// replace the old data even when its response arrives after the user
// moved on to another week.
func TestReloadResultAfterNavigating(t *testing.T) {
	ctx := uitest.Ctx(t)
	p := NewTab(ctx).(*tabPage)
	h := uitest.New(t, p)
	cur := dateKey(p.wk.mon)
	p.wk.reload() // request in flight (not run)
	seq, gen := p.wk.load.Seq, p.wk.gen
	h.Key("l")
	ru := []api.Lesson{{ID: "ru", Discipline: "Математический анализ", TimeZone: "Europe/Moscow",
		DateStart: api.FlexTime{Time: uitest.Now.Add(2 * time.Hour)}, DateEnd: api.FlexTime{Time: uitest.Now.Add(3 * time.Hour)}}}
	h.Send(ui.Result[weekResult]{ID: p.id, Seq: seq, Data: weekResult{Key: cur, Gen: gen, Lessons: ru}})
	h.Key("h")
	if v := h.View(100, 30); !strings.Contains(v, "Математический анализ") {
		t.Errorf("reloaded week shows the old data:\n%s", v)
	}
}

// A stream lecture listed once per group (own id and group, same event)
// is one row, and isn't reported as overlapping itself. Parallel online
// seminars with different lecturers stay separate.
func TestStreamDuplicatesMerged(t *testing.T) {
	ctx := uitest.Ctx(t)
	p := NewTab(ctx).(*tabPage)
	h := uitest.New(t, p)
	start := time.Date(2026, 10, 13, 10, 0, 0, 0, api.Moscow)
	mk := func(id, group, room string, lects ...string) api.Lesson {
		return api.Lesson{ID: api.FlexString(id), GroupID: api.FlexString(group), Stream: "s#" + group, Discipline: "Calculus",
			KindOfWork: "Lecture", Auditorium: room, TimeZone: "Europe/Moscow", LecturerEmails: lects,
			DateStart: api.FlexTime{Time: start}, DateEnd: api.FlexTime{Time: start.Add(80 * time.Minute)}}
	}
	ls := []api.Lesson{
		mk("1", "ruz1", "R101", "a@hse.ru"), mk("2", "ruz2", "R101", "a@hse.ru"), mk("3", "ruz3", "R101", "a@hse.ru"),
		mk("4", "ruz4", "Online", "b@hse.ru"), mk("5", "ruz5", "Online", "c@hse.ru"),
	}
	h.Send(ui.Result[weekResult]{ID: p.id, Seq: p.wk.load.Seq, Data: weekResult{Key: "2026-10-12", Gen: p.wk.gen, Lessons: ls}})
	if n := len(p.wk.lessons); n != 3 {
		t.Errorf("%d lessons shown, want 3 (one stream lecture + two parallel online seminars)", n)
	}
	h.Key("g")
	if v := h.View(120, 30); strings.Contains(v, "Overlaps") && strings.Contains(rowWith(v, "Overlaps"), "R101") {
		t.Errorf("stream lecture overlaps itself:\n%s", v)
	}
}

// "Your time" names the viewer's weekday when it differs from the
// lesson's date (Monday evening in Moscow is Tuesday night in Tokyo).
func TestYourTimeOtherDay(t *testing.T) {
	tokyo, _ := time.LoadLocation("Asia/Tokyo")
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, tokyo)
	start := time.Date(2026, 10, 12, 18, 10, 0, 0, api.Moscow)
	l := api.Lesson{TimeZone: "Europe/Moscow", DateStart: api.FlexTime{Time: start}, DateEnd: api.FlexTime{Time: start.Add(80 * time.Minute)}}
	if got := timeLine(l, now); !strings.Contains(got, "MSK = Tue 00:10–01:30 your time") {
		t.Errorf("timeLine = %q", got)
	}
	l.DateStart.Time = start.Add(-8 * time.Hour) // 10:10 MSK = 16:10 JST, same day
	l.DateEnd.Time = start.Add(-8*time.Hour + 80*time.Minute)
	if got := timeLine(l, now); !strings.Contains(got, "MSK = 16:10–17:30 your time") {
		t.Errorf("timeLine = %q", got)
	}
}

// Every row of the list drops the same columns (end time, kind, room), so
// the columns stay aligned whatever each lesson's room is.
func TestLessonColumnsAligned(t *testing.T) {
	ctx := uitest.Ctx(t)
	p := NewTab(ctx).(*tabPage)
	h := uitest.New(t, p)
	var ls []api.Lesson
	for i, room := range []string{"303", "Online", "", "R1234567890", "4205"} {
		s := time.Date(2026, 10, 13, 9+i, 0, 0, 0, api.Moscow)
		ls = append(ls, api.Lesson{ID: api.FlexString(room + "x"), Discipline: "Дисциплина " + room, KindOfWork: "Seminar", Auditorium: room,
			TimeZone: "Europe/Moscow", DateStart: api.FlexTime{Time: s}, DateEnd: api.FlexTime{Time: s.Add(80 * time.Minute)}})
	}
	h.Send(ui.Result[weekResult]{ID: p.id, Seq: p.wk.load.Seq, Data: weekResult{Key: "2026-10-12", Gen: p.wk.gen, Lessons: ls}})
	for w := 60; w <= 130; w++ {
		v := checkSize(t, h, w, 12)
		var withKind, withEnd, rows int
		col := -1
		for _, line := range strings.Split(v, "\n") {
			left, _, _ := strings.Cut(line, "│")
			i := strings.Index(left, "Ди")
			if i < 0 {
				continue
			}
			rows++
			if strings.Contains(left, "SEM") {
				withKind++
			}
			if strings.Contains(left, "–") {
				withEnd++
			}
			c := len([]rune(left[:i]))
			if col >= 0 && c != col {
				t.Errorf("width %d: discipline column at %d and %d:\n%s", w, col, c, v)
				break
			}
			col = c
		}
		if rows != 5 || (withKind != 0 && withKind != rows) || (withEnd != 0 && withEnd != rows) {
			t.Errorf("width %d: %d rows, %d with kind, %d with end time:\n%s", w, rows, withKind, withEnd, v)
		}
	}
}

// While refreshes fail (offline), the periodic re-check doesn't retry on
// every tick.
func TestStaleRefreshFailingNoHammering(t *testing.T) {
	now := uitest.Now
	ctx := uitest.Ctx(t)
	ctx.Now = func() time.Time { return now }
	p := NewTab(ctx).(*tabPage)
	h := uitest.New(t, p)
	p.wk.reload() // in flight, then fails
	h.Send(ui.Result[weekResult]{ID: p.id, Seq: p.wk.load.Seq, Data: weekResult{Key: dateKey(p.wk.mon), Gen: p.wk.gen}, Err: &api.NetworkError{Err: io.EOF}})
	seq := p.wk.load.Seq
	for i := 0; i < 5; i++ {
		now = now.Add(30 * time.Second)
		h.Send(tickMsg{id: p.id})
	}
	if p.wk.load.Seq != seq {
		t.Errorf("failed week re-requested on every tick (%d requests)", p.wk.load.Seq-seq)
	}
	if v := h.View(100, 30); !strings.Contains(v, "Calculus") {
		t.Errorf("failed refresh lost the data:\n%s", v)
	}
}
