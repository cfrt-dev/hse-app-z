package timetable

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"hse-app-z/internal/api"
	"hse-app-z/internal/config"
	"hse-app-z/internal/ui"
	"hse-app-z/internal/ui/avatar"
)

// Async payloads of the target page (distinct types so results can't be
// confused with each other).
type (
	profileData struct{ Person api.Person }
	linkData    struct{ URL string }
	subsData    struct{ People []api.Person }
)

// noTimetable is shown instead of the list when a person has none.
func noTimetable() string {
	return ui.Tr("No timetable for this person", "У этого человека нет расписания")
}

// targetPage is the profile + timetable page of a person, group or room.
type targetPage struct {
	ctx *ui.Ctx
	id  int
	t   ui.Target
	wk  *week

	person   *api.Person // full profile (people only)
	profLoad ui.Load
	link     string
	linkLoad ui.Load
	subs     []api.Person
	subLoad  ui.Load

	// showProfile: the right pane shows the profile instead of the
	// selected lesson (until the user moves in the list; i toggles).
	showProfile bool
	pscroll     ui.Scroll
}

// NewTargetPage returns the profile + timetable page of a person, group or room.
func NewTargetPage(ctx *ui.Ctx, t ui.Target) ui.Page {
	t.Key = strings.TrimSpace(t.Key)
	switch t.Kind {
	case config.KindGroup, config.KindAuditorium:
	default:
		t.Kind = config.KindPerson
		t.Key = strings.ToLower(t.Key)
	}
	p := &targetPage{ctx: ctx, id: ui.NewID(), t: t, showProfile: true}
	p.wk = newWeek(ctx, p.id, func() api.LessonQuery { return p.t.Query() })
	p.wk.self = t
	if p.isPerson() {
		// Wait for the profile: it may say there is no timetable at all.
		p.wk.disable(nil)
		if t.Key == "" {
			p.wk.disable(noTimetable)
		}
	}
	return p
}

func (p *targetPage) isPerson() bool { return p.t.Kind == config.KindPerson }

func (p *targetPage) Init() tea.Cmd {
	cmds := []tea.Cmd{tick(p.ctx, p.id)}
	switch {
	case !p.isPerson():
		cmds = append(cmds, p.wk.start())
	case p.t.Key != "":
		cmds = append(cmds, p.fetchProfile(), p.fetchLink())
	}
	// The search hit may already carry the picture.
	cmds = append(cmds, p.requestAvatar())
	return tea.Batch(cmds...)
}

func (p *targetPage) Title() string   { return p.displayName() }
func (p *targetPage) Capturing() bool { return false }

// --------------------------------------------------------------- fetching

func (p *targetPage) fetchProfile() tea.Cmd {
	if p.ctx == nil || p.ctx.API == nil {
		return nil
	}
	client, email := p.ctx.API, p.t.Key
	return ui.Fetch[profileData](p.id, p.profLoad.Begin(), func(c context.Context) (profileData, api.Meta, error) {
		per, meta, err := client.Person(c, email)
		return profileData{Person: per}, meta, err
	})
}

func (p *targetPage) fetchLink() tea.Cmd {
	if p.ctx == nil || p.ctx.API == nil {
		return nil
	}
	client, email := p.ctx.API, p.t.Key
	return ui.Fetch[linkData](p.id, p.linkLoad.Begin(), func(c context.Context) (linkData, api.Meta, error) {
		u, meta, err := client.PersonLink(c, email)
		return linkData{URL: u}, meta, err
	})
}

func (p *targetPage) fetchSubs() tea.Cmd {
	if p.ctx == nil || p.ctx.API == nil {
		return nil
	}
	client, email := p.ctx.API, p.t.Key
	return ui.Fetch[subsData](p.id, p.subLoad.Begin(), func(c context.Context) (subsData, api.Meta, error) {
		ps, meta, err := client.Subordinates(c, email)
		return subsData{People: ps}, meta, err
	})
}

