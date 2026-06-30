package remote

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/denialbb/limen/internal/bus"
	"github.com/denialbb/limen/internal/git"
	"github.com/denialbb/limen/internal/orchestrator"
	"github.com/denialbb/limen/internal/state"
)

type piWorker struct {
	opts *options
}

func NewPiWorker(opts ...Option) orchestrator.Worker {
	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}
	return &piWorker{opts: o}
}

func (w *piWorker) ProduceSolution(ctx context.Context, task *state.Task, wt *git.Worktree, feedback string, em orchestrator.Emitter) error {
	cmd := exec.CommandContext(ctx, "pi", "--mode", "rpc")
	cmd.Dir = wt.Path

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("piworker: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("piworker: stdout pipe: %w", err)
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("piworker: start: %w", err)
	}

	if em != nil {
		em.Publish(&bus.WorkerStarted{
			TaskID:    task.ID,
			Timestamp: time.Now(),
		})
	}

	// Construct the prompt
	promptText := fmt.Sprintf("Task Description:\n%s\n\nWhen you are finished, you MUST run the command `limen ready-for-review --task-id %s --summary \"<summary>\"`. Wait for the verdict. If it says approved, you can finish. If it says rejected with feedback, you must revise your work and then call ready-for-review again.", task.Description, task.ID)
	if feedback != "" {
		promptText += fmt.Sprintf("\n\nPrevious feedback:\n%s", feedback)
	}

	prompt := map[string]interface{}{
		"type":   "prompt",
		"prompt": promptText,
	}
	promptBytes, _ := json.Marshal(prompt)
	promptBytes = append(promptBytes, '\n')

	if _, err := stdin.Write(promptBytes); err != nil {
		return fmt.Errorf("piworker: write prompt: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		
		var msg map[string]interface{}
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}

		msgType, _ := msg["type"].(string)
		
		if msgType == "agent_end" {
			break
		}
		
		if em != nil {
			if msgType == "message_update" {
				// We can publish this as a WorkerToolCall or similar event if needed
				content, _ := msg["content"].(string)
				em.Publish(&bus.WorkerToolCall{
					TaskID:    task.ID,
					Tool:      "pi_message",
					Args:      content,
					Timestamp: time.Now(),
				})
			} else if strings.HasPrefix(msgType, "tool_execution_") {
				toolName, _ := msg["tool"].(string)
				em.Publish(&bus.WorkerToolCall{
					TaskID:    task.ID,
					Tool:      toolName,
					Args:      line,
					Timestamp: time.Now(),
				})
			}
		}
	}

	if err := scanner.Err(); err != nil && err != io.EOF {
		return fmt.Errorf("piworker: read stdout: %w", err)
	}

	// Wait for process to exit
	if err := cmd.Wait(); err != nil {
		// Ignore ExitError if context was canceled or process was killed
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
