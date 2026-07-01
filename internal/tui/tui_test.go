package tui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/denialbb/limen/internal/bus"
	"github.com/denialbb/limen/internal/git"
	"github.com/denialbb/limen/internal/state"
)

// ansiRe strips ANSI escape sequences so assertions can be written against the
// human-readable substrings rather than the styled output.
var ansiRe = regexp.MustCompile("\x1b\\[[0-9;]*[a-zA-Z]")

func stripANSI(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}

// newSizedModel constructs a Model, then drives a WindowSizeMsg through it so
// every sub-component is sized for headless View rendering.
func newSizedModel(t *testing.T, taskID string, eventBus bus.EventBus) Model {
	t.Helper()
	m := NewModel(taskID, eventBus)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return updated.(Model)
}

// key sends a string key to the model's Update and returns the resulting model.
func key(t *testing.T, m Model, k string) Model {
	t.Helper()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
	return updated.(Model)
}

// publishAndPump publishes ev to the bus and delivers the resulting busEventMsg
// to the model by pumping exactly one event from the model's subscription
// channel. This exercises the real ChannelBus -> channel -> Model path.
func publishAndPump(t *testing.T, m Model, b bus.EventBus, ev bus.Event) Model {
	t.Helper()
	b.Publish(ev)
	ev2 := <-m.eventCh
	updated, _ := m.Update(busEventMsg{event: ev2})
	return updated.(Model)
}

func TestNewModel(t *testing.T) {
	b := bus.NewChannelBus()
	defer b.Close()
	m := newSizedModel(t, "task-1", b)

	if m.TaskID() != "task-1" {
		t.Fatalf("TaskID = %q, want %q", m.TaskID(), "task-1")
	}
	if m.currentTab != tabRouter {
		t.Fatalf("currentTab = %d, want %d (Router)", m.currentTab, tabRouter)
	}
	// Sub-components are value-typed now, so they can never be nil. Verify
	// initialization through their observable state instead.
	if m.header.taskID != "task-1" {
		t.Fatalf("header not initialized: taskID = %q", m.header.taskID)
	}
	if m.tabStrip.Active() != 0 {
		t.Fatalf("tab strip not initialized: active = %d", m.tabStrip.Active())
	}
	if len(m.router.Lines()) != 0 || len(m.worker.Lines()) != 0 ||
		len(m.validator.Lines()) != 0 || len(m.timeline.Lines()) != 0 {
		t.Fatalf("tab components should start empty")
	}
	if m.eventCh == nil {
		t.Fatalf("eventCh not subscribed")
	}
	if m.Finalized() {
		t.Fatalf("new model should not be finalized")
	}
}

func TestTabSwitching(t *testing.T) {
	b := bus.NewChannelBus()
	defer b.Close()
	m := newSizedModel(t, "task-1", b)

	// 1..4 jump directly to the corresponding tab.
	for i, want := range []tabID{
		tabRouter, tabWorker, tabValidator, tabTimeline,
	} {
		m = key(t, m, string(rune('1'+i)))
		if m.currentTab != want {
			t.Fatalf("after pressing %d: currentTab = %d, want %d", i+1, m.currentTab, want)
		}
		if m.tabStrip.Active() != int(want) {
			t.Fatalf("tab strip not synced: active = %d, want %d", m.tabStrip.Active(), int(want))
		}
	}

	// ] wraps from Timeline back to Router.
	m = key(t, m, "]")
	if m.currentTab != tabRouter {
		t.Fatalf("after ] at Timeline: currentTab = %d, want %d", m.currentTab, tabRouter)
	}

	// [ wraps from Router forward to Timeline.
	m = key(t, m, "[")
	if m.currentTab != tabTimeline {
		t.Fatalf("after [ at Router: currentTab = %d, want %d", m.currentTab, tabTimeline)
	}

	// Linear cycling via ].
	m = key(t, m, "]") // Timeline -> Router
	m = key(t, m, "]") // Router -> Worker
	if m.currentTab != tabWorker {
		t.Fatalf("after two ]: currentTab = %d, want %d", m.currentTab, tabWorker)
	}
}

