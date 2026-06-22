package orchestrator

import (
	"context"
	"errors"

	"github.com/denialbb/limen/internal/state"
)

var (
	ErrGitInvalid          = errors.New("git state is invalid")
	ErrValidationFailed    = errors.New("validation failed")
	ErrUnresolvableEntropy = errors.New("unresolvable entropy during routing")
	ErrNotImplemented      = errors.New("not implemented")
)

// RouterDecision dictates the next step after router evaluation.
type RouterDecision string

const (
	DecisionProceed  RouterDecision = "PROCEED"
	DecisionExpand   RouterDecision = "EXPAND"
	DecisionEscalate RouterDecision = "ESCALATE"
)

// Router evaluates the task and context entropy.
type Router interface {
	Evaluate(ctx context.Context, task *state.Task) (RouterDecision, error)
}

// Worker is responsible for generating candidate solutions in an isolated worktree.
type Worker interface {
	ProduceSolution(ctx context.Context, task *state.Task) error
}

// Validator evaluates the correctness of the candidate solution.
type Validator interface {
	// Evaluate returns a boolean indicating if the solution passed, and feedback if not.
	Evaluate(ctx context.Context, task *state.Task) (bool, string, error)
}

// GitClient handles physical layer operations like validating the repository and committing.
type GitClient interface {
	IsValid(ctx context.Context) (bool, error)
	CommitWorktree(ctx context.Context, taskID string) error
	ResolveConflict(ctx context.Context, taskID string) error
}

// Orchestrator defines the main contract for running the Limen Go Core Loop.
// It is the sole component permitted to advance the task's state.
type Orchestrator interface {
	// RunTask executes the pipeline for a given task, enforcing all invariants.
	// Pipeline steps:
	// 1. Validate Git state
	// 2. Build retrieval context
	// 3. Worker produces candidate solution
	// 4. Validator evaluates correctness
	// 5. Handle validator failure / retry loop
	// 6. Handle Git conflicts
	// 7. Commit via Go Core if successful
	RunTask(ctx context.Context, taskID string) error
}

// StubOrchestrator provides a failing stub implementation for TDD.
type StubOrchestrator struct {
	store     state.Store
	router    Router
	worker    Worker
	validator Validator
	git       GitClient
}

// NewOrchestrator returns a new instance of Orchestrator.
func NewOrchestrator(store state.Store, router Router, worker Worker, validator Validator, git GitClient) Orchestrator {
	return &StubOrchestrator{
		store:     store,
		router:    router,
		worker:    worker,
		validator: validator,
		git:       git,
	}
}

// RunTask executes the pipeline for a given task.
func (o *StubOrchestrator) RunTask(ctx context.Context, taskID string) error {
	task, err := o.store.GetTask(taskID)
	if err != nil {
		return err
	}

	valid, err := o.git.IsValid(ctx)
	if err != nil {
		return err
	}
	if !valid {
		return ErrGitInvalid
	}

	if err := o.store.TransitionState(task.ID, state.StateRoutingEvaluation); err != nil {
		return err
	}

	decision, err := o.router.Evaluate(ctx, task)
	if err != nil {
		return err
	}

	switch decision {
	case DecisionEscalate:
		_ = o.store.TransitionState(task.ID, state.StateFailedEscalated)
		return ErrUnresolvableEntropy
	case DecisionExpand:
		return ErrNotImplemented
	case DecisionProceed:
		// continue
	default:
		return errors.New("unknown routing decision")
	}

	for {
		if err := o.store.TransitionState(task.ID, state.StateWorkerRunning); err != nil {
			return err
		}

		if err := o.worker.ProduceSolution(ctx, task); err != nil {
			return err
		}

		if err := o.store.TransitionState(task.ID, state.StateAwaitingValidation); err != nil {
			return err
		}

		passes, _, err := o.validator.Evaluate(ctx, task)
		if err != nil {
			return err
		}

		if passes {
			if err := o.store.TransitionState(task.ID, state.StateApproved); err != nil {
				return err
			}
			if err := o.git.CommitWorktree(ctx, task.ID); err != nil {
				return err
			}
			return o.store.TransitionState(task.ID, state.StateCommitted)
		}

		if err := o.store.IncrementRetry(task.ID); err != nil {
			if errors.Is(err, state.ErrMaxRetriesReached) {
				_ = o.store.TransitionState(task.ID, state.StateFailedEscalated)
				return ErrValidationFailed
			}
			return err
		}

		if err := o.store.TransitionState(task.ID, state.StateRevisionRequested); err != nil {
			return err
		}
	}
}
