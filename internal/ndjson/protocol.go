// Package ndjson implements the bidirectional NDJSON envelope used by Limen's
// interactive mode to communicate between the in-process Go Core and the
// Python L1/L2/L3 subprocess clients.
//
// Each line on the transport is one JSON object. Three payload kinds flow over
// the wire:
//
//   - event:          Python streams live activity to Go (EventEnvelope).
//   - tool_request:   Python invokes a Go Core tool (ToolRequest).
//   - tool_response:  Go replies to a tool request (ToolResponse).
//
// The Envelope discriminated union wraps all three so a single reader can
// dispatch every incoming line. See .agents/docs/interactive_tui.md for the
// architecture and event taxonomy.
package ndjson

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// Envelope kind constants. These populate Envelope.Kind and select which
// pointer field carries the payload.
const (
	KindEvent        = "event"
	KindToolRequest  = "tool_request"
	KindToolResponse = "tool_response"
)

// Event type constants. Dot-namespaced per the taxonomy in
// .agents/docs/interactive_tui.md. These are advisory labels emitted by the
// Python clients; the Go side does not enforce them but relies on them for
// routing into TUI tabs.
const (
	EventTypeTaskStateChanged   = "task.state_changed"
	EventTypeContextBuilt       = "context.built"
	EventTypeRouterExamining    = "router.examining"
	EventTypeRouterDecision     = "router.decision"
	EventTypeWorkerStarted      = "worker.started"
	EventTypeWorkerToolCall     = "worker.tool_call"
	EventTypeWorkerFileEdit     = "worker.file_edit"
	EventTypeWorkerFinished     = "worker.finished"
	EventTypeValidatorExamining = "validator.examining"
	EventTypeValidatorCriterion = "validator.criterion_result"
	EventTypeValidatorVerdict   = "validator.verdict"
	EventTypeConflictDetected   = "conflict.detected"
	EventTypeTaskFinalized      = "task.finalized"
)

// Tool name constants. These name the RPC tools the Python clients may invoke
// on the Go Core, mapping onto the existing orchestrator/state surfaces.
const (
	ToolStateTransition  = "state.transition"
	ToolStateGetTask     = "state.get_task"
	ToolStateRecordTool  = "state.record_tool_call"
	ToolGitProvision     = "git.provision_worktree"
	ToolGitCheckConflict = "git.check_conflicts"
	ToolGitGetDiff       = "git.get_diff"
	ToolGitCommit        = "git.commit_worktree"
	ToolGitDestroy       = "git.destroy_worktree"
	ToolFileWrite        = "file.write"
)

// EventEnvelope is emitted by a Python client to stream live activity to Go.
type EventEnvelope struct {
	Type      string          `json:"type"`
	TaskID    string          `json:"task_id"`
	Payload   json.RawMessage `json:"payload"`
	Timestamp int64           `json:"timestamp"`
}

// ToolRequest is emitted by a Python client to invoke a Go Core tool.
type ToolRequest struct {
	ID   string          `json:"id"`
	Tool string          `json:"tool"`
	Args json.RawMessage `json:"args"`
}

