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
	var logFile *os.File
	if w.opts.logDir != "" {
		logPath := filepath.Join(w.opts.logDir, fmt.Sprintf("%s-worker.log", task.ID))
		if err := os.MkdirAll(w.opts.logDir, 0755); err == nil {
			logFile, err = os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
			if err == nil {
				defer logFile.Close()
				cmd.Stderr = logFile
			} else {
				cmd.Stderr = os.Stderr
			}
		} else {
			cmd.Stderr = os.Stderr
		}
	} else {
		cmd.Stderr = os.Stderr
	}

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
	promptText := fmt.Sprintf("Task ID: %s\n\nTask: %s\n\nWhen you are finished, you MUST run the command `limen ready-for-review --task-id %s --summary \"<summary>\"`. Wait for the verdict. If it says approved, you can finish. If it says rejected with feedback, you must revise your work and then call ready-for-review again.", task.ID, task.Prompt, task.ID)
	if feedback != "" {
		promptText += fmt.Sprintf("\n\nPrevious feedback:\n%s", feedback)
	}

	prompt := map[string]interface{}{
		"id":      task.ID,
		"type":    "prompt",
		"message": promptText,
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

		// TEMPORARY DEBUG: write raw pi lines to a file so we can inspect them
		// (Removed temporary debug logging)

		msgType, _ := msg["type"].(string)

		if msgType == "agent_end" {
			break
		}

		if em != nil {
			// Skip reasoning messages (message_update); only publish tool executions
			if msgType == "tool_execution_start" {
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

				em.Publish(&bus.WorkerToolCall{
					TaskID:    task.ID,
					Tool:      toolName,
					Args:      argsStr,
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
