// Package search is the Search tab: live search over people, groups and
// rooms, with the user's own timetable and favourites when the query is
// empty.
package search

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"hse-app-z/internal/api"
	"hse-app-z/internal/config"
	"hse-app-z/internal/ui"
	"hse-app-z/internal/ui/avatar"
)

// minQuery is the shortest query (in runes) that triggers a search.
const minQuery = 2

// Request pacing (variables so tests can shorten them).
var (
	// debounce is the pause after the last keystroke before searching.
	debounce = 300 * time.Millisecond
	// minGap spaces out the searches fired while typing: a slow typist
	// would otherwise send a request per keystroke, and the API allows
	// only ~60 requests a minute for the whole app.
	minGap = time.Second
	// rateLimitWait is the pause after a 429 that didn't say how long to
	// wait; maxRateLimitWait caps the pause the server asks for.
	rateLimitWait    = 10 * time.Second
	maxRateLimitWait = time.Minute
	// clock is the wall clock used for pacing (ctx.Time is frozen in
	// demo mode).
	clock = time.Now
)

// Async payloads. Distinct types keep the three sources apart even though
// they share the page id.
type (
	hits struct {
		q     string
		items []api.Person
	}
	serverFavs []api.Person
	bannerList []api.Banner
)

// debounceMsg fires debounce after a keystroke; stale ones are ignored.
type debounceMsg struct{ id, seq int }

type rowKind int

const (
	rowHeader rowKind = iota
	rowHint
	rowMe
	rowFav    // a locally starred target
	rowPerson // a search hit or a favourite from the official app
)

type row struct {
	kind   rowKind
	fav    config.Favourite
	person api.Person
	server bool // favourite starred in the official app
	target ui.Target
	ok     bool // target can be opened
}

// key identifies the row across rebuilds (selection preservation).
func (r row) key() string {
	switch r.kind {
	case rowMe:
		return "me"
	case rowFav, rowPerson:
		if r.ok {
			return r.target.Kind + ":" + r.target.Key
		}
		return "id:" + string(r.person.ID) + ":" + r.person.DisplayName()
	}
	return ""
}

// Page is the search tab.
type Page struct {
	ctx *ui.Ctx
	id  int

	input input

	// Search state. results belong to query; inflight is being fetched.
	load     ui.Load
	results  []api.Person
	query    string
	inflight string
	debSeq   int
	dirty    bool // a debounce tick is pending

	lastReq   time.Time // when the last search request was sent
	holdUntil time.Time // rate limited: no automatic search before this
	retrying  bool      // the pending/in-flight search is the retry after a 429

	favLoad ui.Load
	server  []api.Person

	banLoad ui.Load
	banners []api.Banner

	inited bool
	rows   []row
	list   ui.List
	scroll ui.Scroll
	selKey string
}

// New returns the tab page.
func New(ctx *ui.Ctx) ui.Page {
	p := &Page{
		ctx: ctx,
		id:  ui.NewID(),
		input: input{
			Prompt: "/ ",
			Limit:  200,
		},
	}
	p.list.Skip = func(i int) bool {
		return i >= 0 && i < len(p.rows) && (p.rows[i].kind == rowHeader || p.rows[i].kind == rowHint)
	}
	p.rebuild(false)
	return p
}

func (p *Page) Title() string { return ui.Tr("Search", "Поиск") }

// placeholder is the hint shown in the empty query field.
func placeholder() string {
	return ui.Tr("name, email, group (БИБ255) or room…", "ФИО, почта, группа (БИБ255) или аудитория…")
}

// Capturing is true while the query field has focus.
func (p *Page) Capturing() bool { return p.input.Focused() }