// refresh reloads the profile (people) and the shown week; reload also
// drops cached weeks.
func (p *targetPage) refresh(reload bool) tea.Cmd {
	var cmds []tea.Cmd
	if p.isPerson() && p.t.Key != "" {
		cmds = append(cmds, p.fetchProfile(), p.fetchLink())
	}
	if reload {
		cmds = append(cmds, p.wk.reload())
	} else {
		cmds = append(cmds, p.wk.refresh())
	}
	return tea.Batch(cmds...)
}

func notFound(err error) bool {
	var ae *api.APIError
	return errors.As(err, &ae) && ae.NotFound()
}

// ----------------------------------------------------------------- update

func (p *targetPage) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case ui.Result[weekResult]:
		if msg.ID != p.id {
			return nil
		}
		return p.wk.handleResult(msg)

	case ui.Result[profileData]:
		if msg.ID != p.id || !p.profLoad.Accept(msg.Seq) {
			return nil
		}
		cmds := []tea.Cmd{p.profLoad.Done(msg.Meta, msg.Err)}
		if msg.Err != nil {
			if notFound(msg.Err) {
				p.person = nil
			}
			// No profile: still try the timetable.
			if !p.wk.enabled && p.wk.blocked == nil {
				cmds = append(cmds, p.wk.enable())
			}
			return tea.Batch(cmds...)
		}
		per := msg.Data.Person
		p.person = &per
		if strings.TrimSpace(p.t.Title) == "" {
			p.t.Title = ui.Clean(per.DisplayName())
		}
		switch {
		case !per.HasTimetable():
			p.wk.disable(noTimetable)
		case !p.wk.enabled:
			cmds = append(cmds, p.wk.enable())
		}
		if per.HasSubordinates() {
			cmds = append(cmds, p.fetchSubs())
		} else {
			p.subs = nil
		}
		cmds = append(cmds, p.requestAvatar())
		return tea.Batch(cmds...)

	case ui.Result[linkData]:
		if msg.ID != p.id || !p.linkLoad.Accept(msg.Seq) {
			return nil
		}
		if msg.Err == nil {
			p.link = strings.TrimSpace(msg.Data.URL)
		}
		// A missing public page is not worth a sign-in prompt or an error.
		p.linkLoad.Loading = false
		return nil

	case ui.Result[subsData]:
		if msg.ID != p.id || !p.subLoad.Accept(msg.Seq) {
			return nil
		}
		cmd := p.subLoad.Done(msg.Meta, msg.Err)
		if msg.Err == nil {
			p.subs = msg.Data.People
		}
		return cmd

	case tickMsg:
		if msg.id != p.id {
			return nil
		}
		return tea.Batch(tick(p.ctx, p.id), p.wk.refreshIfStale())

	case ui.ReloadMsg:
		return p.refresh(true)

	case tea.KeyMsg:
		return p.handleKey(msg)
	}
	return nil
}

func (p *targetPage) profileShown() bool { return p.showProfile || !p.wk.enabled }

func (p *targetPage) handleKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "r":
		return p.refresh(false)
	case "enter":
		return p.menu()
	case "f":
		return ui.ToggleFavourite(p.ctx, p.favTarget())
	case "i":
		if !p.wk.enabled {
			return info("No timetable to show", "Расписания нет")
		}
		p.showProfile = !p.showProfile
		p.pscroll.Reset()
		return nil
	case "w":
		return p.openPage()
	case "e":
		if e := p.email(); e != "" {
			return ui.Copy(e, copiedEmail())
		}
		return warn("No email", "Почта не указана")
	case "c":
		if t, ok := p.chiefTarget(); ok {
			return ui.OpenTarget(t)
		}
		return warn("No chief listed", "Руководитель не указан")
	case "m":
		if p.profileShown() && p.t.Kind == config.KindAuditorium {
			if g := p.roomLocation(); g != nil {
				return ui.OpenURL(ui.MapURL(g.Lat(), g.Lng()))
			}
		}
	}
	if p.profileShown() && p.pscroll.HandleKey(msg) {
		return nil
	}
	cmd, _ := p.wk.update(msg)
	if p.wk.moved {
		p.wk.moved = false
		if p.showProfile {
			p.showProfile = false
			p.wk.scroll.Reset()
		}
	}
	return cmd
}

