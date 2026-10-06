// Package timetable implements the Schedule tab (the signed-in user's
// week) and the profile + timetable page of a person, group or room. Both
// are built on the same week component: lessons grouped by day on the
// left, details of the selected lesson on the right.
package timetable

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"hse-app-z/internal/api"
	"hse-app-z/internal/ui"
)

// tickMsg refreshes "now" markers and relative times.
type tickMsg struct{ id int }

const tickEvery = 30 * time.Second

// tick re-renders periodically; skipped in demo mode, whose clock is fixed.
func tick(ctx *ui.Ctx, id int) tea.Cmd {
	if ctx == nil || ctx.Demo {
		return nil
	}
	return tea.Tick(tickEvery, func(time.Time) tea.Msg { return tickMsg{id: id} })
}

// leftWidth is the lesson list width: a bit wider than the default split so
// discipline names stay readable.
func leftWidth(w int) int {
	lw := ui.LeftWidth(w)
	if w >= 100 {
		lw = max(lw, min(w*9/20, 60))
	}
	return lw
}

// tabPage is the Schedule tab: the signed-in user's own timetable.
type tabPage struct {
	ctx *ui.Ctx
	id  int
	wk  *week
}

// NewTab returns the user's own schedule tab.
func NewTab(ctx *ui.Ctx) ui.Page {
	p := &tabPage{ctx: ctx, id: ui.NewID()}
	p.wk = newWeek(ctx, p.id, func() api.LessonQuery {
		// Re-read on every fetch: the account can change after re-login.
		// No selector = the caller's own lessons.
		return api.LessonQuery{Email: strings.TrimSpace(ctx.Me.Email)}
	})
	return p
}

func (p *tabPage) Init() tea.Cmd { return tea.Batch(p.wk.start(), tick(p.ctx, p.id)) }

func (p *tabPage) Title() string   { return ui.Tr("Schedule", "Расписание") }
func (p *tabPage) Capturing() bool { return false }

func (p *tabPage) Hints() []ui.Hint {
	return []ui.Hint{
		{Key: "h/l", Desc: ui.Tr("week", "неделя")},
		{Key: "t", Desc: ui.Tr("today", "сегодня")},
		{Key: "enter", Desc: ui.Tr("actions", "действия")},
		{Key: "o", Desc: ui.Tr("link", "ссылка")},
		{Key: "p", Desc: ui.Tr("lecturer", "преподаватель")},
		{Key: "a", Desc: ui.Tr("room", "аудитория")},
		{Key: "[/]", Desc: ui.Tr("prev/next day", "пред./след. день")},
		{Key: "m", Desc: ui.Tr("map", "карта")},
		{Key: "s", Desc: ui.Tr("group timetable", "расписание группы")},
		{Key: "y", Desc: ui.Tr("copy", "копировать")},
		{Key: "r", Desc: ui.Tr("refresh", "обновить")},
	}
}

func (p *tabPage) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case ui.Result[weekResult]:
		if msg.ID != p.id {
			return nil
		}
		return p.wk.handleResult(msg)
	case tickMsg:
		if msg.id != p.id {
			return nil
		}
		return tea.Batch(tick(p.ctx, p.id), p.wk.refreshIfStale())
	case ui.ReloadMsg:
		return p.wk.reload()
	case tea.KeyMsg:
		switch msg.String() {
		case "r":
			return p.wk.refresh()
		case "enter":
			l, ok := p.wk.selected()
			if !ok {
				return nil
			}
			return ui.ShowMenu(menuTitle(l), p.wk.lessonActions(l)...)
		}
		cmd, _ := p.wk.update(msg)
		p.wk.moved = false
		return cmd
	}
	return nil
}

func (p *tabPage) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	lw := leftWidth(width)
	left := p.wk.viewList(lw, height)
	right := p.wk.viewDetail(ui.RightWidth(width, lw), height)
	return ui.Split(width, height, lw, left, right)
}
