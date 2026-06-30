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
	Prompt             string
}

// ToolCall represents a single tool invocation recorded against a task.
type ToolCall struct {
	ID       int64
	TaskID   string
	Call     string
	Args     string
	Response string
}

// Store defines the contract for persisting and retrieving task state.
// The Go Core is the exclusive owner of this state, utilizing SQLite.
type Store interface {
	// CreateTask initializes a task in the CREATED state.
	CreateTask(id string, maxRetries int, prompt string) (*Task, error)

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
	// args and response capture the full tool-call shape so the canonical
	// log is not a lossy label-only trace.
	RecordToolCall(id, call, args, response string) error

	// GetToolCalls returns all tool calls recorded for the task in insertion order.
	GetToolCalls(id string) ([]ToolCall, error)

	// RecordValidationDecision persists the validation decision and feedback.
	RecordValidationDecision(id string, pass bool, feedback string) error

	// RecordFinalOutput persists the final output produced for the task.
	RecordFinalOutput(id, output string) error

	// RecordContextSnapshot persists the context snapshot for the task.
	RecordContextSnapshot(id string, snapshot string) error

	// TransitionAndRecordFinalOutput transitions the task to newState and records
	// the final output in a single atomic transaction. This ensures there is no
	// window where the task is in newState without a recorded final output.
	TransitionAndRecordFinalOutput(id string, newState TaskState, finalOutput string) error

	// TransitionAndRecordContextSnapshot transitions the task to newState and records
	// the context snapshot in a single atomic transaction.
	TransitionAndRecordContextSnapshot(id string, newState TaskState, snapshot string) error

	// WriteCallbackSignal writes a pending callback signal and returns its ID.
	WriteCallbackSignal(taskID, summary string) (int64, error)

	// PollCallbackSignal checks if the callback has a verdict.
	// Returns the verdict, a boolean indicating if it's completed, and error.
	PollCallbackSignal(callbackID int64) (string, bool, error)

	// GetPendingCallback retrieves a pending callback for a task.
	// Returns (callbackID, summary, found, error).
	GetPendingCallback(taskID string) (int64, string, bool, error)

	// WriteCallbackVerdict writes the verdict for a pending callback, marking it completed.
	WriteCallbackVerdict(callbackID int64, verdict string) error
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


