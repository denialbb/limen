package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/denialbb/limen/internal/bus"
	"github.com/denialbb/limen/internal/state"
	"github.com/denialbb/limen/internal/tui/tabs"
)

// component_test.go verifies the value-receiver Bubble Tea component contract
// directly: each sub-component's Init/Update/SetWidth/SetSize methods return a
// NEW value rather than mutating the receiver in place, following the idiomatic
// Bubble Tea / bubbles convention. Integration-level behavior (event routing,
// tab switching, full-screen rendering) is covered by tui_test.go; this file
// asserts the value-semantics invariant itself, which is what the recent
// pointer->value refactor introduced.
//
// NOTE: Header and TabStrip live in package tui, so these tests can read their
// unexported fields directly. The tab types (RouterTab, WorkerTab,
// ValidatorTab, TimelineTab) live in package tabs, so their unexported
// viewport/lines fields are NOT accessible here; for those we verify value
// semantics through the exported Lines/View surface and observable rendering
// behavior instead.

// ---------------------------------------------------------------------------
// Header
// ---------------------------------------------------------------------------

func TestHeaderInitReturnsNil(t *testing.T) {
	h := NewHeader("test")
	if h.Init() != nil {
		t.Fatalf("Header.Init() = %v, want nil", h.Init())
	}
}

func TestHeaderUpdateReturnsNewValue(t *testing.T) {
	h := NewHeader("test")
	h2, cmd := h.Update(tabs.EventMsg{Event: &bus.TaskStateChanged{
		To:        state.StateWorkerRunning,
		Timestamp: time.Now(),
	}})

	if h2.state != state.StateWorkerRunning {
		t.Fatalf("returned Header.state = %q, want %q", h2.state, state.StateWorkerRunning)
	}
	// Value semantics: the original receiver must not be mutated by Update.
	if h.state != state.StateCreated {
		t.Fatalf("original Header.state = %q, want %q (Update must not mutate the receiver)",
			h.state, state.StateCreated)
	}
	if cmd != nil {
		t.Fatalf("Header.Update cmd = %v, want nil", cmd)
	}
}

func TestHeaderSetWidthReturnsNewValue(t *testing.T) {
	h := NewHeader("test")
	h2 := h.SetWidth(80)

	if h2.width != 80 {
		t.Fatalf("returned Header.width = %d, want 80", h2.width)
	}
	if h.width != 0 {
		t.Fatalf("original Header.width = %d, want 0 (SetWidth must not mutate the receiver)",
			h.width)
	}
}

func TestHeaderUpdateIgnoresNonEventMsg(t *testing.T) {
	h := NewHeader("test")
	// Header.Update only reacts to tabs.EventMsg; any other tea.Msg must be a
	// no-op that returns the receiver unchanged with a nil command.
	msgs := []tea.Msg{
		tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")},
		tea.WindowSizeMsg{Width: 80, Height: 24},
	}
	for i, msg := range msgs {
		h2, cmd := h.Update(msg)
		// Header has only comparable fields, so == proves the whole value is
		// unchanged (not just the fields we think to check).
		if h2 != h {
			t.Fatalf("msg %d (%T): returned Header differs from original: got %+v, want %+v",
				i, msg, h2, h)
		}
		if cmd != nil {
			t.Fatalf("msg %d (%T): Update cmd = %v, want nil", i, msg, cmd)
		}
	}
}

// ---------------------------------------------------------------------------
// TabStrip
// ---------------------------------------------------------------------------

func TestTabStripInitReturnsNil(t *testing.T) {
	ts := NewTabStrip()
	if ts.Init() != nil {
		t.Fatalf("TabStrip.Init() = %v, want nil", ts.Init())
	}
}

func TestTabStripSetActiveReturnsNewValue(t *testing.T) {
	ts := NewTabStrip()

	ts2 := ts.SetActive(2)
	if ts2.Active() != 2 {
		t.Fatalf("returned TabStrip.Active() = %d, want 2", ts2.Active())
	}
	if ts.Active() != 0 {
		t.Fatalf("original TabStrip.Active() = %d, want 0 (SetActive must not mutate the receiver)",
			ts.Active())
	}

	// Out-of-range indices are a no-op: the receiver is returned unchanged.
	if ts3 := ts.SetActive(-1); ts3.Active() != 0 {
		t.Fatalf("SetActive(-1).Active() = %d, want 0 (no-op for out-of-range)", ts3.Active())
	}
	if ts4 := ts.SetActive(99); ts4.Active() != 0 {
		t.Fatalf("SetActive(99).Active() = %d, want 0 (no-op for out-of-range)", ts4.Active())
	}
}