func (p *targetPage) openPage() tea.Cmd {
	switch {
	case p.link != "":
		return ui.OpenURL(p.link)
	case p.linkLoad.Loading:
		return info("Still loading the public page link…", "Ссылка на страницу ещё загружается…")
	}
	return warn("No public page on hse.ru", "Нет страницы на hse.ru")
}

// copiedEmail names a copied email address in the footer.
func copiedEmail() string { return ui.Tr("email", "адрес почты") }

// menu is the action menu: the selected lesson's actions plus profile
// actions, the visible pane's first.
func (p *targetPage) menu() tea.Cmd {
	prof := p.profileActions()
	l, ok := p.wk.selected()
	if !ok || !p.wk.enabled {
		return ui.ShowMenu(p.displayName(), prof...)
	}
	lesson := p.wk.lessonActions(l)
	if p.profileShown() {
		return ui.ShowMenu(p.displayName(), append(prof, lesson...)...)
	}
	return ui.ShowMenu(menuTitle(l), append(lesson, prof...)...)
}

func (p *targetPage) profileActions() []ui.Action {
	var acts []ui.Action
	fav := p.favTarget()
	label := ui.Tr("Add to favourites", "Добавить в избранное")
	if p.starred() {
		label = ui.Tr("Remove from favourites", "Убрать из избранного")
	}
	ctx := p.ctx
	acts = append(acts, ui.Action{Key: "f", Label: label, Run: func() tea.Cmd { return ui.ToggleFavourite(ctx, fav) }})
	if e := p.email(); e != "" {
		what := copiedEmail()
		acts = append(acts,
			ui.Action{Key: "e", Label: ui.Tr("Copy email", "Скопировать почту"), Run: func() tea.Cmd { return ui.Copy(e, what) }},
			ui.Action{Label: ui.Tr("Send email", "Написать письмо"), Run: func() tea.Cmd { return ui.OpenURL("mailto:" + e) }})
	}
	if u := p.link; u != "" {
		acts = append(acts, ui.Action{Key: "w", Label: ui.Tr("Open hse.ru page", "Открыть страницу на hse.ru"), Run: func() tea.Cmd { return ui.OpenURL(u) }})
	}
	if p.person != nil {
		for _, a := range p.person.StaffAddress {
			for _, u := range officeLinks(a) {
				acts = append(acts, ui.Action{Label: ui.Tr("Join office hours (", "Подключиться к часам приёма (") + hostOf(u) + ")", Run: func() tea.Cmd { return ui.OpenURL(u) }})
			}
		}
	}
	if t, ok := p.chiefTarget(); ok {
		acts = append(acts, ui.Action{Key: "c", Label: ui.Tr("Chief: ", "Руководитель: ") + t.Title, Run: func() tea.Cmd { return ui.OpenTarget(t) }})
	}
	for _, s := range p.subs {
		if t, ok := ui.TargetFromPerson(s); ok {
			acts = append(acts, ui.Action{Label: ui.Tr("Subordinate: ", "Подчинённый: ") + t.Title, Run: func() tea.Cmd { return ui.OpenTarget(t) }})
		}
	}
	if p.t.Kind == config.KindAuditorium {
		if g := p.roomLocation(); g != nil {
			lat, lng := g.Lat(), g.Lng()
			acts = append(acts, ui.Action{Key: "m", Label: ui.Tr("Open room on map", "Открыть аудиторию на карте"), Run: func() tea.Cmd { return ui.OpenURL(ui.MapURL(lat, lng)) }})
		}
	}
	return acts
}

