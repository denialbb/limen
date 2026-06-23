// Package tabs contains the four switchable tab models rendered by the
// interactive TUI. Each tab owns a bubbles viewport, accumulates formatted
// output lines from the bus event stream, and exposes a uniform Update /
// SetSize / View surface for the top-level Model to drive.
package tabs

import (
	"strconv"
	"strings"
	"time"

	"github.com/denialbb/limen/internal/bus"
)

// EventMsg is the message the top-level Model forwards to a tab's Update to
// signal that a bus.Event has arrived. Tabs are free to ignore events they do
// not care about; the Timeline tab accepts all of them.
type EventMsg struct {
	Event bus.Event
}

// timestampFormat is the compact clock format used in tab output. Keeping it
// here lets every tab share the same rendering without re-declaring literals.
const timestampFormat = "15:04:05"

// appendLine is a small helper shared by tabs to push a new line into the
// accumulated output and refresh the viewport content.
func appendLine(lines *[]string, vp lineSetter, ts time.Time, body string) {
	*lines = append(*lines, "["+ts.Format(timestampFormat)+"] "+body)
	vp.SetContent(strings.Join(*lines, "\n"))
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