func TestTabStripSetSizeReturnsNewValue(t *testing.T) {
	ts := NewTabStrip()
	ts2 := ts.SetSize(100)

	// Both the width>0 and width=0 render paths must complete without panicking.
	_ = ts2.View()
	_ = ts.View()

	if ts2.width != 100 {
		t.Fatalf("returned TabStrip.width = %d, want 100", ts2.width)
	}
	if ts.width != 0 {
		t.Fatalf("original TabStrip.width = %d, want 0 (SetSize must not mutate the receiver)",
			ts.width)
	}
}

// ---------------------------------------------------------------------------
// Tabs (package tabs)
//
// The tab types live in package tabs, so their unexported viewport/lines fields
// are not visible from this package-level test. Value semantics are verified
// through the exported Lines/View surface and observable rendering instead.
// ---------------------------------------------------------------------------

func TestTabInitReturnsNil(t *testing.T) {
	cases := []struct {
		name string
		init tea.Cmd
	}{
		{"RouterTab", tabs.NewRouterTab().Init()},
		{"WorkerTab", tabs.NewWorkerTab().Init()},
		{"ValidatorTab", tabs.NewValidatorTab().Init()},
		{"TimelineTab", tabs.NewTimelineTab().Init()},
	}
	for _, c := range cases {
		if c.init != nil {
			t.Fatalf("%s Init() = %v, want nil", c.name, c.init)
		}
	}
}

func TestTabUpdateReturnsNewValue(t *testing.T) {
	r := tabs.NewRouterTab().SetSize(80, 24)
	r2, cmd := r.Update(tabs.EventMsg{Event: &bus.ContextBuilt{
		SnapshotSize: 128,
		Timestamp:    time.Now(),
	}})

	if lines := r2.Lines(); len(lines) != 1 {
		t.Fatalf("returned RouterTab Lines() = %d, want 1: %v", len(lines), lines)
	} else if !strings.Contains(lines[0], "Context built") {
		t.Fatalf("returned RouterTab line 0 = %q, want it to contain \"Context built\"", lines[0])
	}
	// Value semantics: the original tab must still be empty after Update.
	if lines := r.Lines(); len(lines) != 0 {
		t.Fatalf("original RouterTab Lines() = %d, want 0 (Update must not mutate the receiver): %v",
			len(lines), lines)
	}
	if cmd != nil {
		t.Fatalf("RouterTab.Update cmd = %v, want nil", cmd)
	}
}

// TestTabSetSizeReturnsNewValue verifies SetSize's value semantics through
// observable rendering. The tab is seeded with three lines of content first so
// the viewport's height is visible through View(): the default 1x1 viewport
// wraps each line to a single character and shows only one of them, while an
// 80x24 viewport shows the full body. If SetSize mutated the receiver in place,
// the original would render the full body too.
func TestTabSetSizeReturnsNewValue(t *testing.T) {
	now := time.Now()
	r := tabs.NewRouterTab()
	// Seed three router-relevant lines so the body spans more than one line.
	r, _ = r.Update(tabs.EventMsg{Event: &bus.ContextBuilt{SnapshotSize: 128, Timestamp: now}})
	r, _ = r.Update(tabs.EventMsg{Event: &bus.RouterExamining{Entropy: 0.25, Timestamp: now}})
	r, _ = r.Update(tabs.EventMsg{Event: &bus.RouterDecisionEvent{
		Decision:  bus.DecisionProceed,
		Rationale: "ok",
		Timestamp: now,
	}})

	// r still has its default 1x1 viewport (NewRouterTab -> viewport.New(1, 1)).
	viewBefore := r.View()

	// SetSize must return a new value with the resized viewport, leaving r
	// untouched.
	r2 := r.SetSize(80, 24)

	// Value semantics: r's rendering is unchanged by the SetSize call.
	if viewAfter := r.View(); viewAfter != viewBefore {
		t.Fatalf("SetSize mutated the original: r.View() before = %q, after = %q",
			viewBefore, viewAfter)
	}

	// r2's 24-tall viewport shows the full body: all three markers are visible.
	r2View := stripANSI(r2.View())
	for _, want := range []string{"Context built", "Router examining", "Router decision"} {
		if !strings.Contains(r2View, want) {
			t.Fatalf("r2.View() missing %q after SetSize(80,24)\n--- got ---\n%s", want, r2View)
		}
	}

	// r's 1-tall viewport shows only a single (wrapped) visual line, so at most
	// one of the three markers can be present. If SetSize had mutated r, all
	// three would appear here too.
	rView := stripANSI(r.View())
	found := 0
	for _, marker := range []string{"Context built", "Router examining", "Router decision"} {
		if strings.Contains(rView, marker) {
			found++
		}
	}
	if found > 1 {
		t.Fatalf("original r.View() shows %d markers, want <= 1 (1-tall viewport); "+
			"SetSize likely mutated the receiver\n--- got ---\n%s", found, rView)
	}
}

