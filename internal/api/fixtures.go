package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// FixtureTransport serves recorded API responses from a directory (see
// internal/api/testdata). It powers --demo mode and UI tests, emulating the
// server-side filtering that matters (lesson date ranges, search queries,
// rating types).
type FixtureTransport struct {
	Dir string
	// MeEmail is the signed-in demo user.
	MeEmail string
	// Latency simulates network delay.
	Latency time.Duration
}

// DemoEmail is the account the fixtures were recorded with.
const DemoEmail = "makvalnikitin@edu.hse.ru"

func (f *FixtureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if f.Latency > 0 {
		select {
		case <-time.After(f.Latency):
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	}
	if req.Body != nil {
		_, _ = io.Copy(io.Discard, req.Body)
		req.Body.Close()
	}
	body, status := f.route(req)
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     http.Header{"Content-Type": {"application/json; charset=utf-8"}},
		Body:       io.NopCloser(bytes.NewReader(body)),
		Request:    req,
	}, nil
}

func (f *FixtureTransport) load(name string) []byte {
	b, err := os.ReadFile(filepath.Join(f.Dir, name))
	if err != nil {
		return []byte(`{"error":{"name":"FixtureMissing","message":"fixture ` + name + ` not found"}}`)
	}
	return b
}

func (f *FixtureTransport) me() string {
	if f.MeEmail != "" {
		return f.MeEmail
	}
	return DemoEmail
}

func (f *FixtureTransport) route(req *http.Request) ([]byte, int) {
	p := req.URL.Path
	q := req.URL.Query()
	switch {
	case p == "/v3/ruz/lessons":
		return f.lessons(q), 200

	case p == "/v3/dump/search":
		return f.search(q.Get("q")), 200

	case strings.HasPrefix(p, "/v3/dump/email/"):
		rest := strings.TrimPrefix(p, "/v3/dump/email/")
		email, suffix, _ := strings.Cut(rest, "/")
		email = strings.ToLower(email)
		staff := strings.HasSuffix(email, "@hse.ru")
		switch suffix {
		case "link":
			if staff {
				return f.load("link_staff.json"), 200
			}
			return f.load("link_student.json"), 200
		case "subordinates":
			return f.load("subordinates.json"), 200
		case "":
			switch {
			case email == strings.ToLower(f.me()):
				return f.load("person_me.json"), 200
			case strings.Contains(email, "nobody") || !strings.Contains(email, "@"):
				return f.load("error_404.json"), 404
			case email == "avbelov@hse.ru":
				return f.load("person_chief.json"), 200
			case staff:
				return f.load("person_staff.json"), 200
			default:
				return f.load("person_student.json"), 200
			}
		}

	case p == "/v3/dump/favourites/me":
		return f.load("favourites.json"), 200

	case p == "/education/grades":
		if q.Get("academic_year") == "2025/2026" {
			return f.load("grades_2025.json"), 200
		}
		return f.load("grades_default.json"), 200

	case p == "/education/ratings":
		return f.ratings(q), 200

	case p == "/v3/dump/buildings/groups" && req.Method == http.MethodPost:
		return f.load("buildings.json"), 201

	case p == "/food/cafes":
		return f.load("cafes.json"), 200

	case strings.HasPrefix(p, "/food/cafes/"):
		rest := strings.TrimPrefix(p, "/food/cafes/")
		if strings.HasSuffix(rest, "/menu") {
			var m map[string]any
			if json.Unmarshal(f.load("menu.json"), &m) == nil {
				m["cafe_id"] = strings.TrimSuffix(rest, "/menu")
				if d := q.Get("day"); d != "" {
					m["current_day"] = d
				}
				b, _ := json.Marshal(m)
				return b, 200
			}
		}
		return f.load("cafe.json"), 200

	case p == "/tasks/services":
		if q.Get("category") != "" {
			return f.load("services_category.json"), 200
		}
		return f.load("services.json"), 200

	case p == "/tasks":
		return f.load("tasks.json"), 200

	case p == "/v2/stories":
		return f.load("stories.json"), 200

	case p == "/banners":
		if q.Get("section") == "search" {
			return f.load("banners_search.json"), 200
		}
		return f.load("banners_empty.json"), 200
	}
	return f.load("error_404.json"), 404
}

