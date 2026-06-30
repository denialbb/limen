package integration

import (
	"bufio"
	"encoding/json"
	"io"
	"os/exec"
	"testing"
)

func TestPiRPCHandshake(t *testing.T) {
	// Check if pi is available
	_, err := exec.LookPath("pi")
	if err != nil {
		t.Skip("pi command not found in PATH, skipping test")
	}

	// Create a temp directory
	tmpDir := t.TempDir()

	// Spawn pi --mode rpc
	cmd := exec.Command("pi", "--mode", "rpc")
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

	// Send a hardcoded JSONL prompt
	prompt := map[string]interface{}{
		"type":   "prompt",
		"prompt": "Create a file named test.txt containing the word 'success'",
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
		t.Fatalf("Error reading stdout: %v", err)
	}

	if !foundAgentEnd {
		t.Errorf("Did not receive agent_end message")
	}

	// Clean up
	stdin.Close()
	cmd.Process.Kill()
	cmd.Wait()
}
