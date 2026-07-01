// Package tabs contains the four switchable tab models rendered by the
// interactive TUI. Each tab owns a bubbles viewport, accumulates formatted
// output lines from the bus event stream, and exposes a uniform Update /
// SetSize / View surface for the top-level Model to drive.
package tabs

import (
	"encoding/json"
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

// formatToolCall renders a tool call as a compact, human-readable string.
// e.Tool is the tool name; e.Args is a JSON object of arguments.
// Output examples: "bash: go test ./...", "read: main.go", "edit: main.go"
func formatToolCall(tool, argsJSON string) string {
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return tool + ": " + argsJSON
	}
	switch tool {
	case "bash":
		if cmd, ok := args["command"].(string); ok {
			cmd = strings.ReplaceAll(cmd, "\n", " ")
			if len(cmd) > 100 {
				cmd = cmd[:100] + "…"
			}
			return "bash: " + cmd
		}
	case "read":
		if path, ok := args["path"].(string); ok {
			return "read: " + path
		}
	case "edit":
		if path, ok := args["path"].(string); ok {
			return "edit: " + path
		}
	case "write":
		if path, ok := args["path"].(string); ok {
			return "write: " + path
		}
	}
	// Generic fallback: tool + first string value found in args
	for _, v := range args {
		if s, ok := v.(string); ok {
			if len(s) > 80 {
				s = s[:80] + "…"
			}
			return tool + ": " + s
		}
	}
	return tool
}