// ---------------------------------------------------------------- helpers

// bestPerson is the full profile, else the search hit we came from.
func (p *targetPage) bestPerson() *api.Person {
	if p.person != nil {
		return p.person
	}
	return p.t.Person
}

func (p *targetPage) displayName() string {
	if s := ui.Clean(p.t.Title); s != "" {
		// Room and group titles made by the app carry a word in the
		// language of the moment ("Room 303"): show it in the current one.
		switch p.t.Kind {
		case config.KindAuditorium:
			return retitle(s, roomPrefixes, roomPrefix())
		case config.KindGroup:
			return retitle(s, groupPrefixes, groupPrefix())
		}
		return s
	}
	if per := p.bestPerson(); per != nil {
		if s := ui.Clean(per.DisplayName()); s != "" {
			return s
		}
	}
	if p.t.Key != "" {
		return ui.Clean(p.t.Key)
	}
	return ui.Tr("Timetable", "Расписание")
}

func (p *targetPage) email() string {
	if !p.isPerson() {
		return ""
	}
	if per := p.bestPerson(); per != nil && strings.Contains(per.Email, "@") {
		return ui.Clean(per.Email)
	}
	if strings.Contains(p.t.Key, "@") {
		return ui.Clean(p.t.Key)
	}
	return ""
}

func (p *targetPage) starred() bool {
	return p.ctx != nil && p.ctx.Favs != nil && p.ctx.Favs.Has(p.t.Kind, p.t.Key)
}

// favTarget is the target as stored in favourites (with the best names).
func (p *targetPage) favTarget() ui.Target {
	t := p.t
	t.Person = nil
	t.Title = p.displayName()
	if strings.TrimSpace(t.Subtitle) == "" {
		if per := p.bestPerson(); per != nil {
			t.Subtitle = ui.Clean(per.Description)
		}
	}
	return t
}

// chiefTarget is the chief of the main position (else of any position).
func (p *targetPage) chiefTarget() (ui.Target, bool) {
	if p.person == nil {
		return ui.Target{}, false
	}
	var chief *api.Person
	for _, sp := range p.person.StaffPositions {
		if sp.Chief == nil {
			continue
		}
		if chief == nil || sp.IsMain {
			chief = sp.Chief
		}
		if sp.IsMain {
			break
		}
	}
	if chief == nil {
		return ui.Target{}, false
	}
	t, ok := ui.TargetFromPerson(*chief)
	if !ok || sameTarget(t, p.t) {
		return ui.Target{}, false
	}
	return t, true
}

// roomLocation is the room's map point: from the search hit, else from
// any loaded lesson held there.
func (p *targetPage) roomLocation() *api.GeoPoint {
	if p.t.Person != nil && p.t.Person.AuditoriumLocation.Valid() {
		return p.t.Person.AuditoriumLocation
	}
	key := strings.TrimPrefix(strings.ToLower(p.t.Key), "ruz")
	for _, st := range p.wk.cache {
		for _, l := range st.lessons {
			id := strings.TrimPrefix(strings.ToLower(string(l.AuditoriumID)), "ruz")
			if id == key && l.Location.Valid() {
				return l.Location
			}
		}
	}
	return nil
}

// ------------------------------------------------------------------- view

