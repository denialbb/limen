package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-isatty"

	"github.com/denialbb/limen/internal/bus"
	"github.com/denialbb/limen/internal/git"
	"github.com/denialbb/limen/internal/orchestrator"
	"github.com/denialbb/limen/internal/remote"
	"github.com/denialbb/limen/internal/state"
	"github.com/denialbb/limen/internal/tui"
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
type cliValidator struct {
	cmd    string
	logDir string
}

func (v *cliValidator) Evaluate(ctx context.Context, task *state.Task, wt *git.Worktree, em orchestrator.Emitter) (bool, string, error) {
	log.Printf("Validator evaluating solution for task %s", task.ID)

	passes := true
	feedback := "LGTM"

	if v.cmd != "" {
		cmdParts := strings.Fields(v.cmd)
		c := exec.CommandContext(ctx, cmdParts[0], cmdParts[1:]...)
		c.Dir = wt.Path

		var outBuf strings.Builder
		var logFile *os.File
		var err error

		if v.logDir != "" {
			logPath := filepath.Join(v.logDir, fmt.Sprintf("%s-validator.log", task.ID))
			if err := os.MkdirAll(v.logDir, 0755); err == nil {
				logFile, err = os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
			}
		}

		if logFile != nil {
			defer logFile.Close()
			c.Stdout = io.MultiWriter(&outBuf, logFile)
			c.Stderr = io.MultiWriter(&outBuf, logFile)
		} else {
			c.Stdout = &outBuf
			c.Stderr = &outBuf
		}

		err = c.Run()
		out := outBuf.String()
		if err != nil {
			passes = false
			feedback = fmt.Sprintf("Command %q failed:\n%s\nError: %v", v.cmd, out, err)
		} else {
			passes = true
			feedback = fmt.Sprintf("Command %q passed:\n%s", v.cmd, out)
		}
	}

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
		Passed:    passes,
		Detail:    feedback,
		Timestamp: time.Now(),
	})
	em.Publish(&bus.ValidatorVerdict{
		TaskID:    task.ID,
		Passes:    passes,
		Feedback:  feedback,
		Timestamp: time.Now(),
	})
	return passes, feedback, nil
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
	case "ready-for-review":
		runReadyForReviewCmd()
	case "submit-verdict":
		runSubmitVerdictCmd()
	case "tui":
		// NOTE: Explicit alias for the default bare invocation. Kept so that
		// subcommand-style invocation remains available alongside the simple form.
		runTUICmd()
	default:
		if strings.HasPrefix(command, "-") {
			os.Args = append([]string{os.Args[0], "tui"}, os.Args[1:]...)
			runTUICmd()
			return
		}
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, "Usage: limen [command] [arguments]\n")
	fmt.Fprintf(os.Stderr, "Commands:\n")
	fmt.Fprintf(os.Stderr, "  limen                  Launch the interactive TUI (default)\n")
	fmt.Fprintf(os.Stderr, "  limen tui              Alias for the default interactive TUI\n")
	fmt.Fprintf(os.Stderr, "  limen run-task         Run a task through the orchestrator (one-shot)\n")
	fmt.Fprintf(os.Stderr, "  limen ready-for-review Write a ready signal to the DB and poll for a verdict\n")
	fmt.Fprintf(os.Stderr, "  limen submit-verdict   Record a validation verdict and unblock ready-for-review\n")
}

