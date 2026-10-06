package timetable

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"hse-app-z/internal/api"
	"hse-app-z/internal/ui"
)

// want is the selection to apply once the shown week's data is available.
type want int

const (
	wantKeep    want = iota // keep the selected lesson (refresh)
	wantNow                 // ongoing → next upcoming → last → first
	wantFirst               // first lesson of the week
	wantLastDay             // first lesson of the last day with lessons
)

// weekResult is the payload of a week fetch.
type weekResult struct {
	Key     string // Monday, YYYY-MM-DD
	Gen     int    // cache generation (bumped on reload)
	Lessons []api.Lesson
}

type weekState struct {
	lessons []api.Lesson
	loaded  bool
	err     error
	meta    api.Meta
	at      time.Time // when the last request for the week finished
	gen     int       // cache generation the data was fetched in
}

// weekTTL is how long a fetched week is shown without asking the server
// again: the app stays open for days while rooms and times change (the
// HTTP cache's ETags make the re-check cheap).
const weekTTL = 10 * time.Minute

// old reports whether the last request for a week (successful or not)
// finished long ago.
func (w *week) old(st *weekState) bool {
	d := w.now().Sub(st.at)
	return d > weekTTL || d < -weekTTL
}

// stale reports whether a cached week should be re-checked when shown:
// fetched before a reload (other language, other account) or long ago.
func (w *week) stale(st *weekState) bool { return st.gen != w.gen || w.old(st) }

// row is a list row: a day header or a lesson (index into week.lessons).
type row struct {
	header bool
	day    time.Time
	idx    int
}

// week is the reusable Mon–Sun timetable: a list of lessons grouped by day
// on the left and the selected lesson's details on the right. It belongs to
// a page (id) and is driven by that page's Update/View.
type week struct {
	ctx   *ui.Ctx
	id    int
	query func() api.LessonQuery
	// self is the page's own target (actions that would reopen it are hidden).
	self ui.Target

	enabled bool
	// blocked is shown instead of the list while disabled (nil = loading);
	// a func so the text follows the interface language.
	blocked func() string

	mon        time.Time // Monday of the shown week (civil date)
	cache      map[string]*weekState
	gen        int
	sig        string // selector of the cached data
	load       ui.Load
	loadingKey string
	want       want

	lessons []api.Lesson
	rows    []row
	list    ui.List
	scroll  ui.Scroll
	// moved is set when the user navigated (the target page then switches
	// from the profile to the lesson details).
	moved bool
}

func newWeek(ctx *ui.Ctx, id int, query func() api.LessonQuery) *week {
	w := &week{ctx: ctx, id: id, query: query, cache: map[string]*weekState{}, enabled: true, want: wantNow}
	w.list.Skip = func(i int) bool { return i >= 0 && i < len(w.rows) && w.rows[i].header }
	w.mon = w.thisMonday()
	return w
}

func (w *week) now() time.Time { return w.ctx.Time() }

func (w *week) thisMonday() time.Time { return mondayOf(viewerDay(w.now())) }

func (w *week) curKey() string { return dateKey(w.mon) }

func querySig(q api.LessonQuery) string {
	return strings.ToLower(q.Email) + "|" + q.Group + "|" + q.Auditorium
}

// start shows the current week and loads it.
func (w *week) start() tea.Cmd { return w.goTo(w.thisMonday(), wantNow) }

// enable turns the timetable on (after the profile allowed it).
func (w *week) enable() tea.Cmd {
	w.enabled, w.blocked = true, nil
	return w.goTo(w.mon, w.want)
}

// disable hides the timetable behind msg (nil = loading placeholder).
func (w *week) disable(msg func() string) {
	w.enabled, w.blocked = false, msg
	w.lessons, w.rows = nil, nil
	w.list.SetLen(0)
}

// goTo shows the week starting at mon, fetching it unless cached.
func (w *week) goTo(mon time.Time, wnt want) tea.Cmd {
	if !mon.Equal(w.mon) || wnt != wantKeep {
		w.scroll.Reset()
	}
	w.mon, w.want = mon, wnt
	if !w.enabled {
		return nil
	}
	w.rebuild()
	if st := w.cache[w.curKey()]; st != nil && st.loaded && !w.stale(st) {
		return nil
	}
	// Not cached, or cached long ago: (re)fetch; old data stays on screen.
	if w.load.Loading && w.loadingKey == w.curKey() {
		return nil
	}
	return w.fetch()
}

