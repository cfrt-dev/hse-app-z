package search

import (
	"strconv"
	"strings"
	"time"

	"hse-app-z/internal/api"
	"hse-app-z/internal/config"
	"hse-app-z/internal/ui"
	"hse-app-z/internal/ui/avatar"
)

func (p *Page) View(w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	// Favourites may have changed on another tab: rows are cheap to rebuild.
	p.rebuild(true)

	top := []string{p.inputLine(w)}
	top = append(top, p.bannerLines(w, h)...)
	if len(top) >= h {
		return ui.Fit(strings.Join(top[:h], "\n"), w, h)
	}
	bodyH := h - len(top)
	lw := ui.LeftWidth(w)
	body := ui.Split(w, bodyH, lw, p.leftView(lw, bodyH), p.preview(ui.RightWidth(w, lw), bodyH))
	return ui.Fit(strings.Join(top, "\n")+"\n"+body, w, h)
}

func (p *Page) inputLine(w int) string {
	hint := ui.StyleDim.Render(ui.Tr("/ or i to type", "/ или i — ввод"))
	style := ui.StyleDim
	if p.input.Focused() {
		hint = ui.StyleDim.Render(ui.Tr("enter results · esc leave", "enter результаты · esc выйти"))
		style = ui.StyleKey
	}
	p.input.Placeholder = placeholder()
	if ui.Width(hint)+40 > w {
		hint = ""
	}
	field := p.input.View(w-ui.Width(hint)-1, style)
	if hint == "" {
		return ui.Trunc(field, w)
	}
	return ui.PadRight(field, w-ui.Width(hint)) + hint
}

// bannerLines renders the notice under the field: two lines when there is
// room, one otherwise.
func (p *Page) bannerLines(w, h int) []string {
	b, ok := p.banner()
	if !ok {
		return nil
	}
	title, desc := ui.Clean(b.Title), ui.Clean(b.Description)
	if title == "" {
		title, desc = desc, ""
	}
	hint := ""
	if b.IsDismissible && !p.input.Focused() && w >= 50 {
		hint = ui.StyleDim.Render(ui.Tr(" x dismiss", " x скрыть"))
	}
	mark := ui.StyleWarn.Render("» ")
	if h >= 16 && desc != "" {
		first := mark + ui.StyleDim.Render(ui.Trunc(title, w-2-ui.Width(hint)))
		return []string{
			ui.PadRight(first, w-ui.Width(hint)) + hint,
			ui.StyleDim.Render("  " + ui.Trunc(desc, w-2)),
		}
	}
	text := ui.JoinNonEmpty(" — ", title, desc)
	return []string{ui.PadRight(mark+ui.StyleDim.Render(ui.Trunc(text, w-2-ui.Width(hint))), w-ui.Width(hint)) + hint}
}

func (p *Page) leftView(w, h int) string {
	var title, note string
	q := p.q()
	if p.home() {
		title = ui.Tr("Quick open", "Быстрый доступ")
		switch {
		case q != "":
			note = ui.StyleDim.Render(ui.Trf("type %d+ characters", "введите от %d символов", minQuery))
		default:
			note = ui.LoadNote(p.favLoad)
		}
	} else {
		title = ui.Tr("Results", "Результаты")
		note = p.resultNote(q)
	}
	head := paneTitle(title, note, w)
	if h <= 1 {
		return head
	}
	if !p.home() {
		if sv := p.resultState(q, w, h-1); sv != "" {
			return head + "\n" + sv
		}
	}
	return head + "\n" + p.list.Render(w, h-1, p.renderRow)
}

// paneTitle is ui.PaneTitle that shortens a long note (errors) instead of
// dropping the title.
func paneTitle(title, note string, w int) string {
	return ui.PaneTitle(title, ui.Trunc(note, max(0, w-ui.Width(title)-2)), w)
}

func (p *Page) resultNote(q string) string {
	switch {
	case p.held():
		return ui.StyleWarn.Render(ui.Tr("Rate limited · will retry", "Лимит запросов · повторим позже"))
	case p.dirty || p.load.Loading:
		return ui.StyleDim.Render(ui.Tr("searching…", "поиск…"))
	case p.load.Err != nil || p.load.Meta.Stale:
		return ui.LoadNote(p.load)
	case p.query == q:
		return ui.StyleDim.Render(ui.Count(len(p.results), "result", "results", "результат", "результата", "результатов"))
	}
	return ""
}

