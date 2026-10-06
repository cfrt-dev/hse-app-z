// Package grades is the Grades tab: the student's marks for one academic
// year and program, grouped by module, with the details of the selected
// discipline next to the list.
package grades

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"hse-app-z/internal/api"
	"hse-app-z/internal/ui"
)

type page struct {
	ctx *ui.Ctx
	id  int

	load ui.Load
	data api.GradesResponse
	// year and program are the requested selection ("" = server default:
	// current academic year, main program).
	year, program string

	rows   []row
	list   ui.List
	scroll ui.Scroll
	// selID is the selected grade's id, kept across reloads.
	selID string

	banner ui.Banners

	// who is the account the data was last requested for (see ReloadMsg).
	who     string
	fetched bool
}

// row is a list entry: a module header (g == nil) or a grade.
type row struct {
	header string
	g      *api.Grade
}

// New returns the tab page.
func New(ctx *ui.Ctx) ui.Page {
	p := &page{ctx: ctx, id: ui.NewID(), banner: ui.Banners{Section: "grades"}}
	p.list.Skip = func(i int) bool { return i >= 0 && i < len(p.rows) && p.rows[i].g == nil }
	return p
}

func (p *page) Title() string   { return ui.Tr("Grades", "Оценки") }
func (p *page) Capturing() bool { return false }

func (p *page) Init() tea.Cmd { return tea.Batch(p.fetch(), p.banner.Fetch(p.ctx, p.id)) }

func (p *page) fetch() tea.Cmd {
	if p.ctx == nil || p.ctx.API == nil {
		return nil
	}
	client, year, prog := p.ctx.API, p.year, p.program
	p.who, p.fetched = p.ctx.Me.Email, true
	return ui.Fetch(p.id, p.load.Begin(), func(c context.Context) (api.GradesResponse, api.Meta, error) {
		return client.Grades(c, year, prog)
	})
}

func (p *page) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case ui.Result[api.GradesResponse]:
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
		if g := p.selected(); g != nil {
			return copyGrade(*g)
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
		p.syncSelection()
	}
	return nil
}

// ------------------------------------------------------------- data

func (p *page) apply(resp api.GradesResponse) {
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
	p.rebuild()
}

// forgetOtherAccount runs on reload: when a different account has signed
// in, the previous one's grades must not stay on screen while (or if) the
// reload fails, and its year/program ids mean nothing for the new account.
func (p *page) forgetOtherAccount() {
	if p.ctx == nil || !p.fetched || p.who == p.ctx.Me.Email {
		return
	}
	p.year, p.program = "", ""
	p.data = api.GradesResponse{}
	p.load.Loaded, p.load.Err, p.load.Meta = false, nil, api.Meta{}
	p.resetData()
}

// rebuild regroups the items into module sections, keeping the selection.
func (p *page) rebuild() {
	items := append([]api.Grade(nil), p.data.Items...)
	sort.SliceStable(items, func(i, j int) bool { return less(items[i], items[j]) })
	p.rows = p.rows[:0]
	last := ""
	for i := range items {
		_, label := moduleOf(items[i])
		if i == 0 || label != last {
			p.rows = append(p.rows, row{header: label})
			last = label
		}
		p.rows = append(p.rows, row{g: &items[i]})
	}
	p.list.SetLen(len(p.rows))
	found := -1
	if p.selID != "" {
		for i, r := range p.rows {
			if r.g != nil && string(r.g.ID) == p.selID {
				found = i
				break
			}
		}
	}
	if found >= 0 {
		p.list.Select(found)
	} else {
		p.list.Select(p.list.Cursor)
		p.scroll.Reset()
	}
	if g := p.selected(); g != nil {
		p.selID = string(g.ID)
	}
}

func (p *page) syncSelection() {
	p.scroll.Reset()
	p.selID = ""
	if g := p.selected(); g != nil {
		p.selID = string(g.ID)
	}
}

