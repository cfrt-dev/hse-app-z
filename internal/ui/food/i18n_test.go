package food

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"hse-app-z/internal/api"
	"hse-app-z/internal/ui"
	"hse-app-z/internal/ui/uitest"
)

// englishUI are words this package (or the open-status labels it shows)
// writes itself. Fixture data is partly English ("St. Petersburg, …",
// "Building А", dish names), so data words are not listed.
var englishUI = regexp.MustCompile(`\b(Cafés|Cafe|Café|cafes|All|Moscow|Nizhny|Novgorod|Perm|SPb|open|closed|until|opens|since|today|tomorrow|Today|Hours|hours|Address|Directions|Where|Load|Menu|menu|available|Photos|photos|photo|Map|map|floor|room|Closed|Local|Nothing|changes|shows|city|Opening|published|Price|Weight|Calories|Tags|Section|Composition|Unnamed|for the|other days|Loading|vegan|veg|diet|spicy|lent|Mon|Tue|Wed|Thu|Fri|Sat|Sun|Monday|Tuesday|Wednesday|Thursday|Friday|Saturday|Sunday|n/a|listed|right now|refresh|actions|scroll|address|previous|next day)\b`)

func setRU(t *testing.T) {
	t.Helper()
	ui.SetLang("ru")
	t.Cleanup(func() { ui.SetLang("en") })
}

func noEnglish(t *testing.T, where, text string) {
	t.Helper()
	if m := englishUI.FindAllString(text, -1); len(m) > 0 {
		t.Errorf("%s: English UI words %q in:\n%s", where, m, text)
	}
}

// ruViews renders p at 100×30 and 60×12 (checking sizes) and collects
// the views and hints.
type ruViews struct {
	t     *testing.T
	views map[string]string
}

func (r *ruViews) render(name string, p ui.Page) string {
	r.t.Helper()
	var big string
	for _, sz := range [][2]int{{100, 30}, {60, 12}} {
		v := checkSize(r.t, p, sz[0], sz[1])
		r.views[fmt.Sprintf("%s %dx%d", name, sz[0], sz[1])] = v
		if big == "" {
			big = v
		}
	}
	r.views[name+" title"] = p.Title()
	for _, h := range p.Hints() {
		r.views[name+" hint "+h.Key] = h.Desc
	}
	return big
}