// resultState is the placeholder shown instead of an empty result list.
func (p *Page) resultState(q string, w, h int) string {
	switch {
	case len(p.results) > 0:
		return ""
	case p.held():
		return ui.Placeholder(w, h, ui.StyleWarn.Render(ui.Tr("Too many requests", "Слишком много запросов"))+"\n\n"+
			ui.StyleDim.Render(ui.Tr("The search runs again in a moment", "Поиск скоро повторится")))
	case p.dirty || p.load.Loading:
		return ui.Placeholder(w, h, ui.StyleDim.Render(ui.Tr("Searching…", "Поиск…")))
	case p.load.Err != nil:
		// r would be typed into the field while it has focus.
		key := "r"
		if p.input.Focused() {
			key = "enter"
		}
		return ui.Placeholder(w, h, ui.StyleErr.Render(ui.Clean(ui.ErrText(p.load.Err)))+"\n\n"+
			ui.StyleDim.Render(ui.Trf("press %s to retry", "нажмите %s, чтобы повторить", key)))
	case p.query == q:
		return ui.Placeholder(w, h, ui.Trf("No matches for “%s”", "По запросу «%s» ничего не найдено", ui.Clean(q))+"\n\n"+
			ui.StyleDim.Render(ui.Tr("Try a surname, an email, a group like БИБ255 or a room like R615",
				"Попробуйте фамилию, почту, группу (БИБ255) или аудиторию (R615)")))
	}
	return ui.Placeholder(w, h, ui.StyleDim.Render(ui.Tr("Searching…", "Поиск…")))
}

// badge is the type badge padded to a fixed column.
func badge(t string) string { return ui.PadRight(ui.TypeBadge(t), 4) }

// favType maps a stored favourite to a search type for its badge.
func favType(f config.Favourite) string {
	switch f.Kind {
	case config.KindGroup:
		return api.TypeGroup
	case config.KindAuditorium:
		return api.TypeAuditorium
	}
	if strings.HasSuffix(strings.ToLower(f.Key), "@edu.hse.ru") {
		return api.TypeStudent
	}
	return api.TypeStaff
}

func meType(email string) string {
	if strings.HasSuffix(strings.ToLower(email), "@edu.hse.ru") {
		return api.TypeStudent
	}
	return api.TypeStaff
}

// hitName is the row label of a search hit.
func hitName(p api.Person) string {
	name := ui.Clean(p.DisplayName())
	if name == "" {
		name = ui.Tr("(no name)", "(без имени)")
	}
	return name
}

// hitDesc is the dim text after the name.
func hitDesc(p api.Person) string {
	switch {
	case p.IsGroup():
		course := ""
		if p.Course.OK && p.Course.V > 0 {
			course = ui.Trf("course %d", "%d курс", p.Course.Int())
		}
		prog := ui.Clean(p.ProgramName)
		if prog == "" {
			prog = ui.Clean(p.Description)
		}
		return ui.JoinNonEmpty(" · ", course, prog)
	case p.IsAuditorium():
		return ui.JoinNonEmpty(" · ", ui.Clean(p.AuditoriumType), ui.Clean(p.Description))
	}
	return ui.Clean(p.Description)
}

func rowTitle(r row) string {
	switch r.kind {
	case rowMe, rowFav:
		return ui.Clean(r.target.Title)
	case rowPerson:
		if r.person.IsAuditorium() {
			return ui.Tr("Room ", "Аудитория ") + hitName(r.person)
		}
		return hitName(r.person)
	case rowHeader:
		return ui.Tr("Favourites", "Избранное")
	case rowHint:
		return ui.Tr("Star people, groups or rooms with f", "Добавляйте людей, группы и аудитории в избранное клавишей f")
	}
	return ""
}

func (p *Page) renderRow(i int, sel bool, w int) string {
	r := p.rows[i]
	var content string
	star := ""
	if p.starred(r) {
		star = " " + ui.StyleWarn.Render("★")
	}
	switch r.kind {
	case rowHeader:
		return ui.HeaderRow(rowTitle(r), w)
	case rowHint:
		return " " + ui.StyleDim.Render(ui.Trunc(rowTitle(r), w-1))
	case rowMe:
		content = badge(meType(r.target.Key)) + " " + ui.Clean(r.target.Title) + star
	case rowFav:
		content = badge(favType(r.fav)) + " " + ui.Clean(r.fav.Title) + star
		if sub := ui.Clean(r.fav.Subtitle); sub != "" {
			content += "  " + ui.StyleDim.Render(sub)
		}
	case rowPerson:
		content = badge(r.person.Type) + " " + hitName(r.person) + star
		if r.server && star == "" {
			content += " " + ui.StyleDim.Render("☆")
		}
		if d := hitDesc(r.person); d != "" {
			content += "  " + ui.StyleDim.Render(d)
		}
	}
	return ui.Row(sel, content, w)
}

// --------------------------------------------------------------- preview

func typeLabel(t string) string {
	switch strings.ToUpper(t) {
	case api.TypeStudent:
		return ui.Tr("Student", "Студент")
	case api.TypeStaff:
		return ui.Tr("Staff", "Сотрудник")
	case api.TypeGroup:
		return ui.Tr("Group", "Группа")
	case api.TypeAuditorium:
		return ui.Tr("Room", "Аудитория")
	case "":
		return ui.Tr("Person", "Человек")
	}
	return ui.Clean(strings.ToLower(t))
}