func (p *page) selected() *api.Grade {
	if !p.list.HasSelection() || p.list.Cursor >= len(p.rows) {
		return nil
	}
	return p.rows[p.list.Cursor].g
}

var digitsRe = regexp.MustCompile(`\d+`)

// Sort keys of the module groups that have no module number.
const (
	keyNamed = 1000 // a module name without a number ("Summer school")
	keyOther = 2000 // no module information at all
)

// moduleOf returns a sort key and header label for a grade's module.
func moduleOf(g api.Grade) (int, string) {
	if n, err := strconv.Atoi(strings.TrimSpace(string(g.ModuleNum))); err == nil && n > 0 && n < 100 {
		return n, ui.Trf("Module %d", "Модуль %d", n)
	}
	name := ui.Clean(g.ModuleName)
	if m := digitsRe.FindString(name); m != "" {
		if n, err := strconv.Atoi(m); err == nil && n > 0 && n < 100 {
			return n, ui.Trf("Module %d", "Модуль %d", n)
		}
	}
	if name != "" {
		return keyNamed, name
	}
	return keyOther, ui.Tr("Other", "Другое")
}

// less orders grades by module, then date (undated last), then name.
func less(a, b api.Grade) bool {
	ka, la := moduleOf(a)
	kb, lb := moduleOf(b)
	if ka != kb {
		return ka < kb
	}
	if la != lb {
		return la < lb
	}
	da, db := a.Date.Time, b.Date.Time
	if da.IsZero() != db.IsZero() {
		return !da.IsZero()
	}
	if !da.Equal(db) {
		return da.Before(db)
	}
	return strings.ToLower(ui.Clean(a.Discipline)) < strings.ToLower(ui.Clean(b.Discipline))
}

// ------------------------------------------------------ year & program

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

// neighbourYear is the year delta steps away (negative = older); ok is
// false at either end unless wrap is set.
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
	return p.selectYear(y)
}

// selectYear switches to another year. The old year's grades are cleared
// so the list never shows one year under another year's header.
func (p *page) selectYear(y string) tea.Cmd {
	p.year = y
	p.resetData()
	return p.fetch()
}

func (p *page) resetData() {
	p.data.Items = nil
	p.selID = ""
	p.list.Cursor = 0
	p.scroll.Reset()
	p.rebuild()
}

func index(xs []string, x string) int {
	for i, v := range xs {
		if v == x {
			return i
		}
	}
	return -1
}

func sameID(a, b string) bool {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	return a == b || strings.TrimLeft(a, "0") == strings.TrimLeft(b, "0")
}

// programs are the selectable programs: entries without an id are dropped
// and duplicates ("000275111" and 275111 are the same) merged, so p always
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

func (p *page) nextProgram() (api.Program, bool) {
	progs := p.programs()
	if len(progs) < 2 {
		return api.Program{}, false
	}
	i := p.programIndex()
	return progs[(i+1)%len(progs)], true
}

func (p *page) cycleProgram() tea.Cmd {
	next, ok := p.nextProgram()
	if !ok {
		if !p.load.Loaded {
			return nil
		}
		return ui.Info("%s", ui.Tr("Only one program available", "Доступна только одна программа"))
	}
	p.program = strings.TrimSpace(string(next.ID))
	p.resetData()
	return p.fetch()
}

// programLabel is "Name · description" for the selected program.
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
	g := p.selected()
	if g == nil {
		return nil
	}
	gr := *g
	actions := []ui.Action{{Key: "c", Label: ui.Tr("Copy discipline and grade", "Копировать дисциплину и оценку"), Run: func() tea.Cmd { return copyGrade(gr) }}}
	if y, ok := p.neighbourYear(-1, true); ok {
		actions = append(actions, ui.Action{Key: "y", Label: ui.Tr("Switch to ", "Перейти на ") + ui.Clean(y), Run: func() tea.Cmd { return p.selectYear(y) }})
	}
	if next, ok := p.nextProgram(); ok {
		id := strings.TrimSpace(string(next.ID))
		actions = append(actions, ui.Action{Key: "p", Label: ui.Tr("Switch program to ", "Сменить программу на ") + ui.Clean(next.DisplayName()), Run: func() tea.Cmd {
			p.program = id
			p.resetData()
			return p.fetch()
		}})
	}
	return ui.ShowMenu(ui.Clean(gr.Discipline), actions...)
}

