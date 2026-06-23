package tui

import "github.com/charmbracelet/lipgloss"

// Theme holds all visual constants for the TUI. Every magic number in the
// rendering code must come from here.
type Theme struct {
	// Header constants.
	HeaderBgColor    string
	HeaderFgColor    string
	HeaderPadH       int
	HeaderFieldColor string
	HeaderStateColor string
	HeaderCountColor string

	// Tab constants.
	TabActiveBgColor string
	TabActiveFgColor string
	TabInactiveColor string
	TabPadH          int
	TabBoundaryPad   int

	// Separator constants.
	SeparatorColor string
	SeparatorRune  string
	SeparatorPadV  int
}

func NewTheme() *Theme {
	return &Theme{
		HeaderBgColor:    "63",
		HeaderFgColor:    "15",
		HeaderPadH:       1,
		HeaderFieldColor: "252",
		HeaderStateColor: "213",
		HeaderCountColor: "250",

		TabActiveBgColor: "63",
		TabActiveFgColor: "15",
		TabInactiveColor: "245",
		TabPadH:          1,
		TabBoundaryPad:   2,

		SeparatorColor: "240",
		SeparatorRune:  "─",
		SeparatorPadV:  0,
	}
}

// HeaderStyles builds the lipgloss styles for the header bar. Every segment
// carries the bar's Background so that nested ANSI resets from inner Render
// calls do not break the full-width background fill. The bar style has Width
// set so lipgloss pads any shortfall with bg-styled whitespace.
//
// The pattern follows lipgloss's own canonical status-bar example:
//   - bar:    outer wrapper, Background + Width, fills any edge shortfall.
//   - brand:  bold, high-contrast fg, bg from base, Padding for bg-covered spacing.
//   - field:  normal fg, bg from base, Padding for bg-covered spacing.
//   - state:  bold accent fg, bg from base, Padding for bg-covered spacing.
//   - count:  muted fg, bg from base, Padding for bg-covered spacing.
//   - filler: bg only, flexible Width to push the right group to the edge.
//
// Spacing between segments comes from Padding (which lipgloss colors with the
// segment's own background), never from literal " " characters. This is the
// critical rule: plain spaces inside a Background-wrapped string are NOT
// bg-styled by lipgloss's renderer.
func (t *Theme) HeaderStyles(width int) (bar, brand, field, state, count, filler lipgloss.Style) {
	bg := lipgloss.Color(t.HeaderBgColor)

	bar = lipgloss.NewStyle().
		Background(bg).
		Width(width)

	base := lipgloss.NewStyle().
		Background(bg).
		Padding(0, t.HeaderPadH)

	brand = base.Copy().
		Bold(true).
		Foreground(lipgloss.Color(t.HeaderFgColor))

	field = base.Copy().
		Foreground(lipgloss.Color(t.HeaderFieldColor))

	state = base.Copy().
		Bold(true).
		Foreground(lipgloss.Color(t.HeaderStateColor))

	count = base.Copy().
		Foreground(lipgloss.Color(t.HeaderCountColor))

	filler = lipgloss.NewStyle().
		Background(bg)

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