func (p *Page) Hints() []ui.Hint {
	if p.input.Focused() {
		return []ui.Hint{
			{Key: "enter", Desc: ui.Tr("go to results", "к результатам")},
			{Key: "esc", Desc: ui.Tr("leave the field", "выйти из поля")},
			{Key: "ctrl+u", Desc: ui.Tr("clear", "очистить")},
			{Key: "ctrl+w", Desc: ui.Tr("delete word", "удалить слово")},
		}
	}
	hints := []ui.Hint{
		{Key: "enter", Desc: ui.Tr("open timetable", "открыть расписание")},
		{Key: "/ i", Desc: ui.Tr("type a query", "ввести запрос")},
		{Key: "f", Desc: ui.Tr("star", "в избранное")},
		{Key: "y", Desc: ui.Tr("copy email", "копировать почту")},
		{Key: ".", Desc: ui.Tr("actions", "действия")},
		{Key: "m", Desc: ui.Tr("map (rooms)", "карта (аудитории)")},
	}
	if b, ok := p.banner(); ok && b.IsDismissible {
		hints = append(hints, ui.Hint{Key: "x", Desc: ui.Tr("dismiss notice", "скрыть уведомление")})
	}
	return append(hints,
		ui.Hint{Key: "r", Desc: ui.Tr("refresh", "обновить")},
		ui.Hint{Key: "J/K", Desc: ui.Tr("scroll details", "прокрутка деталей")})
}

// Init loads favourites and notices. The query field is NOT focused here:
// the tab is often reached with 1-8/tab, and a focused field would swallow
// the next tab-switch digits. "/" (anywhere) and "i" focus it.
func (p *Page) Init() tea.Cmd {
	p.inited = true
	return tea.Batch(p.fetchFavs(), p.fetchBanners())
}

func (p *Page) focusInput() {
	p.input.Focus()
	p.input.CursorEnd()
}

// q is the normalised query (trimmed, inner whitespace collapsed).
func (p *Page) q() string { return strings.Join(strings.Fields(p.input.Value()), " ") }

func searchable(q string) bool { return utf8.RuneCountInString(q) >= minQuery }

// home reports whether the list shows Me + favourites instead of results.
func (p *Page) home() bool { return !searchable(p.q()) }

// ------------------------------------------------------------------ fetching

func (p *Page) fetchFavs() tea.Cmd {
	if p.ctx == nil || p.ctx.API == nil {
		return nil
	}
	client := p.ctx.API
	return ui.Fetch(p.id, p.favLoad.Begin(), func(ctx context.Context) (serverFavs, api.Meta, error) {
		out, meta, err := client.Favourites(ctx)
		return serverFavs(out), meta, err
	})
}

func (p *Page) fetchBanners() tea.Cmd {
	if p.ctx == nil || p.ctx.API == nil {
		return nil
	}
	client := p.ctx.API
	return ui.Fetch(p.id, p.banLoad.Begin(), func(ctx context.Context) (bannerList, api.Meta, error) {
		out, meta, err := client.Banners(ctx, "search")
		return bannerList(out), meta, err
	})
}

// search starts a request for the current query unless its results are
// already shown (or already on the way). force refetches anyway.
func (p *Page) search(force bool) tea.Cmd {
	q := p.q()
	if !searchable(q) || p.ctx == nil || p.ctx.API == nil {
		return nil
	}
	if !force {
		if p.load.Loading && p.inflight == q {
			return nil
		}
		if !p.load.Loading && p.query == q && p.load.Err == nil {
			return nil
		}
	}
	p.inflight = q
	p.lastReq = clock()
	client := p.ctx.API
	return ui.Fetch(p.id, p.load.Begin(), func(ctx context.Context) (hits, api.Meta, error) {
		items, meta, err := client.Search(ctx, q)
		return hits{q: q, items: items}, meta, err
	})
}

// holdOff is how long a search triggered by typing must still wait: at
// least minGap after the previous request, and past a rate-limit pause.
func (p *Page) holdOff() time.Duration {
	until := p.lastReq.Add(minGap)
	if p.holdUntil.After(until) {
		until = p.holdUntil
	}
	return until.Sub(clock())
}

// held reports whether a pending search waits out a rate-limit pause.
func (p *Page) held() bool { return p.dirty && clock().Before(p.holdUntil) }

// tick fires a debounceMsg for the current edit after d.
func (p *Page) tick(d time.Duration) tea.Cmd {
	id, seq := p.id, p.debSeq
	return tea.Tick(d, func(time.Time) tea.Msg { return debounceMsg{id: id, seq: seq} })
}

