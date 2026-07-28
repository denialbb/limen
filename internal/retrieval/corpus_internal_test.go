package retrieval

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestIsBinaryClampsToBinaryCheckLen pins the scan window: isBinary inspects
// only the first binaryCheckLen bytes, so a null byte past that window does not
// make the file binary. Without the clamp the last case would flip to true,
// which is what makes this table a real guard on the length arithmetic.
func TestIsBinaryClampsToBinaryCheckLen(t *testing.T) {
	// A null byte just past the scan window, in data longer than the window.
	pastWindow := bytes.Repeat([]byte("a"), binaryCheckLen+64)
	pastWindow[binaryCheckLen+10] = 0

	// A null byte just inside the scan window, same oversized data.
	insideWindow := bytes.Repeat([]byte("a"), binaryCheckLen+64)
	insideWindow[binaryCheckLen-1] = 0

	// The cases above are written in terms of binaryCheckLen, so they move with
	// the constant and cannot pin its value. These two use absolute offsets:
	// together they assert the window is exactly the first 512 bytes, no more
	// and no less. A NUL at index 511 is the last byte inside the window; the
	// one at index 512 is the first byte outside it.
	lastInside := bytes.Repeat([]byte("a"), 1024)
	lastInside[511] = 0
	firstOutside := bytes.Repeat([]byte("a"), 1024)
	firstOutside[512] = 0

	// A NUL at the very first byte: the scan reports the index of the match,
	// and index 0 is a match like any other.
	leadingNul := append([]byte{0}, bytes.Repeat([]byte("a"), 16)...)

	tests := []struct {
		name string
		data []byte
		want bool
	}{
		{"empty data is not binary", nil, false},
		{"short text is not binary", []byte("package main\n"), false},
		{"null inside the first binaryCheckLen bytes is binary", insideWindow, true},
		{"null past binaryCheckLen is not binary (clamped scan)", pastWindow, false},
		{"null at byte 511 is inside the window", lastInside, true},
		{"null at byte 512 is outside the window", firstOutside, false},
		{"null at byte 0 is binary", leadingNul, true},
		{"single null byte is binary", []byte{0}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isBinary(tc.data); got != tc.want {
				t.Fatalf("isBinary(len=%d) = %v, want %v", len(tc.data), got, tc.want)
			}
		})
	}
}

// setupCorpusRepo builds a git repository exercising every branch of the
// loader's per-file filter: a committed text file, a nested one, a one-byte
// file (the smallest thing that is still content), a binary file, an empty
// file, a file staged but never committed (so `git show HEAD:` fails for it),
// and an untracked file that must not be enumerated at all.
func setupCorpusRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	write := func(name string, content []byte) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", name, err)
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	run("init", "-b", "main")
	run("config", "user.name", "Test User")
	run("config", "user.email", "test@example.com")

	write("a.go", []byte("package main\n"))
	write("sub/c.go", []byte("package sub\n"))
	write("tiny.txt", []byte("x"))
	write("logo.bin", []byte{'P', 'N', 'G', 0, 1, 2})
	write("empty.txt", nil)
	run("add", "a.go", "sub/c.go", "tiny.txt", "logo.bin", "empty.txt")
	run("commit", "-m", "initial commit")

	// Staged but never committed: ls-files lists it, `git show HEAD:` cannot
	// resolve it, so the loader must skip it rather than fail the whole load.
	write("staged-only.txt", []byte("not in HEAD\n"))
	run("add", "staged-only.txt")

	// Never added: must not appear at all.
	write("untracked.txt", []byte("invisible\n"))

	return dir
}

// corpusPaths lists the loaded paths in load order.
func corpusPaths(c Corpus) []string {
	out := make([]string, len(c))
	for i, f := range c {
		out[i] = f.Path
	}
	return out
}

// TestGitCorpusLoader_LoadsTrackedTextFiles drives the real loader against a
// real repository. It is the only test of the corpus loader's own logic: which
// files are enumerated, which are filtered out, and what content each carries.
func TestGitCorpusLoader_LoadsTrackedTextFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir := setupCorpusRepo(t)

	corpus, err := GitCorpusLoader{}.Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// git ls-files reports paths sorted, so the load order is deterministic.
	want := []string{"a.go", "sub/c.go", "tiny.txt"}
	if got := corpusPaths(corpus); !equalStrings(got, want) {
		t.Fatalf("loaded %v, want %v", got, want)
	}

	byPath := map[string]string{}
	for _, f := range corpus {
		byPath[f.Path] = string(f.Content)
	}
	if got := byPath["a.go"]; got != "package main\n" {
		t.Errorf("a.go content = %q, want %q", got, "package main\n")
	}
	if got := byPath["tiny.txt"]; got != "x" {
		t.Errorf("tiny.txt content = %q, want %q; a one-byte file is still content", got, "x")
	}
}

// TestGitCorpusLoader_RepoPathResolution pins how the repository root is
// chosen: the explicit argument wins, an empty argument falls back to the
// configured RepoPath, and having neither is an error rather than a silent
// load of the process's working directory.
func TestGitCorpusLoader_RepoPathResolution(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir := setupCorpusRepo(t)
	ctx := context.Background()

	t.Run("explicit argument wins", func(t *testing.T) {
		// The configured RepoPath is deliberately invalid: if it were used,
		// the load would fail.
		corpus, err := GitCorpusLoader{RepoPath: filepath.Join(dir, "does-not-exist")}.Load(ctx, dir)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if len(corpus) == 0 {
			t.Error("explicit repo path was ignored")
		}
	})

	t.Run("empty argument falls back to RepoPath", func(t *testing.T) {
		corpus, err := GitCorpusLoader{RepoPath: dir}.Load(ctx, "")
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if len(corpus) == 0 {
			t.Error("configured RepoPath was not used")
		}
	})

	t.Run("no path at all is an error", func(t *testing.T) {
		if _, err := (GitCorpusLoader{}).Load(ctx, ""); err == nil {
			t.Error("expected an error when neither a path argument nor RepoPath is set")
		}
	})

	t.Run("non-repository is an error", func(t *testing.T) {
		if _, err := (GitCorpusLoader{}).Load(ctx, t.TempDir()); err == nil {
			t.Error("expected an error for a directory that is not a git repository")
		}
	})
}

// TestGitCorpusLoader_HonorsContextCancellation asserts a cancelled context
// stops the load and surfaces the cancellation, rather than spending a
// subprocess per tracked file on work nobody is waiting for.
func TestGitCorpusLoader_HonorsContextCancellation(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir := setupCorpusRepo(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := (GitCorpusLoader{}).Load(ctx, dir); err == nil {
		t.Error("expected an error from a cancelled context")
	}
}

// TestListTrackedFiles_ReturnsTrimmedNonEmptyPaths covers the enumerator on its
// own: every tracked path is reported, including the staged-only one that the
// content step later discards.
func TestListTrackedFiles_ReturnsTrimmedNonEmptyPaths(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir := setupCorpusRepo(t)

	paths, err := listTrackedFiles(context.Background(), dir)
	if err != nil {
		t.Fatalf("listTrackedFiles: %v", err)
	}
	want := []string{"a.go", "empty.txt", "logo.bin", "staged-only.txt", "sub/c.go", "tiny.txt"}
	if !equalStrings(paths, want) {
		t.Fatalf("listTrackedFiles = %v, want %v", paths, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
