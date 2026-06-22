package integration_test

import (
	"context"
	"testing"

	"github.com/denialbb/limen/internal/git"
	"github.com/denialbb/limen/internal/orchestrator"
	"github.com/denialbb/limen/internal/state"
)

type dummyRouter struct{}

func (r *dummyRouter) Evaluate(ctx context.Context, task *state.Task) (orchestrator.RouterDecision, error) {
	return orchestrator.DecisionProceed, nil
}

type dummyWorker struct{
	callCount int
}

func (w *dummyWorker) ProduceSolution(ctx context.Context, task *state.Task) error {
	w.callCount++
	return nil
}

type dummyValidator struct{
	passes bool
}

func (v *dummyValidator) Evaluate(ctx context.Context, task *state.Task) (bool, string, error) {
	return v.passes, "feedback", nil
}

type dummyGitClient struct {
	manager git.WorktreeManager
}

func (g *dummyGitClient) IsValid(ctx context.Context) (bool, error) {
	return true, nil
}

func (g *dummyGitClient) CommitWorktree(ctx context.Context, taskID string) error {
	return nil
}

func (g *dummyGitClient) ResolveConflict(ctx context.Context, taskID string) error {
	return nil
}

func TestFullOrchestrationCycle(t *testing.T) {
	store := state.NewStore()
	manager := git.NewWorktreeManager(".")
	router := &dummyRouter{}
	worker := &dummyWorker{}
	validator := &dummyValidator{passes: true}
	gitClient := &dummyGitClient{manager: manager}

	orch := orchestrator.NewOrchestrator(store, router, worker, validator, gitClient)

	taskID := "task-integration-1"
	_, err := store.CreateTask(taskID, 3)
	if err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	err = orch.RunTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("Orchestrator run failed: %v", err)
	}

	task, err := store.GetTask(taskID)
	if err != nil {
		t.Fatalf("Failed to get task: %v", err)
	}

	if task.CurrentState != state.StateCommitted {
		t.Errorf("Expected state COMMITTED, got %s", task.CurrentState)
	}
	
	if worker.callCount != 1 {
		t.Errorf("Expected worker to be called exactly once, got %d", worker.callCount)
	}
}

func TestFullOrchestrationCycle_ValidatorRetry(t *testing.T) {
	store := state.NewStore()
	manager := git.NewWorktreeManager(".")
	router := &dummyRouter{}
	worker := &dummyWorker{}
	validator := &dummyValidator{passes: false}
	gitClient := &dummyGitClient{manager: manager}

	orch := orchestrator.NewOrchestrator(store, router, worker, validator, gitClient)

	taskID := "task-integration-2"
	_, err := store.CreateTask(taskID, 2)
	if err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	err = orch.RunTask(context.Background(), taskID)
	if err == nil {
		t.Fatal("Expected error after validator exhausted retries, got nil")
	}

	task, err := store.GetTask(taskID)
	if err != nil {
		t.Fatalf("Failed to get task: %v", err)
	}

	if task.CurrentState != state.StateFailedEscalated {
		t.Errorf("Expected state FAILED_ESCALATED, got %s", task.CurrentState)
	}
	
	// Called once initially + 2 retries = 3 calls
	if worker.callCount != 3 {
		t.Errorf("Expected worker to be called 3 times, got %d", worker.callCount)
	}
}
