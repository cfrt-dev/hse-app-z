package timetable

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"hse-app-z/internal/api"
	"hse-app-z/internal/config"
	"hse-app-z/internal/kitty"
	"hse-app-z/internal/ui"
	"hse-app-z/internal/ui/avatar"
	"hse-app-z/internal/ui/uitest"
)

// russian switches the interface to Russian for one test. The language is
// process-wide: tests calling this must not run in parallel.
func russian(t *testing.T) {
	t.Helper()
	ui.SetLang("ru")
	t.Cleanup(func() { ui.SetLang("en") })
}

// englishUI lists English words and phrases this package writes on screen.
// Fixture data is partly English (discipline names, "Seminar", Latin
// names, "room 422" in an address), so only our own wording is listed,
// case-sensitively and as whole words.
var englishUI = regexp.MustCompile(`\b(` + strings.Join([]string{
	"Schedule", "Timetable", "Loading", "loading", "today", "this week", "next week", "last week", "weeks",
	"No classes", "Nothing scheduled", "other weeks", "starts", "ends in", "now ·", "tomorrow", "ended",
	"started", "ago", "past", "in \\d+ days", "\\d+ min", "\\d+ h", "pairs?", "online", "Online",
	"untitled", "\\+1 day", "your time", "banned", "Date", "Time", "Building", "Overlaps", "Note",
	"Lecturers?", "Details", "Stream", "Group", "Course page", "Updated", "changed recently",
	"Profile", "Type", "About", "Email", "Birthday", "Campus", "Web page", "Positions", "Position",
	"main", "chief", "Office", "phone", "ext", "Office hours", "Education", "Program", "since", "group",
	"Subordinates", "Student", "Staff", "not found", "not available", "press", "retry",
	"RUZ id", "Location", "Course", "Room", "open the map",
	"January", "February", "March", "April", "June", "July", "August", "September", "October",
	"November", "December", "Jan", "Feb", "Mar", "Apr", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec",
	"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun", "Monday", "Tuesday", "Wednesday", "Thursday",
	"Friday", "Saturday", "Sunday",
}, "|") + `)\b`)

// checkRU renders the page at the sizes of the brief and fails on
// overflow or on leftover English interface words.
func checkRU(t *testing.T, h *uitest.Harness, what string) string {
	t.Helper()
	var big string
	for _, sz := range [][2]int{{100, 30}, {60, 12}, {120, 30}, {60, 10}} {
		v := checkSize(t, h, sz[0], sz[1])
		if sz[0] == 100 {
			big = v
		}
		if m := englishUI.FindAllString(v, -1); len(m) > 0 {
			t.Errorf("%s %dx%d: English left: %q\n%s", what, sz[0], sz[1], m, v)
		}
	}
	var hints []string
	for _, hn := range h.Page.Hints() {
		hints = append(hints, hn.Desc)
	}
	if m := englishUI.FindAllString(h.Page.Title()+" | "+strings.Join(hints, " | "), -1); len(m) > 0 {
		t.Errorf("%s: English in title/hints: %q", what, m)
	}
	return big
}

// checkMenuRU opens the action menu and checks its labels.
func checkMenuRU(t *testing.T, h *uitest.Harness, what string) ui.MenuMsg {
	t.Helper()
	before := len(h.Emitted)
	h.Key("enter")
	var m ui.MenuMsg
	for _, e := range h.Emitted[before:] {
		if mm, ok := e.(ui.MenuMsg); ok {
			m = mm
		}
	}
	for _, a := range m.Actions {
		if hit := englishUI.FindAllString(a.Label, -1); len(hit) > 0 {
			t.Errorf("%s: English in menu label %q: %q", what, a.Label, hit)
		}
	}
	return m
}

// checkStatusesRU checks the footer messages emitted since before.
func checkStatusesRU(t *testing.T, h *uitest.Harness, before int, what string) {
	t.Helper()
	for _, e := range h.Emitted[before:] {
		s, ok := e.(ui.StatusMsg)
		if !ok {
			continue
		}
		// Copy/open statuses quote data (lesson summary, URLs): only the
		// part before the first ":" is ours.
		text := s.Text
		if i := strings.Index(text, ":"); i >= 0 {
			text = text[:i]
		}
		if hit := englishUI.FindAllString(text, -1); len(hit) > 0 {
			t.Errorf("%s: English in status %q: %q", what, s.Text, hit)
		}
	}
}

