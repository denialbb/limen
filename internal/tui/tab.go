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

	boundary := theme.TabBoundaryPad
	gapBetween := theme.TabPadBetween

	if width <= 0 {
		gap := strings.Repeat(" ", gapBetween)
		inner := strings.Join(parts, gap)
		return strings.Repeat(" ", boundary) + inner + strings.Repeat(" ", boundary)
	}

	tabWidths := make([]int, len(parts))
	totalTabsWidth := 0
	for i, p := range parts {
		tabWidths[i] = lipgloss.Width(p)
		totalTabsWidth += tabWidths[i]
	}

	totalBoundaries := 2 * boundary
	gapCount := len(parts) - 1
	available := width - totalTabsWidth - totalBoundaries

	baseGap := 0
	if gapCount > 0 && available > gapCount {
		baseGap = available / gapCount
	}

	var sb strings.Builder
	sb.WriteString(strings.Repeat(" ", boundary))
	for i, tab := range parts {
		sb.WriteString(tab)
		if i < gapCount {
			gapSize := baseGap
			if i < available%gapCount {
				gapSize++
			}
			if gapSize < 1 {
				gapSize = 1
			}
			sb.WriteString(strings.Repeat(" ", gapSize))
		}
	}
	sb.WriteString(strings.Repeat(" ", boundary))

	return lipgloss.Place(width, 1, lipgloss.Center, lipgloss.Center, sb.String())
}