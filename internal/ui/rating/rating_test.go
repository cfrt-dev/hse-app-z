package rating

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
		t.Errorf("%d×%d: view has %d lines", w, h, len(lines))
	}
	for i, l := range lines {
		if n := ansi.StringWidth(l); n > w {
			t.Errorf("%d×%d: line %d is %d cells wide: %q", w, h, i, n, l)
		}
	}
}

// checkSizes renders at several sizes, scrolled to the top and bottom of
// the detail pane.
func checkSizes(t *testing.T, p *page) {
	t.Helper()
	for _, sz := range [][2]int{{100, 30}, {60, 12}, {60, 10}, {120, 30}, {20, 5}} {
		p.scroll.Reset()
		checkSize(t, p.View(sz[0], sz[1]), sz[0], sz[1])
		p.scroll.Offset = 1000
		checkSize(t, p.View(sz[0], sz[1]), sz[0], sz[1])
	}
	p.scroll.Reset()
}

func send(h *uitest.Harness, p *page, resp api.RatingsResponse, err error) {
	h.Send(ui.Result[api.RatingsResponse]{ID: p.id, Seq: p.load.Seq, Data: resp, Err: err})
}

func TestRender(t *testing.T) {
	p, h := newPage(t)
	if len(p.items) != 5 {
		t.Fatalf("items %d", len(p.items))
	}
	v := h.View(120, 30)
	t.Logf("rating 120×30:\n%s", v)
	for _, want := range []string{
		"2025/2026", "All types ‹t›", "Information Security · БИБ255, 2 course",
		"CUR Current · modules 3–4", "top 54%", "After retakes · modules 1–2",
		"Thu 16 Jul 2026", "2025/2026 · module 4", "Bachelor · Moscow",
		"14191 / 26050", "247 / 453", "70 / 127", "Percentile   54",
		"GPA          6.81", "Average      6.73", "Credits      47",
		"By grade     8–10: 4 · 6–7: 4 · 4–5: 3 · 0–3: 0", "Retakes      1 (0 passed)",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q", want)
		}
	}
	if strings.Contains(v, "‹y›") {
		t.Error("year hint with a single year")
	}
	checkSizes(t, p)
	// Newest first.
	for i := 1; i < len(p.items); i++ {
		if p.items[i].PublishedAt.After(p.items[i-1].PublishedAt.Time) {
			t.Fatal("items not sorted newest first")
		}
	}
	// The disciplines table is below the fold: scroll down to it.
	h.Key(strings.Repeat("J", 40))
	v = h.View(100, 30)
	if !strings.Contains(v, "Discrete Mathematics") || !strings.Contains(v, "grade") {
		t.Errorf("disciplines not reachable:\n%s", v)
	}
	h.Key("j")
	if p.scroll.Offset != 0 {
		t.Error("scroll not reset")
	}
	if p.selected().Type != "minor" {
		t.Errorf("second item is %q", p.selected().Type)
	}
	d := ansi.Strip(p.detail(60))
	if !strings.Contains(d, "examb 1 · examg 6 · examn 1") {
		t.Errorf("other counters:\n%s", d)
	}
}

func TestDisciplineOrder(t *testing.T) {
	p, _ := newPage(t)
	d := ansi.Strip(disciplinesBlock(p.items[0].DisciplineList, 60))
	lines := strings.Split(d, "\n")
	if !strings.HasPrefix(lines[1], "Discrete Mathematics") {
		t.Errorf("highest grade not first:\n%s", d)
	}
	if last := lines[len(lines)-1]; !strings.HasPrefix(last, "Physics") {
		t.Errorf("unexpected last row %q", last)
	}
	for _, l := range lines {
		if ansi.StringWidth(l) > 60 {
			t.Errorf("row too wide: %q", l)
		}
	}
}

