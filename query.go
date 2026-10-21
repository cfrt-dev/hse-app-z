package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"hse-app-z/internal/api"
	"hse-app-z/internal/auth"
	"hse-app-z/internal/config"
	"hse-app-z/internal/ui"
)

// Query commands print data without starting the UI: plain text for people,
// --json (the API's own objects) for scripts and agents.

const queryUsage = `Data commands (no UI; add --json for machine-readable output):
  hse-app-z schedule [flags]   lessons (default: yours, today)
      --from DATE              first day: YYYY-MM-DD, today, tomorrow,
                               yesterday (default today)
      --days N                 number of days (default 1)
      --week                   the whole Mon–Sun week containing --from
      --group ID | --room ID | --email EMAIL
                               someone else's timetable (IDs from "search")
  hse-app-z grades [--year 2025/2026] [--program ID]
  hse-app-z rating [--type current|cumul|retake|minor] [--year Y]
                   [--module N] [--program ID]
  hse-app-z search QUERY       people, groups and rooms
  hse-app-z person [EMAIL]     a profile (default: yours)
  hse-app-z cafes              cafés with their ids and opening hours
  hse-app-z menu CAFE_ID [--day monday]
  hse-app-z news               news stories

  These also take --lang en|ru and --demo/--fixtures. Warnings (e.g. "offline,
  showing cached data") go to stderr; data goes to stdout.
`

type query struct {
	args  int // allowed positional arguments: 0, 1, or -1 for any number
	need  int // required positional arguments
	flags func(fs *flag.FlagSet) func(q *queryRun) error
	run   func(q *queryRun) error
}

// queryRun is one invocation of a query command.
type queryRun struct {
	ctx    context.Context
	api    *api.Client
	me     ui.Me
	now    time.Time
	args   []string
	json   bool
	out    io.Writer
	warn   io.Writer
	values map[string]string
}

var queryStderr io.Writer = os.Stderr

var queries = map[string]query{
	"schedule": {flags: scheduleFlags, run: runSchedule},
	"grades":   {flags: valueFlags("year", "program"), run: runGrades},
	"rating":   {flags: valueFlags("year", "program", "type", "module"), run: runRating},
	"search":   {args: -1, need: 1, run: runSearch},
	"person":   {args: 1, run: runPerson},
	"cafes":    {run: runCafes},
	"menu":     {args: 1, need: 1, flags: valueFlags("day"), run: runMenu},
	"news":     {run: runNews},
}

// valueFlags declares string flags whose values land in q.values.
func valueFlags(names ...string) func(fs *flag.FlagSet) func(q *queryRun) error {
	return func(fs *flag.FlagSet) func(q *queryRun) error {
		ptrs := map[string]*string{}
		for _, n := range names {
			ptrs[n] = fs.String(n, "", "")
		}
		return func(q *queryRun) error {
			for n, p := range ptrs {
				q.values[n] = strings.TrimSpace(*p)
			}
			return nil
		}
	}
}

