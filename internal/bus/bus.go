// Package bus provides the in-process event transport for the Limen
// interactive TUI. It defines the event taxonomy emitted by the
// orchestrator and the cognitive components (Router, Worker, Validator),
// together with a buffered, blocking-publish channel bus implementation.
//
// The package is intentionally free of any import on the orchestrator
// package: the orchestrator imports bus (to accept an Emitter parameter),
// so a reverse import would create a cycle. The RouterDecision type is
// therefore mirrored locally; see its doc comment for details.
package bus

import (
	"sync"
	"time"

	"github.com/denialbb/limen/internal/git"
	"github.com/denialbb/limen/internal/state"
)

// Event is the marker interface implemented by every event in the
// taxonomy. The unexported kind method returns the event's type name,
// used for routing and test assertions without relying on reflection.
type Event interface {
	kind() string
}

// EventBus is the subscription-based transport consumed by the TUI.
type EventBus interface {
	// Publish delivers an event to every subscriber. Implementations
	// must not drop events; blocking backpressure is permitted.
	Publish(Event)
	// Subscribe returns a receive-only channel from which the caller
	// consumes events. Multiple subscribers are supported; each
	// receives its own copy of the stream.
	Subscribe() <-chan Event
	// Close shuts the bus down. Subscriber channels are closed and
	// subsequent Publish calls become no-ops.
	Close()
}

// SubscriberBufferSize is the per-subscriber channel capacity. It
// matches the fixed buffer (1024) specified in the interactive TUI
// design document. It is exported so consumers and tests can reason
// about backpressure without duplicating the magic number.
const SubscriberBufferSize = 1024

// ChannelBus is the in-process, channel-backed EventBus implementation.
//
// Backpressure policy: Publish blocks when any subscriber's buffer is
// full. No events are dropped. In single-task v1 the TUI drains far
// faster than producers fill, so the block is effectively never hit.
// This must be revisited for multi-task v2, where fan-out to N slow
// consumers can stall the producer.
type ChannelBus struct {
	mu          sync.Mutex
	subscribers []chan Event
	closed      bool
}

// NewChannelBus constructs an empty ChannelBus.
func NewChannelBus() *ChannelBus {
	return &ChannelBus{}
}

// Subscribe returns a receive-only channel for the event stream. Each
// subscriber receives its own buffered channel. When the bus is closed,
// every subscriber channel is closed.
//
// Subscribe after Close returns an already-closed channel so the caller
// observes a clean shutdown rather than blocking forever on a stream
// that will never deliver.
func (b *ChannelBus) Subscribe() <-chan Event {
	ch := make(chan Event, SubscriberBufferSize)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		// NOTE: Bus already closed; close the fresh channel immediately.
		close(ch)
		return ch
	}
	b.subscribers = append(b.subscribers, ch)
	return ch
}

// Publish fans an event out to every subscriber, blocking on each
// until the event is accepted. After Close, Publish is a no-op: the
// event is silently discarded rather than panicking.
//
// The mutex is held for the full fan-out so that Close cannot close a
// subscriber channel mid-send (which would panic on a send to a closed
// channel). This serializes publishers and means Close waits for any
// in-flight Publish to complete; both are acceptable v1 tradeoffs per
// the design document's single-task, fast-consumer assumptions.
//
// TODO: For multi-task v2, consider non-blocking sends with drop-or-queue
// semantics to prevent a slow consumer from stalling Close indefinitely.
func (b *ChannelBus) Publish(ev Event) {
	// NOTE: Silently drop nil events as a defensive measure so a stray
	// bus.Publish(nil) cannot poison subscriber streams (and downstream
	// kind() dereferences cannot panic on a nil interface).
	if ev == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		// No-op after Close: avoids a panic during orchestrator
		// teardown where a final event may race with Close.
		return
	}
	for _, ch := range b.subscribers {
		ch <- ev
	}
}

// Close shuts the bus down. All subscriber channels are closed and the
// subscriber slice is cleared. Publish after Close is a no-op. Calling
// Close more than once is safe.
func (b *ChannelBus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for _, ch := range b.subscribers {
		close(ch)
	}
	b.subscribers = nil
}

// EventSink is the narrow publish-only interface passed to the cognitive
// components (Router, Retriever, Worker, Validator) as the Emitter
// parameter. Exposing only Publish prevents components from subscribing
// or tearing down the bus.
type EventSink interface {
	Publish(Event)
}

// Compile-time assertions that the concrete types satisfy the interfaces.
var (
	_ EventBus  = (*ChannelBus)(nil)
	_ EventSink = (*ChannelBus)(nil)
	_ EventSink = (*RecorderEmitter)(nil)
)

// RecorderEmitter is an EventSink that records every published event in
// memory. It is intended for test assertions: orchestrator tests migrate
// from ad-hoc recording to this emitter per the design document.
type RecorderEmitter struct {
	mu     sync.Mutex
	events []Event
}

// NewRecorderEmitter constructs an empty RecorderEmitter.
func NewRecorderEmitter() *RecorderEmitter {
	return &RecorderEmitter{}
}

