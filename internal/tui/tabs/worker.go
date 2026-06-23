package tabs

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/bubbles/viewport"

	"github.com/denialbb/limen/internal/bus"
)

// WorkerTab renders Worker activity: worktree details, tool-call stream, file
// edits, completion, and any conflict regions reported by git.
type WorkerTab struct {
	viewport viewport.Model
	lines    []string
}

// NewWorkerTab constructs an empty WorkerTab with a default 1x1 footprint.
func NewWorkerTab() WorkerTab {
	w := WorkerTab{}
	w.viewport = viewport.New(1, 1)
	return w
}

// Init satisfies the tea.Model surface; the tab has no async work of its own.
func (w WorkerTab) Init() tea.Cmd { return nil }

// SetSize resizes the Worker viewport.
func (w WorkerTab) SetSize(width, height int) WorkerTab {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	w.viewport.Width = width
	w.viewport.Height = height
	return w
}

// Update ingests either an EventMsg carrying a worker-relevant bus event or a
// tea.KeyMsg for scroll.
func (w WorkerTab) Update(msg tea.Msg) (WorkerTab, tea.Cmd) {
	switch m := msg.(type) {
	case EventMsg:
		w = w.handleEvent(m.Event)
	case tea.KeyMsg:
		w.viewport, _ = w.viewport.Update(m)
	}
	return w, nil
}

// handleEvent formats and appends a worker-relevant event line. It returns
// the modified tab value so Update can thread it back under value semantics.
func (w WorkerTab) handleEvent(ev bus.Event) WorkerTab {
	switch e := ev.(type) {
	case *bus.WorkerStarted:
		body := fmt.Sprintf(
			"Worker started: %s (base: %s, retry: %d)",
			e.WorktreePath, baseCommitLabel(e.BaseCommit), e.Retry,
		)
		appendLine(&w.lines, &w.viewport, e.Timestamp, body)
	case *bus.WorkerToolCall:
		appendLine(&w.lines, &w.viewport, e.Timestamp, "Tool call: "+e.Tool)
	case *bus.WorkerFileEdit:
		body := fmt.Sprintf("File edit: %s (%s)", e.Path, e.Op)
		appendLine(&w.lines, &w.viewport, e.Timestamp, body)
	case *bus.WorkerFinished:
		appendLine(&w.lines, &w.viewport, e.Timestamp, "Worker finished")
	case *bus.ConflictDetected:
		body := fmt.Sprintf("Conflict detected: %d region(s)", len(e.Regions))
		appendLine(&w.lines, &w.viewport, e.Timestamp, body)
	}
	return w
}

// baseCommitLabel renders the base commit short form, falling back to "HEAD"
// when the orchestrator-provided base commit is empty (the v1 stub uses HEAD).
func baseCommitLabel(base string) string {
	if base == "" {
		return "HEAD"
	}
	return base
}

// View renders the accumulated lines through the viewport.
func (w WorkerTab) View() string {
	if w.viewport.Height <= 0 {
		return ""
	}
	return w.viewport.View()
}

// Lines returns a defensive copy of the accumulated output lines.
func (w WorkerTab) Lines() []string {
	out := make([]string, len(w.lines))
	copy(out, w.lines)
	return out
}