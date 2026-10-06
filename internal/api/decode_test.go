package api

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fixtureClient is a real Client wired to the recorded responses.
func fixtureClient(t *testing.T) *Client {
	t.Helper()
	c := NewClient("http://fixtures.invalid", nil, NewCache(""))
	c.HTTP = &http.Client{Transport: &FixtureTransport{Dir: "testdata"}}
	return c
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Every fixture must be valid JSON; a broken file would silently fall back
// to the FixtureMissing envelope in FixtureTransport.
func TestFixturesAreValidJSON(t *testing.T) {
	files, err := filepath.Glob("testdata/*.json")
	mustOK(t, err)
	if len(files) < 30 {
		t.Fatalf("expected the recorded fixtures, found %d files", len(files))
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		mustOK(t, err)
		if !json.Valid(b) {
			t.Errorf("%s is not valid JSON", f)
		}
	}
}

func TestDecodeLessonsFixtures(t *testing.T) {
	c := fixtureClient(t)
	ctx := t.Context()

	// Own timetable: lessons_me_week.json + lessons_me.json, merged and sorted.
	ls, meta, err := c.Lessons(ctx, LessonQuery{})
	mustOK(t, err)
	if meta.FromCache || meta.Stale || meta.Fetched.IsZero() {
		t.Errorf("unexpected meta for a fresh response: %+v", meta)
	}
	if len(ls) != 26 {
		t.Fatalf("got %d lessons, want 26", len(ls))
	}
	for i := 1; i < len(ls); i++ {
		if ls[i].DateStart.Before(ls[i-1].DateStart.Time) {
			t.Fatalf("lessons not sorted at %d: %v before %v", i, ls[i].DateStart, ls[i-1].DateStart)
		}
	}
	var seminar *Lesson
	for i := range ls {
		if ls[i].ID == "1549245" {
			seminar = &ls[i]
		}
	}
	if seminar == nil {
		t.Fatal("lesson 1549245 missing")
	}
	if seminar.AuditoriumID != "526" || seminar.DisciplineID != "44917" || seminar.BuildingID != "27" {
		t.Errorf("numeric ids not decoded as strings: %q %q %q", seminar.AuditoriumID, seminar.DisciplineID, seminar.BuildingID)
	}
	if seminar.GroupID != "ruz75091" {
		t.Errorf("group id %q", seminar.GroupID)
	}
	if got := seminar.Start(); got.Location().String() != "Europe/Moscow" || got.Hour() != 13 || got.Day() != 12 {
		t.Errorf("Start() = %v, want 2026-10-12 13:00 MSK", got)
	}
	if got := seminar.End(); got.Hour() != 14 || got.Minute() != 20 {
		t.Errorf("End() = %v", got)
	}
	if !seminar.LessonNumberStart.OK || seminar.LessonNumberStart.Int() != 3 || seminar.ImportanceLevel.Int() != 2 {
		t.Errorf("nums: %+v %+v", seminar.LessonNumberStart, seminar.ImportanceLevel)
	}
	if !seminar.Location.Valid() || seminar.Location.Lng() < 37 || seminar.Location.Lat() < 55 {
		t.Errorf("location %+v", seminar.Location)
	}
	if len(seminar.LecturerProfiles) != 1 || seminar.LecturerProfiles[0].Email != "aivchenko@hse.ru" || seminar.LecturerProfiles[0].Type != TypeStaff {
		t.Errorf("lecturer profiles %+v", seminar.LecturerProfiles)
	}
	if seminar.StreamName() != "Теория вероятностей и математическая статистика" {
		t.Errorf("StreamName() = %q", seminar.StreamName())
	}
	if seminar.Kind() != "Seminar" || seminar.IsOnline() {
		t.Errorf("Kind/IsOnline: %q %v", seminar.Kind(), seminar.IsOnline())
	}

	// The online lessons carry the same URL in stream_links and the note.
	online := 0
	for _, l := range ls {
		if !l.IsOnline() {
			continue
		}
		online++
		links := l.Links()
		if len(links) != 1 || !strings.HasPrefix(links[0].Link, "https://hse.mts-link.ru/") {
			t.Errorf("online lesson %s links = %+v, want one deduplicated link", l.ID, links)
		}
	}
	if online == 0 {
		t.Error("expected online lessons in the fixtures")
	}

	for _, q := range []LessonQuery{
		{Group: "ruz47990"},
		{Auditorium: "ruz1033"},
		{Email: "yakinderknecht@hse.ru"},
		{Email: "midmreznichenko@edu.hse.ru"},
	} {
		ls, _, err := c.Lessons(ctx, q)
		mustOK(t, err)
		if len(ls) == 0 {
			t.Errorf("%+v: no lessons", q)
		}
		for _, l := range ls {
			if l.ID == "" || l.DateStart.IsZero() || l.DateEnd.IsZero() || l.Discipline == "" {
				t.Errorf("%+v: incomplete lesson %+v", q, l)
			}
		}
	}

	// The auditorium fixture has a description that repeats the link.
	ls, _, err = c.Lessons(ctx, LessonQuery{Auditorium: "ruz1033"})
	mustOK(t, err)
	found := false
	for _, l := range ls {
		if links := l.Links(); len(links) > 0 {
			found = true
			if len(links) != 1 || links[0].Link != "https://telemost.yandex.ru/j/88705648092192" {
				t.Errorf("auditorium lesson links = %+v", links)
			}
		}
	}
	if !found {
		t.Error("expected a lesson with stream links in lessons_auditorium.json")
	}
}

func TestDecodeLessonsFixtureDateRange(t *testing.T) {
	c := fixtureClient(t)
	start := time.Date(2026, 10, 12, 0, 0, 0, 0, Moscow)
	end := time.Date(2026, 10, 13, 0, 0, 0, 0, Moscow)
	ls, _, err := c.Lessons(t.Context(), LessonQuery{Start: start, End: end})
	mustOK(t, err)
	if len(ls) == 0 {
		t.Fatal("no lessons on 12–13 Oct")
	}
	for _, l := range ls {
		d := l.Start()
		if d.Day() != 12 && d.Day() != 13 {
			t.Errorf("lesson on %v outside the inclusive range", d)
		}
	}
}

func TestDecodeSearchFixtures(t *testing.T) {
	c := fixtureClient(t)
	hits, _, err := c.Search(t.Context(), "")
	mustOK(t, err)
	byType := map[string][]Person{}
	for _, h := range hits {
		byType[h.Type] = append(byType[h.Type], h)
		if h.ID == "" || h.DisplayName() == "" {
			t.Errorf("hit without id/name: %+v", h)
		}
	}
	for _, typ := range []string{TypeStudent, TypeStaff, TypeGroup, TypeAuditorium} {
		if len(byType[typ]) == 0 {
			t.Errorf("no %s hits decoded", typ)
		}
	}

	g := byType[TypeGroup][0]
	if !g.IsGroup() || g.IsPerson() || g.Label != "БИБ255" || g.DisplayName() != "БИБ255" || g.Course.Int() != 2 || g.ProgramName == "" || g.ID != "ruz47990" {
		t.Errorf("group hit: %+v", g)
	}
	a := byType[TypeAuditorium][0]
	if !a.IsAuditorium() || a.IsPerson() || a.Room != "R615" || a.DisplayName() != "R615" || a.AuditoriumType == "" || !a.AuditoriumLocation.Valid() || a.ID != "ruz1033" {
		t.Errorf("auditorium hit: %+v", a)
	}
	for _, p := range append(byType[TypeStudent], byType[TypeStaff]...) {
		if !p.IsPerson() || p.FullName == "" || !strings.Contains(p.Email, "@") {
			t.Errorf("person hit: %+v", p)
		}
	}

	// FixtureTransport filters by the query.
	hits, _, err = c.Search(t.Context(), "Reznichenko")
	mustOK(t, err)
	if len(hits) == 0 {
		t.Fatal("no hits for Reznichenko")
	}
	for _, h := range hits {
		if !strings.Contains(strings.ToLower(h.FullName+h.Email), "reznichenko") {
			t.Errorf("unexpected hit %+v", h)
		}
	}
}

func TestDecodePersonFixtures(t *testing.T) {
	c := fixtureClient(t)
	ctx := t.Context()

	me, _, err := c.Person(ctx, DemoEmail)
	mustOK(t, err)
	if me.Type != TypeStudent || me.Names == nil || me.Names.FirstName != "Maksim" || len(me.Education) != 1 {
		t.Fatalf("me: %+v", me)
	}
	if e := me.Education[0]; e.GroupTitle != "БИБ255" || e.StartYear != "2024" || e.SmartPlanProgram != "97" || e.Campus != "CAMPUS_MOS" {
		t.Errorf("education: %+v", e)
	}
	if !me.HasTimetable() || me.HasSubordinates() {
		t.Errorf("me flags: timetable=%v subordinates=%v", me.HasTimetable(), me.HasSubordinates())
	}
	if me.BirthDay() != "20 August 2007" {
		t.Errorf("BirthDay() = %q", me.BirthDay())
	}

	staff, _, err := c.Person(ctx, "yakinderknecht@hse.ru")
	mustOK(t, err)
	if staff.Type != TypeStaff || staff.BirthDay() != "14 November" {
		t.Errorf("staff: type=%q birthday=%q", staff.Type, staff.BirthDay())
	}
	if len(staff.StaffAddress) != 1 || staff.StaffAddress[0].RoomCode != "422" || staff.StaffAddress[0].PhoneInternalExt != "" {
		t.Errorf("staff address: %+v", staff.StaffAddress)
	}
	if len(staff.StaffPositions) != 1 {
		t.Fatalf("staff positions: %+v", staff.StaffPositions)
	}
	pos := staff.StaffPositions[0]
	if pos.UnitID != "1130" || !pos.IsMain || pos.Chief == nil || pos.Chief.Email != "avbelov@hse.ru" || pos.Chief.BirthDay() != "22 January" {
		t.Errorf("staff position: %+v chief=%+v", pos, pos.Chief)
	}

	chief, _, err := c.Person(ctx, "AVBelov@hse.ru")
	mustOK(t, err)
	if !chief.HasSubordinates() || !chief.HasTimetable() || len(chief.StaffPositions) != 3 {
		t.Errorf("chief: %+v", chief)
	}
	if chief.StaffAddress[0].PhoneInternalExt != "11086" || chief.StaffAddress[0].PhoneInternalFull == "" {
		t.Errorf("chief phones: %+v", chief.StaffAddress[0])
	}

	student, _, err := c.Person(ctx, "midmreznichenko@edu.hse.ru")
	mustOK(t, err)
	if student.Type != TypeStudent || student.Education[0].GroupTitle != "БИБ252" || student.AvatarURL == "" {
		t.Errorf("student: %+v", student)
	}

	_, _, err = c.Person(ctx, "nobody@hse.ru")
	var ae *APIError
	if !errors.As(err, &ae) || !ae.NotFound() || ae.Name != "NotFound" || ae.Message != "users.get: User not found" || ae.TraceID != "8f6d1f84-ea73-4a59-a41c-1fac0c86fd22" {
		t.Errorf("unknown person: err=%v (%+v)", err, ae)
	}

	link, _, err := c.PersonLink(ctx, "yakinderknecht@hse.ru")
	mustOK(t, err)
	if link != "https://www.hse.ru/staff/kinderknecht/" {
		t.Errorf("staff link %q", link)
	}
	link, _, err = c.PersonLink(ctx, "midmreznichenko@edu.hse.ru")
	mustOK(t, err)
	if link != "" {
		t.Errorf("student link (url: null) = %q, want empty", link)
	}

	subs, _, err := c.Subordinates(ctx, "avbelov@hse.ru")
	mustOK(t, err)
	if subs == nil || len(subs) != 0 {
		t.Errorf("subordinates = %#v, want empty non-nil slice", subs)
	}
	favs, _, err := c.Favourites(ctx)
	mustOK(t, err)
	if len(favs) != 0 {
		t.Errorf("favourites = %+v", favs)
	}
}

func TestDecodeGradesFixtures(t *testing.T) {
	c := fixtureClient(t)
	ctx := t.Context()

	// Current year: nothing graded yet ("grade" missing).
	def, _, err := c.Grades(ctx, "", "")
	mustOK(t, err)
	if len(def.Items) == 0 || def.SelectedAcademicYear != "2026/2027" || def.CurrentAcademicYear != "2026/2027" {
		t.Fatalf("default grades: %+v", def)
	}
	for _, g := range def.Items {
		if g.Grade != nil || g.HasGrade() || g.GradeText() != "" || g.ProgramID != "000275111" || !g.Date.IsZero() {
			t.Errorf("ungraded item decoded as %+v", g)
		}
	}

	last, _, err := c.Grades(ctx, "2025/2026", "000275111")
	mustOK(t, err)
	if last.SelectedProgram != "000275111" || len(last.AvailablePrograms) != 1 || last.AvailablePrograms[0].DisplayName() != "Информационная безопасность" {
		t.Errorf("selectors: %+v", last)
	}
	if len(last.AvailableAcademicYears) != 2 {
		t.Errorf("years: %v", last.AvailableAcademicYears)
	}
	var pass, numeric *Grade
	for i := range last.Items {
		g := &last.Items[i]
		if !g.HasGrade() {
			t.Errorf("2025/2026 item %s not graded", g.ID)
		}
		if g.Discipline == "Physical Training" {
			pass = g
		}
		if g.ID == "8119242" {
			numeric = g
		}
	}
	if pass == nil || pass.GradeText() != "pass" || pass.Grade.TenPoint.OK {
		t.Errorf("{pass:true} grade: %+v", pass)
	}
	if numeric == nil {
		t.Fatal("grade 8119242 missing")
	}
	if numeric.GradeText() != "9" || numeric.Grade.FivePoint.Int() != 5 || numeric.Grade.Pass == nil || *numeric.Grade.Pass {
		t.Errorf("numeric grade: %+v", numeric.Grade)
	}
	// "2025-12-24T00:00:00" is Moscow midnight.
	if want := time.Date(2025, 12, 24, 0, 0, 0, 0, Moscow); !numeric.Date.Equal(want) {
		t.Errorf("date = %v, want %v", numeric.Date, want)
	}
	if numeric.ModuleNum != "2" || numeric.Credits.Int() != 4 {
		t.Errorf("module/credits: %q %v", numeric.ModuleNum, numeric.Credits)
	}
}

func TestDecodeRatingsFixtures(t *testing.T) {
	c := fixtureClient(t)
	ctx := t.Context()

	all, _, err := c.Ratings(ctx, RatingQuery{})
	mustOK(t, err)
	if len(all.Items) != 5 {
		t.Fatalf("got %d ratings", len(all.Items))
	}
	if all.SelectedProgram != "1306560" || all.AvailablePrograms[0].ID != "1306560" || all.AvailablePrograms[0].DisplayName() != "Information Security" {
		t.Errorf("numeric program ids: %+v", all)
	}
	if len(all.AvailableModuleNumbers) != 2 || all.AvailableModuleNumbers[1].Int() != 4 || all.SelectedModuleNumber.OK {
		t.Errorf("module numbers: %+v selected=%+v", all.AvailableModuleNumbers, all.SelectedModuleNumber)
	}
	for _, r := range all.Items {
		if r.GradeMinor.OK || r.ExamE.OK || r.ExamF.OK || r.ExamP.OK {
			t.Errorf("null nums decoded as set: %+v", r)
		}
		if r.GradeMinor.String() != "—" {
			t.Errorf("unset Num String() = %q", r.GradeMinor.String())
		}
		if !r.GPA.OK || !r.PlaceCurr.OK || !r.PlaceTotal.OK || r.PublishedAt.IsZero() || len(r.DisciplineList) == 0 {
			t.Errorf("incomplete rating: %+v", r)
		}
	}
	minor := all.Items[0]
	if want := time.Date(2026, 3, 23, 20, 15, 4, 235e6, time.UTC); !minor.PublishedAt.Equal(want) {
		t.Errorf("published_at = %v, want %v", minor.PublishedAt.UTC(), want)
	}
	if minor.GradeMid.Fixed(2) != "6.14" || minor.PlaceCurr.Int() != 5315 {
		t.Errorf("minor: %v %v", minor.GradeMid, minor.PlaceCurr)
	}

	cumul, _, err := c.Ratings(ctx, RatingQuery{Type: "cumul"})
	mustOK(t, err)
	if len(cumul.Items) != 1 || cumul.Items[0].Type != "cumul" || cumul.SelectedType != "cumul" {
		t.Errorf("cumul: %+v", cumul.Items)
	}
	m4, _, err := c.Ratings(ctx, RatingQuery{Type: "current", ModuleNumber: 4})
	mustOK(t, err)
	if len(m4.Items) != 1 || m4.Items[0].GPA.Fixed(2) != "6.81" {
		t.Errorf("current m4: %+v", m4.Items)
	}

	// The two recorded filtered responses decode too.
	for _, f := range []string{"ratings_cumul.json", "ratings_current_m4.json"} {
		var r RatingsResponse
		b, err := os.ReadFile(filepath.Join("testdata", f))
		mustOK(t, err)
		mustOK(t, decode(b, &r, f))
		if len(r.Items) != 1 || r.SelectedType == "" {
			t.Errorf("%s: %+v", f, r)
		}
	}
}

func TestDecodeBuildingsFixture(t *testing.T) {
	c := fixtureClient(t)
	groups, _, err := c.Buildings(t.Context())
	mustOK(t, err)
	if len(groups) != 4 {
		t.Fatalf("got %d campus groups", len(groups))
	}
	campuses := map[string]bool{}
	var buildings, withCafes, withLibs int
	var sanitary string
	for _, g := range groups {
		campuses[g.Campus] = true
		for _, b := range g.Buildings {
			buildings++
			if b.ID == "" || b.Name == "" {
				t.Errorf("building without id/name: %+v", b)
			}
			if len(b.Cafes) > 0 {
				withCafes++
				for _, cf := range b.Cafes {
					if cf.ID == "" || cf.Name == "" {
						t.Errorf("cafe without id/name in %s", b.Name)
					}
				}
			}
			if len(b.LibrariesV3) > 0 {
				withLibs++
				for _, l := range b.LibrariesV3 {
					if l.ID == "" || l.Name == "" || len(l.Offices) == 0 {
						t.Errorf("library %+v", l)
					}
					for _, o := range l.Offices {
						for _, r := range o.Rules {
							if r.Schedule != nil && r.Schedule.SanitaryDay != "" {
								sanitary = r.Schedule.SanitaryDay
							}
						}
					}
				}
			}
		}
	}
	for _, cp := range []string{"CAMPUS_MOS", "CAMPUS_PERM", "CAMPUS_SPB", "CAMPUS_NNOV"} {
		if !campuses[cp] {
			t.Errorf("campus %s missing", cp)
		}
		if CampusName(cp) == cp {
			t.Errorf("CampusName(%s) not humanised", cp)
		}
	}
	if buildings != 49 || withCafes != 29 || withLibs != 13 {
		t.Errorf("buildings=%d withCafes=%d withLibs=%d", buildings, withCafes, withLibs)
	}
	if sanitary != "SANITARY_DAY_FIRST_FRIDAY" {
		t.Errorf("sanitary day %q", sanitary)
	}

	// MIEM library reading room: weekly hours.
	var miem *Building
	for gi := range groups {
		for bi := range groups[gi].Buildings {
			if groups[gi].Buildings[bi].ID == "27" {
				miem = &groups[gi].Buildings[bi]
			}
		}
	}
	if miem == nil || len(miem.LibrariesV3) == 0 {
		t.Fatal("MIEM building or its library missing")
	}
	hours := miem.LibrariesV3[0].Offices[0].Rules[0].Schedule.OpeningHours
	if h, ok := HoursFor(hours, time.Monday); !ok || h.Range() != "10:00–21:00" {
		t.Errorf("MIEM reading room Monday = %+v", h)
	}
	if _, ok := HoursFor(hours, time.Sunday); ok {
		t.Error("MIEM reading room has no Sunday entry")
	}
	if !miem.Location.Valid() {
		t.Error("building location missing")
	}
}

func TestDecodeCafesFixtures(t *testing.T) {
	c := fixtureClient(t)
	ctx := t.Context()

	groups, _, err := c.Cafes(ctx)
	mustOK(t, err)
	if len(groups) != 32 {
		t.Fatalf("got %d cafe groups", len(groups))
	}
	if groups[0].CampusID != "3" || !groups[0].Coordinates.Valid() {
		t.Errorf("group 0: %+v", groups[0])
	}
	var n, perm, withNav int
	for _, g := range groups {
		for _, cf := range g.Cafes {
			n++
			if cf.ID == "" || cf.Name == "" {
				t.Errorf("cafe without id/name: %+v", cf)
			}
			if cf.Navigation != nil {
				withNav++
			}
			if cf.Coordinates.Valid() && cf.Coordinates.Lng.V > 50 {
				perm++
				if z := cf.Zone().String(); z != "Asia/Yekaterinburg" {
					t.Errorf("Perm cafe %s zone = %s", cf.Name, z)
				}
			}
			// Status must never panic on real schedules.
			_ = cf.Status(time.Date(2026, 10, 6, 12, 0, 0, 0, Moscow))
		}
	}
	if n != 58 || perm == 0 || withNav == 0 {
		t.Errorf("cafes=%d perm=%d withNav=%d", n, perm, withNav)
	}

	cafe, _, err := c.Cafe(ctx, "65109fbeb78246260f15bc88")
	mustOK(t, err)
	if cafe.Name != "Столовая на Таллинской" || !cafe.HasMenu || len(cafe.ClosedDates) != 5 || len(cafe.OpeningHours) != 7 || cafe.Zone() != Moscow {
		t.Errorf("cafe: %+v", cafe)
	}

	menu, _, err := c.CafeMenu(ctx, "65109fbeb78246260f15bc88", "Monday")
	mustOK(t, err)
	if menu.CafeID != "65109fbeb78246260f15bc88" || menu.CurrentDay != "monday" || len(menu.AvailableDays) != 6 || len(menu.Sections) != 9 {
		t.Fatalf("menu: id=%q day=%q days=%v sections=%d", menu.CafeID, menu.CurrentDay, menu.AvailableDays, len(menu.Sections))
	}
	set := menu.Sections[0]
	if set.Price.Int() != 280 || set.SectionName == "" || len(set.Items) == 0 {
		t.Errorf("set section: %+v", set)
	}
	it := set.Items[0]
	if it.ItemName == "" || it.ItemNameOpt == "" || it.Weight == "" || it.Price.OK || it.ItemID == "" {
		t.Errorf("menu item: %+v", it)
	}
	priced := false
	for _, s := range menu.Sections {
		for _, it := range s.Items {
			if it.Price.OK && it.Price.V > 0 {
				priced = true
			}
		}
	}
	if !priced {
		t.Error("no priced menu items decoded")
	}
}

func TestDecodeServicesTasksStoriesBanners(t *testing.T) {
	c := fixtureClient(t)
	ctx := t.Context()

	root, _, err := c.Services(ctx, "")
	mustOK(t, err)
	if len(root) != 10 {
		t.Fatalf("got %d services", len(root))
	}
	var folders, links int
	for _, s := range root {
		if s.ID == "" || s.Name == "" {
			t.Errorf("service %+v", s)
		}
		if s.IsFolder() {
			folders++
			if s.URL != "" || s.Category == "" {
				t.Errorf("folder %+v", s)
			}
		} else {
			links++
			if s.URL == "" {
				t.Errorf("link without url %+v", s)
			}
		}
		if s.ID == "2031" && s.IsFolder() {
			t.Error("service 2031 has a url and must not be a folder")
		}
		if s.ID == "2582" && !s.IsFolder() {
			t.Error("service 2582 has only a category and must be a folder")
		}
	}
	if folders == 0 || links == 0 {
		t.Errorf("folders=%d links=%d", folders, links)
	}
	sub, _, err := c.Services(ctx, "/ELK_YA-STUDENT_DOOR")
	mustOK(t, err)
	if len(sub) != 20 {
		t.Errorf("category listing: %d", len(sub))
	}

	tasks, _, err := c.Tasks(ctx, "")
	mustOK(t, err)
	if tasks.Cursor != "0" || tasks.NextCursor != "0" || tasks.Items == nil || len(tasks.Items) != 0 {
		t.Errorf("tasks: %+v", tasks)
	}

	stories, _, err := c.Stories(ctx)
	mustOK(t, err)
	if len(stories) != 12 {
		t.Fatalf("got %d stories", len(stories))
	}
	blank := 0
	for _, s := range stories {
		if s.Title == "⠀" {
			blank++
		}
		if s.ID == "" || s.Publisher.Name == "" || len(s.Pages) == 0 || s.Published().IsZero() {
			t.Errorf("story %+v", s)
		}
		for _, p := range s.Pages {
			if p.Action != nil && p.Action.Link == "" {
				t.Errorf("story action without link: %+v", p.Action)
			}
		}
	}
	if blank == 0 {
		t.Error("expected stories with the invisible U+2800 title")
	}
	if want := time.Date(2026, 9, 2, 8, 0, 0, 0, time.UTC); !stories[0].Pages[0].DatePublished.Equal(want) {
		t.Errorf("date_published = %v", stories[0].Pages[0].DatePublished)
	}
	if stories[0].Pages[0].Duration.Int() != 5 || stories[0].Pages[0].Action.Title == "" {
		t.Errorf("page 0: %+v", stories[0].Pages[0])
	}

	none, _, err := c.Banners(ctx, "")
	mustOK(t, err)
	if len(none) != 0 {
		t.Errorf("banners: %+v", none)
	}
	bs, _, err := c.Banners(ctx, "search")
	mustOK(t, err)
	if len(bs) != 1 || bs[0].ID != "6ab3395b85ef4e9464628df4" || !bs[0].IsDismissible || !bs[0].Enabled() || bs[0].Title == "" || bs[0].Section != "search" || bs[0].Href() != "" {
		t.Errorf("search banners: %+v", bs)
	}
}

// ------------------------------------------------------------- flex types

func TestFlexString(t *testing.T) {
	var v struct {
		Num, Str, Null, Obj, Arr, Float, Bool, Padded, Neg, Missing FlexString
	}
	in := `{"Num": 1130, "Str": "lk3615", "Null": null, "Obj": {"x": 1}, "Arr": [1, 2],
		"Float": 6.73, "Bool": true, "Padded": "000275111", "Neg": -3}`
	mustOK(t, json.Unmarshal([]byte(in), &v))
	want := map[string]FlexString{
		"Num": "1130", "Str": "lk3615", "Null": "", "Obj": "", "Arr": "", "Float": "6.73",
		"Bool": "true", "Padded": "000275111", "Neg": "-3", "Missing": "",
	}
	got := map[string]FlexString{
		"Num": v.Num, "Str": v.Str, "Null": v.Null, "Obj": v.Obj, "Arr": v.Arr, "Float": v.Float,
		"Bool": v.Bool, "Padded": v.Padded, "Neg": v.Neg, "Missing": v.Missing,
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s = %q, want %q", k, got[k], w)
		}
	}
	if v.Str.String() != "lk3615" {
		t.Errorf("String() = %q", v.Str.String())
	}

	// null over an existing value resets it.
	f := FlexString("old")
	mustOK(t, json.Unmarshal([]byte("null"), &f))
	if f != "" {
		t.Errorf("null decoded as %q", f)
	}
	// An object inside a document doesn't fail the rest of it.
	var doc struct {
		ID   FlexString `json:"id"`
		Name string     `json:"name"`
	}
	mustOK(t, json.Unmarshal([]byte(`{"id": {"$oid": "x"}, "name": "kept"}`), &doc))
	if doc.Name != "kept" || doc.ID != "" {
		t.Errorf("doc = %+v", doc)
	}
}

