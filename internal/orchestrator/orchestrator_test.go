package orchestrator_test

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/denialbb/limen/internal/bus"
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

func (m *mockStore) RecordToolCall(id, call, args, response string) error {
	return nil
}

func (m *mockStore) GetToolCalls(id string) ([]state.ToolCall, error) {
	return nil, nil
}

func (m *mockStore) WriteCallbackSignal(taskID, summary string) (int64, error) {
	return 1, nil
}

func (m *mockStore) PollCallbackSignal(callbackID int64) (string, bool, error) {
	return "", false, nil
}

func (m *mockStore) GetPendingCallback(taskID string) (int64, string, bool, error) {
	return 0, "", false, nil
}

func (m *mockStore) WriteCallbackVerdict(callbackID int64, verdict string) error {
	return nil
}

// recordingMockStore wraps a mockStore and records tool-call invocations.
type recordingMockStore struct {
	mockStore
	calls []toolCallRecord
}

type toolCallRecord struct {
	taskID   string
	call     string
	args     string
	response string
}

func (r *recordingMockStore) RecordToolCall(id, call, args, response string) error {
	r.calls = append(r.calls, toolCallRecord{taskID: id, call: call, args: args, response: response})
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

func (m *mockStore) TransitionAndRecordFinalOutput(id string, newState state.TaskState, finalOutput string) error {
	t, ok := m.tasks[id]
	if !ok {
		return state.ErrTaskNotFound
	}
	if !state.IsValidTransition(t.CurrentState, newState) {
		return state.ErrInvalidTransition
	}
	t.CurrentState = newState
	t.FinalOutput = finalOutput
	return nil
}

func (m *mockStore) TransitionAndRecordContextSnapshot(id string, newState state.TaskState, snapshot string) error {
	t, ok := m.tasks[id]
	if !ok {
		return state.ErrTaskNotFound
	}
	if !state.IsValidTransition(t.CurrentState, newState) {
		return state.ErrInvalidTransition
	}
	t.CurrentState = newState
	t.ContextSnapshot = snapshot
	return nil
}

// Simple mocks for other interfaces
type mockRouter struct {
	decision orchestrator.RouterDecision
}

func (m *mockRouter) Evaluate(ctx context.Context, task *state.Task, em orchestrator.Emitter) (orchestrator.RouterDecision, error) {
	return m.decision, nil
}

type mockRetriever struct{}

func (m *mockRetriever) Retrieve(ctx context.Context, task *state.Task, em orchestrator.Emitter) (string, error) {
	return "mock-context", nil
}

type mockWorker struct {
	called bool
}

func (m *mockWorker) ProduceSolution(ctx context.Context, task *state.Task, wt *git.Worktree, feedback string, em orchestrator.Emitter) error {
	m.called = true
	return nil
}

type mockValidator struct {
	passes bool
}

func (m *mockValidator) Evaluate(ctx context.Context, task *state.Task, wt *git.Worktree, em orchestrator.Emitter) (bool, string, error) {
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

func (m *mockGit) ProvisionThrowawayWorktree(ctx context.Context, patch string) (*git.Worktree, error) {
	return &git.Worktree{Path: "/tmp/mock-throwaway", Branch: "", BaseCommit: "mock-base"}, nil
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
	return newTestOrchestratorWithBus(store, bus.NewChannelBus(), router, worker, validator, gitClient)
}

// newTestOrchestratorWithBus constructs an orchestrator wired to a caller-
// supplied bus, so tests that assert on emitted events can subscribe or
// inspect the stream.
func newTestOrchestratorWithBus(store state.Store, b bus.EventBus, router orchestrator.Router, worker orchestrator.Worker, validator orchestrator.Validator, gitClient orchestrator.GitClient) orchestrator.Orchestrator {
	return orchestrator.NewOrchestrator(store, b, router, &mockRetriever{}, worker, validator, gitClient, worktreeRoot())
}

func worktreeRoot() string {
	// NOTE: t.TempDir() returns an absolute path; for tests without a *testing.T
	// we use a fixed absolute fallback. Production callers must supply an absolute
	// path explicitly.
	return filepath.Join("/tmp", "limen-test-worktrees")
}

func TestRunTask_Success(t *testing.T) {
	store := &mockStore{tasks: make(map[string]*state.Task)}
	task, _ := store.CreateTask("task-1", 3, "")

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
	task, _ := store.CreateTask("task-1", 3, "")

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
	task, _ := store.CreateTask("task-1", 1, "") // Only 1 retry allowed

	router := &mockRouter{decision: orchestrator.DecisionProceed}
	worker := &mockWorker{}
	validator := &mockValidator{passes: false} // Will fail validation, trigger retry
	git := &mockGit{valid: true}

	orch, rec := newRecordingOrchestrator(store, router, worker, validator, git)
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

	finals := rec.EventsByKind("TaskFinalized")
	if len(finals) != 1 {
		t.Fatalf("expected 1 TaskFinalized event, got %d", len(finals))
	}
	f, ok := finals[0].(*bus.TaskFinalized)
	if !ok {
		t.Fatalf("expected TaskFinalized, got %T", finals[0])
	}
	if f.FinalState != state.StateFailedEscalated {
		t.Errorf("expected FinalState FAILED_ESCALATED, got %s", f.FinalState)
	}
	if f.TaskID != task.ID {
		t.Errorf("expected TaskID %s, got %s", task.ID, f.TaskID)
	}
}

func TestRunTask_GitInvalid(t *testing.T) {
	store := &mockStore{tasks: make(map[string]*state.Task)}
	task, _ := store.CreateTask("task-1", 3, "")

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
	task, _ := store.CreateTask("task-1", 3, "")

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
	task, _ := store.CreateTask("task-1", 3, "")

	router := &mockRouter{decision: orchestrator.DecisionExpand}
	worker := &mockWorker{}
	validator := &mockValidator{passes: true}
	git := &mockGit{valid: true}

	orch, rec := newRecordingOrchestrator(store, router, worker, validator, git)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := orch.RunTask(ctx, task.ID)

	if !errors.Is(err, orchestrator.ErrUnresolvableEntropy) {
		t.Fatalf("expected ErrUnresolvableEntropy after max expand iterations, got: %v", err)
	}

	if task.CurrentState != state.StateFailedEscalated {
		t.Errorf("expected state FAILED_ESCALATED, got: %s", task.CurrentState)
	}

	finals := rec.EventsByKind("TaskFinalized")
	if len(finals) != 1 {
		t.Fatalf("expected 1 TaskFinalized event, got %d", len(finals))
	}
	f, ok := finals[0].(*bus.TaskFinalized)
	if !ok {
		t.Fatalf("expected TaskFinalized, got %T", finals[0])
	}
	if f.FinalState != state.StateFailedEscalated {
		t.Errorf("expected FinalState FAILED_ESCALATED, got %s", f.FinalState)
	}
	if f.FinalOutputRef != "" {
		t.Errorf("expected empty FinalOutputRef on expand exhaustion, got %q", f.FinalOutputRef)
	}
	if f.TaskID != task.ID {
		t.Errorf("expected TaskID %s, got %s", task.ID, f.TaskID)
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

func (m *mockErrorWorker) ProduceSolution(ctx context.Context, task *state.Task, wt *git.Worktree, feedback string, em orchestrator.Emitter) error {
	return errMockWorker
}

// Compile-time check that mockErrorWorker implements Worker.
var _ orchestrator.Worker = (*mockErrorWorker)(nil)

func TestRunTask_GitError(t *testing.T) {
	store := &mockStore{tasks: make(map[string]*state.Task)}
	task, _ := store.CreateTask("task-1", 3, "")

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
	task, _ := store.CreateTask("task-1", 3, "")

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
	task, _ := store.CreateTask("task-1", 3, "")

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
	task, _ := store.CreateTask("task-1", 3, "")

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
func (m *mockGitBase) ProvisionThrowawayWorktree(ctx context.Context, patch string) (*git.Worktree, error) {
	return &git.Worktree{Path: "/tmp/mock-throwaway", Branch: "", BaseCommit: "mock-base"}, nil
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

func (m *mockErrorRouter) Evaluate(ctx context.Context, task *state.Task, em orchestrator.Emitter) (orchestrator.RouterDecision, error) {
	return orchestrator.DecisionProceed, errMockRouter
}

// Compile-time check that mockErrorRouter implements Router.
var _ orchestrator.Router = (*mockErrorRouter)(nil)

type mockErrorValidator struct{}

func (m *mockErrorValidator) Evaluate(ctx context.Context, task *state.Task, wt *git.Worktree, em orchestrator.Emitter) (bool, string, error) {
	return false, "", errMockValidator
}

// Compile-time check that mockErrorValidator implements Validator.
var _ orchestrator.Validator = (*mockErrorValidator)(nil)

func TestRunTask_ContextCancellation(t *testing.T) {
	store := &mockStore{tasks: make(map[string]*state.Task)}
	task, _ := store.CreateTask("task-1", 3, "")

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

func (m *mockWorkerCancel) ProduceSolution(ctx context.Context, task *state.Task, wt *git.Worktree, feedback string, em orchestrator.Emitter) error {
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
	task, _ := store.CreateTask("task-1", 3, "")

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

// recorderBus is an EventBus adapter that records every published event into
// a bus.RecorderEmitter. It is used by event-assertion tests that need to
// inspect the published stream without spawning a subscriber goroutine.
// Subscribe returns an already-closed channel so the EventBus contract is
// satisfied without a real consumer.
type recorderBus struct {
	rec *bus.RecorderEmitter
}

func (r *recorderBus) Publish(ev bus.Event)        { r.rec.Publish(ev) }
func (r *recorderBus) Subscribe() <-chan bus.Event { ch := make(chan bus.Event); close(ch); return ch }
func (r *recorderBus) Close()                      {}

// Compile-time check that recorderBus satisfies bus.EventBus.
var _ bus.EventBus = (*recorderBus)(nil)

// newRecordingOrchestrator wires an orchestrator to a recorderBus and returns
// both the orchestrator and the underlying recorder for assertions.
func newRecordingOrchestrator(store state.Store, router orchestrator.Router, worker orchestrator.Worker, validator orchestrator.Validator, gitClient orchestrator.GitClient) (orchestrator.Orchestrator, *bus.RecorderEmitter) {
	rec := bus.NewRecorderEmitter()
	b := &recorderBus{rec: rec}
	return newTestOrchestratorWithBus(store, b, router, worker, validator, gitClient), rec
}

// findStateChange returns the TaskStateChanged event with the given from/to
// pair, or nil if none was emitted. It scans the recorder's events directly so
// the assertion is exact rather than substring-based.
func findStateChange(t *testing.T, rec *bus.RecorderEmitter, from, to state.TaskState) *bus.TaskStateChanged {
	t.Helper()
	for _, ev := range rec.Events() {
		sc, ok := ev.(*bus.TaskStateChanged)
		if !ok {
			continue
		}
		if sc.From == from && sc.To == to {
			return sc
		}
	}
	return nil
}

// TestRunTask_EmitsTaskStateChanged verifies that the orchestrator emits a
// TaskStateChanged event for the CREATED -> CONTEXT_BUILDING -> ROUTING_EVAL
// -> WORKER_RUNNING -> AWAITING_VALIDATION -> APPROVED -> COMMITTED happy
// path. Each transition must publish exactly one event with the right pair.
func TestRunTask_EmitsTaskStateChanged(t *testing.T) {
	store := &mockStore{tasks: make(map[string]*state.Task)}
	task, _ := store.CreateTask("task-events-1", 3, "")

	router := &mockRouter{decision: orchestrator.DecisionProceed}
	worker := &mockWorker{}
	validator := &mockValidator{passes: true}
	gitClient := &mockGit{valid: true}

	orch, rec := newRecordingOrchestrator(store, router, worker, validator, gitClient)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	if err := orch.RunTask(ctx, task.ID); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	expected := []struct{ from, to state.TaskState }{
		{state.StateCreated, state.StateContextBuilding},
		{state.StateContextBuilding, state.StateRoutingEvaluation},
		{state.StateRoutingEvaluation, state.StateWorkerRunning},
		{state.StateWorkerRunning, state.StateAwaitingValidation},
		{state.StateAwaitingValidation, state.StateApproved},
		{state.StateApproved, state.StateCommitted},
	}
	changes := rec.EventsByKind("TaskStateChanged")
	if len(changes) != len(expected) {
		t.Fatalf("expected %d TaskStateChanged events, got %d", len(expected), len(changes))
	}
	for i, want := range expected {
		sc, ok := changes[i].(*bus.TaskStateChanged)
		if !ok {
			t.Fatalf("event %d: expected TaskStateChanged, got %T", i, changes[i])
		}
		if sc.From != want.from || sc.To != want.to {
			t.Errorf("event %d: expected %s->%s, got %s->%s",
				i, want.from, want.to, sc.From, sc.To)
		}
		if sc.TaskID != task.ID {
			t.Errorf("event %d: expected TaskID %s, got %s", i, task.ID, sc.TaskID)
		}
		if sc.Timestamp.IsZero() {
			t.Errorf("event %d: expected non-zero timestamp", i)
		}
	}
}

// TestRunTask_EmitsTaskFinalizedOnCommitted verifies that the COMMITTED
// terminal state emits exactly one TaskFinalized event carrying the diff
// reference recorded as the final output.
func TestRunTask_EmitsTaskFinalizedOnCommitted(t *testing.T) {
	store := &mockStore{tasks: make(map[string]*state.Task)}
	task, _ := store.CreateTask("task-finalized-1", 3, "")

	router := &mockRouter{decision: orchestrator.DecisionProceed}
	worker := &mockWorker{}
	validator := &mockValidator{passes: true}
	gitClient := &mockGit{valid: true}

	orch, rec := newRecordingOrchestrator(store, router, worker, validator, gitClient)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	if err := orch.RunTask(ctx, task.ID); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	finals := rec.EventsByKind("TaskFinalized")
	if len(finals) != 1 {
		t.Fatalf("expected 1 TaskFinalized event, got %d", len(finals))
	}
	f, ok := finals[0].(*bus.TaskFinalized)
	if !ok {
		t.Fatalf("expected TaskFinalized, got %T", finals[0])
	}
	if f.FinalState != state.StateCommitted {
		t.Errorf("expected FinalState COMMITTED, got %s", f.FinalState)
	}
	if f.FinalOutputRef == "" {
		t.Errorf("expected non-empty FinalOutputRef, got %q", f.FinalOutputRef)
	}
	if f.TaskID != task.ID {
		t.Errorf("expected TaskID %s, got %s", task.ID, f.TaskID)
	}
}

type mockBlockingWorker struct {
	dbPath string
}

func (m *mockBlockingWorker) ProduceSolution(ctx context.Context, task *state.Task, wt *git.Worktree, feedback string, em orchestrator.Emitter) error {
	cmd := exec.CommandContext(ctx, "go", "run", "github.com/denialbb/limen/cmd/limen", "ready-for-review", "--task-id", task.ID, "--summary", "mock summary", "--db-path", m.dbPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ready-for-review failed: %w, out: %s", err, string(out))
	}
	if !strings.Contains(string(out), `"passes":true`) {
		return fmt.Errorf("unexpected verdict: %s", string(out))
	}
	return nil
}

func TestBlockingCallbackRoundTrip(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := state.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	task, _ := store.CreateTask("task-1", 5, "")

	router := &mockRouter{decision: orchestrator.DecisionProceed}
	worker := &mockBlockingWorker{dbPath: dbPath}
	validator := &mockValidator{passes: true}
	gitMock := &mockGit{valid: true}

	orch := newTestOrchestrator(store, router, worker, validator, gitMock)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := orch.RunTask(ctx, task.ID); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	tTask, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if tTask.CurrentState != state.StateCommitted {
		t.Errorf("expected state COMMITTED, got: %s", tTask.CurrentState)
	}
}


// TestRunTask_EmitsTaskFinalizedOnEscalation verifies that the FAILED_ESCALATED
// terminal state (via router Escalate) emits exactly one TaskFinalized event
// with an empty FinalOutputRef.
func TestRunTask_EmitsTaskFinalizedOnEscalation(t *testing.T) {
	store := &mockStore{tasks: make(map[string]*state.Task)}
	task, _ := store.CreateTask("task-finalized-2", 3, "")

	router := &mockRouter{decision: orchestrator.DecisionEscalate}
	worker := &mockWorker{}
	validator := &mockValidator{passes: true}
	gitClient := &mockGit{valid: true}

	orch, rec := newRecordingOrchestrator(store, router, worker, validator, gitClient)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_ = orch.RunTask(ctx, task.ID)

	finals := rec.EventsByKind("TaskFinalized")
	if len(finals) != 1 {
		t.Fatalf("expected 1 TaskFinalized event, got %d", len(finals))
	}
	f, ok := finals[0].(*bus.TaskFinalized)
	if !ok {
		t.Fatalf("expected TaskFinalized, got %T", finals[0])
	}
	if f.FinalState != state.StateFailedEscalated {
		t.Errorf("expected FinalState FAILED_ESCALATED, got %s", f.FinalState)
	}
	if f.FinalOutputRef != "" {
		t.Errorf("expected empty FinalOutputRef on escalation, got %q", f.FinalOutputRef)
	}
}

// TestRunTask_EmitsConflictDetected verifies that the orchestrator publishes a
// ConflictDetected event carrying the extracted conflict regions when the git
// layer reports a conflict, and that the loop still completes to COMMITTED
// after the conflict is resolved on the retry.
func TestRunTask_EmitsConflictDetected(t *testing.T) {
	store := &mockStore{tasks: make(map[string]*state.Task)}
	task, _ := store.CreateTask("task-conflict-1", 3, "")

	router := &mockRouter{decision: orchestrator.DecisionProceed}
	worker := &mockWorker{}
	validator := &mockValidator{passes: true}
	gitClient := &mockGitConflict{mockGitBase: mockGitBase{valid: true}}

	orch, rec := newRecordingOrchestrator(store, router, worker, validator, gitClient)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	if err := orch.RunTask(ctx, task.ID); err != nil {
		t.Fatalf("expected nil, got: %v", err)
	}

	conflicts := rec.EventsByKind("ConflictDetected")
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 ConflictDetected event, got %d", len(conflicts))
	}
	c, ok := conflicts[0].(*bus.ConflictDetected)
	if !ok {
		t.Fatalf("expected ConflictDetected, got %T", conflicts[0])
	}
	if c.TaskID != task.ID {
		t.Errorf("expected TaskID %s, got %s", task.ID, c.TaskID)
	}
	if len(c.Regions) != 1 || c.Regions[0].FilePath != "file.txt" {
		t.Errorf("expected 1 region at file.txt, got %+v", c.Regions)
	}
	if c.Timestamp.IsZero() {
		t.Error("expected non-zero timestamp")
	}

	// The escalation path on conflict exhaustion must not fire here; the loop
	// should still terminate with a COMMITTED finalize event.
	if findStateChange(t, rec, state.StateApproved, state.StateCommitted) == nil {
		t.Error("expected APPROVED->COMMITTED transition after conflict was resolved")
	}
}

// mockGitConflictAlways reports a conflict on every CheckForConflicts call and
// returns a stable conflict region from ExtractConflictRegions. It is used to
// drive the retry-exhaustion path in the conflict-handling loop.
type mockGitConflictAlways struct {
	mockGitBase
	extractCount int
}

func (m *mockGitConflictAlways) CheckForConflicts(ctx context.Context, wt *git.Worktree) (bool, error) {
	return true, nil
}

func (m *mockGitConflictAlways) ExtractConflictRegions(ctx context.Context, wt *git.Worktree) ([]git.ConflictRegion, error) {
	m.extractCount++
	return []git.ConflictRegion{{FilePath: "file.txt"}}, nil
}

// TestRunTask_EmitsTaskFinalizedOnConflictExhaustion verifies that when a
// conflict is reported on every pass and retries are exhausted, the task
// terminates with FAILED_ESCALATED and emits exactly one TaskFinalized event.
func TestRunTask_EmitsTaskFinalizedOnConflictExhaustion(t *testing.T) {
	store := &mockStore{tasks: make(map[string]*state.Task)}
	task, _ := store.CreateTask("task-conflict-exhaust-1", 1, "")

	router := &mockRouter{decision: orchestrator.DecisionProceed}
	worker := &mockWorker{}
	validator := &mockValidator{passes: true}
	gitClient := &mockGitConflictAlways{mockGitBase: mockGitBase{valid: true}}

	orch, rec := newRecordingOrchestrator(store, router, worker, validator, gitClient)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := orch.RunTask(ctx, task.ID)
	if err == nil {
		t.Fatal("expected error after conflict retries exhausted, got nil")
	}

	if task.CurrentState != state.StateFailedEscalated {
		t.Errorf("expected state FAILED_ESCALATED, got: %s", task.CurrentState)
	}

	finals := rec.EventsByKind("TaskFinalized")
	if len(finals) != 1 {
		t.Fatalf("expected 1 TaskFinalized event, got %d", len(finals))
	}
	f, ok := finals[0].(*bus.TaskFinalized)
	if !ok {
		t.Fatalf("expected TaskFinalized, got %T", finals[0])
	}
	if f.FinalState != state.StateFailedEscalated {
		t.Errorf("expected FinalState FAILED_ESCALATED, got %s", f.FinalState)
	}
	if f.TaskID != task.ID {
		t.Errorf("expected TaskID %s, got %s", task.ID, f.TaskID)
	}
}

// TestRunTask_RecordsToolCalls verifies that the orchestrator records
// tool calls with the full (call, args, response) shape through the happy
// path, guarding against the lossy label-only recording fixed by BUG #1.
func TestRunTask_RecordsToolCalls(t *testing.T) {
	store := &recordingMockStore{mockStore: mockStore{tasks: make(map[string]*state.Task)}}
	task, _ := store.CreateTask("task-tc-1", 3, "")

	router := &mockRouter{decision: orchestrator.DecisionProceed}
	worker := &mockWorker{}
	validator := &mockValidator{passes: true}
	gitClient := &mockGit{valid: true}

	orch := orchestrator.NewOrchestrator(store, bus.NewChannelBus(), router, &mockRetriever{}, worker, validator, gitClient, worktreeRoot())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	if err := orch.RunTask(ctx, task.ID); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// The happy path exercises router.Evaluate, worker.ProduceSolution, and
	// validator.Evaluate. Verify all three were recorded with the correct
	// shape (even though args/response are empty for now — the shape is what
	// matters; real args/response will come with the spike).
	if len(store.calls) != 3 {
		t.Fatalf("expected 3 tool calls on happy path, got: %d", len(store.calls))
	}

	expected := []toolCallRecord{
		{taskID: task.ID, call: "router.Evaluate", args: "", response: ""},
		{taskID: task.ID, call: "worker.ProduceSolution", args: "", response: ""},
		{taskID: task.ID, call: "validator.Evaluate", args: "", response: ""},
	}

	for i, want := range expected {
		if store.calls[i].call != want.call {
			t.Errorf("call %d: expected %q, got %q", i, want.call, store.calls[i].call)
		}
		if store.calls[i].taskID != want.taskID {
			t.Errorf("call %d: expected taskID %q, got %q", i, want.taskID, store.calls[i].taskID)
		}
		if store.calls[i].args != want.args {
			t.Errorf("call %d: expected args %q, got %q", i, want.args, store.calls[i].args)
		}
		if store.calls[i].response != want.response {
			t.Errorf("call %d: expected response %q, got %q", i, want.response, store.calls[i].response)
		}
	}
}