func TestEventRouting(t *testing.T) {
	b := bus.NewChannelBus()
	defer b.Close()
	m := newSizedModel(t, "task-routing", b)

	now := time.Now()
	m = publishAndPump(t, m, b, &bus.ContextBuilt{TaskID: "task-routing", SnapshotSize: 128, Timestamp: now})
	m = publishAndPump(t, m, b, &bus.RouterExamining{TaskID: "task-routing", Entropy: 0.25, Timestamp: now})
	m = publishAndPump(t, m, b, &bus.RouterDecisionEvent{TaskID: "task-routing", Decision: bus.DecisionProceed, Rationale: "ok", Timestamp: now})
	m = publishAndPump(t, m, b, &bus.WorkerStarted{TaskID: "task-routing", WorktreePath: "/tmp/wt", BaseCommit: "HEAD", Retry: 0, Timestamp: now})
	m = publishAndPump(t, m, b, &bus.WorkerToolCall{TaskID: "task-routing", Tool: "write_file", Args: "x.txt", Timestamp: now})
	m = publishAndPump(t, m, b, &bus.WorkerFileEdit{TaskID: "task-routing", Path: "x.txt", Op: "create", DiffHunk: "+", Timestamp: now})
	m = publishAndPump(t, m, b, &bus.WorkerFinished{TaskID: "task-routing", Timestamp: now})
	m = publishAndPump(t, m, b, &bus.ConflictDetected{TaskID: "task-routing", Regions: []git.ConflictRegion{{FilePath: "x.txt"}}, Timestamp: now})
	m = publishAndPump(t, m, b, &bus.ValidatorExamining{TaskID: "task-routing", Criteria: []string{"compiles"}, Timestamp: now})
	m = publishAndPump(t, m, b, &bus.ValidatorCriterionResult{TaskID: "task-routing", Criterion: "compiles", Passed: true, Detail: "ok", Timestamp: now})
	m = publishAndPump(t, m, b, &bus.ValidatorVerdict{TaskID: "task-routing", Passes: true, Feedback: "LGTM", Timestamp: now})
	m = publishAndPump(t, m, b, &bus.TaskStateChanged{TaskID: "task-routing", From: state.StateCreated, To: state.StateContextBuilding, Timestamp: now})
	m = publishAndPump(t, m, b, &bus.TaskFinalized{TaskID: "task-routing", FinalState: state.StateCommitted, Timestamp: now})

	routerLines := m.router.Lines()
	if len(routerLines) != 3 {
		t.Fatalf("router lines = %v, want 3 entries", routerLines)
	}
	if !strings.Contains(routerLines[0], "Context built:") {
		t.Fatalf("router line 0 = %q, want Context built", routerLines[0])
	}
	if !strings.Contains(routerLines[2], "Router decision: PROCEED") {
		t.Fatalf("router line 2 = %q, want decision", routerLines[2])
	}

	workerLines := m.worker.Lines()
	if len(workerLines) != 5 {
		t.Fatalf("worker lines = %v, want 5 entries", workerLines)
	}
	if !strings.Contains(workerLines[0], "Worker started:") {
		t.Fatalf("worker line 0 = %q, want Worker started", workerLines[0])
	}
	if !strings.Contains(workerLines[1], "write_file") {
		t.Fatalf("worker line 1 = %q, want write_file tool call", workerLines[1])
	}
	if !strings.Contains(workerLines[2], "File edit: x.txt") {
		t.Fatalf("worker line 2 = %q, want File edit", workerLines[2])
	}
	if !strings.Contains(workerLines[3], "Worker finished") {
		t.Fatalf("worker line 3 = %q, want Worker finished", workerLines[3])
	}
	if !strings.Contains(workerLines[4], "Conflict detected: 1 region(s)") {
		t.Fatalf("worker line 4 = %q, want Conflict detected", workerLines[4])
	}

	validatorLines := m.validator.Lines()
	if len(validatorLines) != 3 {
		t.Fatalf("validator lines = %v, want 3 entries", validatorLines)
	}
	if !strings.Contains(validatorLines[1], "Criterion \"compiles\": PASS") {
		t.Fatalf("validator line 1 = %q, want Criterion result", validatorLines[1])
	}
	if !strings.Contains(validatorLines[2], "Verdict: PASS") {
		t.Fatalf("validator line 2 = %q, want Verdict", validatorLines[2])
	}

	// Timeline received all 13 events.
	timelineLines := m.timeline.Lines()
	if len(timelineLines) != 13 {
		t.Fatalf("timeline lines = %d, want 13", len(timelineLines))
	}

	// TaskFinalized finalizes the model and auto-switches to the Timeline tab.
	if !m.Finalized() {
		t.Fatalf("model should be finalized after TaskFinalized")
	}
	if m.FinalState() != state.StateCommitted {
		t.Fatalf("final state = %q, want %q", m.FinalState(), state.StateCommitted)
	}
	if m.currentTab != tabTimeline {
		t.Fatalf("currentTab = %d, want %d (Timeline)", m.currentTab, tabTimeline)
	}
}

