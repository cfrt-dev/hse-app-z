package api

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const dateLayout = "2006-01-02"

// Lessons returns the timetable for q, sorted by start time. The server
// treats start and end as inclusive dates; lessons outside the range that
// the server may still return are filtered out.
func (c *Client) Lessons(ctx context.Context, q LessonQuery) ([]Lesson, Meta, error) {
	v := url.Values{}
	switch {
	case q.Email != "":
		v.Set("email", q.Email)
	case q.Group != "":
		v.Set("group", q.Group)
	case q.Auditorium != "":
		v.Set("auditorium", q.Auditorium)
	}
	if !q.Start.IsZero() {
		v.Set("start", q.Start.Format(dateLayout))
	}
	if !q.End.IsZero() {
		v.Set("end", q.End.Format(dateLayout))
	}
	var out []Lesson
	meta, err := c.get(ctx, "/v3/ruz/lessons", v, &out)
	if err != nil {
		return nil, meta, err
	}
	out = filterLessons(out, q.Start, q.End)
	return out, meta, nil
}

func filterLessons(ls []Lesson, start, end time.Time) []Lesson {
	res := ls[:0]
	for _, l := range ls {
		if l.DateStart.IsZero() {
			continue
		}
		day := l.Start()
		d := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
		if !start.IsZero() && d.Before(time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)) {
			continue
		}
		if !end.IsZero() && d.After(time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, time.UTC)) {
			continue
		}
		res = append(res, l)
	}
	sort.SliceStable(res, func(i, j int) bool {
		if !res[i].DateStart.Equal(res[j].DateStart.Time) {
			return res[i].DateStart.Before(res[j].DateStart.Time)
		}
		return res[i].Discipline < res[j].Discipline
	})
	return res
}

// Search finds people, groups and rooms.
func (c *Client) Search(ctx context.Context, query string) ([]Person, Meta, error) {
	var out []Person
	meta, err := c.get(ctx, "/v3/dump/search", url.Values{"q": {query}}, &out)
	return out, meta, err
}

func emailPath(email string, suffix string) string {
	return "/v3/dump/email/" + url.PathEscape(strings.TrimSpace(email)) + suffix
}

// Person returns the full profile for an email.
func (c *Client) Person(ctx context.Context, email string) (Person, Meta, error) {
	var out Person
	meta, err := c.get(ctx, emailPath(email, ""), nil, &out)
	return out, meta, err
}

// PersonLink returns the public profile page URL ("" when there is none).
func (c *Client) PersonLink(ctx context.Context, email string) (string, Meta, error) {
	var out PersonLink
	meta, err := c.get(ctx, emailPath(email, "/link"), nil, &out)
	return strings.TrimSpace(out.URL), meta, err
}

// Subordinates lists the staff reporting to email.
func (c *Client) Subordinates(ctx context.Context, email string) ([]Person, Meta, error) {
	var out []Person
	meta, err := c.get(ctx, emailPath(email, "/subordinates"), nil, &out)
	return out, meta, err
}

// Favourites returns the people the user starred in the official app.
func (c *Client) Favourites(ctx context.Context) ([]Person, Meta, error) {
	var out []Person
	meta, err := c.get(ctx, "/v3/dump/favourites/me", nil, &out)
	return out, meta, err
}

// Grades returns grades for an academic year ("2025/2026") and program id;
// empty values select the server defaults (current year, main program).
func (c *Client) Grades(ctx context.Context, academicYear, programID string) (GradesResponse, Meta, error) {
	v := url.Values{}
	if academicYear != "" {
		v.Set("academic_year", academicYear)
	}
	if programID != "" {
		v.Set("program_id", programID)
	}
	var out GradesResponse
	meta, err := c.get(ctx, "/education/grades", v, &out)
	return out, meta, err
}

// Ratings returns published rating snapshots. Without a Type the server
// returns every type for the selected year.
func (c *Client) Ratings(ctx context.Context, q RatingQuery) (RatingsResponse, Meta, error) {
	v := url.Values{}
	if q.AcademicYear != "" {
		v.Set("academic_year", q.AcademicYear)
	}
	if q.ProgramID != "" {
		v.Set("program_id", q.ProgramID)
	}
	if q.Type != "" {
		v.Set("type", q.Type)
	}
	if q.ModuleNumber > 0 {
		v.Set("module_number", strconv.Itoa(q.ModuleNumber))
	}
	var out RatingsResponse
	meta, err := c.get(ctx, "/education/ratings", v, &out)
	return out, meta, err
}

// Buildings returns campus buildings with their cafes and libraries.
func (c *Client) Buildings(ctx context.Context) ([]CampusGroup, Meta, error) {
	var out []CampusGroup
	meta, err := c.do(ctx, http.MethodPost, "/v3/dump/buildings/groups", nil, map[string]bool{"is_free": true}, &out)
	return out, meta, err
}

// Cafes returns all cafes grouped by building.
func (c *Client) Cafes(ctx context.Context) ([]CafeGroup, Meta, error) {
	var out []CafeGroup
	meta, err := c.get(ctx, "/food/cafes", nil, &out)
	return out, meta, err
}

// Cafe returns one cafe.
func (c *Client) Cafe(ctx context.Context, id string) (Cafe, Meta, error) {
	var out Cafe
	meta, err := c.get(ctx, "/food/cafes/"+url.PathEscape(id), nil, &out)
	return out, meta, err
}

// CafeMenu returns a cafe's menu for a weekday ("monday"…); empty = today.
func (c *Client) CafeMenu(ctx context.Context, id, day string) (Menu, Meta, error) {
	v := url.Values{}
	if day != "" {
		v.Set("day", strings.ToLower(day))
	}
	var out Menu
	meta, err := c.get(ctx, "/food/cafes/"+url.PathEscape(id)+"/menu", v, &out)
	return out, meta, err
}

// Services lists SmartPoint services; category "" is the root.
func (c *Client) Services(ctx context.Context, category string) ([]Service, Meta, error) {
	v := url.Values{}
	if category != "" {
		v.Set("category", category)
	}
	var out []Service
	meta, err := c.get(ctx, "/tasks/services", v, &out)
	return out, meta, err
}

// Tasks lists the user's SmartPoint requests; cursor "" is the first page.
func (c *Client) Tasks(ctx context.Context, cursor string) (TasksPage, Meta, error) {
	v := url.Values{}
	if cursor != "" && cursor != "0" {
		v.Set("cursor", cursor)
	}
	var out TasksPage
	meta, err := c.get(ctx, "/tasks", v, &out)
	return out, meta, err
}

// Stories returns the news stories shown on the home screen.
func (c *Client) Stories(ctx context.Context) ([]Story, Meta, error) {
	var out []Story
	meta, err := c.get(ctx, "/v2/stories", nil, &out)
	return out, meta, err
}

// Banners returns announcement banners for a section ("", "ratings",
// "grades", "search").
func (c *Client) Banners(ctx context.Context, section string) ([]Banner, Meta, error) {
	v := url.Values{}
	if section != "" {
		v.Set("section", section)
	}
	var out []Banner
	meta, err := c.get(ctx, "/banners", v, &out)
	return out, meta, err
}
