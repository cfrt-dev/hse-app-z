package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"hse-app-z/internal/api"
)

// Interface language. Pages build their text at View time with Tr, so a
// language switch (L) takes effect on the next frame everywhere.

var curLang atomic.Value // "en" | "ru"

// SetLang selects the interface language ("en" or "ru").
func SetLang(lang string) {
	if lang != "ru" {
		lang = "en"
	}
	curLang.Store(lang)
}

// Lang is the interface language ("en" by default).
func Lang() string {
	if l, ok := curLang.Load().(string); ok {
		return l
	}
	return "en"
}

// RU reports whether the interface is Russian.
func RU() bool { return Lang() == "ru" }

// Tr picks the string for the current language.
func Tr(en, ru string) string {
	if RU() {
		return ru
	}
	return en
}

// Trf is Tr followed by fmt.Sprintf.
func Trf(en, ru string, args ...any) string { return fmt.Sprintf(Tr(en, ru), args...) }

// PluralRU picks the Russian form for n: one (1, 21…), few (2–4, 22–24…),
// many (0, 5–20, 25…).
func PluralRU(n int, one, few, many string) string {
	if n < 0 {
		n = -n
	}
	switch {
	case n%10 == 1 && n%100 != 11:
		return one
	case n%10 >= 2 && n%10 <= 4 && (n%100 < 12 || n%100 > 14):
		return few
	}
	return many
}

// Count renders "3 lessons" / "3 урока" in the current language.
func Count(n int, enOne, enMany, ruOne, ruFew, ruMany string) string {
	if RU() {
		return fmt.Sprintf("%d %s", n, PluralRU(n, ruOne, ruFew, ruMany))
	}
	if n == 1 {
		return fmt.Sprintf("%d %s", n, enOne)
	}
	return fmt.Sprintf("%d %s", n, enMany)
}

// ------------------------------------------------------------------ dates

var (
	ruWeekShort = [7]string{"вс", "пн", "вт", "ср", "чт", "пт", "сб"}
	ruWeekLong  = [7]string{"воскресенье", "понедельник", "вторник", "среда", "четверг", "пятница", "суббота"}
	ruMonShort  = [13]string{"", "янв", "фев", "мар", "апр", "мая", "июн", "июл", "авг", "сен", "окт", "ноя", "дек"}
	ruMonLong   = [13]string{"", "январь", "февраль", "март", "апрель", "май", "июнь", "июль", "август", "сентябрь", "октябрь", "ноябрь", "декабрь"}
	ruMonGen    = [13]string{"", "января", "февраля", "марта", "апреля", "мая", "июня", "июля", "августа", "сентября", "октября", "ноября", "декабря"}
)

// WeekdayShort: "Mon" / "пн".
func WeekdayShort(d time.Weekday) string {
	if RU() {
		return ruWeekShort[d]
	}
	return d.String()[:3]
}

// WeekdayLong: "Monday" / "понедельник".
func WeekdayLong(d time.Weekday) string {
	if RU() {
		return ruWeekLong[d]
	}
	return d.String()
}

// MonthShort: "Oct" / "окт" (Russian uses the genitive for "мая").
func MonthShort(m time.Month) string {
	if RU() {
		return ruMonShort[m]
	}
	return m.String()[:3]
}

// MonthLong: "October" / "октябрь" (standalone form).
func MonthLong(m time.Month) string {
	if RU() {
		return ruMonLong[m]
	}
	return m.String()
}

// MonthDay: "October" / "октября" (form used after a day number).
func MonthDay(m time.Month) string {
	if RU() {
		return ruMonGen[m]
	}
	return m.String()
}

func capFirst(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	return strings.ToUpper(string(r[:1])) + string(r[1:])
}

// FmtDay: "Mon 12 Oct" / "Пн 12 окт" (list headers).
func FmtDay(t time.Time) string {
	return capFirst(WeekdayShort(t.Weekday())) + " " + FmtDayMonth(t)
}

// FmtDayMonth: "12 Oct" / "12 окт".
func FmtDayMonth(t time.Time) string {
	return fmt.Sprintf("%d %s", t.Day(), MonthShort(t.Month()))
}

// FmtDayLong: "Tuesday, 13 October" / "Вторник, 13 октября".
func FmtDayLong(t time.Time) string {
	return fmt.Sprintf("%s, %d %s", capFirst(WeekdayLong(t.Weekday())), t.Day(), MonthDay(t.Month()))
}

// FmtDate: "Mon 2 Jan 2006" / "Пн 2 янв 2006".
func FmtDate(t time.Time) string {
	return fmt.Sprintf("%s %d %s %d", capFirst(WeekdayShort(t.Weekday())), t.Day(), MonthShort(t.Month()), t.Year())
}

// FmtDateShort: "2 Jan 2006" / "2 янв 2006".
func FmtDateShort(t time.Time) string {
	return fmt.Sprintf("%d %s %d", t.Day(), MonthShort(t.Month()), t.Year())
}

// FmtDateLong: "2 January 2006" / "2 января 2006".
func FmtDateLong(t time.Time) string {
	return fmt.Sprintf("%d %s %d", t.Day(), MonthDay(t.Month()), t.Year())
}

// FmtDateTime: "25 May 2026 19:15" / "25 мая 2026 19:15".
func FmtDateTime(t time.Time) string {
	return FmtDateShort(t) + " " + t.Format("15:04")
}

