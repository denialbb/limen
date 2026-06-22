package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func setupTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// Initialize git repo
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init failed: %v", err)
	}

	// Configure git
	cmd = exec.Command("git", "config", "user.name", "Test User")
	cmd.Dir = dir
	cmd.Run()
	cmd = exec.Command("git", "config", "user.email", "test@example.com")
	cmd.Dir = dir
	cmd.Run()

	// Initial commit
	err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("base content\n"), 0644)
	if err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command("git", "add", "file.txt")
	cmd.Dir = dir
	cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "Initial commit")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git commit failed: %v", err)
	}

	return dir
}

func TestProvisionWorktree(t *testing.T) {
	repoDir := setupTestRepo(t)
	manager := NewWorktreeManager(repoDir)
	ctx := context.Background()

	wtPath := filepath.Join(t.TempDir(), "wt1")
	wt, err := manager.ProvisionWorktree(ctx, "main", "task-branch", wtPath)
	if err != nil {
		t.Fatalf("ProvisionWorktree failed unexpectedly: %v", err)
	}
	if wt == nil {
		t.Fatal("Expected provisioned worktree, got nil")
	}

	if wt.Path != wtPath {
		t.Errorf("Expected path %s, got %s", wtPath, wt.Path)
	}

	// Verify worktree exists and is on the right branch
	cmd := exec.Command("git", "branch", "--show-current")
	cmd.Dir = wtPath
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("Failed to get current branch in worktree: %v", err)
	}
	if string(out) != "task-branch\n" {
		t.Errorf("Expected branch task-branch, got %s", string(out))
	}
}

func TestCheckForConflicts_NoConflict(t *testing.T) {
	repoDir := setupTestRepo(t)
	manager := NewWorktreeManager(repoDir)
	ctx := context.Background()

	wtPath := filepath.Join(t.TempDir(), "wt2")
	wt, err := manager.ProvisionWorktree(ctx, "main", "task-branch-2", wtPath)
	if err != nil {
		t.Fatal(err)
	}

	hasConflict, err := manager.CheckForConflicts(ctx, wt)
	if err != nil {
		t.Fatalf("CheckForConflicts failed unexpectedly: %v", err)
	}
	if hasConflict {
		t.Error("Expected no conflict, but a conflict was detected")
	}
}

func TestCheckForConflicts_WithConflict(t *testing.T) {
	repoDir := setupTestRepo(t)
	manager := NewWorktreeManager(repoDir)
	ctx := context.Background()

	// Create a branch from master
	cmd := exec.Command("git", "branch", "conflict-branch")
	cmd.Dir = repoDir
	cmd.Run()

	// Commit on master
	os.WriteFile(filepath.Join(repoDir, "file.txt"), []byte("master content\n"), 0644)
	cmd = exec.Command("git", "commit", "-am", "Master commit")
	cmd.Dir = repoDir
	cmd.Run()

	// Provision worktree on conflict-branch
	wtPath := filepath.Join(t.TempDir(), "wt3")
	wt, err := manager.ProvisionWorktree(ctx, "conflict-branch", "", wtPath)
	if err != nil {
		t.Fatal(err)
	}

	// Commit on worktree
	os.WriteFile(filepath.Join(wtPath, "file.txt"), []byte("branch content\n"), 0644)
	cmd = exec.Command("git", "commit", "-am", "Branch commit")
	cmd.Dir = wtPath
	cmd.Run()

	// Try to merge master into worktree, causing conflict
	cmd = exec.Command("git", "merge", "main")
	cmd.Dir = wtPath
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("Expected merge to fail with conflict, but it succeeded. Output: %s", string(out))
	}
	t.Logf("Merge output: %s", string(out))

	hasConflict, err := manager.CheckForConflicts(ctx, wt)
	if err != nil {
		t.Fatalf("CheckForConflicts failed unexpectedly: %v", err)
	}
	if !hasConflict {
		t.Error("Expected conflict, but none was detected")
	}
}

func TestExtractConflictRegions(t *testing.T) {
	repoDir := setupTestRepo(t)
	manager := NewWorktreeManager(repoDir)
	ctx := context.Background()

	// Setup conflict
	cmd := exec.Command("git", "branch", "conflict-branch-4")
	cmd.Dir = repoDir
	cmd.Run()

	os.WriteFile(filepath.Join(repoDir, "file.txt"), []byte("master content\n"), 0644)
	cmd = exec.Command("git", "commit", "-am", "Master commit")
	cmd.Dir = repoDir
	cmd.Run()

	wtPath := filepath.Join(t.TempDir(), "wt4")
	wt, err := manager.ProvisionWorktree(ctx, "conflict-branch-4", "", wtPath)
	if err != nil {
		t.Fatal(err)
	}

	os.WriteFile(filepath.Join(wtPath, "file.txt"), []byte("branch content\n"), 0644)
	cmd = exec.Command("git", "commit", "-am", "Branch commit")
	cmd.Dir = wtPath
	cmd.Run()

	cmd = exec.Command("git", "merge", "main")
	cmd.Dir = wtPath
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("Expected merge to fail with conflict, but it succeeded. Output: %s", string(out))
	}
	t.Logf("Merge output: %s", string(out))

	// Extract conflicts
	regions, err := manager.ExtractConflictRegions(ctx, wt)
	if err != nil {
		t.Fatalf("ExtractConflictRegions failed unexpectedly: %v", err)
	}

	if len(regions) == 0 {
		t.Fatal("Expected conflict regions, got none")
	}

	if regions[0].FilePath != "file.txt" {
		t.Errorf("Expected file path file.txt, got %s", regions[0].FilePath)
	}

	// Print to see diffs
	t.Logf("BaseDiff: %q", regions[0].BaseDiff)
	t.Logf("ProposedDiff: %q", regions[0].ProposedDiff)
}