func (p *targetPage) Hints() []ui.Hint {
	star := ui.Tr("star", "в избранное")
	if p.starred() {
		star = ui.Tr("unstar", "из избранного")
	}
	var hs []ui.Hint
	if p.wk.enabled {
		hs = append(hs,
			ui.Hint{Key: "h/l", Desc: ui.Tr("week", "неделя")},
			ui.Hint{Key: "t", Desc: ui.Tr("today", "сегодня")},
			ui.Hint{Key: "enter", Desc: ui.Tr("actions", "действия")},
			ui.Hint{Key: "o", Desc: ui.Tr("link", "ссылка")},
			ui.Hint{Key: "p", Desc: ui.Tr("lecturer", "преподаватель")},
			ui.Hint{Key: "f", Desc: star})
		if p.profileShown() {
			hs = append(hs, ui.Hint{Key: "i", Desc: ui.Tr("lesson", "пара")})
		} else {
			hs = append(hs, ui.Hint{Key: "i", Desc: ui.Tr("profile", "профиль")})
		}
	} else {
		hs = append(hs, ui.Hint{Key: "enter", Desc: ui.Tr("actions", "действия")}, ui.Hint{Key: "f", Desc: star})
	}
	if p.isPerson() {
		if p.link != "" {
			hs = append(hs, ui.Hint{Key: "w", Desc: ui.Tr("hse.ru page", "страница hse.ru")})
		}
		hs = append(hs, ui.Hint{Key: "e", Desc: ui.Tr("copy email", "копировать почту")})
		if _, ok := p.chiefTarget(); ok {
			hs = append(hs, ui.Hint{Key: "c", Desc: ui.Tr("chief", "руководитель")})
		}
	}
	if p.wk.enabled {
		hs = append(hs,
			ui.Hint{Key: "a", Desc: ui.Tr("room", "аудитория")},
			ui.Hint{Key: "[/]", Desc: ui.Tr("prev/next day", "пред./след. день")},
			ui.Hint{Key: "m", Desc: ui.Tr("map", "карта")},
			ui.Hint{Key: "s", Desc: ui.Tr("group timetable", "расписание группы")},
			ui.Hint{Key: "y", Desc: ui.Tr("copy", "копировать")})
	}
	return append(hs, ui.Hint{Key: "r", Desc: ui.Tr("refresh", "обновить")})
}

func (p *targetPage) subtitle() string {
	switch p.t.Kind {
	case config.KindGroup:
		return ui.JoinNonEmpty(" · ", ui.Tr("Group", "Группа"), ui.Clean(p.t.Subtitle))
	case config.KindAuditorium:
		if s := ui.Clean(p.t.Subtitle); s != "" {
			return s
		}
		return ui.Tr("Room timetable", "Расписание аудитории")
	}
	if per := p.bestPerson(); per != nil {
		return ui.JoinNonEmpty(" · ", typeLabel(per.Type), ui.Clean(per.Description), p.email())
	}
	return ui.JoinNonEmpty(" · ", ui.Clean(p.t.Subtitle), p.email())
}

func (p *targetPage) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	star := ""
	if p.starred() {
		star = ui.StyleWarn.Render(" ★")
	}
	head := ui.StyleTitle.Render(ui.Trunc(p.displayName(), max(1, width-ui.Width(star)))) + star
	head = ui.Trunc(head, width) + "\n" + ui.StyleDim.Render(ui.Trunc(p.subtitle(), width))
	if height <= 3 {
		return ui.Fit(head, width, height)
	}
	body := height - 2
	lw := leftWidth(width)
	rw := ui.RightWidth(width, lw)
	left := p.wk.viewList(lw, body)
	var right string
	if p.profileShown() {
		right = p.profileView(rw, body)
	} else {
		right = p.wk.viewDetail(rw, body)
	}
	return head + "\n" + ui.Split(width, body, lw, left, right)
}

func (p *targetPage) profileView(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	title, note := ui.Tr("Profile", "Профиль"), ""
	switch p.t.Kind {
	case config.KindGroup:
		title = ui.Tr("Group", "Группа")
	case config.KindAuditorium:
		title = ui.Tr("Room", "Аудитория")
	default:
		note = ui.LoadNote(p.profLoad)
	}
	head := ui.PaneTitle(title, note, width)
	if height == 1 {
		return head
	}
	var content string
	switch p.t.Kind {
	case config.KindGroup:
		content = p.groupContent(width)
	case config.KindAuditorium:
		content = p.roomContent(width)
	default:
		content = p.personContent(width)
	}
	return head + "\n" + p.pscroll.Render(content, width, height-1)
}

