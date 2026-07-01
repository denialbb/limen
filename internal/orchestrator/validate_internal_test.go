package orchestrator

import (
	"context"
	"errors"
	"testing"

	"github.com/denialbb/limen/internal/bus"
	"github.com/denialbb/limen/internal/git"
	"github.com/denialbb/limen/internal/state"
)

// vcStore is a minimal in-memory Store for driving validateCandidate. It embeds
// state.Store so unused methods satisfy the interface while panicking if hit.
type vcStore struct {
	state.Store
	task            state.Task
	recordedPass    bool
	recordedFeedbk  string
	recordCalled    bool
	transitions     []state.TaskState
	recordDecideErr error
}

func (s *vcStore) GetTask(id string) (*state.Task, error) {
	t := s.task
	return &t, nil
}

func (s *vcStore) TransitionState(id string, newState state.TaskState) error {
	s.transitions = append(s.transitions, newState)
	s.task.CurrentState = newState
	return nil
}

func (s *vcStore) RecordToolCall(id, call, args, response string) error { return nil }

func (s *vcStore) RecordValidationDecision(id string, pass bool, feedback string) error {
	s.recordCalled = true
	s.recordedPass = pass
	s.recordedFeedbk = feedback
	return s.recordDecideErr
}

// vcValidator returns a fixed verdict.
type vcValidator struct {
	passes   bool
	feedback string
	err      error
}

func (v *vcValidator) Evaluate(ctx context.Context, task *state.Task, wt *git.Worktree, em Emitter) (bool, string, error) {
	return v.passes, v.feedback, v.err
}

// vcGit is a minimal WorktreeManager. It embeds git.WorktreeManager so unused
// methods satisfy the interface.
type vcGit struct {
	git.WorktreeManager
	hasConflicts bool
	regions      []git.ConflictRegion
	conflictErr  error
	extractErr   error
}

func (g *vcGit) GetWorktreeDiff(ctx context.Context, wt *git.Worktree) (string, error) {
	return "diff", nil
}

func (g *vcGit) ProvisionThrowawayWorktree(ctx context.Context, patch string) (*git.Worktree, error) {
	return &git.Worktree{Path: "/tmp/throwaway"}, nil
}

func (g *vcGit) DestroyWorktree(ctx context.Context, wt *git.Worktree) error { return nil }

func (g *vcGit) CheckForConflicts(ctx context.Context, wt *git.Worktree) (bool, error) {
	return g.hasConflicts, g.conflictErr
}

func (g *vcGit) ExtractConflictRegions(ctx context.Context, wt *git.Worktree) ([]git.ConflictRegion, error) {
	return g.regions, g.extractErr
}

// vcBus captures published events.
type vcBus struct {
	events []bus.Event
}

func (b *vcBus) Publish(e bus.Event)         { b.events = append(b.events, e) }
func (b *vcBus) Subscribe() <-chan bus.Event { return nil }
func (b *vcBus) Close()                      {}

func newVCOrchestrator(store *vcStore, val *vcValidator, g *vcGit, b *vcBus) *OrchestratorImpl {
	return &OrchestratorImpl{
		store:     store,
		bus:       b,
		validator: val,
		git:       g,
	}
}

func TestValidateCandidate_Pass(t *testing.T) {
	store := &vcStore{task: state.Task{ID: "t1", CurrentState: state.StateWorkerRunning}}
	val := &vcValidator{passes: true, feedback: ""}
	g := &vcGit{hasConflicts: false}
	b := &vcBus{}
	o := newVCOrchestrator(store, val, g, b)

	outcome, err := o.validateCandidate(context.Background(), &store.task, &git.Worktree{}, b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !outcome.passes {
		t.Fatalf("expected passes=true, got false")
	}
	if !store.recordCalled || store.recordedPass != true {
		t.Fatalf("expected RecordValidationDecision(pass=true), got called=%v pass=%v", store.recordCalled, store.recordedPass)
	}
	if len(store.transitions) != 1 || store.transitions[0] != state.StateAwaitingValidation {
		t.Fatalf("expected single transition to AWAITING_VALIDATION, got %v", store.transitions)
	}
}

func TestValidateCandidate_FailWithRevision(t *testing.T) {
	store := &vcStore{task: state.Task{ID: "t2", CurrentState: state.StateWorkerRunning}}
	val := &vcValidator{passes: false, feedback: "please fix the tests"}
	g := &vcGit{}
	b := &vcBus{}
	o := newVCOrchestrator(store, val, g, b)

	outcome, err := o.validateCandidate(context.Background(), &store.task, &git.Worktree{}, b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.passes {
		t.Fatalf("expected passes=false")
	}
	if outcome.feedback != "please fix the tests" {
		t.Fatalf("expected validator feedback to flow through, got %q", outcome.feedback)
	}
	if !store.recordCalled || store.recordedPass != false || store.recordedFeedbk != "please fix the tests" {
		t.Fatalf("unexpected recorded decision: called=%v pass=%v feedback=%q", store.recordCalled, store.recordedPass, store.recordedFeedbk)
	}
}

func TestValidateCandidate_Conflict(t *testing.T) {
	store := &vcStore{task: state.Task{ID: "t3", CurrentState: state.StateWorkerRunning}}
	// Validator passes, but git reports a conflict, which must flip the outcome.
	val := &vcValidator{passes: true, feedback: ""}
	regions := []git.ConflictRegion{{FilePath: "main.go", BaseDiff: "a", ProposedDiff: "b"}}
	g := &vcGit{hasConflicts: true, regions: regions}
	b := &vcBus{}
	o := newVCOrchestrator(store, val, g, b)

	outcome, err := o.validateCandidate(context.Background(), &store.task, &git.Worktree{}, b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.passes {
		t.Fatalf("expected conflict to force passes=false")
	}
	if outcome.feedback == "" {
		t.Fatalf("expected conflict feedback to be populated")
	}
	if !store.recordCalled || store.recordedPass != false {
		t.Fatalf("expected recorded pass=false on conflict")
	}
	var sawConflict bool
	for _, e := range b.events {
		if cd, ok := e.(*bus.ConflictDetected); ok {
			sawConflict = true
			if len(cd.Regions) != 1 {
				t.Fatalf("expected 1 conflict region published, got %d", len(cd.Regions))
			}
		}
	}
	if !sawConflict {
		t.Fatalf("expected a ConflictDetected event to be published")
	}
}

func TestValidateCandidate_RecordDecisionError(t *testing.T) {
	store := &vcStore{
		task:            state.Task{ID: "t4", CurrentState: state.StateWorkerRunning},
		recordDecideErr: errors.New("db down"),
	}
	val := &vcValidator{passes: true}
	g := &vcGit{}
	b := &vcBus{}
	o := newVCOrchestrator(store, val, g, b)

	if _, err := o.validateCandidate(context.Background(), &store.task, &git.Worktree{}, b); err == nil {
		t.Fatalf("expected error from RecordValidationDecision to propagate")
	}
}
