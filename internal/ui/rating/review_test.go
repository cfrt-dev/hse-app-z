package rating

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
		resp := p.data
		resp.AvailableAcademicYears = []string{"2024/2025", "2025/2026"}
		send(h, p, resp, nil)
		older := press(p, "y") // → 2024/2025
		newer := press(p, "y") // wraps → 2025/2026
		if p.curYear() != "2025/2026" {
			t.Fatalf("selection after two presses: %q", p.curYear())
		}
		first, second := older(), newer()
		if newerFirst {
			first, second = second, first
		}
		h.Send(first)
		h.Send(second)
		if p.curYear() != "2025/2026" || len(p.items) != 5 {
			t.Errorf("newerFirst=%v: year %q, %d items", newerFirst, p.curYear(), len(p.items))
		}
	}
}

func TestProgramsDuplicatesAndBlankIDs(t *testing.T) {
	p, h := newPage(t)
	resp := p.data
	resp.AvailablePrograms = []api.Program{
		{ID: "1306560", Title: "Main"},
		{ID: "001306560", Title: "Main again"},
		{ID: "", Title: "Broken"},
		{ID: "42", Title: "Second"},
	}
	send(h, p, resp, nil)
	press(p, "p")
	if !sameID(p.program, "42") {
		t.Fatalf("p went to %q, want the second program", p.program)
	}
	press(p, "p")
	if !sameID(p.program, "1306560") {
		t.Fatalf("p from the last program went to %q, want the first", p.program)
	}

	resp.AvailablePrograms = []api.Program{{ID: "1306560", Title: "Main"}, {ID: "1306560", Title: "Main"}, {ID: ""}}
	send(h, p, resp, nil)
	if hasHint(p, "p") || strings.Contains(h.View(140, 30), "‹p›") {
		t.Error("program switch advertised with a single distinct program")
	}
	seq := p.load.Seq
	h.Key("p")
	if p.load.Seq != seq {
		t.Error("p refetched the same program")
	}
}

func TestYearListIncludesShownYear(t *testing.T) {
	p, h := newPage(t)
	p.year = ""
	resp := api.RatingsResponse{
		AvailableAcademicYears: []string{"2023/2024", "2024/2025"},
		CurrentAcademicYear:    "2025/2026",
		Items:                  []api.Rating{{Title: "X", Type: "current"}},
	}
	send(h, p, resp, nil)
	press(p, "h")
	if p.year != "2024/2025" {
		t.Errorf("h from the shown year 2025/2026 went to %q, want 2024/2025", p.year)
	}
}

func TestEmptyAvailableYearsDoesNotTrap(t *testing.T) {
	p, h := newPage(t)
	resp := p.data
	resp.AvailableAcademicYears = []string{"2024/2025", "2025/2026"}
	send(h, p, resp, nil)
	press(p, "h")
	send(h, p, api.RatingsResponse{SelectedAcademicYear: "2024/2025", Items: []api.Rating{{Title: "Old", Type: "current"}}}, nil)
	if p.curYear() != "2024/2025" {
		t.Fatalf("year %q", p.curYear())
	}
	press(p, "l")
	if p.year != "2025/2026" {
		t.Errorf("l after a response without available years: year %q (user is stuck)", p.year)
	}
}

func TestTypeFilterDroppedWhenTypeDisappears(t *testing.T) {
	p, h := newPage(t)
	h.Key("t", "t", "t", "t") // minor
	if p.typ != "minor" {
		t.Fatalf("filter %q", p.typ)
	}
	resp := p.data
	resp.AvailableTypes = []string{"current"}
	var kept []api.Rating
	for _, r := range resp.Items {
		if r.Type == "current" {
			kept = append(kept, r)
		}
	}
	resp.Items = kept
	send(h, p, resp, nil)
	if p.typ != "" || len(p.items) != len(kept) {
		t.Errorf("filter %q, %d items shown", p.typ, len(p.items))
	}
	if !strings.Contains(h.View(100, 30), "All types") {
		t.Errorf("selector:\n%s", h.View(100, 30))
	}
}

func TestReloadAfterAccountChange(t *testing.T) {
	p, h := newPage(t)
	resp := p.data
	resp.AvailablePrograms = append(resp.AvailablePrograms, api.Program{ID: "42", Title: "Minor"})
	send(h, p, resp, nil)
	press(p, "p")
	if p.program != "42" {
		t.Fatalf("program %q", p.program)
	}
	send(h, p, resp, nil) // the fixture's answer, as if for program 42
	p.program = "42"
	p.ctx.Me = ui.Me{Email: "someone.else@edu.hse.ru"}
	p.Update(ui.ReloadMsg{})
	if len(p.items) != 0 || p.program != "" || p.year != "" {
		t.Errorf("previous account's data kept: %d items, program %q, year %q", len(p.items), p.program, p.year)
	}
	if v := h.View(100, 30); !strings.Contains(v, "Loading") {
		t.Errorf("view after account change:\n%s", v)
	}
}
