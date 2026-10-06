// Package rating is the Rating tab: published rating snapshots (current,
// cumulative, after retakes, minor) with places, percentile, scores, exam
// counts and the disciplines that went into each one.
package rating

import (
	"context"
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"hse-app-z/internal/api"
	"hse-app-z/internal/ui"
)

func emptyText() string {
	return ui.Tr("No ratings published for this selection", "По выбранным параметрам рейтингов нет")
}

type page struct {
	ctx *ui.Ctx
	id  int

	load ui.Load
	data api.RatingsResponse
	// year and program are the requested selection ("" = server default).
	year, program string
	// typ is the client-side type filter ("" = all types).
	typ string

	items  []*api.Rating // shown: filtered, newest first
	list   ui.List
	scroll ui.Scroll
	selKey string

	banner ui.Banners

	// who is the account the data was last requested for (see ReloadMsg).
	who     string
	fetched bool
}

// New returns the tab page.
func New(ctx *ui.Ctx) ui.Page {
	return &page{ctx: ctx, id: ui.NewID(), banner: ui.Banners{Section: "ratings"}}
}

func (p *page) Title() string   { return ui.Tr("Rating", "Рейтинг") }
func (p *page) Capturing() bool { return false }

func (p *page) Init() tea.Cmd { return tea.Batch(p.fetch(), p.banner.Fetch(p.ctx, p.id)) }

// fetch loads every rating type of the selected year (no type parameter:
// the type filter is applied locally).
func (p *page) fetch() tea.Cmd {
	if p.ctx == nil || p.ctx.API == nil {
		return nil
	}
	client := p.ctx.API
	q := api.RatingQuery{AcademicYear: p.year, ProgramID: p.program}
	p.who, p.fetched = p.ctx.Me.Email, true
	return ui.Fetch(p.id, p.load.Begin(), func(c context.Context) (api.RatingsResponse, api.Meta, error) {
		return client.Ratings(c, q)
	})
}

func (p *page) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case ui.Result[api.RatingsResponse]:
		if msg.ID != p.id || !p.load.Accept(msg.Seq) {
			return nil
		}
		cmd := p.load.Done(msg.Meta, msg.Err)
		if msg.Err == nil {
			p.apply(msg.Data)
		}
		return cmd
	case ui.Result[[]api.Banner]:
		if msg.ID == p.id {
			p.banner.Handle(msg)
		}
		return nil
	case ui.ReloadMsg:
		p.forgetOtherAccount()
		return tea.Batch(p.fetch(), p.banner.Fetch(p.ctx, p.id))
	case tea.KeyMsg:
		return p.key(msg)
	}
	return nil
}

func (p *page) key(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "r":
		return tea.Batch(p.fetch(), p.banner.Fetch(p.ctx, p.id))
	case "t":
		return p.cycleType()
	case "y":
		return p.stepYear(-1, true)
	case "Y":
		return p.stepYear(1, true)
	case "h", "left":
		return p.stepYear(-1, false)
	case "l", "right":
		return p.stepYear(1, false)
	case "p":
		return p.cycleProgram()
	case "c":
		if r := p.selected(); r != nil {
			return copySummary(*r)
		}
		return nil
	case "x":
		return p.banner.Dismiss(p.ctx)
	case "enter":
		return p.menu()
	}
	if p.scroll.HandleKey(msg) {
		return nil
	}
	before := p.list.Cursor
	if p.list.HandleKey(msg) && p.list.Cursor != before {
		p.scroll.Reset()
		p.selKey = ""
		if r := p.selected(); r != nil {
			p.selKey = ratingKey(*r)
		}
	}
	return nil
}

// ------------------------------------------------------------- data

func (p *page) apply(resp api.RatingsResponse) {
	if len(resp.AvailableAcademicYears) == 0 {
		// Some responses don't repeat the list of years; keep the known
		// ones so the user can still switch back.
		resp.AvailableAcademicYears = p.years()
	}
	p.data = resp
	if y := strings.TrimSpace(resp.SelectedAcademicYear); y != "" {
		p.year = y
	}
	if s := strings.TrimSpace(string(resp.SelectedProgram)); s != "" {
		p.program = s
	}
	if p.typ != "" && index(p.types(), p.typ) < 0 {
		p.typ = ""
	}
	p.rebuild()
}

