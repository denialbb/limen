package tabs

import (
	"fmt"
	"strings"

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
	viewport       viewport.Model
	lines          []string
	finalState     string
	finalOutputRef string
	finalized      bool
}

// NewTimelineTab constructs an empty TimelineTab with a default 1x1 footprint.
func NewTimelineTab() TimelineTab {
	t := TimelineTab{}
	t.viewport = viewport.New(1, 1)
	return t
}

// Init satisfies the tea.Model surface; the tab has no async work of its own.
func (t TimelineTab) Init() tea.Cmd { return nil }

// SetSize resizes the Timeline viewport. When the tab is finalized, the
// completion footer's line count is reserved so the viewport never overdraws
// it.
func (t TimelineTab) SetSize(width, height int) TimelineTab {
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
	return t
}

// Update ingests either an EventMsg or a scroll key. Every event is appended
// as a one-line summary; TaskFinalized additionally records the terminal
// state so a footer can surface it after the event pump stops.
func (t TimelineTab) Update(msg tea.Msg) (TimelineTab, tea.Cmd) {
	switch m := msg.(type) {
	case EventMsg:
		t = t.handleEvent(m.Event)
	case tea.KeyMsg:
		t.viewport, _ = t.viewport.Update(m)
	}
	return t, nil
}

// handleEvent formats and appends a single one-line event summary. It returns
// the modified tab value so Update can thread it back under value semantics.
func (t TimelineTab) handleEvent(ev bus.Event) TimelineTab {
	if ev == nil {
		return t
	}
	summary := summarizeEvent(ev)
	if summary == "" {
		return t
	}
	appendLine(&t.lines, &t.viewport, ev.Time(), summary)
	if fin, ok := ev.(*bus.TaskFinalized); ok {
		t.finalized = true
		t.finalState = string(fin.FinalState)
		t.finalOutputRef = fin.FinalOutputRef
		// Reserve vertical space for the completion footer now that it will
		// render, so the viewport body does not overdraw it.
		t = t.reserveFooterSpace()
	}
	return t
}

// reserveFooterSpace shrinks the viewport by the footer's line count. Called
// once on finalization (after the viewport was already sized to the full
// content area) and reused by SetSize on subsequent resizes.
func (t TimelineTab) reserveFooterSpace() TimelineTab {
	h := t.viewport.Height - t.footerLineCount()
	if h < 1 {
		h = 1
	}
	t.viewport.Height = h
	return t
}

// footerLineCount reports the number of lines the completion footer occupies:
// one line for the final state, plus a second line for the output reference
// when one was recorded. Returns zero before finalization.
func (t TimelineTab) footerLineCount() int {
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
func (t TimelineTab) renderFooter() string {
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
func (t TimelineTab) View() string {
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
func (t TimelineTab) Lines() []string {
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
		return fmt.Sprintf("validator examining: %d criteria", len(e.Criteria))
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
		return fmt.Sprintf("conflict detected: %d region(s)", len(e.Regions))
	case *bus.TaskFinalized:
		return fmt.Sprintf("FINALIZED: state=%s output=%q", e.FinalState, truncate(e.FinalOutputRef, 40))
	case *bus.OrchestratorError:
		return fmt.Sprintf("orchestrator error: %s", e.Error)
	}
	return ""
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