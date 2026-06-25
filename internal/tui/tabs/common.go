// Package tabs contains the four switchable tab models rendered by the
// interactive TUI. Each tab owns a bubbles viewport, accumulates formatted
// output lines from the bus event stream, and exposes a uniform Update /
// SetSize / View surface for the top-level Model to drive.
package tabs

import (
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/denialbb/limen/internal/bus"
)

// EventMsg is the message the top-level Model forwards to a tab's Update to
// signal that a bus.Event has arrived. Tabs are free to ignore events they do
// not care about; the Timeline tab accepts all of them.
type EventMsg struct {
	Event bus.Event
}

// EventFormatter is assigned by the parent tui package at init to format and color event log lines without introducing a cyclic dependency.
var EventFormatter func(time.Time, string) string

// FooterStyle is assigned by the parent tui package at init to style the timeline completion footer.
var FooterStyle lipgloss.Style

// timestampFormat is the compact clock format used in tab output. Keeping it
// here lets every tab share the same rendering without re-declaring literals.
const timestampFormat = "15:04:05"

// appendLine is a small helper shared by tabs to push a new line into the
// accumulated output and refresh the viewport content.
func appendLine(lines *[]string, vp lineSetter, vpWidth int, ts time.Time, body string) {
	var styled string
	if EventFormatter != nil {
		styled = EventFormatter(ts, body)
	} else {
		styled = "[" + ts.Format(timestampFormat) + "] " + body
	}
	*lines = append(*lines, styled)
	vp.SetContent(wrapLines(*lines, vpWidth))
}

// wrapLines wraps each line in lines to the specified width using lipgloss.
func wrapLines(lines []string, width int) string {
	if width <= 0 {
		return strings.Join(lines, "\n")
	}
	var wrapped []string
	for _, line := range lines {
		wrapped = append(wrapped, lipgloss.NewStyle().Width(width).Render(line))
	}
	return strings.Join(wrapped, "\n")
}

// lineSetter is the narrow interface tabs require from their viewport so the
// helper can stay decoupled from the bubbles viewport type. Viewport.SetContent
// has a pointer receiver, so callers pass &tab.viewport.
type lineSetter interface {
	SetContent(string)
}

// floatToText renders a float64 in a compact, fixed-point form suitable for
// entropy scores and similar printed metrics.
func floatToText(f float64) string {
	return strconv.FormatFloat(f, 'f', 3, 64)
}