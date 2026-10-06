package search

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"hse-app-z/internal/api"
	"hse-app-z/internal/config"
	"hse-app-z/internal/ui"
)

// englishUI are words this package writes itself; none may show up in the
// Russian interface. (Fixture data is partly English — "Student",
// "Employee", names — so data words are not listed.)
var englishUI = regexp.MustCompile(`\b(Search|Searching|searching|Results|results|result|Quick open|Favourites|favourites|Star|star|unstar|Starred|press|type|characters|matches|Try|Room|Group|Staff|Person|Course|course|Program|Details|Building|Email|email|Birthday|timetable|copy|map|leave|dismiss|Rate limited|Too many|no name|Your own|jumps|anywhere|open|retry|room|group)\b`)

func setRU(t *testing.T) {
	t.Helper()
	ui.SetLang("ru")
	t.Cleanup(func() { ui.SetLang("en") })
}

func noEnglish(t *testing.T, where, text string) {
	t.Helper()
	if m := englishUI.FindAllString(text, -1); len(m) > 0 {
		t.Errorf("%s: English UI words %q in:\n%s", where, m, text)
	}
}

func TestRussianInterface(t *testing.T) {
	setRU(t)
	p, h, ctx := newPage(t)
	if p.Title() != "Поиск" {
		t.Errorf("title %q", p.Title())
	}
	views := map[string]string{}
	render := func(name string) {
		t.Helper()
		for _, sz := range [][2]int{{100, 30}, {60, 12}} {
			key := fmt.Sprintf("%s %dx%d", name, sz[0], sz[1])
			views[key] = checkSize(t, p, sz[0], sz[1])
		}
		for _, hn := range p.Hints() {
			noEnglish(t, name+" hint", hn.Desc)
		}
	}
	lastStatus := func() string {
		st := h.Statuses()
		if len(st) == 0 {
			return ""
		}
		return st[len(st)-1]
	}

	// The notice: its text is (English) data, the dismiss hint is ours.
	h.Key("esc")
	if v := h.View(100, 30); !strings.Contains(v, "x скрыть") || strings.Contains(v, "dismiss") {
		t.Errorf("banner hint not translated:\n%s", v)
	}
	for _, b := range p.banners {
		ctx.Settings.DismissBanner(bannerKey(b))
	}
	h.Key("i")

	v := h.View(100, 30)
	for _, want := range []string{"ФИО, почта, группа (БИБ255) или аудитория…", "Быстрый доступ", "Я · Demo Student", "Избранное",
		"Добавляйте людей, группы и", "enter результаты · esc выйти"} {
		if !strings.Contains(v, want) {
			t.Errorf("home lacks %q:\n%s", want, v)
		}
	}
	render("home focused")
	t.Logf("home 100x30:\n%s", v)

	h.Key("esc")
	if v := h.View(100, 30); !strings.Contains(v, "Ваше расписание") || !strings.Contains(v, "Почта") {
		t.Errorf("Me preview:\n%s", v)
	}
	render("home list")

	h.Key("i", "R")
	if v := h.View(100, 30); !strings.Contains(v, "введите от 2 символов") {
		t.Errorf("short-query note:\n%s", v)
	}
	render("short query")

	h.Key("ezn", "enter")
	v = h.View(100, 30)
	for _, want := range []string{"Результаты", "12 результатов", "Сотрудник · Employee", "СОТ  Reznichenko", "enter расписание", "y копировать почту"} {
		if !strings.Contains(v, want) {
			t.Errorf("results lack %q:\n%s", want, v)
		}
	}
	render("results")
	t.Logf("results 100x30:\n%s", v)

	h.Key(".")
	m, ok := h.LastMenu()
	if !ok {
		t.Fatal("no action menu")
	}
	var labels []string
	for _, a := range m.Actions {
		labels = append(labels, a.Label)
	}
	if got := strings.Join(labels, " | "); got != "Открыть расписание | Добавить в избранное | Копировать почту" {
		t.Errorf("menu labels %q", got)
	}
	h.Key("y")
	if st := lastStatus(); !strings.Contains(st, "почта") {
		t.Errorf("copy status %q", st)
	}
	h.Key("f")
	if v := h.View(100, 30); !strings.Contains(v, "В избранном") || !strings.Contains(v, "f убрать") {
		t.Errorf("starred preview:\n%s", v)
	}
	render("starred")
	h.Key("f")

	// A group and a room.
	h.Key("i", "ctrl+u", "БИБ", "enter")
	for i, r := range p.rows {
		if r.person.IsGroup() {
			p.list.Select(i)
			break
		}
	}
	v = h.View(100, 30)
	for _, want := range []string{"Группа", "Программа", "Описание", "y копировать ID группы"} {
		if !strings.Contains(v, want) {
			t.Errorf("group preview lacks %q:\n%s", want, v)
		}
	}
	render("group")
	h.Key("y")
	if st := lastStatus(); !strings.Contains(st, "ID группы") {
		t.Errorf("copy group status %q", st)
	}
	h.Key("i", "ctrl+u", "R6", "enter")
	v = h.View(100, 30)
	for _, want := range []string{"Аудитория R615", "Здание", "m открыть карту", "m карта"} {
		if !strings.Contains(v, want) {
			t.Errorf("room preview lacks %q:\n%s", want, v)
		}
	}
	render("room")
	t.Logf("room 100x30:\n%s", v)
	h.Key(".")
	if m, _ := h.LastMenu(); len(m.Actions) != 4 || m.Actions[3].Label != "Открыть карту" || m.Actions[2].Label != "Копировать ID аудитории" {
		t.Errorf("room menu %+v", m.Actions)
	}

	// Hits without email, without a type, a room without location.
	h.Key("i", "ctrl+u", "ab", "esc")
	h.Send(ui.Result[hits]{ID: p.id, Seq: p.load.Begin(), Data: hits{q: "ab", items: []api.Person{
		{FullName: "Без Почты", Type: "STUDENT", BirthDate: "0000-10-13"},
		{Type: "", ID: "x1", Email: "x@hse.ru", BirthDate: "2001-05-02"},
		{Type: "AUDITORIUM", ID: "r1", Room: "101"},
		{Type: "GROUP", ID: "g1", Label: "ГР1", Course: api.Num{V: 2, OK: true}},
	}}})
	v = h.View(100, 30)
	for _, want := range []string{"Нет почты — у этой записи нет расписания", "13 октября"} {
		if !strings.Contains(v, want) {
			t.Errorf("no-email preview lacks %q:\n%s", want, v)
		}
	}
	render("odd hits")
	h.Key("enter")
	if st := lastStatus(); !strings.Contains(st, "нет почты") {
		t.Errorf("no-email status %q", st)
	}
	h.Key("f")
	if st := lastStatus(); !strings.Contains(st, "Нельзя добавить в избранное") {
		t.Errorf("no-email star status %q", st)
	}
	h.Key("m")
	if st := lastStatus(); st != "Карта есть только у аудиторий" {
		t.Errorf("map status %q", st)
	}
	h.Key("j")
	if v := h.View(100, 30); !strings.Contains(v, "Человек") || !strings.Contains(v, "2 мая 2001") {
		t.Errorf("typeless hit:\n%s", v)
	}
	render("typeless")
	h.Key("j", "m")
	if st := lastStatus(); st != "Местоположение аудитории неизвестно" {
		t.Errorf("room map status %q", st)
	}
	h.Key("j")
	if v := h.View(100, 30); !strings.Contains(v, "2 курс") || !strings.Contains(v, "Курс") {
		t.Errorf("group course:\n%s", v)
	}
	render("group course")

	// No matches, errors, rate limiting.
	h.Key("i", "ctrl+u", "zzzz")
	if v := h.View(100, 30); !strings.Contains(v, "По запросу «zzzz» ничего не найдено") || !strings.Contains(v, "0 результатов") {
		t.Errorf("no matches:\n%s", v)
	}
	render("no matches")
	h.Send(ui.Result[hits]{ID: p.id, Seq: p.load.Begin(), Err: &api.NetworkError{Err: errString("offline")}})
	if v := h.View(100, 30); !strings.Contains(v, "нажмите enter, чтобы повторить") {
		t.Errorf("error state:\n%s", v)
	}
	render("error")
	h.Key("esc")
	if v := h.View(100, 30); !strings.Contains(v, "нажмите r, чтобы повторить") {
		t.Errorf("error state (list):\n%s", v)
	}
	p.Update(ui.Result[hits]{ID: p.id, Seq: p.load.Begin(), Err: &api.RateLimitError{}})
	if v := h.View(100, 30); !strings.Contains(v, "Слишком много запросов") {
		t.Errorf("rate limited:\n%s", v)
	}
	render("rate limited")

	// The tips pane (nothing selectable).
	ctx.Me = ui.Me{}
	ctx.Favs = config.LoadFavourites("")
	p.server = nil
	p.query, p.results = "", nil
	h.Key("i", "ctrl+u")
	if v := h.View(100, 30); !strings.Contains(v, "Поиск по университету") {
		t.Errorf("tips:\n%s", v)
	}
	render("tips")

	for name, v := range views {
		noEnglish(t, name, v)
	}

	// Switching back takes effect on the next frame.
	ui.SetLang("en")
	if v := h.View(100, 30); !strings.Contains(v, "Search the university") || !strings.Contains(v, "name, email, group") {
		t.Errorf("English after switching back:\n%s", v)
	}
}