// refreshIfStale re-checks the shown week when its data is old (called
// periodically; at most once per weekTTL, also while requests fail).
func (w *week) refreshIfStale() tea.Cmd {
	if st := w.cache[w.curKey()]; w.enabled && st != nil && st.loaded && w.old(st) {
		return w.refresh()
	}
	return nil
}

func (w *week) fetch() tea.Cmd {
	if w.ctx == nil || w.ctx.API == nil {
		return nil
	}
	q := w.query()
	sig := querySig(q)
	if sig != w.sig {
		// Different person/account: nothing cached applies.
		w.cache = map[string]*weekState{}
		w.sig = sig
		w.rebuild()
	}
	q.Start = w.mon
	q.End = w.mon.AddDate(0, 0, 6)
	key, gen := w.curKey(), w.gen
	w.loadingKey = key
	seq := w.load.Begin()
	client := w.ctx.API
	return ui.Fetch[weekResult](w.id, seq, func(c context.Context) (weekResult, api.Meta, error) {
		ls, meta, err := client.Lessons(c, q)
		return weekResult{Key: key, Gen: gen, Lessons: ls}, meta, err
	})
}

// refresh refetches the shown week, keeping it on screen meanwhile.
func (w *week) refresh() tea.Cmd {
	if !w.enabled || (w.load.Loading && w.loadingKey == w.curKey()) {
		return nil
	}
	return w.fetch()
}

// reload drops cached weeks (language or account changed) and refetches
// the shown one, which stays visible until the new data arrives.
func (w *week) reload() tea.Cmd {
	w.gen++
	cur := w.curKey()
	for k := range w.cache {
		if k != cur {
			delete(w.cache, k)
		}
	}
	if !w.enabled {
		return nil
	}
	if querySig(w.query()) != w.sig {
		// Someone else signed in: start over at their current week.
		w.mon, w.want = w.thisMonday(), wantNow
	}
	return w.fetch()
}

// handleResult stores a fetched week. Superseded responses only fill the
// cache (they are still valid data for their week).
func (w *week) handleResult(msg ui.Result[weekResult]) tea.Cmd {
	key := msg.Data.Key
	if !w.load.Accept(msg.Seq) {
		if msg.Err == nil && msg.Data.Gen == w.gen && key != "" {
			// Fill a gap, or replace data from before a reload (the
			// reload's own response may arrive after the user moved on).
			if st := w.cache[key]; st == nil || !st.loaded || st.gen != w.gen {
				w.cache[key] = &weekState{lessons: normalize(msg.Data.Lessons), loaded: true, meta: msg.Meta, at: w.now(), gen: msg.Data.Gen}
				if key == w.curKey() && w.enabled {
					w.rebuild()
				}
			}
		}
		return nil
	}
	cmd := w.load.Done(msg.Meta, msg.Err)
	if key == "" {
		key = w.loadingKey
	}
	st := w.cache[key]
	if st == nil {
		st = &weekState{}
		w.cache[key] = st
	}
	st.at = w.now()
	if msg.Err != nil {
		st.err = msg.Err
	} else {
		st.lessons = normalize(msg.Data.Lessons)
		st.loaded, st.err, st.meta, st.gen = true, nil, msg.Meta, msg.Data.Gen
	}
	if key == w.curKey() && w.enabled {
		w.rebuild()
	}
	return cmd
}

// curLoad is the load state of the shown week.
func (w *week) curLoad() ui.Load {
	l := ui.Load{Seq: w.load.Seq, Loading: w.load.Loading && w.loadingKey == w.curKey()}
	if st := w.cache[w.curKey()]; st != nil {
		l.Loaded, l.Err, l.Meta = st.loaded, st.err, st.meta
	}
	return l
}

// rebuild recomputes rows for the shown week and applies the wanted
// selection (kept pending until the week's data has arrived).
func (w *week) rebuild() {
	prev := ""
	if l, ok := w.selected(); ok {
		prev = lessonKey(l)
	}
	st := w.cache[w.curKey()]
	if st == nil || !st.loaded {
		w.lessons, w.rows = nil, nil
		w.list.SetLen(0)
		return
	}
	w.lessons = st.lessons
	w.rows = w.rows[:0]
	var day time.Time
	for i, l := range w.lessons {
		d := lessonDay(l)
		if i == 0 || !d.Equal(day) {
			w.rows = append(w.rows, row{header: true, day: d, idx: -1})
			day = d
		}
		w.rows = append(w.rows, row{day: d, idx: i})
	}
	w.list.SetLen(len(w.rows))
	switch w.want {
	case wantKeep:
		if i := w.rowOf(prev); i >= 0 {
			w.list.Select(i)
		}
	case wantNow:
		w.list.Select(w.nowRow())
	case wantFirst:
		w.list.Select(0)
	case wantLastDay:
		last := 0
		for i, r := range w.rows {
			if r.header {
				last = i
			}
		}
		w.list.Select(last + 1)
	}
	w.want = wantKeep
}

