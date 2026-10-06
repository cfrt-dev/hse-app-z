package timetable

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"hse-app-z/internal/api"
	"hse-app-z/internal/config"
	"hse-app-z/internal/ui"
)

// ------------------------------------------------------------------ dates

// civil strips the clock and zone: the calendar date of t (as seen in t's
// own location) at midnight UTC, so date arithmetic never trips over DST.
func civil(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// viewerDay is the viewer's calendar date. Day headers show the lessons'
// own (schedule) dates; "today" is the viewer's. Every today/tomorrow
// decision (header mark, next-lesson mark, "starts in"/"tomorrow") uses
// this one date so the list and the details agree when the viewer's zone
// is not the schedule's (studying from abroad).
func viewerDay(now time.Time) time.Time { return civil(now) }

// mondayOf returns the Monday of the week containing the civil date d.
func mondayOf(d time.Time) time.Time {
	off := (int(d.Weekday()) + 6) % 7
	return d.AddDate(0, 0, -off)
}

func dateKey(d time.Time) string { return d.Format("2006-01-02") }

// dayLabel is "Mon 12 Oct" / "Пн 12 окт".
func dayLabel(d time.Time) string { return ui.FmtDay(d) }

// weekTitle renders a Mon–Sun range: "12–18 Oct 2026",
// "28 Sep – 4 Oct 2026", "29 Dec 2026 – 4 Jan 2027" (in Russian
// "12–18 окт 2026", "28 сен – 4 окт 2026").
func weekTitle(mon time.Time) string {
	sun := mon.AddDate(0, 0, 6)
	switch {
	case mon.Year() != sun.Year():
		return ui.FmtDateShort(mon) + " – " + ui.FmtDateShort(sun)
	case mon.Month() != sun.Month():
		return ui.FmtDayMonth(mon) + " – " + ui.FmtDateShort(sun)
	}
	return fmt.Sprintf("%d–%d %s %d", mon.Day(), sun.Day(), ui.MonthShort(sun.Month()), sun.Year())
}

// relWeek describes mon relative to the current week's Monday cur.
func relWeek(mon, cur time.Time) string {
	n := int(mon.Sub(cur).Hours()/24) / 7
	switch {
	case n == 0:
		return ui.Tr("this week", "эта неделя")
	case n == 1:
		return ui.Tr("next week", "следующая неделя")
	case n == -1:
		return ui.Tr("last week", "прошлая неделя")
	case n > 1:
		return ui.Tr("in ", "через ") + ui.Count(n, "week", "weeks", "неделю", "недели", "недель")
	}
	return ui.Count(-n, "week", "weeks", "неделю", "недели", "недель") + ui.Tr(" ago", " назад")
}

// fmtDur renders a duration rounded up to minutes: "25 min", "2 h",
// "2 h 10 min" ("25 мин", "2 ч", "2 ч 10 мин").
func fmtDur(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	m := int((d + time.Minute - 1) / time.Minute)
	switch {
	case m < 60:
		return ui.Trf("%d min", "%d мин", m)
	case m%60 == 0:
		return ui.Trf("%d h", "%d ч", m/60)
	}
	return ui.Trf("%d h %d min", "%d ч %d мин", m/60, m%60)
}

// ---------------------------------------------------------------- lessons

// lessonKey identifies a lesson occurrence (the API repeats ids for
// recurring lessons, so the start time is part of the key).
func lessonKey(l api.Lesson) string {
	id := strings.TrimSpace(string(l.ID))
	if id == "" {
		id = l.Hash
	}
	if id == "" {
		id = l.Discipline + "|" + l.Auditorium + "|" + l.Kind()
	}
	return id + "|" + strconv.FormatInt(l.DateStart.Unix(), 10)
}

// eventKey identifies the event behind a lesson record: RUZ lists a
// stream lecture once per group (own id, group and stream code), but it is
// one event — same time, place, discipline, kind, lecturers and links.
func eventKey(l api.Lesson) string {
	lects := make([]string, 0, len(l.LecturerEmails))
	for _, e := range l.LecturerEmails {
		lects = append(lects, strings.ToLower(strings.TrimSpace(e)))
	}
	sort.Strings(lects)
	var links []string
	for _, s := range l.Links() {
		links = append(links, s.Link)
	}
	end, _ := endOf(l)
	return strings.Join([]string{
		strconv.FormatInt(l.DateStart.Unix(), 10), strconv.FormatInt(end.Unix(), 10),
		strings.ToLower(ui.Clean(l.Discipline)), strings.ToLower(ui.Clean(l.Kind())),
		strings.ToLower(ui.Clean(l.Auditorium)), strings.ToLower(ui.Clean(l.Building)),
		strings.Join(lects, ","), strings.Join(links, " "),
	}, "|")
}

// normalize drops undated lessons and duplicates (repeated records and
// per-group copies of one stream lesson), and sorts by start.
func normalize(ls []api.Lesson) []api.Lesson {
	seen := map[string]bool{}
	out := make([]api.Lesson, 0, len(ls))
	for _, l := range ls {
		if l.DateStart.IsZero() {
			continue
		}
		k, ev := lessonKey(l), "event|"+eventKey(l)
		if seen[k] || seen[ev] {
			continue
		}
		seen[k], seen[ev] = true, true
		out = append(out, l)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if !a.DateStart.Equal(b.DateStart.Time) {
			return a.DateStart.Before(b.DateStart.Time)
		}
		ea, _ := endOf(a)
		eb, _ := endOf(b)
		if !ea.Equal(eb) {
			return ea.Before(eb)
		}
		return a.Discipline < b.Discipline
	})
	return out
}

