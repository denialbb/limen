package bus_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/denialbb/limen/internal/bus"
	"github.com/denialbb/limen/internal/git"
	"github.com/denialbb/limen/internal/orchestrator"
	"github.com/denialbb/limen/internal/state"
)

// recvEvent reads a single event from ch or fails the test after a
// timeout. It returns the received event for assertion.
func recvEvent(t *testing.T, ch <-chan bus.Event) bus.Event {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(time.Second):
		t.Fatal("did not receive expected event within timeout")
		return nil
	}
}

func TestChannelBus_PublishSubscribe(t *testing.T) {
	b := bus.NewChannelBus()
	ch := b.Subscribe()

	ev := &bus.TaskStateChanged{
		TaskID:    "ts-1",
		From:      state.StateCreated,
		To:        state.StateContextBuilding,
		Timestamp: time.Now(),
	}
	b.Publish(ev)

	got := recvEvent(t, ch)
	if got != ev {
		t.Fatalf("received %p, want %p", got, ev)
	}
	b.Close()
}

func TestChannelBus_MultipleSubscribers(t *testing.T) {
	b := bus.NewChannelBus()
	ch1 := b.Subscribe()
	ch2 := b.Subscribe()

	ev := &bus.TaskStateChanged{
		TaskID:    "multi",
		From:      state.StateCreated,
		To:        state.StateContextBuilding,
		Timestamp: time.Now(),
	}
	b.Publish(ev)

	// Both subscribers must receive the same event value.
	for i, ch := range []<-chan bus.Event{ch1, ch2} {
		got := recvEvent(t, ch)
		if got != ev {
			t.Fatalf("subscriber %d received %p, want %p", i, got, ev)
		}
	}
	b.Close()
}

// TestChannelBus_BlockingPublish verifies the documented backpressure
// contract: when a subscriber's buffer is full, Publish blocks (rather
// than dropping), and the blocked event is delivered once space frees up.
func TestChannelBus_BlockingPublish(t *testing.T) {
	b := bus.NewChannelBus()
	ch := b.Subscribe()

	// Fill the subscriber buffer to capacity without draining.
	for i := 0; i < bus.SubscriberBufferSize; i++ {
		b.Publish(&bus.TaskStateChanged{TaskID: "fill", Timestamp: time.Now()})
	}

	// The next publish must block because the buffer is full.
	blocked := &bus.TaskStateChanged{TaskID: "blocked", Timestamp: time.Now()}
	done := make(chan struct{})
	go func() {
		b.Publish(blocked)
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("Publish completed without blocking on a full buffer; no-drop contract violated")
	case <-time.After(50 * time.Millisecond):
		// Expected: Publish is blocked awaiting buffer space.
	}

	// Drain in a goroutine, collecting events for later assertion.
	var drained []bus.Event
	drainDone := make(chan struct{})
	go func() {
		for ev := range ch {
			drained = append(drained, ev)
		}
		close(drainDone)
	}()

	select {
	case <-done:
		// Blocked publish unblocked once draining freed buffer space.
	case <-time.After(time.Second):
		t.Fatal("blocked Publish did not complete after draining started")
	}

	b.Close()
	<-drainDone

	want := bus.SubscriberBufferSize + 1
	if len(drained) != want {
		t.Fatalf("expected %d events, got %d (event dropped)", want, len(drained))
	}
	last, ok := drained[len(drained)-1].(*bus.TaskStateChanged)
	if !ok || last.TaskID != "blocked" {
		t.Fatalf("last drained event was not the blocked publish: %+v", drained[len(drained)-1])
	}
}

func TestChannelBus_Close(t *testing.T) {
	b := bus.NewChannelBus()
	ch := b.Subscribe()
	b.Close()

	// The subscriber channel must be closed (receive returns
	// immediately with ok == false).
	select {
	case ev, ok := <-ch:
		if ok {
			t.Fatalf("expected closed channel, received event: %+v", ev)
		}
	default:
		t.Fatal("subscriber channel was not closed by Close")
	}

	// Publish after Close must be a no-op, not a panic.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Publish after Close panicked: %v", r)
		}
	}()
	b.Publish(&bus.TaskStateChanged{TaskID: "post-close", Timestamp: time.Now()})
}

// TestChannelBus_CloseIsIdempotent verifies that a second Close call is
// safe and does not double-close subscriber channels.
func TestChannelBus_CloseIsIdempotent(t *testing.T) {
	b := bus.NewChannelBus()
	_ = b.Subscribe()
	b.Close()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("second Close panicked: %v", r)
		}
	}()
	b.Close()
}

// TestChannelBus_SubscribeAfterClose documents the defensive contract:
// subscribing after Close returns an already-closed channel rather than
// a stream that will never deliver.
func TestChannelBus_SubscribeAfterClose(t *testing.T) {
	b := bus.NewChannelBus()
	b.Close()
	ch := b.Subscribe()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("Subscribe after Close should return a closed channel")
		}
	default:
		t.Fatal("Subscribe after Close should return a closed channel")
	}
}

