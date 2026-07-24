package remote

import (
	"encoding/json"
	"time"

	"github.com/denialbb/limen/internal/bus"
	"github.com/denialbb/limen/internal/orchestrator"
)

// opencodeCommandArgs builds the argv to launch opencode in headless one-shot
// mode. The rendered prompt is appended by the driver as the positional
// message. `--auto` auto-approves permissions; `--format json` emits NDJSON
// events on stdout. The model flag is omitted when unset so opencode uses its
// configured default.
func opencodeCommandArgs(o *options) []string {
	args := []string{"opencode", "run", "--format", "json", "--auto"}
	if o.workerModel != "" {
		args = append(args, "-m", o.workerModel)
	}
	return args
}

// opencodeDialect describes opencode's `run --format json` wire protocol for the
// generic agentWorker driver. opencode is a one-shot-family dialect: the prompt
// is passed as an argv positional, there is no explicit end event, and the
// driver scans stdout to EOF. opencode's edit tool works, so no constraint
// block. The blocking ready-for-review bash call keeps `run` alive across
// verdict rounds.
func opencodeDialect() dialect {
	return dialect{
		name:          "opencode",
		baseArgs:      opencodeCommandArgs,
		promptViaArgv: true,
		encodeStdin:   nil,
		decode:        decodeOpencodeEvent,
		constraints:   "",
	}
}

// NewOpencodeWorker constructs an opencode-backed worker on the generic
// agentWorker driver.
func NewOpencodeWorker(opts ...Option) orchestrator.Worker {
	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}
	return newAgentWorker(opencodeDialect(), o)
}

// decodeOpencodeEvent translates one line of opencode's `run --format json`
// output into bus events. It is a pure function over the raw line, task ID, and
// timestamp. Mapping (spike 015-0):
//
//   - tool_use: part.tool + part.state.input -> WorkerToolCall.
//   - text: part.text -> WorkerAgentMessage(message).
//   - step_start / step_finish -> ignored (EOF is the driver's end signal).
//
// opencode never reports end (one-shot family), so end is always false.
func decodeOpencodeEvent(line, taskID string, now time.Time) decodeResult {
	var msg map[string]interface{}
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		return decodeResult{}
	}

	part, _ := msg["part"].(map[string]interface{})
	if part == nil {
		return decodeResult{}
	}

	switch msgType, _ := msg["type"].(string); msgType {
	case "tool_use":
		toolName, _ := part["tool"].(string)
		var argsStr string
		if st, ok := part["state"].(map[string]interface{}); ok {
			if input, ok := st["input"]; ok {
				b, _ := json.Marshal(input)
				argsStr = string(b)
			}
		}
		return decodeResult{events: []bus.Event{&bus.WorkerToolCall{
			TaskID:    taskID,
			Tool:      toolName,
			Args:      argsStr,
			Timestamp: now,
		}}}

	case "text":
		if text, _ := part["text"].(string); text != "" {
			return decodeResult{events: []bus.Event{&bus.WorkerAgentMessage{
				TaskID:    taskID,
				Kind:      "message",
				Text:      text,
				Timestamp: now,
			}}}
		}
	}

	return decodeResult{}
}