func TestDestroyWorktree(t *testing.T) {
	repoDir := setupTestRepo(t)
	manager := NewWorktreeManager(repoDir)
	ctx := context.Background()

	wtPath := filepath.Join(t.TempDir(), "wt5")
	wt, err := manager.ProvisionWorktree(ctx, "main", "destroy-branch", wtPath)
	if err != nil {
		t.Fatal(err)
	}

	err = manager.DestroyWorktree(ctx, wt)
	if err != nil {
		t.Fatalf("DestroyWorktree failed unexpectedly: %v", err)
	}

	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Errorf("Expected worktree directory %s to be deleted", wtPath)
	}
}

func TestDestroyWorktree_MissingDir(t *testing.T) {
	repoDir := setupTestRepo(t)
	manager := NewWorktreeManager(repoDir)
	ctx := context.Background()

	wtPath := filepath.Join(t.TempDir(), "wt6")
	wt, err := manager.ProvisionWorktree(ctx, "main", "destroy-branch-2", wtPath)
	if err != nil {
		t.Fatal(err)
	}

	// Remove dir manually
	os.RemoveAll(wtPath)

	err = manager.DestroyWorktree(ctx, wt)
	if err != nil {
		t.Fatalf("DestroyWorktree should handle missing directory gracefully: %v", err)
	}
}

func TestProvisionWorktree_InvalidCommit(t *testing.T) {
	repoDir := setupTestRepo(t)
	manager := NewWorktreeManager(repoDir)
	ctx := context.Background()

	wtPath := filepath.Join(t.TempDir(), "wt-invalid")
	_, err := manager.ProvisionWorktree(ctx, "invalid-commit-sha", "new-branch", wtPath)
	if err == nil {
		t.Fatal("Expected error when provisioning with invalid base commit, got nil")
	}
}

func TestCheckForConflicts_UncommittedChangesNoConflict(t *testing.T) {
	repoDir := setupTestRepo(t)
	manager := NewWorktreeManager(repoDir)
	ctx := context.Background()

	wtPath := filepath.Join(t.TempDir(), "wt-uncommitted")
	wt, _ := manager.ProvisionWorktree(ctx, "main", "branch-uncommitted", wtPath)
	
	// Modify file but do not commit, and no merge conflict
	os.WriteFile(filepath.Join(wtPath, "file.txt"), []byte("modified content\n"), 0644)

	hasConflict, err := manager.CheckForConflicts(ctx, wt)
	if err != nil {
		t.Fatalf("CheckForConflicts failed unexpectedly: %v", err)
	}
	if hasConflict {
		t.Error("Expected no conflict, but a conflict was detected")
	}
}

func TestExtractConflictRegions_MultipleConflicts(t *testing.T) {
	repoDir := setupTestRepo(t)
	manager := NewWorktreeManager(repoDir)
	ctx := context.Background()

	// Setup master
	cmd := exec.Command("git", "branch", "conflict-branch-multi")
	cmd.Dir = repoDir
	cmd.Run()

	os.WriteFile(filepath.Join(repoDir, "file.txt"), []byte("line1 master\nc1\nc2\nc3\nc4\nc5\nline3 master\n"), 0644)
	cmd = exec.Command("git", "commit", "-am", "Master multi commit")
	cmd.Dir = repoDir
	cmd.Run()

	wtPath := filepath.Join(t.TempDir(), "wt-multi")
	wt, _ := manager.ProvisionWorktree(ctx, "conflict-branch-multi", "", wtPath)

	os.WriteFile(filepath.Join(wtPath, "file.txt"), []byte("line1 branch\nc1\nc2\nc3\nc4\nc5\nline3 branch\n"), 0644)
	cmd = exec.Command("git", "commit", "-am", "Branch multi commit")
	cmd.Dir = wtPath
	cmd.Run()

	// Merge
	cmd = exec.Command("git", "merge", "main")
	cmd.Dir = wtPath
	cmd.Run()

	regions, err := manager.ExtractConflictRegions(ctx, wt)
	if err != nil {
		t.Fatalf("ExtractConflictRegions failed unexpectedly: %v", err)
	}

	if len(regions) != 2 {
		t.Fatalf("Expected 2 conflict regions, got %d", len(regions))
	}
}

func TestDestroyWorktree_WithUncommittedChanges(t *testing.T) {
	repoDir := setupTestRepo(t)
	manager := NewWorktreeManager(repoDir)
	ctx := context.Background()

	wtPath := filepath.Join(t.TempDir(), "wt-destroy-dirty")
	wt, _ := manager.ProvisionWorktree(ctx, "main", "branch-destroy-dirty", wtPath)

	os.WriteFile(filepath.Join(wtPath, "file.txt"), []byte("dirty content\n"), 0644)

	err := manager.DestroyWorktree(ctx, wt)
	if err != nil {
		t.Fatalf("DestroyWorktree failed unexpectedly on dirty worktree: %v", err)
	}

	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Errorf("Expected worktree directory %s to be deleted", wtPath)
	}
}