// TestChannelBus_ConcurrentPublish uses concurrent publishers and a
// single subscriber to verify that no events are lost or duplicated.
func TestChannelBus_ConcurrentPublish(t *testing.T) {
	b := bus.NewChannelBus()
	ch := b.Subscribe()

	const publishers = 8
	const perPublisher = 200
	var wg sync.WaitGroup
	wg.Add(publishers)
	for p := 0; p < publishers; p++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < perPublisher; i++ {
				b.Publish(&bus.WorkerToolCall{
					TaskID:    "concurrent",
					Tool:      "echo",
					Args:      fmt.Sprintf("p=%d i=%d", id, i),
					Timestamp: time.Now(),
				})
			}
		}(p)
	}

	var received []bus.Event
	collectorDone := make(chan struct{})
	go func() {
		for ev := range ch {
			received = append(received, ev)
		}
		close(collectorDone)
	}()

	wg.Wait()
	b.Close()
	<-collectorDone

	want := publishers * perPublisher
	if len(received) != want {
		t.Fatalf("expected %d events, got %d (events lost)", want, len(received))
	}

	// Verify no duplicates were synthesized and none lost by counting
	// unique (publisher, index) pairs.
	seen := make(map[string]bool, want)
	for _, ev := range received {
		tc, ok := ev.(*bus.WorkerToolCall)
		if !ok {
			t.Fatalf("unexpected event type: %T", ev)
		}
		if seen[tc.Args] {
			t.Fatalf("duplicate event detected: %s", tc.Args)
		}
		seen[tc.Args] = true
	}
}

func TestRecorderEmitter_Records(t *testing.T) {
	rec := bus.NewRecorderEmitter()
	now := time.Now()
	evs := []bus.Event{
		&bus.TaskStateChanged{TaskID: "t1", Timestamp: now},
		&bus.WorkerToolCall{TaskID: "t1", Tool: "ls", Timestamp: now},
		&bus.TaskStateChanged{TaskID: "t2", Timestamp: now},
		&bus.WorkerFinished{TaskID: "t1", Timestamp: now},
	}
	for _, ev := range evs {
		rec.Publish(ev)
	}

	got := rec.Events()
	if len(got) != len(evs) {
		t.Fatalf("Events() len = %d, want %d", len(got), len(evs))
	}
	for i, e := range got {
		if e != evs[i] {
			t.Fatalf("Events()[%d] = %p, want %p", i, e, evs[i])
		}
	}

	if n := len(rec.EventsByKind("TaskStateChanged")); n != 2 {
		t.Fatalf("EventsByKind(TaskStateChanged) = %d, want 2", n)
	}
	if n := len(rec.EventsByKind("WorkerFinished")); n != 1 {
		t.Fatalf("EventsByKind(WorkerFinished) = %d, want 1", n)
	}
	if n := len(rec.EventsByKind("DoesNotExist")); n != 0 {
		t.Fatalf("EventsByKind(unknown) = %d, want 0", n)
	}

	// Mutating the returned slice must not corrupt internal state:
	// Events() must return a defensive copy.
	got[0] = nil
	again := rec.Events()
	if again[0] == nil {
		t.Fatal("Events() returned a slice sharing storage with internal state")
	}
}

// TestRecorderEmitter_Concurrent verifies that concurrent publishes are
// safely recorded without lost writes.
func TestRecorderEmitter_Concurrent(t *testing.T) {
	rec := bus.NewRecorderEmitter()
	const goroutines = 16
	const perG = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				rec.Publish(&bus.WorkerToolCall{
					TaskID:    "c",
					Tool:      "t",
					Args:      fmt.Sprintf("g=%d i=%d", id, i),
					Timestamp: time.Now(),
				})
			}
		}(g)
	}
	wg.Wait()

	want := goroutines * perG
	if got := len(rec.Events()); got != want {
		t.Fatalf("recorded %d events, want %d (lost writes)", got, want)
	}
}

// TestRecorderEmitter_AsEventSink verifies the recorder satisfies the
// EventSink contract when used through the narrow interface.
func TestRecorderEmitter_AsEventSink(t *testing.T) {
	var sink bus.EventSink = bus.NewRecorderEmitter()
	now := time.Now()
	sink.Publish(&bus.TaskStateChanged{TaskID: "sink", Timestamp: now})

	// Re-assert via the concrete type to inspect recorded state.
	rec := sink.(*bus.RecorderEmitter)
	if n := len(rec.Events()); n != 1 {
		t.Fatalf("Events() via EventSink = %d, want 1", n)
	}
}

