package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"time"

	"github.com/denialbb/limen/internal/bus"
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

// Emitter is the narrow interface passed to cognitive components so they can
// publish structured events to the event bus without depending on the full
// EventBus interface. It is an alias for bus.EventSink.
type Emitter = bus.EventSink

// Router evaluates the task and context entropy.
type Router interface {
	Evaluate(ctx context.Context, task *state.Task, em Emitter) (RouterDecision, error)
}

// Retriever builds the ephemeral retrieval context for a task.
type Retriever interface {
	Retrieve(ctx context.Context, task *state.Task, em Emitter) (string, error)
}

// Worker is responsible for generating candidate solutions in an isolated worktree.
type Worker interface {
	ProduceSolution(ctx context.Context, task *state.Task, wt *git.Worktree, feedback string, em Emitter) error
}

// Validator evaluates the correctness of the candidate solution.
type Validator interface {
	// Evaluate returns a boolean indicating if the solution passed, and feedback if not.
	Evaluate(ctx context.Context, task *state.Task, wt *git.Worktree, em Emitter) (bool, string, error)
}

// GitClient handles physical layer operations like validating the repository, committing,
// and managing worktrees. It is the git.WorktreeManager contract, aliased so orchestrator
// consumers depend on a single interface satisfied directly by the WorktreeManager.
type GitClient = git.WorktreeManager

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
	store        state.Store
	signaler     state.Signaler
	bus          bus.EventBus
	router       Router
	retriever    Retriever
	worker       Worker
	validator    Validator
	git          GitClient
	worktreeRoot string
}

// NewOrchestrator returns a new instance of Orchestrator.
// worktreeRoot must be an absolute path; it is the parent directory for all
// ephemeral task worktrees.
//
// The bus parameter is the EventBus through which the orchestrator publishes
// TaskStateChanged, ConflictDetected, and TaskFinalized events. It is also
// passed to each cognitive component as their Emitter (EventBus is a superset
// of EventSink), so components share the same transport as the orchestrator.
func NewOrchestrator(store state.Store, signaler state.Signaler, bus bus.EventBus, router Router, retriever Retriever, worker Worker, validator Validator, git GitClient, worktreeRoot string) Orchestrator {
	return &OrchestratorImpl{
		store:        store,
		signaler:     signaler,
		bus:          bus,
		router:       router,
		retriever:    retriever,
		worker:       worker,
		validator:    validator,
		git:          git,
		worktreeRoot: worktreeRoot,
	}
}

func (o *OrchestratorImpl) recordToolCall(taskID, tool, args, response string) {
	// NOTE: Best-effort recording. Failure to persist observability data should
	// not abort the workflow.
	_ = o.store.RecordToolCall(taskID, tool, args, response)
}

// emitter returns the EventSink used to publish events. The stored EventBus
// itself satisfies EventSink (it has Publish), so it is returned directly.
// Centralizing this keeps the wiring seam explicit for a future RedisBus swap.
func (o *OrchestratorImpl) emitter() Emitter {
	return o.bus
}

// transitionAndEmit performs a state transition and publishes a TaskStateChanged
// event capturing the from/to states. It refreshes the task from the store to
// read the authoritative current state before transitioning.
func (o *OrchestratorImpl) transitionAndEmit(taskID string, to state.TaskState, em Emitter) error {
	current, err := o.store.GetTask(taskID)
	if err != nil {
		return err
	}
	from := current.CurrentState
	if err := o.store.TransitionState(taskID, to); err != nil {
		return err
	}
	em.Publish(&bus.TaskStateChanged{
		From:      from,
		To:        to,
		TaskID:    taskID,
		Timestamp: time.Now(),
	})
	return nil
}

// finalizeAndEmit transitions the task to a terminal state and publishes a
// TaskFinalized event with the final output reference (non-empty only on the
// COMMITTED path).
func (o *OrchestratorImpl) finalizeAndEmit(taskID string, to state.TaskState, finalOutputRef string, em Emitter) error {
	if err := o.transitionAndEmit(taskID, to, em); err != nil {
		return err
	}
	em.Publish(&bus.TaskFinalized{
		TaskID:         taskID,
		FinalState:     to,
		FinalOutputRef: finalOutputRef,
		Timestamp:      time.Now(),
	})
	return nil
}

