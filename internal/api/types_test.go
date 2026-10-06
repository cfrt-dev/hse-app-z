package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLessonLinks(t *testing.T) {
	l := Lesson{
		StreamLinks: []StreamLink{
			{Link: " https://zoom.us/j/1 ", Description: "Zoom"},
			{Link: "https://zoom.us/j/1", Description: "duplicate"},
			{Link: "", Description: "empty"},
			{Link: "https://teams/x"},
		},
		Note: "Join: https://zoom.us/j/1 or https://meet/2\nslides http://slides/3 ftp://no and https://meet/2",
	}
	got := l.Links()
	want := []StreamLink{
		{Link: "https://zoom.us/j/1", Description: "Zoom"},
		{Link: "https://teams/x"},
		{Link: "https://meet/2"},
		{Link: "http://slides/3"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("Links() = %+v\nwant      %+v", got, want)
	}
	if links := (Lesson{Note: "room changed"}).Links(); len(links) != 0 {
		t.Errorf("no-URL note produced %+v", links)
	}
}

func TestLessonHelpers(t *testing.T) {
	streams := map[string]string{
		"26_27_М_ТВИМС_Г_1159405_5#Г#Теория вероятностей": "Теория вероятностей",
		"plain stream": "plain stream",
		"trailing#":    "trailing#",
		"":             "",
	}
	for in, want := range streams {
		if got := (Lesson{Stream: in}).StreamName(); got != want {
			t.Errorf("StreamName(%q) = %q, want %q", in, got, want)
		}
	}

	online := map[string]bool{
		"Online": true, " online ": true, "Онлайн": true, "online (Zoom)": true, "ОНЛАЙН": true,
		"303": false, "": false, "R615": false,
	}
	for in, want := range online {
		if got := (Lesson{Auditorium: in}).IsOnline(); got != want {
			t.Errorf("IsOnline(%q) = %v", in, got)
		}
	}

	if k := (Lesson{Type: "Seminar", KindOfWork: "Practical class"}).Kind(); k != "Practical class" {
		t.Errorf("Kind() = %q", k)
	}
	if k := (Lesson{Type: "Lecture", KindOfWork: "  "}).Kind(); k != "Lecture" {
		t.Errorf("Kind() fallback = %q", k)
	}

	l := Lesson{TimeZone: "Asia/Yekaterinburg", DateStart: FlexTime{time.Date(2026, 10, 5, 5, 0, 0, 0, time.UTC)}}
	if l.Start().Hour() != 10 || l.Zone().String() != "Asia/Yekaterinburg" {
		t.Errorf("Perm lesson start %v", l.Start())
	}
	if (Lesson{}).Zone() != Moscow {
		t.Error("default lesson zone should be Moscow")
	}
}

func TestPersonHelpers(t *testing.T) {
	birthdays := map[string]string{
		"0000-11-14":   "14 November",
		" 0000-01-22 ": "22 January",
		"0000-02-29":   "29 February",
		"2007-08-20":   "20 August 2007",
		"":             "",
		"0000-13-01":   "13-01",
		"unknown":      "unknown",
	}
	for in, want := range birthdays {
		if got := (Person{BirthDate: in}).BirthDay(); got != want {
			t.Errorf("BirthDay(%q) = %q, want %q", in, got, want)
		}
	}

	yes, no := true, false
	if !(Person{}).HasTimetable() || !(Person{IsTimetableAvailable: &yes}).HasTimetable() || (Person{IsTimetableAvailable: &no}).HasTimetable() {
		t.Error("HasTimetable: nil and true → true, false → false")
	}
	if (Person{}).HasSubordinates() || !(Person{IsSubordinatesAvailable: &yes}).HasSubordinates() || (Person{IsSubordinatesAvailable: &no}).HasSubordinates() {
		t.Error("HasSubordinates: only explicit true → true")
	}

	names := []struct {
		p    Person
		want string
	}{
		{Person{FullName: "A B", Label: "L", Email: "e"}, "A B"},
		{Person{FullName: "  ", Label: "БИБ255"}, "БИБ255"},
		{Person{Room: "R615"}, "R615"},
		{Person{Email: "x@hse.ru"}, "x@hse.ru"},
		{Person{ID: "lk1"}, "lk1"},
	}
	for _, n := range names {
		if got := n.p.DisplayName(); got != n.want {
			t.Errorf("DisplayName(%+v) = %q", n.p, got)
		}
	}
	if !(Person{Type: "group"}).IsGroup() || !(Person{Type: "Auditorium"}).IsAuditorium() || !(Person{Type: "STAFF"}).IsPerson() || !(Person{}).IsPerson() {
		t.Error("type predicates")
	}
}

func TestGradeText(t *testing.T) {
	yes, no := true, false
	cases := []struct {
		g    *GradeValue
		text string
		has  bool
	}{
		{nil, "", false},
		{&GradeValue{}, "", false},
		{&GradeValue{TenPoint: NewNum(9), FivePoint: NewNum(5), Pass: &no}, "9", true},
		{&GradeValue{TenPoint: NewNum(7.5)}, "7.5", true},
		{&GradeValue{Pass: &yes}, "pass", true},
		{&GradeValue{Pass: &no}, "fail", true},
		{&GradeValue{TenPoint: NewNum(0), Pass: &yes}, "0", true},
	}
	for _, c := range cases {
		g := Grade{Grade: c.g}
		if g.GradeText() != c.text || g.HasGrade() != c.has {
			t.Errorf("%+v: GradeText=%q HasGrade=%v", c.g, g.GradeText(), g.HasGrade())
		}
	}
}

func TestSmallTypeHelpers(t *testing.T) {
	if (Program{ID: "1", Title: "T", Name: "N"}).DisplayName() != "T" ||
		(Program{ID: "1", Name: "N"}).DisplayName() != "N" ||
		(Program{ID: "1"}).DisplayName() != "1" {
		t.Error("Program.DisplayName")
	}
	campuses := map[string]string{"CAMPUS_MOS": "Moscow", "campus_spb": "St. Petersburg", "CAMPUS_NNOV": "Nizhny Novgorod", "CAMPUS_PERM": "Perm", "": "Other", "CAMPUS_KAZ": "KAZ"}
	for in, want := range campuses {
		if got := CampusName(in); got != want {
			t.Errorf("CampusName(%q) = %q", in, got)
		}
	}
	off := false
	if !(Banner{}).Enabled() || (Banner{IsEnabled: &off}).Enabled() {
		t.Error("Banner.Enabled")
	}
	if (Banner{URL: "u", Link: "l"}).Href() != "u" || (Banner{Link: "l"}).Href() != "l" {
		t.Error("Banner.Href")
	}
	folders := []struct {
		s    Service
		want bool
	}{
		{Service{Category: "/ELK"}, true},
		{Service{URL: "https://x"}, false},
		{Service{URL: "https://x", Category: "/ELK"}, false},
		{Service{URL: "  ", Category: "/ELK"}, true},
		{Service{}, false},
	}
	for _, f := range folders {
		if f.s.IsFolder() != f.want {
			t.Errorf("IsFolder(%+v) = %v", f.s, !f.want)
		}
	}
	s := Story{Pages: []StoryPage{
		{DatePublished: FlexTime{time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)}},
		{},
		{DatePublished: FlexTime{time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)}},
	}}
	if got := s.Published(); !got.Equal(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("Published() = %v", got)
	}
	if !(Story{}).Published().IsZero() {
		t.Error("story without pages")
	}
	var g *GeoPoint
	if g.Valid() || g.Lng() != 0 || g.Lat() != 0 || (&GeoPoint{Coordinates: []float64{1}}).Valid() {
		t.Error("GeoPoint nil/short handling")
	}
	var ll *LatLng
	if ll.Valid() || (&LatLng{Lat: NewNum(1)}).Valid() {
		t.Error("LatLng validity")
	}
}

