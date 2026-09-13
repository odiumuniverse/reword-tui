package ui

import (
	"github.com/charmbracelet/lipgloss"
)

var (
	fg     = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff"))
	dim    = lipgloss.NewStyle().Foreground(lipgloss.Color("#7b8496"))
	faint  = lipgloss.NewStyle().Foreground(lipgloss.Color("#4a5160"))
	accent = lipgloss.NewStyle().Foreground(lipgloss.Color("#5ea1ff"))
	pink   = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff7eb6"))
	yellow = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffd479"))
	green  = lipgloss.NewStyle().Foreground(lipgloss.Color("#5eff8a"))
	red    = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff6e5e"))
	orange = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffbd5e"))
	bold   = lipgloss.NewStyle().Bold(true)
	sel    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#5ea1ff"))
	box    = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("#4a5160")).Padding(0, 2)
	abox   = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("#5ea1ff")).Padding(0, 2)
	ybox   = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("#ffd479")).Padding(0, 2)
)

func width() int { return 76 }
