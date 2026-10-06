package campus

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"hse-app-z/internal/api"
	"hse-app-z/internal/ui"
	"hse-app-z/internal/ui/uitest"
)

// englishUI are words this package (or the open-status labels it shows)
// writes itself. Addresses and building names in the fixture are English
// data ("Moscow, …", "St. Petersburg, building …"), so city names are
// checked where they are ours (headers, filters) instead.
var englishUI = regexp.MustCompile(`\b(Buildings|Campus|campus|All|Address|Location|open|closed|until|opens|since|today|tomorrow|Cafés|cafés|café|Café|Libraries|library|libraries|Library|Office|phone|ext|Sanitary|schedule|daily|from|map|switch|here|24h|Mon|Tue|Wed|Thu|Fri|Sat|Sun|cleaning|month|Other|n/a|actions|refresh|scroll|filter|copy|week)\b`)

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

func wantAll(t *testing.T, where, v string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(v, w) {
			t.Errorf("%s lacks %q:\n%s", where, w, v)
		}
	}
}

func lastStatus(h *uitest.Harness) string {
	st := h.Statuses()
	if len(st) == 0 {
		return ""
	}
	return st[len(st)-1]
}

func TestRussianInterface(t *testing.T) {
	setRU(t)
	p, h := newPage(t)
	views := map[string]string{}
	render := func(name string) string {
		t.Helper()
		var big string
		for _, sz := range [][2]int{{100, 30}, {60, 12}} {
			v := h.View(sz[0], sz[1])
			checkSize(t, p.View(sz[0], sz[1]), sz[0], sz[1])
			views[fmt.Sprintf("%s %dx%d", name, sz[0], sz[1])] = v
			if big == "" {
				big = v
			}
		}
		views[name+" title"] = p.Title()
		for _, hn := range p.Hints() {
			views[name+" hint "+hn.Key] = hn.Desc
		}
		return big
	}
	if p.Title() != "Кампус" {
		t.Errorf("title %q", p.Title())
	}

	selectBuilding(t, p, "Pokrovsky B., h.11")
	v := render("pokrovsky")
	wantAll(t, "list", v, "Здания", "кампус: Все · c — сменить", " Москва", "7 кафе · библиотека", "Кампус       Москва",
		"Координаты", "m открыть карту", "Кафе", "Библиотеки")
	t.Logf("campus 100x30:\n%s", v)
	if wide := h.View(200, 40); !strings.Contains(wide, "Все │ Москва │ Нижний Новгород │ Пермь │ Санкт-Петербург") {
		t.Errorf("filter tabs:\n%s", wide)
	}
	d := ansi.Strip(p.detail(*p.selected(), 80))
	views["pokrovsky detail"] = d
	wantAll(t, "detail", d,
		"3, 4 этаж: открыто · до 21:00 · сегодня 10:00–21:00",
		"Пн–Пт 10:00–21:00, Сб 10:00–18:00, Вс закрыто",
		"2 этаж: открыто круглосуточно",
		"ежедневно круглосуточно",
		"открыто · до 20:00 · сегодня 12:00–20:00",
		"тел. +7 495 916-89-27",
		"Санитарный день — в первую пятницу месяца",
		"Столовая на Покровке",
		"открыто · до 21:00 · сегодня 08:30–21:00")

	selectBuilding(t, p, "Myasnitskaya ul., h.20")
	d = ansi.Strip(p.detail(*p.selected(), 90))
	views["myasnitskaya detail"] = d
	wantAll(t, "Myasnitskaya", d,
		"Необходимо предварительное бронирование книг: открыто · до 20:00",
		"Пн закрыто, Вт 12:00–20:00, Ср закрыто, Чт 12:00–20:00, Пт–Вс закрыто",
		"+7 495 772-95-90 доб. 15571")
	render("myasnitskaya")

	// Filters, menu, statuses.
	for _, want := range []string{"Москва", "Нижний Новгород", "Пермь", "Санкт-Петербург", "Все"} {
		h.Key("c")
		if p.filterName() != want {
			t.Fatalf("filter %q, want %q", p.filterName(), want)
		}
		render("filter " + want)
	}
	selectBuilding(t, p, "MIEM")
	h.Key("enter")
	m, ok := h.LastMenu()
	if !ok || len(m.Actions) != 2 || m.Actions[0].Label != "Открыть на карте" || m.Actions[1].Label != "Копировать адрес" {
		t.Errorf("menu %+v", m.Actions)
	}
	h.Key("y")
	if st := lastStatus(h); !strings.HasPrefix(st, "Скопировано: адрес") {
		t.Errorf("copy status %q", st)
	}

	// Odd data: no campus, no names, no location, no schedule.
	weird := []api.CampusGroup{
		{Campus: "", Buildings: []api.Building{
			{ID: "1", Name: "Пустое здание"},
			{ID: "2", Cafes: []api.Cafe{{}}, LibrariesV3: []api.Library{{Offices: []api.LibraryOffice{{
				Rules:        []api.LibraryRule{{Name: "Зал", Schedule: nil}, {Schedule: &api.LibrarySchedule{SanitaryDay: "SANITARY_DAY_LAST_MONDAY"}}},
				PhoneNumbers: []string{",15241"},
			}}}}},
		}},
		{Campus: "CAMPUS_PERM", Buildings: []api.Building{{ID: "3", Name: "Корпус в Перми", Campus: "CAMPUS_PERM"}}},
	}
	h.Send(ui.Result[[]api.CampusGroup]{ID: p.id, Seq: p.load.Seq, Data: weird})
	h.Key("g")
	v = render("weird")
	wantAll(t, "weird list", v, " Пермь", " Другое", "Здание 2", "1 кафе · библиотека")
	d = render("weird 2")
	wantAll(t, "weird detail", d, "Здание 2", "Кампус       Другое", "Кафе", "нет расписания")
	d = ansi.Strip(p.detail(*p.selected(), 80))
	views["weird detail"] = d
	wantAll(t, "weird detail", d, "Библиотека", "Отдел", "нет расписания", "доб. 15241", "Санитарный день — в последний понедельник месяца")
	h.Key("m")
	if st := lastStatus(h); st != "Нет координат: Здание 2" {
		t.Errorf("map status %q", st)
	}
	h.Key("y")
	if st := lastStatus(h); st != "Нет адреса: Здание 2" {
		t.Errorf("copy status %q", st)
	}
	h.Key("enter")
	if st := lastStatus(h); st != "Нет действий: Здание 2" {
		t.Errorf("menu status %q", st)
	}
	h.Key("j")
	wantAll(t, "empty building", render("empty building"), "Здесь нет кафе и библиотек")

	h.Send(ui.Result[[]api.CampusGroup]{ID: p.id, Seq: p.load.Seq, Data: nil})
	wantAll(t, "empty", render("empty"), "Нет зданий")

	for name, v := range views {
		noEnglish(t, name, v)
	}
}