func TestTaskStateChangedUpdatesHeader(t *testing.T) {
	b := bus.NewChannelBus()
	defer b.Close()
	m := newSizedModel(t, "task-hdr", b)

	m = publishAndPump(t, m, b, &bus.TaskStateChanged{
		TaskID:    "task-hdr",
		From:      state.StateCreated,
		To:        state.StateWorkerRunning,
		Timestamp: time.Now(),
	})

	if m.header.state != state.StateWorkerRunning {
		t.Fatalf("header state = %q, want %q", m.header.state, state.StateWorkerRunning)
	}
}

func TestTaskFinalizedAutoSwitchesToTimeline(t *testing.T) {
	b := bus.NewChannelBus()
	defer b.Close()
	m := newSizedModel(t, "task-fin", b)

	// Force a non-Timeline tab to be active first so the auto-switch is
	// observable.
	m = key(t, m, "1") // Router

	m = publishAndPump(t, m, b, &bus.TaskFinalized{
		TaskID:     "task-fin",
		FinalState: state.StateCommitted,
		Timestamp:  time.Now(),
	})

	if !m.Finalized() {
		t.Fatalf("model should be finalized after TaskFinalized")
	}
	if m.FinalState() != state.StateCommitted {
		t.Fatalf("final state = %q, want %q", m.FinalState(), state.StateCommitted)
	}
	if m.currentTab != tabTimeline {
		t.Fatalf("currentTab = %d, want %d (Timeline)", m.currentTab, tabTimeline)
	}
}

// TestCompletionFooter verifies that the Timeline tab renders the completion
// footer (final state + output reference) once TaskFinalized arrives. This
// guards against the B1 regression where state was collected but never read.
func TestCompletionFooter(t *testing.T) {
	b := bus.NewChannelBus()
	defer b.Close()
	m := newSizedModel(t, "task-foot", b)

	// Drive a non-trivial state transition first so the timeline body is
	// non-empty and the footer is visually distinct from the scroll region.
	m = publishAndPump(t, m, b, &bus.TaskStateChanged{
		TaskID:    "task-foot",
		From:      state.StateCreated,
		To:        state.StateWorkerRunning,
		Timestamp: time.Now(),
	})

	m = publishAndPump(t, m, b, &bus.TaskFinalized{
		TaskID:         "task-foot",
		FinalState:     state.StateFailedEscalated,
		FinalOutputRef: "diff --git a/x b/y @@ ...",
		Timestamp:      time.Now(),
	})

	if !m.Finalized() {
		t.Fatalf("model should be finalized after TaskFinalized")
	}
	if m.currentTab != tabTimeline {
		t.Fatalf("currentTab = %d, want Timeline so the footer is visible", m.currentTab)
	}

	out := stripANSI(m.View())
	if !strings.Contains(out, "FINAL: FAILED_ESCALATED") {
		t.Fatalf("View() missing final state footer line\n--- got ---\n%s", out)
	}
	if !strings.Contains(out, "y") {
		t.Fatalf("View() missing output reference in footer\n--- got ---\n%s", out)
	}

	// The footer must render below the timeline body, so both the body line
	// and the footer should be present simultaneously.
	if !strings.Contains(out, "WORKER_RUNNING") {
		t.Fatalf("View() missing timeline body content alongside footer\n--- got ---\n%s", out)
	}

	// The header should reflect finalization by swapping the animated spinner
	// for a static "done" marker (guards Header.finalized against becoming a
	// dead write again).
	if !strings.Contains(out, "done") {
		t.Fatalf("View() missing finalized marker in header\n--- got ---\n%s", out)
	}
}

