package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	activeTabStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("63")).Padding(0, 1)
	inactiveTabStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Padding(0, 1)
)

type TabStrip struct {
	tabs      []string
	activeIdx int
}

func NewTabStrip() *TabStrip {
	return &TabStrip{
		tabs:      []string{"Router", "Worker", "Validator", "Timeline"},
		activeIdx: 0,
	}
}

func (t *TabStrip) SetActive(idx int) {
	if idx < 0 || idx >= len(t.tabs) {
		return
	}
	t.activeIdx = idx
}

func (t *TabStrip) Active() int { return t.activeIdx }

func (t *TabStrip) View(width int) string {
	parts := make([]string, len(t.tabs))
	for i, label := range t.tabs {
		if i == t.activeIdx {
			parts[i] = activeTabStyle.Render(label)
		} else {
			parts[i] = inactiveTabStyle.Render(label)
		}
	}
	tabBar := strings.Join(parts, "  ")
	if width <= 0 {
		return tabBar
	}
	return lipgloss.Place(width, 1, lipgloss.Center, lipgloss.Center, tabBar)
}