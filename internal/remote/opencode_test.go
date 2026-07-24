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

func TestDecodeOpencodeEvent(t *testing.T) {
	now := time.Unix(1700000000, 0)
	const taskID = "task-o"

	tests := []struct {
		name string
		line string
		want []bus.Event
	}{
		{
			name: "tool_use maps state.input to WorkerToolCall",
			line: `{"type":"tool_use","part":{"type":"tool","tool":"bash","callID":"c1","state":{"status":"completed","input":{"command":"echo hi"},"output":"hi\n"}}}`,
			want: []bus.Event{&bus.WorkerToolCall{TaskID: taskID, Tool: "bash", Args: `{"command":"echo hi"}`, Timestamp: now}},
		},
		{
			name: "text maps to WorkerAgentMessage",
			line: `{"type":"text","part":{"type":"text","text":"done"}}`,
			want: []bus.Event{&bus.WorkerAgentMessage{TaskID: taskID, Kind: "message", Text: "done", Timestamp: now}},
		},
		{
			name: "step_start emits nothing",
			line: `{"type":"step_start","part":{"type":"step-start"}}`,
		},
		{
			name: "step_finish emits nothing",
			line: `{"type":"step_finish","part":{"reason":"stop"}}`,
		},
		{
			name: "invalid json emits nothing",
			line: `not json`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := decodeOpencodeEvent(tc.line, taskID, now)
			if res.end {
				t.Fatalf("opencode is one-shot; end must never be true")
			}
			if len(res.events) != len(tc.want) {
				t.Fatalf("got %d events, want %d: %#v", len(res.events), len(tc.want), res.events)
			}
			for i, want := range tc.want {
				assertEventEqual(t, res.events[i], want)
			}
		})
	}
}

func TestOpencodeWorkerInjectedCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake shell script fixture requires a POSIX shell")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-opencode.sh")
	fixture := `#!/bin/sh
echo '{"type":"step_start","part":{"type":"step-start"}}'
echo '{"type":"tool_use","part":{"type":"tool","tool":"bash","state":{"status":"completed","input":{"command":"echo hi"},"output":"hi\n"}}}'
echo '{"type":"step_finish","part":{"reason":"tool-calls"}}'
echo '{"type":"text","part":{"type":"text","text":"done"}}'
echo '{"type":"step_finish","part":{"reason":"stop"}}'
`
	if err := os.WriteFile(script, []byte(fixture), 0755); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	w := newAgentWorkerCmd(opencodeDialect(), []string{"/bin/sh", script}, defaultOptions())
	rec := bus.NewRecorderEmitter()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := w.ProduceSolution(ctx, &state.Task{ID: "task-o"}, &git.Worktree{Path: dir}, "", rec); err != nil {
		t.Fatalf("ProduceSolution: %v", err)
	}

	got := kindsOf(rec.Events())
	want := []string{"WorkerStarted", "WorkerToolCall", "WorkerAgentMessage", "WorkerFinished"}
	assertKinds(t, got, want)
}

func TestOpencodeCommandArgsAndFamily(t *testing.T) {
	d := opencodeDialect()
	if !d.promptViaArgv {
		t.Fatalf("opencode must be one-shot family (promptViaArgv=true)")
	}
	base := d.baseArgs(defaultOptions())
	for _, want := range []string{"run", "--format", "json", "--auto"} {
		if !contains(base, want) {
			t.Fatalf("missing %q in %v", want, base)
		}
	}
	if contains(base, "-m") {
		t.Fatalf("expected no -m by default, got %v", base)
	}
	o := defaultOptions()
	o.workerModel = "anthropic/claude-opus-4-8"
	withModel := opencodeCommandArgs(o)
	if !contains(withModel, "-m") || !contains(withModel, "anthropic/claude-opus-4-8") {
		t.Fatalf("expected -m anthropic/claude-opus-4-8, got %v", withModel)
	}
}

func TestOpencodeWorkerRealBinary(t *testing.T) {
	if os.Getenv("LIMEN_E2E_REAL_AGENTS") != "1" {
		t.Skip("set LIMEN_E2E_REAL_AGENTS=1 to run real-agent e2e")
	}
	if _, err := exec.LookPath("opencode"); err != nil {
		t.Skip("opencode not found in PATH")
	}

	dir := t.TempDir()
	w := NewOpencodeWorker()
	rec := bus.NewRecorderEmitter()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	task := &state.Task{ID: "e2e-opencode", Prompt: "Reply with exactly the word: done. Do not use any tools."}
	if err := w.ProduceSolution(ctx, task, &git.Worktree{Path: dir}, "", rec); err != nil {
		t.Fatalf("ProduceSolution: %v", err)
	}
	if len(rec.EventsByKind("WorkerFinished")) != 1 {
		t.Fatalf("expected exactly one WorkerFinished, got events %v", kindsOf(rec.Events()))
	}
}
