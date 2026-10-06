package grades

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"hse-app-z/internal/api"
	"hse-app-z/internal/ui"
	"hse-app-z/internal/ui/uitest"
)

func newPage(t *testing.T) (*page, *uitest.Harness) {
	t.Helper()
	p := New(uitest.Ctx(t)).(*page)
	return p, uitest.New(t, p)
}

func checkSize(t *testing.T, view string, w, h int) {
	t.Helper()
	lines := strings.Split(view, "\n")
	if len(lines) > h {
		t.Errorf("view has %d lines, want ≤ %d", len(lines), h)
	}
	for i, l := range lines {
		if n := ansi.StringWidth(l); n > w {
			t.Errorf("line %d is %d cells wide (> %d): %q", i, n, w, l)
		}
	}
}

func checkSizes(t *testing.T, p ui.Page) {
	t.Helper()
	for _, sz := range [][2]int{{100, 30}, {60, 12}, {60, 10}, {120, 30}, {20, 5}} {
		checkSize(t, p.View(sz[0], sz[1]), sz[0], sz[1])
	}
}

func send(h *uitest.Harness, p *page, resp api.GradesResponse, err error) {
	h.Send(ui.Result[api.GradesResponse]{ID: p.id, Seq: p.load.Seq, Data: resp, Err: err})
}

func TestDefaultYear(t *testing.T) {
	p, h := newPage(t)
	v := h.View(100, 30)
	checkSizes(t, p)
	if p.curYear() != "2026/2027" || len(p.data.Items) != 16 {
		t.Fatalf("year %q items %d", p.curYear(), len(p.data.Items))
	}
	for _, want := range []string{"2026/2027 ‹y›", "Информационная безопасность · БИБ255, 2 course", "Module 1", "Not graded yet", "0/16 graded"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "‹p›") {
		t.Error("program hint shown with a single program")
	}
	// The first row is a header; the cursor must sit on a grade.
	if p.selected() == nil || p.rows[0].g != nil {
		t.Fatal("cursor not on a grade row")
	}
}

func TestYearSwitch(t *testing.T) {
	p, h := newPage(t)
	h.Key("y")
	if p.curYear() != "2025/2026" || len(p.data.Items) != 20 {
		t.Fatalf("after y: year %q items %d", p.curYear(), len(p.data.Items))
	}
	v := h.View(120, 30)
	t.Logf("grades 120×30:\n%s", v)
	for _, want := range []string{"2025/2026", "avg ", "60 cr", "Algebra and Geometry", "10 / 10 · 5 (excellent)", "pass"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q", want)
		}
	}
	checkSizes(t, p)

	// h/l go older/newer without wrapping.
	h.Key("h")
	if p.curYear() != "2025/2026" {
		t.Fatalf("h past the oldest year moved to %q", p.curYear())
	}
	if s := h.Statuses(); len(s) == 0 || !strings.Contains(s[len(s)-1], "No older") {
		t.Errorf("statuses %v", s)
	}
	h.Key("l")
	if p.curYear() != "2026/2027" || len(p.data.Items) != 16 {
		t.Fatalf("after l: year %q items %d", p.curYear(), len(p.data.Items))
	}
	h.Key("Y")
	if p.curYear() != "2025/2026" {
		t.Fatalf("Y wraps: got %q", p.curYear())
	}
}

