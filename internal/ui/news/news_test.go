package news

import (
	"strings"
	"testing"
	"time"

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

func newPage(t *testing.T) (*Page, *uitest.Harness) {
	t.Helper()
	p := New(uitest.Ctx(t)).(*Page)
	return p, uitest.New(t, p)
}

func TestRenderAndOrder(t *testing.T) {
	p, h := newPage(t)
	if len(p.stories) != 12 {
		t.Fatalf("stories: %d", len(p.stories))
	}
	for i := 1; i < len(p.stories); i++ {
		if p.stories[i].Published().After(p.stories[i-1].Published()) {
			t.Errorf("stories not sorted newest first at %d", i)
		}
	}
	for _, sz := range [][2]int{{100, 30}, {60, 12}, {120, 30}, {60, 10}} {
		for i := range p.stories {
			p.list.Select(i)
			checkSize(t, h.View(sz[0], sz[1]), sz[0], sz[1])
		}
	}
	p.list.Select(0)
	v := h.View(120, 30)
	t.Logf("120×30:\n%s", v)
	for _, want := range []string{"News", "05 Oct", "HSE Business Club", "Подробности", "02 Sep", "Вышка в Макс · HSE University"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q", want)
		}
	}
	if strings.Contains(v, "⠀") {
		t.Errorf("invisible title char leaked into the view")
	}
	// The first story (HSE Business Club) has three pages without action.
	if !strings.Contains(v, "no link") || !strings.Contains(v, "4. Подробности → https://t.me/hsebusinessclub/2877") {
		t.Errorf("pages section:\n%s", v)
	}
	checkSize(t, h.View(60, 12), 60, 12)
	t.Logf("60×12:\n%s", h.View(60, 12))
}

func TestTitleFallbacks(t *testing.T) {
	act := func(title, link string) *api.StoryAction { return &api.StoryAction{Title: title, Link: link} }
	cases := []struct {
		s    api.Story
		want string
	}{
		{api.Story{Title: "Вышка в Макс"}, "Вышка в Макс"},
		{api.Story{Title: "⠀", Pages: []api.StoryPage{{}, {Action: act("⠀", "x")}, {Action: act("Регистрация", "https://a")}},
			Publisher: api.StoryPublisher{Name: "Pub"}}, "Регистрация"},
		{api.Story{Title: " ⠀ ", Pages: []api.StoryPage{{Action: nil}}, Publisher: api.StoryPublisher{Name: "\x1b[31mINACCEL\x1b[0m"}}, "INACCEL"},
		{api.Story{Title: "⠀"}, "Story"},
	}
	for _, c := range cases {
		if got := StoryTitle(c.s); got != c.want {
			t.Errorf("StoryTitle = %q, want %q", got, c.want)
		}
	}
	// In the fixture, invisible titles fall back to the action title, and
	// the publisher is shown next to it.
	p, h := newPage(t)
	v := h.View(120, 30)
	if !strings.Contains(v, "Регистрация · INACCEL’26") {
		t.Errorf("fallback title row missing:\n%s", v)
	}
	for _, s := range p.stories {
		if StoryTitle(s) == "" || strings.TrimSpace(ui.Clean(StoryTitle(s))) == "" {
			t.Errorf("empty title for story %s", s.ID)
		}
	}
}

func selectPublisher(t *testing.T, p *Page, name string) api.Story {
	t.Helper()
	for i, s := range p.stories {
		if s.Publisher.Name == name {
			p.list.Select(i)
			return s
		}
	}
	t.Fatalf("no story by %q", name)
	return api.Story{}
}

func TestKeysAndMenu(t *testing.T) {
	p, h := newPage(t)
	selectPublisher(t, p, "HSE University")
	h.Key("o")
	if got := lastStatus(h); got != "Opened https://max.ru/hse_official" {
		t.Errorf("o: %q", got)
	}
	h.Key("y")
	if got := lastStatus(h); got != "Copied link: https://max.ru/hse_official" {
		t.Errorf("y: %q", got)
	}
	h.Key("i")
	if got := lastStatus(h); !strings.HasPrefix(got, "Opened https://www.hse.ru/pubs/share/folder/8ebc994d1118a4/") {
		t.Errorf("i: %q", got)
	}
	h.Key("enter")
	m, ok := h.LastMenu()
	if !ok {
		t.Fatal("no menu")
	}
	var labels []string
	for _, a := range m.Actions {
		labels = append(labels, a.Label)
	}
	want := []string{
		"Open: Подписаться (max.ru/hse_official)",
		"Open: Подписаться на «Афишу Вышки» (max.ru/HSEAfisha)",
		"Open: Подписаться на «Вышку IQ» (max.ru/iq_media_hse)",
		"Open: Все каналы Вышки в Макс (hse.ru/hse_max)",
		"Copy link",
		"Open cover image",
		"Open publisher image",
	}
	if strings.Join(labels, "\n") != strings.Join(want, "\n") {
		t.Errorf("menu labels:\n%s", strings.Join(labels, "\n"))
	}
	if m.Title != "Вышка в Макс" {
		t.Errorf("menu title %q", m.Title)
	}
	if !h.RunAction("Афишу") || lastStatus(h) != "Opened https://max.ru/HSEAfisha" {
		t.Errorf("menu action: %q", lastStatus(h))
	}
	if !h.RunAction("publisher image") || !strings.Contains(lastStatus(h), "930691930.jpeg") {
		t.Errorf("publisher image: %q", lastStatus(h))
	}

	// Repeated links are listed once.
	selectPublisher(t, p, "INACCEL’26")
	h.Key("enter")
	m, _ = h.LastMenu()
	opens := 0
	for _, a := range m.Actions {
		if strings.HasPrefix(a.Label, "Open: ") {
			opens++
		}
	}
	if opens != 1 {
		t.Errorf("INACCEL menu has %d open actions, want 1", opens)
	}

	// A story whose first page has no action opens the first page that has one.
	selectPublisher(t, p, "HSE Online")
	h.Key("o")
	if got := lastStatus(h); !strings.HasPrefix(got, "Opened https://online.hse.ru/other/work-desk") {
		t.Errorf("o on HSE Online: %q", got)
	}
}

