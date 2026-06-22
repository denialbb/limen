package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"

	"github.com/denialbb/limen/internal/git"
	"github.com/denialbb/limen/internal/state"
)

var (
	ErrGitInvalid          = errors.New("git state is invalid")
	ErrValidationFailed    = errors.New("validation failed")
	ErrUnresolvableEntropy = errors.New("unresolvable entropy during routing")
	ErrNotImplemented      = errors.New("not implemented")
	ErrInvalidTaskID       = errors.New("task ID contains unsafe characters")
)

// RouterDecision dictates the next step after router evaluation.
type RouterDecision string

const (
	DecisionProceed  RouterDecision = "PROCEED"
	DecisionExpand   RouterDecision = "EXPAND"
	DecisionEscalate RouterDecision = "ESCALATE"
)

// maxExpandIterations bounds the number of times the router may loop back to
// CONTEXT_BUILDING before the orchestrator escalates. This enforces Invariant #3
// ("No Infinite Loops") for the expand path.
const maxExpandIterations = 5

var taskIDSafeRegex = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// Router evaluates the task and context entropy.
type Router interface {
	Evaluate(ctx context.Context, task *state.Task) (RouterDecision, error)
}

// Retriever builds the ephemeral retrieval context for a task.
type Retriever interface {
	Retrieve(ctx context.Context, task *state.Task) (string, error)
}

// Worker is responsible for generating candidate solutions in an isolated worktree.
type Worker interface {
	ProduceSolution(ctx context.Context, task *state.Task, wt *git.Worktree, feedback string) error
}

// Validator evaluates the correctness of the candidate solution.
type Validator interface {
	// Evaluate returns a boolean indicating if the solution passed, and feedback if not.
	Evaluate(ctx context.Context, task *state.Task, wt *git.Worktree) (bool, string, error)
}