// rateLimited reacts to a 429: searches triggered by typing pause until
// the server is ready again, and a search that failed outright is retried
// once after the pause (the user may simply stop typing and wait).
func (p *Page) rateLimited(err error, meta api.Meta) tea.Cmd {
	var rl *api.RateLimitError
	switch {
	case errors.As(err, &rl):
		wait := rl.RetryAfter
		if wait <= 0 {
			wait = rateLimitWait
		}
		p.holdUntil = clock().Add(min(wait, maxRateLimitWait))
	case err == nil && meta.Stale && meta.StaleReason == "rate limited":
		// Cached results are shown; just don't make it worse.
		p.holdUntil = clock().Add(rateLimitWait)
		return nil
	default:
		return nil
	}
	if p.retrying || p.dirty {
		// Already retried once, or a newer edit is waiting anyway.
		p.retrying = false
		return nil
	}
	p.retrying = true
	p.dirty = true
	p.debSeq++
	return p.tick(p.holdOff())
}

// cancelSearch drops the in-flight request (its result will be ignored).
func (p *Page) cancelSearch() {
	if p.load.Loading {
		p.load.Seq++
		p.load.Loading = false
	}
	p.inflight = ""
}

// queryChanged reacts to an edit: schedules a debounced search, or goes
// back to the favourites list for short queries.
func (p *Page) queryChanged() tea.Cmd {
	p.debSeq++ // invalidates pending ticks
	p.retrying = false
	q := p.q()
	defer p.rebuild(false)
	if !searchable(q) {
		p.dirty = false
		p.cancelSearch()
		return nil
	}
	if q == p.query && p.load.Err == nil {
		// Back to the query whose results we already have.
		p.dirty = false
		p.cancelSearch()
		return nil
	}
	p.dirty = true
	return p.tick(max(debounce, p.holdOff()))
}

// searchNow skips the debounce and any hold-off (enter in the field: the
// user asked explicitly).
func (p *Page) searchNow() tea.Cmd {
	p.debSeq++
	p.dirty = false
	p.retrying = false
	return p.search(false)
}

func (p *Page) refresh() tea.Cmd {
	cmds := []tea.Cmd{p.fetchFavs(), p.fetchBanners()}
	if !p.home() {
		p.debSeq++
		p.dirty = false
		p.retrying = false
		cmds = append(cmds, p.search(true))
	}
	return tea.Batch(cmds...)
}

// ------------------------------------------------------------------ update

func (p *Page) Update(msg tea.Msg) tea.Cmd {
	cmd := p.update(msg)
	// The selection may have changed (keys, new results, favourites):
	// start loading the selected person's picture. Request is a no-op for
	// loaded, loading or disabled images.
	if a := avatar.Request(p.avatarURL()); a != nil {
		return tea.Batch(cmd, a)
	}
	return cmd
}

// avatarURL is the picture of the selected row ("" when it has none).
func (p *Page) avatarURL() string {
	r, ok := p.selected()
	if !ok {
		return ""
	}
	return rowAvatar(r)
}

// rowAvatar is a person hit's picture URL; groups, rooms, Me and local
// favourites have none.
func rowAvatar(r row) string {
	if r.kind != rowPerson || !r.person.IsPerson() {
		return ""
	}
	return strings.TrimSpace(r.person.AvatarURL)
}