// runTUICmd launches the interactive terminal UI.
//
// The TUI owns a bus.ChannelBus, subscribes to it, passes the bus to
// NewOrchestrator, and pumps bus.Event values into tea.Msg values for
// rendering. The orchestrator runs in a goroutine; when it finishes the bus is
// closed, the TUI's event pump observes the closed channel, auto-switches to
// the Timeline tab for review, and the user quits with q.
//
// If stdout is not a TTY (piped output, CI runners, non-interactive shells),
// Bubble Tea is skipped and the run-task log-style output path is used
// instead. This keeps the bare invocation safe for scripts and CI.
func runTUICmd() {
	tuiFlags := flag.NewFlagSet("tui", flag.ExitOnError)
	taskID := tuiFlags.String("task-id", "", "The ID of the task to run")
	prompt := tuiFlags.String("prompt", "", "The initial prompt for the task")
	dbPath := tuiFlags.String("db-path", "", "Path to the SQLite database (default: <repo-path>/limen.db)")
	repoPath := tuiFlags.String("repo-path", ".", "Path to the target git repository")
	mockFlag := tuiFlags.Bool("mock", true, "Use Python mock backend for cognitive components")
	mockTranscript := tuiFlags.String("mock-transcript", "src/limen/mock/transcripts/spike.json", "Path to the mock transcript JSON file")
	workerBackend := tuiFlags.String("worker-backend", "pi", "Backend to use for the worker (pi, cli, mock)")
	workerPiProvider := tuiFlags.String("worker-pi-provider", "", "Provider for the pi worker (e.g. mistral, openai)")
	workerPiModel := tuiFlags.String("worker-pi-model", "", "Model for the pi worker (e.g. codestral-latest)")
	validatorCmd := tuiFlags.String("validator-cmd", "", "Command to run for cli validator")

	if err := tuiFlags.Parse(os.Args[2:]); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing flags: %v\n", err)
		os.Exit(1)
	}

	if *taskID == "" {
		fmt.Fprintf(os.Stderr, "--task-id is required\n")
		tuiFlags.Usage()
		os.Exit(1)
	}

	if *prompt == "" {
		fmt.Fprintf(os.Stderr, "--prompt is required\n")
		tuiFlags.Usage()
		os.Exit(1)
	}

	if *dbPath == "" {
		*dbPath = filepath.Join(*repoPath, "limen.db")
	}

	if !isTTY(os.Stdout.Fd()) {
		// NOTE: Non-interactive stdout. Fall back to the one-shot log style so
		// pipes and CI get the same outcome reporting without ANSI pollution.
		runTaskOneShot(*taskID, *prompt, *dbPath, *repoPath, *mockFlag, *mockTranscript, *workerBackend, *workerPiProvider, *workerPiModel, *validatorCmd)
		return
	}

	runTaskInteractive(*taskID, *prompt, *dbPath, *repoPath, *mockFlag, *mockTranscript, *workerBackend, *workerPiProvider, *workerPiModel, *validatorCmd)
}

// isTTY reports whether the given file descriptor is an interactive terminal.
// It explicitly handles the Cygwin terminal case via go-isatty so WSL/MSYS
// pipes that wrap a real terminal still detect as TTYs.
func isTTY(fd uintptr) bool {
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}

// detectDefaultBranch returns the symbolic HEAD branch name of the repo at
// repoPath (typically "main" or "master"). Falls back to "main" on error.
func detectDefaultBranch(repoPath string) string {
	out, err := exec.Command("git", "-C", repoPath, "symbolic-ref", "--short", "HEAD").Output()
	if err != nil {
		return "main"
	}
	if b := strings.TrimSpace(string(out)); b != "" {
		return b
	}
	return "main"
}

