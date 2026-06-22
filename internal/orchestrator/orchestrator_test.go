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

func TestRunTask_GitInvalid(t *testing.T) {
	store := &mockStore{tasks: make(map[string]*state.Task)}
	task, _ := store.CreateTask("task-1", 3)

	router := &mockRouter{decision: orchestrator.DecisionProceed}
	worker := &mockWorker{}
	validator := &mockValidator{passes: true}
	git := &mockGit{valid: false} // Invalid git

	orch := orchestrator.NewOrchestrator(store, router, worker, validator, git)
	err := orch.RunTask(context.Background(), task.ID)

	if !errors.Is(err, orchestrator.ErrGitInvalid) {
		t.Fatalf("expected ErrGitInvalid, got: %v", err)
	}
}

func TestRunTask_TaskNotFound(t *testing.T) {
	store := &mockStore{tasks: make(map[string]*state.Task)}
	
	router := &mockRouter{decision: orchestrator.DecisionProceed}
	worker := &mockWorker{}
	validator := &mockValidator{passes: true}
	git := &mockGit{valid: true}

	orch := orchestrator.NewOrchestrator(store, router, worker, validator, git)
	err := orch.RunTask(context.Background(), "non-existent")

	if err == nil {
		t.Fatal("expected error for non-existent task, got nil")
	}
}

func TestRunTask_WorkerError(t *testing.T) {
	store := &mockStore{tasks: make(map[string]*state.Task)}
	task, _ := store.CreateTask("task-1", 3)

	router := &mockRouter{decision: orchestrator.DecisionProceed}
	
	// inline mock for worker error
	worker := &mockErrorWorker{}
	validator := &mockValidator{passes: true}
	git := &mockGit{valid: true}

	orch := orchestrator.NewOrchestrator(store, router, worker, validator, git)
	err := orch.RunTask(context.Background(), task.ID)

	if err == nil {
		t.Fatal("expected error due to worker failure, got nil")
	}
}

func TestRunTask_RouterExpand(t *testing.T) {
	store := &mockStore{tasks: make(map[string]*state.Task)}
	task, _ := store.CreateTask("task-1", 3)

	router := &mockRouter{decision: orchestrator.DecisionExpand}
	worker := &mockWorker{}
	validator := &mockValidator{passes: true}
	git := &mockGit{valid: true}

	orch := orchestrator.NewOrchestrator(store, router, worker, validator, git)
	err := orch.RunTask(context.Background(), task.ID)

	if !errors.Is(err, orchestrator.ErrNotImplemented) {
		t.Fatalf("expected ErrNotImplemented for DecisionExpand, got: %v", err)
	}
}

type mockErrorWorker struct{}
func (m *mockErrorWorker) ProduceSolution(ctx context.Context, task *state.Task) error {
	return errors.New("worker simulated error")
}

func TestRunTask_GitError(t *testing.T) {
	store := &mockStore{tasks: make(map[string]*state.Task)}
	task, _ := store.CreateTask("task-1", 3)

	router := &mockRouter{decision: orchestrator.DecisionProceed}
	worker := &mockWorker{}
	validator := &mockValidator{passes: true}
	git := &mockErrorGit{}

	orch := orchestrator.NewOrchestrator(store, router, worker, validator, git)
	err := orch.RunTask(context.Background(), task.ID)

	if err == nil || err.Error() != "git error" {
		t.Fatalf("expected git error, got: %v", err)
	}
}

func TestRunTask_RouterError(t *testing.T) {
	store := &mockStore{tasks: make(map[string]*state.Task)}
	task, _ := store.CreateTask("task-1", 3)

	router := &mockErrorRouter{}
	worker := &mockWorker{}
	validator := &mockValidator{passes: true}
	git := &mockGit{valid: true}

	orch := orchestrator.NewOrchestrator(store, router, worker, validator, git)
	err := orch.RunTask(context.Background(), task.ID)

	if err == nil || err.Error() != "router error" {
		t.Fatalf("expected router error, got: %v", err)
	}
}

func TestRunTask_ValidatorError(t *testing.T) {
	store := &mockStore{tasks: make(map[string]*state.Task)}
	task, _ := store.CreateTask("task-1", 3)

	router := &mockRouter{decision: orchestrator.DecisionProceed}
	worker := &mockWorker{}
	validator := &mockErrorValidator{}
	git := &mockGit{valid: true}

	orch := orchestrator.NewOrchestrator(store, router, worker, validator, git)
	err := orch.RunTask(context.Background(), task.ID)

	if err == nil || err.Error() != "validator error" {
		t.Fatalf("expected validator error, got: %v", err)
	}
}

func TestRunTask_CommitError(t *testing.T) {
	store := &mockStore{tasks: make(map[string]*state.Task)}
	task, _ := store.CreateTask("task-1", 3)

	router := &mockRouter{decision: orchestrator.DecisionProceed}
	worker := &mockWorker{}
	validator := &mockValidator{passes: true}
	git := &mockGitCommitError{mockGit: mockGit{valid: true}}

	orch := orchestrator.NewOrchestrator(store, router, worker, validator, git)
	err := orch.RunTask(context.Background(), task.ID)

	if err == nil || err.Error() != "commit error" {
		t.Fatalf("expected commit error, got: %v", err)
	}
}

type mockErrorGit struct{}
func (m *mockErrorGit) IsValid(ctx context.Context) (bool, error) {
	return false, errors.New("git error")
}
func (m *mockErrorGit) CommitWorktree(ctx context.Context, taskID string) error {
	return nil
}
func (m *mockErrorGit) ResolveConflict(ctx context.Context, taskID string) error {
	return nil
}

type mockGitCommitError struct {
	mockGit
}
func (m *mockGitCommitError) CommitWorktree(ctx context.Context, taskID string) error {
	return errors.New("commit error")
}

type mockErrorRouter struct{}
func (m *mockErrorRouter) Evaluate(ctx context.Context, task *state.Task) (orchestrator.RouterDecision, error) {
	return orchestrator.DecisionProceed, errors.New("router error")
}

type mockErrorValidator struct{}
func (m *mockErrorValidator) Evaluate(ctx context.Context, task *state.Task) (bool, string, error) {
	return false, "", errors.New("validator error")
}
