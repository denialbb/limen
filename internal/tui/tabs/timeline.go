package tabs

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	ltree "github.com/charmbracelet/lipgloss/tree"

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

// transitionEntry holds one buffered state-machine transition. Transitions are
// collected and flushed as a lipgloss/tree block so they render with proper
// ├─ / └─ connectors rather than as flat independent lines.
type transitionEntry struct{ from, to string }

// transitionStyle styles the state-transition tree connectors and text.
var transitionStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

// transitionEnumerator produces compact ├─ / └─ connectors.
var transitionEnumerator ltree.Enumerator = func(ch ltree.Children, i int) string {
	if i == ch.Length()-1 {
		return "└─ "
	}
	return "├─ "
}

// transitionIndenter produces │  under live siblings and spaces under the last.
var transitionIndenter ltree.Indenter = func(ch ltree.Children, i int) string {
	if i == ch.Length()-1 {
		return "   "
	}
	return "│  "
}

// TimelineTab renders every event the TUI receives, in publication order. It
// is the canonical "all activity" view: the other tabs filter, this one shows
// the full sequence including state-machine transitions and the final
// terminal event.
type TimelineTab struct {
	viewport       viewport.Model
	lines          []string
	pending        []transitionEntry // buffered transitions, flushed as a tree block
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
	t.viewport.SetContent(wrapLines(t.lines, width))
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

// handleEvent processes one bus.Event. State transitions are buffered in
// t.pending so they can be flushed as a tree block (├─/└─ connectors) before
// the next semantic event, giving a clear visual hierarchy.
func (t TimelineTab) handleEvent(ev bus.Event) TimelineTab {
	if ev == nil {
		return t
	}

	if sc, ok := ev.(*bus.TaskStateChanged); ok {
		t.pending = append(t.pending, transitionEntry{
			from: string(sc.From),
			to:   string(sc.To),
		})
		return t
	}

	// Non-transition event: flush any buffered transitions as a tree first.
	t = t.flushPending()

	summary := summarizeEvent(ev)
	if summary != "" {
		appendLine(&t.lines, &t.viewport, t.viewport.Width, ev.Time(), summary)
	}

	if fin, ok := ev.(*bus.TaskFinalized); ok {
		t.finalized = true
		t.finalState = string(fin.FinalState)
		t.finalOutputRef = fin.FinalOutputRef
		t = t.reserveFooterSpace()
	}
	return t
}

// flushPending renders buffered state transitions as a lipgloss/tree block and
// appends the rendered lines to t.lines. The tree package handles ├─/└─
// connectors automatically. No-op when there are no pending transitions.
func (t TimelineTab) flushPending() TimelineTab {
	if len(t.pending) == 0 {
		return t
	}

	children := make([]any, len(t.pending))
	for i, pe := range t.pending {
		children[i] = pe.from + " → " + pe.to
	}

	rendered := ltree.New().
		Enumerator(transitionEnumerator).
		Indenter(transitionIndenter).
		EnumeratorStyle(transitionStyle).
		ItemStyle(transitionStyle).
		Child(children...).
		String()

	for _, line := range strings.Split(strings.TrimRight(rendered, "\n"), "\n") {
		// lipgloss/tree emits an empty root line when the root value is ""; skip
		// any line whose visible width is zero.
		if lipgloss.Width(line) == 0 {
			continue
		}
		t.lines = append(t.lines, line)
	}
	t.viewport.SetContent(wrapLines(t.lines, t.viewport.Width))
	t.viewport.GotoBottom()
	t.pending = nil
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
		body += "\n" + extractDiffFiles(t.finalOutputRef)
	}
	style := FooterStyle
	if style.Copy().GetBackground() == lipgloss.Color("") {
		style = footerStyle
	}
	return style.Render(body)
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

// summarizeEvent produces a single-line summary for main milestone events only.
// Detail-level events (tool calls, file edits, agent messages, per-criterion
// results, router entropy) are omitted here and shown only in their specific tab.
func summarizeEvent(ev bus.Event) string {
	switch e := ev.(type) {
	case *bus.TaskStateChanged:
		_ = e
		return "" // handled by flushPending as a tree block
	case *bus.ContextBuilt:
		return fmt.Sprintf("context built: %d bytes", e.SnapshotSize)
	case *bus.RouterDecisionEvent:
		return fmt.Sprintf("router: %s — %s", e.Decision, e.Rationale)
	case *bus.WorkerStarted:
		return fmt.Sprintf("worker started (retry=%d)", e.Retry)
	case *bus.WorkerFinished:
		return "worker finished"
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

// extractDiffFiles extracts the list of changed filenames from a git diff string.
// It parses lines starting with "diff --git " and extracts the target filename (b/...).
// If parsing fails, it returns the original diff as a fallback.
func extractDiffFiles(diff string) string {
	var files []string
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			// "diff --git a/foo.txt b/foo.txt" -> "foo.txt"
			parts := strings.Fields(line)
			if len(parts) >= 4 {
				name := strings.TrimPrefix(parts[3], "b/")
				files = append(files, name)
			}
		}
	}
	if len(files) == 0 {
		return diff // fallback: original if unparseable
	}
	return strings.Join(files, ", ")
}

// truncate clips s to at most maxLen bytes, appending an ellipsis when the
// original was longer. Used to keep the timeline view compact.
//
// TODO: byte-based truncation can split a multi-byte UTF-8 rune. The current
// inputs are ASCII git refs, so this is acceptable for v1; switch to
// utf8.DecodeRune-based counting if non-ASCII output refs appear in v2.
func truncate(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