// endOf is the lesson end, ok=false when missing or before the start.
func endOf(l api.Lesson) (time.Time, bool) {
	if l.DateEnd.IsZero() || l.DateEnd.Before(l.DateStart.Time) {
		return time.Time{}, false
	}
	return l.DateEnd.Time, true
}

func lessonDay(l api.Lesson) time.Time { return civil(l.Start()) }

func isOngoing(l api.Lesson, now time.Time) bool {
	e, ok := endOf(l)
	return ok && !now.Before(l.DateStart.Time) && now.Before(e)
}

func isPast(l api.Lesson, now time.Time) bool {
	if e, ok := endOf(l); ok {
		return !now.Before(e)
	}
	return now.After(l.DateStart.Time)
}

// timeRange is "10:00–11:20" in the lesson's own zone ("10:00" without an end).
func timeRange(l api.Lesson) string {
	s := l.Start().Format("15:04")
	if e, ok := endOf(l); ok {
		return s + "–" + e.In(l.Zone()).Format("15:04")
	}
	return s
}

func discipline(l api.Lesson) string {
	if d := ui.Clean(l.Discipline); d != "" {
		return d
	}
	return ui.Tr("(untitled)", "(без названия)")
}

// onlineWord is the room of an online lesson.
func onlineWord() string { return ui.Tr("online", "онлайн") }

// roomShort is the room column of a list row.
func roomShort(l api.Lesson) string {
	if l.IsOnline() {
		return onlineWord()
	}
	return ui.Clean(l.Auditorium)
}

// place is "room 303, 34 Tallinskaya st." / "online" / "".
func place(l api.Lesson) string {
	if l.IsOnline() {
		return onlineWord()
	}
	room := ui.Clean(l.Auditorium)
	if room != "" {
		room = ui.Tr("room ", "ауд. ") + room
	}
	return ui.JoinNonEmpty(", ", room, ui.Clean(l.Building))
}

// summary is the one-line text copied with y.
func summary(l api.Lesson) string {
	what := discipline(l)
	if k := ui.Clean(l.Kind()); k != "" {
		what += " (" + k + ")"
	}
	return ui.JoinNonEmpty(", ", ui.FmtDate(l.Start())+" "+timeRange(l), what, place(l))
}

// pairText is "pair 3" / "pairs 3–4" / "" ("пара 3" / "пары 3–4").
func pairText(l api.Lesson) string {
	s, e := l.LessonNumberStart, l.LessonNumberEnd
	if !s.OK || s.V <= 0 {
		return ""
	}
	if e.OK && e.Int() > s.Int() {
		return ui.Trf("pairs %d–%d", "пары %d–%d", s.Int(), e.Int())
	}
	return ui.Trf("pair %d", "пара %d", s.Int())
}

