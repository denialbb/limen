package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func setupTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	cmd := exec.Command("git", "init", "-b", "main")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init failed: %v", err)
	}

	cmd = exec.Command("git", "config", "user.name", "Test User")
	cmd.Dir = dir
	cmd.Run()
	cmd = exec.Command("git", "config", "user.email", "test@example.com")
	cmd.Dir = dir
	cmd.Run()

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
	manager := NewWorktreeManager(repoDir, "main")
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
	manager := NewWorktreeManager(repoDir, "main")
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
	manager := NewWorktreeManager(repoDir, "main")
	ctx := context.Background()

	baseCommit := getHeadCommit(t, repoDir)

	os.WriteFile(filepath.Join(repoDir, "file.txt"), []byte("master content\n"), 0644)
	cmd := exec.Command("git", "commit", "-am", "Master commit")
	cmd.Dir = repoDir
	cmd.Run()

	wtPath := filepath.Join(t.TempDir(), "wt3")
	wt, err := manager.ProvisionWorktree(ctx, baseCommit, "", wtPath)
	if err != nil {
		t.Fatal(err)
	}

	os.WriteFile(filepath.Join(wtPath, "file.txt"), []byte("branch content\n"), 0644)

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
	manager := NewWorktreeManager(repoDir, "main")
	ctx := context.Background()

	baseCommit := getHeadCommit(t, repoDir)

	os.WriteFile(filepath.Join(repoDir, "file.txt"), []byte("master content\n"), 0644)
	cmd := exec.Command("git", "commit", "-am", "Master commit")
	cmd.Dir = repoDir
	cmd.Run()

	wtPath := filepath.Join(t.TempDir(), "wt4")
	wt, err := manager.ProvisionWorktree(ctx, baseCommit, "", wtPath)
	if err != nil {
		t.Fatal(err)
	}

	os.WriteFile(filepath.Join(wtPath, "file.txt"), []byte("branch content\n"), 0644)

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

	t.Logf("BaseDiff: %q", regions[0].BaseDiff)
	t.Logf("ProposedDiff: %q", regions[0].ProposedDiff)
}

func TestDestroyWorktree(t *testing.T) {
	repoDir := setupTestRepo(t)
	manager := NewWorktreeManager(repoDir, "main")
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
	manager := NewWorktreeManager(repoDir, "main")
	ctx := context.Background()

	wtPath := filepath.Join(t.TempDir(), "wt6")
	wt, err := manager.ProvisionWorktree(ctx, "main", "destroy-branch-2", wtPath)
	if err != nil {
		t.Fatal(err)
	}

	os.RemoveAll(wtPath)

	err = manager.DestroyWorktree(ctx, wt)
	if err != nil {
		t.Fatalf("DestroyWorktree should handle missing directory gracefully: %v", err)
	}
}

func TestProvisionWorktree_InvalidCommit(t *testing.T) {
	repoDir := setupTestRepo(t)
	manager := NewWorktreeManager(repoDir, "main")
	ctx := context.Background()

	wtPath := filepath.Join(t.TempDir(), "wt-invalid")
	_, err := manager.ProvisionWorktree(ctx, "invalid-commit-sha", "new-branch", wtPath)
	if err == nil {
		t.Fatal("Expected error when provisioning with invalid base commit, got nil")
	}
}

func TestCheckForConflicts_UncommittedChangesNoConflict(t *testing.T) {
	repoDir := setupTestRepo(t)
	manager := NewWorktreeManager(repoDir, "main")
	ctx := context.Background()

	wtPath := filepath.Join(t.TempDir(), "wt-uncommitted")
	wt, _ := manager.ProvisionWorktree(ctx, "main", "branch-uncommitted", wtPath)

	os.WriteFile(filepath.Join(wtPath, "new-file.txt"), []byte("new content\n"), 0644)

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
	manager := NewWorktreeManager(repoDir, "main")
	ctx := context.Background()

	baseCommit := getHeadCommit(t, repoDir)

	os.WriteFile(filepath.Join(repoDir, "file.txt"), []byte("line1 master\nc1\nc2\nc3\nc4\nc5\nline3 master\n"), 0644)
	cmd := exec.Command("git", "commit", "-am", "Master multi commit")
	cmd.Dir = repoDir
	cmd.Run()

	wtPath := filepath.Join(t.TempDir(), "wt-multi")
	wt, _ := manager.ProvisionWorktree(ctx, baseCommit, "", wtPath)

	os.WriteFile(filepath.Join(wtPath, "file.txt"), []byte("line1 branch\nc1\nc2\nc3\nc4\nc5\nline3 branch\n"), 0644)

	regions, err := manager.ExtractConflictRegions(ctx, wt)
	if err != nil {
		t.Fatalf("ExtractConflictRegions failed unexpectedly: %v", err)
	}

	if len(regions) != 1 {
		t.Fatalf("Expected 1 conflict region, got %d", len(regions))
	}
}

