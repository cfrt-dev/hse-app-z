package timetable

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"hse-app-z/internal/api"
	"hse-app-z/internal/config"
	"hse-app-z/internal/ui"
	"hse-app-z/internal/ui/uitest"
)

// checkSize fails when the rendered view exceeds w×h.
func checkSize(t *testing.T, h *uitest.Harness, w, ht int) string {
	t.Helper()
	raw := h.Page.View(w, ht)
	lines := strings.Split(raw, "\n")
	if len(lines) > ht {
		t.Errorf("%dx%d: %d lines", w, ht, len(lines))
	}
	for i, l := range lines {
		if n := ansi.StringWidth(l); n > w {
			t.Errorf("%dx%d: line %d is %d cells: %q", w, ht, i, n, ansi.Strip(l))
		}
	}
	return ansi.Strip(raw)
}

func checkSizes(t *testing.T, h *uitest.Harness) {
	t.Helper()
	for _, sz := range [][2]int{{100, 30}, {60, 12}, {120, 40}, {80, 24}, {60, 10}, {40, 8}, {200, 50}} {
		checkSize(t, h, sz[0], sz[1])
	}
}

func selectedLesson(t *testing.T, p ui.Page) api.Lesson {
	t.Helper()
	var wk *week
	switch p := p.(type) {
	case *tabPage:
		wk = p.wk
	case *targetPage:
		wk = p.wk
	}
	l, ok := wk.selected()
	if !ok {
		t.Fatalf("no lesson selected")
	}
	return l
}

func lastStatus(h *uitest.Harness) string {
	s := h.Statuses()
	if len(s) == 0 {
		return ""
	}
	return s[len(s)-1]
}

func TestTabWeek(t *testing.T) {
	ctx := uitest.Ctx(t)
	p := NewTab(ctx)
	h := uitest.New(t, p)
	v := checkSize(t, h, 120, 30)
	t.Log("\n" + v)
	checkSizes(t, h)

	for _, want := range []string{"12–18 Oct 2026", "Mon 12 Oct", "Tue 13 Oct · today", "Sat 17 Oct", "this week", "SEM", "LEC", "online"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q", want)
		}
	}
	if strings.Contains(v, "Sun 18 Oct") {
		t.Errorf("empty Sunday got a header")
	}
	// 12:30 MSK on Tuesday: the 11:10–12:30 lecture just ended, Calculus
	// at 14:40 is next.
	l := selectedLesson(t, p)
	if l.Discipline != "Calculus" || l.Start().Format("15:04") != "14:40" {
		t.Fatalf("initial selection = %s %s, want Calculus 14:40", l.Discipline, l.Start())
	}
	if !strings.Contains(v, "starts in 2 h 10 min") {
		t.Errorf("detail lacks relative status:\n%s", v)
	}
	if !strings.Contains(v, "○") {
		t.Errorf("next lesson not marked")
	}

	h.Key("h")
	v = h.View(100, 30)
	if !strings.Contains(v, "5–11 Oct 2026") || !strings.Contains(v, "last week") {
		t.Fatalf("h didn't go to the previous week:\n%s", v)
	}
	if l := selectedLesson(t, p); l.Start().Day() != 5 {
		t.Errorf("previous week should select its first lesson, got %s", l.Start())
	}
	h.Key("l", "l")
	v = h.View(100, 30)
	if !strings.Contains(v, "19–25 Oct 2026") || !strings.Contains(v, "next week") {
		t.Fatalf("l l didn't reach next week:\n%s", v)
	}
	h.Key("t")
	if l := selectedLesson(t, p); l.Discipline != "Calculus" || l.Start().Day() != 13 {
		t.Errorf("t should return to the next lesson today, got %s %s", l.Discipline, l.Start())
	}

	// Cached weeks render without another request.
	pg := p.(*tabPage)
	seq := pg.wk.load.Seq
	h.Key("h", "l")
	if pg.wk.load.Seq != seq {
		t.Errorf("cached weeks were refetched (seq %d → %d)", seq, pg.wk.load.Seq)
	}
	// r refetches the current week but keeps the selection.
	h.Key("t", "j")
	sel := lessonKey(selectedLesson(t, p))
	h.Key("r")
	if pg.wk.load.Seq != seq+1 {
		t.Errorf("r didn't refetch")
	}
	if l := selectedLesson(t, p); lessonKey(l) != sel {
		t.Errorf("refresh lost the selection: %s", l.Discipline)
	}
}

