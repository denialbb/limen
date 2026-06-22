package state

import (
	"errors"
)

// TaskState represents the lifecycle state of a task in the Limen orchestration engine.
type TaskState string

const (
	StateCreated            TaskState = "CREATED"
	StateContextBuilding    TaskState = "CONTEXT_BUILDING"
	StateRoutingEvaluation  TaskState = "ROUTING_EVALUATION"
	StateWorkerRunning      TaskState = "WORKER_RUNNING"
	StateAwaitingValidation TaskState = "AWAITING_VALIDATION"
	StateRevisionRequested  TaskState = "REVISION_REQUESTED"
	StateFailedEscalated    TaskState = "FAILED_ESCALATED"
	StateApproved           TaskState = "APPROVED"
	StateCommitted          TaskState = "COMMITTED"
)

var (
	ErrInvalidTransition = errors.New("invalid state transition")
	ErrMaxRetriesReached = errors.New("maximum retries reached")
	ErrTaskNotFound      = errors.New("task not found")
	ErrTaskAlreadyExists = errors.New("task already exists")
)

// Task represents a unit of work managed by Limen.
type Task struct {
	ID                 string
	CurrentState       TaskState
	RetryCount         int
	MaxRetries         int
	ValidationDecision string
	FinalOutput        string
	ContextSnapshot    string
}

// Store defines the contract for persisting and retrieving task state.
// The Go Core is the exclusive owner of this state, utilizing SQLite.
type Store interface {
	// CreateTask initializes a task in the CREATED state.
	CreateTask(id string, maxRetries int) (*Task, error)

	// GetTask retrieves the current state of a task.
	GetTask(id string) (*Task, error)

	// TransitionState attempts to transition the task to a new state.
	// It must strictly enforce the Limen Task State Machine invariants.
	TransitionState(id string, newState TaskState) error

	// IncrementRetry increments the retry counter for the task,
	// enforcing the invariant that REVISION_REQUESTED to WORKER_RUNNING
	// is gated by the max retry limit.
	IncrementRetry(id string) error

	// RecordToolCall persists a tool call made while processing the task.
	RecordToolCall(id, call string) error

	// RecordValidationDecision persists the validation decision and feedback.
	RecordValidationDecision(id string, pass bool, feedback string) error

	// RecordFinalOutput persists the final output produced for the task.
	RecordFinalOutput(id, output string) error

	// RecordContextSnapshot persists the context snapshot for the task.
	RecordContextSnapshot(id string, snapshot string) error
}

// IsValidTransition encodes the Limen Task State Machine.
// It is exported so tests and mocks can delegate to the canonical definition.
func IsValidTransition(current, next TaskState) bool {
	switch current {
	case StateCreated:
		return next == StateContextBuilding
	case StateContextBuilding:
		return next == StateRoutingEvaluation
	case StateRoutingEvaluation:
		// NOTE: Allow going back to context building for router Expand
		// decisions, or straight to escalation for router Escalate decisions.
		return next == StateWorkerRunning || next == StateContextBuilding || next == StateFailedEscalated
	case StateWorkerRunning:
		return next == StateAwaitingValidation
	case StateAwaitingValidation:
		return next == StateApproved || next == StateRevisionRequested || next == StateFailedEscalated
	case StateRevisionRequested:
		// NOTE: Allow escalation from revision requested if we decide to
		// abandon retrying (e.g. max retries exhausted).
		return next == StateWorkerRunning || next == StateFailedEscalated
	case StateApproved:
		return next == StateCommitted
	case StateFailedEscalated, StateCommitted:
		return false
	default:
		return false
	}
}