func wantAll(t *testing.T, where, v string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(v, w) {
			t.Errorf("%s lacks %q:\n%s", where, w, v)
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

func TestRussianInterface(t *testing.T) {
	setRU(t)
	rv := &ruViews{t: t, views: map[string]string{}}
	p, h := newFood(t)
	if p.Title() != "Еда" {
		t.Errorf("title %q", p.Title())
	}
	v := rv.render("all", p)
	wantAll(t, "all cities", v, "Кафе · Все города", "открыто", "Столовая на Саратовском", "открыто · до", "Часы работы", "Вт   ", "сегодня", "Сегодня")
	t.Logf("food 100x30:\n%s", v)

	// City filters: detection works on the raw (English) data.
	h.Key("c")
	wantAll(t, "Moscow", rv.render("moscow", p), "Кафе · Москва")
	h.Key("c")
	v = rv.render("spb", p)
	wantAll(t, "SPb", v, "Кафе · Санкт-Петербург", " ul.Kantemirovskaya 3")
	if strings.Contains(v, " St. Petersburg, ul.Kantemirovskaya") {
		t.Errorf("city prefix should be dropped under the city filter:\n%s", v)
	}
	h.Key("c")
	wantAll(t, "NN", rv.render("nn", p), "Кафе · Нижний Новгород", " B. Pecherskaya, 25/12")
	if v := h.View(60, 12); !strings.Contains(v, "Кафе · Н. Новгород") {
		t.Errorf("short city name:\n%s", v)
	}
	h.Key("c")
	v = rv.render("perm", p)
	wantAll(t, "Perm", v, "Кафе · Пермь", " Studencheskaya, 38", "Время", "14:30 UTC+5")
	h.Key("c")
	wantAll(t, "all again", h.View(100, 30), "Пермь, Lebedeva, 27", "Нижний Новгород, B. Pecherskaya, 25/12")

	// Actions and statuses.
	h.Key("g", ".")
	m, ok := h.LastMenu()
	if !ok {
		t.Fatal("no action menu")
	}
	var labels []string
	for _, a := range m.Actions {
		labels = append(labels, a.Label)
	}
	if got := strings.Join(labels, " | "); got != "Меню | Открыть карту | Открыть фото | Копировать адрес" {
		t.Errorf("menu labels %q", got)
	}
	h.Key("y")
	if st := lastStatus(h); !strings.HasPrefix(st, "Скопировано: адрес") {
		t.Errorf("copy status %q", st)
	}

	// Open now only.
	h.Key("o")
	wantAll(t, "open only", rv.render("open only", p), "открыто сейчас")
	h.Key("c", "c", "c", "c")
	pl := p.places
	p.places = []place{{group: api.CafeGroup{CampusName: "P Lebedeva, 27", Coordinates: &api.LatLng{Lat: api.NewNum(58), Lng: api.NewNum(56.2)},
		Cafes: []api.Cafe{{Name: "Закрытое", OpeningHours: []api.OpeningHours{{DayOfWeek: "tuesday"}}}}}, city: cityPerm}}
	p.rebuild()
	wantAll(t, "nothing open", rv.render("nothing open", p), "Сейчас в Перми ничего не открыто", "o — все кафе · c — сменить город")
	h.Key("o", "c", "c")
	wantAll(t, "no cafes", rv.render("no cafes", p), "Нет кафе в Москве", "c — сменить город")
	h.Key("c", "c", "c", "c")
	p.places = pl
	p.rebuild()

	// Hand-made edge cafés: closed dates, navigation, load, banner.
	hours := []api.OpeningHours{
		{DayOfWeek: "monday", IsOpen: true, StartTime: "09:00", EndTime: "14:00"},
		{DayOfWeek: "tuesday", IsOpen: true, StartTime: "09:00", EndTime: "14:00"},
		{DayOfWeek: "sunday", IsOpen: true},
	}
	perm := &api.LatLng{Lat: api.NewNum(58), Lng: api.NewNum(56.2)}
	h.Send(ui.Result[[]api.CafeGroup]{ID: p.id, Seq: p.load.Begin(), Data: []api.CafeGroup{
		{CampusName: "P Lebedeva, 27", Coordinates: perm, Cafes: []api.Cafe{{ID: "p1", Name: "Пермская столовая", OpeningHours: hours}}},
		{CampusName: "Pokrovsky", Cafes: []api.Cafe{
			{ID: "m1", Name: "Кафе на Покровке", OpeningHours: hours, HasMenu: true, Photos: []string{"https://x.example/1.jpg", "https://x.example/2.jpg"},
				ClosedDates: []string{"2026-10-14", "2026-10-15", "2026-10-16", "2026-10-20"},
				Navigation:  &api.CafeNavigation{Room: "104", Floor: "1"}, CurrentLoad: "средняя"},
			{ID: "m2", Name: ""},
		}},
	}})
	v = rv.render("edge closed", p)
	wantAll(t, "closed café", v, "закрыто · откроется", "Вс   открыто")
	h.Key("j")
	v = rv.render("edge", p)
	wantAll(t, "edge café", v, "14 окт – 16 окт, 20 окт", "этаж 1, пом. 104", "Загрузка", "средняя", "Закрыто", "Пн   09:00–14:00",
		"Ср   —", "Фото", "2 фото — p, чтобы открыть", "есть — enter, чтобы открыть")
	h.Key("m")
	if st := lastStatus(h); st != "Местоположение кафе неизвестно" {
		t.Errorf("map status %q", st)
	}
	h.Key("j")
	v = rv.render("unknown", p)
	wantAll(t, "unknown hours", v, "часы неизвестны", "Кафе", "Часы работы не опубликованы")
	h.Key("M")
	if st := lastStatus(h); st != "У этого кафе нет меню" {
		t.Errorf("no-menu status %q", st)
	}
	h.Key("p")
	if st := lastStatus(h); st != "У этого кафе нет фото" {
		t.Errorf("photo status %q", st)
	}

	// Errors (the core state view) keep the page title Russian.
	h.Send(ui.Result[[]api.CafeGroup]{ID: p.id, Seq: p.load.Begin(), Data: nil})
	rv.render("empty", p)

	for name, v := range rv.views {
		noEnglish(t, name, v)
	}
}

func TestRussianMenu(t *testing.T) {
	setRU(t)
	rv := &ruViews{t: t, views: map[string]string{}}
	p, h := newFood(t)
	h.Key("enter")
	mp := h.LastPushed()
	if mp == nil {
		t.Fatal("enter should push the menu page")
	}
	if !strings.HasPrefix(mp.Title(), "Меню · Столовая на Саратовском") {
		t.Fatalf("title %q", mp.Title())
	}
	mh := uitest.New(t, mp)
	v := rv.render("menu", mp)
	wantAll(t, "menu", v, "Вторник · сегодня", "“FIX” set — 280 ₽", "Состав")
	t.Logf("menu 100x30:\n%s", v)
	mh.Key("l")
	if v := rv.render("wednesday", mp); !strings.Contains(v, "Среда") || strings.Contains(v, "сегодня") {
		t.Errorf("l should go to Wednesday:\n%s", v)
	}
	mh.Key("h", "h", "h")
	if st := lastStatus(mh); st != "Раньше понедельника меню нет" {
		t.Errorf("first-day note %q", st)
	}
	mh.Key("t")

	// Item details.
	page := mp.(*menuPage)
	for i, r := range page.rows {
		if !r.header() && page.menu.Sections[r.sec].Items[r.item].ItemName == "Herculean milk porridge with butter" {
			page.list.Select(i)
			break
		}
	}
	wantAll(t, "item", rv.render("item", mp), "Цена", "54 ₽", "Раздел")

	// Hand-made menus.
	mh.Send(ui.Result[api.Menu]{ID: page.id, Seq: page.load.Begin(), Data: api.Menu{CurrentDay: "wednesday", Sections: []api.MenuSection{
		{SectionName: "Set", Price: api.NewNum(199.5), Items: []api.MenuItem{{ItemName: "Soup", Chips: []string{"вегетарианское", "острое", "веганское", "постное", "диетическое"}}}},
		{Items: []api.MenuItem{{}}},
	}}})
	v = rv.render("set menu", mp)
	wantAll(t, "set menu", v, "Среда", "199.50 ₽ за «Set»", "вегет", "остр", "веган", "пост", "диет", "Меню", "Метки")
	mh.Key("j")
	wantAll(t, "unnamed", rv.render("unnamed", mp), "Блюдо без названия")
	mh.Send(ui.Result[api.Menu]{ID: page.id, Seq: page.load.Begin(), Data: api.Menu{CurrentDay: "wednesday"}})
	wantAll(t, "empty day", rv.render("empty day", mp), "Нет меню на среду", "h/l другие дни")
	mh.Send(ui.Result[api.Menu]{ID: page.id, Seq: page.load.Begin(), Err: &api.APIError{Status: 404}})
	wantAll(t, "404", rv.render("404", mp), "Меню недоступно", "t сегодня · h/l другие дни")
	page.day, page.load.Loading = "friday", true
	wantAll(t, "switching", rv.render("switching", mp), "Пятница", "Загрузка…")
	page.load.Loading = false

	for name, v := range rv.views {
		noEnglish(t, name, v)
	}
	_ = p
}

func TestCityDetectionIgnoresLanguage(t *testing.T) {
	setRU(t)
	cases := map[string]city{
		"St. Petersburg, ul.Sedova": citySPb, "СПб, Седова": citySPb, "N.N., Rodionova": cityNN, "Н.Н., Родионова": cityNN,
		"Пермь, Лебедева": cityPerm, "Perm, Lebedeva": cityPerm, "P Lebedeva, 27": cityPerm, "Moscow, Myasnitskaya": cityMoscow,
		"Москва, Мясницкая": cityMoscow, "Myasnitskaya": cityOther,
	}
	for name, want := range cases {
		if got := cityOf(api.CafeGroup{CampusName: name}); got != want {
			t.Errorf("%q: got %d want %d", name, got, want)
		}
	}
	// Order is the same in both languages.
	p, _ := newFood(t)
	var ru []string
	for _, pl := range p.places {
		ru = append(ru, pl.group.CampusName)
	}
	ui.SetLang("en")
	p2, _ := newFood(t)
	ui.SetLang("ru")
	for i, pl := range p2.places {
		if pl.group.CampusName != ru[i] {
			t.Fatalf("order differs at %d: %q vs %q", i, pl.group.CampusName, ru[i])
		}
	}
	if got := buildingName(api.CafeGroup{CampusName: "N.N., Rodionova 136"}, cityNN); got != "Нижний Новгород, Rodionova 136" {
		t.Errorf("NN building %q", got)
	}
	if got := trimCity("St. Petersburg, ul.Sedova", citySPb); got != "ul.Sedova" {
		t.Errorf("trim %q", got)
	}
	if got := trimCity("Санкт-Петербург, Седова", citySPb); got != "Седова" {
		t.Errorf("trim RU %q", got)
	}
}
