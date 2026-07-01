package remote

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/denialbb/limen/internal/bus"
	"github.com/denialbb/limen/internal/git"
	"github.com/denialbb/limen/internal/orchestrator"
	"github.com/denialbb/limen/internal/state"
)

type piWorker struct {
	cmdArgs []string
	opts    *options
}

// piCommandArgs builds the default argv used to launch the pi binary in RPC
// mode with the configured provider and model.
func piCommandArgs(o *options) []string {
	return []string{
		"pi", "--mode", "rpc",
		"--no-extensions",
		"--exclude-tools", "fetch,browser,internet",
		"--provider", o.piProvider,
		"--model", o.piModel,
	}
}

func NewPiWorker(opts ...Option) orchestrator.Worker {
	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}
	return &piWorker{cmdArgs: piCommandArgs(o), opts: o}
}

// newPiWorkerCmd creates a piWorker backed by an explicit argv, letting tests
// point it at a fake script that emits Pi's NDJSON dialect.
func newPiWorkerCmd(args []string, opts ...Option) *piWorker {
	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}
	return &piWorker{cmdArgs: args, opts: o}
}

func (w *piWorker) ProduceSolution(ctx context.Context, task *state.Task, wt *git.Worktree, feedback string, em orchestrator.Emitter) error {
	cmd := exec.CommandContext(ctx, w.cmdArgs[0], w.cmdArgs[1:]...)
	cmd.Dir = wt.Path

	// Prepend the limen binary's directory to PATH so the agent can call
	// `limen ready-for-review` without knowing the absolute path.
	if selfPath, err := os.Executable(); err == nil {
		selfDir := filepath.Dir(selfPath)
		cmd.Env = append(os.Environ(), fmt.Sprintf("PATH=%s:%s", selfDir, os.Getenv("PATH")))
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("piworker: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("piworker: stdout pipe: %w", err)
	}
	// Tee Pi's stdout to the log file so raw RPC events are captured for debugging.
	// Pi's stderr also goes to the log file (not os.Stderr) so it doesn't bleed
	// through the alt screen TUI and corrupt the display.
	var reader io.Reader = stdout
	if w.opts.logDir != "" {
		logPath := filepath.Join(w.opts.logDir, fmt.Sprintf("%s-worker.log", task.ID))
		if err := os.MkdirAll(w.opts.logDir, 0755); err == nil {
			if f, err2 := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err2 == nil {
				defer f.Close()
				reader = io.TeeReader(stdout, f)
				cmd.Stderr = f
			}
		}
	}
	if cmd.Stderr == nil {
		cmd.Stderr = io.Discard
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("piworker: start: %w", err)
	}

	// Close the stdout pipe when the context is cancelled so the scanner
	// below is unblocked even if Pi's bash child processes are still alive
	// and holding the write end of the pipe open (exec.CommandContext only
	// kills the direct child, not its descendants).
	stopPipeClose := context.AfterFunc(ctx, func() { stdout.Close() })

	if em != nil {
		em.Publish(&bus.WorkerStarted{
			TaskID:       task.ID,
			WorktreePath: wt.Path,
			BaseCommit:   wt.BaseCommit,
			Retry:        task.RetryCount,
			Timestamp:    time.Now(),
		})
	}

	promptText := fmt.Sprintf(
		"Task ID: %s\n\nTask: %s\n\nIMPORTANT CONSTRAINTS:\n"+
			"- Do NOT use the edit tool — it does not work in this environment. Use bash commands (sed, awk, python, or direct file writes) for all file modifications.\n"+
			"- When you are finished, you MUST run: `limen ready-for-review --task-id %s --summary \"<summary>\"`. Wait for the verdict. If approved, you can finish. If rejected with feedback, revise your work and call ready-for-review again.",
		task.ID, task.Prompt, task.ID,
	)
	if feedback != "" {
		promptText += fmt.Sprintf("\n\nPrevious feedback:\n%s", feedback)
	}

	promptBytes, _ := json.Marshal(map[string]interface{}{
		"id":      task.ID,
		"type":    "prompt",
		"message": promptText,
	})
	promptBytes = append(promptBytes, '\n')

	if _, err := stdin.Write(promptBytes); err != nil {
		return fmt.Errorf("piworker: write prompt: %w", err)
	}

	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()

		res := decodePiEvent(line, task.ID, time.Now())

		if res.agentEnd {
			// Signal EOF so Pi can exit cleanly rather than blocking on stdin.
			stdin.Close()
			break
		}

		if em == nil {
			continue
		}
		for _, ev := range res.events {
			em.Publish(ev)
		}
	}

	stopPipeClose() // no-op if context already fired; prevents goroutine leak on clean exit

	if err := scanner.Err(); err != nil && err != io.EOF {
		// Suppress the error if context was cancelled — pipe close caused it.
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("piworker: read stdout: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("piworker: wait: %w", err)
	}

	if em != nil {
		em.Publish(&bus.WorkerFinished{
			TaskID:    task.ID,
			Timestamp: time.Now(),
		})
	}

	return nil
}

// piDecodeResult is the outcome of decoding a single Pi RPC line: the bus
// events to emit and whether the line signals the end of the agent run.
type piDecodeResult struct {
	events   []bus.Event
	agentEnd bool
}

// decodePiEvent translates one line of Pi's RPC NDJSON dialect into the bus
// events it should produce. It is a pure function over the raw line, task ID,
// and timestamp, keeping Pi's dialect decoding separate from process I/O.
func decodePiEvent(line, taskID string, now time.Time) piDecodeResult {
	var msg map[string]interface{}
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		return piDecodeResult{}
	}

	msgType, _ := msg["type"].(string)

	switch msgType {
	case "agent_end":
		return piDecodeResult{agentEnd: true}

	case "tool_execution_start":
		toolName, _ := msg["toolName"].(string)
		if toolName == "" {
			toolName, _ = msg["tool"].(string)
		}
		var argsStr string
		if args, ok := msg["args"]; ok {
			b, _ := json.Marshal(args)
			argsStr = string(b)
		} else if args, ok := msg["arguments"]; ok {
			b, _ := json.Marshal(args)
			argsStr = string(b)
		} else {
			argsStr = line
		}
		return piDecodeResult{events: []bus.Event{&bus.WorkerToolCall{
			TaskID:    taskID,
			Tool:      toolName,
			Args:      argsStr,
			Timestamp: now,
		}}}

	case "turn_end":
		// Extract agent thinking and text from the completed assistant turn.
		turnMsg, _ := msg["message"].(map[string]interface{})
		if role, _ := turnMsg["role"].(string); role != "assistant" {
			return piDecodeResult{}
		}
		content, _ := turnMsg["content"].([]interface{})
		var events []bus.Event
		for _, raw := range content {
			part, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			switch part["type"] {
			case "thinking":
				if text, _ := part["thinking"].(string); text != "" {
					events = append(events, &bus.WorkerAgentMessage{
						TaskID:    taskID,
						Kind:      "thinking",
						Text:      text,
						Timestamp: now,
					})
				}
			case "text":
				if text, _ := part["text"].(string); text != "" {
					events = append(events, &bus.WorkerAgentMessage{
						TaskID:    taskID,
						Kind:      "message",
						Text:      text,
						Timestamp: now,
					})
				}
			}
		}
		return piDecodeResult{events: events}
	}

	return piDecodeResult{}
}
