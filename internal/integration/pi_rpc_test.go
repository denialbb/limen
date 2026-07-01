package integration

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestPiRPCHandshake(t *testing.T) {
	// Check if pi is available
	_, err := exec.LookPath("pi")
	if err != nil {
		t.Skip("pi command not found in PATH, skipping test")
	}

	// Create a temp directory
	tmpDir := t.TempDir()

	// --no-extensions disables the permission system extension so Pi does not
	// emit interactive extension_ui_request prompts that would block the pipe.
	cmd := exec.Command("pi", "--mode", "rpc", "--no-extensions")
	cmd.Dir = tmpDir

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("Failed to create stdin pipe: %v", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("Failed to create stdout pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start pi: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	// Close stdout pipe on context cancellation so the scanner below is
	// unblocked even if Pi's bash children outlive the kill signal.
	defer context.AfterFunc(ctx, func() { stdout.Close() })()

	// Send a hardcoded JSONL prompt
	prompt := map[string]interface{}{
		"id":      "req-1",
		"type":    "prompt",
		"message": "Create a file named test.txt containing the word 'success'",
	}
	promptBytes, _ := json.Marshal(prompt)
	promptBytes = append(promptBytes, '\n')

	if _, err := stdin.Write(promptBytes); err != nil {
		t.Fatalf("Failed to write to stdin: %v", err)
	}
	// Close stdin so Pi knows there is no more input, if it cares.
	// Actually for Pi RPC, closing stdin usually shuts it down, but let's see.
	// Maybe we just let it run.

	// Read from stdout
	scanner := bufio.NewScanner(stdout)
	foundAgentEnd := false

Loop:
	for scanner.Scan() {
		line := scanner.Text()
		t.Logf("pi out: %s", line)

		var msg map[string]interface{}
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			// Ignore lines that aren't JSON
			continue
		}

		if msgType, ok := msg["type"].(string); ok {
			switch msgType {
			case "agent_end":
				foundAgentEnd = true
				break Loop
			case "message_update":
				t.Logf("Observed message_update event")
			}
			if len(msgType) > 15 && msgType[:15] == "tool_execution_" {
				t.Logf("Observed tool_execution event: %s", msgType)
			}
		}
	}

	if err := scanner.Err(); err != nil && err != io.EOF {
		// Ignore closed-pipe error from our context-cancel cleanup.
		if ctx.Err() == nil || !strings.Contains(err.Error(), "file already closed") {
			t.Fatalf("Error reading stdout: %v", err)
		}
	}

	if !foundAgentEnd {
		if ctx.Err() != nil {
			t.Errorf("Timed out waiting for agent_end")
		} else {
			t.Errorf("Did not receive agent_end message")
		}
	}

	// Clean up
	stdin.Close()
	cmd.Process.Kill()
	cmd.Wait()
}
