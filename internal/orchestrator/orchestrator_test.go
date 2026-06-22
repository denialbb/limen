package orchestrator_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/denialbb/limen/internal/git"
	"github.com/denialbb/limen/internal/orchestrator"
	"github.com/denialbb/limen/internal/state"
)

// sentinel errors for mock failures
var (
	errMockNotFound   = errors.New("not found")
	errMockGitFailure = errors.New("git error")
	errMockRouter     = errors.New("router error")
	errMockValidator  = errors.New("validator error")
	errMockCommit     = errors.New("commit error")
	errMockWorker     = errors.New("worker simulated error")
)

// mockStore is a fake state store for testing that delegates transition validity
// to the canonical state package definition.
type mockStore struct {
	tasks map[string]*state.Task
}

func (m *mockStore) CreateTask(id string, maxRetries int) (*state.Task, error) {
	if _, exists := m.tasks[id]; exists {
		return nil, state.ErrTaskAlreadyExists
	}
	t := &state.Task{ID: id, CurrentState: state.StateCreated, MaxRetries: maxRetries}
	m.tasks[id] = t
	return t, nil
}

func (m *mockStore) GetTask(id string) (*state.Task, error) {
	if t, ok := m.tasks[id]; ok {
		return t, nil
	}
	return nil, state.ErrTaskNotFound
}

func (m *mockStore) TransitionState(id string, newState state.TaskState) error {
	t, ok := m.tasks[id]
	if !ok {
		return state.ErrTaskNotFound
	}
	if !state.IsValidTransition(t.CurrentState, newState) {
		return state.ErrInvalidTransition
	}
	t.CurrentState = newState
	return nil
}

func (m *mockStore) IncrementRetry(id string) error {
	t, ok := m.tasks[id]
	if !ok {
		return state.ErrTaskNotFound
	}
	// NOTE: Terminal states may never be retried.
	if t.CurrentState == state.StateFailedEscalated || t.CurrentState == state.StateCommitted {
		return state.ErrInvalidTransition
	}
	// NOTE: Retries may only be incremented when the task is in the
	// RevisionRequested state.
	if t.CurrentState != state.StateRevisionRequested {
		return state.ErrInvalidTransition
	}
	if t.RetryCount >= t.MaxRetries {
		return state.ErrMaxRetriesReached
	}
	t.RetryCount++
	return nil
}

func (m *mockStore) RecordToolCall(id, call string) error {
	return nil
}

func (m *mockStore) RecordValidationDecision(id string, pass bool, feedback string) error {
	return nil
}

func (m *mockStore) RecordFinalOutput(id, output string) error {
	return nil
}

func (m *mockStore) RecordContextSnapshot(id, snapshot string) error {
	return nil
}

// Simple mocks for other interfaces
type mockRouter struct {
	decision orchestrator.RouterDecision
}

func (m *mockRouter) Evaluate(ctx context.Context, task *state.Task) (orchestrator.RouterDecision, error) {
	return m.decision, nil
}

type mockRetriever struct{}

func (m *mockRetriever) Retrieve(ctx context.Context, task *state.Task) (string, error) {
	return "mock-context", nil
}

type mockWorker struct {
	called bool
}

func (m *mockWorker) ProduceSolution(ctx context.Context, task *state.Task, wt *git.Worktree, feedback string) error {
	m.called = true
	return nil
}

type mockValidator struct {
	passes bool
}

func (m *mockValidator) Evaluate(ctx context.Context, task *state.Task, wt *git.Worktree) (bool, string, error) {
	return m.passes, "feedback", nil
}

type mockGit struct {
	valid bool
}

func (m *mockGit) IsValid(ctx context.Context) (bool, error) {
	return m.valid, nil
}

func (m *mockGit) ProvisionWorktree(ctx context.Context, baseCommit, branchName, path string) (*git.Worktree, error) {
	return &git.Worktree{Path: path, Branch: branchName, BaseCommit: baseCommit}, nil
}