func TestNum(t *testing.T) {
	cases := []struct {
		in   string
		ok   bool
		v    float64
		str  string
		name string
	}{
		{`7`, true, 7, "7", "int"},
		{`6.73`, true, 6.73, "6.73", "float"},
		{`-1.5`, true, -1.5, "-1.5", "negative"},
		{`0`, true, 0, "0", "zero is set"},
		{`"7.5"`, true, 7.5, "7.5", "numeric string"},
		{`"6,73"`, true, 6.73, "6.73", "comma decimal"},
		{`" 280 "`, true, 280, "280", "padded string"},
		{`1e3`, true, 1000, "1000", "exponent"},
		{`null`, false, 0, "—", "null"},
		{`""`, false, 0, "—", "empty string"},
		{`"abc"`, false, 0, "—", "garbage string"},
		{`"1,234.5"`, false, 0, "—", "thousands separator"},
		{`true`, false, 0, "—", "bool"},
		{`{"v": 1}`, false, 0, "—", "object"},
		{`[1]`, false, 0, "—", "array"},
		{`"NaN"`, false, 0, "—", "NaN string"},
		{`"Infinity"`, false, 0, "—", "Inf string"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			n := NewNum(42) // decoding must overwrite previous state
			if err := json.Unmarshal([]byte(c.in), &n); err != nil {
				t.Fatalf("Unmarshal(%s) failed: %v", c.in, err)
			}
			if n.OK != c.ok || (c.ok && math.Abs(n.V-c.v) > 1e-9) || (!c.ok && n.V != 0) {
				t.Errorf("Unmarshal(%s) = %+v, want ok=%v v=%v", c.in, n, c.ok, c.v)
			}
			if n.String() != c.str {
				t.Errorf("String() = %q, want %q", n.String(), c.str)
			}
			b, err := json.Marshal(n)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			var back Num
			mustOK(t, json.Unmarshal(b, &back))
			if back != n {
				t.Errorf("roundtrip %s -> %s -> %+v", c.in, b, back)
			}
		})
	}

	// A garbage field doesn't fail the surrounding document.
	var r RatingDiscipline
	mustOK(t, json.Unmarshal([]byte(`{"discipline":"X","current_rating":"n/a","grade":"8,5"}`), &r))
	if r.Discipline != "X" || r.CurrentRating.OK || r.Grade.V != 8.5 {
		t.Errorf("rating discipline: %+v", r)
	}

	if s := NewNum(6.7349).Fixed(2); s != "6.73" {
		t.Errorf("Fixed(2) = %q", s)
	}
	if s := (Num{}).Fixed(2); s != "—" {
		t.Errorf("unset Fixed = %q", s)
	}
	if NewNum(9.99).Int() != 9 || (Num{}).Int() != 0 {
		t.Error("Int() should truncate and be 0 when unset")
	}
}

