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
	"time"

	"github.com/denialbb/limen/internal/bus"
	"github.com/denialbb/limen/internal/git"
	"github.com/denialbb/limen/internal/orchestrator"
	"github.com/denialbb/limen/internal/state"
)

// cliRouter is a placeholder router that always proceeds.
// TODO: Replace with the real routing heuristic or LLM evaluator.
type cliRouter struct{}

func (c *cliRouter) Evaluate(ctx context.Context, task *state.Task, em orchestrator.Emitter) (orchestrator.RouterDecision, error) {
	// NOTE: Synthetic event stream per the v1 de-risking plan: emit the full
	// Router taxonomy so the TUI has something to render before the real
	// Python L1 client exists.
	em.Publish(&bus.RouterExamining{
		TaskID:         task.ID,
		ContextExcerpt: "(placeholder context)",
		Entropy:        0.0,
		Timestamp:      time.Now(),
	})
	em.Publish(&bus.RouterDecisionEvent{
		TaskID:      task.ID,
		Decision:    bus.DecisionProceed,
		Rationale:   "placeholder router always proceeds",
		ExpandCount: 0,
		Timestamp:   time.Now(),
	})
	return orchestrator.DecisionProceed, nil
}

// cliRetriever is a placeholder retriever that returns an empty context manifest.
// TODO: Replace with the real progressive retrieval pipeline (BM25 + semantic).
type cliRetriever struct{}

func (c *cliRetriever) Retrieve(ctx context.Context, task *state.Task, em orchestrator.Emitter) (string, error) {
	// NOTE: Snapshot size is 0 because the placeholder retriever emits no
	// manifest yet; the real pipeline will populate this from the assembled
	// retrieval context.
	em.Publish(&bus.ContextBuilt{
		TaskID:       task.ID,
		SnapshotSize: 0,
		ManifestRef:  "",
		Timestamp:    time.Now(),
	})
	return "", nil
}

// cliWorker is a placeholder worker that logs and does nothing.
// TODO: Replace with the real LLM-backed worker.
type cliWorker struct{}

// ProduceSolution implements the worker interface.
func (c *cliWorker) ProduceSolution(ctx context.Context, task *state.Task, wt *git.Worktree, feedback string, em orchestrator.Emitter) error {
	log.Printf("Worker producing solution for task %s", task.ID)

	// NOTE: Synthetic Worker taxonomy stream so the TUI shows realistic
	// activity while the Python L2 client is still a TODO stub.
	em.Publish(&bus.WorkerStarted{
		TaskID:       task.ID,
		WorktreePath: wt.Path,
		BaseCommit:   wt.BaseCommit,
		Retry:        task.RetryCount,
		Timestamp:    time.Now(),
	})
	em.Publish(&bus.WorkerToolCall{
		TaskID:    task.ID,
		Tool:      "write_file",
		Args:      "dummy_solution.txt",
		Timestamp: time.Now(),
	})

	dummyPath := filepath.Join(wt.Path, "dummy_solution.txt")
	if err := os.WriteFile(dummyPath, []byte("Hello from cliWorker"), 0644); err != nil {
		return err
	}

	em.Publish(&bus.WorkerFileEdit{
		TaskID:    task.ID,
		Path:      "dummy_solution.txt",
		Op:        "create",
		DiffHunk:  "Hello from cliWorker",
		Timestamp: time.Now(),
	})
	em.Publish(&bus.WorkerFinished{
		TaskID:    task.ID,
		Timestamp: time.Now(),
	})
	return nil
}

// cliValidator is a placeholder validator that always passes.
// TODO: Replace with the real L3 validator.
type cliValidator struct{}

func (v *cliValidator) Evaluate(ctx context.Context, task *state.Task, wt *git.Worktree, em orchestrator.Emitter) (bool, string, error) {
	log.Printf("Validator evaluating solution for task %s", task.ID)

	criteria := []string{"placeholder_criterion"}
	// NOTE: Synthetic Validator taxonomy stream; the per-criterion result and
	// overall verdict are emitted to give the TUI something concrete to render.
	em.Publish(&bus.ValidatorExamining{
		TaskID:    task.ID,
		Criteria:  criteria,
		Timestamp: time.Now(),
	})
	em.Publish(&bus.ValidatorCriterionResult{
		TaskID:    task.ID,
		Criterion: "placeholder_criterion",
		Passed:    true,
		Detail:    "placeholder validator always passes",
		Timestamp: time.Now(),
	})
	em.Publish(&bus.ValidatorVerdict{
		TaskID:    task.ID,
		Passes:    true,
		Feedback:  "LGTM",
		Timestamp: time.Now(),
	})
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
	// NOTE: The bare invocation `limen` is the primary human-facing entry point
	// and launches the interactive TUI by default. See .agents/docs/interactive_tui.md.
	if len(os.Args) < 2 {
		runTUICmd()
		return
	}

	command := os.Args[1]

	switch command {
	case "run-task":
		runTaskCmd()
	case "tui":
		// NOTE: Explicit alias for the default bare invocation. Kept so that
		// subcommand-style invocation remains available alongside the simple form.
		runTUICmd()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, "Usage: limen [command] [arguments]\n")
	fmt.Fprintf(os.Stderr, "Commands:\n")
	fmt.Fprintf(os.Stderr, "  limen            Launch the interactive TUI (default)\n")
	fmt.Fprintf(os.Stderr, "  limen tui        Alias for the default interactive TUI\n")
	fmt.Fprintf(os.Stderr, "  limen run-task   Run a task through the orchestrator (one-shot)\n")
}

// runTUICmd launches the interactive terminal UI.
// TODO: Implement the Bubble Tea program per .agents/docs/interactive_tui.md.
//       The TUI will own a bus.ChannelBus, subscribe to it, pass the bus to
//       NewOrchestrator, and pump events into tea.Msg values for rendering.
//       For now this is a placeholder so the entry-point contract is in place.
func runTUICmd() {
	fmt.Fprintln(os.Stderr, "limen: interactive TUI is not yet implemented")
	fmt.Fprintln(os.Stderr, "See .agents/docs/interactive_tui.md for the design.")
	fmt.Fprintln(os.Stderr, "Use `limen run-task --task-id <id>` for the one-shot path.")
	os.Exit(1)
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

	// NOTE: run-task is one-shot scripting mode. There is no TUI subscriber, so
	// a fresh ChannelBus with no subscribers discards every published event
	// (Publish fans out to an empty subscriber slice and returns immediately,
	// without blocking). The bus is still threaded through the orchestrator
	// so the synthetic events from the CLI stubs are produced on the canonical
	// transport and a future TTY-aware mode can subscribe without recompiling.
	// TODO: For the bare `limen` invocation, runTUICmd will wire a real
	// subscriber and tea.Program per .agents/docs/interactive_tui.md.
	eventBus := bus.NewChannelBus()
	defer eventBus.Close()

	orch := orchestrator.NewOrchestrator(
		store,
		eventBus,
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
