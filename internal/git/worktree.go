// Package git provides the Git Worktree Manager for the Limen Go Core.
package git

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Worktree represents an isolated Git worktree environment.
type Worktree struct {
	// Path is the absolute filesystem path to the provisioned worktree.
	Path string
	// Branch is the name of the branch checked out in the worktree. Empty means detached HEAD.
	Branch string
	// BaseCommit is the SHA of the base commit from which this worktree was derived.
	BaseCommit string
}

// ConflictRegion represents a structured diff region where intentions conflict.
type ConflictRegion struct {
	// FilePath is the path to the file containing the conflict, relative to the repository root.
	FilePath string
	// BaseDiff is the diff from the base commit.
	BaseDiff string
	// ProposedDiff is the diff proposed by the worker.
	ProposedDiff string
}

// WorktreeManager defines the contract for managing ephemeral Git worktrees.
type WorktreeManager interface {
	// ProvisionWorktree creates an isolated environment via `git worktree add`.
	ProvisionWorktree(ctx context.Context, baseCommit, branchName, path string) (*Worktree, error)
	// CheckForConflicts detects if the worker's uncommitted patch conflicts with the canonical branch.
	CheckForConflicts(ctx context.Context, wt *Worktree) (bool, error)
	// ExtractConflictRegions extracts conflicting diff regions if a conflict is detected.
	ExtractConflictRegions(ctx context.Context, wt *Worktree) ([]ConflictRegion, error)
	// CommitWorktree applies the worker's uncommitted patch to the canonical branch and commits.
	CommitWorktree(ctx context.Context, taskID string, wt *Worktree) error
	// DestroyWorktree deletes the ephemeral worktree directory and prunes it from Git.
	DestroyWorktree(ctx context.Context, wt *Worktree) error
	// GetWorktreeDiff returns the worker's uncommitted changes relative to HEAD.
	GetWorktreeDiff(ctx context.Context, wt *Worktree) (string, error)
}

type worktreeManagerImpl struct {
	repoPath        string
	canonicalBranch string
}

// NewWorktreeManager creates a new instance of the WorktreeManager.
// canonicalBranch is the explicit branch that approved worker patches are merged into.
func NewWorktreeManager(repoPath, canonicalBranch string) WorktreeManager {
	if canonicalBranch == "" {
		canonicalBranch = "main"
	}
	return &worktreeManagerImpl{
		repoPath:        repoPath,
		canonicalBranch: canonicalBranch,
	}
}

// ProvisionWorktree creates an isolated environment via `git worktree add`.
func (m *worktreeManagerImpl) ProvisionWorktree(ctx context.Context, baseCommit, branchName, path string) (*Worktree, error) {
	cmdVerify := exec.CommandContext(ctx, "git", "rev-parse", "--verify", baseCommit)
	cmdVerify.Dir = m.repoPath
	if err := cmdVerify.Run(); err != nil {
		return nil, fmt.Errorf("invalid base commit %s: %w", baseCommit, err)
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve absolute path %s: %w", path, err)
	}

	var cmd *exec.Cmd
	if branchName != "" {
		cmd = exec.CommandContext(ctx, "git", "worktree", "add", "-b", branchName, absPath, baseCommit)
	} else {
		cmd = exec.CommandContext(ctx, "git", "worktree", "add", absPath, baseCommit)
	}
	cmd.Dir = m.repoPath

	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("git worktree add failed: %w, output: %s", err, string(out))
	}

	return &Worktree{
		Path:       absPath,
		Branch:     branchName,
		BaseCommit: baseCommit,
	}, nil
}

// getWorktreeDiff returns the worker's uncommitted changes relative to HEAD.
// Under the No Git Noise contract, HEAD equals BaseCommit, so this diff is the
// complete proposed patch. New untracked files are included by marking them with
// intent-to-add before diffing.
func (m *worktreeManagerImpl) GetWorktreeDiff(ctx context.Context, wt *Worktree) (string, error) {
	addCmd := exec.CommandContext(ctx, "git", "add", "-N", ".")
	addCmd.Dir = wt.Path
	if err := addCmd.Run(); err != nil {
		return "", fmt.Errorf("git add -N failed: %w", err)
	}

	cmd := exec.CommandContext(ctx, "git", "diff", "HEAD")
	cmd.Dir = wt.Path
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git diff HEAD failed: %w", err)
	}
	return string(out), nil
}

