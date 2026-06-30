package integration_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/denialbb/limen/internal/bus"
	"github.com/denialbb/limen/internal/git"
	"github.com/denialbb/limen/internal/orchestrator"
	"github.com/denialbb/limen/internal/state"
)

// binaryCache builds the limen binary at most once per test run.
var (
	binaryOnce  sync.Once
	binaryPath  string
	binaryErr   error
)

func setupTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-b", "main")
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

// buildLimenBinary compiles cmd/limen into a temp binary and returns its path.
// The binary is built at most once per test run and cached.
func buildLimenBinary(t *testing.T) string {
	t.Helper()
	binaryOnce.Do(func() {
		root := repoRoot(t)
		bin := filepath.Join(t.TempDir(), "limen")
		// NODE: Build from the repo root so module resolution finds
		// internal packages correctly.
		cmd := exec.Command("go", "build", "-o", bin, "./cmd/limen")
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			binaryErr = fmt.Errorf("build limen binary: %w\nOutput: %s", err, string(out))
			return
		}
		binaryPath = bin
	})
	if binaryErr != nil {
		t.Fatalf("buildLimenBinary: %v", binaryErr)
	}
	return binaryPath
}

// repoRoot returns the absolute path to the repository root by searching
// upward from the test file for go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to get caller info")
	}
	dir := filepath.Dir(filename)
	// Walk up until we find go.mod
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root (go.mod not found)")
		}
		dir = parent
	}
}

