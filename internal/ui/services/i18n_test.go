package services

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"hse-app-z/internal/api"
	"hse-app-z/internal/ui"
	"hse-app-z/internal/ui/uitest"
)

// setRU switches the interface to Russian for one test. The language is
// process-wide: tests calling it must not run in parallel.
func setRU(t *testing.T) {
	t.Helper()
	ui.SetLang("ru")
	t.Cleanup(func() { ui.SetLang("en") })
}

// englishWords are English UI words this package itself writes (English
// service names are replaced in these tests, see russify).
var englishWords = []string{
	"Services", "Nothing in this folder", "No services", "My requests", "requests", "request",
	"press", "view", "open", "copy", "link", "Folder", "browse", "No link", "Service", "Untitled",
	"Load more", "Loading", "Request", "yes", "no", "No requests",
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

// noURLs drops links from s: they are data and may contain any word.
func noURLs(s string) string {
	return regexp.MustCompile(`https?://\S*`).ReplaceAllString(s, "")
}

func checkRU(t *testing.T, what, s string) {
	t.Helper()
	if l := leftovers(noURLs(s), englishWords); len(l) > 0 {
		t.Errorf("%s: English words %q left in:\n%s", what, l, s)
	}
}

// russify gives the catalogue Russian names and descriptions (one entry
// keeps neither, to show the id fallback).
func russify(svcs []api.Service) []api.Service {
	out := append([]api.Service(nil), svcs...)
	for i := range out {
		out[i].Name = fmt.Sprintf("Сервис №%d", i+1)
		if out[i].Description != "" {
			out[i].Description = "Описание сервиса"
		}
	}
	out = append(out, api.Service{ID: "77"}, api.Service{})
	return out
}

func tasksRU() api.TasksPage {
	return api.TasksPage{Cursor: "0", NextCursor: "c2", Items: []map[string]any{
		{"id": float64(1), "title": "Справка об обучении", "status": "В работе", "url": "https://lk.hse.ru/requests/1", "срочно": true},
		{"комментарий": "строка", "готово": false},
	}}
}

func TestRussian(t *testing.T) {
	setRU(t)
	p, h := newRoot(t)
	if p.Title() != "Сервисы" {
		t.Errorf("title %q", p.Title())
	}
	h.Send(ui.Result[[]api.Service]{ID: p.id, Seq: p.svcLoad.Seq, Data: russify(p.services)})
	h.Send(ui.Result[api.TasksPage]{ID: p.id, Seq: p.tasksLoad.Seq, Data: tasksRU()})
	var all string
	for i := 0; i < p.rowCount(); i++ {
		p.list.Select(i)
		for _, sz := range [][2]int{{100, 30}, {60, 12}} {
			v := h.View(sz[0], sz[1])
			checkSize(t, v, sz[0], sz[1])
			checkRU(t, fmt.Sprintf("row %d %dx%d", i, sz[0], sz[1]), v)
			all += v + "\n"
		}
	}
	for _, want := range []string{
		"Сервисы", "Мои заявки (2+)", "2+ заявок SmartPoint", "нажмите enter, чтобы открыть",
		"Папка · нажмите enter, чтобы открыть", "enter открыть · y копировать ссылку", "Нет ссылки",
		"Сервис 77", "Сервис без названия",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("views lack %q", want)
		}
	}
	p.list.Select(1)
	t.Logf("services RU 100×30:\n%s", h.View(100, 30))
	for _, hint := range p.Hints() {
		if regexp.MustCompile(`[A-Za-z]`).MatchString(hint.Desc) {
			t.Errorf("hint %q: %q", hint.Key, hint.Desc)
		}
	}

	// Statuses.
	plain := false
	for i := 0; i < p.rowCount(); i++ {
		p.list.Select(i)
		if s, ok := p.selectedService(); ok && p.kind(s) == kindPlain && s.Name == "" && s.ID == "" {
			plain = true
			h.Key("enter")
			if got := lastStatus(h); got != "У «Сервис без названия» нет ссылки" {
				t.Errorf("enter on a plain entry: %q", got)
			}
			h.Key("y")
			if got := lastStatus(h); got != "Нет ссылки для копирования" {
				t.Errorf("y: %q", got)
			}
			h.Key("o")
			if got := lastStatus(h); got != "Нет ссылки" {
				t.Errorf("o: %q", got)
			}
		}
	}
	if !plain {
		t.Error("no plain entry to check the statuses on")
	}

	// A folder page is named after its folder at render time.
	for i := 0; i < p.rowCount(); i++ {
		p.list.Select(i)
		if s, ok := p.selectedService(); ok && p.kind(s) == kindFolder {
			h.Key("enter")
			break
		}
	}
	sub, ok := h.LastPushed().(*Page)
	if !ok || !strings.HasPrefix(sub.Title(), "Сервис №") {
		t.Fatalf("pushed %T", h.LastPushed())
	}
	sub.svc.Name, sub.svc.Description = "", ""
	if got := sub.Title(); !strings.HasPrefix(got, "Сервис ") {
		t.Errorf("unnamed folder title %q", got)
	}
	sub.services = nil
	sub.svcLoad = ui.Load{Loaded: true}
	if v := ansi.Strip(sub.View(60, 12)); !strings.Contains(v, "В этой папке пусто") {
		t.Errorf("empty folder:\n%s", v)
	}

	// The requests page.
	p.list.Select(0)
	h.Key("enter")
	rp, ok := h.LastPushed().(*requestsPage)
	if !ok {
		t.Fatalf("pushed %T", h.LastPushed())
	}
	rh := uitest.New(t, rp)
	if rp.Title() != "Мои заявки" {
		t.Errorf("title %q", rp.Title())
	}
	all = ""
	for i := 0; i < rp.rowCount(); i++ {
		rp.list.Select(i)
		for _, sz := range [][2]int{{100, 30}, {60, 12}} {
			v := rh.View(sz[0], sz[1])
			checkSize(t, v, sz[0], sz[1])
			checkRU(t, fmt.Sprintf("request %d %dx%d", i, sz[0], sz[1]), v)
			all += v + "\n"
		}
	}
	for _, want := range []string{"Справка об обучении", "В работе", "Заявка 2", "Загрузить ещё…", "нажмите enter, чтобы загрузить ещё", "да", "нет", "o открыть ссылку · y копировать ссылку"} {
		if !strings.Contains(all, want) {
			t.Errorf("requests views lack %q", want)
		}
	}
	rp.list.Select(0)
	t.Logf("requests RU 100×30:\n%s", rh.View(100, 30))
	rp.list.Select(1)
	rh.Key("o")
	if got := lastStatus(rh); got != "У этой заявки нет ссылки" {
		t.Errorf("o: %q", got)
	}
	rp.load.Loading = true
	rp.list.Select(rp.rowCount() - 1)
	if v := rh.View(100, 30); !strings.Contains(v, "Загрузка…") {
		t.Errorf("loading row:\n%s", v)
	}
	for _, hint := range rp.Hints() {
		if regexp.MustCompile(`[A-Za-z]`).MatchString(hint.Desc) {
			t.Errorf("hint %q: %q", hint.Key, hint.Desc)
		}
	}

	// Error notes and empty states.
	rp.apply(tasksResult{})
	rp.load = ui.Load{Loaded: true}
	if v := rh.View(60, 12); !strings.Contains(v, "Заявок нет") {
		t.Errorf("no requests:\n%s", v)
	}
	h.Send(ui.Result[api.TasksPage]{ID: p.id, Seq: p.tasksLoad.Begin(), Err: errors.New("boom")})
	if v := h.View(100, 30); !strings.Contains(v, "⚠ заявки: boom") {
		t.Errorf("tasks error note:\n%s", v)
	}
	h.Send(ui.Result[[]api.Service]{ID: p.id, Seq: p.svcLoad.Seq, Data: []api.Service{}})
	p.tasks = nil
	p.relist("")
	if v := h.View(100, 30); !strings.Contains(v, "Нет доступных сервисов") {
		t.Errorf("no services:\n%s", v)
	}

	// Back to English on the next frame.
	ui.SetLang("en")
	if p.Title() != "Services" || rp.Title() != "My requests" {
		t.Errorf("English titles %q / %q", p.Title(), rp.Title())
	}
}