func TestEdgeCases(t *testing.T) {
	p, h := newPage(t)
	weird := []api.Story{
		{ID: "1", Title: "⠀"},
		{ID: "2", Title: strings.Repeat("Длинный заголовок ", 20), Pages: []api.StoryPage{
			{Action: &api.StoryAction{Title: "", Link: "javascript:alert(1)"}, Image: strings.Repeat("x", 300)},
			{Action: &api.StoryAction{Title: "t", Link: ""}},
			{DatePublished: api.FlexTime{Time: time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)}, Action: &api.StoryAction{Title: "Go", Link: "https://example.com/" + strings.Repeat("a", 200)}},
		}},
		{ID: "3", Title: "Dated", Pages: []api.StoryPage{{DatePublished: api.FlexTime{Time: time.Date(2027, 1, 2, 3, 0, 0, 0, time.UTC)}}}},
	}
	h.Send(ui.Result[[]api.Story]{ID: p.id, Seq: p.load.Seq, Data: weird})
	if p.stories[0].ID != "3" || p.stories[2].ID != "1" {
		t.Errorf("zero dates should sort last: %s %s %s", p.stories[0].ID, p.stories[1].ID, p.stories[2].ID)
	}
	for i := range p.stories {
		p.list.Select(i)
		for _, sz := range [][2]int{{100, 30}, {60, 12}, {60, 10}} {
			checkSize(t, h.View(sz[0], sz[1]), sz[0], sz[1])
		}
		h.Key("o", "y", "i", "enter", "J", "K")
	}
	// The unsafe first link is skipped; o opens the first usable one.
	p.list.Select(1)
	h.Key("o")
	if got := lastStatus(h); !strings.HasPrefix(got, "Opened https://example.com/aaa") {
		t.Errorf("unsafe first link: %q", got)
	}
	t.Logf("edge 100×30:\n%s", h.View(100, 30))

	h.Send(ui.Result[[]api.Story]{ID: p.id, Seq: p.load.Seq, Data: []api.Story{}})
	v := h.View(60, 12)
	checkSize(t, v, 60, 12)
	if !strings.Contains(v, "No news right now") {
		t.Errorf("empty:\n%s", v)
	}
	h.Key("o", "enter", "j", "J")
}

func TestReloadKeepsSelection(t *testing.T) {
	p, h := newPage(t)
	s := selectPublisher(t, p, "Конкурс НИРС")
	seq := p.load.Seq
	h.Send(ui.ReloadMsg{})
	if p.load.Seq != seq+1 {
		t.Errorf("reload didn't refetch")
	}
	if cur, _ := p.selected(); cur.ID != s.ID {
		t.Errorf("selection lost on reload")
	}
	h.Key("r")
	if p.load.Seq != seq+2 {
		t.Errorf("r didn't refetch")
	}
}

func TestShortURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://max.ru/hse_official":                     "max.ru/hse_official",
		"https://inaccel.ingroup-sts.ru/?utm_source=corp": "inaccel.ingroup-sts.ru",
		"https://www.hse.ru/hse_max/":                     "hse.ru/hse_max",
		"not a url":                                       "not a url",
		"https://example.com/" + strings.Repeat("a", 100): "example.com/" + strings.Repeat("a", 27) + "…",
	} {
		if got := ShortURL(in); got != want {
			t.Errorf("ShortURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBalanceSGR(t *testing.T) {
	in := "a \x1b[2mone\ntwo\x1b[0m b\nplain"
	got := balanceSGR(in)
	want := "a \x1b[2mone\x1b[0m\n\x1b[2mtwo\x1b[0m b\nplain"
	if got != want {
		t.Errorf("balanceSGR = %q, want %q", got, want)
	}
}
