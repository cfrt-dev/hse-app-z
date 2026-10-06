package ui

import (
	"errors"
	"testing"
	"time"

	"hse-app-z/internal/api"
)

func withLang(t *testing.T, lang string) {
	t.Helper()
	prev := Lang()
	SetLang(lang)
	t.Cleanup(func() { SetLang(prev) })
}

func TestTrAndPlural(t *testing.T) {
	withLang(t, "en")
	if Tr("Grades", "Оценки") != "Grades" || Count(1, "lesson", "lessons", "пара", "пары", "пар") != "1 lesson" || Count(3, "lesson", "lessons", "пара", "пары", "пар") != "3 lessons" {
		t.Error("english")
	}
	SetLang("ru")
	if !RU() || Tr("Grades", "Оценки") != "Оценки" {
		t.Error("russian")
	}
	for n, want := range map[int]string{0: "пар", 1: "пара", 2: "пары", 4: "пары", 5: "пар", 11: "пар", 12: "пар", 14: "пар", 21: "пара", 22: "пары", 25: "пар", 101: "пара", 111: "пар"} {
		if got := PluralRU(n, "пара", "пары", "пар"); got != want {
			t.Errorf("PluralRU(%d) = %s, want %s", n, got, want)
		}
	}
	SetLang("xx")
	if Lang() != "en" {
		t.Error("unknown languages fall back to en")
	}
}

func TestDates(t *testing.T) {
	d := time.Date(2026, 5, 13, 19, 15, 0, 0, time.UTC) // Wednesday
	withLang(t, "en")
	for got, want := range map[string]string{
		FmtDay(d): "Wed 13 May", FmtDayLong(d): "Wednesday, 13 May", FmtDate(d): "Wed 13 May 2026",
		FmtDateTime(d): "13 May 2026 19:15", FmtDateLong(d): "13 May 2026", FmtDayMonthLong(d): "13 May",
	} {
		if got != want {
			t.Errorf("en: got %q want %q", got, want)
		}
	}
	SetLang("ru")
	for got, want := range map[string]string{
		FmtDay(d): "Ср 13 мая", FmtDayLong(d): "Среда, 13 мая", FmtDate(d): "Ср 13 мая 2026",
		FmtDateTime(d): "13 мая 2026 19:15", FmtDateLong(d): "13 мая 2026", MonthLong(d.Month()): "май",
		WeekdayShort(time.Sunday): "вс",
	} {
		if got != want {
			t.Errorf("ru: got %q want %q", got, want)
		}
	}
}

func TestOpenLabelAndErrors(t *testing.T) {
	at := time.Date(2026, 10, 5, 22, 0, 0, 0, api.Moscow) // Monday
	std := []api.OpeningHours{{DayOfWeek: "monday", IsOpen: true, StartTime: "09:00", EndTime: "21:00"}, {DayOfWeek: "tuesday", IsOpen: true, StartTime: "09:00", EndTime: "21:00"}}
	withLang(t, "ru")
	if got := OpenLabel(api.Status(std, nil, at)); got != "закрыто · откроется завтра 09:00" {
		t.Errorf("got %q", got)
	}
	if got := OpenLabel(api.Status(std, nil, at.Add(-10*time.Hour))); got != "открыто · до 21:00" {
		t.Errorf("got %q", got)
	}
	if got := OpenLabel(api.OpenStatus{}); got != "часы неизвестны" {
		t.Errorf("got %q", got)
	}
	if got := CampusName("CAMPUS_SPB"); got != "Санкт-Петербург" {
		t.Errorf("got %q", got)
	}
	if got := SanitaryDayText("SANITARY_DAY_FIRST_FRIDAY"); got != "санитарный день — в первую пятницу месяца" {
		t.Errorf("got %q", got)
	}
	if got := SanitaryDayText("SANITARY_DAY_LAST_MONDAY"); got != "санитарный день — в последний понедельник месяца" {
		t.Errorf("got %q", got)
	}
	if got := SanitaryDayText("SANITARY_DAY_THIRD_SUNDAY"); got != "санитарный день — в третье воскресенье месяца" {
		t.Errorf("got %q", got)
	}
	if got := SanitaryDayText("SANITARY_DAY_SECOND_TUESDAY"); got != "санитарный день — во второй вторник месяца" {
		t.Errorf("got %q", got)
	}
	if got := ErrText(&api.NetworkError{Err: errors.New("dial tcp")}); got != "Нет связи с серверами ВШЭ — проверьте подключение" {
		t.Errorf("got %q", got)
	}
	if got := ErrText(api.ErrLoginRequired); got != "Сессия истекла — войдите снова" {
		t.Errorf("got %q", got)
	}
	SetLang("en")
	if got := ErrText(api.ErrLoginRequired); got != api.Friendly(api.ErrLoginRequired) {
		t.Errorf("en must match api.Friendly: %q", got)
	}
}

func TestBadgesFollowLanguage(t *testing.T) {
	withLang(t, "ru")
	if KindShort("Seminar") != "СЕМ" || KindShort("Лекция") != "ЛЕК" || Width(KindShort("Practical sessions")) != 3 {
		t.Error("lesson kinds")
	}
	if Clean(ansiStrip(TypeBadge("STUDENT"))) != "СТУ" || ansiStrip(TypeBadge("AUDITORIUM")) != "АУД" {
		t.Error("search badges")
	}
	SetLang("en")
	if KindShort("Seminar") != "SEM" || ansiStrip(TypeBadge("STAFF")) != "STF" {
		t.Error("english badges")
	}
}

func ansiStrip(s string) string { return Clean(s) }