func TestTypeFilter(t *testing.T) {
	p, h := newPage(t)
	h.Key("t")
	if p.typ != "current" || len(p.items) != 2 {
		t.Fatalf("filter %q items %d", p.typ, len(p.items))
	}
	for _, r := range p.items {
		if r.Type != "current" {
			t.Fatalf("filtered list has %q", r.Type)
		}
	}
	v := h.View(100, 30)
	if !strings.Contains(v, "Current ‹t›") || !strings.Contains(v, "2 of 5 ratings") || strings.Contains(v, "MIN") {
		t.Errorf("filtered view:\n%s", v)
	}
	order := []string{"cumul", "retake", "minor", ""}
	for _, want := range order {
		h.Key("t")
		if p.typ != want {
			t.Fatalf("t cycled to %q, want %q", p.typ, want)
		}
	}
	if len(p.items) != 5 {
		t.Fatalf("all types: %d items", len(p.items))
	}
	// Refresh keeps the filter; the selection survives.
	h.Key("t", "t", "j")
	sel := p.selKey
	h.Key("r")
	if p.typ != "cumul" || p.selKey != sel {
		t.Errorf("after refresh: filter %q sel %q", p.typ, p.selKey)
	}
	checkSizes(t, p)
}

func TestActionMenu(t *testing.T) {
	p, h := newPage(t)
	h.Key("enter")
	m, ok := h.LastMenu()
	if !ok || m.Title != "Current · modules 3–4" {
		t.Fatalf("menu %+v", m)
	}
	if !h.RunAction("copy summary") {
		t.Fatal("no copy action")
	}
	s := h.Statuses()
	if len(s) == 0 || !strings.Contains(s[len(s)-1], "place 14191/26050, 247/453 in program, percentile 54, GPA 6.81") {
		t.Errorf("statuses %v", s)
	}
	if !h.RunAction("show only: current") {
		t.Fatal("no filter action")
	}
	if p.typ != "current" || len(p.items) != 2 {
		t.Fatalf("filter %q", p.typ)
	}
	h.Key("enter")
	if !h.RunAction("show all") || p.typ != "" {
		t.Fatalf("show all: %q", p.typ)
	}
	h.Key("j", "c")
	if s := h.Statuses(); !strings.Contains(s[len(s)-1], "Rating for minor") {
		t.Errorf("c copies the selected rating: %v", s)
	}
}

func TestYears(t *testing.T) {
	p, h := newPage(t)
	h.Key("y")
	if s := h.Statuses(); len(s) == 0 || !strings.Contains(s[len(s)-1], "No other academic years") {
		t.Errorf("statuses %v", s)
	}
	resp := p.data
	resp.AvailableAcademicYears = []string{"2025/2026", "2024/2025"}
	resp.AvailablePrograms = append(resp.AvailablePrograms, api.Program{ID: "42", Title: "Minor program"})
	send(h, p, resp, nil)
	v := h.View(120, 30)
	if !strings.Contains(v, "2025/2026 ‹y›") || !strings.Contains(v, "‹p›") {
		t.Errorf("selector:\n%s", v)
	}
	seq := p.load.Seq
	h.Key("h")
	if p.load.Seq != seq+1 {
		t.Fatal("h didn't refetch")
	}
	// The fixture answers with 2025/2026 again, which the page trusts.
	if p.curYear() != "2025/2026" || len(p.items) != 5 {
		t.Errorf("year %q items %d", p.curYear(), len(p.items))
	}
	send(h, p, resp, nil)
	h.Key("l")
	if s := h.Statuses(); !strings.Contains(s[len(s)-1], "No newer") {
		t.Errorf("statuses %v", s)
	}
	seq = p.load.Seq
	h.Key("p")
	if p.load.Seq != seq+1 {
		t.Fatal("p didn't refetch")
	}
}

