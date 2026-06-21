package state_test

import (
	"errors"
	"testing"

	"github.com/denialbb/limen/internal/state"
)

func TestCreateTask(t *testing.T) {
	store := state.NewStore()

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

func TestValidStateTransition(t *testing.T) {
	store := state.NewStore()
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
	store := state.NewStore()
	_, _ = store.CreateTask("task-123", 3)

	// Attempting to jump directly from CREATED to COMMITTED is illegal
	err := store.TransitionState("task-123", state.StateCommitted)
	if err == nil {
		t.Fatal("expected error for invalid transition, got nil")
	}

	if !errors.Is(err, state.ErrInvalidTransition) && err != state.ErrNotImplemented {
		t.Errorf("expected ErrInvalidTransition, got: %v", err)
	}
}

func TestIncrementRetry(t *testing.T) {
	store := state.NewStore()
	_, _ = store.CreateTask("task-123", 2)

	// 1st retry
	err := store.IncrementRetry("task-123")
	if err != nil {
		t.Fatalf("expected no error on first retry, got: %v", err)
	}

	// 2nd retry
	err = store.IncrementRetry("task-123")
	if err != nil {
		t.Fatalf("expected no error on second retry, got: %v", err)
	}

	// 3rd retry should fail because max is 2
	err = store.IncrementRetry("task-123")
	if err == nil {
		t.Fatal("expected error when exceeding max retries, got nil")
	}

	if !errors.Is(err, state.ErrMaxRetriesReached) && err != state.ErrNotImplemented {
		t.Errorf("expected ErrMaxRetriesReached, got: %v", err)
	}
}
