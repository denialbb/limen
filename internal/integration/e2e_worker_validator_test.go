package integration_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/denialbb/limen/internal/retrieval"
	"github.com/denialbb/limen/internal/state"
)

// TestEndToEnd_ProceedOnLowFloors (renamed from TestEndToEndWorkerValidatorLoop)
// forces PROCEED by setting coverage and confidence floors to 0, which bypasses
// the router cascade (ADR 0008). Uses a mock pi script that makes two attempts
// (fail then pass), then asserts COMMITTED and 2 validation decisions.
func TestEndToEnd_ProceedOnLowFloors(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	binaryPath := buildLimenBinary(t)

	// Create a real git repo.
	repoDir := setupTestRepo(t)

	// test.sh is the test suite.
	testScript := filepath.Join(repoDir, "test.sh")
	os.WriteFile(testScript, []byte(`#!/bin/bash
if ! grep -q "a + b" math.txt; then
	echo "Test failed"
	exit 1
fi
echo "Test passed"
exit 0
`), 0755)

	// Create initial corpus file.
	mathFile := filepath.Join(repoDir, "math.txt")
	os.WriteFile(mathFile, []byte("empty"), 0644)

	// Commit fixture.
	cmd := exec.Command("git", "add", ".")
	cmd.Dir = repoDir
	cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "fixture")
	cmd.Dir = repoDir
	cmd.Run()

	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "limen.db")

	taskID := "test-e2e-proceed"

	// Create a mock "pi" script that simulates a worker making two attempts.
	piScript := filepath.Join(t.TempDir(), "pi")
	piCode := `#!/bin/bash
# Mock pi that reads prompt, makes a bad change, waits for feedback, makes a good
# change. Output agent_end at the end.
DIR=$(pwd)
cat > $DIR/math.txt << 'INNER'
a - b
INNER

# Call ready-for-review (will fail).
` + binaryPath + ` ready-for-review --task-id ` + taskID + ` --db-path ` + dbPath + ` --summary "first attempt" > /dev/null

cat > $DIR/math.txt << 'INNER'
a + b
INNER

# Call ready-for-review (will pass).
` + binaryPath + ` ready-for-review --task-id ` + taskID + ` --db-path ` + dbPath + ` --summary "second attempt" > /dev/null

echo '{"type":"agent_end"}'
`
	os.WriteFile(piScript, []byte(piCode), 0755)

	// Add pi to PATH.
	os.Setenv("PATH", filepath.Dir(piScript)+":"+os.Getenv("PATH"))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// NOTE: --coverage-floor=0 --confidence-floor=0 bypass the router cascade
	// (ADR 0008), forcing PROCEED even with zero-coverage retrieval.
	cmdRun := exec.CommandContext(ctx, binaryPath, "run-task",
		"--task-id", taskID,
		"--prompt", "test prompt",
		"--db-path", dbPath,
		"--repo-path", repoDir,
		"--mock=false",
		"--worker-backend=pi",
		"--validator-cmd", "bash test.sh",
		"--coverage-floor=0",
		"--confidence-floor=0",
	)
	cmdRun.Dir = repoDir

	output, err := cmdRun.CombinedOutput()
	outputStr := string(output)

	if err != nil {
		t.Fatalf("limen run-task failed: %v\nOutput: %s", err, outputStr)
	}

	if !strings.Contains(outputStr, "Task completed with state: COMMITTED") {
		t.Errorf("Expected log output to contain 'COMMITTED', got:\n%s", outputStr)
	}

	// Assert store state.
	store, err := state.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("Failed to open store at %s: %v", dbPath, err)
	}
	defer store.Close()

	decisions, err := store.GetValidationDecisions(taskID)
	if err != nil {
		t.Fatalf("Failed to get validation decisions: %v", err)
	}
	if len(decisions) != 2 {
		t.Errorf("Expected 2 validation decisions, got %d", len(decisions))
	} else {
		if decisions[0].Pass {
			t.Errorf("Expected first decision to fail")
		}
		if !decisions[1].Pass {
			t.Errorf("Expected second decision to pass")
		}
	}
}

