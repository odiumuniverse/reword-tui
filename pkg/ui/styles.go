package ui

import (
	"github.com/charmbracelet/lipgloss"
)

// Color roles: one color, one meaning. Check this table before adding
// any new colored element. Each role pins exact values for truecolor,
// 256-color and 16-color terminals (ANSI index in parentheses), so the UI
// keeps its roles on 16-color terminals instead of nearest-match guessing.
// Under NO_COLOR lipgloss renders plain text.
var (
	// neutral: ~80% of the screen.
	fg    = lipgloss.NewStyle().Foreground(lipgloss.CompleteColor{TrueColor: "#ffffff", ANSI256: "15", ANSI: "15"})
	dim   = lipgloss.NewStyle().Foreground(lipgloss.CompleteColor{TrueColor: "#7b8496", ANSI256: "243", ANSI: "7"})
	faint = lipgloss.NewStyle().Foreground(lipgloss.CompleteColor{TrueColor: "#6c7086", ANSI256: "242", ANSI: "8"})
	bold  = lipgloss.NewStyle().Bold(true)

	// interactive, blue (12): cursor, active frame, footer key letters, brand.
	interactive = lipgloss.NewStyle().Foreground(lipgloss.CompleteColor{TrueColor: "#5ea1ff", ANSI256: "75", ANSI: "12"})
	sel         = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.CompleteColor{TrueColor: "#5ea1ff", ANSI256: "75", ANSI: "12"})
	abox        = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.CompleteColor{TrueColor: "#5ea1ff", ANSI256: "75", ANSI: "12"}).Padding(0, 2)

	// content, cyan (14): native translation, revealed answer.
	content = lipgloss.NewStyle().Foreground(lipgloss.CompleteColor{TrueColor: "#5ecfd6", ANSI256: "80", ANSI: "14"})

	// okStyle, green (10): correct answer, clean sync, mastered, "on".
	okStyle = lipgloss.NewStyle().Foreground(lipgloss.CompleteColor{TrueColor: "#5eff8a", ANSI256: "84", ANSI: "10"})

	// badStyle, red (9): wrong answer, dirty sync, errors.
	badStyle = lipgloss.NewStyle().Foreground(lipgloss.CompleteColor{TrueColor: "#ff6e5e", ANSI256: "203", ANSI: "9"})

	// attn, yellow (11): due counts, confirmations, stale, untracked, unset goal, warnings,
	// week activity dots and streak.
	attn = lipgloss.NewStyle().Foreground(lipgloss.CompleteColor{TrueColor: "#ffd479", ANSI256: "221", ANSI: "11"})
	ybox = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.CompleteColor{TrueColor: "#ffd479", ANSI256: "221", ANSI: "11"}).Padding(0, 2)

	// prog, violet (5) to pink (13): word stages, week bars, goal, session progress.
	progLo = lipgloss.NewStyle().Foreground(lipgloss.CompleteColor{TrueColor: "#b39ddb", ANSI256: "140", ANSI: "5"})
	prog   = lipgloss.NewStyle().Foreground(lipgloss.CompleteColor{TrueColor: "#ff7eb6", ANSI256: "212", ANSI: "13"})
	progHi = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.CompleteColor{TrueColor: "#ff7eb6", ANSI256: "212", ANSI: "13"})

	box = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.CompleteColor{TrueColor: "#6c7086", ANSI256: "242", ANSI: "8"}).Padding(0, 2)
)

// roleStyles maps a role name to the style carrying it, for tests that pin
// the 16-color degradation of every role.
var roleStyles = []struct {
	name  string
	style lipgloss.Style
	want  string // ANSI-16 index
}{
	{"fg", fg, "15"},
	{"dim", dim, "7"},
	{"faint", faint, "8"},
	{"interactive", interactive, "12"},
	{"content", content, "14"},
	{"okStyle", okStyle, "10"},
	{"badStyle", badStyle, "9"},
	{"attn", attn, "11"},
	{"progLo", progLo, "5"},
	{"prog", prog, "13"},
}

// stageRamp maps an SRS step S0..S7 to its maturation glyph and style.
// Glyph shape duplicates color, so stages read without color too.
var stageRamp = [8]struct {
	glyph string
	style lipgloss.Style
}{
	{"○", faint},   // S0 new
	{"◔", progLo},  // S1
	{"◔", progLo},  // S2
	{"◑", prog},    // S3
	{"◑", prog},    // S4
	{"◕", prog},    // S5
	{"◕", progHi},  // S6
	{"✦", okStyle}, // S7 mastered
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