func TestNavigationAndDetail(t *testing.T) {
	p, h := newPage(t)
	h.Key("y")
	first := p.selected()
	if first == nil || first.Discipline != "Algebra and Geometry" {
		t.Fatalf("first grade %+v", first)
	}
	h.Key("j")
	second := p.selected()
	if second == first {
		t.Fatal("j didn't move")
	}
	h.Key("G")
	if p.selected() == nil {
		t.Fatal("G landed on a header")
	}
	h.Key("g", "J", "J")
	if p.scroll.Offset < 0 {
		t.Fatal("bad scroll")
	}
	h.Key("j")
	if p.scroll.Offset != 0 {
		t.Error("scroll not reset on selection change")
	}
	// A grade with several lecturers, and one with a retake.
	pick := func(ok func(g *api.Grade) bool) string {
		for i, r := range p.rows {
			if r.g != nil && ok(r.g) {
				p.list.Select(i)
				return ansi.Strip(p.detail(50))
			}
		}
		t.Fatal("no matching grade in fixture")
		return ""
	}
	d := pick(func(g *api.Grade) bool { return strings.Contains(g.Lecturer, ",") })
	for _, want := range []string{"Lecturers", "Hours", "classroom of", "Module       1", "Date         Tue 28 Oct 2025"} {
		if !strings.Contains(d, want) {
			t.Errorf("detail lacks %q:\n%s", want, d)
		}
	}
	t.Logf("detail:\n%s", d)
	d = pick(func(g *api.Grade) bool { return g.RepassCount.V > 0 })
	if !strings.Contains(d, "Retakes      1") {
		t.Errorf("retake detail:\n%s", d)
	}
}

func TestActionMenu(t *testing.T) {
	p, h := newPage(t)
	h.Key("y", "enter")
	m, ok := h.LastMenu()
	if !ok || m.Title != "Algebra and Geometry" {
		t.Fatalf("menu %+v", m)
	}
	if !h.RunAction("copy") {
		t.Fatal("no copy action")
	}
	if s := h.Statuses(); len(s) == 0 || !strings.Contains(s[len(s)-1], "Algebra and Geometry — 10/10 (5, excellent)") {
		t.Errorf("statuses %v", h.Statuses())
	}
	if !h.RunAction("switch to 2026/2027") {
		t.Fatal("no year action")
	}
	if p.curYear() != "2026/2027" {
		t.Fatalf("year %q", p.curYear())
	}
	h.Key("c")
	if s := h.Statuses(); !strings.Contains(s[len(s)-1], "not graded yet") {
		t.Errorf("statuses %v", s)
	}
}

func TestEdgeCases(t *testing.T) {
	p, h := newPage(t)
	tr, fl := true, false
	weird := api.GradesResponse{
		Items: []api.Grade{
			{Discipline: "\x1b[31mInjected\x1b[0m " + strings.Repeat("Очень длинное название ", 10), Grade: &api.GradeValue{}},
			{Discipline: "", ModuleNum: "abc"},
			{Discipline: "Pass test", TypeRaw: "Зачёт", Grade: &api.GradeValue{Pass: &tr}, ModuleName: "Summer 5"},
			{Discipline: "Failed", Grade: &api.GradeValue{Pass: &fl}, ModuleNum: "0", RepassCount: api.NewNum(2)},
			{Discipline: "Ten", Grade: &api.GradeValue{TenPoint: api.NewNum(4)}, Credits: api.NewNum(3), PeriodCredits: api.NewNum(1)},
		},
	}
	send(h, p, weird, nil)
	if len(p.rows) == 0 {
		t.Fatal("no rows")
	}
	checkSizes(t, p)
	for i := range p.rows {
		p.list.Select(i)
		checkSizes(t, p)
	}
	v := h.View(100, 30)
	if strings.Contains(v, "\x1b[31m") {
		t.Error("escape sequence leaked")
	}
	for _, want := range []string{"Other", "Module 5", "pass", "fail", "avg 4"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}

	// Totally empty response: no years, no programs, no items.
	send(h, p, api.GradesResponse{}, nil)
	checkSizes(t, p)
	v = h.View(100, 30)
	if !strings.Contains(v, "No grades for") {
		t.Errorf("empty view:\n%s", v)
	}
	// The known years are kept, so the year keys still work.
	h.Key("y", "h", "l", "p", "enter", "c", "x", "j", "J")
	checkSizes(t, p)

	// Empty year.
	send(h, p, api.GradesResponse{SelectedAcademicYear: "2027/2028", AvailableAcademicYears: []string{"2027/2028"}}, nil)
	if v := h.View(100, 30); !strings.Contains(v, "No grades for 2027/2028 yet") {
		t.Errorf("empty year view:\n%s", v)
	}

	// Error with no data → retry hint; r reloads from fixtures.
	p2, h2 := newPage(t)
	p2.load = ui.Load{}
	p2.data = api.GradesResponse{}
	p2.rebuild()
	p2.load.Begin()
	send(h2, p2, api.GradesResponse{}, errors.New("boom"))
	v = h2.View(100, 30)
	if !strings.Contains(v, "boom") || !strings.Contains(v, "press r to retry") {
		t.Errorf("error view:\n%s", v)
	}
	checkSizes(t, p2)
	h2.Key("r")
	if len(p2.data.Items) != 16 {
		t.Fatalf("retry loaded %d items", len(p2.data.Items))
	}

	// Stale result from an older request is ignored.
	seq := p2.load.Seq
	h2.Send(ui.Result[api.GradesResponse]{ID: p2.id, Seq: seq - 1, Data: api.GradesResponse{}})
	h2.Send(ui.Result[api.GradesResponse]{ID: p2.id + 1000, Seq: seq, Data: api.GradesResponse{}})
	if len(p2.data.Items) != 16 {
		t.Fatal("foreign/stale result applied")
	}

	// Reload keeps the year and the selection.
	h2.Key("y", "j", "j")
	sel := p2.selID
	h2.Send(ui.ReloadMsg{})
	if p2.curYear() != "2025/2026" || p2.selID != sel {
		t.Errorf("reload: year %q sel %q→%q", p2.curYear(), sel, p2.selID)
	}
}

