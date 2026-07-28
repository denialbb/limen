package acceptance

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/denialbb/limen/internal/bus"
	"github.com/denialbb/limen/internal/git"
	"github.com/denialbb/limen/internal/orchestrator"
	"github.com/denialbb/limen/internal/retrieval"
	"github.com/denialbb/limen/internal/state"
)

// world is the mutable state one scenario operates on. Every scenario gets a
// fresh world from newWorld, so scenarios cannot leak into each other.
//
// The cognition layer (router, retriever, worker, validator) is mocked exactly
// as the existing integration tests mock it; git and the state store are real,
// because the states under assertion are produced by the real orchestrator
// driving a real SQLite store.
type world struct {
	t *testing.T

	store    *state.SQLiteStore
	repoDir  string
	manager  git.WorktreeManager
	recorder *recordingBus

	router    *scriptedRouter
	retriever *scriptedRetriever
	worker    *recordingWorker
	validator *scriptedValidator

	taskID     string
	maxRetries int

	// baseCommit is the canonical branch head before the run, so a scenario can
	// assert the branch advanced.
	baseCommit string

	// runErr is whatever RunTask returned. Scenarios assert terminal state
	// rather than the error, but escalation paths legitimately return one and
	// the world keeps it for the steps that care.
	runErr error
	ran    bool

	// cursor is how far through the recorded transition sequence the assertions
	// have advanced. It is what turns a list of "it reaches X" steps into an
	// ordering assertion.
	cursor int
}

// recordingBus is a bus.EventBus that records every published event
// synchronously. Using it instead of a ChannelBus keeps scenarios deterministic:
// there is no subscriber goroutine to drain and no channel buffering to race on.
type recordingBus struct {
	mu     sync.Mutex
	events []bus.Event
	closed bool
}

func newRecordingBus() *recordingBus { return &recordingBus{} }

// Publish implements bus.EventBus.
func (r *recordingBus) Publish(ev bus.Event) {
	if ev == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.events = append(r.events, ev)
}

// Subscribe implements bus.EventBus. Acceptance scenarios read the recorded
// slice directly, so the returned channel is closed immediately rather than
// left open for a consumer that never arrives.
func (r *recordingBus) Subscribe() <-chan bus.Event {
	ch := make(chan bus.Event)
	close(ch)
	return ch
}

// Close implements bus.EventBus.
func (r *recordingBus) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
}

// transitions returns the ordered list of states the task moved through.
func (r *recordingBus) transitions() []state.TaskState {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []state.TaskState
	for _, ev := range r.events {
		if sc, ok := ev.(*bus.TaskStateChanged); ok {
			out = append(out, sc.To)
		}
	}
	return out
}

var _ bus.EventBus = (*recordingBus)(nil)

// scriptedRouter returns a pre-set decision sequence. The last entry repeats,
// so a scenario that only cares about the first decision need not script every
// expand iteration.
type scriptedRouter struct {
	decisions []orchestrator.RouterDecision
	calls     int
}

func (r *scriptedRouter) Evaluate(ctx context.Context, task *state.Task, em orchestrator.Emitter) (orchestrator.RouterDecision, error) {
	d := r.decisions[min(r.calls, len(r.decisions)-1)]
	r.calls++
	return d, nil
}

// scriptedRetriever returns a manifest per call, mirroring progressive
// retrieval: successive expand iterations may return progressively richer
// context. The last entry repeats.
type scriptedRetriever struct {
	manifests []string
	calls     int
}

func (r *scriptedRetriever) Retrieve(ctx context.Context, task *state.Task, es retrieval.ExpandState, em orchestrator.Emitter) (string, error) {
	m := r.manifests[min(r.calls, len(r.manifests)-1)]
	r.calls++
	return m, nil
}

// recordingWorker writes a file into the worktree (the "No Git Noise" contract:
// edit, never commit) and counts how many times it was asked to produce.
type recordingWorker struct {
	calls    int
	feedback []string
}

func (w *recordingWorker) ProduceSolution(ctx context.Context, task *state.Task, wt *git.Worktree, feedback string, em orchestrator.Emitter) error {
	w.calls++
	w.feedback = append(w.feedback, feedback)
	name := fmt.Sprintf("solution-%d.txt", w.calls)
	return os.WriteFile(filepath.Join(wt.Path, name), []byte("solution\n"), 0644)
}

// scriptedValidator returns a pre-set verdict sequence; the last entry repeats.
type scriptedValidator struct {
	verdicts []bool
	calls    int
}

func (v *scriptedValidator) Evaluate(ctx context.Context, task *state.Task, wt *git.Worktree, em orchestrator.Emitter) (bool, string, error) {
	passes := v.verdicts[min(v.calls, len(v.verdicts)-1)]
	v.calls++
	if passes {
		return true, "", nil
	}
	return false, fmt.Sprintf("revision %d: tests do not pass", v.calls), nil
}

// acceptanceGit adapts the real WorktreeManager and reports the repository as
// valid, matching the integration tests' dummyGitClient.
type acceptanceGit struct {
	manager git.WorktreeManager
}

func (g *acceptanceGit) IsValid(ctx context.Context) (bool, error) { return true, nil }