func (p *Page) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case ui.FocusSearchMsg:
		p.inited = true
		p.focusInput()
		return nil

	case ui.ReloadMsg:
		return p.refresh()

	case debounceMsg:
		if msg.id != p.id || msg.seq != p.debSeq {
			return nil
		}
		if d := p.holdOff(); d > 0 {
			return p.tick(d) // too soon after the last request: wait
		}
		p.dirty = false
		return p.search(false)

	case ui.Result[hits]:
		if msg.ID != p.id || !p.load.Accept(msg.Seq) {
			return nil
		}
		cmd := p.load.Done(msg.Meta, msg.Err)
		p.inflight = ""
		if msg.Err == nil {
			p.results = msg.Data.items
			p.query = msg.Data.q
		}
		retry := p.rateLimited(msg.Err, msg.Meta)
		if msg.Err == nil {
			p.retrying = false
		}
		p.rebuild(false)
		return tea.Batch(cmd, retry)

	case ui.Result[serverFavs]:
		if msg.ID != p.id || !p.favLoad.Accept(msg.Seq) {
			return nil
		}
		cmd := p.favLoad.Done(msg.Meta, msg.Err)
		if msg.Err == nil {
			p.server = msg.Data
		}
		p.rebuild(true)
		return cmd

	case ui.Result[bannerList]:
		if msg.ID != p.id || !p.banLoad.Accept(msg.Seq) {
			return nil
		}
		cmd := p.banLoad.Done(msg.Meta, msg.Err)
		if msg.Err == nil {
			p.banners = msg.Data
		}
		return cmd

	case tea.KeyMsg:
		if p.input.Focused() {
			return p.inputKey(msg)
		}
		return p.listKey(msg)
	}
	return nil
}

func (p *Page) inputKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		p.input.Blur()
		return nil
	case "enter":
		cmd := p.searchNow()
		if p.list.HasSelection() || cmd != nil || p.load.Loading {
			p.input.Blur()
		}
		return cmd
	case "down", "ctrl+n":
		if p.list.HasSelection() {
			p.input.Blur()
		}
		return nil
	case "tab":
		// Always leaves the field, so a second tab switches tabs as
		// everywhere else (even when there is nothing to select).
		p.input.Blur()
		return nil
	case "up", "ctrl+p", "shift+tab":
		return nil
	}
	before := p.input.Value()
	if !p.input.Update(msg) || p.input.Value() == before {
		return nil
	}
	return p.queryChanged()
}

func (p *Page) listKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "i", "esc":
		p.focusInput()
		return nil
	case "enter", "o":
		return p.open()
	case ".", "a":
		return p.actions()
	case "f":
		r, ok := p.selected()
		if !ok {
			return nil
		}
		return p.toggle(r)
	case "y":
		r, ok := p.selected()
		if !ok {
			return nil
		}
		text, what := copyOf(r)
		if text == "" {
			return ui.Warn("%s", ui.Tr("Nothing to copy", "Нечего копировать"))
		}
		return ui.Copy(text, what.name())
	case "m":
		r, ok := p.selected()
		if !ok {
			return nil
		}
		return openMap(r)
	case "x":
		return p.dismissBanner()
	case "r":
		return p.refresh()
	}
	if p.list.HandleKey(msg) {
		p.syncSel()
		return nil
	}
	p.scroll.HandleKey(msg)
	return nil
}

// ---------------------------------------------------------------- actions

func (p *Page) selected() (row, bool) {
	if !p.list.HasSelection() || p.list.Cursor >= len(p.rows) {
		return row{}, false
	}
	return p.rows[p.list.Cursor], true
}

func (p *Page) open() tea.Cmd {
	r, ok := p.selected()
	if !ok {
		return nil
	}
	if !r.ok {
		return ui.Warn(ui.Tr("%s has no email — no timetable to open", "%s: нет почты — расписание недоступно"), ui.Clean(r.person.DisplayName()))
	}
	return ui.OpenTarget(r.target)
}

func (p *Page) starred(r row) bool {
	if !r.ok || p.ctx == nil || p.ctx.Favs == nil {
		return false
	}
	return p.ctx.Favs.Has(r.target.Kind, r.target.Key)
}

func (p *Page) toggle(r row) tea.Cmd {
	if !r.ok {
		return ui.Warn(ui.Tr("Can't star %s: no email", "Нельзя добавить в избранное %s: нет почты"), ui.Clean(r.person.DisplayName()))
	}
	if p.ctx == nil || p.ctx.Favs == nil {
		return nil
	}
	cmd := ui.ToggleFavourite(p.ctx, r.target)
	p.rebuild(true)
	return cmd
}