// TestCompletionFooterNoOutputRef verifies the footer degrades to a single
// state line when FinalOutputRef is empty, rather than printing a dangling
// "output:" label.
func TestCompletionFooterNoOutputRef(t *testing.T) {
	b := bus.NewChannelBus()
	defer b.Close()
	m := newSizedModel(t, "task-foot-empty", b)

	m = publishAndPump(t, m, b, &bus.TaskFinalized{
		TaskID:     "task-foot-empty",
		FinalState: state.StateCommitted,
		Timestamp:  time.Now(),
	})

	out := stripANSI(m.View())
	if !strings.Contains(out, "FINAL: COMMITTED") {
		t.Fatalf("View() missing final state footer line\n--- got ---\n%s", out)
	}
	if strings.Contains(out, "output:") {
		t.Fatalf("View() should not render output line when ref is empty\n--- got ---\n%s", out)
	}
}

func TestBusChannelClosedFinalizes(t *testing.T) {
	b := bus.NewChannelBus()
	m := newSizedModel(t, "task-close", b)

	// Close the bus; the next read returns a closed-channel zero value.
	b.Close()
	ev, ok := <-m.eventCh
	if ev != nil || ok {
		t.Fatalf("expected closed channel read, got ev=%v ok=%v", ev, ok)
	}
	updated, cmd := m.Update(busChannelClosedMsg{})
	m = updated.(Model)

	if !m.Finalized() {
		t.Fatalf("model should be finalized after channel close")
	}
	if m.currentTab != tabTimeline {
		t.Fatalf("currentTab = %d, want Timeline after close", m.currentTab)
	}
	if cmd != nil {
		t.Fatalf("channel-closed handler should not keep pumping; got cmd=%v", cmd)
	}
}

func TestQuitOnQ(t *testing.T) {
	b := bus.NewChannelBus()
	defer b.Close()
	m := newSizedModel(t, "task-quit", b)

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	m = updated.(Model)

	if !m.quitting {
		t.Fatalf("quitting flag not set after q")
	}
	if cmd == nil {
		t.Fatalf("Update returned nil command for q; want tea.Quit")
	}
	// Execute the command: it should produce a quit message. tea.Quit returns
	// a tea.QuitMsg (exported) in current Bubble Tea; the older unexported
	// quitMsg name is accepted too in case of a downstream version change.
	msg := cmd()
	if msg == nil {
		t.Fatalf("quit command returned nil message")
	}
	got := typeName(msg)
	if got != "quitMsg" && got != "QuitMsg" {
		t.Fatalf("quit cmd produced %s, want a quit message", got)
	}
}

func TestQuitOnCtrlC(t *testing.T) {
	b := bus.NewChannelBus()
	defer b.Close()
	m := newSizedModel(t, "task-ctrlc", b)

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = updated.(Model)

	if !m.quitting {
		t.Fatalf("quitting flag not set after Ctrl+C")
	}
	if cmd == nil {
		t.Fatalf("Update returned nil command for Ctrl+C; want tea.Quit")
	}
	// Execute the command: it should produce a quit message, just like the q
	// keybinding tested above.
	msg := cmd()
	if msg == nil {
		t.Fatalf("quit command returned nil message")
	}
	got := typeName(msg)
	if got != "quitMsg" && got != "QuitMsg" {
		t.Fatalf("quit cmd produced %s, want a quit message", got)
	}
}