// provisionTempWorktree creates a detached worktree at the given commit and returns its path.
func (m *worktreeManagerImpl) provisionTempWorktree(ctx context.Context, commit string) (string, error) {
	tempDir, err := os.MkdirTemp("", "limen-wt-*")
	if err != nil {
		return "", fmt.Errorf("create temp dir: %w", err)
	}

	cmdAdd := exec.CommandContext(ctx, "git", "worktree", "add", "--detach", tempDir, commit)
	cmdAdd.Dir = m.repoPath
	if out, err := cmdAdd.CombinedOutput(); err != nil {
		os.RemoveAll(tempDir)
		return "", fmt.Errorf("git worktree add detached failed: %w, output: %s", err, string(out))
	}
	return tempDir, nil
}

// removeTempWorktree force-removes a temporary worktree.
func (m *worktreeManagerImpl) removeTempWorktree(ctx context.Context, path string) {
	cmdRm := exec.CommandContext(ctx, "git", "worktree", "remove", "--force", path)
	cmdRm.Dir = m.repoPath
	if err := cmdRm.Run(); err != nil {
		// NOTE: Best-effort cleanup; the temp dir will be removed by the OS.
		_ = os.RemoveAll(path)
	}
}

func (m *worktreeManagerImpl) CheckForConflicts(ctx context.Context, wt *Worktree) (bool, error) {
	diff, err := m.GetWorktreeDiff(ctx, wt)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(diff) == "" {
		return false, nil
	}

	tempDir, err := m.provisionTempWorktree(ctx, m.canonicalBranch)
	if err != nil {
		return false, err
	}
	defer m.removeTempWorktree(ctx, tempDir)

	applyCmd := exec.CommandContext(ctx, "git", "apply", "--check")
	applyCmd.Dir = tempDir
	applyCmd.Stdin = strings.NewReader(diff)
	if err := applyCmd.Run(); err != nil {
		return true, nil
	}
	return false, nil
}

// ExtractConflictRegions extracts conflicting diff regions from the worker's patch.
func (m *worktreeManagerImpl) ExtractConflictRegions(ctx context.Context, wt *Worktree) ([]ConflictRegion, error) {
	diff, err := m.GetWorktreeDiff(ctx, wt)
	if err != nil {
		return nil, err
	}

	tempDir, err := m.provisionTempWorktree(ctx, m.canonicalBranch)
	if err != nil {
		return nil, err
	}
	defer m.removeTempWorktree(ctx, tempDir)

	// Apply with 3-way fallback so conflicts are materialized as conflict markers.
	applyCmd := exec.CommandContext(ctx, "git", "apply", "-3")
	applyCmd.Dir = tempDir
	applyCmd.Stdin = strings.NewReader(diff)
	_ = applyCmd.Run()

	cmd := exec.CommandContext(ctx, "git", "diff", "--name-only", "--diff-filter=U")
	cmd.Dir = tempDir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff failed: %w", err)
	}

	var regions []ConflictRegion
	files := strings.Split(strings.TrimSpace(string(out)), "\n")

	for _, f := range files {
		if f == "" {
			continue
		}

		data, err := os.ReadFile(filepath.Join(tempDir, f))
		if err != nil {
			continue
		}

		base, proposed := parseConflictMarkers(string(data))
		regions = append(regions, ConflictRegion{
			FilePath:     f,
			BaseDiff:     base,
			ProposedDiff: proposed,
		})
	}

	return regions, nil
}

// parseConflictMarkers splits a file containing Git conflict markers into the
// base (HEAD) and proposed (incoming) portions.
func parseConflictMarkers(content string) (base, proposed string) {
	const (
		ours   = "<<<<<<< "
		mid    = "======="
		theirs = ">>>>>>> "
	)

	start := strings.Index(content, ours)
	if start == -1 {
		return "", ""
	}
	midIdx := strings.Index(content[start:], "\n"+mid+"\n")
	if midIdx == -1 {
		return "", ""
	}
	midIdx += start
	endIdx := strings.Index(content[midIdx+len("\n"+mid+"\n"):], theirs)
	if endIdx == -1 {
		return "", ""
	}
	endIdx += midIdx + len("\n"+mid+"\n")

	base = content[start+len(ours) : midIdx]
	proposed = content[midIdx+len("\n"+mid+"\n") : endIdx]
	return base, proposed
}

