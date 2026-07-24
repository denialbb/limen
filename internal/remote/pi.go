package remote

import (
	"encoding/json"
	"time"

	"github.com/denialbb/limen/internal/bus"
	"github.com/denialbb/limen/internal/orchestrator"
)

// piEditToolConstraint is pi's dialect-owned constraint block: pi's edit tool
// does not work in this environment, so all file mutations must go through
// bash. This constraint is pi-only and must NOT leak into other dialects, whose
// edit tools work (issue 015).
const piEditToolConstraint = "- Do NOT use the edit tool — it does not work in this environment. Use bash commands (sed, awk, python, or direct file writes) for all file modifications.\n"

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

// piDialect describes pi's RPC NDJSON wire protocol for the generic
// agentWorker driver. Pi is an RPC-family dialect: the prompt is delivered on
// stdin, the process stays alive across the blocking ready-for-review callback,
// and decodePiEvent reports the explicit agent_end event.
func piDialect() dialect {
	return dialect{
		name:          "pi",
		baseArgs:      piCommandArgs,
		promptViaArgv: false,
		encodeStdin: func(taskID, promptText string) []byte {
			b, _ := json.Marshal(map[string]interface{}{
				"id":      taskID,
				"type":    "prompt",
				"message": promptText,
			})
			return append(b, '\n')
		},
		decode: func(line, taskID string, now time.Time) decodeResult {
			res := decodePiEvent(line, taskID, now)
			return decodeResult{events: res.events, end: res.agentEnd}
		},
		constraints: piEditToolConstraint,
	}
}

// NewPiWorker constructs a pi-backed worker on the generic agentWorker driver.
func NewPiWorker(opts ...Option) orchestrator.Worker {
	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}
	return newAgentWorker(piDialect(), o)
}

// newPiWorkerCmd creates a pi worker backed by an explicit argv, letting tests
// point it at a fake script that emits Pi's NDJSON dialect.
func newPiWorkerCmd(args []string, opts ...Option) *agentWorker {
	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}
	return newAgentWorkerCmd(piDialect(), args, o)
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
