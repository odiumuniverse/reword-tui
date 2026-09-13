package ui

import (
	"github.com/charmbracelet/lipgloss"
)

// Color roles: one color, one meaning. Check this table before adding
// any new colored element. Each role degrades to the named ANSI-16 color
// in parentheses, so the UI stays readable on 16-color terminals; under
// NO_COLOR lipgloss renders plain text.
var (
	// neutral: ~80% of the screen.
	fg    = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff")) // 15
	dim   = lipgloss.NewStyle().Foreground(lipgloss.Color("#7b8496")) // 7
	faint = lipgloss.NewStyle().Foreground(lipgloss.Color("#6c7086")) // 8
	bold  = lipgloss.NewStyle().Bold(true)

	// interactive, blue (12): cursor, active frame, footer key letters, brand.
	interactive = lipgloss.NewStyle().Foreground(lipgloss.Color("#5ea1ff"))
	sel         = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#5ea1ff"))
	abox        = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("#5ea1ff")).Padding(0, 2)

	// content, cyan (14): native translation, revealed answer.
	content = lipgloss.NewStyle().Foreground(lipgloss.Color("#5ecfd6"))

	// ok, green (10): correct answer, clean sync, mastered, "on".
	ok = lipgloss.NewStyle().Foreground(lipgloss.Color("#5eff8a"))

	// bad, red (9): wrong answer, dirty sync, errors.
	bad = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff6e5e"))

	// attn, yellow (11): due counts, confirmations, stale, untracked, unset goal.
	attn = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffd479"))
	ybox = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("#ffd479")).Padding(0, 2)

	// prog, violet (5) to pink (13): word stages, week bars and dots,
	// goal, streak, session progress.
	progLo = lipgloss.NewStyle().Foreground(lipgloss.Color("#b39ddb"))
	prog   = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff7eb6"))
	progHi = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ff7eb6"))

	box = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("#6c7086")).Padding(0, 2)
)

// stageRamp maps an SRS step S0..S7 to its maturation glyph and style.
// Glyph shape duplicates color, so stages read without color too.
var stageRamp = [8]struct {
	glyph string
	style lipgloss.Style
}{
	{"○", faint},  // S0 new
	{"◔", progLo}, // S1
	{"◔", progLo}, // S2
	{"◑", prog},   // S3
	{"◑", prog},   // S4
	{"◕", prog},   // S5
	{"◕", progHi}, // S6
	{"✦", ok},     // S7 mastered
}

// stageMark renders the maturation glyph for an SRS step, clamped to S0..S7.
func stageMark(step int64) string {
	if step < 0 {
		step = 0
	}
	if step > 7 {
		step = 7
	}
	s := stageRamp[step]
	return s.style.Render(s.glyph)
}

// wordStage returns the maturation glyph for the stronger side of a word.
func wordStage(recStep, repStep int64) string {
	return stageMark(max(recStep, repStep))
}