// forgetOtherAccount runs on reload: when a different account has signed
// in, the previous one's ratings must not stay on screen while (or if) the
// reload fails, and its year/program ids mean nothing for the new account.
func (p *page) forgetOtherAccount() {
	if p.ctx == nil || !p.fetched || p.who == p.ctx.Me.Email {
		return
	}
	p.year, p.program = "", ""
	p.data = api.RatingsResponse{}
	p.load.Loaded, p.load.Err, p.load.Meta = false, nil, api.Meta{}
	p.resetData()
}

func ratingKey(r api.Rating) string {
	return normType(r.Type) + "|" + r.Title + "|" + r.LearnPeriod + "|" + r.PublishedAt.String()
}

// rebuild applies the type filter and sorts newest first, keeping the
// selected rating when it is still shown.
func (p *page) rebuild() {
	p.items = p.items[:0]
	for i := range p.data.Items {
		r := &p.data.Items[i]
		if p.typ == "" || normType(r.Type) == p.typ {
			p.items = append(p.items, r)
		}
	}
	sort.SliceStable(p.items, func(i, j int) bool {
		return p.items[i].PublishedAt.After(p.items[j].PublishedAt.Time)
	})
	p.list.SetLen(len(p.items))
	found := -1
	for i, r := range p.items {
		if p.selKey != "" && ratingKey(*r) == p.selKey {
			found = i
			break
		}
	}
	if found >= 0 {
		p.list.Select(found)
	} else {
		p.list.Select(0)
		p.scroll.Reset()
	}
	p.selKey = ""
	if r := p.selected(); r != nil {
		p.selKey = ratingKey(*r)
	}
}

func (p *page) selected() *api.Rating {
	if !p.list.HasSelection() || p.list.Cursor >= len(p.items) {
		return nil
	}
	return p.items[p.list.Cursor]
}

// ---------------------------------------------------------------- types

func normType(t string) string { return strings.ToLower(strings.TrimSpace(ui.Clean(t))) }

var typeRank = map[string]int{"current": 0, "cumul": 1, "retake": 2, "minor": 3}