func TestTabNavigation(t *testing.T) {
	ctx := uitest.Ctx(t)
	p := NewTab(ctx)
	h := uitest.New(t, p)

	// ] → first lesson of the next day with lessons (Wed 14: Brain and Psyche).
	h.Key("]")
	if l := selectedLesson(t, p); l.Start().Day() != 14 {
		t.Fatalf("] → %s", l.Start())
	}
	h.Key("]")
	if l := selectedLesson(t, p); l.Start().Day() != 16 {
		t.Fatalf("] → %s, want Fri 16", l.Start())
	}
	h.Key("[", "[")
	if l := selectedLesson(t, p); l.Start().Day() != 13 || l.Start().Hour() != 11 {
		t.Fatalf("[ [ → %s, want Tue 13 first lesson", l.Start())
	}
	h.Key("[", "[")
	// Monday 12 → previous week, first lesson of its last day (Sat 10).
	if l := selectedLesson(t, p); l.Start().Day() != 10 {
		t.Fatalf("[ across the week → %s, want Sat 10", l.Start())
	}
	h.Key("]")
	if l := selectedLesson(t, p); l.Start().Day() != 12 {
		t.Fatalf("] across the week → %s, want Mon 12", l.Start())
	}
	// j/k move between lessons, skipping headers.
	h.Key("j", "j")
	if l := selectedLesson(t, p); l.Start().Day() != 13 {
		t.Fatalf("j j → %s", l.Start())
	}
	h.Key("G")
	if l := selectedLesson(t, p); l.Start().Day() != 17 {
		t.Fatalf("G → %s", l.Start())
	}
	h.Key("J", "J", "K")
	checkSizes(t, h)

	// An empty week still navigates.
	h.Key("l", "l", "l", "l")
	v := h.View(100, 30)
	if !strings.Contains(v, "No classes this week") {
		t.Fatalf("empty week:\n%s", v)
	}
	checkSizes(t, h)
	h.Key("o")
	if s := lastStatus(h); s != "No lesson selected" {
		t.Errorf("o on an empty week: %q", s)
	}
	h.Key("t")
	if l := selectedLesson(t, p); l.Start().Day() != 13 {
		t.Fatalf("t → %s", l.Start())
	}
}

func TestLessonActions(t *testing.T) {
	ctx := uitest.Ctx(t)
	p := NewTab(ctx)
	h := uitest.New(t, p)
	h.Key("enter")
	m, ok := h.LastMenu()
	if !ok {
		t.Fatal("enter opened no menu")
	}
	var labels []string
	for _, a := range m.Actions {
		labels = append(labels, a.Key+" "+a.Label)
	}
	t.Logf("menu %q:\n%s", m.Title, strings.Join(labels, "\n"))
	for _, want := range []string{"Lecturer:", "Room timetable: 402", "Group/stream timetable", "Open course page", "Open on map", "Copy summary"} {
		if !strings.Contains(strings.Join(labels, "\n"), want) {
			t.Errorf("menu lacks %q", want)
		}
	}
	if !h.RunAction("room timetable") {
		t.Fatal("no room action")
	}
	tg, ok := h.LastTarget()
	if !ok || tg.Kind != config.KindAuditorium || tg.Key == "" || !strings.HasPrefix(tg.Title, "Room ") {
		t.Fatalf("room target = %+v", tg)
	}
	h.RunAction("lecturer")
	if tg, _ := h.LastTarget(); tg.Kind != config.KindPerson || !strings.Contains(tg.Key, "@") {
		t.Fatalf("lecturer target = %+v", tg)
	}
	h.RunAction("stream")
	if tg, _ := h.LastTarget(); tg.Kind != config.KindGroup || tg.Key == "" {
		t.Fatalf("group target = %+v", tg)
	}
	h.RunAction("copy")
	if s := lastStatus(h); !strings.Contains(s, "Tue 13 Oct 2026 14:40–16:00") || !strings.Contains(s, "Calculus") {
		t.Errorf("copy summary: %q", s)
	}
	h.Key("o")
	if s := lastStatus(h); !strings.Contains(s, "hse.ru") {
		t.Errorf("o: %q", s)
	}
	h.Key("m")
	if s := lastStatus(h); !strings.Contains(s, "yandex.ru/maps") {
		t.Errorf("m: %q", s)
	}
	h.Key("p")
	if tg, _ := h.LastTarget(); tg.Kind != config.KindPerson {
		t.Errorf("p: %+v", tg)
	}

	// Online lesson (Wed 14): o opens the stream link, a refuses.
	h.Key("]")
	l := selectedLesson(t, p)
	if !l.IsOnline() {
		t.Fatalf("expected the online lesson, got %s", l.Discipline)
	}
	before := len(h.Emitted)
	h.Key("a")
	for _, m := range h.Emitted[before:] {
		if _, ok := m.(ui.OpenTargetMsg); ok {
			t.Errorf("a opened a room for an online lesson")
		}
	}
	h.Key("enter")
	m, _ = h.LastMenu()
	for _, a := range m.Actions {
		if strings.HasPrefix(a.Label, "Room timetable") {
			t.Errorf("online lesson menu has a room action")
		}
	}
}