// CommitWorktree applies the worker's uncommitted patch to the canonical branch and commits.
// It honors the No Git Noise contract by never trusting worker commits; only the
// uncommitted diff is transferred into a detached temporary worktree.
func (m *worktreeManagerImpl) CommitWorktree(ctx context.Context, taskID string, wt *Worktree) error {
	diff, err := m.GetWorktreeDiff(ctx, wt)
	if err != nil {
		return err
	}
	if strings.TrimSpace(diff) == "" {
		return fmt.Errorf("no uncommitted changes in worktree; nothing to commit")
	}

	tempDir, err := m.provisionTempWorktree(ctx, m.canonicalBranch)
	if err != nil {
		return err
	}
	defer m.removeTempWorktree(ctx, tempDir)

	applyCmd := exec.CommandContext(ctx, "git", "apply")
	applyCmd.Dir = tempDir
	applyCmd.Stdin = strings.NewReader(diff)
	if out, err := applyCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("apply worker diff failed: %w, output: %s", err, string(out))
	}

	addCmd := exec.CommandContext(ctx, "git", "add", "-A")
	addCmd.Dir = tempDir
	if err := addCmd.Run(); err != nil {
		return fmt.Errorf("stage changes: %w", err)
	}

	commitMsg := fmt.Sprintf("Complete task %s\n\nApplied worker patch from isolated worktree.", taskID)
	commitCmd := exec.CommandContext(ctx, "git", "commit", "-m", commitMsg)
	commitCmd.Dir = tempDir
	if out, err := commitCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git commit failed: %w, output: %s", err, string(out))
	}

	newCommitCmd := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
	newCommitCmd.Dir = tempDir
	newCommitOut, err := newCommitCmd.Output()
	if err != nil {
		return fmt.Errorf("rev-parse new commit: %w", err)
	}
	newCommit := strings.TrimSpace(string(newCommitOut))

	// NOTE: Use update-ref rather than branch -f because Git refuses to force-update
	// a branch that is currently checked out in any worktree.
	updateCmd := exec.CommandContext(ctx, "git", "update-ref", "refs/heads/"+m.canonicalBranch, newCommit)
	updateCmd.Dir = m.repoPath
	if out, err := updateCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("update canonical branch %s: %w, output: %s", m.canonicalBranch, err, string(out))
	}

	// NOTE: After update-ref advances the branch ref, the main checkout's
	// working tree and index are stale (HEAD changed but files didn't).
	// Reset hard to sync them. This is safe because the orchestrator
	// already verified git.IsValid() (clean tree) before RunTask.
	resetCmd := exec.CommandContext(ctx, "git", "reset", "--hard", "HEAD")
	resetCmd.Dir = m.repoPath
	if out, err := resetCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("reset main working tree: %w, output: %s", err, string(out))
	}

	return nil
}

// DestroyWorktree deletes the ephemeral worktree directory.
func (m *worktreeManagerImpl) DestroyWorktree(ctx context.Context, wt *Worktree) error {
	cmd := exec.CommandContext(ctx, "git", "worktree", "remove", "--force", wt.Path)
	cmd.Dir = m.repoPath
	if err := cmd.Run(); err != nil {
		// NOTE: log the original error before falling back to manual cleanup + prune
		if removeErr := os.RemoveAll(wt.Path); removeErr != nil {
			log.Printf("failed to remove worktree path manually: %v", removeErr)
		}
		cmdPrune := exec.CommandContext(ctx, "git", "worktree", "prune", "--expire", "now")
		cmdPrune.Dir = m.repoPath
		if errPrune := cmdPrune.Run(); errPrune != nil {
			return errors.Join(
				fmt.Errorf("destroy worktree: %w", err),
				fmt.Errorf("prune: %w", errPrune),
			)
		}
	}
	return nil
}
