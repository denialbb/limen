package remote

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/denialbb/limen/internal/bus"
	"github.com/denialbb/limen/internal/git"
	"github.com/denialbb/limen/internal/state"
)

func TestDecodeAgyEvent(t *testing.T) {
	now := time.Unix(1700000000, 0)
	// agy emits plain text with no event stream: every line decodes to nothing
	// and never signals end (one-shot family; EOF ends the run).
	for _, line := range []string{"done", "", `{"looks":"like json but ignored"}`, "multi word line"} {
		res := decodeAgyEvent(line, "task-a", now)
		if res.end {
			t.Fatalf("agy is one-shot; end must never be true (line %q)", line)
		}
		if len(res.events) != 0 {
			t.Fatalf("agy emits no events; got %#v for line %q", res.events, line)
		}
	}
}

func TestAgyWorkerInjectedCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake shell script fixture requires a POSIX shell")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-agy.sh")
	// agy prints its final message as plain text and exits.
	fixture := `#!/bin/sh
echo 'I inspected the code and made the change.'
echo 'done'
`
	if err := os.WriteFile(script, []byte(fixture), 0755); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	w := newAgentWorkerCmd(agyDialect(), []string{"/bin/sh", script}, defaultOptions())
	rec := bus.NewRecorderEmitter()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := w.ProduceSolution(ctx, &state.Task{ID: "task-a"}, &git.Worktree{Path: dir}, "", rec); err != nil {
		t.Fatalf("ProduceSolution: %v", err)
	}

	got := kindsOf(rec.Events())
	want := []string{"WorkerStarted", "WorkerFinished"}
	assertKinds(t, got, want)
}

func TestAgyCommandArgsAndFamily(t *testing.T) {
	d := agyDialect()
	if !d.promptViaArgv {
		t.Fatalf("agy must be one-shot family (promptViaArgv=true)")
	}
	base := d.baseArgs(defaultOptions())
	// --print must be the LAST arg so the driver's appended prompt becomes its
	// value.
	if base[len(base)-1] != "--print" {
		t.Fatalf("--print must be last so the prompt is its value, got %v", base)
	}
	if !contains(base, "--dangerously-skip-permissions") || !contains(base, "--print-timeout") {
		t.Fatalf("missing required agy flags: %v", base)
	}
	// Model, when set, appears before --print.
	o := defaultOptions()
	o.workerModel = "gemini-2.5-pro"
	withModel := agyCommandArgs(o)
	if withModel[len(withModel)-1] != "--print" {
		t.Fatalf("--print must stay last even with a model set: %v", withModel)
	}
	if !contains(withModel, "--model") || !contains(withModel, "gemini-2.5-pro") {
		t.Fatalf("expected --model gemini-2.5-pro, got %v", withModel)
	}
}

func TestAgyWorkerRealBinary(t *testing.T) {
	if os.Getenv("LIMEN_E2E_REAL_AGENTS") != "1" {
		t.Skip("set LIMEN_E2E_REAL_AGENTS=1 to run real-agent e2e")
	}
	if _, err := exec.LookPath("agy"); err != nil {
		t.Skip("agy not found in PATH")
	}

	dir := t.TempDir()
	w := NewAgyWorker()
	rec := bus.NewRecorderEmitter()
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	task := &state.Task{ID: "e2e-agy", Prompt: "Reply with exactly the word: done. Do not use any tools."}
	if err := w.ProduceSolution(ctx, task, &git.Worktree{Path: dir}, "", rec); err != nil {
		t.Fatalf("ProduceSolution: %v", err)
	}
	if len(rec.EventsByKind("WorkerFinished")) != 1 {
		t.Fatalf("expected exactly one WorkerFinished, got events %v", kindsOf(rec.Events()))
	}
}