func TestRussianSchedules(t *testing.T) {
	setRU(t)
	oh := func(day string, open bool, s, e string) api.OpeningHours {
		return api.OpeningHours{DayOfWeek: day, IsOpen: open, StartTime: s, EndTime: e}
	}
	for _, c := range []struct {
		hours []api.OpeningHours
		want  string
	}{
		{nil, "закрыто всю неделю"},
		{[]api.OpeningHours{oh("mon", true, "", ""), oh("tue", true, "", ""), oh("wed", true, "", ""), oh("thu", true, "", ""),
			oh("fri", true, "", ""), oh("sat", true, "", ""), oh("sun", true, "", "")}, "ежедневно круглосуточно"},
		{[]api.OpeningHours{oh("saturday", true, "11:00", ""), oh("sunday", true, "", "18:00")},
			"Пн–Пт закрыто, Сб с 11:00, Вс до 18:00"},
	} {
		if got := Weekly(c.hours); got != c.want {
			t.Errorf("Weekly = %q, want %q", got, c.want)
		}
	}
	for in, want := range map[string]string{
		"+74957729590,15241": "+7 495 772-95-90 доб. 15241",
		",61291":             "доб. 61291",
		"+74959168927":       "+7 495 916-89-27",
	} {
		if got := FormatPhone(in); got != want {
			t.Errorf("FormatPhone(%q) = %q, want %q", in, got, want)
		}
	}
	if got := statusLine(api.OpenStatus{}); ansi.Strip(got) != "нет расписания" {
		t.Errorf("unknown status %q", ansi.Strip(got))
	}
	if got := HoursText(oh("monday", false, "", "")); got != "закрыто" {
		t.Errorf("HoursText %q", got)
	}
}
