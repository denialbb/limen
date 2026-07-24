package integration_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/denialbb/limen/internal/state"
)

// runGit runs a git command in dir, failing the test on error.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// runLimenTask runs `limen run-task` against the fixture with the claude worker
// and agy validator backends, forcing PROCEED via zero floors (ADR 0008), and
// returns the combined output.
func runLimenTask(ctx context.Context, binaryPath, repoDir, taskID, dbPath string) (string, error) {
	cmd := exec.CommandContext(ctx, binaryPath, "run-task",
		"--task-id", taskID,
		"--prompt", "make math.txt contain a + b",
		"--db-path", dbPath,
		"--repo-path", repoDir,
		"--mock=false",
		"--worker-backend=claude",
		"--validator-backend=agy",
		"--coverage-floor=0",
		"--confidence-floor=0",
	)
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestEndToEnd_ClaudeWorker_AgyValidator proves the multi-driver seam (issue
// 015): a fixture task reaches COMMITTED with --worker-backend=claude and
// --validator-backend=agy, using fake CLIs so it runs in CI without real
// agents or tokens. The fake claude worker makes a wrong change, calls
// ready-for-review, is rejected, revises, and calls again; the fake agy
// validator greps the throwaway worktree (a real check of the applied diff) and
// emits a LIMEN_VERDICT sentinel each round. Rejection path returns feedback and
// the worker revises; the second round passes and the loop commits.
func TestEndToEnd_ClaudeWorker_AgyValidator(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	binaryPath := buildLimenBinary(t)
	repoDir := setupTestRepo(t)

	// Fixture corpus file the worker must edit.
	mathFile := filepath.Join(repoDir, "math.txt")
	if err := os.WriteFile(mathFile, []byte("empty"), 0644); err != nil {
		t.Fatalf("seed math.txt: %v", err)
	}
	runGit(t, repoDir, "add", ".")
	runGit(t, repoDir, "commit", "-m", "fixture")

	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "limen.db")
	taskID := "test-e2e-claude-agy"

	// Fake CLIs live in one dir prepended to PATH so `claude` and `agy` resolve
	// to these scripts (real binaries are shadowed).
	fakeBin := t.TempDir()

	// Fake claude worker: RPC family. Drains the stream-json prompt on stdin,
	// makes a wrong change, calls ready-for-review (rejected), revises, calls
	// again (approved), then emits the stream-json `result` end event.
	claudeScript := "#!/bin/bash\n" +
		"cat >/dev/null &\n" +
		"DIR=$(pwd)\n" +
		"printf 'a - b\\n' > \"$DIR/math.txt\"\n" +
		binaryPath + " ready-for-review --task-id " + taskID + " --db-path " + dbPath + " --summary 'first attempt' >/dev/null 2>&1\n" +
		"printf 'a + b\\n' > \"$DIR/math.txt\"\n" +
		binaryPath + " ready-for-review --task-id " + taskID + " --db-path " + dbPath + " --summary 'second attempt' >/dev/null 2>&1\n" +
		"echo '{\"type\":\"result\",\"subtype\":\"success\",\"result\":\"done\"}'\n"
	if err := os.WriteFile(filepath.Join(fakeBin, "claude"), []byte(claudeScript), 0755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}

	// Fake agy validator: greps the throwaway worktree (the applied diff) and
	// emits the verdict sentinel — a genuine check of the artifact, not a canned
	// answer.
	agyScript := "#!/bin/bash\n" +
		"if grep -q 'a + b' math.txt 2>/dev/null; then\n" +
		"  echo 'LIMEN_VERDICT: {\"passes\":true,\"feedback\":\"math.txt contains a + b\"}'\n" +
		"else\n" +
		"  echo 'LIMEN_VERDICT: {\"passes\":false,\"feedback\":\"math.txt must contain a + b\"}'\n" +
		"fi\n"
	if err := os.WriteFile(filepath.Join(fakeBin, "agy"), []byte(agyScript), 0755); err != nil {
		t.Fatalf("write fake agy: %v", err)
	}

	origPath := os.Getenv("PATH")
	os.Setenv("PATH", fakeBin+string(os.PathListSeparator)+origPath)
	t.Cleanup(func() { os.Setenv("PATH", origPath) })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	out, err := runLimenTask(ctx, binaryPath, repoDir, taskID, dbPath)
	if err != nil {
		t.Fatalf("limen run-task failed: %v\nOutput: %s", err, out)
	}
	if !strings.Contains(out, "Task completed with state: COMMITTED") {
		t.Fatalf("expected COMMITTED, got:\n%s", out)
	}

	store, err := state.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	decisions, err := store.GetValidationDecisions(taskID)
	if err != nil {
		t.Fatalf("get validation decisions: %v", err)
	}
	if len(decisions) != 2 {
		t.Fatalf("expected 2 validation decisions (reject then pass), got %d", len(decisions))
	}
	if decisions[0].Pass {
		t.Errorf("expected first decision to reject")
	}
	if !decisions[1].Pass {
		t.Errorf("expected second decision to pass")
	}
}
