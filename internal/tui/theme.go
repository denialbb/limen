package tui

import "github.com/charmbracelet/lipgloss"

// Theme holds all visual constants for the TUI. Every magic number in the
// rendering code must come from here.
type Theme struct {
	// Header constants.
	HeaderBgColor      string
	HeaderFgColor      string
	HeaderPadH         int
	HeaderFieldColor   string
	HeaderStateColor   string
	HeaderCountColor   string

	// Tab constants.
	TabActiveBgColor   string
	TabActiveFgColor   string
	TabInactiveColor   string
	TabPadH            int
	TabPadBetween      int
	TabBoundaryPad     int

	// Separator constants.
	SeparatorColor     string
	SeparatorRune      string
	SeparatorPadH      int
	SeparatorPadV      int

	// Content padding.
	ContentPadLeft     int
}

func NewTheme() *Theme {
	return &Theme{
		HeaderBgColor:    "63",
		HeaderFgColor:    "15",
		HeaderPadH:       1,
		HeaderFieldColor: "252",
		HeaderStateColor: "213",
		HeaderCountColor: "250",

		TabActiveBgColor:   "63",
		TabActiveFgColor:   "15",
		TabInactiveColor:   "245",
		TabPadH:            1,
		TabPadBetween:      3,
		TabBoundaryPad:     2,

		SeparatorColor:   "240",
		SeparatorRune:    "─",
		SeparatorPadH:    0,
		SeparatorPadV:    1,

		ContentPadLeft: 0,
	}
}

// HeaderStyles builds the lipgloss styles for the header using the theme values.
func (t *Theme) HeaderStyles() (brand, field, state, count, container lipgloss.Style) {
	brand = lipgloss.NewStyle().
		Bold(true).
		Background(lipgloss.Color(t.HeaderBgColor)).
		Foreground(lipgloss.Color(t.HeaderFgColor)).
		Padding(0, t.HeaderPadH)

	field = lipgloss.NewStyle().
		Foreground(lipgloss.Color(t.HeaderFieldColor))

	state = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(t.HeaderStateColor))

	count = lipgloss.NewStyle().
		Foreground(lipgloss.Color(t.HeaderCountColor))

	container = lipgloss.NewStyle().
		Background(lipgloss.Color(t.HeaderBgColor))

	return
}

// SeparatorStyle returns a style for rendering the separator line.
func (t *Theme) SeparatorStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(t.SeparatorColor))
}

// TabStyles builds the lipgloss styles for the tab strip.
func (t *Theme) TabStyles() (active, inactive lipgloss.Style) {
	active = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(t.TabActiveFgColor)).
		Background(lipgloss.Color(t.TabActiveBgColor)).
		Padding(0, t.TabPadH)

	inactive = lipgloss.NewStyle().
		Foreground(lipgloss.Color(t.TabInactiveColor)).
		Padding(0, t.TabPadH)

	return
}

// package-level theme singleton used by header, tab, and model.
var theme = NewTheme()