func TestFlexTime(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want time.Time
	}{
		{"RFC3339 Z", `"2026-10-12T10:00:00Z"`, time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)},
		{"millis Z", `"2026-09-02T08:00:00.000Z"`, time.Date(2026, 9, 2, 8, 0, 0, 0, time.UTC)},
		{"offset with millis", `"2026-03-23T23:15:04.235+03:00"`, time.Date(2026, 3, 23, 20, 15, 4, 235e6, time.UTC)},
		{"offset without colon", `"2026-03-23T23:15:04.235+0300"`, time.Date(2026, 3, 23, 20, 15, 4, 235e6, time.UTC)},
		{"naive is Moscow", `"2025-12-24T00:00:00"`, time.Date(2025, 12, 23, 21, 0, 0, 0, time.UTC)},
		{"naive with fraction", `"2025-12-24T00:00:00.5"`, time.Date(2025, 12, 23, 21, 0, 0, 5e8, time.UTC)},
		{"naive with space", `"2025-12-24 10:30:00"`, time.Date(2025, 12, 24, 7, 30, 0, 0, time.UTC)},
		{"date only is Moscow", `"2026-08-27"`, time.Date(2026, 8, 26, 21, 0, 0, 0, time.UTC)},
		{"padded", `" 2026-08-27 "`, time.Date(2026, 8, 26, 21, 0, 0, 0, time.UTC)},
		{"garbage", `"next tuesday"`, time.Time{}},
		{"empty", `""`, time.Time{}},
		{"null", `null`, time.Time{}},
		{"number", `1760000000`, time.Time{}},
		{"object", `{"$date": "2026-01-01"}`, time.Time{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ft := FlexTime{time.Now()} // must be reset
			if err := json.Unmarshal([]byte(c.in), &ft); err != nil {
				t.Fatalf("Unmarshal(%s): %v", c.in, err)
			}
			if !ft.Equal(c.want) || ft.IsZero() != c.want.IsZero() {
				t.Errorf("Unmarshal(%s) = %v, want %v", c.in, ft.Time, c.want)
			}
		})
	}

	// Naive values land in the Moscow zone so calendar dates are preserved.
	if d := ParseTime("2025-12-24T00:00:00"); d.Location() != Moscow || d.Day() != 24 {
		t.Errorf("naive time = %v (%v)", d, d.Location())
	}

	// Marshal roundtrip; zero marshals to null.
	in := FlexTime{time.Date(2026, 3, 23, 23, 15, 4, 235e6, Moscow)}
	b, err := json.Marshal(in)
	mustOK(t, err)
	var out FlexTime
	mustOK(t, json.Unmarshal(b, &out))
	if !out.Equal(in.Time) {
		t.Errorf("roundtrip %v -> %s -> %v", in, b, out)
	}
	b, err = json.Marshal(FlexTime{})
	mustOK(t, err)
	if string(b) != "null" {
		t.Errorf("zero FlexTime marshals to %s", b)
	}
}