// runQuery parses the command's own flags (the global ones already parsed
// before the command name are passed in) and runs it.
func runQuery(name string, args []string, stdout io.Writer, demo bool, fixtures, lang string) error {
	qc := queries[name]
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOut := fs.Bool("json", false, "")
	fs.BoolVar(&demo, "demo", demo, "")
	fs.StringVar(&fixtures, "fixtures", fixtures, "")
	fs.StringVar(&lang, "lang", lang, "")
	var apply func(q *queryRun) error
	if qc.flags != nil {
		apply = qc.flags(fs)
	}
	// Positionals and flags may be interleaved ("search Ivan --json").
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				fmt.Fprint(stdout, usage)
				return nil
			}
			return usagef("%s: %v", name, err)
		}
		args = fs.Args()
		if len(args) == 0 {
			break
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
	if len(pos) < qc.need {
		return usagef("%s needs an argument", name)
	}
	if qc.args >= 0 && len(pos) > qc.args {
		return usagef("unexpected argument %q", pos[qc.args])
	}
	lang = strings.ToLower(strings.TrimSpace(lang))
	if lang != "" && lang != "en" && lang != "ru" {
		return usagef("--lang must be en or ru")
	}
	if fixtures != "" && !demo {
		return usagef("--fixtures needs --demo")
	}

	q := &queryRun{args: pos, json: *jsonOut, out: stdout, warn: queryStderr, values: map[string]string{}, now: time.Now()}
	if apply != nil {
		if err := apply(q); err != nil {
			return err
		}
	}

	paths, err := config.DefaultPaths()
	if err != nil {
		return fmt.Errorf("can't find config dir: %w", err)
	}
	if lang == "" {
		lang = config.LoadSettings(paths.SettingsFile()).GetLang()
	}
	if demo {
		dir, err := findFixtures(fixtures)
		if err != nil {
			return err
		}
		q.api = api.NewClient("http://demo.invalid", nil, api.NewCache(""))
		q.api.HTTP = &http.Client{Transport: &api.FixtureTransport{Dir: dir}}
		q.me = ui.Me{Email: api.DemoEmail, Name: "Demo Student"}
		q.now = demoNow
	} else {
		src, err := loadSource(&auth.Store{Path: paths.TokensFile()}, newAuthClient())
		if err != nil {
			return err
		}
		if !src.SignedIn() {
			return friendlyAuthErr(api.ErrLoginRequired)
		}
		c := src.Claims()
		q.me = ui.Me{Email: c.UserEmail(), Name: c.DisplayName()}
		q.api = api.NewClient("", src, api.NewCache(paths.HTTPCacheDir()))
		q.api.SetCacheScope(c.Sub)
	}
	q.api.SetLang(lang)
	ui.SetLang(lang)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	q.ctx = ctx
	if err := qc.run(q); err != nil {
		if auth.IsLoginRequired(err) {
			return friendlyAuthErr(err)
		}
		var ue usageError
		if errors.As(err, &ue) {
			return err
		}
		return errors.New(api.Friendly(err))
	}
	return nil
}

// note reports where the data came from when it isn't fresh.
func (q *queryRun) note(m api.Meta) {
	if m.Stale {
		msg := "showing cached data"
		if !m.Fetched.IsZero() {
			msg += " from " + m.Fetched.Local().Format(time.DateTime)
		}
		if m.StaleReason != "" {
			msg += " (" + m.StaleReason + ")"
		}
		fmt.Fprintln(q.warn, "hse-app-z: warning:", msg)
	}
}

// emit prints v as JSON with --json, otherwise calls text.
func (q *queryRun) emit(v any, text func(w io.Writer)) error {
	if q.json {
		enc := json.NewEncoder(q.out)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(v)
	}
	text(q.out)
	return nil
}

// ----------------------------------------------------------------- schedule

func scheduleFlags(fs *flag.FlagSet) func(q *queryRun) error {
	from := fs.String("from", "today", "")
	days := fs.Int("days", 1, "")
	week := fs.Bool("week", false, "")
	group := fs.String("group", "", "")
	room := fs.String("room", "", "")
	email := fs.String("email", "", "")
	return func(q *queryRun) error {
		n := 0
		for _, s := range []string{*group, *room, *email} {
			if strings.TrimSpace(s) != "" {
				n++
			}
		}
		if n > 1 {
			return usagef("use only one of --group, --room and --email")
		}
		if *days < 1 || *days > 62 {
			return usagef("--days must be 1-62")
		}
		if *week && *days != 1 {
			return usagef("use either --week or --days")
		}
		q.values["from"] = strings.ToLower(strings.TrimSpace(*from))
		q.values["days"] = strconv.Itoa(*days)
		if *week {
			q.values["week"] = "1"
		}
		q.values["group"] = strings.TrimSpace(*group)
		q.values["room"] = strings.TrimSpace(*room)
		q.values["email"] = strings.TrimSpace(*email)
		return nil
	}
}

func parseDay(s string, now time.Time) (time.Time, error) {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	switch s {
	case "", "today":
		return today, nil
	case "tomorrow":
		return today.AddDate(0, 0, 1), nil
	case "yesterday":
		return today.AddDate(0, 0, -1), nil
	}
	t, err := time.ParseInLocation("2006-01-02", s, now.Location())
	if err != nil {
		return time.Time{}, usagef("--from must be YYYY-MM-DD, today, tomorrow or yesterday")
	}
	return t, nil
}

