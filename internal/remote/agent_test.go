package remote

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/denialbb/limen/internal/bus"
	"github.com/denialbb/limen/internal/git"
	"github.com/denialbb/limen/internal/state"
)

// testDialectRPC decodes a toy RPC line protocol used to exercise the generic
// driver's RPC family path (prompt via stdin, explicit end event):
//
//	TOOL <name>   -> WorkerToolCall
//	MSG <text>    -> WorkerAgentMessage(message)
//	END           -> end of run
func testDialectRPC() dialect {
	return dialect{
		name:     "test-rpc",
		baseArgs: func(o *options) []string { return []string{"test-rpc"} },
		encodeStdin: func(taskID, promptText string) []byte {
			return []byte(promptText + "\n")
		},
		decode: func(line, taskID string, now time.Time) decodeResult {
			switch {
			case line == "END":
				return decodeResult{end: true}
			case strings.HasPrefix(line, "TOOL "):
				return decodeResult{events: []bus.Event{&bus.WorkerToolCall{
					TaskID: taskID, Tool: strings.TrimPrefix(line, "TOOL "), Timestamp: now,
				}}}
			case strings.HasPrefix(line, "MSG "):
				return decodeResult{events: []bus.Event{&bus.WorkerAgentMessage{
					TaskID: taskID, Kind: "message", Text: strings.TrimPrefix(line, "MSG "), Timestamp: now,
				}}}
			}
			return decodeResult{}
		},
	}
}

// testDialectOneShot decodes the same toy protocol but as a one-shot family
// dialect (prompt via argv, no end event — scan to EOF).
func testDialectOneShot() dialect {
	d := testDialectRPC()
	d.name = "test-oneshot"
	d.promptViaArgv = true
	d.encodeStdin = nil
	// One-shot dialects have no end event; END lines decode to nothing.
	d.decode = func(line, taskID string, now time.Time) decodeResult {
		switch {
		case strings.HasPrefix(line, "TOOL "):
			return decodeResult{events: []bus.Event{&bus.WorkerToolCall{
				TaskID: taskID, Tool: strings.TrimPrefix(line, "TOOL "), Timestamp: now,
			}}}
		case strings.HasPrefix(line, "MSG "):
			return decodeResult{events: []bus.Event{&bus.WorkerAgentMessage{
				TaskID: taskID, Kind: "message", Text: strings.TrimPrefix(line, "MSG "), Timestamp: now,
			}}}
		}
		return decodeResult{}
	}
	return d
}

func TestAgentWorkerRPCFamily(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake shell script fixture requires a POSIX shell")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-rpc.sh")
	// Drains stdin (proving the prompt was delivered without breaking the pipe),
	// emits a tool call and a message, then the end marker.
	fixture := `#!/bin/sh
cat >/dev/null &
echo 'TOOL bash'
echo 'MSG done'
echo 'END'
`
	if err := os.WriteFile(script, []byte(fixture), 0755); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	w := newAgentWorkerCmd(testDialectRPC(), []string{"/bin/sh", script}, defaultOptions())
	rec := bus.NewRecorderEmitter()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := w.ProduceSolution(ctx, &state.Task{ID: "task-rpc"}, &git.Worktree{Path: dir}, "", rec); err != nil {
		t.Fatalf("ProduceSolution: %v", err)
	}

	got := kindsOf(rec.Events())
	want := []string{"WorkerStarted", "WorkerToolCall", "WorkerAgentMessage", "WorkerFinished"}
	assertKinds(t, got, want)
}

func TestAgentWorkerOneShotFamily(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake shell script fixture requires a POSIX shell")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-oneshot.sh")
	// One-shot: ignores stdin, emits lines, exits at EOF (no end marker).
	fixture := `#!/bin/sh
echo 'TOOL edit'
echo 'MSG finished'
`
	if err := os.WriteFile(script, []byte(fixture), 0755); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	w := newAgentWorkerCmd(testDialectOneShot(), []string{"/bin/sh", script}, defaultOptions())
	rec := bus.NewRecorderEmitter()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := w.ProduceSolution(ctx, &state.Task{ID: "task-os"}, &git.Worktree{Path: dir}, "", rec); err != nil {
		t.Fatalf("ProduceSolution: %v", err)
	}

	got := kindsOf(rec.Events())
	want := []string{"WorkerStarted", "WorkerToolCall", "WorkerAgentMessage", "WorkerFinished"}
	assertKinds(t, got, want)
}

func TestRenderWorkerPromptContract(t *testing.T) {
	task := &state.Task{ID: "t1", Prompt: "implement add"}
	got := renderWorkerPrompt(task, "", "")
	// Shared contract elements every dialect must carry.
	for _, want := range []string{
		"Task ID: t1",
		"Task: implement add",
		"limen ready-for-review --task-id t1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt missing %q:\n%s", want, got)
		}
	}
	// Feedback is appended when present.
	withFb := renderWorkerPrompt(task, "tests failed", "")
	if !strings.Contains(withFb, "Previous feedback:\ntests failed") {
		t.Fatalf("feedback not appended:\n%s", withFb)
	}
	// The pi edit-tool constraint must NOT leak into a constraint-free dialect.
	if strings.Contains(got, "Do NOT use the edit tool") {
		t.Fatalf("edit-tool constraint leaked into empty-constraint prompt")
	}
	// A dialect-owned constraint block is injected verbatim.
	withC := renderWorkerPrompt(task, "", "- custom rule\n")
	if !strings.Contains(withC, "- custom rule") {
		t.Fatalf("constraint block not injected:\n%s", withC)
	}
}

func kindsOf(events []bus.Event) []string {
	out := make([]string, 0, len(events))
	for _, ev := range events {
		out = append(out, eventKind(ev))
	}
	return out
}

func assertKinds(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("event kinds = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event kinds = %v, want %v", got, want)
		}
	}
}