// dimLine wraps s at width-indent and renders it dim and indented.
func dimLine(s string, width, indent int) string {
	pad := strings.Repeat(" ", indent)
	return styledWrap(s, width-indent, func(l ...string) string { return pad + ui.StyleDim.Render(l...) })
}

// avatarURL is the person's picture: from the full profile, else from
// the search hit we came from ("" for groups and rooms).
func (p *targetPage) avatarURL() string {
	if !p.isPerson() {
		return ""
	}
	for _, per := range []*api.Person{p.person, p.t.Person} {
		if per != nil && strings.TrimSpace(per.AvatarURL) != "" {
			return strings.TrimSpace(per.AvatarURL)
		}
	}
	return ""
}

// requestAvatar starts loading the picture (nil when images are off or
// there is none).
func (p *targetPage) requestAvatar() tea.Cmd {
	if u := p.avatarURL(); u != "" {
		return avatar.Request(u)
	}
	return nil
}

// avatarGap separates the picture from the text beside it.
const avatarGap = 2

// avatarHead is the top of the profile pane when the picture is ready:
// the picture with the name, type, description and email to its right
// when the pane is wide enough (beside), else the picture alone. ok is
// false without a picture: the pane then renders exactly as without
// images.
func (p *targetPage) avatarHead(per *api.Person, width int) (head string, beside, ok bool) {
	u := p.avatarURL()
	if u == "" || width < avatar.Cols {
		return "", false, false
	}
	blk, ok := avatar.Block(u, avatar.Cols, avatar.Rows)
	if !ok {
		return "", false, false
	}
	img := strings.Split(blk, "\n")
	if width < avatar.Cols+24 {
		return strings.Join(img, "\n"), false, true
	}
	tw := width - avatar.Cols - avatarGap
	var text []string
	add := func(s string) {
		if s != "" {
			text = append(text, strings.Split(s, "\n")...)
		}
	}
	add(styledWrap(p.displayName(), tw, ui.StyleTitle.Render))
	add(styledWrap(typeLabel(per.Type), tw, ui.StyleDim.Render))
	add(styledWrap(ui.Clean(per.Description), tw, ui.StyleDim.Render))
	add(ui.Wrap(p.email(), tw))
	lines := make([]string, max(len(img), len(text)))
	for i := range lines {
		left := strings.Repeat(" ", avatar.Cols)
		if i < len(img) {
			left = ui.PadRight(img[i], avatar.Cols)
		}
		right := ""
		if i < len(text) {
			right = text[i]
		}
		lines[i] = left + strings.Repeat(" ", avatarGap) + right
	}
	return strings.Join(lines, "\n"), true, true
}