// types are the rating types of the loaded year in a fixed, sensible
// order (known types first).
func (p *page) types() []string {
	seen := map[string]bool{}
	var out []string
	add := func(t string) {
		if t = normType(t); t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	for _, t := range p.data.AvailableTypes {
		add(t)
	}
	for _, r := range p.data.Items {
		add(r.Type)
	}
	rank := func(t string) int {
		if r, ok := typeRank[t]; ok {
			return r
		}
		return len(typeRank)
	}
	sort.SliceStable(out, func(i, j int) bool { return rank(out[i]) < rank(out[j]) })
	return out
}

func typeName(t string) string {
	switch normType(t) {
	case "current":
		return ui.Tr("Current", "Текущий")
	case "cumul", "cumulative":
		return ui.Tr("Cumulative", "Накопленный")
	case "retake":
		return ui.Tr("After retakes", "После пересдач")
	case "minor":
		return ui.Tr("Minor", "Майнор")
	}
	if s := titleCase(t); s != "" {
		return s
	}
	return ui.Tr("Other", "Другой")
}

func titleCase(s string) string {
	words := strings.Fields(strings.NewReplacer("_", " ", "-", " ").Replace(strings.ToLower(ui.Clean(s))))
	for i, w := range words {
		r := []rune(w)
		r[0] = unicode.ToUpper(r[0])
		words[i] = string(r)
	}
	return strings.Join(words, " ")
}

// typeBadge is a colored three-letter type tag.
func typeBadge(t string) string {
	switch normType(t) {
	case "current":
		return ui.StyleAccent.Render(ui.Tr("CUR", "ТЕК"))
	case "cumul", "cumulative":
		return ui.StyleOK.Render(ui.Tr("CUM", "НАК"))
	case "retake":
		return ui.StyleWarn.Render(ui.Tr("RET", "ПЕР"))
	case "minor":
		return lipgloss.NewStyle().Foreground(ui.ColorPurple).Render(ui.Tr("MIN", "МНР"))
	}
	r := []rune(strings.ToUpper(normType(t)))
	if len(r) > 3 {
		r = r[:3]
	}
	return ui.StyleDim.Render(ui.PadRight(string(r), 3))
}

func (p *page) typeLabel() string {
	if p.typ == "" {
		return ui.Tr("All types", "Все типы")
	}
	return typeName(p.typ)
}

func (p *page) setType(t string) {
	p.typ = t
	p.rebuild()
}

func (p *page) cycleType() tea.Cmd {
	opts := append([]string{""}, p.types()...)
	if len(opts) < 2 {
		if !p.load.Loaded {
			return nil
		}
		return ui.Info("%s", ui.Tr("No rating types to filter by", "Нет типов рейтинга для фильтра"))
	}
	i := index(opts, p.typ)
	if i < 0 {
		i = 0
	}
	p.setType(opts[(i+1)%len(opts)])
	return nil
}

// ------------------------------------------------------ year & program

func index(xs []string, x string) int {
	for i, v := range xs {
		if v == x {
			return i
		}
	}
	return -1
}

// years are the selectable academic years, oldest first.
func (p *page) years() []string {
	seen := map[string]bool{}
	var out []string
	add := func(y string) {
		if y = strings.TrimSpace(y); y != "" && !seen[y] {
			seen[y] = true
			out = append(out, y)
		}
	}
	for _, y := range p.data.AvailableAcademicYears {
		add(y)
	}
	add(p.data.SelectedAcademicYear)
	// The year shown in the selector (which may be the current academic
	// year when the server didn't say) must be a stop too.
	add(p.curYear())
	sort.Strings(out)
	return out
}

func (p *page) curYear() string {
	for _, y := range []string{p.year, p.data.SelectedAcademicYear, p.data.CurrentAcademicYear} {
		if y = strings.TrimSpace(y); y != "" {
			return y
		}
	}
	return ""
}

func (p *page) neighbourYear(delta int, wrap bool) (string, bool) {
	ys := p.years()
	n := len(ys)
	if n < 2 {
		return "", false
	}
	i := index(ys, p.curYear())
	if i < 0 {
		i = n - 1
	}
	j := i + delta
	if wrap {
		j = ((j % n) + n) % n
	}
	if j < 0 || j >= n || j == i {
		return "", false
	}
	return ys[j], true
}

func (p *page) stepYear(delta int, wrap bool) tea.Cmd {
	if len(p.years()) < 2 {
		if !p.load.Loaded {
			return nil
		}
		return ui.Info("%s", ui.Tr("No other academic years available", "Других учебных лет нет"))
	}
	y, ok := p.neighbourYear(delta, wrap)
	if !ok {
		if delta < 0 {
			return ui.Info("%s", ui.Tr("No older academic year", "Более ранних учебных лет нет"))
		}
		return ui.Info("%s", ui.Tr("No newer academic year", "Более поздних учебных лет нет"))
	}
	p.year = y
	p.resetData()
	return p.fetch()
}

// resetData drops the shown ratings when the selection changes, so the
// list never shows one year under another year's header.
func (p *page) resetData() {
	p.data.Items = nil
	p.selKey = ""
	p.scroll.Reset()
	p.rebuild()
}

func sameID(a, b string) bool {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	return a == b || strings.TrimLeft(a, "0") == strings.TrimLeft(b, "0")
}

// programs are the selectable programs: entries without an id are dropped
// and duplicates (1306560 and "001306560" are the same) merged, so p always
// moves to a different program.
func (p *page) programs() []api.Program {
	var out []api.Program
next:
	for _, pr := range p.data.AvailablePrograms {
		if strings.TrimSpace(string(pr.ID)) == "" {
			continue
		}
		for _, o := range out {
			if sameID(string(o.ID), string(pr.ID)) {
				continue next
			}
		}
		out = append(out, pr)
	}
	return out
}

func (p *page) programIndex() int {
	progs := p.programs()
	for _, want := range []string{p.program, string(p.data.SelectedProgram)} {
		for i, pr := range progs {
			if sameID(string(pr.ID), want) {
				return i
			}
		}
	}
	return -1
}

func (p *page) cycleProgram() tea.Cmd {
	progs := p.programs()
	if len(progs) < 2 {
		if !p.load.Loaded {
			return nil
		}
		return ui.Info("%s", ui.Tr("Only one program available", "Доступна только одна программа"))
	}
	p.program = strings.TrimSpace(string(progs[(p.programIndex()+1)%len(progs)].ID))
	p.resetData()
	return p.fetch()
}

func (p *page) programLabel() string {
	if i := p.programIndex(); i >= 0 {
		pr := p.programs()[i]
		name := ui.Clean(pr.DisplayName())
		if d := ui.Clean(pr.Description); d != "" {
			return name + ui.StyleDim.Render(" · "+d)
		}
		return name
	}
	return ui.Clean(p.program)
}

// ------------------------------------------------------------ actions

func (p *page) menu() tea.Cmd {
	r := p.selected()
	if r == nil {
		return nil
	}
	rt := *r
	actions := []ui.Action{{Key: "c", Label: ui.Tr("Copy summary (place, percentile, GPA)", "Копировать сводку (место, процентиль, GPA)"), Run: func() tea.Cmd { return copySummary(rt) }}}
	if t := normType(rt.Type); t != "" {
		if p.typ == t {
			actions = append(actions, ui.Action{Label: ui.Tr("Show all rating types", "Показать все типы рейтинга"), Run: func() tea.Cmd { p.setType(""); return nil }})
		} else {
			actions = append(actions, ui.Action{Label: ui.Tr("Show only: ", "Показать только: ") + typeName(t), Run: func() tea.Cmd { p.setType(t); return nil }})
		}
	}
	return ui.ShowMenu(shortTitle(rt), actions...)
}

// summary is the plain-text gist of a rating for the clipboard.
func summary(r api.Rating) string {
	title := ui.Clean(r.Title)
	if title == "" {
		title = shortTitle(r)
	}
	var parts []string
	if r.PlaceCurr.OK {
		s := ui.Tr("place ", "место ") + r.PlaceCurr.String()
		if r.PlaceTotal.OK {
			s += "/" + r.PlaceTotal.String()
		}
		parts = append(parts, s)
	}
	if r.PlaceOpCurr.OK {
		s := r.PlaceOpCurr.String()
		if r.PlaceOpTotal.OK {
			s += "/" + r.PlaceOpTotal.String()
		}
		parts = append(parts, s+ui.Tr(" in program", " в программе"))
	}
	if r.Percentil.OK {
		parts = append(parts, ui.Tr("percentile ", "процентиль ")+r.Percentil.String())
	}
	if r.GPA.OK {
		parts = append(parts, "GPA "+r.GPA.String())
	}
	if len(parts) == 0 {
		return title
	}
	return title + ": " + strings.Join(parts, ", ")
}

func copySummary(r api.Rating) tea.Cmd {
	return ui.Copy(summary(r), ui.Tr("rating summary", "сводка рейтинга"))
}

// ---------------------------------------------------------- formatting

var (
	parenRe  = regexp.MustCompile(`\(([^()]*)\)\s*$`)
	rangeRe  = regexp.MustCompile(`^(\d+)\s*[-–—]\s*(\d+)\s*(\S*)`)
	singleRe = regexp.MustCompile(`^(\d+)\s*(\S*)`)
	moduleRe = regexp.MustCompile(`(?i)(\d+)\s*(?:-?(?:й|ый|ой)\s*)?(?:модул|module)`)
	yearRe   = regexp.MustCompile(`\d{4}/\d{4}`)
)

func isModuleWord(w string) bool {
	w = strings.ToLower(w)
	return w == "" || strings.HasPrefix(w, "module") || strings.HasPrefix(w, "модул")
}

// periodPart extracts the modules a rating covers ("modules 3–4") from
// the title's parenthetical or the learn period.
func periodPart(r api.Rating) string {
	if m := parenRe.FindStringSubmatch(ui.Clean(r.Title)); m != nil {
		in := strings.TrimSpace(m[1])
		if mm := rangeRe.FindStringSubmatch(in); mm != nil && isModuleWord(mm[3]) {
			return ui.Tr("modules ", "модули ") + mm[1] + "–" + mm[2]
		}
		if mm := singleRe.FindStringSubmatch(in); mm != nil && isModuleWord(mm[2]) {
			return moduleLabel(mm[1])
		}
		if in != "" {
			return in
		}
	}
	if m := moduleRe.FindStringSubmatch(ui.Clean(r.LearnPeriod)); m != nil {
		return moduleLabel(m[1])
	}
	return ""
}

// moduleLabel is "module 4" / "модуль 4".
func moduleLabel(n string) string { return ui.Tr("module ", "модуль ") + n }

// shortTitle is the list label: "Current · modules 3–4".
func shortTitle(r api.Rating) string {
	if pp := periodPart(r); pp != "" {
		return typeName(r.Type) + " · " + pp
	}
	if t := ui.Clean(r.Title); t != "" {
		return t
	}
	if ui.RU() {
		return "Рейтинг: " + strings.ToLower(typeName(r.Type))
	}
	return typeName(r.Type) + " rating"
}

// periodText is the learn period in the interface language when it can be
// parsed ("2025/2026 учебный год 4 модуль" → "2025/2026 · module 4").
func periodText(r api.Rating) string {
	lp := ui.Clean(r.LearnPeriod)
	year := yearRe.FindString(lp)
	if year == "" {
		year = ui.Clean(r.LearnYear)
	}
	if m := moduleRe.FindStringSubmatch(lp); m != nil {
		return ui.JoinNonEmpty(" · ", year, moduleLabel(m[1]))
	}
	if lp != "" {
		return lp
	}
	return year
}

// placeShort is the compact place info of a list row.
func placeShort(r api.Rating) string {
	switch {
	case r.Percentil.OK:
		return ui.Trf("top %.0f%%", "топ %.0f%%", r.Percentil.V)
	case r.PlaceCurr.OK:
		return "#" + r.PlaceCurr.String()
	}
	return ""
}

func degreeName(d string) string {
	c := strings.ToUpper(ui.Clean(d))
	switch c {
	case "":
		return ""
	case "DEGREE_BACHELOR", "BACHELOR":
		return ui.Tr("Bachelor", "Бакалавриат")
	case "DEGREE_MASTER", "MASTER":
		return ui.Tr("Master", "Магистратура")
	case "DEGREE_SPECIALIST", "SPECIALIST":
		return ui.Tr("Specialist", "Специалитет")
	case "DEGREE_POSTGRADUATE", "DEGREE_PHD", "DEGREE_ASPIRANT", "POSTGRADUATE":
		return ui.Tr("Postgraduate", "Аспирантура")
	}
	return titleCase(strings.TrimPrefix(c, "DEGREE_"))
}

func styleLines(st lipgloss.Style, s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = st.Render(l)
	}
	return strings.Join(lines, "\n")
}