func (m *mockGit) CommitWorktree(ctx context.Context, taskID string, wt *git.Worktree) error {
	return nil
}

func (m *mockGit) CheckForConflicts(ctx context.Context, wt *git.Worktree) (bool, error) {
	return false, nil
}

func (m *mockGit) ExtractConflictRegions(ctx context.Context, wt *git.Worktree) ([]git.ConflictRegion, error) {
	return nil, nil
}

func (m *mockGit) DestroyWorktree(ctx context.Context, wt *git.Worktree) error {
	return nil
}

func (m *mockGit) GetWorktreeDiff(ctx context.Context, wt *git.Worktree) (string, error) {
	return "mock-diff", nil
}

func newTestOrchestrator(store state.Store, router orchestrator.Router, worker orchestrator.Worker, validator orchestrator.Validator, gitClient orchestrator.GitClient) orchestrator.Orchestrator {
	return orchestrator.NewOrchestrator(store, router, &mockRetriever{}, worker, validator, gitClient, worktreeRoot())
}

func worktreeRoot() string {
	// NOTE: t.TempDir() returns an absolute path; for tests without a *testing.T
	// we use a fixed absolute fallback. Production callers must supply an absolute
	// path explicitly.
	return filepath.Join("/tmp", "limen-test-worktrees")
}

