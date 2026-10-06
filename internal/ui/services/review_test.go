package services

import (
	"strings"
	"testing"

	"hse-app-z/internal/api"
	"hse-app-z/internal/ui"
	"hse-app-z/internal/ui/uitest"
)

func TestRequestsDuplicatesAcrossPages(t *testing.T) {
	ctx := uitest.Ctx(t)
	first := api.TasksPage{Cursor: "0", NextCursor: "p2", Items: []map[string]any{
		{"id": float64(1), "title": "one"},
		{"id": float64(2), "title": "two"},
	}}
	rp := newRequests(ctx, &first)
	h := uitest.New(t, rp)
	h.Key("G", "enter")
	// The next page overlaps the first one (a new request shifted the
	// window), and a server that ignores the cursor repeats page one.
	h.Send(ui.Result[tasksResult]{ID: rp.id, Seq: rp.load.Seq, Data: tasksResult{
		Cursor: "p2", More: true,
		Page: api.TasksPage{Cursor: "p2", NextCursor: "p3", Items: []map[string]any{
			{"id": float64(2), "title": "two"},
			{"id": float64(3), "title": "three"},
			{"title": "no id"},
		}},
	}})
	h.Send(ui.Result[tasksResult]{ID: rp.id, Seq: rp.load.Seq, Data: tasksResult{
		Cursor: "p3", More: true,
		Page: api.TasksPage{Cursor: "p3", Items: []map[string]any{
			{"id": float64(1), "title": "one"},
			{"title": "no id"},
		}},
	}})
	var titles []string
	for _, it := range rp.items {
		titles = append(titles, it["title"].(string))
	}
	if len(rp.items) != 4 {
		t.Errorf("items after overlapping pages: %v", titles)
	}
	checkSize(t, h.View(60, 12), 60, 12)
}

func TestRequestsCountWithMorePages(t *testing.T) {
	p, h := newRoot(t)
	one := []map[string]any{{"id": float64(1), "title": "one"}}
	h.Send(ui.Result[api.TasksPage]{ID: p.id, Seq: p.tasksLoad.Seq, Data: api.TasksPage{Cursor: "0", NextCursor: "0", Items: one}})
	if v := h.View(100, 30); !strings.Contains(v, "My requests (1)") {
		t.Errorf("single page:\n%s", v)
	}
	h.Send(ui.Result[api.TasksPage]{ID: p.id, Seq: p.tasksLoad.Seq, Data: api.TasksPage{Cursor: "0", NextCursor: "c2", Items: one}})
	p.list.Select(0)
	v := h.View(100, 30)
	if !strings.Contains(v, "My requests (1+)") || !strings.Contains(v, "1+ SmartPoint requests") {
		t.Errorf("more pages:\n%s", v)
	}
}

func TestReloadAfterAccountChangeDropsRequests(t *testing.T) {
	p, h := newRoot(t)
	h.Send(ui.Result[api.TasksPage]{ID: p.id, Seq: p.tasksLoad.Seq, Data: sampleTasks()})
	p.list.Select(0)
	h.Key("enter")
	rp := h.LastPushed().(*requestsPage)
	rh := uitest.New(t, rp)
	if !p.hasRequestsRow() || len(rp.items) == 0 {
		t.Fatal("setup")
	}
	// Same account (language switch): requests stay while reloading.
	p.Update(ui.ReloadMsg{})
	rp.Update(ui.ReloadMsg{})
	if !p.hasRequestsRow() || len(rp.items) == 0 {
		t.Error("same-account reload dropped the requests")
	}
	p.ctx.Me = ui.Me{Email: "someone.else@edu.hse.ru"}
	p.Update(ui.ReloadMsg{})
	rp.Update(ui.ReloadMsg{})
	if p.hasRequestsRow() || len(rp.items) != 0 || rp.next != "" {
		t.Errorf("previous account's requests kept: row %v, %d items", p.hasRequestsRow(), len(rp.items))
	}
	if v := rh.View(100, 30); strings.Contains(v, "Certificate of enrolment") {
		t.Errorf("requests view after account change:\n%s", v)
	}
	if v := h.View(100, 30); strings.Contains(v, "My requests") {
		t.Errorf("root view after account change:\n%s", v)
	}
}
