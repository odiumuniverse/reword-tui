package ui

import (
	"github.com/charmbracelet/lipgloss"
)

var (
	fg    = lipgloss.NewStyle().Foreground(lipgloss.CompleteColor{TrueColor: "#ffffff", ANSI256: "15", ANSI: "15"})
	dim   = lipgloss.NewStyle().Foreground(lipgloss.CompleteColor{TrueColor: "#7b8496", ANSI256: "243", ANSI: "7"})
	faint = lipgloss.NewStyle().Foreground(lipgloss.CompleteColor{TrueColor: "#6c7086", ANSI256: "242", ANSI: "8"})
	bold  = lipgloss.NewStyle().Bold(true)

	interactive = lipgloss.NewStyle().Foreground(lipgloss.CompleteColor{TrueColor: "#5ea1ff", ANSI256: "75", ANSI: "12"})
	sel         = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.CompleteColor{TrueColor: "#5ea1ff", ANSI256: "75", ANSI: "12"})
	abox        = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.CompleteColor{TrueColor: "#5ea1ff", ANSI256: "75", ANSI: "12"}).Padding(0, 2)

	content = lipgloss.NewStyle().Foreground(lipgloss.CompleteColor{TrueColor: "#5ecfd6", ANSI256: "80", ANSI: "14"})

	okStyle = lipgloss.NewStyle().Foreground(lipgloss.CompleteColor{TrueColor: "#5eff8a", ANSI256: "84", ANSI: "10"})

	badStyle = lipgloss.NewStyle().Foreground(lipgloss.CompleteColor{TrueColor: "#ff6e5e", ANSI256: "203", ANSI: "9"})

	attn = lipgloss.NewStyle().Foreground(lipgloss.CompleteColor{TrueColor: "#ffd479", ANSI256: "221", ANSI: "11"})
	ybox = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.CompleteColor{TrueColor: "#ffd479", ANSI256: "221", ANSI: "11"}).Padding(0, 2)

	progLo = lipgloss.NewStyle().Foreground(lipgloss.CompleteColor{TrueColor: "#b39ddb", ANSI256: "140", ANSI: "5"})
	prog   = lipgloss.NewStyle().Foreground(lipgloss.CompleteColor{TrueColor: "#ff7eb6", ANSI256: "212", ANSI: "13"})
	progHi = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.CompleteColor{TrueColor: "#ff7eb6", ANSI256: "212", ANSI: "13"})

	box = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.CompleteColor{TrueColor: "#6c7086", ANSI256: "242", ANSI: "8"}).Padding(0, 2)
)

var roleStyles = []struct {
	name  string
	style lipgloss.Style
	want  string
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

var stageRamp = [8]struct {
	glyph string
	style lipgloss.Style
}{
	{"○", faint},
	{"◔", progLo},
	{"◔", progLo},
	{"◑", prog},
	{"◑", prog},
	{"◕", prog},
	{"◕", progHi},
	{"✦", okStyle},
}

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

func wordStage(recStep, repStep int64) string {
	return stageMark(max(recStep, repStep))
}