func TestFriendly(t *testing.T) {
	urlErr := &url.Error{Op: "Get", URL: "https://api.hseapp.ru/v2/stories", Err: errors.New("dial tcp: lookup api.hseapp.ru: no such host")}
	timeoutErr := &url.Error{Op: "Get", URL: "https://api.hseapp.ru/x", Err: errors.New("net/http: request canceled (Client.Timeout exceeded while awaiting headers)")}
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"login", fmt.Errorf("%w (HTTP 401: Bearer token is invalid.)", ErrLoginRequired), "Session expired — please sign in again"},
		{"login deep", fmt.Errorf("refresh: %w", fmt.Errorf("%w: refresh token rejected", ErrLoginRequired)), "Session expired — please sign in again"},
		{"rate limit", &RateLimitError{RetryAfter: 8 * time.Second}, "Rate limited: too many requests, try again in 8s"},
		{"rate limit rounding", &RateLimitError{RetryAfter: 2400 * time.Millisecond}, "Rate limited: too many requests, try again in 2s"},
		{"rate limit unknown", &RateLimitError{}, "Rate limited: too many requests, try again shortly"},
		{"404 message", &APIError{Status: 404, Name: "NotFound", Message: "users.get: User not found"}, "Not found (users.get: User not found)"},
		{"404 bare", &APIError{Status: 404}, "Not found"},
		{"500", &APIError{Status: 500, Message: "boom"}, "HSE server error (500) — try again later"},
		{"503 wrapped", fmt.Errorf("loading: %w", &APIError{Status: 503}), "HSE server error (503) — try again later"},
		{"400", &APIError{Status: 400, Name: "ValidationError"}, "HTTP 400: ValidationError"},
		{"403 message", &APIError{Status: 403, Name: "Forbidden", Message: "no access"}, "HTTP 403: no access"},
		{"decode", &DecodeError{Path: "/v2/stories", Err: errors.New("invalid character '<'")}, "Unexpected response format from /v2/stories"},
		{"network", &NetworkError{Err: urlErr}, "Can't reach HSE servers — check your connection"},
		{"network timeout", &NetworkError{Err: timeoutErr}, "Request timed out — check your connection"},
		{"network deadline", &NetworkError{Err: context.DeadlineExceeded}, "Request timed out — check your connection"},
		{"bare deadline", context.DeadlineExceeded, "Request timed out"},
		{"other", errors.New("something odd"), "something odd"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Friendly(c.err); got != c.want {
				t.Errorf("Friendly = %q, want %q", got, c.want)
			}
		})
	}
}

