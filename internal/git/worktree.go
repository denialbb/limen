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
	"time"
)

// worktreeCleanupTimeout bounds the cleanup subprocesses. Cleanup gets its own
// context because it is usually triggered BY a cancellation: reusing the
// caller's cancelled context would kill every command before it removed
// anything, which is the failure mode this whole path exists to prevent.
const worktreeCleanupTimeout = 30 * time.Second

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
	// IsValid reports whether the repository is in a valid state for operations:
	// it is inside a git worktree, has no uncommitted tracked changes, and passes fsck.
	IsValid(ctx context.Context) (bool, error)
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
	// ProvisionThrowawayWorktree creates a detached worktree with the given patch applied.
	ProvisionThrowawayWorktree(ctx context.Context, patch string) (*Worktree, error)
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

// IsValid verifies the repository is inside a git worktree and has no uncommitted
// changes or known integrity issues.
func (m *worktreeManagerImpl) IsValid(ctx context.Context) (bool, error) {
	cmdDir := exec.CommandContext(ctx, "git", "rev-parse", "--git-dir")
	cmdDir.Dir = m.repoPath
	if out, err := cmdDir.CombinedOutput(); err != nil {
		return false, fmt.Errorf("not a git repository: %w, output: %s", err, string(out))
	}

	cmdStatus := exec.CommandContext(ctx, "git", "status", "--porcelain", "--untracked-files=no")
	cmdStatus.Dir = m.repoPath
	out, err := cmdStatus.Output()
	if err != nil {
		return false, fmt.Errorf("git status failed: %w", err)
	}
	if strings.TrimSpace(string(out)) != "" {
		return false, nil
	}

	cmdFsck := exec.CommandContext(ctx, "git", "fsck", "--full")
	cmdFsck.Dir = m.repoPath
	if err := cmdFsck.Run(); err != nil {
		return false, nil
	}

	return true, nil
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
		// A cancelled context kills `git worktree add` partway through, and git
		// holds a lock on the worktree for the whole of its initialization. The
		// half-built entry it leaves behind is locked, so neither remove nor
		// prune will touch it, and it keeps its branch name reserved — a retry
		// of the same task then fails with "already used by worktree". Nothing
		// else cleans this up, so do it here before returning.
		if ctxErr := ctx.Err(); ctxErr != nil {
			if cleanupErr := m.purgeWorktree(absPath); cleanupErr != nil {
				// The cancellation is the caller's answer; a cleanup failure is
				// secondary but must not vanish.
				log.Printf("cleanup after cancelled worktree provision at %s: %v", absPath, cleanupErr)
			}
			return nil, fmt.Errorf("git worktree add cancelled: %w", ctxErr)
		}
		return nil, fmt.Errorf("git worktree add failed: %w, output: %s", err, string(out))
	}

	return &Worktree{
		Path:       absPath,
		Branch:     branchName,
		BaseCommit: baseCommit,
	}, nil
}

// purgeWorktree removes a worktree from both the filesystem and git's
// administrative area, and verifies the repository is actually clean
// afterwards.
//
// The escalation exists because a worktree can be in one of three states: a
// clean checkout (a plain remove suffices), a dirty one (needs --force), or a
// locked half-initialized one left by an interrupted `git worktree add`, which
// refuses both until the lock is dropped.
//
// The verification exists because git's exit codes do not report this: `git
// worktree prune` exits 0 whether it pruned an entry or skipped a locked one.
// Trusting that status is what let a dangling worktree accumulate silently.
func (m *worktreeManagerImpl) purgeWorktree(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), worktreeCleanupTimeout)
	defer cancel()

	// Drop the initialization lock first: while it stands, every removal below
	// is a no-op. Worktrees that were never locked fail this harmlessly.
	m.gitBestEffort(ctx, "worktree", "unlock", path)
	m.gitBestEffort(ctx, "worktree", "remove", "--force", path)

	var removeErr error
	if err := os.RemoveAll(path); err != nil {
		removeErr = fmt.Errorf("remove worktree directory %s: %w", path, err)
	}

	// Prune whatever administrative entry git left pointing at a directory that
	// no longer exists.
	m.gitBestEffort(ctx, "worktree", "prune", "--expire", "now")

	return errors.Join(removeErr, m.verifyWorktreeGone(ctx, path))
}