func TestRussianSchedule(t *testing.T) {
	russian(t)
	ctx := uitest.Ctx(t)
	p := NewTab(ctx).(*tabPage)
	h := uitest.New(t, p)
	v := checkRU(t, h, "tab")
	t.Log("\n" + h.View(120, 30))
	for _, want := range []string{"Расписание", "12–18 окт 2026", "эта неделя", "Пн 12 окт", "Вт 13 окт · сегодня", "через 2 ч 10 мин", "онлайн", "Дата", "Вторник, 13 октября", "Время", "Аудитория", "Корпус"} {
		if !strings.Contains(v+p.Title(), want) {
			t.Errorf("RU tab lacks %q:\n%s", want, v)
		}
	}
	checkMenuRU(t, h, "lesson menu")
	before := len(h.Emitted)
	h.Key("o", "m", "a", "p", "s", "y")
	checkStatusesRU(t, h, before, "lesson keys")
	if s := lastStatus(h); !strings.Contains(s, "Вт 13 окт 2026 14:40–16:00") {
		t.Errorf("RU summary: %q", s)
	}

	h.Key("h")
	if v := checkRU(t, h, "previous week"); !strings.Contains(v, "5–11 окт 2026") || !strings.Contains(v, "прошлая неделя") {
		t.Errorf("RU previous week:\n%s", v)
	}
	h.Key("l", "l")
	if v := checkRU(t, h, "next week"); !strings.Contains(v, "следующая неделя") {
		t.Errorf("RU next week:\n%s", v)
	}
	h.Key("l", "l", "l")
	v = checkRU(t, h, "empty week")
	if !strings.Contains(v, "через 4 недели") || !strings.Contains(v, "На этой неделе пар нет") || !strings.Contains(v, "Нет пар на неделе 9–15 ноя 2026") {
		t.Errorf("RU empty week:\n%s", v)
	}
	before = len(h.Emitted)
	h.Key("o")
	checkStatusesRU(t, h, before, "empty week keys")
	h.Key("t", "t")
	before = len(h.Emitted)
	for i := 0; i < 30; i++ {
		h.Key("j")
		checkRU(t, h, "lesson")
		checkMenuRU(t, h, "lesson menu")
		h.Key("o", "m", "a", "p", "s", "y")
	}
	checkStatusesRU(t, h, before, "lesson keys")

	// Error state of a week that never loaded.
	h.Key("l")
	delete(p.wk.cache, dateKey(p.wk.mon))
	p.wk.rebuild()
	h.Send(ui.Result[weekResult]{ID: p.id, Seq: p.wk.load.Seq, Data: weekResult{Key: dateKey(p.wk.mon), Gen: p.wk.gen}, Err: &api.APIError{Status: 500}})
	checkRU(t, h, "error")
}