func gradeNum(n api.Num) string {
	if !n.OK {
		return ui.StyleDim.Render("—")
	}
	return ui.GradeStyle(n.V).Render(n.String())
}

func placesBlock(r api.Rating, w int) string {
	type place struct {
		label    string
		cur, tot api.Num
	}
	var rows []place
	for _, pl := range []place{
		{ui.Tr("Overall", "Общий"), r.PlaceCurr, r.PlaceTotal},
		{ui.Tr("In program", "В программе"), r.PlaceOpCurr, r.PlaceOpTotal},
		{ui.Tr("In course", "На курсе"), r.PlaceOpCourseCurr, r.PlaceOpCourseTotal},
		{ui.Tr("In group", "В группе"), r.PlaceGroupOpCurr, r.PlaceGroupOpTotal},
	} {
		if pl.cur.OK {
			rows = append(rows, pl)
		}
	}
	cw, tw := 0, 0
	for _, pl := range rows {
		cw = max(cw, ui.Width(pl.cur.String()))
		if pl.tot.OK {
			tw = max(tw, ui.Width(pl.tot.String()))
		}
	}
	var lines []string
	for _, pl := range rows {
		v := ui.StyleBold.Render(ui.PadLeft(pl.cur.String(), cw))
		if pl.tot.OK {
			v += ui.StyleDim.Render(" / ") + ui.PadRight(pl.tot.String(), tw)
			if pl.tot.V > 0 && pl.cur.V >= 0 && pl.cur.V <= pl.tot.V {
				pct := max(1, math.Round(pl.cur.V/pl.tot.V*100))
				v += ui.StyleDim.Render(ui.Trf("  top %.0f%%", "  топ %.0f%%", pct))
			}
		}
		lines = append(lines, ui.KV(pl.label, v, w))
	}
	if r.Percentil.OK {
		lines = append(lines, ui.KV(ui.Tr("Percentile", "Процентиль"), r.Percentil.String(), w))
	}
	return ui.Lines(lines...)
}