func TestEdgeCases(t *testing.T) {
	p, h := newPage(t)
	weird := api.RatingsResponse{
		AvailableTypes: []string{"weird_kind", "current"},
		Items: []api.Rating{
			{Type: "weird_kind", Title: "\x1b[2J" + strings.Repeat("Очень длинный заголовок ", 8)},
			{},
			{Type: "current", Title: "Текущий рейтинг (3 модуль)", Percentil: api.NewNum(12.6), PlaceCurr: api.NewNum(5),
				DisciplineList: []api.RatingDiscipline{{Discipline: strings.Repeat("Длинная дисциплина ", 6)}, {Grade: api.NewNum(7)}}},
			{Type: "cumul", LearnPeriod: "2024/2025 учебный год 2 модуль", PlaceCurr: api.NewNum(3), PlaceTotal: api.NewNum(1),
				Degree: "DEGREE_SOMETHING_NEW", Campus: "CAMPUS_XYZ", ExamRetake: api.NewNum(2), ExamRetakeGood: api.NewNum(1)},
		},
	}
	send(h, p, weird, nil)
	if len(p.items) != 4 {
		t.Fatalf("items %d", len(p.items))
	}
	for i := range p.items {
		p.list.Select(i)
		checkSizes(t, p)
		d := ansi.Strip(p.detail(50))
		if strings.Contains(d, "\x1b") {
			t.Error("escape leaked")
		}
	}
	types := p.types()
	if len(types) != 3 || types[0] != "current" || types[len(types)-1] != "weird_kind" {
		t.Errorf("types %v", types)
	}
	if got := typeName("weird_kind"); got != "Weird Kind" {
		t.Errorf("typeName %q", got)
	}
	if got := degreeName("DEGREE_SOMETHING_NEW"); got != "Something New" {
		t.Errorf("degreeName %q", got)
	}
	if got := shortTitle(weird.Items[2]); got != "Current · module 3" {
		t.Errorf("shortTitle %q", got)
	}
	if got := shortTitle(weird.Items[3]); got != "Cumulative · module 2" {
		t.Errorf("shortTitle from period %q", got)
	}
	if got := placeShort(weird.Items[2]); got != "top 13%" {
		t.Errorf("placeShort %q", got)
	}

	// Filter to a type, then a reload without it resets the filter.
	h.Key("t", "t", "t")
	if p.typ != "weird_kind" {
		t.Fatalf("filter %q", p.typ)
	}
	send(h, p, api.RatingsResponse{Items: []api.Rating{{Type: "current"}}}, nil)
	if p.typ != "" || len(p.items) != 1 {
		t.Errorf("filter %q items %d", p.typ, len(p.items))
	}

	// Empty response: no years, programs, types or items.
	send(h, p, api.RatingsResponse{}, nil)
	checkSizes(t, p)
	h.Key("t", "y", "Y", "h", "l", "p", "enter", "c", "x", "j", "J", "G")
	if v := h.View(100, 30); !strings.Contains(v, emptyText()) {
		t.Errorf("empty view:\n%s", v)
	}

	// Error without data (Init not run) → retry hint.
	p2 := New(uitest.Ctx(t)).(*page)
	p2.load.Begin()
	p2.Update(ui.Result[api.RatingsResponse]{ID: p2.id, Seq: p2.load.Seq, Err: errors.New("boom")})
	v := ansi.Strip(p2.View(100, 30))
	if !strings.Contains(v, "boom") || !strings.Contains(v, "press r to retry") {
		t.Errorf("error view:\n%s", v)
	}
	checkSizes(t, p2)

	// Foreign results are ignored.
	p.Update(ui.Result[api.RatingsResponse]{ID: p.id + 1000, Seq: p.load.Seq, Data: weird})
	if len(p.items) != 0 {
		t.Error("foreign result applied")
	}
}

func TestBanners(t *testing.T) {
	p, h := newPage(t)
	tr := true
	h.Send(ui.Result[[]api.Banner]{ID: p.id, Seq: p.banner.Load.Seq, Data: []api.Banner{
		{ID: "r1", Title: "Ratings are being recalculated", Description: "Places may change until Friday", IsDismissible: true, IsEnabled: &tr},
	}})
	v := h.View(100, 30)
	if !strings.Contains(v, "! Ratings are being recalculated — Places may change") || !strings.Contains(v, "x hide") {
		t.Errorf("banner:\n%s", v)
	}
	found := false
	for _, hint := range p.Hints() {
		found = found || hint.Key == "x"
	}
	if !found {
		t.Error("no x hint")
	}
	checkSizes(t, p)
	h.Key("x")
	if strings.Contains(h.View(100, 30), "recalculated") || !p.ctx.Settings.BannerDismissed("r1") {
		t.Error("banner not dismissed")
	}
	// Reload re-fetches banners (fixture: none) and data without errors.
	h.Send(ui.ReloadMsg{})
	if len(p.items) != 5 {
		t.Fatal("reload lost data")
	}
	h.Send(ui.Result[[]api.Banner]{ID: p.id, Seq: p.banner.Load.Seq, Err: errors.New("banner boom")})
	if strings.Contains(h.View(100, 30), "boom") {
		t.Error("banner error shown")
	}
}
