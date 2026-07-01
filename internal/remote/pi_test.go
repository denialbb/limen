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

func TestPiWorkerBackend(t *testing.T) {
	_, err := exec.LookPath("pi")
	if err != nil {
		t.Skip("pi command not found in PATH, skipping test")
	}

	wtPath := t.TempDir()
	wt := &git.Worktree{
		Path: wtPath,
	}

	task := &state.Task{
		ID: "test-task-1",
	}

	worker := NewPiWorker(WithShutdownTimeout(1 * time.Second))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = worker.ProduceSolution(ctx, task, wt, "", nil)
	if err != nil && err != context.DeadlineExceeded {
		t.Logf("ProduceSolution returned error: %v", err)
	}
}

// TestPiWorkerInjectedCommand drives ProduceSolution against a fake script
// emitting Pi's NDJSON dialect, exercising the injectable command seam without
// a real pi binary.
func TestPiWorkerInjectedCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake shell script fixture requires a POSIX shell")
	}

	dir := t.TempDir()
	script := filepath.Join(dir, "fake-pi.sh")
	fixture := `#!/bin/sh
# Drain stdin so writing the prompt never blocks or breaks the pipe.
cat >/dev/null &
echo '{"type":"tool_execution_start","toolName":"bash","args":{"cmd":"ls"}}'
echo '{"type":"turn_end","message":{"role":"assistant","content":[{"type":"thinking","thinking":"hmm"},{"type":"text","text":"done"}]}}'
echo '{"type":"agent_end"}'
`
	if err := os.WriteFile(script, []byte(fixture), 0755); err != nil {
		t.Fatalf("write fixture script: %v", err)
	}

	worker := newPiWorkerCmd([]string{"/bin/sh", script}, WithShutdownTimeout(2*time.Second))

	rec := bus.NewRecorderEmitter()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := worker.ProduceSolution(ctx, &state.Task{ID: "task-9"}, &git.Worktree{Path: dir}, "", rec)
	if err != nil {
		t.Fatalf("ProduceSolution: %v", err)
	}

	kinds := []string{}
	for _, ev := range rec.Events() {
		kinds = append(kinds, eventKind(ev))
	}
	want := []string{"WorkerStarted", "WorkerToolCall", "WorkerAgentMessage", "WorkerAgentMessage", "WorkerFinished"}
	if len(kinds) != len(want) {
		t.Fatalf("event kinds = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("event kinds = %v, want %v", kinds, want)
		}
	}
}

func eventKind(ev bus.Event) string {
	switch ev.(type) {
	case *bus.WorkerStarted:
		return "WorkerStarted"
	case *bus.WorkerToolCall:
		return "WorkerToolCall"
	case *bus.WorkerAgentMessage:
		return "WorkerAgentMessage"
	case *bus.WorkerFinished:
		return "WorkerFinished"
	default:
		return "unknown"
	}
}

func TestDecodePiEvent(t *testing.T) {
	now := time.Unix(1700000000, 0)
	const taskID = "task-42"

	tests := []struct {
		name     string
		line     string
		agentEnd bool
		want     []bus.Event
	}{
		{
			name: "tool_execution_start with toolName and args",
			line: `{"type":"tool_execution_start","toolName":"bash","args":{"cmd":"ls"}}`,
			want: []bus.Event{&bus.WorkerToolCall{
				TaskID:    taskID,
				Tool:      "bash",
				Args:      `{"cmd":"ls"}`,
				Timestamp: now,
			}},
		},
		{
			name: "tool_execution_start falls back to tool and arguments keys",
			line: `{"type":"tool_execution_start","tool":"write","arguments":{"path":"a.go"}}`,
			want: []bus.Event{&bus.WorkerToolCall{
				TaskID:    taskID,
				Tool:      "write",
				Args:      `{"path":"a.go"}`,
				Timestamp: now,
			}},
		},
		{
			name: "turn_end emits thinking then message for assistant turn",
			line: `{"type":"turn_end","message":{"role":"assistant","content":[{"type":"thinking","thinking":"pondering"},{"type":"text","text":"done"}]}}`,
			want: []bus.Event{
				&bus.WorkerAgentMessage{TaskID: taskID, Kind: "thinking", Text: "pondering", Timestamp: now},
				&bus.WorkerAgentMessage{TaskID: taskID, Kind: "message", Text: "done", Timestamp: now},
			},
		},
		{
			name: "turn_end for non-assistant role emits nothing",
			line: `{"type":"turn_end","message":{"role":"user","content":[{"type":"text","text":"hi"}]}}`,
			want: nil,
		},
		{
			name:     "agent_end signals end of run with no events",
			line:     `{"type":"agent_end"}`,
			agentEnd: true,
			want:     nil,
		},
		{
			name: "invalid json yields empty result",
			line: `not json`,
			want: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := decodePiEvent(tc.line, taskID, now)

			if res.agentEnd != tc.agentEnd {
				t.Fatalf("agentEnd = %v, want %v", res.agentEnd, tc.agentEnd)
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

func assertEventEqual(t *testing.T, got, want bus.Event) {
	t.Helper()
	switch w := want.(type) {
	case *bus.WorkerToolCall:
		g, ok := got.(*bus.WorkerToolCall)
		if !ok {
			t.Fatalf("event type = %T, want *bus.WorkerToolCall", got)
		}
		if *g != *w {
			t.Fatalf("WorkerToolCall = %#v, want %#v", g, w)
		}
	case *bus.WorkerAgentMessage:
		g, ok := got.(*bus.WorkerAgentMessage)
		if !ok {
			t.Fatalf("event type = %T, want *bus.WorkerAgentMessage", got)
		}
		if *g != *w {
			t.Fatalf("WorkerAgentMessage = %#v, want %#v", g, w)
		}
	default:
		t.Fatalf("unexpected want type %T", want)
	}
}