// RunTask executes the pipeline for a given task.
func (o *OrchestratorImpl) RunTask(ctx context.Context, taskID string) error {
	if !taskIDSafeRegex.MatchString(taskID) {
		return ErrInvalidTaskID
	}

	em := o.emitter()

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

		// NODE: Retrieve context first while still in the previous state,
		// then atomically transition to CONTEXT_BUILDING and record the
		// snapshot. This eliminates the crash window between the state
		// transition and the snapshot write (BUG #3).
		contextSnapshot, err := o.retriever.Retrieve(ctx, task, em)
		if err != nil {
			return err
		}

		// Read authoritative from-state; the task variable may be stale
		// after the routing evaluation on an expand iteration.
		current, err := o.store.GetTask(task.ID)
		if err != nil {
			return err
		}
		fromState := current.CurrentState

		if err := o.store.TransitionAndRecordContextSnapshot(task.ID, state.StateContextBuilding, contextSnapshot); err != nil {
			return err
		}

		em.Publish(&bus.TaskStateChanged{
			From:      fromState,
			To:        state.StateContextBuilding,
			TaskID:    task.ID,
			Timestamp: time.Now(),
		})

		// Refresh the task so the router sees the recorded context snapshot.
		task, err = o.store.GetTask(task.ID)
		if err != nil {
			return err
		}

		if err := o.transitionAndEmit(task.ID, state.StateRoutingEvaluation, em); err != nil {
			return err
		}

		o.recordToolCall(task.ID, "router.Evaluate", "", "")
		decision, err := o.router.Evaluate(ctx, task, em)
		if err != nil {
			return err
		}

		switch decision {
		case DecisionEscalate:
			if err := o.finalizeAndEmit(task.ID, state.StateFailedEscalated, "", em); err != nil {
				return err
			}
			return ErrUnresolvableEntropy
		case DecisionExpand:
			expandCount++
			if expandCount > maxExpandIterations {
				if err := o.finalizeAndEmit(task.ID, state.StateFailedEscalated, "", em); err != nil {
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

		if err := o.transitionAndEmit(task.ID, state.StateWorkerRunning, em); err != nil {
			return err
		}

		o.recordToolCall(task.ID, "worker.ProduceSolution", "", "")
		
		workerErrCh := make(chan error, 1)
		go func() {
			workerErrCh <- o.worker.ProduceSolution(ctx, task, wt, feedback, em)
		}()

		var wErr error
	workerLoop:
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case err := <-workerErrCh:
				wErr = err
				break workerLoop
			case <-time.After(200 * time.Millisecond):
				cbID, _, found, err := o.signaler.GetPendingCallback(task.ID)
				if err == nil && found {
					if err := o.transitionAndEmit(task.ID, state.StateAwaitingValidation, em); err != nil {
						return err
					}
					
					diff, err := o.git.GetWorktreeDiff(ctx, wt)
					if err != nil {
						return err
					}

					tempWt, err := o.git.ProvisionThrowawayWorktree(ctx, diff)
					if err != nil {
						return err
					}

					o.recordToolCall(task.ID, "validator.Evaluate", "", "")
					passes, validationFeedback, err := o.validator.Evaluate(ctx, task, tempWt, em)
					
					_ = o.git.DestroyWorktree(context.WithoutCancel(ctx), tempWt)
					if err != nil {
						return err
					}

					if passes {
						hasConflicts, err := o.git.CheckForConflicts(ctx, wt)
						if err != nil {
							return err
						}
						if hasConflicts {
							passes = false
							regions, extractErr := o.git.ExtractConflictRegions(ctx, wt)
							if extractErr != nil {
								em.Publish(&bus.ConflictDetected{
									TaskID:    task.ID,
									Regions:   nil,
									Timestamp: time.Now(),
								})
								return extractErr
							}
							em.Publish(&bus.ConflictDetected{
								TaskID:    task.ID,
								Regions:   regions,
								Timestamp: time.Now(),
							})
							validationFeedback = fmt.Sprintf("Conflicts detected: %+v", regions)
						}
					}

					if err := o.store.RecordValidationDecision(task.ID, passes, validationFeedback); err != nil {
						return err
					}

					verdict := state.Verdict{Passes: passes, Feedback: validationFeedback}
					_ = o.signaler.WriteCallbackVerdict(cbID, string(verdict.Marshal()))
					
					if passes {
						if err := o.transitionAndEmit(task.ID, state.StateApproved, em); err != nil {
							return err
						}
					} else {
						task, err = o.store.GetTask(task.ID)
						if err != nil {
							return err
						}
						if task.RetryCount >= task.MaxRetries {
							if err := o.finalizeAndEmit(task.ID, state.StateFailedEscalated, "", em); err != nil {
								return err
							}
							return errors.New("validation failed and max retries reached")
						}

						if err := o.transitionAndEmit(task.ID, state.StateRevisionRequested, em); err != nil {
							return err
						}
						if err := o.store.IncrementRetry(task.ID); err != nil {
							return err
						}
						task, err = o.store.GetTask(task.ID)
						if err != nil {
							return err
						}
						feedback = validationFeedback
						if err := o.transitionAndEmit(task.ID, state.StateWorkerRunning, em); err != nil {
							return err
						}
					}
				}
			}
		}

		if wErr != nil {
			return wErr
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		current, err := o.store.GetTask(task.ID)
		if err != nil {
			return err
		}

		if current.CurrentState == state.StateApproved {
			// NOTE: The workerLoop already transitioned to APPROVED via
			// the callback path. Just commit and finalize.
			finalOutput, err := o.git.GetWorktreeDiff(ctx, wt)
			if err != nil {
				return err
			}

			if err := o.git.CommitWorktree(ctx, task.ID, wt); err != nil {
				return err
			}

			if err := o.finalizeAndEmit(task.ID, state.StateCommitted, finalOutput, em); err != nil {
				return err
			}
			return nil
		}

		if current.CurrentState != state.StateWorkerRunning {
			// Unexpected terminal or intermediate state -- escalate.
			if err := o.finalizeAndEmit(task.ID, state.StateFailedEscalated, "", em); err != nil {
				return err
			}
			return errors.New("worker exited without submitting for review")
		}

		// Synchronous validation path: worker returned without registering
		// a callback (e.g. mock/synchronous workers from issues 007-012).
		// Transition through the validation states inline.
		if err := o.transitionAndEmit(task.ID, state.StateAwaitingValidation, em); err != nil {
			return err
		}

		diff, err := o.git.GetWorktreeDiff(ctx, wt)
		if err != nil {
			return err
		}

		tempWt, err := o.git.ProvisionThrowawayWorktree(ctx, diff)
		if err != nil {
			return err
		}

		o.recordToolCall(task.ID, "validator.Evaluate", "", "")
		passes, validationFeedback, vErr := o.validator.Evaluate(ctx, task, tempWt, em)

		_ = o.git.DestroyWorktree(context.WithoutCancel(ctx), tempWt)
		if vErr != nil {
			return vErr
		}

		if passes {
			hasConflicts, err := o.git.CheckForConflicts(ctx, wt)
			if err != nil {
				return err
			}
			if hasConflicts {
				passes = false
				regions, extractErr := o.git.ExtractConflictRegions(ctx, wt)
				if extractErr != nil {
					em.Publish(&bus.ConflictDetected{
						TaskID:    task.ID,
						Regions:   nil,
						Timestamp: time.Now(),
					})
					return extractErr
				}
				em.Publish(&bus.ConflictDetected{
					TaskID:    task.ID,
					Regions:   regions,
					Timestamp: time.Now(),
				})
				validationFeedback = fmt.Sprintf("Conflicts detected: %+v", regions)
			}
		}

		if err := o.store.RecordValidationDecision(task.ID, passes, validationFeedback); err != nil {
			return err
		}

		if passes {
			// NODE: Perform git operations first while still in
			// AWAITING_VALIDATION, then atomically transition to APPROVED
			// and record the final output in a single transaction. This
			// eliminates the crash window between the state transition and
			// the canonical output write (BUG #3).
			finalOutput, err := o.git.GetWorktreeDiff(ctx, wt)
			if err != nil {
				return err
			}

			if err := o.git.CommitWorktree(ctx, task.ID, wt); err != nil {
				return err
			}

			if err := o.store.TransitionAndRecordFinalOutput(task.ID, state.StateApproved, finalOutput); err != nil {
				return err
			}

			em.Publish(&bus.TaskStateChanged{
				From:      state.StateAwaitingValidation,
				To:        state.StateApproved,
				TaskID:    task.ID,
				Timestamp: time.Now(),
			})

			if err := o.finalizeAndEmit(task.ID, state.StateCommitted, finalOutput, em); err != nil {
				return err
			}
			return nil
		}

		// Validation failed -- check retry budget.
		task, err = o.store.GetTask(task.ID)
		if err != nil {
			return err
		}
		if task.RetryCount >= task.MaxRetries {
			if err := o.finalizeAndEmit(task.ID, state.StateFailedEscalated, "", em); err != nil {
				return err
			}
			return ErrValidationFailed
		}

		if err := o.transitionAndEmit(task.ID, state.StateRevisionRequested, em); err != nil {
			return err
		}
		if err := o.store.IncrementRetry(task.ID); err != nil {
			return err
		}
		// NOTE: Refresh task so the next iteration's worker receives
		// the authoritative RetryCount after the increment.
		task, err = o.store.GetTask(task.ID)
		if err != nil {
			return err
		}
		feedback = validationFeedback
	}
}