// TestEndToEndWithRealBinary launches the real limen binary as a subprocess
// against a real git repo with the mock transcript.  This is the Layer 3
// integration test from the PRD — it catches Go-Python contract drift.
//
// NODE: This test is skipped under `go test -short` because it builds the
// binary and runs subprocesses, adding ~5 s to the test suite.
func TestEndToEndWithRealBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping end-to-end binary test in short mode")
	}

	// --- build binary --------------------------------------------------------
	binaryPath := buildLimenBinary(t)

	// --- resolve transcript path ---------------------------------------------
	root := repoRoot(t)
	transcriptPath := filepath.Join(root, "src", "limen", "mock", "transcripts", "spike.json")
	if _, err := os.Stat(transcriptPath); err != nil {
		t.Fatalf("spike transcript not found at %s: %v", transcriptPath, err)
	}

	// --- create real git repo ------------------------------------------------
	repoDir := setupTestRepo(t)
	baseCommit := strings.TrimSpace(getHeadCommit(t, repoDir))

	// NODE: Put the database OUTSIDE the repo directory so git status
	// --porcelain does not see untracked .db / .db-wal / .db-shm files
	// and reject the repo as dirty (cliGit.IsValid checks for a clean
	// worktree before proceeding).
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "limen.db")

	// --- run the binary ------------------------------------------------------
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binaryPath, "run-task",
		"--task-id", "spike-demo",
		"--db-path", dbPath,
		"--repo-path", repoDir,
		"--mock",
		"--mock-transcript", transcriptPath,
	)
	// NODE: The limen binary spawns Python subprocesses that import the
	// `limen` package from src/.  The subprocesses inherit the parent's
	// environment, so we inject PYTHONPATH to point at the repo's `src`
	// directory.  Without this, Python raises ModuleNotFoundError.
	cmd.Dir = repoDir
	cmd.Env = append(os.Environ(), "PYTHONPATH="+filepath.Join(root, "src"))

	output, err := cmd.CombinedOutput()
	// NODE: We capture the output even on error so assertions below can
	// inspect it for diagnostic information.
	outputStr := string(output)

	if err != nil {
		// The task may have failed before COMMITTED; dump the full output
		// for debugging rather than failing silently.
		t.Logf("limen run-task exited with error: %v", err)
	}

	// --- assert: log output shows COMMITTED ----------------------------------
	if !strings.Contains(outputStr, "Task completed with state: COMMITTED") {
		t.Errorf("Expected log output to contain 'COMMITTED', got:\n%s", outputStr)
	}

	// --- assert: canonical branch advanced -----------------------------------
	newCommit := strings.TrimSpace(getHeadCommit(t, repoDir))
	if newCommit == baseCommit {
		t.Errorf("Expected canonical branch (HEAD) to advance from %s", baseCommit)
	}

	// --- assert: solution.txt was committed to main --------------------------
	// NODE: The mock worker writes "fixed solution" on its second attempt
	// (the transcript has two worker entries).  CommitWorktree commits the
	// final worktree state to main via update-ref.
	showCmd := exec.Command("git", "show", "main:solution.txt")
	showCmd.Dir = repoDir
	solutionOut, err := showCmd.Output()
	if err != nil {
		// If solution.txt is not in the tree, the commit may not have happened.
		t.Fatalf("Expected solution.txt to exist in main branch: %v\nOutput: %s\nBinary output:\n%s",
			err, string(solutionOut), outputStr)
	}
	solutionContent := strings.TrimSpace(string(solutionOut))
	if !strings.Contains(solutionContent, "fixed solution") {
		t.Errorf("Expected solution.txt to contain 'fixed solution', got %q", solutionContent)
	}

	// --- assert: store reflects COMMITTED ------------------------------------
	store, err := state.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("Failed to open store at %s: %v", dbPath, err)
	}
	defer store.Close()

	task, err := store.GetTask("spike-demo")
	if err != nil {
		t.Fatalf("Failed to get task: %v", err)
	}
	if task.CurrentState != state.StateCommitted {
		t.Errorf("Expected state COMMITTED, got %s", task.CurrentState)
	}
	if task.FinalOutput == "" {
		t.Error("Expected FinalOutput to be recorded")
	}

	// --- assert: event sequence (from state transitions) ---------------------
	transitions, err := store.GetStateTransitions("spike-demo")
	if err != nil {
		t.Fatalf("Failed to get state transitions: %v", err)
	}

	// NODE: Expected spike path (PRD §"Spike Path"):
	//   1. CONTEXT_BUILDING
	//   2. ROUTING_EVALUATION
	//   3. WORKER_RUNNING
	//   4. AWAITING_VALIDATION
	//   5. REVISION_REQUESTED   ← first validator fail
	//   6. WORKER_RUNNING       ← retry
	//   7. AWAITING_VALIDATION
	//   8. APPROVED             ← second validator pass
	//   9. COMMITTED            ← no conflict, git commit
	expectedToStates := []state.TaskState{
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

	if len(transitions) != len(expectedToStates) {
		t.Errorf("Expected %d state transitions, got %d.\nTransitions:",
			len(expectedToStates), len(transitions))
		for i, st := range transitions {
			t.Errorf("  [%d] %s -> %s", i, st.FromState, st.ToState)
		}
	} else {
		for i, st := range transitions {
			if st.ToState != expectedToStates[i] {
				t.Errorf("Transition %d: expected ToState=%s, got ToState=%s (from %s)",
					i, expectedToStates[i], st.ToState, st.FromState)
			}
		}
	}

	// --- assert: validation decisions (fail then pass) -----------------------
	decisions, err := store.GetValidationDecisions("spike-demo")
	if err != nil {
		t.Fatalf("Failed to get validation decisions: %v", err)
	}
	if len(decisions) != 2 {
		t.Errorf("Expected 2 validation decisions, got %d", len(decisions))
	} else {
		if decisions[0].Pass {
			t.Errorf("Expected first validation decision to fail, got pass=%v", decisions[0].Pass)
		}
		if !decisions[1].Pass {
			t.Errorf("Expected second validation decision to pass, got pass=%v", decisions[1].Pass)
		}
	}

	// --- assert: tool calls recorded -----------------------------------------
	toolCalls, err := store.GetToolCalls("spike-demo")
	if err != nil {
		t.Fatalf("Failed to get tool calls: %v", err)
	}
	// We expect at least: router.Evaluate, worker.ProduceSolution (2x),
	// validator.Evaluate (2x) = 5 tool-call records.
	if len(toolCalls) < 3 {
		t.Errorf("Expected at least 3 tool calls (router, worker, validator), got %d: %v",
			len(toolCalls), toolCalls)
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

func TestSubmitVerdict(t *testing.T) {
	binaryPath := buildLimenBinary(t)
	
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "limen.db")
	
	store, err := state.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("Failed to open store: %v", err)
	}
	defer store.Close()
	
	taskID := "test-submit-verdict-task"
	_, err = store.CreateTask(taskID, 3)
	if err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}
	
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Run ready-for-review in background
	readyCmd := exec.CommandContext(ctx, binaryPath, "ready-for-review",
		"--task-id", taskID,
		"--db-path", dbPath,
		"--summary", "I did the work",
	)
	
	// Capture stdout
	var readyOut strings.Builder
	readyCmd.Stdout = &readyOut
	
	if err := readyCmd.Start(); err != nil {
		t.Fatalf("Failed to start ready-for-review: %v", err)
	}
	
	// Wait a bit to ensure ready-for-review has written the pending callback
	time.Sleep(500 * time.Millisecond)
	
	// 2. Run submit-verdict
	submitCmd := exec.Command(binaryPath, "submit-verdict",
		"--task-id", taskID,
		"--db-path", dbPath,
		"--passes=true",
		"--feedback", "Looks great",
	)
	
	submitOut, err := submitCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("submit-verdict failed: %v\nOutput: %s", err, string(submitOut))
	}
	
	// 3. Wait for ready-for-review to finish
	errCh := make(chan error, 1)
	go func() {
		errCh <- readyCmd.Wait()
	}()
	
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("ready-for-review failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("ready-for-review timed out waiting for verdict")
	}
	
	// 4. Verify output
	outputStr := strings.TrimSpace(readyOut.String())
	if !strings.Contains(outputStr, `"passes":true`) || !strings.Contains(outputStr, `"feedback":"Looks great"`) {
		t.Errorf("Expected output to contain verdict JSON, got: %s", outputStr)
	}
	
	// 5. Verify database records
	decisions, err := store.GetValidationDecisions(taskID)
	if err != nil {
		t.Fatalf("Failed to get validation decisions: %v", err)
	}
	if len(decisions) != 1 {
		t.Fatalf("Expected 1 validation decision, got %d", len(decisions))
	}
	if !decisions[0].Pass || decisions[0].Feedback != "Looks great" {
		t.Errorf("Unexpected validation decision values: %+v", decisions[0])
	}
}