// runTaskInteractive runs the orchestrator in a goroutine and renders the
// Bubble Tea program in the foreground. After the program exits, a single
// final-state line is printed so scripts that parse the trailing output still
// get the outcome.
func runTaskInteractive(taskID, prompt, dbPath, repoPath string, mock bool, mockTranscript string, workerBackend, workerPiProvider, workerPiModel string, validatorCmd string) {
	store, err := state.NewSQLiteStore(dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize SQLite store: %v", err)
	}
	defer store.Close()

	_ = registerDBPath(taskID, dbPath)

	manager := git.NewWorktreeManager(repoPath, detectDefaultBranch(repoPath))

	worktreeRoot, err := filepath.Abs(filepath.Join(repoPath, ".limen", "worktrees"))
	if err != nil {
		log.Fatalf("Failed to resolve worktree root: %v", err)
	}

	eventBus := bus.NewChannelBus()
	defer eventBus.Close()

	var (
		router    orchestrator.Router
		retriever orchestrator.Retriever
		worker    orchestrator.Worker
		validator orchestrator.Validator
		gitClient orchestrator.GitClient
	)

	gitClient = manager

	logDir := filepath.Join(repoPath, ".limen", "logs")

	if mock {
		// NOTE: Wire Python mock backend adapters. Each adapter launches a
		// single-shot `python -m limen.mock.<role>` subprocess per call and
		// passes the transcript path as argv[1] so the mock runtime replays
		// canned entries from the transcript file.
		router = remote.NewRouter([]string{"python", "-m", "limen.mock.router", mockTranscript}, remote.WithLogDir(logDir))
		retriever = &cliRetriever{}
		worker = remote.NewWorker([]string{"python", "-m", "limen.mock.worker", mockTranscript}, remote.WithLogDir(logDir))
		validator = remote.NewValidator([]string{"python", "-m", "limen.mock.validator", mockTranscript}, gitClient, remote.WithLogDir(logDir))
	} else {
		router = &cliRouter{}
		retriever = &cliRetriever{}
		if workerBackend == "pi" {
			worker = remote.NewPiWorker(
				remote.WithLogDir(logDir),
				remote.WithPiProvider(workerPiProvider),
				remote.WithPiModel(workerPiModel),
			)
		} else {
			worker = &cliWorker{}
		}
		validator = &cliValidator{cmd: validatorCmd, logDir: logDir}
	}

	orch := orchestrator.NewOrchestrator(
		store,
		eventBus,
		router,
		retriever,
		worker,
		validator,
		gitClient,
		worktreeRoot,
	)

	if _, err := store.CreateTask(taskID, 3, prompt); err != nil {
		log.Printf("Note: failed to create task %s (it may already exist): %v", taskID, err)
	}

	model := tui.NewModel(taskID, eventBus)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// NOTE: Orchestrator runs on a goroutine. When RunTask returns, the bus is
	// closed, the TUI's event pump sees the closed channel, and the user can
	// review the Timeline tab before quitting with q.
	//
	// The WaitGroup + cancellable context ensure that when the user quits
	// early (presses q before RunTask returns), cancel() signals the
	// orchestrator's ctx.Done() checks and wg.Wait() blocks for its deferred
	// cleanup (DestroyWorktree) to run before the process exits. Without this,
	// main returning would kill the goroutine mid-flight and leak the worktree.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := orch.RunTask(ctx, taskID); err != nil {
			eventBus.Publish(&bus.OrchestratorError{
				TaskID:    taskID,
				Error:     err.Error(),
				Timestamp: time.Now(),
			})
		}
		eventBus.Close()
	}()

	// Silence log output while the TUI owns the terminal. log.Printf writes to
	// stderr which bleeds through the alt screen and corrupts the display.
	// Redirect to the log directory if available, otherwise discard.
	prevLogOut := log.Writer()
	tuiLogPath := filepath.Join(logDir, "tui.log")
	if f, ferr := os.OpenFile(tuiLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); ferr == nil {
		log.SetOutput(f)
		defer func() { log.SetOutput(prevLogOut); f.Close() }()
	} else {
		log.SetOutput(io.Discard)
		defer log.SetOutput(prevLogOut)
	}

	program := tea.NewProgram(model, tea.WithAltScreen())
	if _, err = program.Run(); err != nil {
		log.Fatalf("TUI exited with error: %v", err)
	}
	cancel()  // signal the orchestrator to stop on early quit
	wg.Wait() // let it clean up (DestroyWorktree) before exiting
}

// runTaskWithConfig executes the orchestrator in log-style (non-TUI) mode.
// It performs store creation, worktree root resolution, bus wiring, task
// creation, RunTask execution, and final state logging. Both the explicit
// `run-task` subcommand and the non-TTY fallback from `runTUICmd` delegate here
// to avoid duplicating the setup and teardown logic.
func runTaskWithConfig(taskID, prompt, dbPath, repoPath string, mock bool, mockTranscript string, workerBackend, workerPiProvider, workerPiModel string, validatorCmd string) {
	store, err := state.NewSQLiteStore(dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize SQLite store: %v", err)
	}
	defer store.Close()

	_ = registerDBPath(taskID, dbPath)

	manager := git.NewWorktreeManager(repoPath, detectDefaultBranch(repoPath))

	worktreeRoot, err := filepath.Abs(filepath.Join(repoPath, ".limen", "worktrees"))
	if err != nil {
		log.Fatalf("Failed to resolve worktree root: %v", err)
	}

	// NOTE: Log-style mode has no TUI subscriber, so a fresh ChannelBus with no
	// subscribers discards every published event (Publish fans out to an empty
	// subscriber slice and returns immediately, without blocking). The bus is
	// still threaded through the orchestrator so the synthetic events from the
	// CLI stubs are produced on the canonical transport and the interactive
	// path can subscribe without recompiling.
	eventBus := bus.NewChannelBus()
	defer eventBus.Close()

	var (
		router    orchestrator.Router
		retriever orchestrator.Retriever
		worker    orchestrator.Worker
		validator orchestrator.Validator
		gitClient orchestrator.GitClient
	)

	gitClient = manager

	logDir := filepath.Join(repoPath, ".limen", "logs")

	if mock {
		router = remote.NewRouter([]string{"python", "-m", "limen.mock.router", mockTranscript}, remote.WithLogDir(logDir))
		retriever = &cliRetriever{}
		worker = remote.NewWorker([]string{"python", "-m", "limen.mock.worker", mockTranscript}, remote.WithLogDir(logDir))
		validator = remote.NewValidator([]string{"python", "-m", "limen.mock.validator", mockTranscript}, gitClient, remote.WithLogDir(logDir))
	} else {
		router = &cliRouter{}
		retriever = &cliRetriever{}
		if workerBackend == "pi" {
			worker = remote.NewPiWorker(
				remote.WithLogDir(logDir),
				remote.WithPiProvider(workerPiProvider),
				remote.WithPiModel(workerPiModel),
			)
		} else {
			worker = &cliWorker{}
		}
		validator = &cliValidator{cmd: validatorCmd, logDir: logDir}
	}

	orch := orchestrator.NewOrchestrator(
		store,
		eventBus,
		router,
		retriever,
		worker,
		validator,
		gitClient,
		worktreeRoot,
	)

	// Ensure the task exists. This is for convenience during early development.
	// Production may expect the task to be created by another command/API.
	if _, err := store.CreateTask(taskID, 3, prompt); err != nil {
		log.Printf("Note: failed to create task %s (it may already exist): %v", taskID, err)
	}

	log.Printf("Starting task %s", taskID)
	ctx := context.Background()
	if err := orch.RunTask(ctx, taskID); err != nil {
		log.Fatalf("Task failed: %v", err)
	}

	t, err := store.GetTask(taskID)
	if err != nil {
		log.Fatalf("Failed to retrieve completed task: %v", err)
	}
	log.Printf("Task completed with state: %s", t.CurrentState)
}