// TestEndToEnd_EscalateOnZeroCoverage tests the zero-coverage escape hatch from
// ADR 0005. With default floors, a prompt of "test prompt" against a corpus
// containing only "empty" yields coverage_hint == 0. The escape hatch fires on
// the first pass: no EXPAND, no worker run, immediate ESCALATE.
func TestEndToEnd_EscalateOnZeroCoverage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	binaryPath := buildLimenBinary(t)

	// Create a real git repo.
	repoDir := setupTestRepo(t)

	// Create a corpus file with content that does NOT match the query terms.
	mathFile := filepath.Join(repoDir, "math.txt")
	os.WriteFile(mathFile, []byte("empty"), 0644)

	// Commit fixture.
	cmd := exec.Command("git", "add", ".")
	cmd.Dir = repoDir
	cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "fixture")
	cmd.Dir = repoDir
	cmd.Run()

	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "limen.db")

	taskID := "test-e2e-escalate-zero-cov"

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// NOTE: Default floors (no --coverage-floor / --confidence-floor flags).
	// With "test prompt" vs "empty" corpus, coverage_hint == 0 triggers the
	// escape hatch on the first pass. No worker runs.
	cmdRun := exec.CommandContext(ctx, binaryPath, "run-task",
		"--task-id", taskID,
		"--prompt", "test prompt",
		"--db-path", dbPath,
		"--repo-path", repoDir,
		"--mock=false",
		"--worker-backend=cli",
		"--validator-cmd=true",
	)
	cmdRun.Dir = repoDir

	output, err := cmdRun.CombinedOutput()
	outputStr := string(output)

	// The command should exit with non-zero (RunTask returns err on ESCALATE).
	if err == nil {
		t.Errorf("Expected limen run-task to fail (escalated), but it succeeded.\nOutput: %s", outputStr)
	}

	// The run-task command returns non-zero on escalation (the orchestrator
	// returns ErrUnresolvableEntropy which is logged as "Task failed").
	if !strings.Contains(outputStr, "Task failed") {
		t.Errorf("Expected output to indicate task failure, got:\n%s", outputStr)
	}

	// Assert store state.
	store, err := state.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("Failed to open store at %s: %v", dbPath, err)
	}
	defer store.Close()

	task, err := store.GetTask(taskID)
	if err != nil {
		t.Fatalf("Failed to get task: %v", err)
	}
	if task.CurrentState != state.StateFailedEscalated {
		t.Errorf("Expected state FAILED_ESCALATED, got %s", task.CurrentState)
	}

	// Assert exactly one ContextBuilt event (first retrieve, then escape hatch
	// prevents any loop).
	transitions, err := store.GetStateTransitions(taskID)
	if err != nil {
		t.Fatalf("Failed to get state transitions: %v", err)
	}
	contextBuildingCount := 0
	workerRunningCount := 0
	for _, st := range transitions {
		if st.ToState == state.StateContextBuilding {
			contextBuildingCount++
		}
		if st.ToState == state.StateWorkerRunning {
			workerRunningCount++
		}
	}
	if contextBuildingCount != 1 {
		t.Errorf("Expected exactly 1 ContextBuilt event (ToState=CONTEXT_BUILDING), got %d", contextBuildingCount)
	}
	if workerRunningCount != 0 {
		t.Errorf("Expected WORKER_RUNNING never to be reached, got %d occurrences", workerRunningCount)
	}
}