// Publish appends the event to the recorded history.
func (r *RecorderEmitter) Publish(ev Event) {
	// NOTE: Silently drop nil events as a defensive measure so a stray
	// Publish(nil) cannot corrupt the recording (and EventsByKind's
	// ev.kind() call cannot panic on a nil interface).
	if ev == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

// Events returns a defensive copy of the recorded events in publication
// order. The returned slice is safe for the caller to mutate.
func (r *RecorderEmitter) Events() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Event, len(r.events))
	copy(out, r.events)
	return out
}

// EventsByKind returns the subset of recorded events whose kind matches
// the provided string, in publication order. The returned slice is a
// fresh copy referencing the same event values.
func (r *RecorderEmitter) EventsByKind(kind string) []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Event
	for _, ev := range r.events {
		if ev.kind() == kind {
			out = append(out, ev)
		}
	}
	return out
}

// --- Event taxonomy ---
//
// Each event is a value struct carrying the fields specified in the
// interactive TUI design taxonomy table. Pointer receivers on kind
// allow nil-safety checks at consumption sites where relevant.

// TaskStateChanged signals a task state-machine transition.
type TaskStateChanged struct {
	From      state.TaskState
	To        state.TaskState
	TaskID    string
	Timestamp time.Time
}

func (*TaskStateChanged) kind() string { return "TaskStateChanged" }

// ContextBuilt signals completion of context/retrieval assembly.
type ContextBuilt struct {
	TaskID       string
	SnapshotSize int
	ManifestRef  string
	Timestamp    time.Time
}

func (*ContextBuilt) kind() string { return "ContextBuilt" }

// RouterExamining signals the router beginning its evaluation pass.
type RouterExamining struct {
	TaskID         string
	ContextExcerpt string
	Entropy        float64
	Timestamp      time.Time
}

func (*RouterExamining) kind() string { return "RouterExamining" }

// RouterDecision is the routing decision type. It mirrors
// orchestrator.RouterDecision and is duplicated here to avoid an import
// cycle: the orchestrator package imports bus (to accept an Emitter),
// so bus cannot import orchestrator. Callers constructing a
// RouterDecisionEvent must convert from orchestrator.RouterDecision to
// bus.RouterDecision, e.g. bus.RouterDecision(decision).
type RouterDecision string

const (
	DecisionProceed  RouterDecision = "PROCEED"
	DecisionExpand   RouterDecision = "EXPAND"
	DecisionEscalate RouterDecision = "ESCALATE"
)

// RouterDecisionEvent signals the router's final routing decision.
//
// NOTE: Named RouterDecisionEvent rather than RouterDecision to avoid a
// collision with the mirrored RouterDecision string type above. The
// kind() string remains "RouterDecision" to match the taxonomy label.
type RouterDecisionEvent struct {
	TaskID      string
	Decision    RouterDecision
	Rationale   string
	ExpandCount int
	Timestamp   time.Time
}

func (*RouterDecisionEvent) kind() string { return "RouterDecision" }

// WorkerStarted signals the worker beginning a production pass.
type WorkerStarted struct {
	TaskID       string
	WorktreePath string
	BaseCommit   string
	Retry        int
	Timestamp    time.Time
}

func (*WorkerStarted) kind() string { return "WorkerStarted" }

// WorkerToolCall signals a single tool invocation by the worker.
type WorkerToolCall struct {
	TaskID    string
	Tool      string
	Args      string
	Timestamp time.Time
}

func (*WorkerToolCall) kind() string { return "WorkerToolCall" }

// WorkerFileEdit signals a file mutation by the worker.
type WorkerFileEdit struct {
	TaskID    string
	Path      string
	Op        string
	DiffHunk  string
	Timestamp time.Time
}

func (*WorkerFileEdit) kind() string { return "WorkerFileEdit" }

// WorkerFinished signals the worker completing its production pass.
type WorkerFinished struct {
	TaskID    string
	Timestamp time.Time
}

func (*WorkerFinished) kind() string { return "WorkerFinished" }

// ValidatorExamining signals the validator beginning its evaluation.
type ValidatorExamining struct {
	TaskID    string
	Criteria  []string
	Timestamp time.Time
}

func (*ValidatorExamining) kind() string { return "ValidatorExamining" }

// ValidatorCriterionResult signals a single criterion's pass/fail result.
type ValidatorCriterionResult struct {
	TaskID    string
	Criterion string
	Passed    bool
	Detail    string
	Timestamp time.Time
}

func (*ValidatorCriterionResult) kind() string { return "ValidatorCriterionResult" }

// ValidatorVerdict signals the validator's overall verdict.
type ValidatorVerdict struct {
	TaskID    string
	Passes    bool
	Feedback  string
	Timestamp time.Time
}

func (*ValidatorVerdict) kind() string { return "ValidatorVerdict" }

// ConflictDetected signals git conflict regions in the worker's patch.
type ConflictDetected struct {
	TaskID    string
	Regions   []git.ConflictRegion
	Timestamp time.Time
}

func (*ConflictDetected) kind() string { return "ConflictDetected" }

// TaskFinalized signals a task reaching a terminal state.
type TaskFinalized struct {
	TaskID         string
	FinalState     state.TaskState
	FinalOutputRef string
	Timestamp      time.Time
}

func (*TaskFinalized) kind() string { return "TaskFinalized" }