func TestMultipleLecturers(t *testing.T) {
	ctx := uitest.Ctx(t)
	p := NewTab(ctx)
	h := uitest.New(t, p)
	pg := p.(*tabPage)
	start := time.Date(2026, 10, 13, 10, 0, 0, 0, api.Moscow)
	l := api.Lesson{ID: "x1", Discipline: "Team taught", KindOfWork: "Lecture", TimeZone: "Europe/Moscow",
		DateStart: api.FlexTime{Time: start}, DateEnd: api.FlexTime{Time: start.Add(80 * time.Minute)},
		LecturerProfiles: []api.Person{{FullName: "A One", Email: "a@hse.ru"}, {FullName: "No Email"}},
		LecturerEmails:   []string{"a@hse.ru", "b@hse.ru"}}
	h.Send(ui.Result[weekResult]{ID: pg.id, Seq: pg.wk.load.Seq, Data: weekResult{Key: "2026-10-12", Gen: pg.wk.gen, Lessons: []api.Lesson{l}}})
	h.Key("p")
	m, ok := h.LastMenu()
	if !ok || len(m.Actions) != 2 {
		t.Fatalf("p with two lecturers should open a chooser, got %+v", m)
	}
	h.RunAction("b@hse.ru")
	if tg, _ := h.LastTarget(); tg.Key != "b@hse.ru" {
		t.Errorf("chooser opened %+v", tg)
	}
}

func TestWeekTitle(t *testing.T) {
	cases := map[string]string{
		"2026-10-12": "12–18 Oct 2026",
		"2026-09-28": "28 Sep – 4 Oct 2026",
		"2026-12-28": "28 Dec 2026 – 3 Jan 2027",
	}
	for in, want := range cases {
		d, _ := time.Parse("2006-01-02", in)
		if got := weekTitle(d); got != want {
			t.Errorf("weekTitle(%s) = %q, want %q", in, got, want)
		}
	}
	sun, _ := time.Parse("2006-01-02", "2027-01-03")
	if m := mondayOf(sun); dateKey(m) != "2026-12-28" {
		t.Errorf("mondayOf(Sun 3 Jan 2027) = %s", dateKey(m))
	}
}