func (w *week) rowOf(key string) int {
	if key == "" {
		return -1
	}
	for i, r := range w.rows {
		if !r.header && lessonKey(w.lessons[r.idx]) == key {
			return i
		}
	}
	return -1
}

// nowRow is the ongoing lesson, else the next upcoming one, else the last.
func (w *week) nowRow() int {
	now := w.now()
	ongoing, next, last := -1, -1, 0
	for i, r := range w.rows {
		if r.header {
			continue
		}
		l := w.lessons[r.idx]
		if ongoing < 0 && isOngoing(l, now) {
			ongoing = i
		}
		if next < 0 && l.DateStart.After(now) {
			next = i
		}
		last = i
	}
	switch {
	case ongoing >= 0:
		return ongoing
	case next >= 0:
		return next
	}
	return last
}

// nextToday is the row of the next lesson starting later today (-1: none).
func (w *week) nextToday(now time.Time) int {
	for i, r := range w.rows {
		if r.header {
			continue
		}
		l := w.lessons[r.idx]
		if l.DateStart.After(now) {
			if lessonDay(l).Equal(viewerDay(now)) {
				return i
			}
			return -1
		}
	}
	return -1
}

// selected is the lesson under the cursor.
func (w *week) selected() (api.Lesson, bool) {
	if !w.list.HasSelection() || w.list.Cursor >= len(w.rows) {
		return api.Lesson{}, false
	}
	r := w.rows[w.list.Cursor]
	if r.header || r.idx < 0 || r.idx >= len(w.lessons) {
		return api.Lesson{}, false
	}
	return w.lessons[r.idx], true
}

// jumpDay moves to the first lesson of the previous/next day that has
// lessons, crossing into the adjacent week when needed.
func (w *week) jumpDay(dir int) tea.Cmd {
	l, ok := w.selected()
	if ok {
		cur := lessonDay(l)
		if dir > 0 {
			for i, r := range w.rows {
				if r.header && r.day.After(cur) {
					w.selectRow(i + 1)
					return nil
				}
			}
		} else {
			for i := len(w.rows) - 1; i >= 0; i-- {
				if r := w.rows[i]; r.header && r.day.Before(cur) {
					w.selectRow(i + 1)
					return nil
				}
			}
		}
	}
	if dir > 0 {
		return w.goTo(w.mon.AddDate(0, 0, 7), wantFirst)
	}
	return w.goTo(w.mon.AddDate(0, 0, -7), wantLastDay)
}

func (w *week) selectRow(i int) {
	w.list.Select(i)
	w.scroll.Reset()
}

// update handles timetable keys. handled reports whether the key was used.
func (w *week) update(msg tea.KeyMsg) (cmd tea.Cmd, handled bool) {
	if !w.enabled {
		return nil, false
	}
	switch msg.String() {
	case "h", "left":
		w.moved = true
		return w.goTo(w.mon.AddDate(0, 0, -7), wantFirst), true
	case "l", "right":
		w.moved = true
		return w.goTo(w.mon.AddDate(0, 0, 7), wantFirst), true
	case "t":
		w.moved = true
		return w.goTo(w.thisMonday(), wantNow), true
	case "[":
		w.moved = true
		return w.jumpDay(-1), true
	case "]":
		w.moved = true
		return w.jumpDay(1), true
	case "o", "m", "a", "p", "s", "y":
		l, ok := w.selected()
		if !ok {
			return warn("No lesson selected", "Пара не выбрана"), true
		}
		return w.lessonKey(l, msg.String()), true
	}
	if w.scroll.HandleKey(msg) {
		return nil, true
	}
	before := w.list.Cursor
	if w.list.HandleKey(msg) {
		w.moved = true
		if w.list.Cursor != before {
			w.scroll.Reset()
		}
		return nil, true
	}
	return nil, false
}