func (p *Page) preview(w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	r, ok := p.selected()
	if !ok {
		return p.tips(w, h)
	}
	body := p.previewBody(r, w, h)
	// The list keys would be typed into the field while it has focus
	// (enter there goes to the results), so don't advertise them then.
	foot := ""
	if !p.input.Focused() {
		foot = p.footHint(r, false)
		if ui.Width(foot) > w {
			foot = p.footHint(r, true)
		}
	}
	if h < 4 || foot == "" {
		return p.scroll.Render(body, w, h)
	}
	return p.scroll.Render(body, w, h-1) + "\n" + ui.StyleDim.Render(ui.Trunc(foot, w))
}

func (p *Page) tips(w, h int) string {
	key := ui.StyleKey.Render
	lines := []string{
		ui.StyleTitle.Render(ui.Trunc(ui.Tr("Search the university", "Поиск по университету"), w)),
		"",
		ui.Wrap(ui.Tr("Type a surname, an email, a group (БИБ255) or a room (R615). Results appear as you type.",
			"Введите фамилию, почту, группу (БИБ255) или аудиторию (R615). Результаты появляются по мере ввода."), w),
		"",
		ui.Wrap(ui.Trf("%s or %s jumps to the results, %s leaves the field, %s comes back from anywhere.",
			"%s или %s — к результатам, %s — выйти из поля, %s — вернуться сюда откуда угодно.",
			key("enter"), key("↓"), key("esc"), key("/")), w),
		"",
		ui.Wrap(ui.Trf("Star anything with %s to keep it here.", "Нажмите %s, чтобы добавить в избранное и видеть здесь.", key("f")), w),
	}
	return ui.Fit(strings.Join(lines, "\n"), w, h)
}

// footHint is the key line under the preview; short drops the nouns.
func (p *Page) footHint(r row, short bool) string {
	var parts []string
	if r.ok {
		if short {
			parts = append(parts, ui.Tr("enter open", "enter открыть"))
		} else {
			parts = append(parts, ui.Tr("enter open timetable", "enter расписание"))
		}
		if p.starred(r) {
			parts = append(parts, ui.Tr("f unstar", "f убрать"))
		} else {
			parts = append(parts, ui.Tr("f star", "f в избранное"))
		}
	}
	if text, what := copyOf(r); text != "" {
		if short {
			parts = append(parts, ui.Tr("y copy", "y копировать"))
		} else {
			parts = append(parts, what.hint())
		}
	}
	if _, ok := roomLocation(r); ok {
		parts = append(parts, ui.Tr("m map", "m карта"))
	}
	return strings.Join(parts, " · ")
}

func (p *Page) previewBody(r row, w, h int) string {
	var out []string
	if blk, ok := p.avatarBlock(r, w, h); ok {
		out = withAvatar(blk, func(tw int) []string { return p.previewHead(r, tw) }, w)
	} else {
		out = p.previewHead(r, w)
	}
	out = append(out, p.previewRest(r, w)...)
	return strings.Join(out, "\n")
}

// avatarBlock is the selected person's picture when it is loaded and the
// pane has room for it (beside the name, or above it with the name and
// kind still visible).
func (p *Page) avatarBlock(r row, w, h int) (string, bool) {
	url := rowAvatar(r)
	switch {
	case url == "":
		return "", false
	case w >= avatar.Cols+24:
		if h < avatar.Rows+2 {
			return "", false
		}
	case w < avatar.Cols || h < avatar.Rows+4:
		return "", false
	}
	return avatar.Block(url, avatar.Cols, avatar.Rows)
}

// withAvatar lays out the picture block with the heading lines (which
// head wraps to the width it is given): to the right of the picture when
// the pane is wide enough, else under it.
func withAvatar(block string, head func(w int) []string, w int) []string {
	pic := strings.Split(block, "\n")
	for i, l := range pic {
		pic[i] = ui.PadRight(l, avatar.Cols)
	}
	const gap = 2
	if w < avatar.Cols+24 {
		return append(append(pic, ""), head(w)...)
	}
	tw := w - avatar.Cols - gap
	text := strings.Split(strings.Join(head(tw), "\n"), "\n")
	n := max(len(pic), len(text))
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		left := strings.Repeat(" ", avatar.Cols)
		if i < len(pic) {
			left = pic[i]
		}
		line := left
		if i < len(text) && text[i] != "" {
			line += strings.Repeat(" ", gap) + ui.Trunc(text[i], tw)
		}
		out = append(out, line)
	}
	return out
}

