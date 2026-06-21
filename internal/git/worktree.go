// Package git provides Go Core Git Worktree Manager stubs for Limen.
package git

import (
	"context"
	"errors"
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

// worktreeManagerImpl implements the WorktreeManager interface.
type worktreeManagerImpl struct {
	// TODO: Add dependencies like database connections or Git CLI wrappers.
}

// NewWorktreeManager creates a new instance of the WorktreeManager.
func NewWorktreeManager() WorktreeManager {
	return &worktreeManagerImpl{}
}

// ProvisionWorktree creates an isolated environment via `git worktree add`.
func (m *worktreeManagerImpl) ProvisionWorktree(ctx context.Context, baseCommit, branchName, path string) (*Worktree, error) {
	// TODO: Implement `git worktree add` provisioning logic.
	return nil, errors.New("not implemented")
}

// CheckForConflicts detects if a rebase/merge conflict occurs.
func (m *worktreeManagerImpl) CheckForConflicts(ctx context.Context, wt *Worktree) (bool, error) {
	// TODO: Implement conflict detection logic.
	return false, errors.New("not implemented")
}

// ExtractConflictRegions extracts conflicting diff regions.
func (m *worktreeManagerImpl) ExtractConflictRegions(ctx context.Context, wt *Worktree) ([]ConflictRegion, error) {
	// TODO: Implement conflict region extraction logic.
	return nil, errors.New("not implemented")
}

// DestroyWorktree deletes the ephemeral worktree directory.
func (m *worktreeManagerImpl) DestroyWorktree(ctx context.Context, wt *Worktree) error {
	// TODO: Implement destruction logic (remove directory, `git worktree prune`).
	return errors.New("not implemented")
}
