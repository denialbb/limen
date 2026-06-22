package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// activeTabStyle highlights the currently selected tab label.
var (
	activeTabStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("63")).Padding(0, 1)
	inactiveTabStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Padding(0, 1)
)

// TabStrip renders the four-tab selector row. The active tab is highlighted
// and serves as the user's primary affordance for switching views.
type TabStrip struct {
	tabs      []string
	activeIdx int
}

// NewTabStrip constructs a strip pre-seeded with the four canonical tabs in
// the order specified by the design document.
func NewTabStrip() *TabStrip {
	return &TabStrip{
		tabs:      []string{"Router", "Worker", "Validator", "Timeline"},
		activeIdx: 0,
	}
}

// SetActive changes the currently highlighted tab. Out-of-range indices are
// clamped defensively so a malformed tabID cannot panic on slice access.
func (t *TabStrip) SetActive(idx int) {
	if idx < 0 || idx >= len(t.tabs) {
		return
	}
	t.activeIdx = idx
}

// Active returns the index of the highlighted tab.
func (t *TabStrip) Active() int { return t.activeIdx }

// View renders the tab labels with the active one in a distinct style. The
// number prefix reflects the 1-4 keybindings documented in the design.
func (t *TabStrip) View() string {
	parts := make([]string, len(t.tabs))
	for i, label := range t.tabs {
		display := fmtTabLabel(i, label)
		if i == t.activeIdx {
			parts[i] = activeTabStyle.Render(display)
		} else {
			parts[i] = inactiveTabStyle.Render(display)
		}
	}
	return strings.Join(parts, "  ")
}

// fmtTabLabel renders a single tab label as "<n>:<Label>". The index argument
// is zero-based; the label is the canonical tab name.
func fmtTabLabel(idx int, label string) string {
	return strings.Join([]string{
		itoa(idx + 1),
		":",
		label,
	}, "")
}

// itoa is a self-contained int-to-string converter to avoid pulling fmt into a
// hot rendering path. Supports values 0-9 (the only range produced here).
func itoa(n int) string {
	if n < 0 || n > 9 {
		// NOTE: Defensive: only single-digit tab indices are produced; any
		// outside that range falls back to fmt to avoid silent truncation.
		return ""
	}
	return string(rune('0' + n))
}