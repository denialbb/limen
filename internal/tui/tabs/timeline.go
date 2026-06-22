package tabs

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/bubbles/viewport"

	"github.com/denialbb/limen/internal/bus"
)

// TimelineTab renders every event the TUI receives, in publication order. It
// is the canonical "all activity" view: the other tabs filter, this one shows
// the full sequence including state-machine transitions and the final
// terminal event.
type TimelineTab struct {
	viewport   viewport.Model
	lines      []string
	finalState string
	finalized  bool
}

// NewTimelineTab constructs an empty TimelineTab with a default 1x1 footprint.
func NewTimelineTab() *TimelineTab {
	t := &TimelineTab{}
	t.viewport = viewport.New(1, 1)
	return t
}

// SetSize resizes the Timeline viewport.
func (t *TimelineTab) SetSize(width, height int) {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	t.viewport.Width = width
	t.viewport.Height = height
}

// Update ingests either an EventMsg or a scroll key. Every event is appended
// as a one-line summary; TaskFinalized additionally records the terminal
// state so a footer can surface it after the event pump stops.
func (t *TimelineTab) Update(msg tea.Msg) {
	switch m := msg.(type) {
	case EventMsg:
		t.handleEvent(m.Event)
	case tea.KeyMsg:
		t.viewport, _ = t.viewport.Update(m)
	}
}

// handleEvent formats and appends a single one-line event summary.
func (t *TimelineTab) handleEvent(ev bus.Event) {
	if ev == nil {
		return
	}
	summary := summarizeEvent(ev)
	if summary == "" {
		return
	}
	appendLine(&t.lines, &t.viewport, eventTimestamp(ev), summary)
	if fin, ok := ev.(*bus.TaskFinalized); ok {
		t.finalized = true
		t.finalState = string(fin.FinalState)
	}
}

// View renders the accumulated timeline through the viewport.
func (t *TimelineTab) View() string {
	if t.viewport.Height <= 0 {
		return ""
	}
	return t.viewport.View()
}

// Lines returns a defensive copy of the accumulated timeline lines.
func (t *TimelineTab) Lines() []string {
	out := make([]string, len(t.lines))
	copy(out, t.lines)
	return out
}

// summarizeEvent produces a single-line summary keyed by event type. Returning
// an empty string silently drops types not relevant to the timeline; in
// practice every event in the taxonomy maps to a non-empty summary.
func summarizeEvent(ev bus.Event) string {
	switch e := ev.(type) {
	case *bus.TaskStateChanged:
		return fmt.Sprintf("state: %s -> %s", e.From, e.To)
	case *bus.ContextBuilt:
		return fmt.Sprintf("context built: %d bytes (manifest=%q)", e.SnapshotSize, e.ManifestRef)
	case *bus.RouterExamining:
		return fmt.Sprintf("router examining: entropy=%s", floatToText(e.Entropy))
	case *bus.RouterDecisionEvent:
		return fmt.Sprintf("router decision: %s (expand=%d) — %s", e.Decision, e.ExpandCount, e.Rationale)
	case *bus.WorkerStarted:
		return fmt.Sprintf("worker started: %s (retry=%d)", e.WorktreePath, e.Retry)
	case *bus.WorkerToolCall:
		return fmt.Sprintf("tool call: %s %s", e.Tool, e.Args)
	case *bus.WorkerFileEdit:
		return fmt.Sprintf("file edit: %s (%s)", e.Path, e.Op)
	case *bus.WorkerFinished:
		return "worker finished"
	case *bus.ValidatorExamining:
		return fmt.Sprintf("validator examining: %d criteria", criterionCount(e.Criteria))
	case *bus.ValidatorCriterionResult:
		verdict := "FAIL"
		if e.Passed {
			verdict = "PASS"
		}
		return fmt.Sprintf("criterion %q: %s", e.Criterion, verdict)
	case *bus.ValidatorVerdict:
		v := "FAIL"
		if e.Passes {
			v = "PASS"
		}
		return fmt.Sprintf("verdict: %s — %s", v, strings.TrimSpace(e.Feedback))
	case *bus.ConflictDetected:
		return fmt.Sprintf("conflict detected: %d region(s)", conflictRegionCount(e.Regions))
	case *bus.TaskFinalized:
		return fmt.Sprintf("FINALIZED: state=%s output=%q", e.FinalState, truncate(e.FinalOutputRef, 40))
	}
	return ""
}

// eventTimestamp extracts the timestamp from an event. Falls back to a zero
// time only if the event type is unrecognized; in practice every event in the
// taxonomy carries a Timestamp field.
func eventTimestamp(ev bus.Event) time.Time {
	switch e := ev.(type) {
	case *bus.TaskStateChanged:
		return e.Timestamp
	case *bus.ContextBuilt:
		return e.Timestamp
	case *bus.RouterExamining:
		return e.Timestamp
	case *bus.RouterDecisionEvent:
		return e.Timestamp
	case *bus.WorkerStarted:
		return e.Timestamp
	case *bus.WorkerToolCall:
		return e.Timestamp
	case *bus.WorkerFileEdit:
		return e.Timestamp
	case *bus.WorkerFinished:
		return e.Timestamp
	case *bus.ValidatorExamining:
		return e.Timestamp
	case *bus.ValidatorCriterionResult:
		return e.Timestamp
	case *bus.ValidatorVerdict:
		return e.Timestamp
	case *bus.ConflictDetected:
		return e.Timestamp
	case *bus.TaskFinalized:
		return e.Timestamp
	}
	return time.Time{}
}

// truncate clips s to at most maxLen runes, appending an ellipsis when the
// original was longer. Used to keep the timeline view compact.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}