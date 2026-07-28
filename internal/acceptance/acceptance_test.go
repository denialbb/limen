package acceptance

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/denialbb/limen/internal/orchestrator"
	"github.com/denialbb/limen/internal/state"
)

// TestTaskLifecycle executes features/task_lifecycle.feature against the real
// orchestrator. The feature file is the behavior contract; this file is the
// binding from its prose to the engine.
func TestTaskLifecycle(t *testing.T) {
	path, err := FeaturePath("task_lifecycle.feature")
	if err != nil {
		t.Fatalf("locate feature file: %v", err)
	}
	feature, err := ParseFeatureFile(path)
	if err != nil {
		t.Fatalf("parse feature file: %v", err)
	}

	var w *world
	suite := NewSuite()
	suite.BeforeScenario(func() { w = newWorld(t) })
	registerSteps(suite, func() *world { return w })
	suite.Run(t, feature)
}

// registerSteps binds every step in the feature file to the orchestrator.
// The world is reached through a getter because BeforeScenario replaces it
// before each scenario, and a captured pointer would go stale.
func registerSteps(s *Suite, get func() *world) {
	// --- Background -------------------------------------------------------

	// The orchestrator is wired fresh per scenario by BeforeScenario; this step
	// exists so the contract states the precondition explicitly.
	s.Step(`the orchestrator is initialized`, func(args []string) error {
		if get() == nil {
			return fmt.Errorf("world was not initialized")
		}
		return nil
	})

	s.Step(`the retrieval pipeline returns an empty manifest`, func(args []string) error {
		get().retriever.manifests = []string{""}
		return nil
	})

	// --- Given: scenario preconditions -----------------------------------

	// Zero coverage is the router's escalation trigger: with nothing retrieved
	// there is no context to resolve, so routing escalates rather than proceed.
	s.Step(`coverage hint is 0`, func(args []string) error {
		w := get()
		w.retriever.manifests = []string{""}
		w.router.decisions = []orchestrator.RouterDecision{orchestrator.DecisionEscalate}
		return nil
	})

	s.Step(`the router will decide (PROCEED|EXPAND|ESCALATE)`, func(args []string) error {
		get().router.decisions = []orchestrator.RouterDecision{decisionOf(args[0])}
		return nil
	})

	s.Step(`the router will expand (\d+) times before proceeding`, func(args []string) error {
		n, err := atoi(args[0])
		if err != nil {
			return err
		}
		decisions := make([]orchestrator.RouterDecision, 0, n+1)
		for range n {
			decisions = append(decisions, orchestrator.DecisionExpand)
		}
		decisions = append(decisions, orchestrator.DecisionProceed)
		get().router.decisions = decisions
		return nil
	})

	s.Step(`each retrieval round returns richer context`, func(args []string) error {
		get().retriever.manifests = []string{
			"",
			"chunk-a",
			"chunk-a chunk-b",
			"chunk-a chunk-b chunk-c",
			"chunk-a chunk-b chunk-c chunk-d",
			"chunk-a chunk-b chunk-c chunk-d chunk-e",
		}
		return nil
	})

	s.Step(`the validator will reject the solution (\d+) times then approve`, func(args []string) error {
		n, err := atoi(args[0])
		if err != nil {
			return err
		}
		verdicts := make([]bool, 0, n+1)
		for range n {
			verdicts = append(verdicts, false)
		}
		verdicts = append(verdicts, true)
		get().validator.verdicts = verdicts
		return nil
	})

	s.Step(`the validator will always reject the solution`, func(args []string) error {
		get().validator.verdicts = []bool{false}
		return nil
	})

	s.Step(`the task allows (\d+) retries`, func(args []string) error {
		n, err := atoi(args[0])
		if err != nil {
			return err
		}
		get().maxRetries = n
		return nil
	})

	// --- When: the action under test --------------------------------------

	s.Step(`a task is created`, func(args []string) error {
		return get().run()
	})

	// --- Then: assertions --------------------------------------------------

	// The ordered-subsequence assertion: each "it reaches X" advances a cursor
	// through the recorded transition history, so the steps together assert the
	// order the engine visited those states in.
	s.Step(`it reaches ([A-Z_]+)`, func(args []string) error {
		return get().reaches(state.TaskState(args[0]))
	})

	s.Step(`it returns to ([A-Z_]+)`, func(args []string) error {
		return get().reaches(state.TaskState(args[0]))
	})

	s.Step(`the task ends in ([A-Z_]+)`, func(args []string) error {
		got, err := get().currentState()
		if err != nil {
			return err
		}
		if want := state.TaskState(args[0]); got != want {
			return fmt.Errorf("terminal state is %s, want %s (transitions: %s)",
				got, want, formatStates(get().recorder.transitions()))
		}
		return nil
	})

	s.Step(`the router decided (PROCEED|EXPAND|ESCALATE)`, func(args []string) error {
		w := get()
		if err := w.requireRun(); err != nil {
			return err
		}
		if w.router.calls == 0 {
			return fmt.Errorf("the router was never consulted")
		}
		// The router is scripted, so this asserts the scenario's premise held:
		// the decision the contract names is the one the engine acted on.
		want := decisionOf(args[0])
		last := w.router.decisions[min(w.router.calls-1, len(w.router.decisions)-1)]
		if last != want {
			return fmt.Errorf("router's final decision was %s, want %s", last, want)
		}
		return nil
	})

	s.Step(`the router was consulted (\d+) times`, func(args []string) error {
		want, err := atoi(args[0])
		if err != nil {
			return err
		}
		w := get()
		if err := w.requireRun(); err != nil {
			return err
		}
		if w.router.calls != want {
			return fmt.Errorf("router was consulted %d times, want %d", w.router.calls, want)
		}
		return nil
	})

	s.Step(`the context was rebuilt (\d+) times`, func(args []string) error {
		want, err := atoi(args[0])
		if err != nil {
			return err
		}
		w := get()
		if err := w.requireRun(); err != nil {
			return err
		}
		got := 0
		for _, st := range w.recorder.transitions() {
			if st == state.StateContextBuilding {
				got++
			}
		}
		if got != want {
			return fmt.Errorf("context was built %d times, want %d (transitions: %s)",
				got, want, formatStates(w.recorder.transitions()))
		}
		return nil
	})

	s.Step(`the worker produced a solution`, func(args []string) error {
		w := get()
		if err := w.requireRun(); err != nil {
			return err
		}
		if w.worker.calls == 0 {
			return fmt.Errorf("the worker was never asked to produce a solution")
		}
		return nil
	})

	s.Step(`the worker ran (\d+) times`, func(args []string) error {
		want, err := atoi(args[0])
		if err != nil {
			return err
		}
		w := get()
		if err := w.requireRun(); err != nil {
			return err
		}
		if w.worker.calls != want {
			return fmt.Errorf("worker ran %d times, want %d", w.worker.calls, want)
		}
		return nil
	})

	s.Step(`the worker received the validator's feedback on its retry`, func(args []string) error {
		w := get()
		if err := w.requireRun(); err != nil {
			return err
		}
		if len(w.worker.feedback) < 2 {
			return fmt.Errorf("the worker ran %d times, so it never got a retry", len(w.worker.feedback))
		}
		if w.worker.feedback[0] != "" {
			return fmt.Errorf("first worker run carried feedback %q, want none", w.worker.feedback[0])
		}
		if w.worker.feedback[1] == "" {
			return fmt.Errorf("retry ran with no feedback; the validator's reason was not passed on")
		}
		return nil
	})

	s.Step(`the validator approved the solution`, func(args []string) error {
		w := get()
		if err := w.requireRun(); err != nil {
			return err
		}
		if w.validator.calls == 0 {
			return fmt.Errorf("the validator was never consulted")
		}
		pass, err := recordedDecisionPasses(w)
		if err != nil {
			return err
		}
		if !pass {
			return fmt.Errorf("the persisted validation decision is a rejection, want an approval")
		}
		return nil
	})

	s.Step(`the validator rejected the solution`, func(args []string) error {
		w := get()
		if err := w.requireRun(); err != nil {
			return err
		}
		if w.validator.calls == 0 {
			return fmt.Errorf("the validator was never consulted")
		}
		return nil
	})

	s.Step(`the retry budget is exhausted`, func(args []string) error {
		w := get()
		if err := w.requireRun(); err != nil {
			return err
		}
		task, err := w.store.GetTask(w.taskID)
		if err != nil {
			return err
		}
		if task.RetryCount < task.MaxRetries {
			return fmt.Errorf("retry count %d has not reached the max of %d", task.RetryCount, task.MaxRetries)
		}
		return nil
	})

	s.Step(`the canonical branch advanced`, func(args []string) error {
		w := get()
		if err := w.requireRun(); err != nil {
			return err
		}
		if got := headCommit(w.t, w.repoDir); got == w.baseCommit {
			return fmt.Errorf("canonical branch is still at %s; nothing was merged", got[:8])
		}
		return nil
	})

	s.Step(`the canonical branch did not advance`, func(args []string) error {
		w := get()
		if err := w.requireRun(); err != nil {
			return err
		}
		if got := headCommit(w.t, w.repoDir); got != w.baseCommit {
			return fmt.Errorf("canonical branch moved to %s; a failed task must not merge", got[:8])
		}
		return nil
	})

	s.Step(`no final output was recorded`, func(args []string) error {
		w := get()
		if err := w.requireRun(); err != nil {
			return err
		}
		task, err := w.store.GetTask(w.taskID)
		if err != nil {
			return err
		}
		if task.FinalOutput != "" {
			return fmt.Errorf("a failed task recorded final output %q", task.FinalOutput)
		}
		return nil
	})
}