func runSchedule(q *queryRun) error {
	start, err := parseDay(q.values["from"], q.now)
	if err != nil {
		return err
	}
	days, _ := strconv.Atoi(q.values["days"])
	if q.values["week"] != "" {
		start = start.AddDate(0, 0, -((int(start.Weekday()) + 6) % 7))
		days = 7
	}
	end := start.AddDate(0, 0, days-1)
	lq := api.LessonQuery{Group: q.values["group"], Auditorium: q.values["room"], Email: q.values["email"], Start: start, End: end}
	if lq.Group == "" && lq.Auditorium == "" && lq.Email == "" {
		lq.Email = q.me.Email
	}
	lessons, meta, err := q.api.Lessons(q.ctx, lq)
	if err != nil {
		return err
	}
	q.note(meta)
	if lessons == nil {
		lessons = []api.Lesson{}
	}
	return q.emit(lessons, func(w io.Writer) {
		span := start.Format("Mon 2 Jan 2006")
		if days > 1 {
			span += " – " + end.Format("Mon 2 Jan 2006")
		}
		if len(lessons) == 0 {
			fmt.Fprintf(w, "No lessons (%s).\n", span)
			return
		}
		day := ""
		for _, l := range lessons {
			if d := l.Start().Format("Mon 2 Jan 2006"); d != day {
				if day != "" {
					fmt.Fprintln(w)
				}
				day = d
				fmt.Fprintln(w, d)
			}
			fmt.Fprintf(w, "  %s–%s  %s", l.Start().Format("15:04"), l.End().Format("15:04"), ui.Clean(l.Discipline))
			if k := ui.Clean(l.Kind()); k != "" {
				fmt.Fprintf(w, " (%s)", k)
			}
			fmt.Fprintln(w)
			where := ui.JoinNonEmpty(", ", ui.Clean(l.Auditorium), ui.Clean(l.Building))
			if where != "" {
				fmt.Fprintf(w, "      where: %s\n", where)
			}
			var lecturers []string
			for _, p := range l.LecturerProfiles {
				if n := ui.Clean(p.DisplayName()); n != "" {
					lecturers = append(lecturers, n)
				}
			}
			if len(lecturers) == 0 {
				lecturers = l.LecturerEmails
			}
			if len(lecturers) > 0 {
				fmt.Fprintf(w, "      lecturer: %s\n", strings.Join(lecturers, ", "))
			}
			for _, s := range l.Links() {
				fmt.Fprintf(w, "      link: %s\n", ui.Clean(s.Link))
			}
			if n := ui.Clean(l.Note); n != "" {
				fmt.Fprintf(w, "      note: %s\n", n)
			}
		}
	})
}

// ------------------------------------------------------------------- grades

func runGrades(q *queryRun) error {
	g, meta, err := q.api.Grades(q.ctx, q.values["year"], q.values["program"])
	if err != nil {
		return err
	}
	q.note(meta)
	return q.emit(g, func(w io.Writer) {
		fmt.Fprintf(w, "Academic year %s", g.SelectedAcademicYear)
		for _, p := range g.AvailablePrograms {
			if p.ID == g.SelectedProgram {
				fmt.Fprintf(w, " · %s", ui.Clean(p.DisplayName()))
			}
		}
		fmt.Fprintln(w)
		if len(g.AvailableAcademicYears) > 1 {
			fmt.Fprintf(w, "Other years (--year): %s\n", strings.Join(g.AvailableAcademicYears, ", "))
		}
		if len(g.AvailablePrograms) > 1 {
			for _, p := range g.AvailablePrograms {
				fmt.Fprintf(w, "Program --program %s: %s\n", p.ID, ui.Clean(p.DisplayName()))
			}
		}
		fmt.Fprintln(w)
		if len(g.Items) == 0 {
			fmt.Fprintln(w, "No grades.")
			return
		}
		for _, it := range g.Items {
			mark := it.GradeText()
			if mark == "" {
				mark = "—"
			}
			fmt.Fprintf(w, "%-5s %s", mark, ui.Clean(it.Discipline))
			extra := ui.JoinNonEmpty(", ", ui.Clean(it.TypeRaw), ui.Clean(it.ModuleName))
			if it.Credits.OK {
				extra = ui.JoinNonEmpty(", ", extra, it.Credits.String()+" credits")
			}
			if !it.Date.IsZero() {
				extra = ui.JoinNonEmpty(", ", extra, it.Date.Format("2006-01-02"))
			}
			if extra != "" {
				fmt.Fprintf(w, " (%s)", extra)
			}
			fmt.Fprintln(w)
		}
	})
}