func TestRunTask_Success(t *testing.T) {
	store := &mockStore{tasks: make(map[string]*state.Task)}
	task, _ := store.CreateTask("task-1", 3)

	router := &mockRouter{decision: orchestrator.DecisionProceed}
	worker := &mockWorker{}
	validator := &mockValidator{passes: true}
	git := &mockGit{valid: true}

	orch := newTestOrchestrator(store, router, worker, validator, git)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := orch.RunTask(ctx, task.ID)

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

	orch := newTestOrchestrator(store, router, worker, validator, git)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := orch.RunTask(ctx, task.ID)

	if !errors.Is(err, orchestrator.ErrUnresolvableEntropy) {
		t.Fatalf("expected ErrUnresolvableEntropy, got: %v", err)
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

	orch := newTestOrchestrator(store, router, worker, validator, git)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := orch.RunTask(ctx, task.ID)

	// First failure loops once, second failure triggers FAILED_ESCALATED
	if !errors.Is(err, orchestrator.ErrValidationFailed) {
		t.Fatalf("expected ErrValidationFailed, got: %v", err)
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

	orch := newTestOrchestrator(store, router, worker, validator, git)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := orch.RunTask(ctx, task.ID)

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

	orch := newTestOrchestrator(store, router, worker, validator, git)
	err := orch.RunTask(context.Background(), "non-existent")

	if !errors.Is(err, state.ErrTaskNotFound) {
		t.Fatalf("expected ErrTaskNotFound, got: %v", err)
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

	orch := newTestOrchestrator(store, router, worker, validator, git)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := orch.RunTask(ctx, task.ID)

	if !errors.Is(err, errMockWorker) {
		t.Fatalf("expected worker error, got: %v", err)
	}
}

func TestRunTask_RouterExpand(t *testing.T) {
	store := &mockStore{tasks: make(map[string]*state.Task)}
	task, _ := store.CreateTask("task-1", 3)

	router := &mockRouter{decision: orchestrator.DecisionExpand}
	worker := &mockWorker{}
	validator := &mockValidator{passes: true}
	git := &mockGit{valid: true}

	orch := newTestOrchestrator(store, router, worker, validator, git)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := orch.RunTask(ctx, task.ID)

	if !errors.Is(err, orchestrator.ErrUnresolvableEntropy) {
		t.Fatalf("expected ErrUnresolvableEntropy after max expand iterations, got: %v", err)
	}

	if task.CurrentState != state.StateFailedEscalated {
		t.Errorf("expected state FAILED_ESCALATED, got: %s", task.CurrentState)
	}
}

func TestRunTask_InvalidTaskID(t *testing.T) {
	store := &mockStore{tasks: make(map[string]*state.Task)}

	router := &mockRouter{decision: orchestrator.DecisionProceed}
	worker := &mockWorker{}
	validator := &mockValidator{passes: true}
	git := &mockGit{valid: true}

	orch := newTestOrchestrator(store, router, worker, validator, git)
	err := orch.RunTask(context.Background(), "task/with/slash")

	if !errors.Is(err, orchestrator.ErrInvalidTaskID) {
		t.Fatalf("expected ErrInvalidTaskID, got: %v", err)
	}
}

type mockErrorWorker struct{}

func (m *mockErrorWorker) ProduceSolution(ctx context.Context, task *state.Task, wt *git.Worktree, feedback string) error {
	return errMockWorker
}

// Compile-time check that mockErrorWorker implements Worker.
var _ orchestrator.Worker = (*mockErrorWorker)(nil)

func TestRunTask_GitError(t *testing.T) {
	store := &mockStore{tasks: make(map[string]*state.Task)}
	task, _ := store.CreateTask("task-1", 3)

	router := &mockRouter{decision: orchestrator.DecisionProceed}
	worker := &mockWorker{}
	validator := &mockValidator{passes: true}
	git := &mockErrorGit{}

	orch := newTestOrchestrator(store, router, worker, validator, git)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := orch.RunTask(ctx, task.ID)

	if !errors.Is(err, errMockGitFailure) {
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

	orch := newTestOrchestrator(store, router, worker, validator, git)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := orch.RunTask(ctx, task.ID)

	if !errors.Is(err, errMockRouter) {
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

	orch := newTestOrchestrator(store, router, worker, validator, git)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := orch.RunTask(ctx, task.ID)

	if !errors.Is(err, errMockValidator) {
		t.Fatalf("expected validator error, got: %v", err)
	}
}

func TestRunTask_CommitError(t *testing.T) {
	store := &mockStore{tasks: make(map[string]*state.Task)}
	task, _ := store.CreateTask("task-1", 3)

	router := &mockRouter{decision: orchestrator.DecisionProceed}
	worker := &mockWorker{}
	validator := &mockValidator{passes: true}
	git := &mockGitCommitError{mockGitBase: mockGitBase{valid: true}}

	orch := newTestOrchestrator(store, router, worker, validator, git)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := orch.RunTask(ctx, task.ID)

	if !errors.Is(err, errMockCommit) {
		t.Fatalf("expected commit error, got: %v", err)
	}
}

// mockGitBase provides no-op implementations for all GitClient methods except the ones
// a specific test wants to override.
type mockGitBase struct {
	valid bool
}

func (m *mockGitBase) IsValid(ctx context.Context) (bool, error) {
	return m.valid, nil
}
func (m *mockGitBase) ProvisionWorktree(ctx context.Context, baseCommit, branchName, path string) (*git.Worktree, error) {
	return &git.Worktree{Path: path, Branch: branchName, BaseCommit: baseCommit}, nil
}
func (m *mockGitBase) CommitWorktree(ctx context.Context, taskID string, wt *git.Worktree) error {
	return nil
}
func (m *mockGitBase) CheckForConflicts(ctx context.Context, wt *git.Worktree) (bool, error) {
	return false, nil
}

func (m *mockGitBase) ExtractConflictRegions(ctx context.Context, wt *git.Worktree) ([]git.ConflictRegion, error) {
	return nil, nil
}
func (m *mockGitBase) DestroyWorktree(ctx context.Context, wt *git.Worktree) error {
	return nil
}
func (m *mockGitBase) GetWorktreeDiff(ctx context.Context, wt *git.Worktree) (string, error) {
	return "mock-diff", nil
}

// Ensures mockGitBase satisfies GitClient.
var _ orchestrator.GitClient = (*mockGitBase)(nil)

type mockErrorGit struct {
	mockGitBase
}

func (m *mockErrorGit) IsValid(ctx context.Context) (bool, error) {
	return false, errMockGitFailure
}

type mockGitCommitError struct {
	mockGitBase
}

func (m *mockGitCommitError) CommitWorktree(ctx context.Context, taskID string, wt *git.Worktree) error {
	return errMockCommit
}

type mockErrorRouter struct{}

func (m *mockErrorRouter) Evaluate(ctx context.Context, task *state.Task) (orchestrator.RouterDecision, error) {
	return orchestrator.DecisionProceed, errMockRouter
}

// Compile-time check that mockErrorRouter implements Router.
var _ orchestrator.Router = (*mockErrorRouter)(nil)

type mockErrorValidator struct{}

func (m *mockErrorValidator) Evaluate(ctx context.Context, task *state.Task, wt *git.Worktree) (bool, string, error) {
	return false, "", errMockValidator
}

// Compile-time check that mockErrorValidator implements Validator.
var _ orchestrator.Validator = (*mockErrorValidator)(nil)

func TestRunTask_ContextCancellation(t *testing.T) {
	store := &mockStore{tasks: make(map[string]*state.Task)}
	task, _ := store.CreateTask("task-1", 3)

	router := &mockRouter{decision: orchestrator.DecisionProceed}
	validator := &mockValidator{passes: true}
	gitMock := &mockGitCancel{mockGitBase: mockGitBase{valid: true}}

	ctx, cancel := context.WithCancel(context.Background())

	worker := &mockWorkerCancel{cancelFunc: cancel}
	orch := newTestOrchestrator(store, router, worker, validator, gitMock)

	err := orch.RunTask(ctx, task.ID)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
	if !gitMock.destroyed {
		t.Errorf("expected DestroyWorktree to be called with non-cancellable context")
	}
}

type mockWorkerCancel struct {
	cancelFunc context.CancelFunc
}

func (m *mockWorkerCancel) ProduceSolution(ctx context.Context, task *state.Task, wt *git.Worktree, feedback string) error {
	m.cancelFunc()
	return nil
}

type mockGitCancel struct {
	mockGitBase
	destroyed bool
}

func (m *mockGitCancel) DestroyWorktree(ctx context.Context, wt *git.Worktree) error {
	if ctx.Err() != nil {
		return errors.New("expected non-cancelled context")
	}
	m.destroyed = true
	return nil
}

func TestRunTask_ConflictRepairLoop(t *testing.T) {
	store := &mockStore{tasks: make(map[string]*state.Task)}
	task, _ := store.CreateTask("task-1", 3)

	router := &mockRouter{decision: orchestrator.DecisionProceed}
	worker := &mockWorker{}
	validator := &mockValidator{passes: true}

	gitMock := &mockGitConflict{mockGitBase: mockGitBase{valid: true}}

	orch := newTestOrchestrator(store, router, worker, validator, gitMock)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := orch.RunTask(ctx, task.ID)

	if err != nil {
		t.Fatalf("expected nil, got: %v", err)
	}

	if gitMock.checkCount != 2 {
		t.Errorf("expected CheckForConflicts to be called twice, got: %d", gitMock.checkCount)
	}
	if gitMock.extractCount != 1 {
		t.Errorf("expected ExtractConflictRegions to be called once, got: %d", gitMock.extractCount)
	}
	if task.CurrentState != state.StateCommitted {
		t.Errorf("expected state COMMITTED, got: %v", task.CurrentState)
	}
}

type mockGitConflict struct {
	mockGitBase
	checkCount   int
	extractCount int
}

func (m *mockGitConflict) CheckForConflicts(ctx context.Context, wt *git.Worktree) (bool, error) {
	m.checkCount++
	return m.checkCount == 1, nil
}

func (m *mockGitConflict) ExtractConflictRegions(ctx context.Context, wt *git.Worktree) ([]git.ConflictRegion, error) {
	m.extractCount++
	return []git.ConflictRegion{{FilePath: "file.txt"}}, nil
}