func TestRussianRelativeTimes(t *testing.T) {
	russian(t)
	at := func(day, hh, mm int) time.Time { return time.Date(2026, 10, day, hh, mm, 0, 0, api.Moscow) }
	mk := func(s, e time.Time) api.Lesson {
		return api.Lesson{TimeZone: "Europe/Moscow", DateStart: api.FlexTime{Time: s}, DateEnd: api.FlexTime{Time: e}}
	}
	now := at(13, 12, 30)
	for _, c := range []struct {
		l    api.Lesson
		want string
	}{
		{mk(at(13, 12, 30), at(13, 13, 50)), "идёт · до конца 1 ч 20 мин"},
		{mk(at(13, 12, 0), at(13, 12, 55)), "идёт · до конца 25 мин"},
		{mk(at(13, 14, 40), at(13, 16, 0)), "через 2 ч 10 мин"},
		{mk(at(13, 12, 30).Add(30*time.Second), at(13, 14, 0)), "начинается"},
		{mk(at(14, 9, 30), at(14, 10, 50)), "завтра"},
		{mk(at(15, 9, 30), at(15, 10, 50)), "через 2 дня"},
		{mk(at(18, 9, 30), at(18, 10, 50)), "через 5 дней"},
		{mk(at(13, 9, 0), at(13, 10, 20)), "закончилась"},
		{mk(at(13, 9, 0), time.Time{}), "началась 3 ч 30 мин назад"},
		{mk(at(12, 9, 0), time.Time{}), "прошла"},
	} {
		if got, _ := lessonStatus(c.l, now); got != c.want {
			t.Errorf("lessonStatus(%s) = %q, want %q", c.l.Start(), got, c.want)
		}
	}
	mon := at(12, 0, 0)
	cur := civil(mon)
	for n, want := range map[int]string{0: "эта неделя", 1: "следующая неделя", -1: "прошлая неделя", 2: "через 2 недели",
		5: "через 5 недель", 21: "через 21 неделю", -3: "3 недели назад", -11: "11 недель назад"} {
		if got := relWeek(cur.AddDate(0, 0, 7*n), cur); got != want {
			t.Errorf("relWeek(%d) = %q, want %q", n, got, want)
		}
	}
	for in, want := range map[string]string{"2026-10-12": "12–18 окт 2026", "2026-09-28": "28 сен – 4 окт 2026", "2026-12-28": "28 дек 2026 – 3 янв 2027", "2027-04-26": "26 апр – 2 мая 2027"} {
		d, _ := time.Parse("2006-01-02", in)
		if got := weekTitle(d); got != want {
			t.Errorf("weekTitle(%s) = %q, want %q", in, got, want)
		}
	}
	l := mk(at(13, 9, 30), at(13, 10, 50))
	l.LessonNumberStart, l.LessonNumberEnd = api.NewNum(3), api.NewNum(4)
	tokyo, _ := time.LoadLocation("Asia/Tokyo")
	got := timeLine(mk(at(12, 18, 10), at(12, 19, 30)), time.Date(2026, 10, 12, 12, 0, 0, 0, tokyo))
	if !strings.Contains(got, "(80 мин) МСК = вт 00:10–01:30 по вашему времени") {
		t.Errorf("timeLine = %q", got)
	}
	if got := timeLine(l, now); got != "09:30–10:50 (80 мин) · пары 3–4" {
		t.Errorf("timeLine = %q", got)
	}
	for in, want := range map[string]string{"0000-11-14": "14 ноября", "0000-02-29": "29 февраля", "2008-01-08": "8 января 2008", "0000-13-01": "13-01", "": "", "junk": "junk"} {
		if got := birthday(in); got != want {
			t.Errorf("birthday(%q) = %q, want %q", in, got, want)
		}
	}
	ui.SetLang("en")
	for in, want := range map[string]string{"0000-11-14": "14 November", "0000-02-29": "29 February", "2008-01-08": "8 January 2008", "0000-02-30": "02-30"} {
		if got := birthday(in); got != want {
			t.Errorf("en birthday(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRussianEdgeLessons(t *testing.T) {
	russian(t)
	ctx := uitest.Ctx(t)
	p := NewTab(ctx).(*tabPage)
	h := uitest.New(t, p)
	at := func(day, hh, mm int) api.FlexTime {
		return api.FlexTime{Time: time.Date(2026, 10, day, hh, mm, 0, 0, api.Moscow)}
	}
	ls := []api.Lesson{
		{ID: "1", Discipline: "Без конца", DateStart: at(13, 9, 0)},
		{ID: "2", Discipline: "", KindOfWork: "Экзамен", DateStart: at(13, 12, 0), DateEnd: at(13, 13, 0), Auditorium: "Online"},
		{ID: "3", Discipline: "Накладка", DateStart: at(13, 12, 30), DateEnd: at(13, 14, 0), Auditorium: "R123", Building: "Покровский бульвар, 11"},
		{ID: "4", Discipline: "Ночная", DateStart: at(14, 23, 0), DateEnd: at(15, 1, 30)},
		{ID: "5", Discipline: "Отменённая", IsBan: true, DateStart: at(15, 10, 0), DateEnd: at(15, 11, 20),
			StreamLinks: []api.StreamLink{{Link: "https://zoom.us/j/1"}}, LessonNumberStart: api.NewNum(3),
			Stream: "s#Поток 1", GroupID: "ruz1", DisciplineLink: "https://www.hse.ru/edu/courses/1",
			UpdatedAt: at(12, 10, 0), CreatedAt: at(1, 10, 0), LecturerEmails: []string{"a@hse.ru", "b@hse.ru"}},
	}
	h.Send(ui.Result[weekResult]{ID: p.id, Seq: p.wk.load.Seq, Data: weekResult{Key: "2026-10-12", Gen: p.wk.gen, Lessons: ls}})
	h.Key("g")
	var all []string
	for i := 0; i < len(p.wk.rows); i++ {
		all = append(all, checkRU(t, h, "edge lesson"))
		checkMenuRU(t, h, "edge menu")
		before := len(h.Emitted)
		h.Key("o", "m", "a", "p", "s", "y")
		checkStatusesRU(t, h, before, "edge keys")
		h.Key("j")
	}
	joined := strings.Join(all, "\n")
	for _, want := range []string{"(без названия)", "Онлайн", "Накладка", "отмечена в РУЗ как отменённая", "пара 3", "недавно изменена", "Обновлено", "12 окт 2026 10:00", "(+1 день)", "Преподаватели", "Подробности", "Ссылки", "Курс", "Поток", "Группа", "началась 3 ч 30 мин назад"} {
		if !strings.Contains(joined, want) {
			t.Errorf("RU edge lessons lack %q", want)
		}
	}
}

func TestRussianTargets(t *testing.T) {
	russian(t)
	ctx := uitest.Ctx(t)

	hit := api.Person{Type: "STAFF", FullName: "Kinderkneht Yana Anatolevna", Email: "yakinderknecht@hse.ru", Description: "Профессор"}
	tg, _ := ui.TargetFromPerson(hit)
	h := uitest.New(t, NewTargetPage(ctx, tg))
	v := checkRU(t, h, "staff")
	t.Log("\n" + h.View(120, 30))
	for _, want := range []string{"Сотрудник · Профессор", "Профиль", "Дата рожд.", "14 ноября", "Должности", "основная", "руководитель: Belov", "Кабинет", "Страница", "12–18 окт 2026"} {
		if !strings.Contains(v, want) {
			t.Errorf("RU staff page lacks %q:\n%s", want, v)
		}
	}
	m := checkMenuRU(t, h, "staff menu")
	labels := ""
	for _, a := range m.Actions {
		labels += a.Label + "|"
	}
	for _, want := range []string{"Добавить в избранное", "Скопировать почту", "Написать письмо", "Открыть страницу на hse.ru", "Руководитель: Belov"} {
		if !strings.Contains(labels, want) {
			t.Errorf("RU staff menu lacks %q: %s", want, labels)
		}
	}
	before := len(h.Emitted)
	h.Key("e", "w", "c", "i")
	checkStatusesRU(t, h, before, "staff keys")
	checkRU(t, h, "staff lesson")

	cp := NewTargetPage(ctx, ui.Target{Kind: config.KindPerson, Key: "avbelov@hse.ru"}).(*targetPage)
	chief := uitest.New(t, cp)
	chief.Send(ui.Result[subsData]{ID: cp.id, Seq: cp.subLoad.Seq, Data: subsData{People: []api.Person{
		{FullName: "Kinderkneht Yana Anatolevna", Email: "yakinderknecht@hse.ru", Description: "Профессор", Type: "STAFF"}}}})
	if v := checkRU(t, chief, "chief"); !strings.Contains(v, "тел.") {
		t.Errorf("RU chief lacks the phone:\n%s", v)
	}
	if v := chief.View(100, 60); !strings.Contains(v, "Подчинённые") || !strings.Contains(v, "Часы приёма") && !strings.Contains(v, "Consultation time") {
		t.Errorf("RU chief lacks subordinates:\n%s", v)
	}
	m = checkMenuRU(t, chief, "chief menu")
	labels = ""
	for _, a := range m.Actions {
		labels += a.Label + "|"
	}
	if !strings.Contains(labels, "Подчинённый: Kinderkneht") || !strings.Contains(labels, "Подключиться к часам приёма (us02web.zoom.us)") {
		t.Errorf("RU chief menu: %s", labels)
	}

	student := uitest.New(t, NewTargetPage(ctx, ui.Target{Kind: config.KindPerson, Key: "midmreznichenko@edu.hse.ru"}))
	v = checkRU(t, student, "student")
	for _, want := range []string{"Студент", "Бакалавриат · группа БИБ252 · с 2024", "8 января 2008", "Москва", "Образование"} {
		if !strings.Contains(v, want) {
			t.Errorf("RU student page lacks %q:\n%s", want, v)
		}
	}
	before = len(student.Emitted)
	student.Key("w", "c")
	checkStatusesRU(t, student, before, "student keys")

	nobody := uitest.New(t, NewTargetPage(ctx, ui.Target{Kind: config.KindPerson, Key: "nobody@hse.ru", Title: "Nobody"}))
	if v := checkRU(t, nobody, "404"); !strings.Contains(v, "Профиль не найден") {
		t.Errorf("RU 404:\n%s", v)
	}

	ctx2 := uitest.Ctx(t)
	ctx2.API.HTTP = &http.Client{Transport: &countingTransport{base: ctx2.API.HTTP.Transport,
		profile: `{"full_name":"Hidden Person","email":"x@hse.ru","type":"STAFF","is_timetable_available":false}`}}
	hidden := uitest.New(t, NewTargetPage(ctx2, ui.Target{Kind: config.KindPerson, Key: "x@hse.ru"}))
	if v := checkRU(t, hidden, "no timetable"); !strings.Contains(v, "У этого человека нет расписания") || !strings.Contains(v, "недоступно") {
		t.Errorf("RU no timetable:\n%s", v)
	}
	before = len(hidden.Emitted)
	hidden.Key("i", "o")
	checkStatusesRU(t, hidden, before, "no timetable keys")

	// A failing profile request.
	ctx3 := uitest.Ctx(t)
	ctx3.API.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 500, Header: http.Header{}, Body: http.NoBody, Request: r}, nil
	})}
	broken := uitest.New(t, NewTargetPage(ctx3, ui.Target{Kind: config.KindPerson, Key: "z@hse.ru"}))
	if v := checkRU(t, broken, "profile error"); !strings.Contains(v, "нажмите r, чтобы повторить") {
		t.Errorf("RU profile error:\n%s", v)
	}

	// Loading states (no responses yet).
	loading := NewTargetPage(ctx, ui.Target{Kind: config.KindPerson, Key: "slow@hse.ru"})
	lh := &uitest.Harness{T: t, Page: loading}
	loading.(*targetPage).profLoad.Begin()
	if v := checkRU(t, lh, "loading"); !strings.Contains(v, "Загрузка профиля…") || !strings.Contains(v, "Загрузка…") {
		t.Errorf("RU loading:\n%s", v)
	}

	group := uitest.New(t, NewTargetPage(ctx, ui.Target{Kind: config.KindGroup, Key: "ruz75091", Title: "Теория вероятностей"}))
	if v := checkRU(t, group, "group"); !strings.Contains(v, "Группа") || !strings.Contains(v, "ID в РУЗ") {
		t.Errorf("RU group:\n%s", v)
	}
	group.Key("h")
	checkRU(t, group, "group week")
	checkMenuRU(t, group, "group menu")

	room := NewTargetPage(ctx, ui.Target{Kind: config.KindAuditorium, Key: "526", Title: "Room 303"})
	if room.Title() != "Ауд. 303" {
		t.Errorf("room title in RU = %q", room.Title())
	}
	rh := uitest.New(t, room)
	rh.Key("h")
	rh.Key("i")
	if v := checkRU(t, rh, "room"); !strings.Contains(v, "нажмите m, чтобы открыть карту") || !strings.Contains(v, "Расписание аудитории") {
		t.Errorf("RU room:\n%s", v)
	}
	checkMenuRU(t, rh, "room menu")
	before = len(rh.Emitted)
	rh.Key("a", "s")
	checkStatusesRU(t, rh, before, "room keys")
	ui.SetLang("en")
	if room.Title() != "Room 303" {
		t.Errorf("room title back in EN = %q", room.Title())
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// The interface language can change at any moment: the next frame of an
// already open page is in the new language.
func TestLanguageSwitchLive(t *testing.T) {
	ctx := uitest.Ctx(t)
	p := NewTab(ctx)
	h := uitest.New(t, p)
	if v := h.View(100, 30); !strings.Contains(v, "this week") {
		t.Fatalf("EN:\n%s", v)
	}
	russian(t)
	if v := h.View(100, 30); !strings.Contains(v, "эта неделя") || strings.Contains(v, "this week") {
		t.Errorf("RU after switch:\n%s", v)
	}
	if p.Title() != "Расписание" {
		t.Errorf("title %q", p.Title())
	}
}

// --------------------------------------------------------------- avatars

func testJPEG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 60, 80))
	for y := 0; y < 80; y++ {
		for x := 0; x < 60; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 4), uint8(y * 3), 128, 255})
		}
	}
	var b bytes.Buffer
	_ = jpeg.Encode(&b, img, nil)
	return b.Bytes()
}