func TestStaleWeekResponses(t *testing.T) {
	ctx := uitest.Ctx(t)
	p := NewTab(ctx).(*tabPage)
	h := uitest.New(t, p)
	// A response for an older request must not replace the current week's
	// data, but fills the cache of its own week.
	old := weekResult{Key: "2026-10-12", Gen: p.wk.gen, Lessons: []api.Lesson{{ID: "zz", Discipline: "Stale", DateStart: api.FlexTime{Time: uitest.Now}}}}
	h.Send(ui.Result[weekResult]{ID: p.id, Seq: p.wk.load.Seq - 1, Data: old})
	if v := h.View(100, 30); strings.Contains(v, "Stale") {
		t.Fatalf("stale response replaced loaded data")
	}
	h.Send(ui.Result[weekResult]{ID: p.id, Seq: p.wk.load.Seq - 1, Data: weekResult{Key: "2027-01-04", Gen: p.wk.gen, Lessons: old.Lessons}})
	if st := p.wk.cache["2027-01-04"]; st == nil || !st.loaded {
		t.Errorf("stale response for another week wasn't cached")
	}
	// Other pages' results are ignored.
	h.Send(ui.Result[weekResult]{ID: p.id + 1000, Seq: p.wk.load.Seq, Data: old})
	if v := h.View(100, 30); strings.Contains(v, "Stale") {
		t.Fatalf("foreign result accepted")
	}
	// Reload keeps the shown week until fresh data arrives.
	h.Send(ui.ReloadMsg{})
	if v := h.View(100, 30); !strings.Contains(v, "Calculus") {
		t.Fatalf("reload lost the week:\n%s", v)
	}
	if len(p.wk.cache) != 1 {
		t.Errorf("reload kept %d cached weeks", len(p.wk.cache))
	}
	// Another account signs in: its own week replaces the old data.
	h.Key("l")
	ctx.Me.Email = "midmreznichenko@edu.hse.ru"
	h.Send(ui.ReloadMsg{})
	v := h.View(100, 30)
	if strings.Contains(v, "Calculus") || !strings.Contains(v, "12–18 Oct 2026") || !strings.Contains(v, "No classes this week") {
		t.Fatalf("account switch:\n%s", v)
	}
	h.Key("h")
	if l := selectedLesson(t, p); l.Start().Day() != 5 {
		t.Errorf("new account's previous week: %s", l.Start())
	}
}

