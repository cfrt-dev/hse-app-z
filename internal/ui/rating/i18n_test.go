package rating

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"hse-app-z/internal/api"
	"hse-app-z/internal/ui"
)

// setRU switches the interface to Russian for one test. The language is
// process-wide: tests calling it must not run in parallel.
func setRU(t *testing.T) {
	t.Helper()
	ui.SetLang("ru")
	t.Cleanup(func() { ui.SetLang("en") })
}

// englishWords are English UI words this package itself writes (API data
// is replaced by Russian text in these tests, see russify).
var englishWords = []string{
	"All types", "CUR", "CUM", "RET", "MIN", "Current", "Cumulative", "After retakes", "Minor", "Other",
	"rating", "ratings", "of", "modules", "module", "top", "place", "percentile", "in program",
	"Published", "Period", "Program", "Faculty", "Degree", "course",
	"Bachelor", "Master", "Specialist", "Postgraduate", "Moscow",
	"Places", "Scores", "Exams", "Disciplines", "Overall", "In program", "In course", "In group",
	"Percentile", "Rating", "normalized", "Average", "Minimum", "Minor grade", "Credits", "precise",
	"Total", "By grade", "Retakes", "none", "passed", "grade", "cr", "Current year", "No ratings",
	"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun",
	"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec",
}

func hasHintDesc(hs []ui.Hint, desc string) bool {
	for _, h := range hs {
		if h.Desc == desc {
			return true
		}
	}
	return false
}

// leftovers returns the words found in s as whole words.
func leftovers(s string, words []string) []string {
	var out []string
	for _, w := range words {
		if regexp.MustCompile(`\b` + regexp.QuoteMeta(w) + `\b`).MatchString(s) {
			out = append(out, w)
		}
	}
	return out
}

func checkRU(t *testing.T, what, s string) {
	t.Helper()
	if l := leftovers(s, englishWords); len(l) > 0 {
		t.Errorf("%s: English words %q left in:\n%s", what, l, s)
	}
}

var titleRU = map[string]string{
	"current": "Текущий рейтинг за 2025/2026 учебный год",
	"cumul":   "Накопленный рейтинг за 2025/2026 учебный год",
	"retake":  "Рейтинг после пересдач за 2025/2026 учебный год",
	"minor":   "Рейтинг по майнору за 2025/2026 учебный год",
}

// russify replaces the (partly English) fixture data with Russian text, so
// any English left in a view comes from this package.
func russify(resp api.RatingsResponse) api.RatingsResponse {
	out := resp
	out.Items = append([]api.Rating(nil), resp.Items...)
	for i := range out.Items {
		r := &out.Items[i]
		mods := "(1-2 модули)"
		if strings.Contains(r.Title, "3-4") {
			mods = "(3-4 модули)"
		}
		r.Title = titleRU[normType(r.Type)] + " " + mods
		r.LearnProgram = "Информационная безопасность"
		r.DisciplineList = append([]api.RatingDiscipline(nil), r.DisciplineList...)
		for j := range r.DisciplineList {
			r.DisciplineList[j].Discipline = fmt.Sprintf("Дисциплина №%d", j+1)
		}
	}
	out.AvailablePrograms = append([]api.Program(nil), resp.AvailablePrograms...)
	for i := range out.AvailablePrograms {
		out.AvailablePrograms[i].Title = "Информационная безопасность"
		out.AvailablePrograms[i].Description = "БИБ255, 2 курс"
	}
	return out
}

// renderAllRU selects every rating and checks each view at both sizes,
// scrolled to the top and to the bottom of the detail pane.
func renderAllRU(t *testing.T, p *page, what string) {
	t.Helper()
	for i := range p.items {
		p.list.Select(i)
		for _, sz := range [][2]int{{100, 30}, {60, 12}} {
			for _, off := range []int{0, 1000} {
				p.scroll.Offset = off
				v := ansi.Strip(p.View(sz[0], sz[1]))
				checkSize(t, v, sz[0], sz[1])
				checkRU(t, fmt.Sprintf("%s item %d %dx%d+%d", what, i, sz[0], sz[1], off), v)
			}
		}
		p.scroll.Reset()
		checkRU(t, fmt.Sprintf("%s detail %d", what, i), ansi.Strip(p.detail(60)))
		checkRU(t, fmt.Sprintf("%s summary %d", what, i), summary(*p.items[i]))
	}
	p.list.Select(0)
	checkSizes(t, p)
}