// TestEventKinds verifies that each event type's kind() returns the
// expected taxonomy string. kind() is unexported, so the assertion is
// made indirectly through RecorderEmitter.EventsByKind, which filters
// by kind() internally.
func TestEventKinds(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		ev   bus.Event
		kind string
	}{
		{"TaskStateChanged", &bus.TaskStateChanged{Timestamp: now}, "TaskStateChanged"},
		{"ContextBuilt", &bus.ContextBuilt{Timestamp: now}, "ContextBuilt"},
		{"RouterExamining", &bus.RouterExamining{Timestamp: now}, "RouterExamining"},
		{"RouterDecisionEvent", &bus.RouterDecisionEvent{Timestamp: now}, "RouterDecision"},
		{"WorkerStarted", &bus.WorkerStarted{Timestamp: now}, "WorkerStarted"},
		{"WorkerToolCall", &bus.WorkerToolCall{Timestamp: now}, "WorkerToolCall"},
		{"WorkerFileEdit", &bus.WorkerFileEdit{Timestamp: now}, "WorkerFileEdit"},
		{"WorkerFinished", &bus.WorkerFinished{Timestamp: now}, "WorkerFinished"},
		{"ValidatorExamining", &bus.ValidatorExamining{Timestamp: now}, "ValidatorExamining"},
		{"ValidatorCriterionResult", &bus.ValidatorCriterionResult{Timestamp: now}, "ValidatorCriterionResult"},
		{"ValidatorVerdict", &bus.ValidatorVerdict{Timestamp: now}, "ValidatorVerdict"},
		{"ConflictDetected", &bus.ConflictDetected{Timestamp: now}, "ConflictDetected"},
		{"TaskFinalized", &bus.TaskFinalized{Timestamp: now}, "TaskFinalized"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := bus.NewRecorderEmitter()
			rec.Publish(c.ev)
			got := rec.EventsByKind(c.kind)
			if len(got) != 1 {
				t.Fatalf("EventsByKind(%q) = %d events, want 1", c.kind, len(got))
			}
			if got[0] != c.ev {
				t.Fatalf("EventsByKind(%q) returned %p, want %p", c.kind, got[0], c.ev)
			}
		})
	}
}

// TestRouterDecisionMirrorsOrchestrator verifies the contract that the
// bus-local RouterDecision constants share string values with the
// orchestrator package, so cross-package conversions preserve semantics.
//
// NOTE: bus cannot import orchestrator (cycle: orchestrator imports
// bus), but the external test package bus_test can, since nothing
// imports bus_test. This test is the guard that keeps the mirror in sync.
func TestRouterDecisionMirrorsOrchestrator(t *testing.T) {
	cases := []struct {
		busConst  bus.RouterDecision
		orchConst orchestrator.RouterDecision
		want      string
	}{
		{bus.DecisionProceed, orchestrator.DecisionProceed, "PROCEED"},
		{bus.DecisionExpand, orchestrator.DecisionExpand, "EXPAND"},
		{bus.DecisionEscalate, orchestrator.DecisionEscalate, "ESCALATE"},
	}
	for _, c := range cases {
		if got := string(c.busConst); got != c.want {
			t.Fatalf("bus constant = %q, want %q", got, c.want)
		}
		if got := string(c.orchConst); got != c.want {
			t.Fatalf("orchestrator constant = %q, want %q", got, c.want)
		}
		// The cross-package conversion that producers must perform.
		converted := bus.RouterDecision(c.orchConst)
		if converted != c.busConst {
			t.Fatalf("bus.RouterDecision(%q) = %q, want %q", c.orchConst, converted, c.busConst)
		}
	}
}

// TestConflictDetectedAndFinalizedFields exercises the two event types
// whose fields reference external package types (git.ConflictRegion,
// state.TaskState), guarding against accidental field removal. The kind
// strings for these events are covered by TestEventKinds.
func TestConflictDetectedAndFinalizedFields(t *testing.T) {
	ts := time.Now()
	cd := &bus.ConflictDetected{
		TaskID: "c-1",
		Regions: []git.ConflictRegion{
			{FilePath: "a.go", BaseDiff: "b", ProposedDiff: "p"},
		},
		Timestamp: ts,
	}
	if len(cd.Regions) != 1 || cd.Regions[0].FilePath != "a.go" {
		t.Fatalf("ConflictDetected.Regions not preserved: %+v", cd.Regions)
	}

	tf := &bus.TaskFinalized{
		TaskID:         "f-1",
		FinalState:     state.StateCommitted,
		FinalOutputRef: "sha-abc",
		Timestamp:      ts,
	}
	if tf.FinalState != state.StateCommitted {
		t.Fatalf("TaskFinalized.FinalState = %q, want %q", tf.FinalState, state.StateCommitted)
	}
	if tf.FinalOutputRef != "sha-abc" {
		t.Fatalf("TaskFinalized.FinalOutputRef = %q, want %q", tf.FinalOutputRef, "sha-abc")
	}
}
