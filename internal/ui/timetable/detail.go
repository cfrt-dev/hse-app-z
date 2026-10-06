package timetable

import (
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"hse-app-z/internal/api"
	"hse-app-z/internal/ui"
)

// lessonStatus is the relative state of a lesson at now:
// "now · ends in 25 min", "starts in 2 h 10 min", "tomorrow", "ended"
// ("идёт · до конца 25 мин", "через 2 ч 10 мин", "завтра", "закончилась").
func lessonStatus(l api.Lesson, now time.Time) (string, lipgloss.Style) {
	start := l.DateStart.Time
	end, hasEnd := endOf(l)
	switch {
	case now.Before(start):
		d := start.Sub(now)
		days := int(lessonDay(l).Sub(viewerDay(now)).Hours() / 24)
		switch {
		case d < time.Minute:
			return ui.Tr("starts now", "начинается"), ui.StyleAccent
		case days <= 0 || d < 6*time.Hour: // <0: the viewer's date is ahead of the lesson's
			return ui.Tr("starts in ", "через ") + fmtDur(d), ui.StyleAccent
		case days == 1:
			return ui.Tr("tomorrow", "завтра"), ui.StyleDim
		}
		return ui.Trf("in %d days", "через %d %s", days, ui.PluralRU(days, "день", "дня", "дней")), ui.StyleDim
	case hasEnd && now.Before(end):
		return ui.Tr("now · ends in ", "идёт · до конца ") + fmtDur(end.Sub(now)), ui.StyleOK.Bold(true)
	case !hasEnd:
		if lessonDay(l).Equal(viewerDay(now)) {
			return ui.Trf("started %s ago", "началась %s назад", fmtDur(now.Sub(start))), ui.StyleDim
		}
		return ui.Tr("past", "прошла"), ui.StyleDim
	}
	return ui.Tr("ended", "закончилась"), ui.StyleDim
}

// timeLine is "10:00–11:20 (80 min) · pair 3", with the zone when it
// differs from the viewer's and a marker for lessons ending the next day.
func timeLine(l api.Lesson, now time.Time) string {
	s := timeRange(l)
	if e, ok := endOf(l); ok {
		if !civil(e.In(l.Zone())).Equal(lessonDay(l)) {
			s += ui.Tr(" (+1 day)", " (+1 день)")
		}
		if d := e.Sub(l.DateStart.Time); d > 0 {
			s += " (" + lengthText(d) + ")"
		}
	}
	start := l.Start()
	_, off := start.Zone()
	local := l.DateStart.In(now.Location())
	if _, lo := local.Zone(); lo != off {
		s += " " + zoneLabel(start)
		lt := local.Format("15:04")
		if !civil(local).Equal(lessonDay(l)) {
			// Another day for the viewer: say which, or they'd join a
			// day early (Monday 18:10 in Moscow is Tuesday 00:10 in Tokyo).
			lt = ui.WeekdayShort(local.Weekday()) + " " + local.Format("15:04")
		}
		if e, ok := endOf(l); ok {
			lt += "–" + e.In(now.Location()).Format("15:04")
		}
		s += " = " + lt + ui.Tr(" your time", " по вашему времени")
	}
	return ui.JoinNonEmpty(" · ", s, pairText(l))
}

