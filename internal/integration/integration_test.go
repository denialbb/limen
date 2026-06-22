package integration_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/denialbb/limen/internal/bus"
	"github.com/denialbb/limen/internal/git"
	"github.com/denialbb/limen/internal/orchestrator"
	"github.com/denialbb/limen/internal/state"
)

func setupTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	cmd.Run()
	cmd = exec.Command("git", "config", "user.name", "Test User")
	cmd.Dir = dir
	cmd.Run()
	cmd = exec.Command("git", "config", "user.email", "test@example.com")
	cmd.Dir = dir
	cmd.Run()
	cmd = exec.Command("git", "commit", "--allow-empty", "-m", "init")
	cmd.Dir = dir
	cmd.Run()
	return dir
}

type dummyRouter struct{}

func (r *dummyRouter) Evaluate(ctx context.Context, task *state.Task, em orchestrator.Emitter) (orchestrator.RouterDecision, error) {
	return orchestrator.DecisionProceed, nil
}

type dummyRetriever struct{}

func (r *dummyRetriever) Retrieve(ctx context.Context, task *state.Task, em orchestrator.Emitter) (string, error) {
	return "dummy-context", nil
}

type dummyWorker struct {
	callCount int
}

func (w *dummyWorker) ProduceSolution(ctx context.Context, task *state.Task, wt *git.Worktree, feedback string, em orchestrator.Emitter) error {
	w.callCount++
	// NOTE: Under the No Git Noise contract, the worker edits files but does not
	// commit. The worktree manager captures the uncommitted diff.
	return os.WriteFile(filepath.Join(wt.Path, "solution.txt"), []byte("solution\n"), 0644)
}

type dummyValidator struct {
	passes bool
}

func (v *dummyValidator) Evaluate(ctx context.Context, task *state.Task, wt *git.Worktree, em orchestrator.Emitter) (bool, string, error) {
	return v.passes, "feedback", nil
}

type dummyGitClient struct {
	manager git.WorktreeManager
}

func (g *dummyGitClient) IsValid(ctx context.Context) (bool, error) {
	return true, nil
}

func (g *dummyGitClient) ProvisionWorktree(ctx context.Context, baseCommit, branchName, path string) (*git.Worktree, error) {
	return g.manager.ProvisionWorktree(ctx, baseCommit, branchName, path)
}

func (g *dummyGitClient) CommitWorktree(ctx context.Context, taskID string, wt *git.Worktree) error {
	return g.manager.CommitWorktree(ctx, taskID, wt)
}

func (g *dummyGitClient) CheckForConflicts(ctx context.Context, wt *git.Worktree) (bool, error) {
	return g.manager.CheckForConflicts(ctx, wt)
}

func (g *dummyGitClient) ExtractConflictRegions(ctx context.Context, wt *git.Worktree) ([]git.ConflictRegion, error) {
	return g.manager.ExtractConflictRegions(ctx, wt)
}

func (g *dummyGitClient) DestroyWorktree(ctx context.Context, wt *git.Worktree) error {
	return g.manager.DestroyWorktree(ctx, wt)
}

func (g *dummyGitClient) GetWorktreeDiff(ctx context.Context, wt *git.Worktree) (string, error) {
	return g.manager.GetWorktreeDiff(ctx, wt)
}

func TestFullOrchestrationCycle(t *testing.T) {
	store, err := state.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	repoDir := setupTestRepo(t)
	baseCommit := getHeadCommit(t, repoDir)
	manager := git.NewWorktreeManager(repoDir, "main")
	router := &dummyRouter{}
	retriever := &dummyRetriever{}
	worker := &dummyWorker{}
	validator := &dummyValidator{passes: true}
	gitClient := &dummyGitClient{manager: manager}

	worktreeRoot := t.TempDir()
	b := bus.NewChannelBus()
	defer b.Close()
	orch := orchestrator.NewOrchestrator(store, b, router, retriever, worker, validator, gitClient, worktreeRoot)

	taskID := "task-integration-1"
	_, err = store.CreateTask(taskID, 3)
	if err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	err = orch.RunTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("Orchestrator run failed: %v", err)
	}

	task, err := store.GetTask(taskID)
	if err != nil {
		t.Fatalf("Failed to get task: %v", err)
	}

	if task.CurrentState != state.StateCommitted {
		t.Errorf("Expected state COMMITTED, got %s", task.CurrentState)
	}

	if worker.callCount != 1 {
		t.Errorf("Expected worker to be called exactly once, got %d", worker.callCount)
	}

	if task.FinalOutput == "" {
		t.Error("Expected FinalOutput to be recorded")
	}

	newCommit := getHeadCommit(t, repoDir)
	if newCommit == baseCommit {
		t.Error("Expected canonical branch to advance")
	}
}

func TestFullOrchestrationCycle_ValidatorRetry(t *testing.T) {
	store, err := state.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	repoDir := setupTestRepo(t)
	manager := git.NewWorktreeManager(repoDir, "main")
	router := &dummyRouter{}
	retriever := &dummyRetriever{}
	worker := &dummyWorker{}
	validator := &dummyValidator{passes: false}
	gitClient := &dummyGitClient{manager: manager}

	worktreeRoot := t.TempDir()
	b := bus.NewChannelBus()
	defer b.Close()
	orch := orchestrator.NewOrchestrator(store, b, router, retriever, worker, validator, gitClient, worktreeRoot)

	taskID := "task-integration-2"
	_, err = store.CreateTask(taskID, 2)
	if err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	err = orch.RunTask(context.Background(), taskID)
	if err == nil {
		t.Fatal("Expected error after validator exhausted retries, got nil")
	}

	task, err := store.GetTask(taskID)
	if err != nil {
		t.Fatalf("Failed to get task: %v", err)
	}

	if task.CurrentState != state.StateFailedEscalated {
		t.Errorf("Expected state FAILED_ESCALATED, got %s", task.CurrentState)
	}

	// Called once initially + 2 retries = 3 calls
	if worker.callCount != 3 {
		t.Errorf("Expected worker to be called 3 times, got %d", worker.callCount)
	}
}

func getHeadCommit(t *testing.T, repoDir string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("Failed to get HEAD: %v", err)
	}
	return string(out)
}
