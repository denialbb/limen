package state

import "errors"

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
	ErrNotImplemented    = errors.New("not implemented")
)

// Task represents a unit of work managed by Limen.
type Task struct {
	ID           string
	CurrentState TaskState
	RetryCount   int
	MaxRetries   int
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
}

// StubStore provides a failing stub implementation of the Store interface for TDD.
type StubStore struct{}

// NewStore returns a new Store instance.
func NewStore() Store {
	return &StubStore{}
}

func (s *StubStore) CreateTask(id string, maxRetries int) (*Task, error) {
	return nil, ErrNotImplemented
}

func (s *StubStore) GetTask(id string) (*Task, error) {
	return nil, ErrNotImplemented
}

func (s *StubStore) TransitionState(id string, newState TaskState) error {
	return ErrNotImplemented
}

func (s *StubStore) IncrementRetry(id string) error {
	return ErrNotImplemented
}
