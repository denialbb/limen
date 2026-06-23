package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type TabStrip struct {
	tabs      []string
	activeIdx int
	width     int
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

func (t *TabStrip) SetSize(width int) {
	t.width = width
}

func (t *TabStrip) View() string {
	active, inactive := theme.TabStyles()

	rendered := make([]string, len(t.tabs))
	totalWidth := 0
	for i, label := range t.tabs {
		style := inactive
		if i == t.activeIdx {
			style = active
		}
		rendered[i] = style.Render(label)
		totalWidth += lipgloss.Width(rendered[i])
	}

	boundary := theme.TabBoundaryPad
	gapCount := len(rendered) - 1

	if t.width <= 0 || gapCount <= 0 {
		return strings.Repeat(" ", boundary) +
			strings.Join(rendered, " ") +
			strings.Repeat(" ", boundary)
	}

	available := t.width - totalWidth - 2*boundary
	if available <= 0 {
		return strings.Join(rendered, " ")
	}

	baseGap := available / gapCount
	extra := available % gapCount

	var sb strings.Builder
	sb.WriteString(strings.Repeat(" ", boundary))
	for i, tab := range rendered {
		sb.WriteString(tab)
		if i < gapCount {
			gap := baseGap
			if i < extra {
				gap++
			}
			sb.WriteString(strings.Repeat(" ", gap))
		}
	}
	sb.WriteString(strings.Repeat(" ", boundary))

	return sb.String()
}
