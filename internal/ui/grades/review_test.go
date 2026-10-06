package grades

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"hse-app-z/internal/api"
	"hse-app-z/internal/ui"
	"hse-app-z/internal/ui/uitest"
)

// press delivers a key without running the resulting command, so tests can
// control when (and in which order) responses arrive.
func press(p *page, key string) tea.Cmd { return p.Update(uitest.KeyMsgs(key)[0]) }

func hasHint(p *page, key string) bool {
	for _, h := range p.Hints() {
		if h.Key == key {
			return true
		}
	}
	return false
}

func TestYearSwitchOutOfOrder(t *testing.T) {
	for _, newerFirst := range []bool{true, false} {
		p, h := newPage(t)
		older := press(p, "y") // → 2025/2026
		newer := press(p, "y") // wraps back → 2026/2027
		if p.curYear() != "2026/2027" {
			t.Fatalf("selection after two presses: %q", p.curYear())
		}
		first, second := older(), newer()
		if newerFirst {
			first, second = second, first
		}
		h.Send(first)
		h.Send(second)
		if p.curYear() != "2026/2027" || len(p.data.Items) != 16 || p.data.SelectedAcademicYear != "2026/2027" {
			t.Errorf("newerFirst=%v: year %q, %d items of %q", newerFirst, p.curYear(), len(p.data.Items), p.data.SelectedAcademicYear)
		}
	}
}

func TestProgramsDuplicatesAndBlankIDs(t *testing.T) {
	p, h := newPage(t)
	resp := p.data
	// The same program twice (zero-padded and numeric), a blank id, and a
	// real second program.
	resp.AvailablePrograms = []api.Program{
		{ID: "000275111", Name: "Main"},
		{ID: "275111", Name: "Main again"},
		{ID: "", Name: "Broken"},
		{ID: "000999", Name: "Second"},
	}
	send(h, p, resp, nil)
	press(p, "p")
	if !sameID(p.program, "000999") {
		t.Fatalf("p went to %q, want the second program", p.program)
	}
	press(p, "p")
	if !sameID(p.program, "000275111") {
		t.Fatalf("p from the last program went to %q, want the first", p.program)
	}

	// Only duplicates of one program: nothing to switch to.
	resp.AvailablePrograms = []api.Program{{ID: "000275111", Name: "Main"}, {ID: "275111", Name: "Main"}, {ID: " "}}
	send(h, p, resp, nil)
	if hasHint(p, "p") || strings.Contains(h.View(120, 30), "‹p›") {
		t.Error("program switch advertised with a single distinct program")
	}
	seq := p.load.Seq
	h.Key("p")
	if p.load.Seq != seq {
		t.Error("p refetched the same program")
	}
	if s := h.Statuses(); len(s) == 0 || !strings.Contains(s[len(s)-1], "Only one program") {
		t.Errorf("statuses %v", s)
	}
}

func TestYearListIncludesShownYear(t *testing.T) {
	p, h := newPage(t)
	// The server didn't say which year it picked; the page shows the
	// current academic year, which isn't among the available ones.
	p.year = ""
	send(h, p, api.GradesResponse{
		AvailableAcademicYears: []string{"2023/2024", "2024/2025"},
		CurrentAcademicYear:    "2025/2026",
		Items:                  []api.Grade{{ID: "1", Discipline: "X"}},
	}, nil)
	if !strings.Contains(h.View(100, 30), "2025/2026 ‹y›") {
		t.Fatalf("selector:\n%s", h.View(100, 30))
	}
	press(p, "h")
	if p.year != "2024/2025" {
		t.Errorf("h from the shown year 2025/2026 went to %q, want 2024/2025", p.year)
	}
}

func TestEmptyAvailableYearsDoesNotTrap(t *testing.T) {
	p, h := newPage(t)
	press(p, "y")
	// The older year's response doesn't repeat the list of years.
	send(h, p, api.GradesResponse{SelectedAcademicYear: "2025/2026", Items: []api.Grade{{ID: "1", Discipline: "X"}}}, nil)
	if p.curYear() != "2025/2026" {
		t.Fatalf("year %q", p.curYear())
	}
	press(p, "l")
	if p.year != "2026/2027" {
		t.Errorf("l after a response without available years: year %q (user is stuck)", p.year)
	}
}

func TestSummaryWithoutCredits(t *testing.T) {
	p, h := newPage(t)
	ten := &api.GradeValue{TenPoint: api.NewNum(8)}
	send(h, p, api.GradesResponse{SelectedAcademicYear: "2026/2027", Items: []api.Grade{
		{ID: "1", Discipline: "A", Grade: ten},
		{ID: "2", Discipline: "B"},
	}}, nil)
	s := p.summary(80)
	if strings.Contains(s, "cr") {
		t.Errorf("summary shows credits that the API didn't send: %q", s)
	}
	if !strings.Contains(s, "avg 8") || !strings.Contains(s, "1/2 graded") {
		t.Errorf("summary %q", s)
	}
	// Zero-credit items are real data, though.
	send(h, p, api.GradesResponse{SelectedAcademicYear: "2026/2027", Items: []api.Grade{
		{ID: "1", Discipline: "PE", Credits: api.NewNum(0), Grade: &api.GradeValue{Pass: new(bool)}},
	}}, nil)
	if s := p.summary(80); !strings.Contains(s, "0 cr") {
		t.Errorf("zero-credit summary %q", s)
	}
}

var _ = ui.NewID

func TestReloadAfterAccountChange(t *testing.T) {
	p, h := newPage(t)
	h.Key("y") // 2025/2026, 20 grades
	if p.curYear() != "2025/2026" {
		t.Fatalf("year %q", p.curYear())
	}
	// Language switch (same account): the selection is kept.
	h.Send(ui.ReloadMsg{})
	if p.curYear() != "2025/2026" || len(p.data.Items) != 20 {
		t.Fatalf("same-account reload: year %q, %d items", p.curYear(), len(p.data.Items))
	}
	// Someone else signs in: the previous account's grades disappear at
	// once and its year/program aren't requested for the new account.
	p.ctx.Me = ui.Me{Email: "someone.else@edu.hse.ru"}
	cmd := p.Update(ui.ReloadMsg{})
	if len(p.data.Items) != 0 || p.program != "" || p.year != "" {
		t.Errorf("previous account's data kept: %d items, program %q, year %q", len(p.data.Items), p.program, p.year)
	}
	if v := h.View(100, 30); strings.Contains(v, "Algebra and Geometry") || !strings.Contains(v, "Loading") {
		t.Errorf("view after account change:\n%s", v)
	}
	settle(h, cmd)
	if p.curYear() != "2026/2027" || len(p.data.Items) != 16 {
		t.Errorf("new account got year %q with %d items, want the server default", p.curYear(), len(p.data.Items))
	}
}

// settle runs cmd (and the commands of a batch) and delivers the results.
func settle(h *uitest.Harness, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	msg := cmd()
	if b, ok := msg.(tea.BatchMsg); ok {
		for _, c := range b {
			settle(h, c)
		}
		return
	}
	if msg != nil {
		h.Send(msg)
	}
}
