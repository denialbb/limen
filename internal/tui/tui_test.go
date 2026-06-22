package tui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/denialbb/limen/internal/bus"
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
	if m.header == nil || m.tabStrip == nil || m.router == nil ||
		m.worker == nil || m.validator == nil || m.timeline == nil {
		t.Fatalf("sub-components not initialized")
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
	m = publishAndPump(t, m, b, &bus.ValidatorExamining{TaskID: "task-routing", Criteria: []string{"compiles"}, Timestamp: now})
	m = publishAndPump(t, m, b, &bus.ValidatorCriterionResult{TaskID: "task-routing", Criterion: "compiles", Passed: true, Detail: "ok", Timestamp: now})
	m = publishAndPump(t, m, b, &bus.TaskStateChanged{TaskID: "task-routing", From: state.StateCreated, To: state.StateContextBuilding, Timestamp: now})

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
	if len(workerLines) != 2 {
		t.Fatalf("worker lines = %v, want 2 entries", workerLines)
	}
	if !strings.Contains(workerLines[0], "Worker started:") {
		t.Fatalf("worker line 0 = %q, want Worker started", workerLines[0])
	}
	if !strings.Contains(workerLines[1], "Tool call: write_file") {
		t.Fatalf("worker line 1 = %q, want Tool call", workerLines[1])
	}

	validatorLines := m.validator.Lines()
	if len(validatorLines) != 2 {
		t.Fatalf("validator lines = %v, want 2 entries", validatorLines)
	}
	if !strings.Contains(validatorLines[1], "Criterion \"compiles\": PASS") {
		t.Fatalf("validator line 1 = %q, want Criterion result", validatorLines[1])
	}

	// Timeline received all 8 events.
	timelineLines := m.timeline.Lines()
	if len(timelineLines) != 8 {
		t.Fatalf("timeline lines = %d, want 8", len(timelineLines))
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
		"r:1",
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