func scoresBlock(r api.Rating, w int) string {
	var lines []string
	if r.Rating.OK {
		v := ui.StyleBold.Render(r.Rating.String())
		if r.RatingNorm.OK && r.RatingNorm.V != r.Rating.V {
			v += ui.StyleDim.Render(ui.Tr(" · normalized ", " · нормированный ") + r.RatingNorm.String())
		}
		lines = append(lines, ui.KV(ui.Tr("Rating", "Рейтинг"), v, w))
	} else if r.RatingNorm.OK {
		lines = append(lines, ui.KV(ui.Tr("Rating", "Рейтинг"), r.RatingNorm.String()+ui.StyleDim.Render(ui.Tr(" normalized", " нормированный")), w))
	}
	for _, g := range []struct {
		label string
		n     api.Num
	}{
		{"GPA", r.GPA},
		{ui.Tr("Average", "Средняя"), r.GradeMid},
		{ui.Tr("Minimum", "Минимум"), r.GradeMin},
		{ui.Tr("Minor grade", "За майнор"), r.GradeMinor},
	} {
		if g.n.OK {
			lines = append(lines, ui.KV(g.label, gradeNum(g.n), w))
		}
	}
	credits := r.Credits
	if !credits.OK {
		credits = r.CreditsPrecise
	}
	if credits.OK {
		v := credits.String()
		if r.Credits.OK && r.CreditsPrecise.OK && r.CreditsPrecise.V != r.Credits.V {
			v += ui.StyleDim.Render(ui.Trf(" (%s precise)", " (точно %s)", r.CreditsPrecise.String()))
		}
		lines = append(lines, ui.KV(ui.Tr("Credits", "Кредиты"), v, w))
	}
	return ui.Lines(lines...)
}