// zoneLabel is the lesson's zone abbreviation ("MSK", "UTC+05"; Moscow
// time is "МСК" in Russian).
func zoneLabel(t time.Time) string {
	s := zoneAbbr(t)
	if s == "MSK" && ui.RU() {
		return "МСК"
	}
	return s
}

func zoneAbbr(t time.Time) string {
	name, off := t.Zone()
	if name == "" || name[0] == '+' || name[0] == '-' {
		sign := "+"
		if off < 0 {
			sign, off = "-", -off
		}
		s := fmt.Sprintf("UTC%s%02d", sign, off/3600)
		if m := off % 3600 / 60; m != 0 {
			s += fmt.Sprintf(":%02d", m)
		}
		return s
	}
	return name
}

// noteText is the note without URLs (those are shown as links).
func noteText(l api.Lesson) string {
	var keep []string
	for _, f := range strings.Fields(ui.Clean(l.Note)) {
		if strings.HasPrefix(f, "http://") || strings.HasPrefix(f, "https://") {
			continue
		}
		keep = append(keep, f)
	}
	return strings.Join(keep, " ")
}

// linkLabel is a short label for a lesson link: its description, else host.
func linkLabel(s api.StreamLink) string {
	d := ui.Clean(s.Description)
	if d != "" && d != strings.TrimSpace(s.Link) {
		return d
	}
	if u, err := url.Parse(strings.TrimSpace(s.Link)); err == nil && u.Host != "" {
		return u.Host
	}
	return ui.Clean(s.Link)
}

// localised title prefixes of room and group targets (titles are stored
// in favourites in the language of the moment, so both are recognised).
var (
	roomPrefixes  = []string{"Room ", "Ауд. ", "Аудитория "}
	groupPrefixes = []string{"Group ", "Группа "}
)

func roomPrefix() string  { return ui.Tr("Room ", "Ауд. ") }
func groupPrefix() string { return ui.Tr("Group ", "Группа ") }

// stripPrefix removes the first matching prefix; ok reports a match.
func stripPrefix(s string, prefixes []string) (string, bool) {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return strings.TrimPrefix(s, p), true
		}
	}
	return s, false
}

// retitle shows a "Room 303" / "Group 123" title in the current language.
func retitle(title string, prefixes []string, prefix string) string {
	if rest, ok := stripPrefix(title, prefixes); ok && strings.TrimSpace(rest) != "" {
		return prefix + rest
	}
	return title
}

// ---------------------------------------------------------------- targets

// lecturerTargets lists the lesson's lecturers as openable targets:
// profiles first, then bare emails without a profile.
func lecturerTargets(l api.Lesson) []ui.Target {
	var out []ui.Target
	seen := map[string]bool{}
	for _, p := range l.LecturerProfiles {
		t, ok := ui.TargetFromPerson(p)
		if !ok || t.Kind != config.KindPerson || seen[t.Key] {
			continue
		}
		if t.Title == "" {
			t.Title = t.Key
		}
		seen[t.Key] = true
		out = append(out, t)
	}
	for _, e := range l.LecturerEmails {
		k := strings.ToLower(strings.TrimSpace(e))
		if k == "" || !strings.Contains(k, "@") || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, ui.Target{Kind: config.KindPerson, Key: k, Title: ui.Clean(k)})
	}
	return out
}

// roomTarget is the timetable of the lesson's room (not for online lessons).
func roomTarget(l api.Lesson) (ui.Target, bool) {
	id := strings.TrimSpace(string(l.AuditoriumID))
	if l.IsOnline() || id == "" {
		return ui.Target{}, false
	}
	room := ui.Clean(l.Auditorium)
	if room == "" {
		room = id
	}
	return ui.Target{Kind: config.KindAuditorium, Key: id, Title: roomPrefix() + room, Subtitle: ui.Clean(l.Building)}, true
}

// streamTarget is the timetable of the lesson's group/stream.
func streamTarget(l api.Lesson) (ui.Target, bool) {
	id := strings.TrimSpace(string(l.GroupID))
	if id == "" {
		return ui.Target{}, false
	}
	title := ui.Clean(l.StreamName())
	if title == "" {
		title = groupPrefix() + ui.Clean(id)
	}
	return ui.Target{Kind: config.KindGroup, Key: id, Title: title, Subtitle: discipline(l)}, true
}