func (f *FixtureTransport) lessons(q map[string][]string) []byte {
	get := func(k string) string {
		if v := q[k]; len(v) > 0 {
			return v[0]
		}
		return ""
	}
	var files []string
	switch {
	case get("group") != "":
		files = []string{"lessons_group.json"}
	case get("auditorium") != "":
		files = []string{"lessons_auditorium.json"}
	case get("email") != "" && !strings.EqualFold(get("email"), f.me()):
		if strings.HasSuffix(strings.ToLower(get("email")), "@hse.ru") {
			files = []string{"lessons_staff.json"}
		} else {
			files = []string{"lessons_me_week.json"}
		}
	default:
		files = []string{"lessons_me_week.json", "lessons_me.json"}
	}
	var all []map[string]any
	seen := map[string]bool{}
	for _, name := range files {
		var ls []map[string]any
		_ = json.Unmarshal(f.load(name), &ls)
		for _, l := range ls {
			id, _ := l["id"].(string)
			if seen[id] {
				continue
			}
			seen[id] = true
			all = append(all, l)
		}
	}
	start, _ := time.ParseInLocation("2006-01-02", get("start"), Moscow)
	end, endErr := time.ParseInLocation("2006-01-02", get("end"), Moscow)
	out := []map[string]any{}
	for _, l := range all {
		ds, _ := l["date_start"].(string)
		t := ParseTime(ds).In(Moscow)
		if !start.IsZero() && t.Before(start) {
			continue
		}
		if endErr == nil && !t.Before(end.AddDate(0, 0, 1)) {
			continue
		}
		out = append(out, l)
	}
	b, _ := json.Marshal(out)
	return b
}

func (f *FixtureTransport) search(query string) []byte {
	query = strings.ToLower(strings.TrimSpace(query))
	out := []map[string]any{}
	seen := map[string]bool{}
	for _, name := range []string{"search_people.json", "search_group.json", "search_mixed.json"} {
		var items []map[string]any
		_ = json.Unmarshal(f.load(name), &items)
		for _, it := range items {
			id, _ := it["id"].(string)
			if seen[id] {
				continue
			}
			var hay []string
			for _, k := range []string{"full_name", "label", "room", "email", "description"} {
				if s, ok := it[k].(string); ok {
					hay = append(hay, strings.ToLower(s))
				}
			}
			if query == "" || strings.Contains(strings.Join(hay, " "), query) {
				seen[id] = true
				out = append(out, it)
			}
		}
	}
	b, _ := json.Marshal(out)
	return b
}

func (f *FixtureTransport) ratings(q map[string][]string) []byte {
	var resp map[string]any
	if err := json.Unmarshal(f.load("ratings.json"), &resp); err != nil {
		return f.load("ratings.json")
	}
	typ := ""
	if v := q["type"]; len(v) > 0 {
		typ = v[0]
	}
	module := ""
	if v := q["module_number"]; len(v) > 0 {
		module = v[0]
	}
	if typ == "" && module == "" {
		return f.load("ratings.json")
	}
	items, _ := resp["items"].([]any)
	var kept []any
	for _, it := range items {
		m, _ := it.(map[string]any)
		if typ != "" && m["type"] != typ {
			continue
		}
		if module != "" {
			lp, _ := m["learn_period"].(string)
			if !strings.Contains(lp, " "+module+" ") {
				continue
			}
		}
		kept = append(kept, m)
	}
	if kept == nil {
		kept = []any{}
	}
	resp["items"] = kept
	if typ != "" {
		resp["selected_type"] = typ
	}
	b, _ := json.Marshal(resp)
	return b
}