func TestEdgeCaseLessons(t *testing.T) {
	ctx := uitest.Ctx(t)
	p := NewTab(ctx).(*tabPage)
	h := uitest.New(t, p)
	at := func(day, hh, mm int) api.FlexTime {
		return api.FlexTime{Time: time.Date(2026, 10, day, hh, mm, 0, 0, api.Moscow)}
	}
	long := strings.Repeat("Очень длинное название дисциплины ", 8)
	ls := []api.Lesson{
		{ID: "1", Discipline: "Zero end", DateStart: at(13, 9, 0)},
		{ID: "2", Discipline: "", KindOfWork: "Экзамен", DateStart: at(13, 12, 0), DateEnd: at(13, 13, 0)},
		{ID: "2", Discipline: "", KindOfWork: "Экзамен", DateStart: at(13, 12, 0), DateEnd: at(13, 13, 0)}, // duplicate
		{ID: "3", Discipline: "Overlap", DateStart: at(13, 12, 30), DateEnd: at(13, 14, 0), Auditorium: "R1234567890123456789"},
		{ID: "4", Discipline: "Night", DateStart: at(14, 23, 0), DateEnd: at(15, 1, 30)},
		{ID: "4b", Discipline: "Perm", DateStart: at(14, 8, 0), DateEnd: at(14, 9, 20), TimeZone: "Asia/Yekaterinburg"},
		{ID: "5", Discipline: long, TimeZone: "Mars/Olympus", DateStart: at(15, 10, 0), DateEnd: at(15, 11, 20), IsBan: true,
			Note: "bring \x1b[31mlaptop\x1b[0m https://example.com/x", StreamLinks: []api.StreamLink{{Link: "https://zoom.us/j/1", Description: "Zoom"}, {Link: ""}},
			LessonNumberStart: api.NewNum(3), LessonNumberEnd: api.NewNum(4),
			UpdatedAt: at(12, 10, 0), CreatedAt: at(1, 10, 0)},
		{ID: "6", Discipline: "End before start", DateStart: at(16, 10, 0), DateEnd: at(16, 9, 0), Location: &api.GeoPoint{}},
		{ID: "7", Discipline: "Undated"},
	}
	for i := 0; i < 70; i++ {
		ls = append(ls, api.Lesson{ID: api.FlexString("g" + time.Duration(i).String()), Discipline: "Group lesson", DateStart: at(12+i%7, 8+i%10, 0), DateEnd: at(12+i%7, 9+i%10, 20), Auditorium: "Online"})
	}
	h.Send(ui.Result[weekResult]{ID: p.id, Seq: p.wk.load.Seq, Data: weekResult{Key: "2026-10-12", Gen: p.wk.gen, Lessons: ls}})
	for i := 0; i < len(p.wk.rows)+2; i++ {
		checkSizes(t, h)
		h.Key("enter", "o", "m", "a", "p", "s", "y", "J")
		h.Key("j")
	}
	h.Key("g")
	for i := 0; i < len(p.wk.rows); i++ {
		l, _ := p.wk.selected()
		if l.Discipline == "" {
			v := checkSize(t, h, 100, 30)
			if !strings.Contains(v, "(untitled)") {
				t.Errorf("untitled lesson not labelled:\n%s", v)
			}
		}
		if strings.HasPrefix(l.Discipline, "Очень") {
			v := checkSize(t, h, 120, 40)
			t.Log("\n" + v)
			for _, want := range []string{"banned/cancelled", "pairs 3–4", "Zoom", "bring laptop", "changed recently"} {
				if !strings.Contains(v, want) {
					t.Errorf("edge lesson detail lacks %q", want)
				}
			}
			if strings.Contains(v, "\x1b[31m") {
				t.Errorf("escape codes from API data leaked")
			}
		}
		if l.Discipline == "Overlap" {
			v := checkSize(t, h, 120, 40)
			if !strings.Contains(v, "now · ends in 1 h 30 min") || !strings.Contains(v, "Overlaps") {
				t.Errorf("ongoing lesson detail:\n%s", v)
			}
		}
		if l.Discipline == "Zero end" {
			if v := checkSize(t, h, 120, 40); !strings.Contains(v, "started 3 h 30 min ago") {
				t.Errorf("zero-end lesson detail:\n%s", v)
			}
		}
		if l.Discipline == "Night" {
			v := checkSize(t, h, 120, 40)
			if !strings.Contains(v, "23:00–01:30 (+1 day) (150 min)") {
				t.Errorf("night lesson detail:\n%s", v)
			}
		}
		if l.Discipline == "Perm" {
			v := checkSize(t, h, 120, 40)
			if !strings.Contains(v, "10:00–11:20 (80 min) UTC+05 = 08:00–09:20 your") {
				t.Errorf("other-zone lesson detail:\n%s", v)
			}
		}
		h.Key("j")
	}
	n := 0
	for _, r := range p.wk.rows {
		if !r.header && p.wk.lessons[r.idx].ID == "2" {
			n++
		}
		if !r.header && p.wk.lessons[r.idx].Discipline == "Undated" {
			t.Errorf("undated lesson listed")
		}
	}
	if n != 1 {
		t.Errorf("duplicate lesson shown %d times", n)
	}

	// Empty and error results.
	h.Send(ui.Result[weekResult]{ID: p.id, Seq: p.wk.load.Seq, Data: weekResult{Key: "2026-10-12", Gen: p.wk.gen}})
	if v := h.View(60, 12); !strings.Contains(v, "No classes") {
		t.Errorf("empty week:\n%s", v)
	}
	checkSizes(t, h)
	h.Key("l")
	// A failed refresh keeps the week's data and shows the error in the title.
	h.Send(ui.Result[weekResult]{ID: p.id, Seq: p.wk.load.Seq, Data: weekResult{Key: "2026-10-19", Gen: p.wk.gen}, Err: &api.APIError{Status: 500}})
	if v := h.View(100, 30); !strings.Contains(v, "server error") || !strings.Contains(v, "Calculus") {
		t.Errorf("failed refresh:\n%s", v)
	}
	// A week that never loaded shows the error with a retry hint.
	delete(p.wk.cache, "2026-10-19")
	p.wk.rebuild()
	h.Send(ui.Result[weekResult]{ID: p.id, Seq: p.wk.load.Seq, Data: weekResult{Key: "2026-10-19", Gen: p.wk.gen}, Err: &api.APIError{Status: 500}})
	if v := h.View(100, 30); !strings.Contains(v, "server error") || !strings.Contains(v, "retry") {
		t.Errorf("error state:\n%s", v)
	}
	checkSizes(t, h)
	// Going back to a fine week doesn't show the other week's error.
	h.Key("h")
	if v := h.View(100, 30); strings.Contains(v, "server error") {
		t.Errorf("error leaked into another week:\n%s", v)
	}
}