func sameTarget(a, b ui.Target) bool {
	return a.Kind != "" && a.Kind == b.Kind && targetKey(a) == targetKey(b)
}

// targetKey normalises a target key for comparison: emails ignore case;
// RUZ group and room ids come as "ruz526" from search but as "526" in
// lessons (rooms), and the API accepts both.
func targetKey(t ui.Target) string {
	k := strings.ToLower(strings.TrimSpace(t.Key))
	if t.Kind == config.KindGroup || t.Kind == config.KindAuditorium {
		k = strings.TrimPrefix(k, "ruz")
	}
	return k
}

// --------------------------------------------------------------- profiles

func typeLabel(t string) string {
	switch strings.ToUpper(strings.TrimSpace(t)) {
	case api.TypeStudent:
		return ui.Tr("Student", "Студент")
	case api.TypeStaff:
		return ui.Tr("Staff", "Сотрудник")
	case api.TypeGroup:
		return ui.Tr("Group", "Группа")
	case api.TypeAuditorium:
		return ui.Tr("Room", "Аудитория")
	case "":
		return ""
	}
	return capitalize(strings.ToLower(ui.Clean(t)))
}

// degreeLabel turns DEGREE_BACHELOR into "Bachelor" / "Бакалавриат".
func degreeLabel(s string) string {
	s = strings.TrimPrefix(strings.ToUpper(ui.Clean(s)), "DEGREE_")
	if s == "" {
		return ""
	}
	if ui.RU() {
		switch s {
		case "BACHELOR":
			return "Бакалавриат"
		case "MASTER":
			return "Магистратура"
		case "SPECIALIST", "SPECIALITY", "SPECIALTY":
			return "Специалитет"
		case "POSTGRADUATE", "PHD", "ASPIRANTURA", "DOCTORAL":
			return "Аспирантура"
		}
	}
	return capitalize(strings.ToLower(strings.ReplaceAll(s, "_", " ")))
}

// birthday renders a profile's birth date ("14 November", "8 January
// 2008"; "0000" as the year hides it).
func birthday(raw string) string {
	b := strings.TrimSpace(raw)
	if b == "" {
		return ""
	}
	if rest, ok := strings.CutPrefix(b, "0000-"); ok {
		if d, ok := civilDate(2000, rest); ok { // a leap year: 29 February is fine
			return ui.FmtDayMonthLong(d)
		}
		return ui.Clean(rest)
	}
	if t, err := time.Parse("2006-01-02", b); err == nil {
		return ui.FmtDateLong(t)
	}
	return ui.Clean(b)
}

// civilDate parses "MM-DD" in year y, rejecting dates that don't exist.
func civilDate(y int, md string) (time.Time, bool) {
	ms, ds, ok := strings.Cut(md, "-")
	if !ok {
		return time.Time{}, false
	}
	m, err1 := strconv.Atoi(ms)
	d, err2 := strconv.Atoi(ds)
	if err1 != nil || err2 != nil || len(ms) != 2 || len(ds) != 2 {
		return time.Time{}, false
	}
	t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	if t.Month() != time.Month(m) || t.Day() != d {
		return time.Time{}, false
	}
	return t, true
}

func capitalize(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	return strings.ToUpper(string(r[0])) + string(r[1:])
}

// unitShort keeps the last two distinct segments of an "A → B → C" path.
func unitShort(path string) string {
	var segs []string
	seen := map[string]bool{}
	for _, s := range strings.Split(path, "→") {
		s = ui.Clean(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		segs = append(segs, s)
	}
	if len(segs) > 2 {
		segs = segs[len(segs)-2:]
	}
	return strings.Join(segs, " → ")
}

// wrapIndent wraps s to width and indents every line by n spaces.
func wrapIndent(s string, width, n int) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	return ui.Indent(ui.Wrap(s, max(1, width-n)), strings.Repeat(" ", n))
}

// styledWrap wraps plain text and styles each line (styles applied after
// wrapping so escape codes never get split).
func styledWrap(s string, width int, style func(...string) string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(ui.Wrap(s, max(1, width)), "\n")
	for i, l := range lines {
		lines[i] = style(l)
	}
	return strings.Join(lines, "\n")
}