func examsBlock(r api.Rating, w int) string {
	var lines []string
	if r.ExamAll.OK {
		lines = append(lines, ui.KV(ui.Tr("Total", "Всего"), r.ExamAll.String(), w))
	}
	type bucket struct {
		label string
		n     api.Num
		rep   float64
	}
	var parts, rows []string
	for _, b := range []bucket{{"8–10", r.Exam10, 9}, {"6–7", r.Exam7, 6.5}, {"4–5", r.Exam5, 4.5}, {"0–3", r.Exam3, 0}} {
		if !b.n.OK {
			continue
		}
		parts = append(parts, ui.GradeStyle(b.rep).Render(b.label)+": "+b.n.String())
		rows = append(rows, ui.KV(b.label, b.n.String(), w))
	}
	if len(parts) > 0 {
		one := strings.Join(parts, ui.StyleDim.Render(" · "))
		if ui.Width(one) <= w-ui.LabelWidth-1 && w >= 30 {
			lines = append(lines, ui.KV(ui.Tr("By grade", "По оценкам"), one, w))
		} else {
			lines = append(lines, rows...)
		}
	}
	if r.ExamRetake.OK {
		v := ui.StyleDim.Render(ui.Tr("none", "нет"))
		if r.ExamRetake.V > 0 {
			v = ui.StyleWarn.Render(r.ExamRetake.String())
			if r.ExamRetakeGood.OK {
				v += ui.StyleDim.Render(ui.Trf(" (%s passed)", " (сдано %s)", r.ExamRetakeGood.String()))
			}
		}
		lines = append(lines, ui.KV(ui.Tr("Retakes", "Пересдачи"), v, w))
	}
	var other []string
	for _, o := range []struct {
		name string
		n    api.Num
	}{{"examb", r.ExamB}, {"examg", r.ExamG}, {"examn", r.ExamN}, {"examz", r.ExamZ}, {"exame", r.ExamE}, {"examf", r.ExamF}, {"examp", r.ExamP}} {
		if o.n.OK && o.n.V != 0 {
			other = append(other, o.name+" "+o.n.String())
		}
	}
	if len(other) > 0 {
		lines = append(lines, ui.KV(ui.Tr("Other", "Другое"), ui.StyleDim.Render(strings.Join(other, " · ")), w))
	}
	return ui.Lines(lines...)
}

