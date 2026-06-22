package state_test

import (
	"errors"
	"testing"

	"github.com/denialbb/limen/internal/state"
)

// newTestStore creates a fresh in-memory SQLite store for a single test.
func newTestStore(t *testing.T) *state.SQLiteStore {
	t.Helper()
	store, err := state.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("failed to create test store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}

func TestCreateTask(t *testing.T) {
	store := newTestStore(t)

	task, err := store.CreateTask("task-123", 3)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if task.ID != "task-123" {
		t.Errorf("expected ID task-123, got: %s", task.ID)
	}

	if task.CurrentState != state.StateCreated {
		t.Errorf("expected state CREATED, got: %s", task.CurrentState)
	}

	if task.MaxRetries != 3 {
		t.Errorf("expected max retries 3, got: %d", task.MaxRetries)
	}

	if task.RetryCount != 0 {
		t.Errorf("expected initial retry count 0, got: %d", task.RetryCount)
	}
}

func TestCreateTaskAlreadyExists(t *testing.T) {
	store := newTestStore(t)
	_, _ = store.CreateTask("task-123", 3)

	_, err := store.CreateTask("task-123", 3)
	if !errors.Is(err, state.ErrTaskAlreadyExists) {
		t.Errorf("expected ErrTaskAlreadyExists, got: %v", err)
	}
}

func TestGetTask(t *testing.T) {
	store := newTestStore(t)
	_, _ = store.CreateTask("task-123", 3)

	task, err := store.GetTask("task-123")
	if err != nil {
		t.Fatalf("expected no error getting task, got: %v", err)
	}

	if task.ID != "task-123" {
		t.Errorf("expected ID task-123, got: %s", task.ID)
	}
}

func TestGetTaskNotFound(t *testing.T) {
	store := newTestStore(t)
	_, err := store.GetTask("task-404")
	if !errors.Is(err, state.ErrTaskNotFound) {
		t.Errorf("expected ErrTaskNotFound, got: %v", err)
	}
}

func TestValidStateTransition(t *testing.T) {
	store := newTestStore(t)
	_, _ = store.CreateTask("task-123", 3)

	err := store.TransitionState("task-123", state.StateContextBuilding)
	if err != nil {
		t.Fatalf("expected valid transition, got error: %v", err)
	}

	task, err := store.GetTask("task-123")
	if err != nil {
		t.Fatalf("expected no error getting task, got: %v", err)
	}

	if task.CurrentState != state.StateContextBuilding {
		t.Errorf("expected state CONTEXT_BUILDING, got: %s", task.CurrentState)
	}
}

func TestInvalidStateTransition(t *testing.T) {
	store := newTestStore(t)
	_, _ = store.CreateTask("task-123", 3)

	// Attempting to jump directly from CREATED to COMMITTED is illegal
	err := store.TransitionState("task-123", state.StateCommitted)
	if err == nil {
		t.Fatal("expected error for invalid transition, got nil")
	}

	if !errors.Is(err, state.ErrInvalidTransition) {
		t.Errorf("expected ErrInvalidTransition, got: %v", err)
	}
}

func TestTransitionTaskNotFound(t *testing.T) {
	store := newTestStore(t)
	err := store.TransitionState("task-404", state.StateContextBuilding)
	if !errors.Is(err, state.ErrTaskNotFound) {
		t.Errorf("expected ErrTaskNotFound, got: %v", err)
	}
}

func TestAllValidTransitions(t *testing.T) {
	store := newTestStore(t)
	id := "task-1"
	_, _ = store.CreateTask(id, 3)

	transitions := []state.TaskState{
		state.StateContextBuilding,
		state.StateRoutingEvaluation,
		state.StateWorkerRunning,
		state.StateAwaitingValidation,
		state.StateRevisionRequested,
		state.StateWorkerRunning,
		state.StateAwaitingValidation,
		state.StateApproved,
		state.StateCommitted,
	}

	for _, nextState := range transitions {
		err := store.TransitionState(id, nextState)
		if err != nil {
			t.Fatalf("expected valid transition to %s, got error: %v", nextState, err)
		}
	}
}

// mustTransition is a test helper that transitions a task and fails the test on error.
func mustTransition(t *testing.T, store state.Store, id string, to state.TaskState) {
	t.Helper()
	if err := store.TransitionState(id, to); err != nil {
		t.Fatalf("transition to %s: %v", to, err)
	}
}

func TestIncrementRetry(t *testing.T) {
	store := newTestStore(t)
	_, _ = store.CreateTask("task-123", 2)

	// NODE: IncrementRetry requires the task to be in StateRevisionRequested.
	walkToRevision := func() {
		mustTransition(t, store, "task-123", state.StateContextBuilding)
		mustTransition(t, store, "task-123", state.StateRoutingEvaluation)
		mustTransition(t, store, "task-123", state.StateWorkerRunning)
		mustTransition(t, store, "task-123", state.StateAwaitingValidation)
		mustTransition(t, store, "task-123", state.StateRevisionRequested)
	}

	walkToRevision()

	// 1st retry
	err := store.IncrementRetry("task-123")
	if err != nil {
		t.Fatalf("expected no error on first retry, got: %v", err)
	}

	// Walk back to RevisionRequested for 2nd retry
	mustTransition(t, store, "task-123", state.StateWorkerRunning)
	mustTransition(t, store, "task-123", state.StateAwaitingValidation)
	mustTransition(t, store, "task-123", state.StateRevisionRequested)

	// 2nd retry
	err = store.IncrementRetry("task-123")
	if err != nil {
		t.Fatalf("expected no error on second retry, got: %v", err)
	}

	// Walk back to RevisionRequested for 3rd attempt (should exceed max)
	mustTransition(t, store, "task-123", state.StateWorkerRunning)
	mustTransition(t, store, "task-123", state.StateAwaitingValidation)
	mustTransition(t, store, "task-123", state.StateRevisionRequested)

	// 3rd retry should fail because max is 2
	err = store.IncrementRetry("task-123")
	if err == nil {
		t.Fatal("expected error when exceeding max retries, got nil")
	}

	if !errors.Is(err, state.ErrMaxRetriesReached) {
		t.Errorf("expected ErrMaxRetriesReached, got: %v", err)
	}
}

func TestIncrementRetry_GuardNotInRevisionRequested(t *testing.T) {
	store := newTestStore(t)
	_, _ = store.CreateTask("task-123", 3)

	// Task is in CREATED, not RevisionRequested – IncrementRetry should reject.
	err := store.IncrementRetry("task-123")
	if !errors.Is(err, state.ErrInvalidTransition) {
		t.Errorf("expected ErrInvalidTransition for CREATED task, got: %v", err)
	}
}

func TestIncrementRetry_GuardTerminalState(t *testing.T) {
	store := newTestStore(t)
	_, _ = store.CreateTask("task-terminal", 3)

	mustTransition(t, store, "task-terminal", state.StateContextBuilding)
	mustTransition(t, store, "task-terminal", state.StateRoutingEvaluation)
	mustTransition(t, store, "task-terminal", state.StateWorkerRunning)
	mustTransition(t, store, "task-terminal", state.StateAwaitingValidation)
	mustTransition(t, store, "task-terminal", state.StateRevisionRequested)

	// Increment retry then escalate to FAILED_ESCALATED.
	if err := store.IncrementRetry("task-terminal"); err != nil {
		t.Fatalf("unexpected error on first retry: %v", err)
	}
	mustTransition(t, store, "task-terminal", state.StateWorkerRunning)
	mustTransition(t, store, "task-terminal", state.StateAwaitingValidation)
	mustTransition(t, store, "task-terminal", state.StateFailedEscalated)

	// Retries must be rejected once the task has reached a terminal state.
	err := store.IncrementRetry("task-terminal")
	if !errors.Is(err, state.ErrInvalidTransition) {
		t.Errorf("expected ErrInvalidTransition for terminal task, got: %v", err)
	}
}

func TestIncrementRetryTaskNotFound(t *testing.T) {
	store := newTestStore(t)
	err := store.IncrementRetry("task-404")
	if !errors.Is(err, state.ErrTaskNotFound) {
		t.Errorf("expected ErrTaskNotFound, got: %v", err)
	}
}

func TestRecordToolCall(t *testing.T) {
	store := newTestStore(t)
	_, _ = store.CreateTask("task-123", 3)

	if err := store.RecordToolCall("task-123", "tool-1"); err != nil {
		t.Fatalf("expected no error recording tool call, got: %v", err)
	}
}

func TestRecordToolCallTaskNotFound(t *testing.T) {
	store := newTestStore(t)
	err := store.RecordToolCall("task-404", "tool-1")
	if !errors.Is(err, state.ErrTaskNotFound) {
		t.Errorf("expected ErrTaskNotFound, got: %v", err)
	}
}

func TestRecordValidationDecision(t *testing.T) {
	store := newTestStore(t)
	_, _ = store.CreateTask("task-123", 3)

	if err := store.RecordValidationDecision("task-123", false, "needs work"); err != nil {
		t.Fatalf("expected no error recording validation decision, got: %v", err)
	}

	task, err := store.GetTask("task-123")
	if err != nil {
		t.Fatalf("expected no error getting task, got: %v", err)
	}

	if task.ValidationDecision == "" {
		t.Error("expected ValidationDecision to be persisted")
	}
}

func TestRecordValidationDecisionTaskNotFound(t *testing.T) {
	store := newTestStore(t)
	err := store.RecordValidationDecision("task-404", true, "")
	if !errors.Is(err, state.ErrTaskNotFound) {
		t.Errorf("expected ErrTaskNotFound, got: %v", err)
	}
}

func TestRecordFinalOutput(t *testing.T) {
	store := newTestStore(t)
	_, _ = store.CreateTask("task-123", 3)

	if err := store.RecordFinalOutput("task-123", "final answer"); err != nil {
		t.Fatalf("expected no error recording final output, got: %v", err)
	}

	task, err := store.GetTask("task-123")
	if err != nil {
		t.Fatalf("expected no error getting task, got: %v", err)
	}

	if task.FinalOutput != "final answer" {
		t.Errorf("expected FinalOutput 'final answer', got: %s", task.FinalOutput)
	}
}

func TestRecordFinalOutputTaskNotFound(t *testing.T) {
	store := newTestStore(t)
	err := store.RecordFinalOutput("task-404", "output")
	if !errors.Is(err, state.ErrTaskNotFound) {
		t.Errorf("expected ErrTaskNotFound, got: %v", err)
	}
}

func TestStateTransitionsRecorded(t *testing.T) {
	store := newTestStore(t)
	_, _ = store.CreateTask("task-123", 3)

	mustTransition(t, store, "task-123", state.StateContextBuilding)
	mustTransition(t, store, "task-123", state.StateRoutingEvaluation)

	transitions, err := store.GetStateTransitions("task-123")
	if err != nil {
		t.Fatalf("expected no error getting transitions, got: %v", err)
	}

	if len(transitions) != 2 {
		t.Fatalf("expected 2 transitions, got: %d", len(transitions))
	}

	if transitions[0].FromState != state.StateCreated || transitions[0].ToState != state.StateContextBuilding {
		t.Errorf("expected CREATED -> CONTEXT_BUILDING, got: %s -> %s", transitions[0].FromState, transitions[0].ToState)
	}

	if transitions[1].FromState != state.StateContextBuilding || transitions[1].ToState != state.StateRoutingEvaluation {
		t.Errorf("expected CONTEXT_BUILDING -> ROUTING_EVALUATION, got: %s -> %s", transitions[1].FromState, transitions[1].ToState)
	}
}

func TestValidationDecisionsAppended(t *testing.T) {
	store := newTestStore(t)
	_, _ = store.CreateTask("task-123", 3)

	if err := store.RecordValidationDecision("task-123", false, "needs work"); err != nil {
		t.Fatalf("expected no error recording first decision, got: %v", err)
	}
	if err := store.RecordValidationDecision("task-123", true, "looks good"); err != nil {
		t.Fatalf("expected no error recording second decision, got: %v", err)
	}

	decisions, err := store.GetValidationDecisions("task-123")
	if err != nil {
		t.Fatalf("expected no error getting decisions, got: %v", err)
	}

	if len(decisions) != 2 {
		t.Fatalf("expected 2 validation decisions, got: %d", len(decisions))
	}

	if decisions[0].Pass || decisions[0].Feedback != "needs work" {
		t.Errorf("expected first decision pass=false feedback='needs work', got pass=%v feedback=%q", decisions[0].Pass, decisions[0].Feedback)
	}

	if !decisions[1].Pass || decisions[1].Feedback != "looks good" {
		t.Errorf("expected second decision pass=true feedback='looks good', got pass=%v feedback=%q", decisions[1].Pass, decisions[1].Feedback)
	}
}

func TestRecordContextSnapshot(t *testing.T) {
	store := newTestStore(t)
	_, _ = store.CreateTask("task-123", 3)

	if err := store.RecordContextSnapshot("task-123", "snapshot-data"); err != nil {
		t.Fatalf("expected no error recording context snapshot, got: %v", err)
	}

	task, err := store.GetTask("task-123")
	if err != nil {
		t.Fatalf("expected no error getting task, got: %v", err)
	}

	if task.ContextSnapshot != "snapshot-data" {
		t.Errorf("expected ContextSnapshot 'snapshot-data', got: %s", task.ContextSnapshot)
	}
}

func TestRecordContextSnapshotTaskNotFound(t *testing.T) {
	store := newTestStore(t)
	err := store.RecordContextSnapshot("task-404", "snapshot")
	if !errors.Is(err, state.ErrTaskNotFound) {
		t.Errorf("expected ErrTaskNotFound, got: %v", err)
	}
}