func (g *acceptanceGit) ProvisionWorktree(ctx context.Context, baseCommit, branchName, path string) (*git.Worktree, error) {
	return g.manager.ProvisionWorktree(ctx, baseCommit, branchName, path)
}

func (g *acceptanceGit) ProvisionThrowawayWorktree(ctx context.Context, patch string) (*git.Worktree, error) {
	return g.manager.ProvisionThrowawayWorktree(ctx, patch)
}

func (g *acceptanceGit) CommitWorktree(ctx context.Context, taskID string, wt *git.Worktree) error {
	return g.manager.CommitWorktree(ctx, taskID, wt)
}

func (g *acceptanceGit) CheckForConflicts(ctx context.Context, wt *git.Worktree) (bool, error) {
	return g.manager.CheckForConflicts(ctx, wt)
}

func (g *acceptanceGit) ExtractConflictRegions(ctx context.Context, wt *git.Worktree) ([]git.ConflictRegion, error) {
	return g.manager.ExtractConflictRegions(ctx, wt)
}

func (g *acceptanceGit) DestroyWorktree(ctx context.Context, wt *git.Worktree) error {
	return g.manager.DestroyWorktree(ctx, wt)
}

func (g *acceptanceGit) GetWorktreeDiff(ctx context.Context, wt *git.Worktree) (string, error) {
	return g.manager.GetWorktreeDiff(ctx, wt)
}

// newWorld builds a scenario's world with defaults describing the happy path:
// the router proceeds, the worker succeeds, the validator approves. Scenarios
// override those defaults through their Given steps before the task runs.
func newWorld(t *testing.T) *world {
	t.Helper()

	store, err := state.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("open in-memory store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	repoDir := initRepo(t)

	return &world{
		t:          t,
		store:      store,
		repoDir:    repoDir,
		manager:    git.NewWorktreeManager(repoDir, "main"),
		recorder:   newRecordingBus(),
		router:     &scriptedRouter{decisions: []orchestrator.RouterDecision{orchestrator.DecisionProceed}},
		retriever:  &scriptedRetriever{manifests: []string{""}},
		worker:     &recordingWorker{},
		validator:  &scriptedValidator{verdicts: []bool{true}},
		baseCommit: headCommit(t, repoDir),
		maxRetries: 3,
	}
}

// run creates the task and drives the orchestrator to completion. The whole run
// is synchronous, so by the time it returns the recorded transition sequence is
// final and every assertion below is deterministic.
func (w *world) run() error {
	if w.ran {
		return fmt.Errorf("the task has already been run in this scenario")
	}
	w.ran = true
	w.taskID = "acceptance-task"

	if _, err := w.store.CreateTask(w.taskID, w.maxRetries, "acceptance prompt"); err != nil {
		return fmt.Errorf("create task: %w", err)
	}

	worktreeRoot := w.t.TempDir()
	orch := orchestrator.NewOrchestrator(
		w.store, w.store, w.recorder,
		w.router, w.retriever, w.worker, w.validator,
		&acceptanceGit{manager: w.manager}, worktreeRoot,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	w.runErr = orch.RunTask(ctx, w.taskID)
	return nil
}

// requireRun guards steps that only make sense after the task has run.
func (w *world) requireRun() error {
	if !w.ran {
		return fmt.Errorf(`no task has been run yet; the scenario needs a "When a task is created" step first`)
	}
	return nil
}

// reaches asserts the task entered want at or after the assertion cursor, then
// advances the cursor past it. Consecutive "it reaches X" steps therefore
// assert an ordered subsequence of the real transition history, not mere
// membership.
func (w *world) reaches(want state.TaskState) error {
	if err := w.requireRun(); err != nil {
		return err
	}
	seen := w.recorder.transitions()
	for i := w.cursor; i < len(seen); i++ {
		if seen[i] == want {
			w.cursor = i + 1
			return nil
		}
	}
	return fmt.Errorf("task never reached %s after position %d; transitions were %s (run error: %v)",
		want, w.cursor, formatStates(seen), w.runErr)
}

// currentState reads the authoritative terminal state from the store rather
// than from the event stream, so the assertion is against persisted truth.
func (w *world) currentState() (state.TaskState, error) {
	if err := w.requireRun(); err != nil {
		return "", err
	}
	task, err := w.store.GetTask(w.taskID)
	if err != nil {
		return "", fmt.Errorf("read task: %w", err)
	}
	return task.CurrentState, nil
}

// formatStates renders a transition list for failure messages.
func formatStates(states []state.TaskState) string {
	parts := make([]string, len(states))
	for i, s := range states {
		parts[i] = string(s)
	}
	return "[" + strings.Join(parts, " -> ") + "]"
}

// initRepo creates a throwaway git repository with one commit, matching the
// integration tests' fixture.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	runGit("init", "-b", "main")
	runGit("config", "user.name", "Acceptance Runner")
	runGit("config", "user.email", "acceptance@example.com")
	runGit("commit", "--allow-empty", "-m", "init")
	return dir
}

// headCommit returns the current HEAD sha of the repository.
func headCommit(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	return strings.TrimSpace(string(out))
}