func TestPrograms(t *testing.T) {
	p, h := newPage(t)
	resp := p.data
	resp.AvailablePrograms = append(resp.AvailablePrograms, api.Program{ID: "000999", Name: "Second program", Description: "Minor"})
	send(h, p, resp, nil)
	if v := h.View(120, 30); !strings.Contains(v, "‹p›") {
		t.Errorf("no program hint:\n%s", v)
	}
	seq := p.load.Seq
	h.Key("p")
	// A new request went out (the fixture answers with the main program,
	// which the page then trusts as the server's selection).
	if p.load.Seq != seq+1 || len(p.data.Items) != 16 {
		t.Fatalf("seq %d→%d, items %d", seq, p.load.Seq, len(p.data.Items))
	}
	send(h, p, resp, nil)
	h.Key("enter")
	if !h.RunAction("switch program to second") {
		t.Fatal("no program action")
	}
	checkSizes(t, p)
}

func TestBanners(t *testing.T) {
	p, h := newPage(t)
	tr, fl := true, false
	h.Send(ui.Result[[]api.Banner]{ID: p.id, Seq: p.banner.Load.Seq, Data: []api.Banner{
		{ID: "off", Title: "Disabled", IsEnabled: &fl},
		{ID: "b1", Title: "Grades are being updated", Description: strings.Repeat("Some long description ", 10), IsDismissible: true, IsEnabled: &tr},
		{ID: "b2", Title: "Second banner"},
	}})
	v := h.View(100, 30)
	if !strings.Contains(v, "! Grades are being updated") || !strings.Contains(v, "x hide") || strings.Contains(v, "Disabled") {
		t.Errorf("banner view:\n%s", v)
	}
	checkSizes(t, p)
	h.Key("x")
	if !p.ctx.Settings.BannerDismissed("b1") {
		t.Error("banner not dismissed")
	}
	v = h.View(100, 30)
	if !strings.Contains(v, "Second banner") || strings.Contains(v, "x hide") {
		t.Errorf("after dismiss:\n%s", v)
	}
	h.Key("x") // not dismissible: no-op
	if !strings.Contains(h.View(100, 30), "Second banner") {
		t.Error("non-dismissible banner hidden")
	}
	// Banner errors are silent.
	h.Send(ui.Result[[]api.Banner]{ID: p.id, Seq: p.banner.Load.Seq, Err: errors.New("banner boom")})
	if strings.Contains(h.View(100, 30), "boom") {
		t.Error("banner error shown")
	}
	checkSizes(t, p)
}