func (p *targetPage) personContent(width int) string {
	var b []string
	per := p.bestPerson()
	if per == nil {
		per = &api.Person{}
	}
	head, beside, pic := p.avatarHead(per, width)
	if pic {
		b = append(b, head+"\n")
	}
	switch err := p.profLoad.Err; {
	case p.person != nil:
	case p.profLoad.Loading:
		b = append(b, ui.StyleDim.Render(ui.Tr("Loading profile…", "Загрузка профиля…")))
	case err != nil && notFound(err):
		b = append(b, ui.StyleWarn.Render(ui.Tr("Profile not found", "Профиль не найден")))
	case err != nil:
		b = append(b, styledWrap(ui.Clean(ui.ErrText(err)), width, ui.StyleErr.Render),
			ui.StyleDim.Render(ui.Tr("press r to retry", "нажмите r, чтобы повторить")))
	}
	if !beside {
		b = append(b,
			ui.KV(ui.Tr("Type", "Тип"), typeLabel(per.Type), width),
			ui.KV(ui.Tr("About", "Описание"), ui.Clean(per.Description), width),
			ui.KV(ui.Tr("Email", "Почта"), p.email(), width))
	}
	b = append(b, ui.KV(ui.Tr("Birthday", "Дата рожд."), birthday(per.BirthDate), width))
	if c := strings.TrimSpace(per.Campus); c != "" {
		b = append(b, ui.KV(ui.Tr("Campus", "Кампус"), ui.Clean(ui.CampusName(c)), width))
	}
	if p.person != nil && !p.person.HasTimetable() {
		b = append(b, ui.KV(ui.Tr("Timetable", "Расписание"), ui.Tr("not available", "недоступно"), width))
	}
	if p.link != "" {
		b = append(b, ui.KV(ui.Tr("Web page", "Страница"), ui.StyleLink.Render(ui.Clean(p.link)), width))
	}

	// Positions (the API repeats identical entries).
	var pos []string
	seen := map[string]bool{}
	for _, sp := range per.StaffPositions {
		name, unit := ui.Clean(sp.PositionName), unitShort(sp.UnitName)
		k := name + "|" + unit
		if seen[k] || (name == "" && unit == "") {
			continue
		}
		seen[k] = true
		if name == "" {
			name = ui.Tr("Position", "Должность")
		}
		line := ui.Wrap(name, width)
		if sp.IsMain {
			line = ui.Trunc(line+ui.StyleDim.Render(ui.Tr(" · main", " · основная")), width)
		}
		pos = append(pos, line)
		if unit != "" {
			pos = append(pos, dimLine(unit, width, 2))
		}
		if sp.Chief != nil {
			if c := ui.Clean(sp.Chief.DisplayName()); c != "" {
				pos = append(pos, dimLine(ui.Tr("chief: ", "руководитель: ")+c, width, 2))
			}
		}
	}
	b = append(b, ui.Section(ui.Tr("Positions", "Должности"), strings.Join(pos, "\n")))

	var office []string
	for _, a := range per.StaffAddress {
		label := ui.Clean(a.Label)
		phone := ui.Clean(string(a.PhoneInternalFull))
		if phone == "" {
			if ext := ui.Clean(string(a.PhoneInternalExt)); ext != "" {
				phone = ui.Tr("ext. ", "доб. ") + ext
			}
		}
		if label == "" && phone == "" {
			continue
		}
		if label != "" {
			office = append(office, ui.Wrap(label, width))
		}
		if phone != "" {
			office = append(office, dimLine(ui.Tr("phone ", "тел. ")+phone, width, 2))
		}
		if text, _ := officeHours(a); text != "" {
			kind := ui.Clean(a.PresenceType)
			if kind == "" {
				kind = ui.Tr("Office hours", "Часы приёма")
			}
			office = append(office, dimLine(kind+": "+text, width, 2))
		}
	}
	b = append(b, ui.Section(ui.Tr("Office", "Кабинет"), strings.Join(office, "\n")))

	var edu []string
	for _, e := range per.Education {
		title := ui.Clean(e.ProgramTitle)
		if title == "" {
			title = ui.Clean(e.Degree)
		}
		if title == "" {
			title = ui.Tr("Program", "Программа")
		}
		degree := degreeLabel(e.DegreeLevel)
		if degree == "" {
			degree = ui.Clean(e.Degree)
		}
		group := ui.Clean(e.GroupTitle)
		if group != "" {
			group = ui.Tr("group ", "группа ") + group
		}
		year := ui.Clean(string(e.StartYear))
		if year != "" {
			year = ui.Tr("since ", "с ") + year
		}
		edu = append(edu, styledWrap(title, width, ui.StyleBold.Render))
		for _, s := range []string{ui.JoinNonEmpty(" · ", degree, group, year), ui.Clean(e.FacultyTitle), ui.Clean(e.UniversityTitle)} {
			if s != "" {
				edu = append(edu, dimLine(s, width, 2))
			}
		}
	}
	b = append(b, ui.Section(ui.Tr("Education", "Образование"), strings.Join(edu, "\n")))

	var subs []string
	for _, s := range p.subs {
		name := ui.Clean(s.DisplayName())
		if name == "" {
			continue
		}
		subs = append(subs, ui.Wrap(name, width))
		if d := ui.JoinNonEmpty(" · ", ui.Clean(s.Description), ui.Clean(s.Email)); d != "" {
			subs = append(subs, dimLine(d, width, 2))
		}
	}
	if len(subs) == 0 && p.subLoad.Loading {
		subs = append(subs, ui.StyleDim.Render(ui.Tr("Loading…", "Загрузка…")))
	}
	b = append(b, ui.Section(ui.Tr("Subordinates", "Подчинённые"), strings.Join(subs, "\n")))
	return ui.Lines(b...)
}

