package tabs

import (
	"strconv"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/bubbles/viewport"

	"github.com/denialbb/limen/internal/bus"
)

// RouterTab renders the Router's reasoning: context assembly, entropy
// examination, and the routing decision with its rationale and expand count.
type RouterTab struct {
	viewport viewport.Model
	lines    []string
}

// NewRouterTab constructs an empty RouterTab. The viewport is given a
// default 1x1 footprint; the top-level model resizes it via SetSize as soon
// as the first tea.WindowSizeMsg arrives.
func NewRouterTab() RouterTab {
	r := RouterTab{}
	r.viewport = viewport.New(1, 1)
	return r
}

// Init satisfies the tea.Model surface; the tab has no async work of its own,
// so it returns a nil command.
func (r RouterTab) Init() tea.Cmd { return nil }

// SetSize resizes the Router viewport. Degenerate sizes are clamped so a
// zero-height resize (e.g. during teardown) never panics.
func (r RouterTab) SetSize(width, height int) RouterTab {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	r.viewport.Width = width
	r.viewport.Height = height
	return r
}

// Update ingests either an EventMsg carrying a routed bus event or a
// tea.KeyMsg for scroll. Router-relevant event kinds are appended to the
// accumulated output; other messages (including non-router events) are
// forwarded to the viewport so scrolling still works.
func (r RouterTab) Update(msg tea.Msg) (RouterTab, tea.Cmd) {
	switch m := msg.(type) {
	case EventMsg:
		r = r.handleEvent(m.Event)
	case tea.KeyMsg:
		r.viewport, _ = r.viewport.Update(m)
	}
	return r, nil
}

// handleEvent formats and appends a router-relevant event line. It returns
// the modified tab value so Update can thread it back to the caller under
// value semantics.
func (r RouterTab) handleEvent(ev bus.Event) RouterTab {
	switch e := ev.(type) {
	case *bus.ContextBuilt:
		// NOTE: Snapshot size is in bytes per the taxonomy; manifestRef can be
		// empty when the v1 retriever stub emits no manifest.
		appendLine(&r.lines, &r.viewport, e.Timestamp,
			"Context built: "+strconv.Itoa(e.SnapshotSize)+" bytes")
	case *bus.RouterExamining:
		entropyText := "entropy=" + floatToText(e.Entropy)
		appendLine(&r.lines, &r.viewport, e.Timestamp, "Router examining: "+entropyText)
	case *bus.RouterDecisionEvent:
		// decision + rationale on a single line, with expand count when > 0
		body := "Router decision: " + string(e.Decision) + " — " + e.Rationale
		if e.ExpandCount > 0 {
			body += " (expand=" + strconv.Itoa(e.ExpandCount) + ")"
		}
		appendLine(&r.lines, &r.viewport, e.Timestamp, body)
	}
	return r
}

// View renders the accumulated lines through the viewport so scrolling
// behaves correctly.
func (r RouterTab) View() string {
	if r.viewport.Height <= 0 {
		// Defensive: tabs are expected to be sized before being viewed.
		return ""
	}
	return r.viewport.View()
}

// Lines returns a defensive copy of the accumulated output lines, in
// publication order. Used by tests to assert routing without pulling in a
// real terminal.
func (r RouterTab) Lines() []string {
	out := make([]string, len(r.lines))
	copy(out, r.lines)
	return out
}