func copyGrade(g api.Grade) tea.Cmd {
	return ui.Copy(ui.Clean(g.Discipline)+" — "+plainGrade(g), ui.Tr("grade", "оценка"))
}

// ---------------------------------------------------------- formatting

// fiveName is the word for a five-point mark ("" for unknown marks).
func fiveName(f int) string {
	switch f {
	case 5:
		return ui.Tr("excellent", "отлично")
	case 4:
		return ui.Tr("good", "хорошо")
	case 3:
		return ui.Tr("satisfactory", "удовлетворительно")
	case 2:
		return ui.Tr("unsatisfactory", "неудовлетворительно")
	}
	return ""
}

// fivePoint is the five-point mark (derived from the ten-point one when
// the API omits it).
func fivePoint(gv *api.GradeValue) int {
	if gv.FivePoint.OK {
		return gv.FivePoint.Int()
	}
	switch v := gv.TenPoint.V; {
	case v >= 8:
		return 5
	case v >= 6:
		return 4
	case v >= 4:
		return 3
	}
	return 2
}

// plainGrade is the uncolored mark for copying.
func plainGrade(g api.Grade) string {
	switch {
	case g.Grade != nil && g.Grade.TenPoint.OK:
		s := g.Grade.TenPoint.String() + "/10"
		f := fivePoint(g.Grade)
		if name := fiveName(f); name != "" {
			s += fmt.Sprintf(" (%d, %s)", f, name)
		}
		return s
	case g.Grade != nil && g.Grade.Pass != nil && *g.Grade.Pass:
		return ui.Tr("pass", "зачёт")
	case g.Grade != nil && g.Grade.Pass != nil:
		return ui.Tr("fail", "незачёт")
	}
	return ui.Tr("not graded yet", "оценки ещё нет")
}

// markCell is the right-aligned mark of a list row (4 cells).
func markCell(g api.Grade) string {
	var s string
	switch {
	case g.Grade != nil && g.Grade.TenPoint.OK:
		s = ui.GradeStyle(g.Grade.TenPoint.V).Bold(true).Render(ui.Trunc(g.Grade.TenPoint.String(), 4))
	case g.Grade != nil && g.Grade.Pass != nil && *g.Grade.Pass:
		s = ui.StyleOK.Render(ui.Tr("pass", "зач"))
	case g.Grade != nil && g.Grade.Pass != nil:
		s = ui.StyleErr.Render(ui.Tr("fail", "н/з"))
	default:
		s = ui.StyleDim.Render("—")
	}
	return ui.PadLeft(s, 4)
}

// typeTag shortens the assessment type ("Exam" → "exam" / "экз").
func typeTag(raw string) string {
	t := strings.ToLower(ui.Clean(raw))
	switch {
	case t == "":
		return ""
	case strings.Contains(t, "exam") || strings.Contains(t, "экзам"):
		return ui.Tr("exam", "экз")
	case strings.Contains(t, "test") || strings.Contains(t, "зач"):
		return ui.Tr("test", "зач")
	}
	r := []rune(t)
	if len(r) > 4 {
		r = r[:4]
	}
	return string(r)
}

func styleLines(st lipgloss.Style, s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = st.Render(l)
	}
	return strings.Join(lines, "\n")
}

