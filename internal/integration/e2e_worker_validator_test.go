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

func TestEndToEndWorkerValidatorLoop(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	binaryPath := buildLimenBinary(t)

	// Create a real git repo
	repoDir := setupTestRepo(t)

	// Write fixture files
	// test_add.sh is the test suite
	testScript := filepath.Join(repoDir, "test.sh")
	os.WriteFile(testScript, []byte(`#!/bin/bash
if ! grep -q "a + b" math.txt; then
	echo "Test failed"
	exit 1
fi
echo "Test passed"
exit 0
`), 0755)
	
	// Create initial file
	mathFile := filepath.Join(repoDir, "math.txt")
	os.WriteFile(mathFile, []byte("empty"), 0644)

	// Commit fixture
	cmd := exec.Command("git", "add", ".")
	cmd.Dir = repoDir
	cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "fixture")
	cmd.Dir = repoDir
	cmd.Run()

	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "limen.db")

	taskID := "test-e2e-loop"

	// Create a fake "pi" script
	piScript := filepath.Join(t.TempDir(), "pi")
	piCode := `#!/bin/bash
# Mock pi that reads prompt, makes a bad change, waits for feedback, makes a good change.
# Output agent_end at the end.
DIR=$(pwd)
cat > $DIR/math.txt << 'INNER'
a - b
INNER

# Call ready-for-review (will fail)
` + binaryPath + ` ready-for-review --task-id ` + taskID + ` --db-path ` + dbPath + ` --summary "first attempt" > /dev/null

cat > $DIR/math.txt << 'INNER'
a + b
INNER

# Call ready-for-review (will pass)
` + binaryPath + ` ready-for-review --task-id ` + taskID + ` --db-path ` + dbPath + ` --summary "second attempt" > /dev/null

echo '{"type":"agent_end"}'
`
	os.WriteFile(piScript, []byte(piCode), 0755)

	// Add pi to PATH
	os.Setenv("PATH", filepath.Dir(piScript)+":"+os.Getenv("PATH"))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmdRun := exec.CommandContext(ctx, binaryPath, "run-task",
		"--task-id", taskID,
		"--db-path", dbPath,
		"--repo-path", repoDir,
		"--mock=false",
		"--worker-backend=pi",
		"--validator-cmd", "bash test.sh",
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

	// Assert store state
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