func disciplinesBlock(list []api.RatingDiscipline, w int) string {
	if len(list) == 0 || w <= 0 {
		return ""
	}
	ds := append([]api.RatingDiscipline(nil), list...)
	sort.SliceStable(ds, func(i, j int) bool {
		a, b := ds[i].Grade, ds[j].Grade
		if a.OK != b.OK {
			return a.OK
		}
		if a.V != b.V {
			return a.V > b.V
		}
		return strings.ToLower(ui.Clean(ds[i].Discipline)) < strings.ToLower(ui.Clean(ds[j].Discipline))
	})
	gradeHead, crHead := ui.Tr("grade", "оценка"), ui.Tr("cr", "кр.")
	gw, cw, longest := max(5, ui.Width(gradeHead)), ui.Width(crHead), 10
	for _, d := range ds {
		gw = max(gw, ui.Width(d.Grade.String()))
		cw = max(cw, ui.Width(d.CurrentRating.String()))
		longest = max(longest, ui.Width(ui.Clean(d.Discipline)))
	}
	// Keep the numbers next to the names on wide panes.
	nameW := min(w-gw-cw-2, longest+2)
	if nameW < 4 {
		// Too narrow for columns: names only.
		var lines []string
		for _, d := range ds {
			lines = append(lines, ui.Trunc(ui.Clean(d.Discipline), w))
		}
		return strings.Join(lines, "\n")
	}
	lines := []string{ui.StyleDim.Render(strings.Repeat(" ", nameW+1) + ui.PadLeft(gradeHead, gw) + " " + ui.PadLeft(crHead, cw))}
	for _, d := range ds {
		name := ui.Clean(d.Discipline)
		if name == "" {
			name = "—"
		}
		cr := ui.StyleDim.Render(d.CurrentRating.String())
		lines = append(lines, ui.PadRight(name, nameW)+" "+ui.PadLeft(gradeNum(d.Grade), gw)+" "+ui.PadLeft(cr, cw))
	}
	return strings.Join(lines, "\n")
}

func (p *page) detail(w int) string {
	r := p.selected()
	if r == nil || w <= 0 {
		return ""
	}
	title := ui.Clean(r.Title)
	if title == "" {
		title = shortTitle(*r)
	}
	var published string
	if !r.PublishedAt.IsZero() {
		published = ui.FmtDate(r.PublishedAt.Time)
	}
	course := ""
	if r.Course.OK {
		// A no-break space keeps "1 курс" on one line.
		course = ui.Trf("course %s", "%s\u00a0курс", r.Course.String())
	}
	campus := ""
	if c := ui.Clean(r.Campus); c != "" {
		campus = ui.Clean(ui.CampusName(c))
	}
	meta := ui.Lines(
		ui.KV(ui.Tr("Published", "Публикация"), published, w),
		ui.KV(ui.Tr("Period", "Период"), periodText(*r), w),
		ui.KV(ui.Tr("Program", "Программа"), ui.JoinNonEmpty(" · ", ui.Clean(r.LearnProgram), ui.Clean(r.Group), course), w),
		ui.KV(ui.Tr("Faculty", "Факультет"), ui.Clean(r.Faculty), w),
		ui.KV(ui.Tr("Degree", "Уровень"), ui.JoinNonEmpty(" · ", degreeName(r.Degree), campus), w),
	)
	return ui.Lines(
		styleLines(ui.StyleTitle, ui.Wrap(title, w)),
		meta,
		ui.Section(ui.Tr("Places", "Места"), placesBlock(*r, w)),
		ui.Section(ui.Tr("Scores", "Баллы"), scoresBlock(*r, w)),
		ui.Section(ui.Tr("Exams", "Экзамены"), examsBlock(*r, w)),
		ui.Section(ui.Tr("Disciplines", "Дисциплины"), disciplinesBlock(r.DisciplineList, w)),
	)
}