// enableAvatars turns images on with a test server serving one JPEG.
func enableAvatars(t *testing.T) string {
	t.Helper()
	jpg := testJPEG()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(jpg)
	}))
	t.Cleanup(srv.Close)
	avatar.Default = &avatar.Registry{}
	avatar.Default.Enable(kitty.Support{Enabled: true}, srv.Client())
	t.Cleanup(func() { avatar.Default = &avatar.Registry{} })
	return srv.URL
}

const placeholderRune = "\U0010EEEE"

// imageCells finds the placeholder block: the first line with image
// cells, its column, and how many lines have them.
func imageCells(raw string) (first, col, rows int) {
	first, col = -1, -1
	for i, l := range strings.Split(raw, "\n") {
		plain := ansi.Strip(l)
		j := strings.Index(plain, placeholderRune)
		if j < 0 {
			continue
		}
		if first < 0 {
			first, col = i, ansi.StringWidth(plain[:j])
		}
		rows++
	}
	return first, col, rows
}

func TestAvatarOnProfile(t *testing.T) {
	base := enableAvatars(t)
	ctx := uitest.Ctx(t)
	ct := &countingTransport{base: ctx.API.HTTP.Transport,
		profile: `{"full_name":"Picture Person","email":"pic@hse.ru","type":"STAFF","description":"Профессор","birth_date":"0000-02-29","avatar_url":"` + base + `/img/user/1.jpg"}`}
	ctx.API.HTTP = &http.Client{Transport: ct}
	p := NewTargetPage(ctx, ui.Target{Kind: config.KindPerson, Key: "pic@hse.ru"}).(*targetPage)
	h := uitest.New(t, p)
	loaded := false
	for _, m := range h.Emitted {
		if m, ok := m.(avatar.LoadedMsg); ok && strings.HasSuffix(m.URL, "/img/user/1.jpg") {
			loaded = true
		}
	}
	if !loaded {
		t.Fatal("the profile's avatar was not requested")
	}

	// Wide pane: the picture at the top, name/type/description/email beside it.
	raw := p.View(120, 30)
	checkSize(t, h, 120, 30)
	first, col, rows := imageCells(raw)
	if rows != avatar.Rows || first != 3 {
		t.Fatalf("picture: %d rows from line %d, want %d from line 3 (pane title below the header):\n%s", rows, first, avatar.Rows, ansi.Strip(raw))
	}
	lines := strings.Split(ansi.Strip(raw), "\n")
	if !strings.Contains(lines[first], "Picture Person") || !strings.Contains(lines[first+1], "Staff") ||
		!strings.Contains(lines[first+2], "Профессор") || !strings.Contains(lines[first+3], "pic@hse.ru") {
		t.Errorf("text beside the picture:\n%s", strings.Join(lines[first:first+avatar.Rows], "\n"))
	}
	if strings.Contains(ansi.Strip(raw), "About") {
		t.Errorf("type/description/email repeated below the picture:\n%s", ansi.Strip(raw))
	}
	if !strings.Contains(ansi.Strip(raw), "29 February") {
		t.Errorf("birthday below the picture missing")
	}
	t.Logf("picture at column %d\n%s", col, strings.ReplaceAll(ansi.Strip(raw), placeholderRune, "#"))

	// Narrow pane: the picture above the usual profile text.
	raw = p.View(60, 24)
	checkSize(t, h, 60, 24)
	first, _, rows = imageCells(raw)
	lines = strings.Split(ansi.Strip(raw), "\n")
	if rows != avatar.Rows || first < 0 {
		t.Fatalf("narrow: picture rows %d:\n%s", rows, ansi.Strip(raw))
	}
	below := strings.Join(lines[first+avatar.Rows:], "\n")
	if !strings.Contains(below, "Type") || !strings.Contains(below, "Staff") || !strings.Contains(below, "pic@hse.ru") {
		t.Errorf("narrow: profile text should follow the picture:\n%s", ansi.Strip(raw))
	}
	for _, sz := range [][2]int{{100, 30}, {60, 12}, {80, 24}, {60, 10}, {40, 8}, {200, 50}} {
		checkSize(t, h, sz[0], sz[1])
	}
	// Scrolling the pane keeps every line within the width.
	h.Key("J", "J", "J")
	checkSizes(t, h)

	// Russian: same layout.
	russian(t)
	if v := checkSize(t, h, 120, 30); !strings.Contains(v, "Сотрудник") || !strings.Contains(v, "29 февраля") {
		t.Errorf("RU with picture:\n%s", v)
	}

	// The lesson pane (i) has no picture.
	h.Key("i")
	if _, _, rows := imageCells(p.View(120, 30)); rows != 0 {
		t.Errorf("lesson details show the picture")
	}
}