// lessonDetail renders everything known about a lesson for the right pane.
// week is the lessons of the shown week (to flag overlaps).
func lessonDetail(l api.Lesson, week []api.Lesson, now time.Time, width int) string {
	var b []string
	b = append(b, styledWrap(discipline(l), width, ui.StyleTitle.Render))

	status, st := lessonStatus(l, now)
	switch kind := ui.Clean(l.Kind()); {
	case kind == "":
		b = append(b, styledWrap(status, width, st.Render))
	case ui.Width(kind)+3+ui.Width(status) <= width:
		b = append(b, ui.KindStyle(l.Kind()).Render(kind)+ui.StyleDim.Render(" · ")+st.Render(status))
	default:
		b = append(b, styledWrap(kind, width, ui.KindStyle(l.Kind()).Render), styledWrap(status, width, st.Render))
	}
	if l.IsBan {
		b = append(b, styledWrap(ui.Tr("⚠ marked as banned/cancelled in RUZ", "⚠ отмечена в РУЗ как отменённая"), width, ui.StyleWarn.Render))
	}
	b = append(b, "")

	date := ui.FmtDayLong(l.Start())
	if l.Start().Year() != now.Year() {
		date += " " + strconv.Itoa(l.Start().Year())
	}
	b = append(b, ui.KV(ui.Tr("Date", "Дата"), date, width))
	b = append(b, ui.KV(ui.Tr("Time", "Время"), timeLine(l, now), width))
	if l.IsOnline() {
		b = append(b, ui.KV(ui.Tr("Room", "Аудитория"), ui.Tr("Online", "Онлайн"), width))
	} else {
		b = append(b, ui.KV(ui.Tr("Room", "Аудитория"), ui.Clean(l.Auditorium), width))
		b = append(b, ui.KV(ui.Tr("Building", "Корпус"), ui.Clean(l.Building), width))
	}
	if ov := overlaps(l, week); ov != "" {
		b = append(b, ui.KV(ui.Tr("Overlaps", "Накладка"), ui.StyleWarn.Render(ov), width))
	}
	b = append(b, ui.KV(ui.Tr("Note", "Примечание"), noteText(l), width))

	// Links (stream links + URLs from the note).
	var links []string
	for _, s := range l.Links() {
		links = append(links, ui.Wrap(ui.StyleLink.Render(ui.Clean(s.Link)), width))
		if d := ui.Clean(s.Description); d != "" && d != strings.TrimSpace(s.Link) {
			links = append(links, styledWrap(d, width-2, func(s ...string) string { return "  " + ui.StyleDim.Render(s...) }))
		}
	}
	b = append(b, ui.Section(ui.Tr("Online", "Ссылки"), strings.Join(links, "\n")))

	// Lecturers.
	var lect []string
	for _, p := range l.LecturerProfiles {
		name := ui.Clean(p.DisplayName())
		if name == "" {
			continue
		}
		lect = append(lect, ui.Wrap(name, width))
		if meta := ui.JoinNonEmpty(" · ", ui.Clean(p.Description), ui.Clean(p.Email)); meta != "" {
			lect = append(lect, styledWrap(meta, width-2, func(s ...string) string { return "  " + ui.StyleDim.Render(s...) }))
		}
	}
	if len(lect) == 0 {
		for _, e := range l.LecturerEmails {
			if e = ui.Clean(e); e != "" {
				lect = append(lect, ui.Wrap(e, width))
			}
		}
	}
	title := ui.Tr("Lecturer", "Преподаватель")
	if len(l.LecturerProfiles) > 1 || (len(l.LecturerProfiles) == 0 && len(l.LecturerEmails) > 1) {
		title = ui.Tr("Lecturers", "Преподаватели")
	}
	b = append(b, ui.Section(title, strings.Join(lect, "\n")))

	// Course and bookkeeping.
	var more []string
	more = append(more, ui.KV(ui.Tr("Stream", "Поток"), ui.Clean(l.StreamName()), width))
	more = append(more, ui.KV(ui.Tr("Group", "Группа"), ui.Clean(string(l.GroupID)), width))
	if u := ui.Clean(l.DisciplineLink); u != "" {
		more = append(more, ui.KV(ui.Tr("Course page", "Курс"), ui.StyleLink.Render(u), width))
	}
	if !l.UpdatedAt.IsZero() {
		upd := ui.FmtDateTime(l.UpdatedAt.In(l.Zone()))
		d := now.Sub(l.UpdatedAt.Time)
		if d < 0 {
			d = -d
		}
		if d <= 7*24*time.Hour && !l.UpdatedAt.Equal(l.CreatedAt.Time) {
			upd += ui.StyleWarn.Render(ui.Tr(" · changed recently", " · недавно изменена"))
		}
		more = append(more, ui.KV(ui.Tr("Updated", "Обновлено"), upd, width))
	}
	b = append(b, ui.Section(ui.Tr("Details", "Подробности"), ui.Lines(more...)))

	return strings.TrimRight(ui.Lines(b...), "\n")
}

// overlaps lists other lessons of the week that intersect l in time.
func overlaps(l api.Lesson, week []api.Lesson) string {
	ls, le := l.DateStart.Time, l.DateStart.Time
	if e, ok := endOf(l); ok {
		le = e
	}
	key := lessonKey(l)
	var out []string
	for _, o := range week {
		if lessonKey(o) == key {
			continue
		}
		os, oe := o.DateStart.Time, o.DateStart.Time
		if e, ok := endOf(o); ok {
			oe = e
		}
		hit := intersects(ls, le, os, oe)
		if hit {
			out = append(out, discipline(o)+" "+timeRange(o))
		}
	}
	return strings.Join(out, "; ")
}

// intersects reports whether [as,ae) and [bs,be) overlap; a lesson without
// an end is a single instant.
func intersects(as, ae, bs, be time.Time) bool {
	switch {
	case as.Equal(ae) && bs.Equal(be):
		return as.Equal(bs)
	case as.Equal(ae):
		return !as.Before(bs) && as.Before(be)
	case bs.Equal(be):
		return !bs.Before(as) && bs.Before(ae)
	}
	return bs.Before(ae) && as.Before(be)
}

// lengthText is a lesson's length: "80 min" (hours only for long events).
func lengthText(d time.Duration) string {
	if d < 4*time.Hour {
		return ui.Trf("%d min", "%d мин", int((d+time.Minute-1)/time.Minute))
	}
	return fmtDur(d)
}
