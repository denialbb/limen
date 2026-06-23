package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
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
	active, inactive := theme.TabStyles()

	parts := make([]string, len(t.tabs))
	for i, label := range t.tabs {
		if i == t.activeIdx {
			parts[i] = active.Render(label)
		} else {
			parts[i] = inactive.Render(label)
		}
	}

	if width <= 0 {
		return strings.Join(parts, strings.Repeat(" ", theme.TabPadBetween))
	}

	tabWidths := make([]int, len(parts))
	totalTabsWidth := 0
	for i, p := range parts {
		tabWidths[i] = lipgloss.Width(p)
		totalTabsWidth += tabWidths[i]
	}

	gapCount := len(parts) - 1
	baseGap := 0
	extra := 0
	if gapCount > 0 {
		available := width - totalTabsWidth
		if available > 0 {
			baseGap = available / gapCount
			extra = available % gapCount
		}
	}

	var sb strings.Builder
	for i, tab := range parts {
		sb.WriteString(tab)
		if i < gapCount {
			gapSize := baseGap
			if i < extra {
				gapSize++
			}
			sb.WriteString(strings.Repeat(" ", gapSize))
		}
	}

	return sb.String()
}