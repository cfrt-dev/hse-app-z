package news

import (
	"strings"
	"testing"

	"hse-app-z/internal/api"
	"hse-app-z/internal/ui"
)

func TestUnsafeLinksAreSkipped(t *testing.T) {
	p, h := newPage(t)
	act := func(title, link string) *api.StoryAction { return &api.StoryAction{Title: title, Link: link} }
	story := api.Story{ID: "s1", Title: "Mixed links", Pages: []api.StoryPage{
		{Action: act("Run", "javascript:alert(1)")},
		{Action: act("Local", "file:///etc/passwd")},
		{Action: act("Site", "https://www.hse.ru/news/")},
	}}
	h.Send(ui.Result[[]api.Story]{ID: p.id, Seq: p.load.Seq, Data: []api.Story{story}})
	h.Key("o")
	if got := lastStatus(h); got != "Opened https://www.hse.ru/news/" {
		t.Errorf("o with an unsafe first link: %q", got)
	}
	h.Key("y")
	if got := lastStatus(h); got != "Copied link: https://www.hse.ru/news/" {
		t.Errorf("y: %q", got)
	}
	h.Key("enter")
	m, ok := h.LastMenu()
	if !ok {
		t.Fatal("no menu")
	}
	for _, a := range m.Actions {
		if strings.Contains(a.Label, "javascript") || strings.Contains(a.Label, "passwd") || strings.Contains(a.Label, "Run") {
			t.Errorf("menu offers an unsafe link: %q", a.Label)
		}
	}

	// Only unsafe links: nothing to open, and no "o open link" promise.
	only := api.Story{ID: "s2", Title: "Bad", Pages: []api.StoryPage{{Action: act("Run", "javascript:alert(1)")}}}
	h.Send(ui.Result[[]api.Story]{ID: p.id, Seq: p.load.Seq, Data: []api.Story{only}})
	h.Key("o")
	if got := lastStatus(h); got != "This story has no link" {
		t.Errorf("o with only unsafe links: %q", got)
	}
	if v := h.View(100, 30); strings.Contains(v, "o open link") {
		t.Errorf("detail advertises o for an unsafe link:\n%s", v)
	}
}

func TestReloadResetsScrollWhenStoryChanges(t *testing.T) {
	p, h := newPage(t)
	long := func(id string) api.Story {
		var pages []api.StoryPage
		for i := 0; i < 30; i++ {
			pages = append(pages, api.StoryPage{Action: &api.StoryAction{Title: "Page", Link: "https://hse.ru/" + id}})
		}
		return api.Story{ID: api.FlexString(id), Title: "Story " + id, Pages: pages}
	}
	h.Send(ui.Result[[]api.Story]{ID: p.id, Seq: p.load.Seq, Data: []api.Story{long("a"), long("b")}})
	p.list.Select(0)
	h.View(100, 12)
	h.Key("J", "J", "J", "J", "J")
	if p.scroll.Offset == 0 {
		t.Fatal("detail didn't scroll")
	}
	// Same story, same place: the scroll position is kept.
	h.Send(ui.Result[[]api.Story]{ID: p.id, Seq: p.load.Seq, Data: []api.Story{long("a"), long("b")}})
	if p.scroll.Offset == 0 {
		t.Error("scroll reset although the same story is selected")
	}
	// Story "a" is gone; "c" now sits at the cursor: start at the top.
	h.Send(ui.Result[[]api.Story]{ID: p.id, Seq: p.load.Seq, Data: []api.Story{long("c"), long("b")}})
	if s, _ := p.selected(); s.ID != "c" || p.scroll.Offset != 0 {
		t.Errorf("selected %q, scroll offset %d (stale scroll from another story)", s.ID, p.scroll.Offset)
	}
}

func TestDetailHintsMatchTheStory(t *testing.T) {
	p, h := newPage(t)
	noImage := api.Story{ID: "1", Title: "Linked", Pages: []api.StoryPage{{Action: &api.StoryAction{Title: "Go", Link: "https://hse.ru"}}}}
	imageOnly := api.Story{ID: "2", Title: "Pictured", PreviewImage: "https://hse.ru/cover.jpg"}
	h.Send(ui.Result[[]api.Story]{ID: p.id, Seq: p.load.Seq, Data: []api.Story{noImage, imageOnly}})
	p.list.Select(0)
	if v := h.View(120, 30); !strings.Contains(v, "o open link") || strings.Contains(v, "i cover image") {
		t.Errorf("story without a cover:\n%s", v)
	}
	p.list.Select(1)
	if v := h.View(120, 30); strings.Contains(v, "o open link") || !strings.Contains(v, "i cover image") {
		t.Errorf("story with only a cover:\n%s", v)
	}
}