func TestErrorTypes(t *testing.T) {
	ne := &NetworkError{Err: errors.New("offline")}
	if ne.Error() != "network error: offline" || ne.Unwrap().Error() != "offline" {
		t.Errorf("NetworkError: %q", ne.Error())
	}
	de := &DecodeError{Path: "/x", Err: errors.New("bad")}
	if de.Error() != "unexpected response from /x: bad" || !errors.Is(de, de.Err) {
		t.Errorf("DecodeError: %q", de.Error())
	}
	if (&APIError{Status: 418}).Error() != "HTTP 418" || (&APIError{Status: 400, Name: "N"}).Error() != "HTTP 400: N" {
		t.Error("APIError.Error")
	}

	if !IsNetwork(ne) || !IsNetwork(fmt.Errorf("x: %w", ne)) {
		t.Error("IsNetwork(NetworkError)")
	}
	if !IsNetwork(&url.Error{Op: "Get", URL: "u", Err: errors.New("x")}) || !IsNetwork(&net.OpError{Op: "dial", Err: errors.New("refused")}) {
		t.Error("IsNetwork(url/net errors)")
	}
	if IsNetwork(nil) || IsNetwork(errors.New("x")) || IsNetwork(&APIError{Status: 500}) || IsNetwork(os.ErrNotExist) {
		t.Error("IsNetwork false positives")
	}
	if !strings.Contains(fmt.Sprint(ErrLoginRequired), "login required") {
		t.Error("ErrLoginRequired text")
	}
}