// ------------------------------------------------------------------- rating

func runRating(q *queryRun) error {
	rq := api.RatingQuery{AcademicYear: q.values["year"], ProgramID: q.values["program"], Type: q.values["type"]}
	if m := q.values["module"]; m != "" {
		n, err := strconv.Atoi(m)
		if err != nil || n < 1 {
			return usagef("--module must be a positive number")
		}
		rq.ModuleNumber = n
	}
	r, meta, err := q.api.Ratings(q.ctx, rq)
	if err != nil {
		return err
	}
	q.note(meta)
	return q.emit(r, func(w io.Writer) {
		if len(r.Items) == 0 {
			fmt.Fprintln(w, "No ratings published.")
			return
		}
		for i, it := range r.Items {
			if i > 0 {
				fmt.Fprintln(w)
			}
			fmt.Fprintln(w, ui.JoinNonEmpty(" · ", ui.Clean(it.Title), it.Type, ui.Clean(it.LearnPeriod)))
			if it.PlaceCurr.OK {
				fmt.Fprintf(w, "  place      %s of %s\n", it.PlaceCurr, it.PlaceTotal)
			}
			if it.Percentil.OK {
				fmt.Fprintf(w, "  percentile %s\n", it.Percentil)
			}
			if it.GPA.OK {
				fmt.Fprintf(w, "  GPA        %s\n", it.GPA)
			}
			if it.GradeMid.OK {
				fmt.Fprintf(w, "  mean grade %s (min %s)\n", it.GradeMid, it.GradeMin)
			}
			if it.Rating.OK {
				fmt.Fprintf(w, "  rating     %s\n", it.Rating)
			}
			if it.Credits.OK {
				fmt.Fprintf(w, "  credits    %s\n", it.Credits)
			}
			for _, d := range it.DisciplineList {
				fmt.Fprintf(w, "    %-4s %s\n", d.Grade, ui.Clean(d.Discipline))
			}
		}
	})
}

// ------------------------------------------------------------------- people

func runSearch(q *queryRun) error {
	text := strings.TrimSpace(strings.Join(q.args, " "))
	if text == "" {
		return usagef("search needs a query")
	}
	hits, meta, err := q.api.Search(q.ctx, text)
	if err != nil {
		return err
	}
	q.note(meta)
	if hits == nil {
		hits = []api.Person{}
	}
	return q.emit(hits, func(w io.Writer) {
		if len(hits) == 0 {
			fmt.Fprintln(w, "Nothing found.")
			return
		}
		for _, p := range hits {
			id := p.Email
			switch {
			case p.IsGroup():
				id = "--group " + string(p.ID)
			case p.IsAuditorium():
				id = "--room " + string(p.ID)
			}
			fmt.Fprintf(w, "%-10s %s", strings.ToLower(p.Type), ui.Clean(p.DisplayName()))
			if id != "" {
				fmt.Fprintf(w, "  [%s]", id)
			}
			if d := ui.Clean(p.Description); d != "" {
				fmt.Fprintf(w, " — %s", d)
			}
			fmt.Fprintln(w)
		}
	})
}

func runPerson(q *queryRun) error {
	email := q.me.Email
	if len(q.args) > 0 {
		email = q.args[0]
	}
	if email == "" {
		return errors.New("the session doesn't name your email; pass one: hse-app-z person EMAIL")
	}
	p, meta, err := q.api.Person(q.ctx, email)
	if err != nil {
		return err
	}
	q.note(meta)
	return q.emit(p, func(w io.Writer) {
		row := func(k, v string) {
			if v = ui.Clean(v); v != "" {
				fmt.Fprintf(w, "%-10s %s\n", k, v)
			}
		}
		row("name", p.DisplayName())
		row("type", strings.ToLower(p.Type))
		row("email", p.Email)
		row("about", p.Description)
		row("campus", api.CampusName(p.Campus))
		row("birthday", p.BirthDay())
		for _, e := range p.Education {
			row("education", ui.JoinNonEmpty(", ", e.ProgramTitle, e.FacultyTitle, e.DegreeLevel, e.GroupTitle))
		}
		for _, s := range p.StaffPositions {
			row("position", ui.JoinNonEmpty(", ", s.PositionName, s.UnitName))
		}
		for _, a := range p.StaffAddress {
			row("office", ui.JoinNonEmpty(", ", a.Label, string(a.RoomCode), string(a.PhoneInternalFull)))
			row("hours", ui.JoinNonEmpty(": ", a.PresenceType, a.PresenceTime))
		}
	})
}