// The picture of the search hit shows while the profile is still loading;
// without images the pane is exactly as before.
func TestAvatarFromSearchHit(t *testing.T) {
	plain := func() string {
		ctx := uitest.Ctx(t)
		hit := api.Person{Type: "STAFF", FullName: "Kinderkneht Yana Anatolevna", Email: "yakinderknecht@hse.ru", Description: "Профессор", AvatarURL: "https://example.invalid/a.jpg"}
		tg, _ := ui.TargetFromPerson(hit)
		return uitest.New(t, NewTargetPage(ctx, tg)).View(120, 30)
	}
	before := plain()
	if strings.Contains(before, placeholderRune) {
		t.Fatal("images are off by default")
	}

	base := enableAvatars(t)
	ctx := uitest.Ctx(t)
	hit := api.Person{Type: "STAFF", FullName: "Slow Profile", Email: "slow@hse.ru", AvatarURL: base + "/hit.jpg"}
	tg, _ := ui.TargetFromPerson(hit)
	p := NewTargetPage(ctx, tg).(*targetPage)
	// Run only the avatar request (the profile never arrives).
	if cmd := p.requestAvatar(); cmd == nil {
		t.Fatal("no avatar request for the search hit")
	} else if _, ok := cmd().(avatar.LoadedMsg); !ok {
		t.Fatal("avatar request didn't load")
	}
	p.profLoad.Begin()
	raw := p.View(100, 20)
	if _, _, rows := imageCells(raw); rows != avatar.Rows {
		t.Errorf("search hit picture not shown:\n%s", ansi.Strip(raw))
	}
	h := &uitest.Harness{T: t, Page: p}
	checkSizes(t, h)

	// Groups and rooms never ask for pictures.
	g := NewTargetPage(ctx, ui.Target{Kind: config.KindGroup, Key: "1", Person: &api.Person{AvatarURL: base + "/g.jpg"}}).(*targetPage)
	if g.requestAvatar() != nil {
		t.Error("group asked for a picture")
	}
}