// runTaskOneShot is the non-TTY fallback. It reuses the run-task log-style
// output and shares the same setup path as the explicit `run-task` subcommand.
func runTaskOneShot(taskID, prompt, dbPath, repoPath string, mock bool, mockTranscript string, workerBackend, workerPiProvider, workerPiModel string, validatorCmd string) {
	runTaskWithConfig(taskID, prompt, dbPath, repoPath, mock, mockTranscript, workerBackend, workerPiProvider, workerPiModel, validatorCmd)
}

func runTaskCmd() {
	runTaskFlags := flag.NewFlagSet("run-task", flag.ExitOnError)
	taskID := runTaskFlags.String("task-id", "", "The ID of the task to run")
	prompt := runTaskFlags.String("prompt", "", "The initial prompt for the task")
	dbPath := runTaskFlags.String("db-path", "", "Path to the SQLite database (default: <repo-path>/limen.db)")
	repoPath := runTaskFlags.String("repo-path", ".", "Path to the target git repository")
	mockFlag := runTaskFlags.Bool("mock", true, "Use Python mock backend for cognitive components")
	mockTranscript := runTaskFlags.String("mock-transcript", "src/limen/mock/transcripts/spike.json", "Path to the mock transcript JSON file")
	workerBackend := runTaskFlags.String("worker-backend", "pi", "Backend to use for the worker (pi, cli, mock)")
	workerPiProvider := runTaskFlags.String("worker-pi-provider", "", "Provider for the pi worker (e.g. mistral, openai)")
	workerPiModel := runTaskFlags.String("worker-pi-model", "", "Model for the pi worker (e.g. codestral-latest)")
	validatorCmd := runTaskFlags.String("validator-cmd", "", "Command to run for cli validator")

	if err := runTaskFlags.Parse(os.Args[2:]); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing flags: %v\n", err)
		os.Exit(1)
	}

	if *taskID == "" {
		fmt.Fprintf(os.Stderr, "--task-id is required\n")
		runTaskFlags.Usage()
		os.Exit(1)
	}

	if *prompt == "" {
		fmt.Fprintf(os.Stderr, "--prompt is required\n")
		runTaskFlags.Usage()
		os.Exit(1)
	}

	if *dbPath == "" {
		*dbPath = filepath.Join(*repoPath, "limen.db")
	}

	runTaskWithConfig(*taskID, *prompt, *dbPath, *repoPath, *mockFlag, *mockTranscript, *workerBackend, *workerPiProvider, *workerPiModel, *validatorCmd)
}