// previewHead is the title and the dim line under it, wrapped to w.
func (p *Page) previewHead(r row, w int) []string {
	var out []string
	add := func(s string) {
		if s != "" {
			out = append(out, s)
		}
	}
	add(ui.StyleTitle.Render(ui.Wrap(rowTitle(r), w)))
	switch r.kind {
	case rowMe:
		add(ui.StyleDim.Render(ui.Wrap(ui.Tr("Your own timetable", "Ваше расписание"), w)))
	case rowFav:
		add(ui.StyleDim.Render(ui.Wrap(ui.JoinNonEmpty(" · ", typeLabel(favType(r.fav)), ui.Clean(r.fav.Subtitle)), w)))
	case rowPerson:
		add(personSub(r.person, w))
	}
	return out
}

// previewRest is everything under the heading.
func (p *Page) previewRest(r row, w int) []string {
	var out []string
	add := func(s string) {
		if s != "" {
			out = append(out, s)
		}
	}
	switch r.kind {
	case rowMe:
		out = append(out, "")
		add(ui.KV(ui.Tr("Email", "Почта"), ui.Clean(r.target.Key), w))
	case rowFav:
		out = append(out, "")
		label := "ID"
		if r.fav.Kind == config.KindPerson {
			label = ui.Tr("Email", "Почта")
		}
		add(ui.KV(label, ui.Clean(r.fav.Key), w))
		if !r.fav.AddedAt.IsZero() {
			add(ui.KV(ui.Tr("Starred", "Добавлено"), ui.FmtDate(r.fav.AddedAt), w))
		}
	case rowPerson:
		out = append(out, personBody(r.person, w)...)
		if !r.ok {
			out = append(out, "", ui.StyleWarn.Render(ui.Wrap(ui.Tr("No email — this entry has no timetable",
				"Нет почты — у этой записи нет расписания"), w)))
		}
		if r.server && !p.starred(r) {
			out = append(out, "", ui.StyleDim.Render(ui.Wrap(ui.Tr("☆ Starred in the HSE App — press f to star it here too",
				"☆ В избранном в приложении ВШЭ — нажмите f, чтобы добавить и сюда"), w)))
		}
	}
	if p.starred(r) {
		out = append(out, "", ui.StyleWarn.Render("★")+" "+ui.Trunc(ui.Tr("In your favourites", "В избранном"), w-2))
	}
	return out
}

// personSub is the dim kind line under a search hit's name.
func personSub(hit api.Person, w int) string {
	switch {
	case hit.IsGroup():
		return ui.StyleDim.Render(ui.Tr("Group", "Группа"))
	case hit.IsAuditorium():
		return ui.StyleDim.Render(ui.Wrap(ui.JoinNonEmpty(" · ", ui.Tr("Room", "Аудитория"), ui.Clean(hit.AuditoriumType)), w))
	}
	return ui.StyleDim.Render(ui.Wrap(ui.JoinNonEmpty(" · ", typeLabel(hit.Type), ui.Clean(hit.Description)), w))
}

// personBody is a search hit's details.
func personBody(hit api.Person, w int) []string {
	out := []string{""}
	add := func(s string) {
		if s != "" {
			out = append(out, s)
		}
	}
	switch {
	case hit.IsGroup():
		if hit.Course.OK && hit.Course.V > 0 {
			add(ui.KV(ui.Tr("Course", "Курс"), strconv.Itoa(hit.Course.Int()), w))
		}
		add(ui.KV(ui.Tr("Program", "Программа"), ui.Clean(hit.ProgramName), w))
		add(ui.KV(ui.Tr("Details", "Описание"), ui.Clean(hit.Description), w))
		add(ui.KV("ID", ui.Clean(string(hit.ID)), w))
	case hit.IsAuditorium():
		add(ui.KV(ui.Tr("Building", "Здание"), ui.Clean(hit.Description), w))
		if realPoint(hit.AuditoriumLocation) {
			add(ui.KV(ui.Tr("Map", "Карта"), ui.StyleKey.Render("m")+ui.Tr(" open map", " открыть карту"), w))
		}
		add(ui.KV("ID", ui.Clean(string(hit.ID)), w))
	default:
		add(ui.KV(ui.Tr("Email", "Почта"), ui.Clean(hit.Email), w))
		add(ui.KV(ui.Tr("Birthday", "День рожд."), ui.Clean(birthday(hit.BirthDate)), w))
	}
	return out
}

// birthday formats a birth date in the interface language; staff profiles
// hide the year as "0000".
func birthday(raw string) string {
	b := strings.TrimSpace(raw)
	if rest, ok := strings.CutPrefix(b, "0000-"); ok {
		if t, err := time.Parse("01-02", rest); err == nil {
			return ui.FmtDayMonthLong(t)
		}
		return rest
	}
	if t, err := time.Parse("2006-01-02", b); err == nil {
		return ui.FmtDateLong(t)
	}
	return b
}
