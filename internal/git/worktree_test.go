package git

import (
	"context"
	"testing"
)

func TestProvisionWorktree(t *testing.T) {
	manager := NewWorktreeManager()
	ctx := context.Background()

	wt, err := manager.ProvisionWorktree(ctx, "main", "task-branch", "/tmp/worktree-task")
	if err != nil {
		t.Fatalf("ProvisionWorktree failed unexpectedly: %v", err)
	}
	if wt == nil {
		t.Fatal("Expected provisioned worktree, got nil")
	}

	if wt.Path != "/tmp/worktree-task" {
		t.Errorf("Expected path /tmp/worktree-task, got %s", wt.Path)
	}
}

func TestCheckForConflicts_NoConflict(t *testing.T) {
	manager := NewWorktreeManager()
	ctx := context.Background()
	wt := &Worktree{Path: "/tmp/wt1", Branch: "b1", BaseCommit: "c1"}

	hasConflict, err := manager.CheckForConflicts(ctx, wt)
	if err != nil {
		t.Fatalf("CheckForConflicts failed unexpectedly: %v", err)
	}
	if hasConflict {
		t.Error("Expected no conflict, but a conflict was detected")
	}
}

func TestCheckForConflicts_WithConflict(t *testing.T) {
	manager := NewWorktreeManager()
	ctx := context.Background()
	wt := &Worktree{Path: "/tmp/wt2", Branch: "b2", BaseCommit: "c2"}

	hasConflict, err := manager.CheckForConflicts(ctx, wt)
	if err != nil {
		t.Fatalf("CheckForConflicts failed unexpectedly: %v", err)
	}
	if !hasConflict {
		t.Error("Expected conflict, but none was detected")
	}
}

func TestExtractConflictRegions(t *testing.T) {
	manager := NewWorktreeManager()
	ctx := context.Background()
	wt := &Worktree{Path: "/tmp/wt3", Branch: "b3", BaseCommit: "c3"}

	regions, err := manager.ExtractConflictRegions(ctx, wt)
	if err != nil {
		t.Fatalf("ExtractConflictRegions failed unexpectedly: %v", err)
	}

	if len(regions) == 0 {
		t.Fatal("Expected conflict regions, got none")
	}

	if regions[0].FilePath == "" {
		t.Error("Expected file path in conflict region")
	}
}

func TestDestroyWorktree(t *testing.T) {
	manager := NewWorktreeManager()
	ctx := context.Background()
	wt := &Worktree{Path: "/tmp/wt4", Branch: "b4", BaseCommit: "c4"}

	err := manager.DestroyWorktree(ctx, wt)
	if err != nil {
		t.Fatalf("DestroyWorktree failed unexpectedly: %v", err)
	}
}
