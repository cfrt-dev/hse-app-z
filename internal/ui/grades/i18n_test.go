package grades

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
	"Module", "Other", "avg", "cr", "graded", "Not graded", "not graded",
	"excellent", "good", "satisfactory", "unsatisfactory",
	"pass", "fail", "Pass", "Fail", "exam", "test",
	"Type", "Date", "Credits", "Hours", "Retakes", "none", "Lecturer", "Lecturers", "Year",
	"classroom", "total", "this period", "Current year", "No grades", "yet", "Untitled",
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

// russify replaces the (partly English) fixture data with Russian text, so
// any English left in a view comes from this package.
func russify(resp api.GradesResponse) api.GradesResponse {
	out := resp
	out.Items = append([]api.Grade(nil), resp.Items...)
	for i := range out.Items {
		g := &out.Items[i]
		g.Discipline = fmt.Sprintf("Дисциплина №%d", i+1)
		switch g.TypeRaw {
		case "Exam":
			g.TypeRaw = "Экзамен"
		case "Test":
			g.TypeRaw = "Зачёт"
		}
		g.ModuleName = strings.ReplaceAll(g.ModuleName, "module", "модуль")
		if g.Lecturer != "" {
			n := strings.Count(g.Lecturer, ",") + 1
			var ls []string
			for j := 0; j < n; j++ {
				ls = append(ls, fmt.Sprintf("Преподаватель %d", j+1))
			}
			g.Lecturer = strings.Join(ls, ", ")
		}
	}
	out.AvailablePrograms = append([]api.Program(nil), resp.AvailablePrograms...)
	for i := range out.AvailablePrograms {
		out.AvailablePrograms[i].Description = strings.ReplaceAll(out.AvailablePrograms[i].Description, "course", "курс")
	}
	return out
}

func stripANSI(s string) string { return ansi.Strip(s) }

func stripView(p *page, w, h int) string { return ansi.Strip(p.View(w, h)) }

// renderAllRU selects every row and checks each view at both sizes.
func renderAllRU(t *testing.T, p *page, what string) {
	t.Helper()
	for i := range p.rows {
		if p.rows[i].g == nil {
			continue
		}
		p.list.Select(i)
		p.syncSelection()
		for _, sz := range [][2]int{{100, 30}, {60, 12}} {
			v := stripView(p, sz[0], sz[1])
			checkSize(t, v, sz[0], sz[1])
			checkRU(t, fmt.Sprintf("%s row %d %dx%d", what, i, sz[0], sz[1]), v)
		}
		// The whole detail, not just what fits on screen.
		checkRU(t, fmt.Sprintf("%s detail %d", what, i), stripANSI(p.detail(60)))
	}
	checkSizes(t, p)
}

