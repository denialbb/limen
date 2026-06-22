package tabs

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"

	"github.com/denialbb/limen/internal/bus"
)

// footerStyle styles the completion footer so it is visually distinct from
// the scrollable timeline body: a bold light-on-magenta block sitting below
// the viewport content. Padding is horizontal-only so it does not consume
// extra vertical rows beyond the footer's own line count.
var footerStyle = lipgloss.NewStyle().
	Bold(true).
	Background(lipgloss.Color("53")).
	Foreground(lipgloss.Color("15")).
	Padding(0, 1)

// TimelineTab renders every event the TUI receives, in publication order. It
// is the canonical "all activity" view: the other tabs filter, this one shows
// the full sequence including state-machine transitions and the final
// terminal event.
type TimelineTab struct {
	viewport        viewport.Model
	lines           []string
	finalState      string
	finalOutputRef  string
	finalized       bool
}

// NewTimelineTab constructs an empty TimelineTab with a default 1x1 footprint.
func NewTimelineTab() *TimelineTab {
	t := &TimelineTab{}
	t.viewport = viewport.New(1, 1)
	return t
}

// SetSize resizes the Timeline viewport. When the tab is finalized, the
// completion footer's line count is reserved so the viewport never overdraws
// it.
func (t *TimelineTab) SetSize(width, height int) {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	height -= t.footerLineCount()
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
		t.finalOutputRef = fin.FinalOutputRef
		// Reserve vertical space for the completion footer now that it will
		// render, so the viewport body does not overdraw it.
		t.reserveFooterSpace()
	}
}

// reserveFooterSpace shrinks the viewport by the footer's line count. Called
// once on finalization (after the viewport was already sized to the full
// content area) and reused by SetSize on subsequent resizes.
func (t *TimelineTab) reserveFooterSpace() {
	h := t.viewport.Height - t.footerLineCount()
	if h < 1 {
		h = 1
	}
	t.viewport.Height = h
}

// footerLineCount reports the number of lines the completion footer occupies:
// one line for the final state, plus a second line for the output reference
// when one was recorded. Returns zero before finalization.
func (t *TimelineTab) footerLineCount() int {
	if !t.finalized {
		return 0
	}
	if t.finalOutputRef == "" {
		return 1
	}
	return 2
}

// renderFooter produces the styled completion footer shown beneath the
// viewport body once the task has reached a terminal state.
func (t *TimelineTab) renderFooter() string {
	stateName := t.finalState
	if stateName == "" {
		stateName = "UNKNOWN"
	}
	body := "FINAL: " + stateName
	if t.finalOutputRef != "" {
		body += "\noutput: " + truncate(t.finalOutputRef, 60)
	}
	return footerStyle.Render(body)
}

// View renders the accumulated timeline through the viewport, and appends
// the completion footer beneath it once the task has finalized.
func (t *TimelineTab) View() string {
	if t.viewport.Height <= 0 {
		// Defensive: tabs are expected to be sized before being viewed.
		return ""
	}
	body := t.viewport.View()
	if !t.finalized {
		return body
	}
	return strings.Join([]string{body, t.renderFooter()}, "\n")
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

// truncate clips s to at most maxLen bytes, appending an ellipsis when the
// original was longer. Used to keep the timeline view compact.
//
// TODO: byte-based truncation can split a multi-byte UTF-8 rune. The current
// inputs are ASCII git refs, so this is acceptable for v1; switch to
// utf8.DecodeRune-based counting if non-ASCII output refs appear in v2.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}