func gradeLine(g api.Grade) string {
	switch {
	case g.Grade != nil && g.Grade.TenPoint.OK:
		s := ui.GradeStyle(g.Grade.TenPoint.V).Bold(true).Render(g.Grade.TenPoint.String() + " / 10")
		f := fivePoint(g.Grade)
		if name := fiveName(f); name != "" {
			s += ui.StyleDim.Render(fmt.Sprintf(" · %d (%s)", f, name))
		}
		return s
	case g.Grade != nil && g.Grade.Pass != nil && *g.Grade.Pass:
		return ui.StyleOK.Bold(true).Render(ui.Tr("Pass", "Зачёт"))
	case g.Grade != nil && g.Grade.Pass != nil:
		return ui.StyleErr.Bold(true).Render(ui.Tr("Fail", "Незачёт"))
	}
	return ui.StyleDim.Render(ui.Tr("Not graded yet", "Оценки ещё нет"))
}

func hoursText(g api.Grade) string {
	aud, all := g.AudHours, g.EntireHours
	switch {
	case aud.OK && all.OK && all.V > 0:
		return ui.Trf("%s classroom of %s total", "%s аудиторных из %s", aud.String(), all.String())
	case aud.OK && aud.V > 0:
		return ui.Trf("%s classroom", "%s аудиторных", aud.String())
	case all.OK && all.V > 0:
		return ui.Trf("%s total", "%s всего", all.String())
	}
	return ""
}

func creditsText(g api.Grade) string {
	if !g.Credits.OK {
		if g.PeriodCredits.OK {
			return g.PeriodCredits.String()
		}
		return ""
	}
	s := g.Credits.String()
	if g.PeriodCredits.OK && g.PeriodCredits.V != g.Credits.V {
		s += ui.StyleDim.Render(ui.Trf(" (%s this period)", " (%s в этом периоде)", g.PeriodCredits.String()))
	}
	return s
}

func lecturers(s string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == '\n' }) {
		if c := ui.Clean(part); c != "" {
			out = append(out, c)
		}
	}
	return out
}

func (p *page) detail(width int) string {
	g := p.selected()
	if g == nil || width <= 0 {
		return ""
	}
	name := ui.Clean(g.Discipline)
	if name == "" {
		name = ui.Tr("Untitled discipline", "Дисциплина без названия")
	}
	var kv []string
	add := func(label, value string) { kv = append(kv, ui.KV(label, value, width)) }
	add(ui.Tr("Type", "Тип"), ui.Clean(g.TypeRaw))
	if !g.Date.IsZero() {
		add(ui.Tr("Date", "Дата"), ui.FmtDate(g.Date.Time))
	}
	switch key, label := moduleOf(*g); {
	case key < keyNamed:
		add(ui.Tr("Module", "Модуль"), strconv.Itoa(key))
	case key == keyNamed:
		add(ui.Tr("Module", "Модуль"), label)
	}
	add(ui.Tr("Credits", "Кредиты"), creditsText(*g))
	add(ui.Tr("Hours", "Часы"), hoursText(*g))
	if g.RepassCount.OK {
		if g.RepassCount.V > 0 {
			add(ui.Tr("Retakes", "Пересдачи"), ui.StyleWarn.Render(g.RepassCount.String()))
		} else {
			add(ui.Tr("Retakes", "Пересдачи"), ui.StyleDim.Render(ui.Tr("none", "нет")))
		}
	}
	if ls := lecturers(g.Lecturer); len(ls) > 0 {
		label := ui.Tr("Lecturer", "Лектор")
		if len(ls) > 1 {
			label = ui.Tr("Lecturers", "Лекторы")
		}
		add(label, ls[0])
		for _, l := range ls[1:] {
			add("", l)
		}
	}
	add(ui.Tr("Year", "Год"), ui.Clean(g.AcademicYear))
	return styleLines(ui.StyleTitle, ui.Wrap(name, width)) + "\n\n" +
		ui.Trunc(gradeLine(*g), width) + "\n\n" +
		ui.Lines(kv...)
}