func (p *targetPage) groupContent(width int) string {
	per := p.t.Person
	if per == nil {
		per = &api.Person{}
	}
	label := ui.Clean(per.Label)
	if label == "" {
		label = ui.Clean(p.t.Title)
	}
	course := ""
	if per.Course.OK {
		course = per.Course.String()
	}
	info := ui.Clean(per.Description)
	if p.t.Person == nil {
		info = ui.Clean(p.t.Subtitle)
	}
	return ui.Lines(
		ui.KV(ui.Tr("Group", "Группа"), label, width),
		ui.KV(ui.Tr("Course", "Курс"), course, width),
		ui.KV(ui.Tr("Program", "Программа"), ui.Clean(per.ProgramName), width),
		ui.KV(ui.Tr("Info", "Описание"), info, width),
		ui.KV(ui.Tr("RUZ id", "ID в РУЗ"), ui.Clean(p.t.Key), width),
	)
}

func (p *targetPage) roomContent(width int) string {
	per := p.t.Person
	if per == nil {
		per = &api.Person{}
	}
	room := ui.Clean(per.Room)
	if room == "" {
		room, _ = stripPrefix(ui.Clean(p.t.Title), roomPrefixes)
	}
	building := ui.Clean(per.Description)
	if building == "" && per.AuditoriumType == "" {
		building = ui.Clean(p.t.Subtitle)
	}
	loc := ""
	if p.roomLocation() != nil {
		loc = ui.Tr("press m to open the map", "нажмите m, чтобы открыть карту")
	}
	return ui.Lines(
		ui.KV(ui.Tr("Room", "Аудитория"), room, width),
		ui.KV(ui.Tr("Type", "Тип"), ui.Clean(per.AuditoriumType), width),
		ui.KV(ui.Tr("Building", "Корпус"), building, width),
		ui.KV(ui.Tr("Location", "На карте"), loc, width),
		ui.KV(ui.Tr("RUZ id", "ID в РУЗ"), ui.Clean(p.t.Key), width),
	)
}

// officeURL finds links in free text where they are often glued to the
// surrounding (Cyrillic) words: "…16-00Подключиться Zoomhttps://zoom.us/j/1?pwd=xИдентификатор…".
var officeURL = regexp.MustCompile(`https?://[A-Za-z0-9\-._~:/?#\[\]@!$&'()*+,;=%]+`)

// officeHours returns the readable office-hours text (links separated from
// the words around them) and the links it contains.
func officeHours(a api.StaffAddress) (string, []string) {
	raw := ui.Clean(a.PresenceTime)
	if raw == "" {
		return "", nil
	}
	var links []string
	text := officeURL.ReplaceAllStringFunc(raw, func(u string) string {
		u = strings.TrimRight(u, ".,;:)")
		links = append(links, u)
		return " " + u + " "
	})
	return ui.Clean(text), links
}

func officeLinks(a api.StaffAddress) []string {
	_, links := officeHours(a)
	var out []string
	for _, l := range links {
		if ui.SafeURL(l) {
			out = append(out, l)
		}
	}
	return out
}

func hostOf(u string) string {
	if pu, err := url.Parse(u); err == nil && pu.Host != "" {
		return strings.TrimPrefix(pu.Host, "www.")
	}
	return ui.Tr("link", "ссылка")
}