func TestTimelineUpdateReturnsNewValue(t *testing.T) {
	tt := tabs.NewTimelineTab().SetSize(80, 24)
	tt2, cmd := tt.Update(tabs.EventMsg{Event: &bus.TaskStateChanged{
		From:      state.StateCreated,
		To:        state.StateWorkerRunning,
		Timestamp: time.Now(),
	}})

	if lines := tt2.Lines(); len(lines) != 1 {
		t.Fatalf("returned TimelineTab Lines() = %d, want 1: %v", len(lines), lines)
	} else if !strings.Contains(lines[0], "state:") {
		t.Fatalf("returned TimelineTab line 0 = %q, want it to contain \"state:\"", lines[0])
	}
	if lines := tt.Lines(); len(lines) != 0 {
		t.Fatalf("original TimelineTab Lines() = %d, want 0 (Update must not mutate the receiver): %v",
			len(lines), lines)
	}
	if cmd != nil {
		t.Fatalf("TimelineTab.Update cmd = %v, want nil", cmd)
	}
}

// TestTimelineFinalizedReservesFooterSpace verifies that a finalization event
// renders the completion footer as a single "FINAL: <state>" line when no
// output reference was recorded.
//
// NOTE: TimelineTab.footerLineCount is unexported in package tabs, so it cannot
// be called directly from this package-level test. We instead verify its
// observable consequence: the footer renders exactly one line ("FINAL:
// COMMITTED") with no "output:" label, which is the rendering behavior that
// footerLineCount==1 produces (a non-empty FinalOutputRef would add a second
// "output:" line and make footerLineCount return 2).
func TestTimelineFinalizedReservesFooterSpace(t *testing.T) {
	tt := tabs.NewTimelineTab().SetSize(80, 24)
	tt2, cmd := tt.Update(tabs.EventMsg{Event: &bus.TaskFinalized{
		FinalState: state.StateCommitted,
		Timestamp:  time.Now(),
	}})

	if cmd != nil {
		t.Fatalf("TimelineTab.Update cmd = %v, want nil", cmd)
	}

	out := stripANSI(tt2.View())
	if !strings.Contains(out, "FINAL: COMMITTED") {
		t.Fatalf("View() missing final-state footer\n--- got ---\n%s", out)
	}
	// No FinalOutputRef was recorded, so the footer is a single line and must
	// not render the "output:" label. This is the observable proof that
	// footerLineCount() == 1 rather than 2.
	if strings.Contains(out, "output:") {
		t.Fatalf("View() should not render an output line when FinalOutputRef is empty\n--- got ---\n%s", out)
	}

	// Value semantics: the original tab was not mutated by Update.
	if lines := tt.Lines(); len(lines) != 0 {
		t.Fatalf("original TimelineTab Lines() = %d, want 0 (Update must not mutate the receiver): %v",
			len(lines), lines)
	}
	if origOut := stripANSI(tt.View()); strings.Contains(origOut, "FINAL:") {
		t.Fatalf("original View() should not render the footer (Update must not mutate the receiver): %q",
			origOut)
	}
}

// ---------------------------------------------------------------------------
// bus.Event.Time() interface method
// ---------------------------------------------------------------------------

// TestEventTimeMethod verifies that every event type's Time() returns its
// embedded Timestamp, exercising the interface method polymorphically across
// several concrete event types.
func TestEventTimeMethod(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		ev   bus.Event
	}{
		{
			name: "TaskStateChanged",
			ev: &bus.TaskStateChanged{
				From:      state.StateCreated,
				To:        state.StateWorkerRunning,
				Timestamp: now,
			},
		},
		{
			name: "ContextBuilt",
			ev: &bus.ContextBuilt{
				SnapshotSize: 128,
				Timestamp:    now,
			},
		},
		{
			name: "TaskFinalized",
			ev: &bus.TaskFinalized{
				FinalState: state.StateCommitted,
				Timestamp:  now,
			},
		},
	}
	for _, c := range cases {
		if got := c.ev.Time(); !got.Equal(now) {
			t.Fatalf("%s Time() = %v, want %v", c.name, got, now)
		}
	}
}