// TestEndToEnd_ExpandUntilConvergence tests EXPAND behavior from ADR 0006/0008.
// A partially-matching prompt with coverage_floor set high forces the EXPAND loop
// to fire repeatedly until maxExpandIterations is exhausted. Asserts multiple
// ContextBuilt events and that the query_id in the final context_snapshot
// contains a "#>" iteration counter.
func TestEndToEnd_ExpandUntilConvergence(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	binaryPath := buildLimenBinary(t)

	// Create a real git repo.
	repoDir := setupTestRepo(t)

	// Create corpus with matching terms ("add", "a", "b", "math", "txt") plus
	// many other terms. The prompt has "frobnicate" which will never match,
	// keeping coverage_hint below the 0.99 floor.
	mathFile := filepath.Join(repoDir, "math.txt")
	corpusContent := "add a b alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi omicron pi rho sigma tau upsilon phi chi psi omega"
	os.WriteFile(mathFile, []byte(corpusContent), 0644)

	// Commit fixture.
	cmd := exec.Command("git", "add", ".")
	cmd.Dir = repoDir
	cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "fixture")
	cmd.Dir = repoDir
	cmd.Run()

	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "limen.db")

	taskID := "test-e2e-expand-converge"

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// NOTE: --coverage-floor=0.99 forces EXPAND since the prompt partially
	// matches the corpus ("add"/"a"/"b"/"math" match, "frobnicate" doesn't).
	// The loop exhausts maxExpandIterations=5 and ends in FAILED_ESCALATED.
	cmdRun := exec.CommandContext(ctx, binaryPath, "run-task",
		"--task-id", taskID,
		"--prompt", "add a and b in math.txt then frobnicate",
		"--db-path", dbPath,
		"--repo-path", repoDir,
		"--mock=false",
		"--worker-backend=cli",
		"--validator-cmd=true",
		"--coverage-floor=0.99",
		"--confidence-floor=0",
	)
	cmdRun.Dir = repoDir

	output, err := cmdRun.CombinedOutput()
	outputStr := string(output)

	if err == nil {
		t.Errorf("Expected limen run-task to fail (EXPAND exhausted), but it succeeded.\nOutput: %s", outputStr)
	}

	if !strings.Contains(outputStr, "Task failed") {
		t.Errorf("Expected output to indicate task failure, got:\n%s", outputStr)
	}

	// Assert store state.
	store, err := state.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("Failed to open store at %s: %v", dbPath, err)
	}
	defer store.Close()

	task, err := store.GetTask(taskID)
	if err != nil {
		t.Fatalf("Failed to get task: %v", err)
	}
	if task.CurrentState != state.StateFailedEscalated {
		t.Errorf("Expected state FAILED_ESCALATED, got %s", task.CurrentState)
	}

	// Assert multiple ContextBuilt events (EXPAND loop ran).
	transitions, err := store.GetStateTransitions(taskID)
	if err != nil {
		t.Fatalf("Failed to get state transitions: %v", err)
	}
	contextBuildingCount := 0
	for _, st := range transitions {
		if st.ToState == state.StateContextBuilding {
			contextBuildingCount++
		}
	}
	if contextBuildingCount <= 1 {
		t.Errorf("Expected more than 1 ContextBuilt event (EXPAND loop), got %d", contextBuildingCount)
	}

	// Assert the query_id in the final context_snapshot has a #>0 iteration
	// counter (primary assertion from ADR 0008).
	manifest, err := retrieval.ParseManifest(task.ContextSnapshot)
	if err != nil {
		t.Fatalf("Failed to parse context snapshot: %v\nRaw: %s", err, task.ContextSnapshot)
	}
	if !strings.Contains(manifest.QueryID, "#") {
		t.Errorf("Expected QueryID to contain '#' with iteration counter, got %q", manifest.QueryID)
	}
	// The iteration counter should be > 0 (EXPAND was called at least once).
	hashIdx := strings.Index(manifest.QueryID, "#")
	if hashIdx < 0 || hashIdx+1 >= len(manifest.QueryID) {
		t.Errorf("Could not extract iteration counter from QueryID %q", manifest.QueryID)
	} else {
		iterStr := manifest.QueryID[hashIdx+1:]
		// iterStr is e.g. "0" for first pass, "5" for exhausted. After EXPAND
		// exhaustion at maxExpandIterations=5, the final retrieval is at
		// iteration 5 (expandCount exceeded → last retrieval was at iter 5
		// before exhaustion). We just need > 0.
		if iterStr == "0" {
			t.Errorf("Expected QueryID iteration counter > 0, got iter=%q from QueryID=%q", iterStr, manifest.QueryID)
		}
	}

	// Assert worker was never called.
	workerRunningCount := 0
	for _, st := range transitions {
		if st.ToState == state.StateWorkerRunning {
			workerRunningCount++
		}
	}
	if workerRunningCount != 0 {
		t.Errorf("Expected WORKER_RUNNING never to be reached, got %d occurrences", workerRunningCount)
	}
}