// summary is the list pane title: year, average, credits, progress.
func (p *page) summary(width int) string {
	var n, graded, numeric int
	var sum, credits, creditsAll float64
	hasCredits := false
	for _, g := range p.data.Items {
		n++
		if g.Credits.OK {
			hasCredits = true
			creditsAll += g.Credits.V
		}
		if !g.HasGrade() {
			continue
		}
		graded++
		if g.Credits.OK {
			credits += g.Credits.V
		}
		if g.Grade.TenPoint.OK {
			numeric++
			sum += g.Grade.TenPoint.V
		}
	}
	year := ui.Clean(p.data.SelectedAcademicYear)
	if year == "" {
		year = ui.Clean(p.curYear())
	}
	var avg, cr, prog string
	if numeric > 0 {
		avg = ui.Tr("avg ", "ср. ") + trimFloat(sum/float64(numeric))
	}
	crUnit := ui.Tr(" cr", " кр.")
	switch {
	case !hasCredits:
		// No credit data: say nothing rather than "0 cr".
	case credits == creditsAll || credits == 0:
		cr = trimFloat(creditsAll) + crUnit
	default:
		cr = trimFloat(credits) + "/" + trimFloat(creditsAll) + crUnit
	}
	if graded < n {
		prog = ui.Trf("%d/%d graded", "%d/%d с оценкой", graded, n)
	}
	candidates := []string{
		ui.JoinNonEmpty(" · ", year, avg, cr, prog),
		ui.JoinNonEmpty(" · ", year, avg, prog),
		ui.JoinNonEmpty(" · ", avg, cr, prog),
		ui.JoinNonEmpty(" · ", avg, prog),
	}
	for _, c := range candidates {
		if c != "" && ui.Width(c) <= width {
			return c
		}
	}
	return ui.Trunc(candidates[0], width)
}

func trimFloat(v float64) string {
	s := strconv.FormatFloat(v, 'f', 2, 64)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
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
	if i < 0 || i >= len(p.rows) {
		return ""
	}
	r := p.rows[i]
	if r.g == nil {
		return ui.HeaderRow(r.header, w)
	}
	inner := w - 1
	nameW := inner - 5
	tag := ""
	if t := typeTag(r.g.TypeRaw); t != "" && nameW-5 >= 24 {
		nameW -= 5
		tag = " " + ui.StyleDim.Render(ui.PadRight(t, 4))
	}
	if nameW < 1 {
		return ui.Row(sel, ui.Clean(r.g.Discipline), w)
	}
	return ui.Row(sel, ui.PadRight(ui.Clean(r.g.Discipline), nameW)+tag+" "+markCell(*r.g), w)
}

func (p *page) emptyText() string {
	if y := ui.Clean(p.curYear()); y != "" {
		return ui.Trf("No grades for %s yet", "Оценок за %s пока нет", y)
	}
	return ui.Tr("No grades yet", "Оценок пока нет")
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
	body := ui.StateView(width, bodyH, p.load, len(p.data.Items), p.emptyText())
	if body == "" {
		lw := ui.LeftWidth(width)
		rw := ui.RightWidth(width, lw)
		note := ui.LoadNote(p.load)
		left := ui.PaneTitle(p.summary(max(0, lw-ui.Width(note)-1)), note, lw)
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
		{Key: "y/Y", Desc: ui.Tr("year", "год")},
		{Key: "enter", Desc: ui.Tr("actions", "действия")},
		{Key: "c", Desc: ui.Tr("copy grade", "копировать оценку")},
	}
	if len(p.programs()) > 1 {
		h = append(h, ui.Hint{Key: "p", Desc: ui.Tr("program", "программа")})
	}
	if p.banner.Dismissible(p.ctx) {
		h = append(h, ui.Hint{Key: "x", Desc: ui.Tr("hide banner", "скрыть объявление")})
	}
	return append(h,
		ui.Hint{Key: "h/l ←/→", Desc: ui.Tr("older / newer year", "пред. / след. год")},
		ui.Hint{Key: "J/K", Desc: ui.Tr("scroll details", "прокрутка деталей")},
		ui.Hint{Key: "r", Desc: ui.Tr("refresh", "обновить")},
	)
}
