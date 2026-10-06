package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Palette. Adaptive colors keep the UI readable on light and dark terminals.
var (
	ColorAccent = lipgloss.AdaptiveColor{Light: "#1F4FD8", Dark: "#7AA7FF"}
	ColorDim    = lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#8B949E"}
	ColorFaint  = lipgloss.AdaptiveColor{Light: "#C9CED6", Dark: "#3A3F47"}
	ColorOK     = lipgloss.AdaptiveColor{Light: "#1A7F37", Dark: "#56D364"}
	ColorWarn   = lipgloss.AdaptiveColor{Light: "#9A6700", Dark: "#E3B341"}
	ColorErr    = lipgloss.AdaptiveColor{Light: "#CF222E", Dark: "#FF7B72"}
	ColorPurple = lipgloss.AdaptiveColor{Light: "#8250DF", Dark: "#D2A8FF"}
	ColorCyan   = lipgloss.AdaptiveColor{Light: "#0E7490", Dark: "#76E3EA"}
	ColorSelBg  = lipgloss.AdaptiveColor{Light: "#E3EBFF", Dark: "#1F2B44"}
)

var (
	StyleBold    = lipgloss.NewStyle().Bold(true)
	StyleTitle   = lipgloss.NewStyle().Bold(true)
	StyleDim     = lipgloss.NewStyle().Foreground(ColorDim)
	StyleFaint   = lipgloss.NewStyle().Foreground(ColorFaint)
	StyleAccent  = lipgloss.NewStyle().Foreground(ColorAccent)
	StyleOK      = lipgloss.NewStyle().Foreground(ColorOK)
	StyleWarn    = lipgloss.NewStyle().Foreground(ColorWarn)
	StyleErr     = lipgloss.NewStyle().Foreground(ColorErr)
	StyleSection = lipgloss.NewStyle().Bold(true).Foreground(ColorAccent)
	// StyleGroupHeader is for non-selectable header rows inside lists
	// (day names, modules, campuses).
	StyleGroupHeader = lipgloss.NewStyle().Bold(true).Foreground(ColorDim)
	StyleKey         = lipgloss.NewStyle().Bold(true).Foreground(ColorAccent)
	StyleSelected    = lipgloss.NewStyle().Bold(true)
	StyleCursor      = lipgloss.NewStyle().Foreground(ColorAccent).Bold(true)
	StyleSep         = lipgloss.NewStyle().Foreground(ColorFaint)
	StyleLink        = lipgloss.NewStyle().Foreground(ColorAccent).Underline(true)
)

// GradeStyle colors a 10-point grade: 8–10 excellent, 6–7 good,
// 4–5 satisfactory, 0–3 fail.
func GradeStyle(v float64) lipgloss.Style {
	switch {
	case v >= 8:
		return StyleOK
	case v >= 6:
		return StyleAccent
	case v >= 4:
		return StyleWarn
	default:
		return StyleErr
	}
}

// KindStyle colors a lesson type.
func KindStyle(kind string) lipgloss.Style {
	k := strings.ToLower(kind)
	switch {
	case strings.Contains(k, "лекц") || strings.Contains(k, "lecture"):
		return lipgloss.NewStyle().Foreground(ColorAccent)
	case strings.Contains(k, "семин") || strings.Contains(k, "seminar"):
		return lipgloss.NewStyle().Foreground(ColorOK)
	case strings.Contains(k, "практ") || strings.Contains(k, "practic") || strings.Contains(k, "лаб") || strings.Contains(k, "lab"):
		return lipgloss.NewStyle().Foreground(ColorCyan)
	case strings.Contains(k, "экзам") || strings.Contains(k, "exam") || strings.Contains(k, "зач") || strings.Contains(k, "test") || strings.Contains(k, "контрол") || strings.Contains(k, "assessment"):
		return lipgloss.NewStyle().Foreground(ColorErr).Bold(true)
	case strings.Contains(k, "консул") || strings.Contains(k, "consult"):
		return lipgloss.NewStyle().Foreground(ColorPurple)
	}
	return StyleDim
}

// KindShort abbreviates a lesson type for list rows ("Lecture" → "LEC").
func KindShort(kind string) string {
	k := strings.ToLower(kind)
	switch {
	case strings.Contains(k, "лекц") || strings.Contains(k, "lecture"):
		return Tr("LEC", "ЛЕК")
	case strings.Contains(k, "семин") || strings.Contains(k, "seminar"):
		return Tr("SEM", "СЕМ")
	case strings.Contains(k, "практ") || strings.Contains(k, "practic"):
		return Tr("PRA", "ПРА")
	case strings.Contains(k, "лаб") || strings.Contains(k, "lab"):
		return Tr("LAB", "ЛАБ")
	case strings.Contains(k, "экзам") || strings.Contains(k, "exam"):
		return Tr("EXM", "ЭКЗ")
	case strings.Contains(k, "зач") || strings.Contains(k, "test"):
		return Tr("TST", "ЗАЧ")
	case strings.Contains(k, "консул") || strings.Contains(k, "consult"):
		return Tr("CON", "КОН")
	case strings.Contains(k, "контрол") || strings.Contains(k, "assessment"):
		return Tr("CTL", "КТР")
	}
	// Unknown kind: its first letters, always exactly three cells (rows
	// are aligned on it) and sanitised (it is raw API text).
	s := ansi.Truncate(strings.ToUpper(Clean(kind)), 3, "")
	return s + strings.Repeat(" ", max(0, 3-ansi.StringWidth(s)))
}

// TypeBadge abbreviates a search hit type.
func TypeBadge(t string) string {
	switch strings.ToUpper(t) {
	case "STUDENT":
		return StyleOK.Render(Tr("STU", "СТУ"))
	case "STAFF":
		return StyleAccent.Render(Tr("STF", "СОТ"))
	case "GROUP":
		return lipgloss.NewStyle().Foreground(ColorPurple).Render(Tr("GRP", "ГРП"))
	case "AUDITORIUM":
		return lipgloss.NewStyle().Foreground(ColorCyan).Render(Tr("ROOM", "АУД"))
	}
	return StyleDim.Render("•")
}
