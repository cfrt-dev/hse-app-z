package services

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"hse-app-z/internal/api"
	"hse-app-z/internal/ui"
	"hse-app-z/internal/ui/uitest"
)

func checkSize(t *testing.T, view string, w, h int) {
	t.Helper()
	lines := strings.Split(view, "\n")
	if len(lines) > h {
		t.Errorf("view has %d lines, want ≤ %d", len(lines), h)
	}
	for i, l := range lines {
		if n := ansi.StringWidth(l); n > w {
			t.Errorf("line %d is %d cells wide (max %d): %q", i, n, w, l)
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

func newRoot(t *testing.T) (*Page, *uitest.Harness) {
	t.Helper()
	p := New(uitest.Ctx(t)).(*Page)
	return p, uitest.New(t, p)
}

func selectService(t *testing.T, p *Page, name string) {
	t.Helper()
	for i := 0; i < p.rowCount(); i++ {
		p.list.Select(i)
		if s, ok := p.selectedService(); ok && s.Name == name {
			return
		}
	}
	t.Fatalf("service %q not found", name)
}

func sampleTasks() api.TasksPage {
	return api.TasksPage{
		Cursor:     "0",
		NextCursor: "c2",
		Items: []map[string]any{
			{"id": float64(101), "title": "Certificate of enrolment", "status": "In progress",
				"url": "https://lk.hse.ru/requests/101", "created_at": "2026-10-01T10:00:00Z",
				"meta": map[string]any{"channel": "app", "tags": []any{"a", "b"}}},
			{"subject": "\x1b[31mWi-Fi\x1b[0m\u2800 doesn't work", "state": map[string]any{"name": "Closed"},
				"comment": "line one\nline two", "rating": nil, "urgent": true},
			{"number": "SP-42"},
			{"foo": strings.Repeat("very long value ", 40)},
		},
	}
}

func TestRootRender(t *testing.T) {
	p, h := newRoot(t)
	if len(p.services) != 10 {
		t.Fatalf("services: %d", len(p.services))
	}
	if p.hasRequestsRow() {
		t.Errorf("empty tasks fixture must not add a requests row")
	}
	for _, sz := range [][2]int{{100, 30}, {60, 12}, {120, 30}, {60, 10}} {
		checkSize(t, h.View(sz[0], sz[1]), sz[0], sz[1])
	}
	v := h.View(120, 30)
	t.Logf("120×30:\n%s", v)
	for _, want := range []string{"Services", "I am a Student", "›", "Rate your courses", "↗", "Folder · press enter to browse"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q", want)
		}
	}
	selectService(t, p, "Rate your courses")
	v = h.View(100, 30)
	checkSize(t, v, 100, 30)
	if !strings.Contains(v, "https://lms.hse.ru/") || !strings.Contains(v, "Student assessment of teachers") {
		t.Errorf("link detail:\n%s", v)
	}
	checkSize(t, h.View(60, 12), 60, 12)
}

func TestFolderPushesCategoryPage(t *testing.T) {
	p, h := newRoot(t)
	selectService(t, p, "I am a Student")
	h.Key("enter")
	pushed, ok := h.LastPushed().(*Page)
	if !ok {
		t.Fatalf("enter on a folder should push a services page, got %T", h.LastPushed())
	}
	if pushed.Title() != "I am a Student" || pushed.category != "/ELK_YA-STUDENT_DOOR" {
		t.Errorf("pushed page: %q %q", pushed.Title(), pushed.category)
	}
	ch := uitest.New(t, pushed)
	if len(pushed.services) != 20 || pushed.services[0].Name != "Elements of Practical Training" {
		t.Fatalf("category page didn't load services_category: %d", len(pushed.services))
	}
	if pushed.hasRequestsRow() {
		t.Errorf("category pages must not show requests")
	}
	v := ch.View(100, 30)
	checkSize(t, v, 100, 30)
	t.Logf("category 100×30:\n%s", v)

	// Enter on a link opens it.
	selectService(t, pushed, "My Courses")
	ch.Key("enter")
	if got := lastStatus(ch); got != "Opened https://edu.hse.ru/my/" {
		t.Errorf("enter on link: %q", got)
	}
	ch.Key("o")
	if got := lastStatus(ch); got != "Opened https://edu.hse.ru/my/" {
		t.Errorf("o on link: %q", got)
	}
	ch.Key("y")
	if got := lastStatus(ch); got != "Copied link: https://edu.hse.ru/my/" {
		t.Errorf("y on link: %q", got)
	}
	// y on a folder has nothing to copy.
	selectService(t, pushed, "Scholarships")
	ch.Key("y")
	if got := lastStatus(ch); got != "No link to copy" {
		t.Errorf("y on folder: %q", got)
	}
}

func TestSelfReferencingFolder(t *testing.T) {
	ctx := uitest.Ctx(t)
	p := newPage(ctx, "/ELK_LOOP", "Loop", nil)
	h := uitest.New(t, p)
	self := []api.Service{
		{ID: "1", Category: "/ELK_LOOP", Name: "Loop again"},
		{ID: "2", Category: "/ELK_LOOP/", Name: "Loop with slash"},
		{ID: "3", Category: "/ELK_OTHER", Name: "Real folder"},
	}
	h.Send(ui.Result[[]api.Service]{ID: p.id, Seq: p.svcLoad.Seq, Data: self})
	if len(p.services) != 3 {
		t.Fatalf("services: %d", len(p.services))
	}
	pushes := func() int {
		n := 0
		for _, m := range h.Emitted {
			if _, ok := m.(ui.PushMsg); ok {
				n++
			}
		}
		return n
	}
	p.list.Select(0)
	h.Key("enter")
	p.list.Select(1)
	h.Key("enter")
	if pushes() != 0 {
		t.Errorf("self-referencing folder was pushed")
	}
	if got := lastStatus(h); !strings.Contains(got, "has no link") {
		t.Errorf("status: %q", got)
	}
	v := h.View(100, 30)
	checkSize(t, v, 100, 30)
	if strings.Contains(v, "Folder · press enter") {
		t.Errorf("self-referencing item shown as folder:\n%s", v)
	}
	// A real folder still pushes, and its children may not point back at
	// any ancestor either.
	p.list.Select(2)
	h.Key("enter")
	child, ok := h.LastPushed().(*Page)
	if !ok {
		t.Fatalf("real folder not pushed")
	}
	if child.kind(api.Service{Category: "/ELK_LOOP", Name: "back to grandparent"}) != kindPlain {
		t.Errorf("folder pointing at an ancestor should not be browsable")
	}
}

func TestRequestsRowAndPage(t *testing.T) {
	p, h := newRoot(t)
	tasks := sampleTasks()
	h.Send(ui.Result[api.TasksPage]{ID: p.id, Seq: p.tasksLoad.Seq, Data: tasks})
	if !p.hasRequestsRow() || p.rowCount() != 11 {
		t.Fatalf("requests row missing: %d rows", p.rowCount())
	}
	v := h.View(100, 30)
	// The fixture page points at a next page: the count is a lower bound.
	if !strings.Contains(v, "My requests (4+)") {
		t.Errorf("view lacks requests row:\n%s", v)
	}
	p.list.Select(0)
	h.Key("enter")
	rp, ok := h.LastPushed().(*requestsPage)
	if !ok {
		t.Fatalf("enter on My requests pushed %T", h.LastPushed())
	}
	rh := uitest.New(t, rp)
	if len(rp.items) != 4 || rp.next != "c2" || rp.rowCount() != 5 {
		t.Fatalf("requests page: %d items, next %q", len(rp.items), rp.next)
	}
	for _, sz := range [][2]int{{100, 30}, {60, 12}, {120, 30}} {
		for i := 0; i < rp.rowCount(); i++ {
			rp.list.Select(i)
			checkSize(t, rh.View(sz[0], sz[1]), sz[0], sz[1])
		}
	}
	rp.list.Select(0)
	v = rh.View(120, 30)
	t.Logf("requests 120×30:\n%s", v)
	for _, want := range []string{"Certificate of enrolment", "In progress", "Wi-Fi doesn't work", "Closed", "SP-42", "Request 4", "Load more…",
		`{"channel":"app","tags":["a","b"]}`, "https://lk.hse.ru/requests/101"} {
		if !strings.Contains(v, want) {
			t.Errorf("requests view lacks %q", want)
		}
	}
	if strings.Contains(v, "\x1b[31m") || strings.Contains(v, "\u2800") {
		t.Errorf("unsanitised text")
	}
	rh.Key("o")
	if got := lastStatus(rh); got != "Opened https://lk.hse.ru/requests/101" {
		t.Errorf("o: %q", got)
	}
	rh.Key("j", "enter")
	if got := lastStatus(rh); got != "This request has no link" {
		t.Errorf("enter on linkless request: %q", got)
	}
	rp.list.Select(1)
	d := ansi.Strip(itemDetail(rp.items[1], 60))
	for _, want := range []string{"comment", "line one", "line two", "rating", "—", "urgent", "yes"} {
		if !strings.Contains(d, want) {
			t.Errorf("detail lacks %q:\n%s", want, d)
		}
	}

	// Load more: the fixture's next page is empty with next_cursor "0".
	rh.Key("G", "enter")
	if rp.next != "" || len(rp.items) != 4 || rp.rowCount() != 4 {
		t.Errorf("after load more: next %q, %d items", rp.next, len(rp.items))
	}
	if strings.Contains(rh.View(100, 30), "Load more") {
		t.Errorf("Load more row should be gone")
	}
}

func TestRequestsPagination(t *testing.T) {
	ctx := uitest.Ctx(t)
	first := api.TasksPage{Cursor: "0", NextCursor: "p2", Items: []map[string]any{{"title": "one"}}}
	rp := newRequests(ctx, &first)
	h := uitest.New(t, rp)
	// Simulate the next page arriving (appended), pointing back at an
	// already fetched cursor: no further "Load more".
	h.Key("G", "enter")
	h.Send(ui.Result[tasksResult]{ID: rp.id, Seq: rp.load.Seq, Data: tasksResult{
		Cursor: "p2", More: true,
		Page: api.TasksPage{Cursor: "p2", NextCursor: "0", Items: []map[string]any{{"title": "two"}}},
	}})
	if len(rp.items) != 2 {
		t.Errorf("items: %d", len(rp.items))
	}
	if rp.items[len(rp.items)-1]["title"] != "two" {
		t.Errorf("page 2 not appended: %v", rp.items)
	}
	// next == cursor means no more.
	rp.apply(tasksResult{Cursor: "x", More: true, Page: api.TasksPage{Cursor: "x", NextCursor: "x"}})
	if rp.next != "" {
		t.Errorf("next == cursor must stop pagination")
	}
	// A cursor seen before must stop pagination (loop guard).
	rp.apply(tasksResult{Cursor: "y", More: true, Page: api.TasksPage{Cursor: "y", NextCursor: "p2"}})
	if rp.next != "" {
		t.Errorf("seen cursor must stop pagination")
	}
	// Refresh replaces everything.
	h.Key("r")
	if len(rp.items) != 0 || rp.next != "" {
		t.Errorf("refresh should replace items with the fixture's empty page: %d", len(rp.items))
	}
	v := h.View(60, 12)
	checkSize(t, v, 60, 12)
	if !strings.Contains(v, "No requests") {
		t.Errorf("empty requests view:\n%s", v)
	}
}

func TestPartialFailure(t *testing.T) {
	p, h := newRoot(t)
	h.Send(ui.Result[api.TasksPage]{ID: p.id, Seq: p.tasksLoad.Begin(), Err: errors.New("boom")})
	v := h.View(100, 30)
	checkSize(t, v, 100, 30)
	if !strings.Contains(v, "I am a Student") || !strings.Contains(v, "requests: boom") {
		t.Errorf("tasks failure should keep services and show a note:\n%s", v)
	}
	// Services failing on first load shows the error state.
	q := newPage(uitest.Ctx(t), "/X", "X", nil)
	qh := uitest.New(t, q)
	qh.Send(ui.Result[[]api.Service]{ID: q.id, Seq: q.svcLoad.Begin(), Err: &api.APIError{Status: 500}})
	v = qh.View(60, 12)
	checkSize(t, v, 60, 12)
	if len(q.services) == 0 || !strings.Contains(v, "HSE server error") {
		// The fixture load succeeded first, so data is kept and the error
		// goes into the pane title.
		t.Errorf("error after data:\n%s", v)
	}
	// Empty folder.
	qh.Send(ui.Result[[]api.Service]{ID: q.id, Seq: q.svcLoad.Begin(), Data: []api.Service{}})
	v = qh.View(60, 12)
	if !strings.Contains(v, "Nothing in this folder") {
		t.Errorf("empty folder:\n%s", v)
	}
	qh.Key("enter", "o", "y", "j", "J")
}

func TestReload(t *testing.T) {
	p, h := newRoot(t)
	selectService(t, p, "Events")
	s, t0 := p.svcLoad.Seq, p.tasksLoad.Seq
	h.Send(ui.ReloadMsg{})
	if p.svcLoad.Seq != s+1 || p.tasksLoad.Seq != t0+1 {
		t.Errorf("reload didn't refetch both sources")
	}
	if sel, _ := p.selectedService(); sel.Name != "Events" {
		t.Errorf("selection lost on reload: %q", sel.Name)
	}
	h.Key("r")
	if p.svcLoad.Seq != s+2 {
		t.Errorf("r didn't refetch")
	}
}
