package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/denialbb/limen/internal/git"
	"github.com/denialbb/limen/internal/orchestrator"
	"github.com/denialbb/limen/internal/state"
)

// cliRouter is a placeholder router that always proceeds.
// TODO: Replace with the real routing heuristic or LLM evaluator.
type cliRouter struct{}

func (c *cliRouter) Evaluate(ctx context.Context, task *state.Task) (orchestrator.RouterDecision, error) {
	return orchestrator.DecisionProceed, nil
}

// cliRetriever is a placeholder retriever that returns an empty context manifest.
// TODO: Replace with the real progressive retrieval pipeline (BM25 + semantic).
type cliRetriever struct{}

func (c *cliRetriever) Retrieve(ctx context.Context, task *state.Task) (string, error) {
	return "", nil
}

// cliWorker is a placeholder worker that logs and does nothing.
// TODO: Replace with the real LLM-backed worker.
type cliWorker struct{}

// ProduceSolution implements the worker interface.
func (c *cliWorker) ProduceSolution(ctx context.Context, task *state.Task, wt *git.Worktree, feedback string) error {
	log.Printf("Worker producing solution for task %s", task.ID)
	dummyPath := filepath.Join(wt.Path, "dummy_solution.txt")
	return os.WriteFile(dummyPath, []byte("Hello from cliWorker"), 0644)
}

// cliValidator is a placeholder validator that always passes.
// TODO: Replace with the real L3 validator.
type cliValidator struct{}

func (v *cliValidator) Evaluate(ctx context.Context, task *state.Task, wt *git.Worktree) (bool, string, error) {
	log.Printf("Validator evaluating solution for task %s", task.ID)
	return true, "LGTM", nil
}

// cliGit implements the orchestrator GitClient using the real WorktreeManager.
type cliGit struct {
	manager    git.WorktreeManager
	repoPath   string
}

func (c *cliGit) IsValid(ctx context.Context) (bool, error) {
	// Pipeline gate 1: verify the repository is inside a git worktree and has no
	// uncommitted changes or known integrity issues.
	cmdDir := exec.CommandContext(ctx, "git", "rev-parse", "--git-dir")
	cmdDir.Dir = c.repoPath
	if out, err := cmdDir.CombinedOutput(); err != nil {
		return false, fmt.Errorf("not a git repository: %w, output: %s", err, string(out))
	}

	cmdStatus := exec.CommandContext(ctx, "git", "status", "--porcelain")
	cmdStatus.Dir = c.repoPath
	out, err := cmdStatus.Output()
	if err != nil {
		return false, fmt.Errorf("git status failed: %w", err)
	}
	if strings.TrimSpace(string(out)) != "" {
		return false, nil
	}

	cmdFsck := exec.CommandContext(ctx, "git", "fsck", "--full")
	cmdFsck.Dir = c.repoPath
	if err := cmdFsck.Run(); err != nil {
		return false, nil
	}

	return true, nil
}

func (c *cliGit) ProvisionWorktree(ctx context.Context, baseCommit, branchName, path string) (*git.Worktree, error) {
	return c.manager.ProvisionWorktree(ctx, baseCommit, branchName, path)
}
func (c *cliGit) CommitWorktree(ctx context.Context, taskID string, wt *git.Worktree) error {
	return c.manager.CommitWorktree(ctx, taskID, wt)
}
func (c *cliGit) CheckForConflicts(ctx context.Context, wt *git.Worktree) (bool, error) {
	return c.manager.CheckForConflicts(ctx, wt)
}
func (c *cliGit) ExtractConflictRegions(ctx context.Context, wt *git.Worktree) ([]git.ConflictRegion, error) {
	return c.manager.ExtractConflictRegions(ctx, wt)
}
func (c *cliGit) DestroyWorktree(ctx context.Context, wt *git.Worktree) error {
	return c.manager.DestroyWorktree(ctx, wt)
}
func (c *cliGit) GetWorktreeDiff(ctx context.Context, wt *git.Worktree) (string, error) {
	return c.manager.GetWorktreeDiff(ctx, wt)
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]

	switch command {
	case "run-task":
		runTaskCmd()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, "Usage: limen <command> [arguments]\n")
	fmt.Fprintf(os.Stderr, "Commands:\n")
	fmt.Fprintf(os.Stderr, "  run-task   Run a task through the orchestrator\n")
}

func runTaskCmd() {
	runTaskFlags := flag.NewFlagSet("run-task", flag.ExitOnError)
	taskID := runTaskFlags.String("task-id", "", "The ID of the task to run")
	dbPath := runTaskFlags.String("db-path", "limen.db", "Path to the SQLite database")
	repoPath := runTaskFlags.String("repo-path", ".", "Path to the target git repository")

	if err := runTaskFlags.Parse(os.Args[2:]); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing flags: %v\n", err)
		os.Exit(1)
	}

	if *taskID == "" {
		fmt.Fprintf(os.Stderr, "--task-id is required\n")
		runTaskFlags.Usage()
		os.Exit(1)
	}

	store, err := state.NewSQLiteStore(*dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize SQLite store: %v", err)
	}
	defer store.Close()

	manager := git.NewWorktreeManager(*repoPath, "main")

	worktreeRoot, err := filepath.Abs(filepath.Join(*repoPath, ".limen", "worktrees"))
	if err != nil {
		log.Fatalf("Failed to resolve worktree root: %v", err)
	}

	orch := orchestrator.NewOrchestrator(
		store,
		&cliRouter{},
		&cliRetriever{},
		&cliWorker{},
		&cliValidator{},
		&cliGit{manager: manager, repoPath: *repoPath},
		worktreeRoot,
	)

	// Ensure the task exists. This is for convenience during early development.
	// Production may expect the task to be created by another command/API.
	_, err = store.CreateTask(*taskID, 3)
	if err != nil {
		log.Printf("Note: failed to create task %s (it may already exist): %v", *taskID, err)
	}

	log.Printf("Starting task %s", *taskID)
	ctx := context.Background()
	if err := orch.RunTask(ctx, *taskID); err != nil {
		log.Fatalf("Task failed: %v", err)
	}

	t, err := store.GetTask(*taskID)
	if err != nil {
		log.Fatalf("Failed to retrieve completed task: %v", err)
	}
	log.Printf("Task completed with state: %s", t.CurrentState)
}