// ---------------------------------------------------------------- view

func (p *page) selectorLine(width int) string {
	year := ui.Clean(p.curYear())
	if year == "" {
		year = ui.Tr("Current year", "Текущий год")
	}
	left := ui.StyleKey.Render(year)
	if len(p.years()) > 1 {
		left += ui.StyleDim.Render(" ‹y›")
	}
	left += "  " + ui.StyleKey.Render(p.typeLabel())
	if len(p.types()) > 1 {
		left += ui.StyleDim.Render(" ‹t›")
	}
	prog := p.programLabel()
	if prog == "" {
		return ui.Trunc(left, width)
	}
	hint := ""
	if len(p.programs()) > 1 {
		hint = ui.StyleDim.Render(" ‹p›")
	}
	avail := width - ui.Width(left) - 2 - ui.Width(hint)
	if avail < 6 {
		return ui.Trunc(left, width)
	}
	return ui.Trunc(left+"  "+ui.Trunc(prog, avail)+hint, width)
}

func (p *page) renderRow(i int, sel bool, w int) string {
	if i < 0 || i >= len(p.items) {
		return ""
	}
	r := *p.items[i]
	inner := w - 1
	right := placeShort(r)
	titleW := inner - 4
	if right != "" {
		titleW -= ui.Width(right) + 1
		if titleW < 8 {
			titleW += ui.Width(right) + 1
			right = ""
		}
	}
	title := shortTitle(r)
	if ui.Width(title) > titleW {
		if pp := periodPart(r); pp != "" {
			title = pp
		}
	}
	content := typeBadge(r.Type) + " " + ui.PadRight(title, max(0, titleW))
	if right != "" {
		content += " " + ui.StyleDim.Render(right)
	}
	return ui.Row(sel, content, w)
}

func (p *page) listTitle() string {
	n, total := len(p.items), len(p.data.Items)
	if p.typ != "" && n != total {
		return ui.Trf("%d of %d ratings", "Рейтинги: %d из %d", n, total)
	}
	return ui.Count(n, "rating", "ratings", "рейтинг", "рейтинга", "рейтингов")
}

func (p *page) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	head := []string{p.selectorLine(width)}
	if b := p.banner.View(p.ctx, width); b != "" && height > 4 {
		head = append(head, b)
	}
	bodyH := height - len(head)
	if bodyH <= 0 {
		return ui.Fit(strings.Join(head, "\n"), width, height)
	}
	body := ui.StateView(width, bodyH, p.load, len(p.items), emptyText())
	if body == "" {
		lw := ui.LeftWidth(width)
		rw := ui.RightWidth(width, lw)
		left := ui.PaneTitle(p.listTitle(), ui.LoadNote(p.load), lw)
		if bodyH > 1 {
			left += "\n" + p.list.Render(lw, bodyH-1, p.renderRow)
		}
		right := p.scroll.Render(p.detail(rw), rw, bodyH)
		body = ui.Split(width, bodyH, lw, left, right)
	}
	return ui.Fit(strings.Join(head, "\n")+"\n"+body, width, height)
}

func (p *page) Hints() []ui.Hint {
	h := []ui.Hint{
		{Key: "t", Desc: ui.Tr("type", "тип")},
		{Key: "enter", Desc: ui.Tr("actions", "действия")},
		{Key: "c", Desc: ui.Tr("copy summary", "копировать сводку")},
		{Key: "y/Y", Desc: ui.Tr("year", "год")},
	}
	if len(p.programs()) > 1 {
		h = append(h, ui.Hint{Key: "p", Desc: ui.Tr("program", "программа")})
	}
	if p.banner.Dismissible(p.ctx) {
		h = append(h, ui.Hint{Key: "x", Desc: ui.Tr("hide banner", "скрыть объявление")})
	}
	return append(h,
		ui.Hint{Key: "J/K", Desc: ui.Tr("scroll details", "прокрутка деталей")},
		ui.Hint{Key: "h/l ←/→", Desc: ui.Tr("older / newer year", "пред. / след. год")},
		ui.Hint{Key: "r", Desc: ui.Tr("refresh", "обновить")},
	)
}