// lessonKey runs the direct-key version of a lesson action.
func (w *week) lessonKey(l api.Lesson, key string) tea.Cmd {
	switch key {
	case "o":
		if links := l.Links(); len(links) > 0 {
			return ui.OpenURL(links[0].Link)
		}
		if u := strings.TrimSpace(l.DisciplineLink); u != "" {
			return ui.OpenURL(u)
		}
		return warn("No link for this lesson", "У этой пары нет ссылки")
	case "m":
		if !l.Location.Valid() {
			return warn("No location for this lesson", "Место проведения не указано")
		}
		return ui.OpenURL(ui.MapURL(l.Location.Lat(), l.Location.Lng()))
	case "a":
		t, ok := roomTarget(l)
		switch {
		case !ok && l.IsOnline():
			return warn("This lesson is online", "Эта пара проходит онлайн")
		case !ok:
			return warn("No room for this lesson", "Аудитория не указана")
		case sameTarget(t, w.self):
			return ui.Info(ui.Tr("Already showing %s", "Уже открыто: %s"), t.Title)
		}
		return ui.OpenTarget(t)
	case "s":
		t, ok := streamTarget(l)
		switch {
		case !ok:
			return warn("No group for this lesson", "Группа не указана")
		case sameTarget(t, w.self):
			return info("Already showing this group", "Эта группа уже открыта")
		}
		return ui.OpenTarget(t)
	case "p":
		var ts []ui.Target
		for _, t := range lecturerTargets(l) {
			if !sameTarget(t, w.self) {
				ts = append(ts, t)
			}
		}
		switch len(ts) {
		case 0:
			return warn("No other lecturer listed", "Других преподавателей нет")
		case 1:
			return ui.OpenTarget(ts[0])
		}
		var acts []ui.Action
		for _, t := range ts {
			t := t
			acts = append(acts, ui.Action{Label: t.Title, Run: func() tea.Cmd { return ui.OpenTarget(t) }})
		}
		return ui.ShowMenu(ui.Tr("Lecturers", "Преподаватели"), acts...)
	case "y":
		return ui.Copy(summary(l), copiedLesson())
	}
	return nil
}

// copiedLesson names a copied lesson summary in the footer.
func copiedLesson() string { return ui.Tr("lesson", "описание пары") }

// warn and info are footer messages in the interface language.
func warn(en, ru string) tea.Cmd { return ui.Warn("%s", ui.Tr(en, ru)) }
func info(en, ru string) tea.Cmd { return ui.Info("%s", ui.Tr(en, ru)) }

// lessonActions is the action menu for a lesson.
func (w *week) lessonActions(l api.Lesson) []ui.Action {
	var acts []ui.Action
	var lects []ui.Target
	for _, t := range lecturerTargets(l) {
		if !sameTarget(t, w.self) {
			lects = append(lects, t)
		}
	}
	for _, t := range lects {
		t := t
		key := ""
		if len(lects) == 1 {
			key = "p"
		}
		acts = append(acts, ui.Action{Key: key, Label: ui.Tr("Lecturer: ", "Преподаватель: ") + t.Title, Run: func() tea.Cmd { return ui.OpenTarget(t) }})
	}
	if t, ok := roomTarget(l); ok && !sameTarget(t, w.self) {
		room, _ := stripPrefix(t.Title, roomPrefixes)
		acts = append(acts, ui.Action{Key: "a", Label: ui.Tr("Room timetable: ", "Расписание аудитории ") + room, Run: func() tea.Cmd { return ui.OpenTarget(t) }})
	}
	if t, ok := streamTarget(l); ok && !sameTarget(t, w.self) {
		acts = append(acts, ui.Action{Key: "s", Label: ui.Tr("Group/stream timetable", "Расписание группы/потока"), Run: func() tea.Cmd { return ui.OpenTarget(t) }})
	}
	oKey := "o"
	for _, s := range l.Links() {
		u := s.Link
		acts = append(acts, ui.Action{Key: oKey, Label: ui.Tr("Open link: ", "Открыть ссылку: ") + ui.Trunc(linkLabel(s), 40), Run: func() tea.Cmd { return ui.OpenURL(u) }})
		oKey = ""
	}
	if u := strings.TrimSpace(l.DisciplineLink); u != "" {
		acts = append(acts, ui.Action{Key: oKey, Label: ui.Tr("Open course page", "Открыть страницу курса"), Run: func() tea.Cmd { return ui.OpenURL(u) }})
	}
	if l.Location.Valid() {
		lat, lng := l.Location.Lat(), l.Location.Lng()
		acts = append(acts, ui.Action{Key: "m", Label: ui.Tr("Open on map", "Открыть на карте"), Run: func() tea.Cmd { return ui.OpenURL(ui.MapURL(lat, lng)) }})
	}
	text, what := summary(l), copiedLesson()
	acts = append(acts, ui.Action{Key: "y", Label: ui.Tr("Copy summary", "Скопировать описание"), Run: func() tea.Cmd { return ui.Copy(text, what) }})
	return acts
}