func TestRussian(t *testing.T) {
	setRU(t)
	p, h := newPage(t)
	if p.Title() != "Рейтинг" {
		t.Errorf("title %q", p.Title())
	}
	send(h, p, russify(p.data), nil)
	v := h.View(100, 30)
	t.Logf("rating RU 100×30:\n%s", v)
	for _, want := range []string{
		"2025/2026", "Все типы ‹t›", "5 рейтингов", "ТЕК Текущий · модули 3–4", "топ 54%",
		"ПЕР После пересдач · модули 1–2", "НАК Накопленный", "МНР Майнор",
		"Публикация   Чт 16 июл 2026", "2025/2026 · модуль 4", "Бакалавриат · Москва",
		"Места", "Общий", "В программе", "На курсе", "Процентиль   54", "Средняя",
		"По оценкам   8–10: 4", "Пересдачи    1 (сдано 0)",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q", want)
		}
	}
	renderAllRU(t, p, "fixture")
	t.Logf("rating RU 60×12:\n%s", h.View(60, 12))
	d := ansi.Strip(p.detail(60))
	for _, want := range []string{"Баллы", "Экзамены", "Дисциплины", "оценка кр.", "топ ", "БИБ255 · 1\u00a0курс"} {
		if !strings.Contains(d, want) {
			t.Errorf("detail lacks %q:\n%s", want, d)
		}
	}

	// Type filter, list title and the menu.
	h.Key("t")
	if !strings.Contains(h.View(100, 30), "Рейтинги: 2 из 5") || !strings.Contains(h.View(100, 30), "Текущий ‹t›") {
		t.Errorf("filtered view:\n%s", h.View(100, 30))
	}
	h.Key("t", "t", "t", "t")
	if p.typ != "" {
		t.Fatalf("filter %q", p.typ)
	}
	h.Key("g", "enter")
	m, ok := h.LastMenu()
	if !ok || m.Title != "Текущий · модули 3–4" {
		t.Fatalf("menu %+v", m)
	}
	for _, a := range m.Actions {
		checkRU(t, "menu", a.Label)
		if l := leftovers(a.Label, []string{"Copy", "summary", "Show", "only", "all"}); len(l) > 0 {
			t.Errorf("menu label %q", a.Label)
		}
	}
	if !h.RunAction("копировать сводку") {
		t.Fatal("no copy action")
	}
	if s := h.Statuses(); !strings.Contains(s[len(s)-1], "место 14191/26050, 247/453 в программе, процентиль 54, GPA 6.81") {
		t.Errorf("copied %q", s[len(s)-1])
	}
	if !h.RunAction("показать только: текущий") || p.typ != "current" {
		t.Fatalf("filter action: %q", p.typ)
	}
	h.Key("enter")
	if !h.RunAction("показать все") || p.typ != "" {
		t.Fatalf("show all: %q", p.typ)
	}
	h.Key("y")
	if s := h.Statuses(); s[len(s)-1] != "Других учебных лет нет" {
		t.Errorf("status %q", s[len(s)-1])
	}
	for _, hint := range p.Hints() {
		if regexp.MustCompile(`[A-Za-z]`).MatchString(hint.Desc) {
			t.Errorf("hint %q: %q", hint.Key, hint.Desc)
		}
	}

	// Hand-made data: every degree, unknown types, normalized ratings,
	// precise credits, a title without a module range.
	var items []api.Rating
	for i, deg := range []string{"DEGREE_BACHELOR", "DEGREE_MASTER", "DEGREE_SPECIALIST", "DEGREE_POSTGRADUATE"} {
		items = append(items, api.Rating{
			Type: []string{"current", "cumul", "retake", "minor"}[i], Degree: deg, Campus: "CAMPUS_SPB",
			Title: "Рейтинг (3 модуль)", Rating: api.NewNum(80), RatingNorm: api.NewNum(75),
			GradeMin: api.NewNum(4), GradeMinor: api.NewNum(8), Credits: api.NewNum(30), CreditsPrecise: api.NewNum(30.5),
			ExamAll: api.NewNum(5), ExamRetake: api.NewNum(0), ExamB: api.NewNum(1),
			PlaceGroupOpCurr: api.NewNum(2), PlaceGroupOpTotal: api.NewNum(25),
		})
	}
	items = append(items,
		api.Rating{Type: "", LearnPeriod: "2024/2025 учебный год 1 модуль", RatingNorm: api.NewNum(70)},
		api.Rating{Type: "current"},
	)
	send(h, p, api.RatingsResponse{Items: items}, nil)
	renderAllRU(t, p, "edge")
	var all string
	for i := range p.items {
		p.list.Select(i)
		all += ansi.Strip(p.detail(70)) + "\n" + ansi.Strip(p.View(100, 30)) + "\n"
	}
	for _, want := range []string{
		"Бакалавриат · Санкт-Петербург", "Магистратура", "Специалитет", "Аспирантура",
		"· нормированный 75", "70 нормированный", "Минимум", "За майнор", "(точно 30.5)",
		"Всего", "Пересдачи    нет", "В группе", "Другое", "модуль 3", "Рейтинг: текущий", "2024/2025 · модуль 1",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("edge views lack %q", want)
		}
	}

	// Empty.
	send(h, p, api.RatingsResponse{}, nil)
	if v := h.View(100, 30); !strings.Contains(v, "По выбранным параметрам рейтингов нет") {
		t.Errorf("empty view:\n%s", v)
	}
	p.year = ""
	if sel := ansi.Strip(p.selectorLine(80)); !strings.Contains(sel, "Текущий год") || !strings.Contains(sel, "Все типы") {
		t.Errorf("selector without a year: %q", sel)
	}
	checkSizes(t, p)

	// The banner text is API data; its dismiss key is not.
	tr := true
	h.Send(ui.Result[[]api.Banner]{ID: p.id, Seq: p.banner.Load.Seq, Data: []api.Banner{
		{ID: "ru1", Title: "Идёт пересчёт", Description: "Данные могут меняться", IsDismissible: true, IsEnabled: &tr},
	}})
	if v := h.View(100, 30); !strings.Contains(v, "! Идёт пересчёт — Данные могут меняться") || strings.Contains(v, "hide") || !strings.Contains(v, "x скрыть") {
		t.Errorf("banner:\n%s", v)
	}
	if hs := p.Hints(); !hasHintDesc(hs, "скрыть объявление") {
		t.Errorf("hints %v", hs)
	}
	checkSizes(t, p)

	// Back to English on the next frame, without a reload.
	ui.SetLang("en")
	send(h, p, api.RatingsResponse{Items: []api.Rating{{Type: "current", Title: "Рейтинг (3-4 модули)"}}}, nil)
	if v := h.View(100, 30); !strings.Contains(v, "CUR Current · modules 3–4") || !strings.Contains(v, "All types") {
		t.Errorf("English after switching back:\n%s", v)
	}
}