// TestOrchestratorErrorRoutesToTimeline verifies that non-fatal orchestrator
// errors are surfaced in the Timeline tab rather than silently dropped.
func TestOrchestratorErrorRoutesToTimeline(t *testing.T) {
	b := bus.NewChannelBus()
	defer b.Close()
	m := newSizedModel(t, "task-err", b)

	m = publishAndPump(t, m, b, &bus.OrchestratorError{
		TaskID:    "task-err",
		Error:     "worktree provision failed",
		Timestamp: time.Now(),
	})

	lines := m.timeline.Lines()
	if len(lines) != 1 {
		t.Fatalf("timeline lines = %d, want 1", len(lines))
	}
	if !strings.Contains(lines[0], "worktree provision failed") {
		t.Fatalf("timeline line = %q, want error text", lines[0])
	}
}

func TestViewRendering(t *testing.T) {
	b := bus.NewChannelBus()
	defer b.Close()
	m := newSizedModel(t, "task-view", b)

	m = publishAndPump(t, m, b, &bus.TaskStateChanged{
		TaskID:    "task-view",
		From:      state.StateCreated,
		To:        state.StateWorkerRunning,
		Timestamp: time.Now(),
	})
	m = publishAndPump(t, m, b, &bus.WorkerStarted{
		TaskID:       "task-view",
		WorktreePath: "/tmp/wt",
		BaseCommit:   "HEAD",
		Retry:        1,
		Timestamp:    time.Now(),
	})

	out := stripANSI(m.View())

	wantSubstrings := []string{
		"limen",
		"task-view",
		"WORKER_RUNNING",
		"retries:1",
		"Router",
		"Worker",
		"Validator",
		"Timeline",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(out, want) {
			t.Fatalf("View() missing %q\n--- got ---\n%s", want, out)
		}
	}
}

func TestRouterTabContentVisibleWhenActive(t *testing.T) {
	b := bus.NewChannelBus()
	defer b.Close()
	m := newSizedModel(t, "task-router", b)

	m = publishAndPump(t, m, b, &bus.RouterExamining{
		TaskID: "task-router", Entropy: 0.5, Timestamp: time.Now(),
	})
	m = key(t, m, "1") // ensure Router is active
	out := stripANSI(m.View())
	if !strings.Contains(out, "Router examining:") {
		t.Fatalf("Router tab content not visible in View(): %s", out)
	}
}

// typeName returns the short type name of an arbitrary value, used to validate
// that a quit command yields a quit message without depending on Bubble Tea's
// unexported concrete type.
func typeName(v tea.Msg) string {
	full := fmt.Sprintf("%T", v)
	// Trim the package qualifier if present.
	if idx := strings.Index(full, "."); idx >= 0 {
		return full[idx+1:]
	}
	return full
}

func TestNewUIImprovements(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	line := theme.FormatEventLine(time.Now(), "verdict: PASS — check")
	if !strings.Contains(line, "PASS") {
		t.Fatalf("FormatEventLine missing PASS keyword")
	}
	if !strings.Contains(line, "\x1b[") {
		t.Fatalf("FormatEventLine missing ANSI styling: %q", line)
	}

	h := NewHeader("task-1")
	h.finalized = true
	h.state = state.StateCommitted
	h.width = 80
	hdrView := h.View()
	if !strings.Contains(stripANSI(hdrView), "✓") {
		t.Fatalf("Header View did not render tick: %q", stripANSI(hdrView))
	}

	b := bus.NewChannelBus()
	defer b.Close()
	m := newSizedModel(t, "task-str", b)
	m = publishAndPump(t, m, b, &bus.TaskFinalized{
		TaskID:     "task-str",
		FinalState: state.StateCommitted,
		Timestamp:  time.Now(),
	})
	strVal := m.String()
	if !strings.Contains(stripANSI(strVal), "limen") || !strings.Contains(stripANSI(strVal), "FINAL: COMMITTED") {
		t.Fatalf("Model.String() did not render header and timeline: %q", stripANSI(strVal))
	}
}