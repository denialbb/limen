package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// splitColumns renders leftContent and rightContent side-by-side, separated
// by a styled vertical bar (│). Both content strings are already-rendered
// multi-line strings. contentHeight is the height of the content area in rows
// (used to build a full-height divider column).
func splitColumns(leftContent, rightContent string, contentHeight int) string {
	divStyle := theme.SplitDividerStyle()
	divLines := make([]string, contentHeight)
	for i := range divLines {
		divLines[i] = divStyle.Render("│")
	}
	dividerCol := strings.Join(divLines, "\n")
	return lipgloss.JoinHorizontal(lipgloss.Top, leftContent, dividerCol, rightContent)
}

// renderPanelTitle produces a one-line section header like:
//
//	─ Router ─────────────────────────────
//
// styled with SplitPanelTitleStyle. width is the total column width.
func renderPanelTitle(label string, width int) string {
	style := theme.SplitPanelTitleStyle()
	prefix := " ─ " + label + " "
	fill := max(0, width-lipgloss.Width(prefix))
	return style.Render(prefix + strings.Repeat("─", fill))
}