func TestStaffTarget(t *testing.T) {
	ctx := uitest.Ctx(t)
	hit := api.Person{Type: "STAFF", FullName: "Kinderkneht Yana Anatolevna", Email: "yakinderknecht@hse.ru", Description: "Профессор"}
	tg, _ := ui.TargetFromPerson(hit)
	p := NewTargetPage(ctx, tg)
	if p.Title() != "Kinderkneht Yana Anatolevna" {
		t.Errorf("title %q", p.Title())
	}
	h := uitest.New(t, p)
	v := checkSize(t, h, 120, 30)
	t.Log("\n" + v)
	checkSizes(t, h)
	for _, want := range []string{"Kinderkneht Yana Anatolevna", "Staff · Профессор · yakinderknecht@hse.ru", "Profile", "14 November", "Belov Aleksandr", "Департамент прикладной математики", "hse.ru/staff/kinderknecht", "room 422", "12–18 Oct 2026"} {
		if !strings.Contains(v, want) {
			t.Errorf("staff page lacks %q", want)
		}
	}
	// i shows the lesson; moving the cursor too.
	h.Key("i")
	if v := h.View(120, 30); !strings.Contains(v, "Probability Theory") || strings.Contains(v, "14 November") {
		t.Errorf("i didn't switch to the lesson:\n%s", v)
	}
	h.Key("i")
	h.Key("j")
	if v := h.View(120, 30); strings.Contains(v, "14 November") {
		t.Errorf("moving the cursor didn't show the lesson")
	}
	// Lecturer of their own lessons: p doesn't reopen the same page.
	before := len(h.Emitted)
	h.Key("p")
	for _, m := range h.Emitted[before:] {
		if _, ok := m.(ui.OpenTargetMsg); ok {
			t.Errorf("p reopened the same person")
		}
	}
	h.Key("c")
	if tg, ok := h.LastTarget(); !ok || tg.Key != "avbelov@hse.ru" {
		t.Errorf("c → %+v", tg)
	}
	h.Key("w")
	if s := lastStatus(h); !strings.Contains(s, "hse.ru/staff/kinderknecht") {
		t.Errorf("w: %q", s)
	}
	h.Key("e")
	if s := lastStatus(h); !strings.Contains(s, "yakinderknecht@hse.ru") {
		t.Errorf("e: %q", s)
	}
	h.Key("f")
	if !ctx.Favs.Has(config.KindPerson, "yakinderknecht@hse.ru") {
		t.Fatalf("f didn't star")
	}
	if v := h.View(100, 30); !strings.Contains(v, "★") {
		t.Errorf("no star in header")
	}
	h.Key("enter")
	m, _ := h.LastMenu()
	var labels []string
	for _, a := range m.Actions {
		labels = append(labels, a.Label)
	}
	all := strings.Join(labels, "|")
	t.Logf("menu %q: %s", m.Title, all)
	for _, want := range []string{"Remove from favourites", "Copy email", "Send email", "Open hse.ru page", "Chief: Belov", "Room timetable", "Copy summary"} {
		if !strings.Contains(all, want) {
			t.Errorf("menu lacks %q", want)
		}
	}
	h.RunAction("send email")
	if s := lastStatus(h); !strings.Contains(s, "mailto:yakinderknecht@hse.ru") {
		t.Errorf("send email: %q", s)
	}
	h.RunAction("remove from favourites")
	if ctx.Favs.Has(config.KindPerson, "yakinderknecht@hse.ru") {
		t.Errorf("unstar failed")
	}
	h.Key("r")
	checkSizes(t, h)
}