// recordedDecisionPasses reads the validation decision the store persisted for
// the task. The store keeps it as a JSON document ({"pass":...,"feedback":...}),
// so the assertion goes through the same shape rather than a bare label.
func recordedDecisionPasses(w *world) (bool, error) {
	task, err := w.store.GetTask(w.taskID)
	if err != nil {
		return false, err
	}
	if task.ValidationDecision == "" {
		return false, fmt.Errorf("no validation decision was persisted for the task")
	}
	var decision struct {
		Pass     bool   `json:"pass"`
		Feedback string `json:"feedback"`
	}
	if err := json.Unmarshal([]byte(task.ValidationDecision), &decision); err != nil {
		return false, fmt.Errorf("persisted validation decision %q is not the expected JSON: %w",
			task.ValidationDecision, err)
	}
	return decision.Pass, nil
}

// decisionOf maps the feature file's decision words onto the orchestrator's
// RouterDecision values.
func decisionOf(word string) orchestrator.RouterDecision {
	switch word {
	case "PROCEED":
		return orchestrator.DecisionProceed
	case "EXPAND":
		return orchestrator.DecisionExpand
	default:
		return orchestrator.DecisionEscalate
	}
}

// atoi parses a step's numeric capture group.
func atoi(s string) (int, error) {
	n := 0
	if s == "" {
		return 0, fmt.Errorf("empty number in step")
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("%q is not a number", s)
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}