func (p *Page) actions() tea.Cmd {
	r, ok := p.selected()
	if !ok {
		return nil
	}
	var acts []ui.Action
	if r.ok {
		t := r.target
		acts = append(acts, ui.Action{Key: "o", Label: ui.Tr("Open timetable", "Открыть расписание"), Run: func() tea.Cmd { return ui.OpenTarget(t) }})
		label := ui.Tr("Star", "Добавить в избранное")
		if p.starred(r) {
			label = ui.Tr("Unstar", "Убрать из избранного")
		}
		acts = append(acts, ui.Action{Key: "f", Label: label, Run: func() tea.Cmd { return p.toggle(r) }})
	}
	if text, what := copyOf(r); text != "" {
		name := what.name()
		acts = append(acts, ui.Action{Key: "y", Label: what.action(), Run: func() tea.Cmd { return ui.Copy(text, name) }})
	}
	if _, ok := roomLocation(r); ok {
		acts = append(acts, ui.Action{Key: "m", Label: ui.Tr("Open map", "Открыть карту"), Run: func() tea.Cmd { return openMap(r) }})
	}
	if len(acts) == 0 {
		return ui.Warn("%s", ui.Tr("Nothing to do with this entry", "С этой записью ничего не сделать"))
	}
	return ui.ShowMenu(rowTitle(r), acts...)
}

// copyWhat names what y copies.
type copyWhat int

const (
	copyEmail copyWhat = iota
	copyGroupID
	copyRoomID
)

// name is the noun for the "Copied …" status.
func (c copyWhat) name() string {
	switch c {
	case copyGroupID:
		return ui.Tr("group id", "ID группы")
	case copyRoomID:
		return ui.Tr("room id", "ID аудитории")
	}
	return ui.Tr("email", "почта")
}

// action is the action-menu label.
func (c copyWhat) action() string {
	switch c {
	case copyGroupID:
		return ui.Tr("Copy group id", "Копировать ID группы")
	case copyRoomID:
		return ui.Tr("Copy room id", "Копировать ID аудитории")
	}
	return ui.Tr("Copy email", "Копировать почту")
}

// hint is the long key hint under the preview ("y copy email").
func (c copyWhat) hint() string {
	switch c {
	case copyGroupID:
		return ui.Tr("y copy group id", "y копировать ID группы")
	case copyRoomID:
		return ui.Tr("y copy room id", "y копировать ID аудитории")
	}
	return ui.Tr("y copy email", "y копировать почту")
}

// copyOf is what y copies: the email for people, the RUZ id otherwise.
func copyOf(r row) (text string, what copyWhat) {
	kind := r.target.Kind
	if r.kind == rowPerson && !r.ok {
		kind = config.KindPerson
	}
	switch kind {
	case config.KindGroup:
		return r.target.Key, copyGroupID
	case config.KindAuditorium:
		return r.target.Key, copyRoomID
	}
	if r.kind == rowPerson {
		return strings.TrimSpace(r.person.Email), copyEmail
	}
	return r.target.Key, copyEmail
}

func roomLocation(r row) (*api.GeoPoint, bool) {
	if r.kind != rowPerson || !r.person.IsAuditorium() || !realPoint(r.person.AuditoriumLocation) {
		return nil, false
	}
	return r.person.AuditoriumLocation, true
}

// realPoint rejects missing, out-of-range and 0,0 placeholder points.
func realPoint(g *api.GeoPoint) bool {
	if !g.Valid() {
		return false
	}
	lat, lng := g.Lat(), g.Lng()
	return lat >= -90 && lat <= 90 && lng >= -180 && lng <= 180 && (lat != 0 || lng != 0)
}

func openMap(r row) tea.Cmd {
	loc, ok := roomLocation(r)
	if !ok {
		if r.target.Kind == config.KindAuditorium {
			return ui.Warn("%s", ui.Tr("No location for this room", "Местоположение аудитории неизвестно"))
		}
		return ui.Warn("%s", ui.Tr("Maps are available for rooms only", "Карта есть только у аудиторий"))
	}
	return ui.OpenURL(ui.MapURL(loc.Lat(), loc.Lng()))
}