func TestChiefWithSubordinates(t *testing.T) {
	ctx := uitest.Ctx(t)
	p := NewTargetPage(ctx, ui.Target{Kind: config.KindPerson, Key: "AVBelov@hse.ru"}).(*targetPage)
	h := uitest.New(t, p)
	if p.Title() != "Belov Aleksandr Vladimirovich" {
		t.Errorf("title from profile = %q", p.Title())
	}
	if p.subLoad.Seq == 0 {
		t.Errorf("subordinates not fetched although the profile has them")
	}
	v := checkSize(t, h, 100, 40)
	if strings.Count(v, "Руководитель департамента") != 1 {
		t.Errorf("duplicate positions not merged:\n%s", v)
	}
	if !strings.Contains(v, "+7495772-95-90,11086") {
		t.Errorf("phone missing:\n%s", v)
	}
	checkSizes(t, h)
}

func TestStudentTarget(t *testing.T) {
	ctx := uitest.Ctx(t)
	p := NewTargetPage(ctx, ui.Target{Kind: config.KindPerson, Key: "midmreznichenko@edu.hse.ru", Title: "Reznichenko Mikhail Dmitrievich"}).(*targetPage)
	h := uitest.New(t, p)
	v := checkSize(t, h, 100, 30)
	t.Log("\n" + v)
	for _, want := range []string{"Student", "Information Security", "Bachelor · group БИБ252 · since 2024", "8 January 2008", "Moscow"} {
		if !strings.Contains(v, want) {
			t.Errorf("student page lacks %q", want)
		}
	}
	if p.subLoad.Seq != 0 {
		t.Errorf("subordinates fetched for a student")
	}
	if p.link != "" {
		t.Errorf("student has no public page, got %q", p.link)
	}
	h.Key("w")
	if s := lastStatus(h); !strings.Contains(s, "No public page") {
		t.Errorf("w: %q", s)
	}
	// The fixture has this student's lessons in the previous week only.
	h.Key("h")
	if _, ok := p.wk.selected(); !ok {
		t.Errorf("no lessons last week")
	}
	checkSizes(t, h)
}

// countingTransport wraps the fixtures: counts lesson requests and can
// override the profile body.
type countingTransport struct {
	base    http.RoundTripper
	lessons int
	profile string
}

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Path == "/v3/ruz/lessons" {
		c.lessons++
	}
	rest := strings.TrimPrefix(r.URL.Path, "/v3/dump/email/")
	if c.profile != "" && rest != r.URL.Path && !strings.Contains(rest, "/") {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(c.profile)), Request: r}, nil
	}
	return c.base.RoundTrip(r)
}

func TestNoTimetable(t *testing.T) {
	ctx := uitest.Ctx(t)
	ct := &countingTransport{base: ctx.API.HTTP.Transport,
		profile: `{"full_name":"Hidden Person","email":"x@hse.ru","type":"STAFF","is_timetable_available":false}`}
	ctx.API.HTTP = &http.Client{Transport: ct}
	p := NewTargetPage(ctx, ui.Target{Kind: config.KindPerson, Key: "x@hse.ru"}).(*targetPage)
	h := uitest.New(t, p)
	v := checkSize(t, h, 100, 30)
	t.Log("\n" + v)
	if !strings.Contains(v, "No timetable for this person") || !strings.Contains(v, "Hidden Person") {
		t.Fatalf("no-timetable state:\n%s", v)
	}
	h.Key("h", "l", "t", "]", "j", "enter", "i", "o", "r")
	checkSizes(t, h)
	if ct.lessons != 0 {
		t.Errorf("lessons fetched %d times although there is no timetable", ct.lessons)
	}

	// A profile that allows it loads the timetable once the profile is in.
	ct2 := &countingTransport{base: uitest.Ctx(t).API.HTTP.Transport}
	ctx.API.HTTP = &http.Client{Transport: ct2}
	p2 := NewTargetPage(ctx, ui.Target{Kind: config.KindPerson, Key: "y@hse.ru"}).(*targetPage)
	uitest.New(t, p2)
	if ct2.lessons != 1 || !p2.wk.enabled {
		t.Errorf("timetable should load once when the profile allows it (requests: %d)", ct2.lessons)
	}
}

