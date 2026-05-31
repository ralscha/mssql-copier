package copier

import "charm.land/lipgloss/v2"

// Color palette.
var (
	colorPrimary   = lipgloss.Color("#7C3AED") // purple
	colorSecondary = lipgloss.Color("#EC4899") // pink
	colorAccent    = lipgloss.Color("#10B981") // green
	colorMuted     = lipgloss.Color("#6B7280") // gray
	colorSubtle    = lipgloss.Color("#374151") // dark gray
)

// Shared styles used across TUI screens.
var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary).
			MarginBottom(1)

	statusStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	statusOKStyle = lipgloss.NewStyle().
			Foreground(colorAccent)

	statusErrStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#EF4444")) // red

	helpStyle = lipgloss.NewStyle().
			Foreground(colorMuted).
			Italic(true).
			MarginTop(1)

	sectionHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorSecondary).
				MarginTop(1).
				MarginBottom(1)

	labelStyle = lipgloss.NewStyle().
			Foreground(colorPrimary).
			Bold(true).
			Width(22)

	activeLabelStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#A78BFA")). // light purple
				Bold(true).
				Width(22)

	buttonStyle = lipgloss.NewStyle().
			Foreground(lipgloss.White).
			Background(colorPrimary).
			Padding(0, 3).
			MarginTop(1)

	activeButtonStyle = lipgloss.NewStyle().
				Foreground(lipgloss.White).
				Background(colorSecondary).
				Padding(0, 3).
				MarginTop(1).
				Bold(true)

	spinnerStyle = lipgloss.NewStyle().
			Foreground(colorPrimary)
)