// banner is the first enabled notice the user hasn't dismissed.
func (p *Page) banner() (api.Banner, bool) {
	for _, b := range p.banners {
		if !b.Enabled() || strings.TrimSpace(ui.Clean(b.Title)+ui.Clean(b.Description)) == "" {
			continue
		}
		if p.ctx != nil && p.ctx.Settings != nil && p.ctx.Settings.BannerDismissed(bannerKey(b)) {
			continue
		}
		return b, true
	}
	return api.Banner{}, false
}

func bannerKey(b api.Banner) string {
	if b.ID != "" {
		return string(b.ID)
	}
	return "title:" + b.Title
}

func (p *Page) dismissBanner() tea.Cmd {
	b, ok := p.banner()
	if !ok {
		return nil
	}
	if !b.IsDismissible {
		return ui.Info("%s", ui.Tr("This notice can't be dismissed", "Это уведомление нельзя скрыть"))
	}
	if p.ctx != nil && p.ctx.Settings != nil {
		p.ctx.Settings.DismissBanner(bannerKey(b))
	}
	return nil
}

// ------------------------------------------------------------------- rows

// rebuild recomputes the rows for the current mode, keeping the selected
// entry when it is still listed (else the same index with keepIndex, or
// the top).
func (p *Page) rebuild(keepIndex bool) {
	prevKey := ""
	if r, ok := p.selected(); ok {
		prevKey = r.key()
	}
	prevIdx := p.list.Cursor
	if p.home() {
		p.rows = p.homeRows()
	} else {
		p.rows = p.resultRows()
	}
	p.list.SetLen(len(p.rows))
	idx := -1
	if prevKey != "" {
		for i, r := range p.rows {
			if r.key() == prevKey {
				idx = i
				break
			}
		}
	}
	switch {
	case idx >= 0:
		p.list.Select(idx)
	case keepIndex:
		p.list.Select(prevIdx)
	default:
		p.list.Select(0)
	}
	p.syncSel()
}

// syncSel resets the detail scroll when the selection changed.
func (p *Page) syncSel() {
	k := ""
	if r, ok := p.selected(); ok {
		k = r.key()
	}
	if k != p.selKey {
		p.selKey = k
		p.scroll.Reset()
	}
}

func (p *Page) homeRows() []row {
	var rows []row
	if p.ctx != nil {
		if email := strings.ToLower(strings.TrimSpace(p.ctx.Me.Email)); email != "" {
			name := ui.Clean(p.ctx.Me.Name)
			if name == "" {
				name = ui.Clean(email)
			}
			t := ui.Target{Kind: config.KindPerson, Key: email, Title: ui.Tr("Me", "Я") + " · " + name}
			rows = append(rows, row{kind: rowMe, target: t, ok: true})
		}
	}
	rows = append(rows, row{kind: rowHeader})
	n := len(rows)
	seen := map[string]bool{}
	if p.ctx != nil && p.ctx.Favs != nil {
		for _, f := range p.ctx.Favs.List() {
			t := ui.TargetFromFavourite(f)
			k := t.Kind + ":" + t.Key
			if seen[k] {
				continue
			}
			seen[k] = true
			rows = append(rows, row{kind: rowFav, fav: f, target: t, ok: true})
		}
	}
	for _, sp := range p.server {
		t, ok := ui.TargetFromPerson(sp)
		if !ok || seen[t.Kind+":"+t.Key] {
			continue
		}
		seen[t.Kind+":"+t.Key] = true
		rows = append(rows, row{kind: rowPerson, person: sp, server: true, target: t, ok: true})
	}
	if len(rows) == n {
		rows = append(rows, row{kind: rowHint})
	}
	return rows
}

func (p *Page) resultRows() []row {
	rows := make([]row, 0, len(p.results))
	for _, r := range p.results {
		t, ok := ui.TargetFromPerson(r)
		rows = append(rows, row{kind: rowPerson, person: r, target: t, ok: ok})
	}
	return rows
}
