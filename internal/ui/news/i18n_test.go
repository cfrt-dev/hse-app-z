package news

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

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

// englishWords are English UI words this package itself writes (English
// publisher names are replaced in these tests, see russify).
var englishWords = []string{
	"News", "Publisher", "Published", "Pages", "no link", "link", "open link", "all actions",
	"cover image", "Story", "No news", "Open", "Copy", "image", "publisher",
	"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun",
	"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec",
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
	return regexp.MustCompile(`(https?://|[a-z0-9-]+\.(ru|com|me)/)\S*`).ReplaceAllString(s, "")
}

func checkRU(t *testing.T, what, s string) {
	t.Helper()
	if l := leftovers(noURLs(s), englishWords); len(l) > 0 {
		t.Errorf("%s: English words %q left in:\n%s", what, l, s)
	}
}

// russify gives the fixture stories Russian publisher names.
func russify(stories []api.Story) []api.Story {
	out := append([]api.Story(nil), stories...)
	for i := range out {
		out[i].Publisher.Name = fmt.Sprintf("Издатель №%d", i+1)
	}
	return out
}

func TestRussian(t *testing.T) {
	setRU(t)
	p, h := newPage(t)
	if p.Title() != "Новости" {
		t.Errorf("title %q", p.Title())
	}
	h.Send(ui.Result[[]api.Story]{ID: p.id, Seq: p.load.Seq, Data: russify(p.stories)})
	for i := range p.stories {
		p.list.Select(i)
		p.scroll.Reset()
		for _, sz := range [][2]int{{100, 30}, {60, 12}} {
			v := h.View(sz[0], sz[1])
			checkSize(t, v, sz[0], sz[1])
			checkRU(t, fmt.Sprintf("story %d %dx%d", i, sz[0], sz[1]), v)
		}
		s, _ := p.selected()
		checkRU(t, fmt.Sprintf("detail %d", i), p.detail(s, 60))
	}
	p.list.Select(0)
	v := h.View(100, 30)
	t.Logf("news RU 100×30:\n%s", v)
	for _, want := range []string{"Новости", "05 окт", "02 сен", "Источник", "Публикация", "Страницы", "нет ссылки", "o открыть ссылку · enter все действия"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q", want)
		}
	}

	// Keys, statuses and the menu.
	for i, s := range p.stories {
		if s.Title == "Вышка в Макс" {
			p.list.Select(i)
		}
	}
	h.Key("enter")
	m, ok := h.LastMenu()
	if !ok || len(m.Actions) != 7 {
		t.Fatalf("menu %+v", m)
	}
	for _, a := range m.Actions {
		checkRU(t, "menu", a.Label)
	}
	if m.Actions[0].Label != "Открыть: Подписаться (max.ru/hse_official)" || m.Actions[4].Label != "Копировать ссылку" {
		t.Errorf("menu labels %q / %q", m.Actions[0].Label, m.Actions[4].Label)
	}
	for _, hint := range p.Hints() {
		if regexp.MustCompile(`[A-Za-z]`).MatchString(hint.Desc) {
			t.Errorf("hint %q: %q", hint.Key, hint.Desc)
		}
	}

	// A story without links or images; dates in another year.
	h.Send(ui.Result[[]api.Story]{ID: p.id, Seq: p.load.Seq, Data: []api.Story{
		{ID: "1", Title: "⠀", Pages: []api.StoryPage{
			{DatePublished: api.FlexTime{Time: time.Date(2025, 5, 4, 10, 0, 0, 0, api.Moscow)}},
			{DatePublished: api.FlexTime{Time: time.Date(2025, 5, 6, 10, 0, 0, 0, api.Moscow)}, Action: &api.StoryAction{Link: "https://example.com"}},
		}},
	}})
	v = h.View(100, 30)
	for _, want := range []string{"Новость", "04 мая", "Вс 4 мая 2025", "Вт 6 мая 2025", "2. ссылка → https://example.com"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	checkRU(t, "linkless", v)
	h.Key("i")
	if got := lastStatus(h); got != "У этой новости нет обложки" {
		t.Errorf("i: %q", got)
	}
	h.Send(ui.Result[[]api.Story]{ID: p.id, Seq: p.load.Seq, Data: []api.Story{{ID: "2", Title: "Пусто"}}})
	h.Key("o")
	if got := lastStatus(h); got != "У этой новости нет ссылки" {
		t.Errorf("o: %q", got)
	}
	h.Key("enter")
	if got := lastStatus(h); got != "В этой новости нечего открыть" {
		t.Errorf("enter: %q", got)
	}

	// Empty.
	h.Send(ui.Result[[]api.Story]{ID: p.id, Seq: p.load.Seq, Data: []api.Story{}})
	if v := h.View(60, 12); !strings.Contains(v, "Новостей пока нет") {
		t.Errorf("empty:\n%s", v)
	}

	// Back to English on the next frame.
	ui.SetLang("en")
	h.Send(ui.Result[[]api.Story]{ID: p.id, Seq: p.load.Seq, Data: []api.Story{{ID: "1", Title: "⠀"}}})
	if v := h.View(100, 30); !strings.Contains(v, "Story") || !strings.Contains(v, "News") {
		t.Errorf("English after switching back:\n%s", v)
	}
}