// menuTitle names a lesson in the action menu.
func menuTitle(l api.Lesson) string {
	return discipline(l) + " · " + ui.FmtDay(l.Start()) + " " + timeRange(l)
}

// ------------------------------------------------------------------ view

// viewList renders the left pane: week title plus day-grouped lessons.
func (w *week) viewList(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	if !w.enabled {
		head := ui.PaneTitle(ui.Tr("Timetable", "Расписание"), "", width)
		if height == 1 {
			return head
		}
		msg := ui.Tr("Loading…", "Загрузка…")
		if w.blocked != nil {
			msg = w.blocked()
		}
		return head + "\n" + ui.Placeholder(width, height-1, ui.StyleDim.Render(msg))
	}
	ld := w.curLoad()
	note := ui.LoadNote(ld)
	if note == "" {
		note = ui.StyleDim.Render(relWeek(w.mon, w.thisMonday()))
	}
	head := ui.PaneTitle(weekTitle(w.mon), note, width)
	if height == 1 {
		return head
	}
	if len(w.rows) == 0 {
		return head + "\n" + ui.StateView(width, height-1, ld, 0, ui.Tr("No classes this week", "На этой неделе пар нет"))
	}
	now := w.now()
	next := w.nextToday(now)
	today := viewerDay(now)
	body := w.list.Render(width, height-1, func(i int, sel bool, cw int) string {
		r := w.rows[i]
		if r.header {
			if r.day.Equal(today) {
				return ui.StyleSection.Render(ui.Trunc(" "+dayLabel(r.day)+ui.Tr(" · today", " · сегодня"), cw))
			}
			return ui.HeaderRow(dayLabel(r.day), cw)
		}
		l := w.lessons[r.idx]
		mark := " "
		switch {
		case l.IsBan:
			mark = ui.StyleErr.Render("✕")
		case isOngoing(l, now):
			mark = ui.StyleAccent.Render("●")
		case i == next:
			mark = ui.StyleAccent.Render("○")
		}
		return ui.Row(sel, lessonLine(l, mark, isPast(l, now), cw-1), cw)
	})
	return head + "\n" + body
}

// lessonLine is "● 10:00–11:20 SEM Probability Theory…  303" in width cells,
// dropping the end time, kind and room as the pane gets narrower.
func lessonLine(l api.Lesson, mark string, past bool, width int) string {
	tr := timeRange(l)
	short := l.Start().Format("15:04")
	kind := ui.KindShort(l.Kind())
	room := ui.Trunc(roomShort(l), 10)
	disc := discipline(l)

	// Which columns fit is decided for a nominal room width, the same for
	// every row, so the columns of the list stay aligned whatever each
	// lesson's room is ("303", "online", none).
	const roomCol = 4
	showEnd, showKind, showRoom := true, true, true
	layout := func(roomW int) int { // width left for the discipline
		n := 2 + 1 // mark + space, space after the time
		if showEnd {
			n += 11
		} else {
			n += 5
		}
		if showKind {
			n += 4
		}
		if showRoom && roomW > 0 {
			n += 2 + roomW
		}
		return width - n
	}
	if layout(roomCol) < 14 {
		showEnd = false
	}
	if layout(roomCol) < 10 {
		showKind = false
	}
	if layout(roomCol) < 8 {
		showRoom = false
	}

	t := short
	if showEnd {
		t = ui.PadRight(tr, 11)
	}
	if past {
		t = ui.StyleDim.Render(t)
	}
	s := mark + " " + t + " "
	if showKind {
		s += ui.KindStyle(l.Kind()).Render(kind) + " "
	}
	dw := layout(ui.Width(room))
	if dw < 1 {
		return ui.Trunc(s+disc, width)
	}
	if past || l.IsBan {
		s += ui.StyleDim.Render(ui.PadRight(disc, dw))
	} else {
		s += ui.PadRight(disc, dw)
	}
	if showRoom && room != "" {
		s += "  " + room
	}
	return s
}

// viewDetail renders the selected lesson (right pane).
func (w *week) viewDetail(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	l, ok := w.selected()
	if !ok {
		msg := ""
		if w.enabled && w.curLoad().Loaded && len(w.rows) == 0 {
			msg = ui.Tr("Nothing scheduled for ", "Нет пар на неделе ") + weekTitle(w.mon) + ".\n\n" +
				ui.Tr("h/l other weeks · t this week", "h/l другие недели · t текущая неделя")
		}
		return ui.Placeholder(width, height, ui.StyleDim.Render(msg))
	}
	return w.scroll.Render(lessonDetail(l, w.lessons, w.now(), width), width, height)
}