// --------------------------------------------------------------------- food

func runCafes(q *queryRun) error {
	groups, meta, err := q.api.Cafes(q.ctx)
	if err != nil {
		return err
	}
	q.note(meta)
	if groups == nil {
		groups = []api.CafeGroup{}
	}
	return q.emit(groups, func(w io.Writer) {
		today := strings.ToLower(q.now.Weekday().String())
		for i, g := range groups {
			if i > 0 {
				fmt.Fprintln(w)
			}
			fmt.Fprintln(w, ui.Clean(g.CampusName))
			for _, c := range g.Cafes {
				hours := "closed today"
				for _, h := range c.OpeningHours {
					if h.DayOfWeek == today && h.IsOpen {
						hours = "today " + ui.JoinNonEmpty("–", h.StartTime, h.EndTime)
					}
				}
				menu := ""
				if c.HasMenu {
					menu = ", has menu"
				}
				fmt.Fprintf(w, "  [%s] %s (%s%s)\n", c.ID, ui.Clean(c.Name), hours, menu)
			}
		}
	})
}

func runMenu(q *queryRun) error {
	m, meta, err := q.api.CafeMenu(q.ctx, q.args[0], q.values["day"])
	if err != nil {
		return err
	}
	q.note(meta)
	return q.emit(m, func(w io.Writer) {
		fmt.Fprintf(w, "Menu for %s", ui.Clean(m.CurrentDay))
		if len(m.AvailableDays) > 0 {
			fmt.Fprintf(w, " (other days with --day: %s)", strings.Join(m.AvailableDays, ", "))
		}
		fmt.Fprintln(w)
		for _, s := range m.Sections {
			fmt.Fprintln(w)
			fmt.Fprint(w, ui.Clean(s.SectionName))
			if s.Price.OK {
				fmt.Fprintf(w, " — %s ₽", s.Price)
			}
			fmt.Fprintln(w)
			for _, it := range s.Items {
				if it.Price.OK {
					fmt.Fprintf(w, "  %-8s %s", it.Price.String()+" ₽", ui.Clean(it.ItemName))
				} else {
					fmt.Fprintf(w, "  %s", ui.Clean(it.ItemName))
				}
				if extra := ui.JoinNonEmpty(", ", ui.Clean(it.Weight), ui.Clean(it.Calories)); extra != "" {
					fmt.Fprintf(w, " (%s)", extra)
				}
				fmt.Fprintln(w)
			}
		}
	})
}

// --------------------------------------------------------------------- news

func runNews(q *queryRun) error {
	stories, meta, err := q.api.Stories(q.ctx)
	if err != nil {
		return err
	}
	q.note(meta)
	if stories == nil {
		stories = []api.Story{}
	}
	return q.emit(stories, func(w io.Writer) {
		for i, s := range stories {
			if i > 0 {
				fmt.Fprintln(w)
			}
			head := ui.Clean(s.Title)
			if t := s.Published(); !t.IsZero() {
				head = ui.JoinNonEmpty(" · ", t.Local().Format("2006-01-02"), head)
			}
			fmt.Fprintln(w, ui.JoinNonEmpty(" · ", head, ui.Clean(s.Publisher.Name)))
			seen := map[string]bool{}
			for _, p := range s.Pages {
				if t := ui.Clean(p.Title); t != "" && !seen[t] {
					seen[t] = true
					fmt.Fprintf(w, "  %s\n", t)
				}
				if t := ui.CleanMulti(p.Text); t != "" && !seen[t] {
					seen[t] = true
					fmt.Fprintln(w, ui.Indent(t, "    "))
				}
				if p.Action != nil {
					if l := ui.Clean(p.Action.Link); l != "" && !seen[l] {
						seen[l] = true
						fmt.Fprintf(w, "    %s\n", l)
					}
				}
			}
		}
	})
}