func TestRussian(t *testing.T) {
	setRU(t)
	p, h := newPage(t)
	if p.Title() != "Оценки" {
		t.Errorf("title %q", p.Title())
	}

	// Default year: nothing graded yet.
	send(h, p, russify(p.data), nil)
	v := h.View(100, 30)
	for _, want := range []string{"2026/2027 ‹y›", "Модуль 1", "Оценки ещё нет", "0/16 с оценкой", "БИБ255, 2 курс"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	renderAllRU(t, p, "2026/2027")

	// The graded year.
	h.Key("y")
	send(h, p, russify(p.data), nil)
	p.list.Select(1)
	p.syncSelection()
	v = h.View(100, 30)
	t.Logf("grades RU 100×30:\n%s", v)
	for _, want := range []string{"2025/2026", "ср. ", "60 кр.", "/ 10 · 5 (отлично)", "экз", "Модуль       1", "Лекторы", "аудиторных из"} {
		if !strings.Contains(v, want) {
			t.Errorf("graded view lacks %q", want)
		}
	}
	renderAllRU(t, p, "2025/2026")
	t.Logf("grades RU 60×12:\n%s", h.View(60, 12))

	// Statuses and the menu.
	h.Key("h")
	if s := h.Statuses(); len(s) == 0 || s[len(s)-1] != "Более ранних учебных лет нет" {
		t.Errorf("statuses %q", s)
	}
	p.list.Select(1)
	p.syncSelection()
	h.Key("enter")
	m, ok := h.LastMenu()
	if !ok || len(m.Actions) < 2 {
		t.Fatalf("menu %+v", m)
	}
	for _, a := range m.Actions {
		checkRU(t, "menu", a.Label)
		if l := leftovers(a.Label, []string{"Copy", "Switch", "program", "grade", "discipline"}); len(l) > 0 {
			t.Errorf("menu label %q", a.Label)
		}
	}
	if !h.RunAction("копировать") {
		t.Fatal("no copy action")
	}
	if s := h.Statuses(); !strings.Contains(s[len(s)-1], "— 10/10 (5, отлично)") {
		t.Errorf("copied %q", s[len(s)-1])
	}
	for _, hint := range p.Hints() {
		if regexp.MustCompile(`[A-Za-z]`).MatchString(hint.Desc) {
			t.Errorf("hint %q: %q", hint.Key, hint.Desc)
		}
	}

	// Hand-made edge cases: pass/fail, named and missing modules, credits
	// of the period, retakes.
	tr, fl := true, false
	send(h, p, api.GradesResponse{SelectedAcademicYear: "2026/2027", Items: []api.Grade{
		{ID: "1", Discipline: "Физкультура", TypeRaw: "Зачёт", Grade: &api.GradeValue{Pass: &tr}, ModuleName: "Летняя школа"},
		{ID: "2", Discipline: "Провал", Grade: &api.GradeValue{Pass: &fl}, ModuleNum: "0", RepassCount: api.NewNum(2)},
		{ID: "3", Discipline: "Десять", Grade: &api.GradeValue{TenPoint: api.NewNum(4)}, Credits: api.NewNum(3), PeriodCredits: api.NewNum(1), RepassCount: api.NewNum(0), EntireHours: api.NewNum(30)},
		{ID: "4", Discipline: "", AudHours: api.NewNum(12)},
	}}, nil)
	v = h.View(100, 30)
	for _, want := range []string{"Другое", "Летняя школа", "зач", "н/з", "ср. 4", "3/4 с оценкой"} {
		if !strings.Contains(v, want) {
			t.Errorf("edge view lacks %q:\n%s", want, v)
		}
	}
	renderAllRU(t, p, "edge")
	details := ""
	for i := range p.rows {
		if p.rows[i].g != nil {
			p.list.Select(i)
			details += stripANSI(p.detail(70)) + "\n"
		}
	}
	for _, want := range []string{"Зачёт", "Незачёт", "4 / 10 · 3 (удовлетворительно)", "Пересдачи    2", "Пересдачи    нет", "(1 в этом периоде)", "30 всего", "12 аудиторных", "Дисциплина без названия", "Модуль       Летняя школа"} {
		if !strings.Contains(details, want) {
			t.Errorf("edge details lack %q:\n%s", want, details)
		}
	}

	// Empty selections.
	send(h, p, api.GradesResponse{SelectedAcademicYear: "2027/2028", AvailableAcademicYears: []string{"2027/2028"}}, nil)
	if v := h.View(100, 30); !strings.Contains(v, "Оценок за 2027/2028 пока нет") {
		t.Errorf("empty view:\n%s", v)
	}
	p.year, p.data = "", api.GradesResponse{}
	if p.emptyText() != "Оценок пока нет" || !strings.Contains(p.selectorLine(80), "Текущий год") {
		t.Errorf("no year: %q / %q", p.emptyText(), stripANSI(p.selectorLine(80)))
	}
	checkSizes(t, p)

	// The banner text is API data; its dismiss key is not.
	tr = true
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
	send(h, p, russify(api.GradesResponse{SelectedAcademicYear: "2026/2027", Items: []api.Grade{{ID: "1", ModuleNum: "1", Discipline: "X"}}}), nil)
	if v := h.View(100, 30); !strings.Contains(v, "Module 1") || !strings.Contains(v, "Not graded yet") {
		t.Errorf("English after switching back:\n%s", v)
	}
}