func TestDestroyWorktree_WithUncommittedChanges(t *testing.T) {
	repoDir := setupTestRepo(t)
	manager := NewWorktreeManager(repoDir, "main")
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

func TestCommitWorktree(t *testing.T) {
	repoDir := setupTestRepo(t)
	manager := NewWorktreeManager(repoDir, "main")
	ctx := context.Background()

	baseCommit := getHeadCommit(t, repoDir)

	wtPath := filepath.Join(t.TempDir(), "wt-commit")
	wt, err := manager.ProvisionWorktree(ctx, baseCommit, "commit-branch", wtPath)
	if err != nil {
		t.Fatal(err)
	}

	os.WriteFile(filepath.Join(wtPath, "file.txt"), []byte("worker content\n"), 0644)

	if err := manager.CommitWorktree(ctx, "task-commit", wt); err != nil {
		t.Fatalf("CommitWorktree failed unexpectedly: %v", err)
	}

	newCommit := getHeadCommit(t, repoDir)
	if newCommit == baseCommit {
		t.Error("Expected canonical branch to advance")
	}

	canonicalContent, err := showFileAtCommit(t, repoDir, "main", "file.txt")
	if err != nil {
		t.Fatalf("Failed to read canonical file content: %v", err)
	}
	if canonicalContent != "worker content\n" {
		t.Errorf("Expected canonical file to contain worker content, got: %s", canonicalContent)
	}

	manager.DestroyWorktree(ctx, wt)
}

func TestCommitWorktree_EmptyDiff(t *testing.T) {
	repoDir := setupTestRepo(t)
	manager := NewWorktreeManager(repoDir, "main")
	ctx := context.Background()

	wtPath := filepath.Join(t.TempDir(), "wt-empty")
	wt, err := manager.ProvisionWorktree(ctx, "main", "empty-branch", wtPath)
	if err != nil {
		t.Fatal(err)
	}

	err = manager.CommitWorktree(ctx, "task-empty", wt)
	if err == nil {
		t.Fatal("Expected error when committing empty diff, got nil")
	}

	manager.DestroyWorktree(ctx, wt)
}

func getHeadCommit(t *testing.T, repoDir string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("Failed to get HEAD: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func showFileAtCommit(t *testing.T, repoDir, branch, file string) (string, error) {
	t.Helper()
	cmd := exec.Command("git", "show", branch+":"+file)
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func TestIsValid_CleanRepo(t *testing.T) {
	repoDir := setupTestRepo(t)
	manager := NewWorktreeManager(repoDir, "main")

	valid, err := manager.IsValid(context.Background())
	if err != nil {
		t.Fatalf("IsValid returned error: %v", err)
	}
	if !valid {
		t.Fatal("expected clean repository to be valid")
	}
}

func TestIsValid_DirtyRepo(t *testing.T) {
	repoDir := setupTestRepo(t)
	manager := NewWorktreeManager(repoDir, "main")

	if err := os.WriteFile(filepath.Join(repoDir, "file.txt"), []byte("dirty change\n"), 0644); err != nil {
		t.Fatal(err)
	}

	valid, err := manager.IsValid(context.Background())
	if err != nil {
		t.Fatalf("IsValid returned error: %v", err)
	}
	if valid {
		t.Fatal("expected repository with uncommitted tracked changes to be invalid")
	}
}

func TestIsValid_NotARepo(t *testing.T) {
	dir := t.TempDir()
	manager := NewWorktreeManager(dir, "main")

	valid, err := manager.IsValid(context.Background())
	if err == nil {
		t.Fatal("expected error for non-git directory")
	}
	if valid {
		t.Fatal("expected non-git directory to be invalid")
	}
}