// FmtDayMonthLong: "13 October" / "13 октября" (birthdays).
func FmtDayMonthLong(t time.Time) string {
	return fmt.Sprintf("%d %s", t.Day(), MonthDay(t.Month()))
}

// ------------------------------------------------------------ data labels

// CampusName localises a campus code (CAMPUS_MOS → Moscow / Москва).
func CampusName(code string) string {
	if !RU() {
		return api.CampusName(code)
	}
	switch strings.ToUpper(code) {
	case "CAMPUS_MOS":
		return "Москва"
	case "CAMPUS_SPB":
		return "Санкт-Петербург"
	case "CAMPUS_NNOV":
		return "Нижний Новгород"
	case "CAMPUS_PERM":
		return "Пермь"
	case "":
		return "Другое"
	}
	return api.CampusName(code)
}

// OpenLabel localises an api.OpenStatus ("open · until 21:00" /
// "открыто · до 21:00").
func OpenLabel(st api.OpenStatus) string {
	if !st.Known {
		return Tr("hours n/a", "часы неизвестны")
	}
	if !RU() {
		return st.Label
	}
	switch st.Kind {
	case api.OpenUntil:
		return "открыто · до " + st.Time
	case api.OpenSince:
		return "открыто · с " + st.Time
	case api.OpenAllDay:
		return "открыто сегодня"
	case api.ClosedOpens:
		var when string
		switch {
		case st.OpensInDays == 1:
			when = "завтра"
		case st.OpensInDays >= 7:
			when = ruWeekShort[st.OpensDay] + " " + FmtDayMonth(st.OpensDate)
		case st.OpensInDays > 1:
			when = ruWeekShort[st.OpensDay]
		}
		when = strings.TrimSpace(when + " " + st.OpensAt)
		if st.ClosedToday {
			return "сегодня закрыто · откроется " + when
		}
		if when == "" {
			return "закрыто"
		}
		return "закрыто · откроется " + when
	}
	return "закрыто"
}

// SanitaryDayText localises library cleaning-day codes.
func SanitaryDayText(code string) string {
	if !RU() {
		return api.SanitaryDayText(code)
	}
	c := strings.ToUpper(strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(code)), "SANITARY_DAY_"))
	parts := strings.Split(c, "_")
	if len(parts) == 2 {
		ord := map[string]string{"FIRST": "первую", "SECOND": "вторую", "THIRD": "третью", "FOURTH": "четвёртую", "LAST": "последнюю"}[parts[0]]
		ordM := map[string]string{"FIRST": "первый", "SECOND": "второй", "THIRD": "третий", "FOURTH": "четвёртый", "LAST": "последний"}[parts[0]]
		if d, ok := api.ParseWeekday(parts[1]); ok && ord != "" {
			// Russian weekday gender: вторник/понедельник/четверг are masculine.
			acc := map[time.Weekday]string{time.Monday: "понедельник", time.Tuesday: "вторник", time.Wednesday: "среду", time.Thursday: "четверг", time.Friday: "пятницу", time.Saturday: "субботу", time.Sunday: "воскресенье"}[d]
			o := ord
			if d == time.Monday || d == time.Tuesday || d == time.Thursday {
				o = ordM
			} else if d == time.Sunday {
				o = map[string]string{"FIRST": "первое", "SECOND": "второе", "THIRD": "третье", "FOURTH": "четвёртое", "LAST": "последнее"}[parts[0]]
			}
			prep := "в"
			if strings.HasPrefix(o, "вт") {
				prep = "во" // во второй вторник
			}
			return "санитарный день — " + prep + " " + o + " " + acc + " месяца"
		}
	}
	return api.SanitaryDayText(code)
}

// ErrText is api.Friendly in the interface language, sanitised for display
// (server messages may contain newlines or escape codes).
func ErrText(err error) string { return Clean(errText(err)) }

func errText(err error) string {
	if err == nil {
		return ""
	}
	if !RU() {
		return api.Friendly(err)
	}
	var (
		ae *api.APIError
		re *api.RateLimitError
		de *api.DecodeError
		ne *api.NetworkError
	)
	switch {
	case errors.Is(err, api.ErrLoginRequired):
		return "Сессия истекла — войдите снова"
	case errors.As(err, &re):
		if re.RetryAfter > 0 {
			return fmt.Sprintf("Слишком много запросов, повторите через %d с", int(re.RetryAfter.Round(time.Second)/time.Second))
		}
		return "Слишком много запросов, повторите чуть позже"
	case errors.As(err, &ae):
		switch {
		case ae.Status == 404:
			if ae.Message != "" {
				return "Не найдено (" + ae.Message + ")"
			}
			return "Не найдено"
		case ae.Status >= 500:
			return fmt.Sprintf("Ошибка сервера НИУ ВШЭ (%d) — попробуйте позже", ae.Status)
		}
		return ae.Error()
	case errors.As(err, &de):
		return "Неожиданный ответ сервера: " + de.Path
	case errors.Is(err, context.DeadlineExceeded):
		return "Превышено время ожидания — проверьте подключение"
	case errors.As(err, &ne):
		if strings.Contains(ne.Err.Error(), "Client.Timeout") || strings.Contains(ne.Err.Error(), "deadline exceeded") {
			return "Превышено время ожидания — проверьте подключение"
		}
		return "Нет связи с серверами ВШЭ — проверьте подключение"
	}
	return api.Friendly(err)
}
