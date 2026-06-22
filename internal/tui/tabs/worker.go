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
func NewWorkerTab() *WorkerTab {
	w := &WorkerTab{}
	w.viewport = viewport.New(1, 1)
	return w
}

// SetSize resizes the Worker viewport.
func (w *WorkerTab) SetSize(width, height int) {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	w.viewport.Width = width
	w.viewport.Height = height
}

// Update ingests either an EventMsg carrying a worker-relevant bus event or a
// tea.KeyMsg for scroll.
func (w *WorkerTab) Update(msg tea.Msg) {
	switch m := msg.(type) {
	case EventMsg:
		w.handleEvent(m.Event)
	case tea.KeyMsg:
		w.viewport, _ = w.viewport.Update(m)
	}
}

// handleEvent formats and appends a worker-relevant event line.
func (w *WorkerTab) handleEvent(ev bus.Event) {
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
func (w *WorkerTab) View() string {
	if w.viewport.Height <= 0 {
		return ""
	}
	return w.viewport.View()
}

// Lines returns a defensive copy of the accumulated output lines.
func (w *WorkerTab) Lines() []string {
	out := make([]string, len(w.lines))
	copy(out, w.lines)
	return out
}