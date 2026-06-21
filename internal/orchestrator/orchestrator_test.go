package orchestrator_test

import (
	"context"
	"errors"
	"testing"

	"github.com/denialbb/limen/internal/orchestrator"
	"github.com/denialbb/limen/internal/state"
)

// mockStore is a fake state store for testing
type mockStore struct {
	tasks map[string]*state.Task
}

func (m *mockStore) CreateTask(id string, maxRetries int) (*state.Task, error) {
	t := &state.Task{ID: id, CurrentState: state.StateCreated, MaxRetries: maxRetries}
	m.tasks[id] = t
	return t, nil
}

func (m *mockStore) GetTask(id string) (*state.Task, error) {
	if t, ok := m.tasks[id]; ok {
		return t, nil
	}
	return nil, errors.New("not found")
}

func (m *mockStore) TransitionState(id string, newState state.TaskState) error {
	if t, ok := m.tasks[id]; ok {
		t.CurrentState = newState
		return nil
	}
	return errors.New("not found")
}

func (m *mockStore) IncrementRetry(id string) error {
	if t, ok := m.tasks[id]; ok {
		if t.RetryCount >= t.MaxRetries {
			return state.ErrMaxRetriesReached
		}
		t.RetryCount++
		return nil
	}
	return errors.New("not found")
}

// Simple mocks for other interfaces
type mockRouter struct {
	decision orchestrator.RouterDecision
}

func (m *mockRouter) Evaluate(ctx context.Context, task *state.Task) (orchestrator.RouterDecision, error) {
	return m.decision, nil
}

type mockWorker struct {
	called bool
}

func (m *mockWorker) ProduceSolution(ctx context.Context, task *state.Task) error {
	m.called = true
	return nil
}

type mockValidator struct {
	passes bool
}

func (m *mockValidator) Evaluate(ctx context.Context, task *state.Task) (bool, string, error) {
	return m.passes, "feedback", nil
}

type mockGit struct {
	valid bool
}

func (m *mockGit) IsValid(ctx context.Context) (bool, error) {
	return m.valid, nil
}

func (m *mockGit) CommitWorktree(ctx context.Context, taskID string) error {
	return nil
}

func (m *mockGit) ResolveConflict(ctx context.Context, taskID string) error {
	return nil
}

func TestRunTask_Success(t *testing.T) {
	store := &mockStore{tasks: make(map[string]*state.Task)}
	task, _ := store.CreateTask("task-1", 3)

	router := &mockRouter{decision: orchestrator.DecisionProceed}
	worker := &mockWorker{}
	validator := &mockValidator{passes: true}
	git := &mockGit{valid: true}

	orch := orchestrator.NewOrchestrator(store, router, worker, validator, git)
	err := orch.RunTask(context.Background(), task.ID)

	if err != nil {
		t.Fatalf("expected no error from RunTask, got: %v", err)
	}

	if task.CurrentState != state.StateCommitted {
		t.Errorf("expected state COMMITTED, got: %s", task.CurrentState)
	}

	if !worker.called {
		t.Error("expected worker to be called")
	}
}

func TestRunTask_EscalateOnRouter(t *testing.T) {
	store := &mockStore{tasks: make(map[string]*state.Task)}
	task, _ := store.CreateTask("task-1", 3)

	router := &mockRouter{decision: orchestrator.DecisionEscalate}
	worker := &mockWorker{}
	validator := &mockValidator{passes: true}
	git := &mockGit{valid: true}

	orch := orchestrator.NewOrchestrator(store, router, worker, validator, git)
	err := orch.RunTask(context.Background(), task.ID)

	// Since the stub returns ErrNotImplemented, this error check will fail in TDD
	// But logically, it should return ErrUnresolvableEntropy or a similar error, or nil but state=FAILED_ESCALATED
	if err == nil {
		t.Fatal("expected error due to router escalation, got nil")
	}

	if task.CurrentState != state.StateFailedEscalated {
		t.Errorf("expected state FAILED_ESCALATED, got: %s", task.CurrentState)
	}
}

func TestRunTask_ValidatorRetry(t *testing.T) {
	store := &mockStore{tasks: make(map[string]*state.Task)}
	task, _ := store.CreateTask("task-1", 1) // Only 1 retry allowed

	router := &mockRouter{decision: orchestrator.DecisionProceed}
	worker := &mockWorker{}
	validator := &mockValidator{passes: false} // Will fail validation, trigger retry
	git := &mockGit{valid: true}

	orch := orchestrator.NewOrchestrator(store, router, worker, validator, git)
	err := orch.RunTask(context.Background(), task.ID)

	// First failure loops once, second failure triggers FAILED_ESCALATED
	if err == nil {
		t.Fatal("expected error due to exhausting retries, got nil")
	}

	if task.RetryCount != 1 {
		t.Errorf("expected 1 retry, got: %d", task.RetryCount)
	}

	if task.CurrentState != state.StateFailedEscalated {
		t.Errorf("expected state FAILED_ESCALATED, got: %s", task.CurrentState)
	}
}
