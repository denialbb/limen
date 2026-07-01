package tui

import (
	"regexp"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/denialbb/limen/internal/tui/tabs"
)

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

	// Footer constants.
	FooterBgColor string
	FooterFgColor string

	// Split layout constants.
	SplitWidthThreshold   int     // terminal width to enter split mode
	SplitHeightThreshold  int     // terminal height to enter split mode
	SplitLeftWidthPct     float64 // fraction of width for the left column
	SplitRouterHeightPct  float64 // fraction of left column height for the router panel
	SplitWorkersHeightPct float64 // fraction of right column height for the workers panel
	SplitDividerColor     string  // color for │ column divider
	SplitPanelTitleColor  string  // color for panel title lines (─ Router ────)

	// Event coloring constants.
	TimestampColor string
	EventTextColor string
	KeywordColors  map[string]string
}

func NewTheme() *Theme {
	return &Theme{
		HeaderBgColor:    "#313244", // Surface 0
		HeaderFgColor:    "#cdd6f4", // Text
		HeaderPadH:       1,
		HeaderFieldColor: "#a6adc8", // Subtext 0
		HeaderStateColor: "#cba6f7", // Mauve
		HeaderCountColor: "#9399b2", // Subtext 1

		TabActiveBgColor: "#cba6f7", // Mauve
		TabActiveFgColor: "#11111b", // Crust
		TabInactiveColor: "#6c7086", // Overlay 0
		TabPadH:          1,
		TabBoundaryPad:   2,

		SeparatorColor: "#45475a", // Surface 1
		SeparatorRune:  "─",
		SeparatorPadV:  0,

		FooterBgColor: "#45475a", // Surface 1
		FooterFgColor: "#fab387", // Peach (high contrast text)

		SplitWidthThreshold:   120,
		SplitHeightThreshold:  30,
		SplitLeftWidthPct:     0.30,
		SplitRouterHeightPct:  0.35,
		SplitWorkersHeightPct: 0.30,
		SplitDividerColor:     "#45475a",
		SplitPanelTitleColor:  "#7f849c",

		TimestampColor: "#585b70", // Pale color (Surface 2)
		EventTextColor: "#cdd6f4", // Normal foreground (Text)
		KeywordColors: map[string]string{
			"PASS":      "#a6e3a1", // Green
			"FAIL":      "#f38ba8", // Red
			"APPROVED":  "#a6e3a1", // Green
			"REVISION":  "#f9e2af", // Yellow
			"PROCEED":   "#a6e3a1", // Green
			"ABORT":     "#f38ba8", // Red
			"CONFLICT":  "#f9e2af", // Yellow
			"FINALIZED": "#74c7ec", // Sapphire
			"CRITICAL":  "#f38ba8", // Red
			"WARNING":   "#f9e2af", // Yellow
			"DONE":      "#a6e3a1", // Green
			"COMMITTED": "#a6e3a1", // Green
		},
	}
}

// HeaderStyles builds the lipgloss styles for the header bar.
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

// SplitDividerStyle returns a style for the vertical │ column divider.
func (t *Theme) SplitDividerStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(t.SplitDividerColor))
}

// SplitPanelTitleStyle returns a style for split-mode panel title bars.
func (t *Theme) SplitPanelTitleStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(t.SplitPanelTitleColor))
}

// FooterStyle returns a style for rendering the timeline completion footer.
func (t *Theme) FooterStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Bold(true).
		Background(lipgloss.Color(t.FooterBgColor)).
		Foreground(lipgloss.Color(t.FooterFgColor)).
		Padding(0, 1)
}

// HintStyle returns a style for the keybinding hint line: dim (same color as
// timestamps) and right-aligned across the given width.
func (t *Theme) HintStyle(width int) lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(t.TimestampColor)).
		Width(width).
		Align(lipgloss.Right)
}

var allCapsRegex = regexp.MustCompile(`\b[A-Z_]{3,}\b`)

// FormatEventLine colors the timestamp, body, and matched keywords.
func (t *Theme) FormatEventLine(ts time.Time, body string) string {
	tsStr := "[" + ts.Format("15:04:05") + "]"
	styledTs := lipgloss.NewStyle().Foreground(lipgloss.Color(t.TimestampColor)).Render(tsStr)

	styledBody := allCapsRegex.ReplaceAllStringFunc(body, func(match string) string {
		if color, exists := t.KeywordColors[match]; exists {
			return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Bold(true).Render(match)
		}
		// Fallback to high-contrast Sapphire color for other ALLCAPS keywords
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#74c7ec")).Bold(true).Render(match)
	})

	styledBody = lipgloss.NewStyle().Foreground(lipgloss.Color(t.EventTextColor)).Render(styledBody)

	return styledTs + " " + styledBody
}

// package-level theme singleton used by header, tab, and model.
var theme = NewTerminalTheme()

func init() {
	tabs.EventFormatter = theme.FormatEventLine
	tabs.FooterStyle = theme.FooterStyle()
}