func TestNobodyTarget(t *testing.T) {
	ctx := uitest.Ctx(t)
	p := NewTargetPage(ctx, ui.Target{Kind: config.KindPerson, Key: "nobody@hse.ru", Title: "Nobody"}).(*targetPage)
	h := uitest.New(t, p)
	v := checkSize(t, h, 100, 30)
	t.Log("\n" + v)
	if !strings.Contains(v, "Profile not found") {
		t.Errorf("404 not reported")
	}
	if p.wk.load.Seq == 0 || !p.wk.enabled {
		t.Errorf("timetable not loaded after 404")
	}
	checkSizes(t, h)
	h.Key("enter", "c", "f", "i", "j")
	checkSizes(t, h)
}

func TestGroupTarget(t *testing.T) {
	ctx := uitest.Ctx(t)
	p := NewTargetPage(ctx, ui.Target{Kind: config.KindGroup, Key: "ruz75091", Title: "Теория вероятностей", Subtitle: "Probability"}).(*targetPage)
	h := uitest.New(t, p)
	v := checkSize(t, h, 100, 30)
	if !strings.Contains(v, "Group · Probability") || !strings.Contains(v, "ruz75091") {
		t.Errorf("group page:\n%s", v)
	}
	if !strings.Contains(v, "No classes this week") {
		t.Errorf("fixture group has no lessons this week:\n%s", v)
	}
	h.Key("h")
	v = checkSize(t, h, 100, 30)
	t.Log("\n" + v)
	if !strings.Contains(v, "Mon 5 Oct") {
		t.Errorf("previous week:\n%s", v)
	}
	// The stream action is hidden on the stream's own page.
	h.Key("enter")
	m, _ := h.LastMenu()
	for _, a := range m.Actions {
		if strings.Contains(a.Label, "stream") {
			t.Errorf("menu offers the same group: %q", a.Label)
		}
	}
	checkSizes(t, h)

	// A group from search shows its metadata.
	hit := api.Person{Type: "GROUP", ID: "ruz47990", Label: "БИБ255", Course: api.NewNum(2), ProgramName: "2019 очная Информационная безопасность 97"}
	tg, _ := ui.TargetFromPerson(hit)
	h2 := uitest.New(t, NewTargetPage(ctx, tg))
	v = checkSize(t, h2, 100, 30)
	for _, want := range []string{"БИБ255", "course 2", "Информационная"} {
		if !strings.Contains(v, want) {
			t.Errorf("group hit page lacks %q", want)
		}
	}
}

func TestRoomTarget(t *testing.T) {
	ctx := uitest.Ctx(t)
	p := NewTargetPage(ctx, ui.Target{Kind: config.KindAuditorium, Key: "526", Title: "Room 303", Subtitle: "34 Tallinskaya st., Moscow"}).(*targetPage)
	h := uitest.New(t, p)
	h.Key("h")
	v := checkSize(t, h, 100, 30)
	t.Log("\n" + v)
	if !strings.Contains(v, "Room 303") || !strings.Contains(v, "Geometry") {
		t.Errorf("room page:\n%s", v)
	}
	// Profile pane is still shown? h moved → lesson details.
	h.Key("i")
	v = checkSize(t, h, 100, 30)
	if !strings.Contains(v, "press m") {
		t.Errorf("room profile should offer the map (location from lessons):\n%s", v)
	}
	h.Key("m")
	if s := lastStatus(h); !strings.Contains(s, "yandex.ru/maps") {
		t.Errorf("m on room profile: %q", s)
	}
	before := len(h.Emitted)
	h.Key("a")
	for _, m := range h.Emitted[before:] {
		if _, ok := m.(ui.OpenTargetMsg); ok {
			t.Errorf("a reopened the same room")
		}
	}
	h.Key("enter")
	m, _ := h.LastMenu()
	for _, a := range m.Actions {
		if strings.HasPrefix(a.Label, "Room timetable") {
			t.Errorf("menu offers the same room")
		}
	}
	checkSizes(t, h)
}