// ToolResponse is written by Go back to the Python client's stdin.
type ToolResponse struct {
	ID     string          `json:"id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// Envelope is the discriminated union wrapping the three payload kinds so a
// single reader can dispatch every incoming line. Exactly one of the pointer
// fields is expected to be non-nil, keyed by Kind.
type Envelope struct {
	Kind     string         `json:"kind"`
	Event    *EventEnvelope `json:"event,omitempty"`
	ToolReq  *ToolRequest   `json:"tool_request,omitempty"`
	ToolResp *ToolResponse  `json:"tool_response,omitempty"`
}

// idCounter is a per-process sequence appended to random IDs to guarantee
// uniqueness even if the random source or clock collides.
var idCounter uint64

// GenerateID returns a unique request ID for tool requests. It combines a
// 8-byte cryptographically random hex token, a unix-nanosecond timestamp, and
// a monotonically increasing counter. The combination is unique within a
// session without depending on an external UUID dependency.
func GenerateID() string {
	var buf [8]byte
	// NOTE: crypto/rand.Read is not expected to fail on supported platforms;
	// fall back to timestamp-only if it does so the helper never panics.
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("%x-%d", time.Now().UnixNano(), atomic.AddUint64(&idCounter, 1))
	}
	seq := atomic.AddUint64(&idCounter, 1)
	return fmt.Sprintf("%s-%016x-%d", hex.EncodeToString(buf[:]), time.Now().UnixNano(), seq)
}

// Encoder writes NDJSON envelopes to an io.Writer. It is safe for concurrent
// use: a mutex serializes writes so lines never interleave.
type Encoder struct {
	mu sync.Mutex
	w  io.Writer
}

// NewEncoder returns an Encoder that writes to w.
func NewEncoder(w io.Writer) *Encoder {
	return &Encoder{w: w}
}

// Encode marshals env to JSON, appends a newline, and writes the line
// atomically under the encoder's mutex.
func (e *Encoder) Encode(env *Envelope) error {
	if env == nil {
		return fmt.Errorf("ndjson: cannot encode nil envelope")
	}
	data, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("ndjson: marshal envelope: %w", err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	// Write data + newline in a single Write call so concurrent encoders and
	// shared writers (e.g. os.File, pipe) never observe torn lines.
	if _, err := e.w.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("ndjson: write envelope: %w", err)
	}
	return nil
}

// EncodeEvent wraps ev in an Envelope of kind "event" and writes it.
func (e *Encoder) EncodeEvent(ev *EventEnvelope) error {
	if ev == nil {
		return fmt.Errorf("ndjson: cannot encode nil event")
	}
	return e.Encode(&Envelope{Kind: KindEvent, Event: ev})
}

// EncodeToolRequest wraps req in an Envelope of kind "tool_request" and writes it.
func (e *Encoder) EncodeToolRequest(req *ToolRequest) error {
	if req == nil {
		return fmt.Errorf("ndjson: cannot encode nil tool request")
	}
	return e.Encode(&Envelope{Kind: KindToolRequest, ToolReq: req})
}

// EncodeToolResponse wraps resp in an Envelope of kind "tool_response" and writes it.
func (e *Encoder) EncodeToolResponse(resp *ToolResponse) error {
	if resp == nil {
		return fmt.Errorf("ndjson: cannot encode nil tool response")
	}
	return e.Encode(&Envelope{Kind: KindToolResponse, ToolResp: resp})
}

// Decoder reads NDJSON envelopes from an io.Reader one line at a time. It is
// not safe for concurrent use; a single reader goroutine should own it.
type Decoder struct {
	scanner *bufio.Scanner
}

// NewDecoder returns a Decoder reading from r. The decoder uses a
// bufio.Scanner with an enlarged buffer to accommodate large payloads.
func NewDecoder(r io.Reader) *Decoder {
	sc := bufio.NewScanner(r)
	// NOTE: Allow up to 1 MiB per line for large tool args / payloads without
	// truncating silently. bufio.MaxScanTokenSize is ~64 KiB by default.
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	return &Decoder{scanner: sc}
}

// Decode reads one line, unmarshals it into an Envelope, and returns it.
// Blank or whitespace-only lines are skipped: Python print/logging output can
// introduce stray empty lines between envelopes, and json.Unmarshal([]byte(""))
// would otherwise fail with "unexpected end of JSON input". Malformed JSON
// returns an error wrapping the offending line content for debugging; it never
// panics. When the underlying reader is exhausted, Decode returns io.EOF.
func (d *Decoder) Decode() (*Envelope, error) {
	for {
		if !d.scanner.Scan() {
			if err := d.scanner.Err(); err != nil {
				return nil, fmt.Errorf("ndjson: scan: %w", err)
			}
			return nil, io.EOF
		}
		line := d.scanner.Bytes()
		// Skip blank or whitespace-only lines between envelopes.
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		buffered := string(line)

		var env Envelope
		if err := json.Unmarshal(line, &env); err != nil {
			return nil, fmt.Errorf("ndjson: unmarshal line %q: %w", buffered, err)
		}
		return &env, nil
	}
}
