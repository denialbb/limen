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

func TestDecodeClaudeEvent(t *testing.T) {
	now := time.Unix(1700000000, 0)
	const taskID = "task-c"

	tests := []struct {
		name string
		line string
		end  bool
		want []bus.Event
	}{
		{
			name: "assistant thinking part",
			line: `{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"pondering","signature":"abc"}]}}`,
			want: []bus.Event{&bus.WorkerAgentMessage{TaskID: taskID, Kind: "thinking", Text: "pondering", Timestamp: now}},
		},
		{
			name: "assistant text part",
			line: `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}`,
			want: []bus.Event{&bus.WorkerAgentMessage{TaskID: taskID, Kind: "message", Text: "done", Timestamp: now}},
		},
		{
			name: "assistant tool_use part",
			line: `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"echo hi"}}]}}`,
			want: []bus.Event{&bus.WorkerToolCall{TaskID: taskID, Tool: "Bash", Args: `{"command":"echo hi"}`, Timestamp: now}},
		},
		{
			name: "result signals end",
			line: `{"type":"result","subtype":"success","result":"done"}`,
			end:  true,
		},
		{
			name: "system init emits nothing",
			line: `{"type":"system","subtype":"init","cwd":"/tmp"}`,
		},
		{
			name: "rate_limit_event emits nothing",
			line: `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed"}}`,
		},
		{
			name: "user tool_result emits nothing",
			line: `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"hi"}]}}`,
		},
		{
			name: "invalid json emits nothing",
			line: `not json`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := decodeClaudeEvent(tc.line, taskID, now)
			if res.end != tc.end {
				t.Fatalf("end = %v, want %v", res.end, tc.end)
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

func TestClaudeWorkerInjectedCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake shell script fixture requires a POSIX shell")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-claude.sh")
	fixture := `#!/bin/sh
cat >/dev/null &
echo '{"type":"system","subtype":"init","cwd":"/tmp"}'
echo '{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"hmm"}]}}'
echo '{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"echo hi"}}]}}'
echo '{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"hi"}]}}'
echo '{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}'
echo '{"type":"result","subtype":"success","result":"done"}'
`
	if err := os.WriteFile(script, []byte(fixture), 0755); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	w := newAgentWorkerCmd(claudeDialect(), []string{"/bin/sh", script}, defaultOptions())
	rec := bus.NewRecorderEmitter()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := w.ProduceSolution(ctx, &state.Task{ID: "task-c"}, &git.Worktree{Path: dir}, "", rec); err != nil {
		t.Fatalf("ProduceSolution: %v", err)
	}

	got := kindsOf(rec.Events())
	want := []string{"WorkerStarted", "WorkerAgentMessage", "WorkerToolCall", "WorkerAgentMessage", "WorkerFinished"}
	assertKinds(t, got, want)
}

func TestClaudeCommandArgs(t *testing.T) {
	// Default: no --model when workerModel is empty.
	base := claudeCommandArgs(defaultOptions())
	if contains(base, "--model") {
		t.Fatalf("expected no --model by default, got %v", base)
	}
	if !contains(base, "stream-json") || !contains(base, "bypassPermissions") {
		t.Fatalf("missing required claude flags: %v", base)
	}
	// Explicit model maps to --model.
	o := defaultOptions()
	o.workerModel = "claude-opus-4-8"
	withModel := claudeCommandArgs(o)
	if !contains(withModel, "--model") || !contains(withModel, "claude-opus-4-8") {
		t.Fatalf("expected --model claude-opus-4-8, got %v", withModel)
	}
}

// TestClaudeWorkerRealBinary exercises the real claude CLI end-to-end. It is
// gated behind LIMEN_E2E_REAL_AGENTS=1 (costs tokens, needs auth) and skipped
// when claude is absent.
func TestClaudeWorkerRealBinary(t *testing.T) {
	if os.Getenv("LIMEN_E2E_REAL_AGENTS") != "1" {
		t.Skip("set LIMEN_E2E_REAL_AGENTS=1 to run real-agent e2e")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not found in PATH")
	}

	dir := t.TempDir()
	w := NewClaudeWorker()
	rec := bus.NewRecorderEmitter()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	task := &state.Task{ID: "e2e-claude", Prompt: "Reply with exactly the word: done. Do not use any tools."}
	if err := w.ProduceSolution(ctx, task, &git.Worktree{Path: dir}, "", rec); err != nil {
		t.Fatalf("ProduceSolution: %v", err)
	}
	if len(rec.EventsByKind("WorkerFinished")) != 1 {
		t.Fatalf("expected exactly one WorkerFinished, got events %v", kindsOf(rec.Events()))
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
