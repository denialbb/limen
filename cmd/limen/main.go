package main

import (
	"context"
	"fmt"
	"os"

	"github.com/denialbb/limen/internal/git"
	"github.com/denialbb/limen/internal/orchestrator"
	"github.com/denialbb/limen/internal/state"
)

// cliRouter mocks the router for the basic CLI testing.
type cliRouter struct{}

func (c *cliRouter) Evaluate(ctx context.Context, task *state.Task) (orchestrator.RouterDecision, error) {
	return orchestrator.DecisionProceed, nil
}

// cliWorker mocks the worker process.
type cliWorker struct{}

func (c *cliWorker) ProduceSolution(ctx context.Context, task *state.Task) error {
	fmt.Printf("Worker producing solution for task %s\n", task.ID)
	return nil
}

// cliValidator mocks the validation process.
type cliValidator struct{}

func (c *cliValidator) Evaluate(ctx context.Context, task *state.Task) (bool, string, error) {
	fmt.Printf("Validator evaluating task %s\n", task.ID)
	return true, "", nil
}

// cliGit implements the orchestrator GitClient using the WorktreeManager.
type cliGit struct {
	manager git.WorktreeManager
}

func (c *cliGit) IsValid(ctx context.Context) (bool, error) { return true, nil }
func (c *cliGit) CommitWorktree(ctx context.Context, taskID string) error {
	fmt.Printf("Committing worktree for task %s\n", taskID)
	return nil
}
func (c *cliGit) ResolveConflict(ctx context.Context, taskID string) error { return nil }

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: limen <command>")
		os.Exit(1)
	}

	command := os.Args[1]

	store := state.NewStore()
	manager := git.NewWorktreeManager(".")

	orch := orchestrator.NewOrchestrator(
		store,
		&cliRouter{},
		&cliWorker{},
		&cliValidator{},
		&cliGit{manager: manager},
	)

	switch command {
	case "run":
		if len(os.Args) < 3 {
			fmt.Println("Usage: limen run <task_id>")
			os.Exit(1)
		}
		taskID := os.Args[2]

		_, err := store.CreateTask(taskID, 3)
		if err != nil {
			fmt.Printf("Failed to create task: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Starting task %s\n", taskID)
		if err := orch.RunTask(context.Background(), taskID); err != nil {
			fmt.Printf("Task failed: %v\n", err)
			os.Exit(1)
		}

		t, _ := store.GetTask(taskID)
		fmt.Printf("Task completed with state: %s\n", t.CurrentState)

	default:
		fmt.Printf("Unknown command: %s\n", command)
		os.Exit(1)
	}
}