func runReadyForReviewCmd() {
	flags := flag.NewFlagSet("ready-for-review", flag.ExitOnError)
	taskID := flags.String("task-id", "", "The ID of the task")
	dbPath := flags.String("db-path", "", "Path to the SQLite database")
	summary := flags.String("summary", "", "Summary of the changes ready for review")

	if err := flags.Parse(os.Args[2:]); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing flags: %v\n", err)
		os.Exit(1)
	}

	if *taskID == "" || *summary == "" {
		fmt.Fprintf(os.Stderr, "--task-id and --summary are required\n")
		flags.Usage()
		os.Exit(1)
	}

	if *dbPath == "" {
		if path, err := getRegisteredDBPath(*taskID); err == nil && path != "" {
			*dbPath = path
		} else if repoRoot, err := findGitCommonDir(); err == nil {
			*dbPath = filepath.Join(repoRoot, "limen.db")
		} else {
			*dbPath = "limen.db"
		}
	}

	store, err := state.NewSQLiteStore(*dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize SQLite store: %v", err)
	}
	defer store.Close()

	cbID, err := store.WriteCallbackSignal(*taskID, *summary)
	if err != nil {
		log.Fatalf("Failed to write callback signal: %v", err)
	}

	for {
		verdict, completed, err := store.PollCallbackSignal(cbID)
		if err != nil {
			log.Fatalf("Error polling callback: %v", err)
		}
		if completed {
			fmt.Println(verdict)
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func runSubmitVerdictCmd() {
	flags := flag.NewFlagSet("submit-verdict", flag.ExitOnError)
	taskID := flags.String("task-id", "", "The ID of the task")
	dbPath := flags.String("db-path", "", "Path to the SQLite database")
	passes := flags.Bool("passes", false, "Whether the solution passes validation")
	feedback := flags.String("feedback", "", "Validation feedback")

	if err := flags.Parse(os.Args[2:]); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing flags: %v\n", err)
		os.Exit(1)
	}

	if *taskID == "" || *feedback == "" {
		fmt.Fprintf(os.Stderr, "--task-id and --feedback are required\n")
		flags.Usage()
		os.Exit(1)
	}

	if *dbPath == "" {
		if path, err := getRegisteredDBPath(*taskID); err == nil && path != "" {
			*dbPath = path
		} else if repoRoot, err := findGitCommonDir(); err == nil {
			*dbPath = filepath.Join(repoRoot, "limen.db")
		} else {
			*dbPath = "limen.db"
		}
	}

	store, err := state.NewSQLiteStore(*dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize SQLite store: %v", err)
	}
	defer store.Close()

	if err := store.RecordValidationDecision(*taskID, *passes, *feedback); err != nil {
		log.Fatalf("Failed to record validation decision: %v", err)
	}

	cbID, _, found, err := store.GetPendingCallback(*taskID)
	if err != nil {
		log.Fatalf("Error checking for pending callback: %v", err)
	}

	if found {
		verdict := fmt.Sprintf(`{"passes":%t,"feedback":%q}`, *passes, *feedback)
		if err := store.WriteCallbackVerdict(cbID, verdict); err != nil {
			log.Fatalf("Failed to write callback verdict: %v", err)
		}
	} else {
		log.Printf("No pending callback found for task %s", *taskID)
	}
}

func registerDBPath(taskID, dbPath string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, ".limen")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	absDBPath, err := filepath.Abs(dbPath)
	if err != nil {
		absDBPath = dbPath
	}

	registryPath := filepath.Join(dir, "tasks.json")

	var registry map[string]string
	for i := 0; i < 5; i++ {
		registry = make(map[string]string)
		data, err := os.ReadFile(registryPath)
		if err != nil {
			if os.IsNotExist(err) {
				break
			}
			time.Sleep(10 * time.Millisecond)
			continue
		}
		if err := json.Unmarshal(data, &registry); err != nil {
			break
		}
		break
	}

	registry[taskID] = absDBPath

	updatedData, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}

	tmpFile := filepath.Join(dir, fmt.Sprintf("tasks.json.%d.tmp", time.Now().UnixNano()))
	if err := os.WriteFile(tmpFile, updatedData, 0644); err != nil {
		return err
	}
	defer os.Remove(tmpFile)

	return os.Rename(tmpFile, registryPath)
}

func getRegisteredDBPath(taskID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	registryPath := filepath.Join(home, ".limen", "tasks.json")
	data, err := os.ReadFile(registryPath)
	if err != nil {
		return "", err
	}
	var registry map[string]string
	if err := json.Unmarshal(data, &registry); err != nil {
		return "", err
	}
	path, ok := registry[taskID]
	if !ok {
		return "", fmt.Errorf("task ID %s not found in registry", taskID)
	}
	return path, nil
}

func findGitCommonDir() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--git-common-dir")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	gitCommonDir := strings.TrimSpace(string(out))
	if gitCommonDir == "" {
		return "", fmt.Errorf("empty git-common-dir")
	}
	if !filepath.IsAbs(gitCommonDir) {
		absGitCommonDir, err := filepath.Abs(gitCommonDir)
		if err != nil {
			return "", err
		}
		gitCommonDir = absGitCommonDir
	}
	if filepath.Base(gitCommonDir) == ".git" {
		return filepath.Dir(gitCommonDir), nil
	}
	return filepath.Dir(filepath.Dir(gitCommonDir)), nil
}

