package remote

import (
	"encoding/json"
	"time"

	"github.com/denialbb/limen/internal/bus"
	"github.com/denialbb/limen/internal/orchestrator"
)

// claudeCommandArgs builds the argv to launch the claude CLI in headless
// stream-json mode. `--verbose` is required alongside stream-json under `-p`;
// bypassPermissions runs unattended. No `--bare` — that would force
// ANTHROPIC_API_KEY and break OAuth/subscription auth (spike 015-0). The model
// flag is omitted when unset so claude uses its configured default.
func claudeCommandArgs(o *options) []string {
	args := []string{
		"claude", "-p",
		"--output-format", "stream-json",
		"--input-format", "stream-json",
		"--verbose",
		"--permission-mode", "bypassPermissions",
	}
	if o.workerModel != "" {
		args = append(args, "--model", o.workerModel)
	}
	return args
}

// claudeDialect describes claude's stream-json wire protocol for the generic
// agentWorker driver. Claude is an RPC-family dialect: the prompt is delivered
// on stdin as a stream-json user envelope, the process stays alive across the
// blocking ready-for-review callback, and decodeClaudeEvent reports the `result`
// event as end-of-run. Claude's edit tool works, so no constraint block.
func claudeDialect() dialect {
	return dialect{
		name:          "claude",
		baseArgs:      claudeCommandArgs,
		promptViaArgv: false,
		encodeStdin: func(taskID, promptText string) []byte {
			b, _ := json.Marshal(map[string]any{
				"type": "user",
				"message": map[string]any{
					"role": "user",
					"content": []map[string]any{
						{"type": "text", "text": promptText},
					},
				},
			})
			return append(b, '\n')
		},
		decode:      decodeClaudeEvent,
		constraints: "",
	}
}

// NewClaudeWorker constructs a claude-backed worker on the generic agentWorker
// driver.
func NewClaudeWorker(opts ...Option) orchestrator.Worker {
	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}
	return newAgentWorker(claudeDialect(), o)
}

// decodeClaudeEvent translates one line of claude's stream-json output into bus
// events and an end signal. It is a pure function over the raw line, task ID,
// and timestamp. Mapping (spike 015-0):
//
//   - assistant message content parts: thinking -> WorkerAgentMessage(thinking),
//     text -> WorkerAgentMessage(message), tool_use -> WorkerToolCall.
//   - result -> end of run (analogue of pi's agent_end).
//   - system (init/hook_*), user (tool_result), rate_limit_event -> ignored.
func decodeClaudeEvent(line, taskID string, now time.Time) decodeResult {
	var msg map[string]any
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		return decodeResult{}
	}

	switch msgType, _ := msg["type"].(string); msgType {
	case "result":
		return decodeResult{end: true}

	case "assistant":
		inner, _ := msg["message"].(map[string]any)
		content, _ := inner["content"].([]any)
		var events []bus.Event
		for _, raw := range content {
			part, ok := raw.(map[string]any)
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
			case "tool_use":
				toolName, _ := part["name"].(string)
				var argsStr string
				if input, ok := part["input"]; ok {
					b, _ := json.Marshal(input)
					argsStr = string(b)
				}
				events = append(events, &bus.WorkerToolCall{
					TaskID:    taskID,
					Tool:      toolName,
					Args:      argsStr,
					Timestamp: now,
				})
			}
		}
		return decodeResult{events: events}
	}

	return decodeResult{}
}
