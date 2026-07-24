package retrieval

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

const (
	maxCorpusFiles = 10_000
	// binaryCheckLen is the number of bytes to inspect for null bytes when
	// deciding whether a file is binary.
	binaryCheckLen = 512
)

// GitCorpusLoader implements CorpusLoader by reading a git repository's tracked
// files via git ls-files and git show HEAD:<path>.
type GitCorpusLoader struct {
	// RepoPath is the default git repository root. Used when the Load method is
	// called with an empty repoPath parameter.
	RepoPath string
}

// Load implements CorpusLoader. It runs git ls-files to enumerate tracked files
// and then reads each file's HEAD content with git show. Empty, binary, and
// unreadable files are silently skipped (best-effort). The total number of files
// loaded is capped at maxCorpusFiles.
func (g GitCorpusLoader) Load(ctx context.Context, repoPath string) (Corpus, error) {
	if repoPath == "" {
		repoPath = g.RepoPath
	}
	if repoPath == "" {
		return nil, fmt.Errorf("corpus loader: empty repo path")
	}

	// 1. Enumerate tracked files.
	paths, err := listTrackedFiles(ctx, repoPath)
	if err != nil {
		return nil, fmt.Errorf("corpus loader: list tracked files: %w", err)
	}
	if len(paths) > maxCorpusFiles {
		paths = paths[:maxCorpusFiles]
	}

	// 2. Load each file's HEAD content.
	corpus := make(Corpus, 0, len(paths))
	for _, p := range paths {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		content, err := showFileAtHEAD(ctx, repoPath, p)
		if err != nil {
			// Best-effort: skip files we cannot read.
			continue
		}
		if len(content) == 0 || isBinary(content) {
			continue
		}
		corpus = append(corpus, File{Path: p, Content: content})
	}
	return corpus, nil
}

// listTrackedFiles runs git ls-files and returns the trimmed, non-empty paths.
func listTrackedFiles(ctx context.Context, repoPath string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "ls-files")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	scanner := bufio.NewScanner(bytes.NewReader(out))
	var paths []string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		paths = append(paths, line)
	}
	return paths, scanner.Err()
}

// showFileAtHEAD runs git show HEAD:<path> and returns the file content.
func showFileAtHEAD(ctx context.Context, repoPath, filePath string) ([]byte, error) {
	ref := fmt.Sprintf("HEAD:%s", filePath)
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "show", ref)
	return cmd.Output()
}

// isBinary checks the first binaryCheckLen bytes of data for a null byte,
// which is a strong signal the file is binary.
func isBinary(data []byte) bool {
	n := len(data)
	if n > binaryCheckLen {
		n = binaryCheckLen
	}
	return bytes.IndexByte(data[:n], 0) >= 0
}