// gitBestEffort runs a git command in the repository and discards its result.
// Every caller is one step of an escalating cleanup, and each step may
// legitimately fail: unlocking a worktree that was never locked, or removing
// one git has already forgotten.
func (m *worktreeManagerImpl) gitBestEffort(ctx context.Context, args ...string) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = m.repoPath
	_ = cmd.Run()
}

// verifyWorktreeGone reports an error if the directory survived cleanup or if
// git still has the worktree registered. This is the check that makes a failed
// cleanup loud instead of silent.
func (m *worktreeManagerImpl) verifyWorktreeGone(ctx context.Context, path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("worktree directory %s still exists after cleanup", path)
	}

	cmd := exec.CommandContext(ctx, "git", "worktree", "list", "--porcelain")
	cmd.Dir = m.repoPath
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("verify worktree removal: %w", err)
	}
	want := canonicalPath(path)
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		listed, ok := strings.CutPrefix(line, "worktree ")
		if !ok {
			continue
		}
		if canonicalPath(strings.TrimSpace(listed)) == want {
			return fmt.Errorf("git still lists worktree %s after cleanup", path)
		}
	}
	return nil
}

// canonicalPath resolves symlinks so a path from git and a path from the caller
// compare equal. EvalSymlinks fails on a path that no longer exists — the
// expected case after a successful cleanup — so it falls back to a lexical
// clean, which is consistent for both sides of the comparison.
func canonicalPath(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(p)
}

// getWorktreeDiff returns the worker's uncommitted changes relative to HEAD.
// Under the No Git Noise contract, HEAD equals BaseCommit, so this diff is the
// complete proposed patch. New untracked files are included by marking them with
// intent-to-add before diffing.
func (m *worktreeManagerImpl) GetWorktreeDiff(ctx context.Context, wt *Worktree) (string, error) {
	resetCmd := exec.CommandContext(ctx, "git", "reset")
	resetCmd.Dir = wt.Path
	if err := resetCmd.Run(); err != nil {
		return "", fmt.Errorf("git reset failed: %w", err)
	}

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

// ProvisionThrowawayWorktree creates a detached worktree with the given patch applied.
func (m *worktreeManagerImpl) ProvisionThrowawayWorktree(ctx context.Context, patch string) (*Worktree, error) {
	tempDir, err := m.provisionTempWorktree(ctx, m.canonicalBranch)
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(patch) != "" {
		applyCmd := exec.CommandContext(ctx, "git", "apply")
		applyCmd.Dir = tempDir
		applyCmd.Stdin = strings.NewReader(patch)
		if out, err := applyCmd.CombinedOutput(); err != nil {
			m.removeTempWorktree(ctx, tempDir)
			return nil, fmt.Errorf("apply worker diff failed: %w, output: %s", err, string(out))
		}
	}

	return &Worktree{
		Path:       tempDir,
		Branch:     "",
		BaseCommit: m.canonicalBranch,
	}, nil
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

// DestroyWorktree deletes the ephemeral worktree directory and removes git's
// record of it.
//
// Removal is attempted politely first and escalated only if that fails, so the
// ordinary case stays a single subprocess. The method returns an error when the
// worktree is still on disk or still registered once every escalation has run:
// a cleanup failure that reports success leaves the worktree's branch name
// reserved, and the next provision of the same task fails with "already used by
// worktree" — far from the code that actually failed.
func (m *worktreeManagerImpl) DestroyWorktree(ctx context.Context, wt *Worktree) error {
	// --force is the ordinary path, not an escalation: a worker's worktree is
	// dirty by definition, and a plain remove refuses a dirty worktree. Unlike
	// prune, remove reports honestly — a zero exit means the worktree and its
	// administrative entry are both gone — so this needs no verification.
	cmd := exec.CommandContext(ctx, "git", "worktree", "remove", "--force", wt.Path)
	cmd.Dir = m.repoPath
	if err := cmd.Run(); err == nil {
		return nil
	}

	// Anything remove refuses: a locked half-initialized entry, a directory
	// already deleted out from under git, a cancelled context.
	if err := m.purgeWorktree(wt.Path); err != nil {
		return fmt.Errorf("destroy worktree %s: %w", wt.Path, err)
	}
	return nil
}
