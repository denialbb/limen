package state

import (
	"errors"
	"sync"
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

// MemoryStore provides an in-memory implementation of the Store interface.
type MemoryStore struct {
	mu    sync.RWMutex
	tasks map[string]*Task
}

// NewStore returns a new Store instance.
func NewStore() Store {
	return &MemoryStore{
		tasks: make(map[string]*Task),
	}
}

func (s *MemoryStore) CreateTask(id string, maxRetries int) (*Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.tasks[id]; exists {
		return nil, ErrTaskAlreadyExists
	}

	task := &Task{
		ID:           id,
		CurrentState: StateCreated,
		RetryCount:   0,
		MaxRetries:   maxRetries,
	}
	s.tasks[id] = task
	return task, nil
}

func (s *MemoryStore) GetTask(id string) (*Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	task, exists := s.tasks[id]
	if !exists {
		return nil, ErrTaskNotFound
	}
	
	tCopy := *task
	return &tCopy, nil
}

func isValidTransition(current, next TaskState) bool {
	switch current {
	case StateCreated:
		return next == StateContextBuilding
	case StateContextBuilding:
		return next == StateRoutingEvaluation
	case StateRoutingEvaluation:
		return next == StateWorkerRunning
	case StateWorkerRunning:
		return next == StateAwaitingValidation
	case StateAwaitingValidation:
		return next == StateApproved || next == StateRevisionRequested || next == StateFailedEscalated
	case StateRevisionRequested:
		return next == StateWorkerRunning
	case StateApproved:
		return next == StateCommitted
	case StateFailedEscalated, StateCommitted:
		return false
	default:
		return false
	}
}

func (s *MemoryStore) TransitionState(id string, newState TaskState) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	task, exists := s.tasks[id]
	if !exists {
		return ErrTaskNotFound
	}

	if !isValidTransition(task.CurrentState, newState) {
		return ErrInvalidTransition
	}

	task.CurrentState = newState
	return nil
}

func (s *MemoryStore) IncrementRetry(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	task, exists := s.tasks[id]
	if !exists {
		return ErrTaskNotFound
	}

	if task.RetryCount >= task.MaxRetries {
		return ErrMaxRetriesReached
	}

	task.RetryCount++
	return nil
}
