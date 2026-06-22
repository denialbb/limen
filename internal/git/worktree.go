// Package git provides Go Core Git Worktree Manager stubs for Limen.
package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// NOTE: This file contains stubs for the Git Worktree Manager.

// Worktree represents an isolated Git worktree environment.
type Worktree struct {
	// Path is the absolute filesystem path to the provisioned worktree.
	Path string
	// Branch is the name of the branch checked out in the worktree.
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
	// CheckForConflicts detects if a rebase/merge conflict occurs when attempting to commit an approved task.
	CheckForConflicts(ctx context.Context, wt *Worktree) (bool, error)
	// ExtractConflictRegions extracts conflicting diff regions if a conflict is detected.
	ExtractConflictRegions(ctx context.Context, wt *Worktree) ([]ConflictRegion, error)
	// DestroyWorktree deletes the ephemeral worktree directory and prunes it from Git.
	DestroyWorktree(ctx context.Context, wt *Worktree) error
}

type worktreeManagerImpl struct {
	repoPath string
}

// NewWorktreeManager creates a new instance of the WorktreeManager.
func NewWorktreeManager(repoPath string) WorktreeManager {
	return &worktreeManagerImpl{
		repoPath: repoPath,
	}
}

// ProvisionWorktree creates an isolated environment via `git worktree add`.
func (m *worktreeManagerImpl) ProvisionWorktree(ctx context.Context, baseCommit, branchName, path string) (*Worktree, error) {
	var cmd *exec.Cmd
	if branchName != "" {
		cmd = exec.CommandContext(ctx, "git", "worktree", "add", "-b", branchName, path, baseCommit)
	} else {
		cmd = exec.CommandContext(ctx, "git", "worktree", "add", path, baseCommit)
	}
	cmd.Dir = m.repoPath

	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("git worktree add failed: %w, output: %s", err, string(out))
	}

	return &Worktree{
		Path:       path,
		Branch:     branchName,
		BaseCommit: baseCommit,
	}, nil
}

// CheckForConflicts detects if a rebase/merge conflict occurs.
func (m *worktreeManagerImpl) CheckForConflicts(ctx context.Context, wt *Worktree) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "ls-files", "--unmerged")
	cmd.Dir = wt.Path
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("git ls-files failed: %w", err)
	}
	return len(strings.TrimSpace(string(out))) > 0, nil
}

// ExtractConflictRegions extracts conflicting diff regions.
func (m *worktreeManagerImpl) ExtractConflictRegions(ctx context.Context, wt *Worktree) ([]ConflictRegion, error) {
	cmd := exec.CommandContext(ctx, "git", "diff", "--name-only", "--diff-filter=U")
	cmd.Dir = wt.Path
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

		filePath := filepath.Join(wt.Path, f)
		contentBytes, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("failed to read conflicted file %s: %w", f, err)
		}

		fileRegions := parseConflictRegions(f, string(contentBytes))
		regions = append(regions, fileRegions...)
	}

	return regions, nil
}

func parseConflictRegions(filePath string, content string) []ConflictRegion {
	var regions []ConflictRegion
	lines := strings.Split(content, "\n")

	inConflict := false
	inBase := false
	inProposed := false

	var baseLines []string
	var proposedLines []string

	for _, line := range lines {
		if strings.HasPrefix(line, "<<<<<<<") {
			inConflict = true
			inBase = true
			inProposed = false
			baseLines = nil
			proposedLines = nil
			continue
		}
		if strings.HasPrefix(line, "=======") && inConflict {
			inBase = false
			inProposed = true
			continue
		}
		if strings.HasPrefix(line, ">>>>>>>") && inConflict {
			regions = append(regions, ConflictRegion{
				FilePath:     filePath,
				BaseDiff:     strings.Join(baseLines, "\n"),
				ProposedDiff: strings.Join(proposedLines, "\n"),
			})
			inConflict = false
			inBase = false
			inProposed = false
			continue
		}

		if inBase {
			baseLines = append(baseLines, line)
		} else if inProposed {
			proposedLines = append(proposedLines, line)
		}
	}
	return regions
}

// DestroyWorktree deletes the ephemeral worktree directory.
func (m *worktreeManagerImpl) DestroyWorktree(ctx context.Context, wt *Worktree) error {
	cmd := exec.CommandContext(ctx, "git", "worktree", "remove", "--force", wt.Path)
	cmd.Dir = m.repoPath
	if err := cmd.Run(); err != nil {
		// Fallback for older git versions or if path is missing
		os.RemoveAll(wt.Path)
		cmdPrune := exec.CommandContext(ctx, "git", "worktree", "prune")
		cmdPrune.Dir = m.repoPath
		if errPrune := cmdPrune.Run(); errPrune != nil {
			return fmt.Errorf("failed to destroy worktree: %v (prune error: %w)", err, errPrune)
		}
	}
	return nil
}