// GitClient handles physical layer operations like validating the repository, committing,
// and managing worktrees. It is designed to wrap the git.WorktreeManager capabilities.
type GitClient interface {
	// IsValid checks that the repository is in a valid state for operations.
	IsValid(ctx context.Context) (bool, error)
	// ProvisionWorktree creates an isolated worktree for the task.
	ProvisionWorktree(ctx context.Context, baseCommit, branchName, path string) (*git.Worktree, error)
	// CommitWorktree commits the worktree for the given task.
	CommitWorktree(ctx context.Context, taskID string, wt *git.Worktree) error
	// CheckForConflicts detects if a merge/rebase conflict exists in the worktree.
	CheckForConflicts(ctx context.Context, wt *git.Worktree) (bool, error)
	// ExtractConflictRegions extracts conflicting diff regions if a conflict is detected.
	ExtractConflictRegions(ctx context.Context, wt *git.Worktree) ([]git.ConflictRegion, error)
	// DestroyWorktree removes the ephemeral worktree and prunes it from Git.
	DestroyWorktree(ctx context.Context, wt *git.Worktree) error
	// GetWorktreeDiff returns the worker's uncommitted changes relative to HEAD.
	GetWorktreeDiff(ctx context.Context, wt *git.Worktree) (string, error)
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

// OrchestratorImpl provides a concrete implementation of Orchestrator.
type OrchestratorImpl struct {
	store       state.Store
	router      Router
	retriever   Retriever
	worker      Worker
	validator   Validator
	git         GitClient
	worktreeRoot string
}

// NewOrchestrator returns a new instance of Orchestrator.
// worktreeRoot must be an absolute path; it is the parent directory for all
// ephemeral task worktrees.
func NewOrchestrator(store state.Store, router Router, retriever Retriever, worker Worker, validator Validator, git GitClient, worktreeRoot string) Orchestrator {
	return &OrchestratorImpl{
		store:        store,
		router:       router,
		retriever:    retriever,
		worker:       worker,
		validator:    validator,
		git:          git,
		worktreeRoot: worktreeRoot,
	}
}

func (o *OrchestratorImpl) recordToolCall(taskID, tool string) {
	// NOTE: Best-effort recording. Failure to persist observability data should
	// not abort the workflow.
	_ = o.store.RecordToolCall(taskID, tool)
}

// RunTask executes the pipeline for a given task.
func (o *OrchestratorImpl) RunTask(ctx context.Context, taskID string) error {
	if !taskIDSafeRegex.MatchString(taskID) {
		return ErrInvalidTaskID
	}

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

	expandCount := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := o.store.TransitionState(task.ID, state.StateContextBuilding); err != nil {
			return err
		}

		contextSnapshot, err := o.retriever.Retrieve(ctx, task)
		if err != nil {
			return err
		}

		if err := o.store.RecordContextSnapshot(task.ID, contextSnapshot); err != nil {
			return err
		}

		// Refresh the task so the router sees the recorded context snapshot.
		task, err = o.store.GetTask(task.ID)
		if err != nil {
			return err
		}

		if err := o.store.TransitionState(task.ID, state.StateRoutingEvaluation); err != nil {
			return err
		}

		o.recordToolCall(task.ID, "router.Evaluate")
		decision, err := o.router.Evaluate(ctx, task)
		if err != nil {
			return err
		}

		switch decision {
		case DecisionEscalate:
			if err := o.store.TransitionState(task.ID, state.StateFailedEscalated); err != nil {
				return err
			}
			return ErrUnresolvableEntropy
		case DecisionExpand:
			expandCount++
			if expandCount > maxExpandIterations {
				if err := o.store.TransitionState(task.ID, state.StateFailedEscalated); err != nil {
					return err
				}
				return ErrUnresolvableEntropy
			}
			continue
		case DecisionProceed:
			// break the routing loop
		default:
			return errors.New("unknown routing decision")
		}
		break
	}

	if !filepath.IsAbs(o.worktreeRoot) {
		return errors.New("worktree root must be an absolute path")
	}

	wtPath := filepath.Join(o.worktreeRoot, "task-"+task.ID)
	wt, err := o.git.ProvisionWorktree(ctx, "HEAD", "task-"+task.ID, wtPath)
	if err != nil {
		return err
	}
	defer func() {
		if err := o.git.DestroyWorktree(context.WithoutCancel(ctx), wt); err != nil {
			// In a real system, this should be logged to a central logger
			_ = err
		}
	}()

	feedback := ""

	for {
		// NOTE: Check for context cancellation at the top of every retry iteration.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := o.store.TransitionState(task.ID, state.StateWorkerRunning); err != nil {
			return err
		}

		o.recordToolCall(task.ID, "worker.ProduceSolution")
		if err := o.worker.ProduceSolution(ctx, task, wt, feedback); err != nil {
			return err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := o.store.TransitionState(task.ID, state.StateAwaitingValidation); err != nil {
			return err
		}

		o.recordToolCall(task.ID, "validator.Evaluate")
		passes, validationFeedback, err := o.validator.Evaluate(ctx, task, wt)
		if err != nil {
			return err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		feedback = validationFeedback

		if err := o.store.RecordValidationDecision(task.ID, passes, feedback); err != nil {
			return err
		}

		if passes {
			hasConflicts, err := o.git.CheckForConflicts(ctx, wt)
			if err != nil {
				return err
			}
			if hasConflicts {
				regions, extractErr := o.git.ExtractConflictRegions(ctx, wt)
				if extractErr != nil {
					return extractErr
				}
				feedback = fmt.Sprintf("Conflicts detected: %+v", regions)

				task, err = o.store.GetTask(task.ID)
				if err != nil {
					return err
				}
				if task.RetryCount >= task.MaxRetries {
					if err := o.store.TransitionState(task.ID, state.StateFailedEscalated); err != nil {
						return err
					}
					return errors.New("worktree has conflicts and max retries reached")
				}

				if err := o.store.TransitionState(task.ID, state.StateRevisionRequested); err != nil {
					return err
				}
				if err := o.store.IncrementRetry(task.ID); err != nil {
					return err
				}
				continue
			}

			if err := o.store.TransitionState(task.ID, state.StateApproved); err != nil {
				return err
			}

			finalOutput, err := o.git.GetWorktreeDiff(ctx, wt)
			if err != nil {
				return err
			}

			if err := o.git.CommitWorktree(ctx, task.ID, wt); err != nil {
				return err
			}
			if err := o.store.RecordFinalOutput(task.ID, finalOutput); err != nil {
				return err
			}
			return o.store.TransitionState(task.ID, state.StateCommitted)
		}

		task, err = o.store.GetTask(task.ID)
		if err != nil {
			return err
		}
		if task.RetryCount >= task.MaxRetries {
			if err := o.store.TransitionState(task.ID, state.StateFailedEscalated); err != nil {
				return err
			}
			return ErrValidationFailed
		}

		if err := o.store.TransitionState(task.ID, state.StateRevisionRequested); err != nil {
			return err
		}
		if err := o.store.IncrementRetry(task.ID); err != nil {
			return err
		}
	}
}
