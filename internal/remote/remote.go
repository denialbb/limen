// Package remote provides NDJSON-subprocess adapters satisfying the
// orchestrator.Router / orchestrator.Worker / orchestrator.Validator
// interfaces.  Each adapter launches a fresh Python subprocess per
// orchestration call (stateless, one invocation per process).
//
// The worker adapter runs a bidirectional synchronous decode loop inline
// on the calling goroutine: on tool_request it handles file.write
// callbacks, writes the tool_response, and loops; on the result event it
// returns.  Router and validator adapters are one-shot reads.
//
// Subprocess lifecycle: ctx.Done() triggers SIGTERM, then SIGKILL after
// a configurable grace period.  The grace period defaults to
// DefaultShutdownTimeout (5s) and can be overridden per adapter via the
// ShutdownTimeoutOption.
package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/denialbb/limen/internal/bus"
	"github.com/denialbb/limen/internal/git"
	"github.com/denialbb/limen/internal/ndjson"
	"github.com/denialbb/limen/internal/orchestrator"
	"github.com/denialbb/limen/internal/state"
)

// DefaultShutdownTimeout is the grace period between SIGTERM and SIGKILL
// when a subprocess is cancelled via ctx.Done().
const DefaultShutdownTimeout = 5 * time.Second

// requestEnvelope is the common outer wrapper for every NDJSON request
// sent from Go to the Python subprocess.
type requestEnvelope struct {
	Task    json.RawMessage `json:"task"`
	Attempt int             `json:"attempt"`
	// Worker-only: the validator's feedback from the prior attempt.
	Feedback string `json:"feedback,omitempty"`
	// Validator-only: the captured worktree diff.
	WorktreeDiff string `json:"worktree_diff,omitempty"`
}

// taskRequest is the task payload embedded in every request envelope.
type taskRequest struct {
	ID              string `json:"id"`
	Description     string `json:"description,omitempty"`
	ContextSnapshot string `json:"context_snapshot,omitempty"`
}

// --- subprocess lifecycle -------------------------------------------------

// subprocess wraps an exec.Cmd and its attached NDJSON encoder/decoder
// with a graceful shutdown goroutine watching ctx.Done().
type subprocess struct {
	cmd     *exec.Cmd
	enc     *ndjson.Encoder
	dec     *ndjson.Decoder
	cancel  context.CancelFunc // watcher's cancellation
	closer  io.Closer          // the stdin pipe (for explicit close on teardown)
	timeout time.Duration
}

// newSubprocess starts cmd, attaches encoder (to stdin) / decoder (from stdout),
// and launches a background goroutine that watches ctx.Done().
func newSubprocess(ctx context.Context, cmd *exec.Cmd, timeout time.Duration, logFile *os.File) (*subprocess, error) {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("remote: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("remote: stdout pipe: %w", err)
	}
	// Capture stderr for diagnostics; the subprocess should not write to
	// stderr except for unexpected crashes.
	if logFile != nil {
		cmd.Stderr = logFile
	} else {
		cmd.Stderr = os.Stderr
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("remote: start subprocess: %w", err)
	}

	sp := &subprocess{
		cmd:     cmd,
		enc:     ndjson.NewEncoder(stdin),
		dec:     ndjson.NewDecoder(stdout),
		closer:  stdin,
		timeout: timeout,
	}

	// Launch the shutdown watcher.
	watcherCtx, cancel := context.WithCancel(context.Background())
	sp.cancel = cancel
	go sp.watchShutdown(watcherCtx, ctx)

	return sp, nil
}

// watchShutdown blocks until either the subprocess exits or ctx is
// cancelled.  On cancellation it sends SIGTERM, waits for timeout, then
// sends SIGKILL.
func (sp *subprocess) watchShutdown(watcherCtx, parentCtx context.Context) {
	select {
	case <-watcherCtx.Done():
		// Explicit teardown via shutdown().
		return
	case <-parentCtx.Done():
	}

	// NODE: Send SIGTERM first so the subprocess can clean up (flush
	// buffers, close files, etc.).  Close stdin so a subprocess reading
	// from stdin sees EOF as an additional shutdown signal.
	_ = sp.cmd.Process.Signal(syscall.SIGTERM)
	_ = sp.closer.Close()

	// Wait for the process to exit after SIGTERM
	graceful := make(chan struct{}, 1)
	go func() {
		_ = sp.cmd.Wait()
		graceful <- struct{}{}
	}()

	select {
	case <-graceful:
		return
	case <-time.After(sp.timeout):
		// Grace period expired; force-kill the process group.
		_ = sp.cmd.Process.Kill()
		<-graceful // drain
	}
}

// shutdown tears down the subprocess: closes stdin, cancels the watcher,
// and waits for the process to exit.
func (sp *subprocess) shutdown() {
	_ = sp.closer.Close()
	sp.cancel()
	_ = sp.cmd.Wait()
}

// --- adapter option -------------------------------------------------------

// Option configures an adapter constructor.
type Option func(*options)

type options struct {
	shutdownTimeout time.Duration
	logDir          string
}

func defaultOptions() *options {
	return &options{
		shutdownTimeout: DefaultShutdownTimeout,
	}
}

// WithShutdownTimeout overrides the grace period between SIGTERM and
// SIGKILL for the subprocess.
func WithShutdownTimeout(d time.Duration) Option {
	return func(o *options) {
		o.shutdownTimeout = d
	}
}

// WithLogDir sets the directory where subprocess logs should be saved.
func WithLogDir(dir string) Option {
	return func(o *options) {
		o.logDir = dir
	}
}

// --- ndjsonRouter ---------------------------------------------------------

// ndjsonRouter implements orchestrator.Router by launching a single-shot
// Python subprocess (one decoder read for the result event).
type ndjsonRouter struct {
	cmdArgs []string // e.g. {"python", "-m", "limen.mock.router", "transcript.json"}
	opts    *options
}

// NewRouter creates a Router adapter backed by the given subprocess
// command.  args is the full argv slice (including the binary).
func NewRouter(args []string, opts ...Option) orchestrator.Router {
	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}
	return &ndjsonRouter{cmdArgs: args, opts: o}
}

// Evaluate implements orchestrator.Router.
func (r *ndjsonRouter) Evaluate(ctx context.Context, task *state.Task, em orchestrator.Emitter) (orchestrator.RouterDecision, error) {
	req := requestEnvelope{
		Task:    mustMarshalJSON(taskRequest{ID: task.ID, Description: task.ID, ContextSnapshot: task.ContextSnapshot}),
		Attempt: 1,
	}

	result, err := r.invoke(ctx, req, task.ID)
	if err != nil {
		return "", err
	}

	decision, _ := result["decision"].(string)
	// Map from transcript decision strings to orchestrator constants.
	var orbDecision orchestrator.RouterDecision
	switch decision {
	case "proceed":
		orbDecision = orchestrator.DecisionProceed
	case "expand":
		orbDecision = orchestrator.DecisionExpand
	case "escalate":
		orbDecision = orchestrator.DecisionEscalate
	default:
		orbDecision = orchestrator.DecisionProceed
	}

	// Emit RouterDecisionEvent to the bus for TUI display.
	if em != nil {
		rationale, _ := result["rationale"].(string)
		em.Publish(&bus.RouterDecisionEvent{
			TaskID:      task.ID,
			Decision:    bus.RouterDecision(orbDecision),
			Rationale:   rationale,
			ExpandCount: 0,
			Timestamp:   time.Now(),
		})
	}

	return orbDecision, nil
}

// invoke launches the subprocess, sends the request envelope, reads
// exactly one result event, and returns the decoded payload.
func (r *ndjsonRouter) invoke(ctx context.Context, req requestEnvelope, taskID string) (map[string]any, error) {
	cmd := exec.Command(r.cmdArgs[0], r.cmdArgs[1:]...)
	var logFile *os.File
	if r.opts.logDir != "" {
		logPath := filepath.Join(r.opts.logDir, fmt.Sprintf("%s-router.log", taskID))
		if err := os.MkdirAll(r.opts.logDir, 0755); err == nil {
			if f, errOpen := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); errOpen == nil {
				logFile = f
				defer logFile.Close()
			}
		}
	}
	sp, err := newSubprocess(ctx, cmd, r.opts.shutdownTimeout, logFile)
	if err != nil {
		return nil, err
	}
	defer sp.shutdown()

	if err := sp.enc.Encode(requestToEnvelope(req)); err != nil {
		return nil, fmt.Errorf("remote: write router request: %w", err)
	}

	return readResultEvent(sp.dec)
}

// --- ndjsonWorker ---------------------------------------------------------

// ndjsonWorker implements orchestrator.Worker by launching a Python
// subprocess and running a synchronous decode loop.  On tool_request
// (kind=tool_request) it dispatches file.write inline; on the result
// event (kind=event) it returns.
type ndjsonWorker struct {
	cmdArgs []string
	opts    *options
}

// NewWorker creates a Worker adapter backed by the given subprocess
// command.
func NewWorker(args []string, opts ...Option) orchestrator.Worker {
	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}
	return &ndjsonWorker{cmdArgs: args, opts: o}
}

// ProduceSolution implements orchestrator.Worker.
func (w *ndjsonWorker) ProduceSolution(ctx context.Context, task *state.Task, wt *git.Worktree, feedback string, em orchestrator.Emitter) error {
	req := requestEnvelope{
		Task:     mustMarshalJSON(taskRequest{ID: task.ID, Description: task.ID}),
		Feedback: feedback,
		Attempt:  task.RetryCount + 1,
	}

	cmd := exec.Command(w.cmdArgs[0], w.cmdArgs[1:]...)
	var logFile *os.File
	if w.opts.logDir != "" {
		logPath := filepath.Join(w.opts.logDir, fmt.Sprintf("%s-worker.log", task.ID))
		if err := os.MkdirAll(w.opts.logDir, 0755); err == nil {
			if f, errOpen := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); errOpen == nil {
				logFile = f
				defer logFile.Close()
			}
		}
	}
	sp, err := newSubprocess(ctx, cmd, w.opts.shutdownTimeout, logFile)
	if err != nil {
		return err
	}
	defer sp.shutdown()

	if err := sp.enc.Encode(requestToEnvelope(req)); err != nil {
		return fmt.Errorf("remote: write worker request: %w", err)
	}

	// Synchronous decode loop: dispatch tool_requests inline, return on
	// the result event.
	for {
		env, err := sp.dec.Decode()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return fmt.Errorf("remote: worker subprocess exited before result event")
			}
			// Check for context cancellation.
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("remote: worker decode: %w", err)
		}

		switch env.Kind {
		case ndjson.KindToolRequest:
			if err := w.dispatchToolRequest(sp.enc, env, wt, task.ID, em); err != nil {
				return err
			}
		case ndjson.KindEvent:
			if env.Event == nil {
				continue
			}
			// Check for error events (e.g. transcript exhaustion).
			if env.Event.Type == "error" {
				payload, _ := decodePayload(env.Event.Payload)
				return fmt.Errorf("remote: worker error: %v", payload["error"])
			}
			// Result event — worker finished successfully.
			return nil
		default:
			// Ignore other envelope kinds; only tool_request and event
			// are expected from the worker.
		}
	}
}

// dispatchToolRequest handles a single tool_request from the worker.
// Currently only file.write is supported.
func (w *ndjsonWorker) dispatchToolRequest(enc *ndjson.Encoder, env *ndjson.Envelope, wt *git.Worktree, taskID string, em orchestrator.Emitter) error {
	req := env.ToolReq
	if req == nil {
		return fmt.Errorf("remote: tool_request with nil ToolReq")
	}

	if em != nil {
		em.Publish(&bus.WorkerToolCall{
			TaskID:    taskID,
			Tool:      req.Tool,
			Args:      string(req.Args),
			Timestamp: time.Now(),
		})
	}

	var args map[string]any
	if err := json.Unmarshal(req.Args, &args); err != nil {
		return writeToolError(enc, req.ID, fmt.Sprintf("invalid args: %v", err))
	}

	switch req.Tool {
	case ndjson.ToolFileWrite:
		return w.handleFileWrite(enc, req.ID, args, wt, taskID, em)
	default:
		return writeToolError(enc, req.ID, fmt.Sprintf("unknown tool: %q", req.Tool))
	}
}

// handleFileWrite writes a file to the worktree, rejecting path escapes.
func (w *ndjsonWorker) handleFileWrite(enc *ndjson.Encoder, reqID string, args map[string]any, wt *git.Worktree, taskID string, em orchestrator.Emitter) error {
	path, _ := args["path"].(string)
	content, _ := args["content"].(string)

	if path == "" {
		return writeToolError(enc, reqID, "file.write: path is empty")
	}

	// Reject path-traversal escapes.
	if strings.Contains(path, "../") || strings.Contains(path, "..\\") {
		return writeToolError(enc, reqID, fmt.Sprintf("file.write: path %q contains ../ escape", path))
	}
	// Reject absolute paths — all writes must be relative to the worktree root.
	if filepath.IsAbs(path) {
		return writeToolError(enc, reqID, fmt.Sprintf("file.write: path %q is absolute; must be relative", path))
	}

	absPath := filepath.Join(wt.Path, path)
	// Create parent directories if needed.
	if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
		return writeToolError(enc, reqID, fmt.Sprintf("file.write: mkdir: %v", err))
	}
	if err := os.WriteFile(absPath, []byte(content), 0644); err != nil {
		return writeToolError(enc, reqID, fmt.Sprintf("file.write: write: %v", err))
	}

	if em != nil {
		em.Publish(&bus.WorkerFileEdit{
			TaskID:    taskID,
			Path:      path,
			Op:        "write",
			Timestamp: time.Now(),
		})
	}

	return enc.EncodeToolResponse(&ndjson.ToolResponse{
		ID:     reqID,
		OK:     true,
		Result: mustMarshalJSON(map[string]any{"path": path}),
	})
}

// --- ndjsonValidator ------------------------------------------------------

// ndjsonValidator implements orchestrator.Validator by launching a
// single-shot Python subprocess (one decoder read for the result event).
type ndjsonValidator struct {
	cmdArgs []string
	opts    *options
	git     orchestrator.GitClient
}

// NewValidator creates a Validator adapter backed by the given subprocess
// command.  It requires a GitClient so it can capture the worktree diff
// before sending the request.
func NewValidator(args []string, git orchestrator.GitClient, opts ...Option) orchestrator.Validator {
	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}
	return &ndjsonValidator{cmdArgs: args, opts: o, git: git}
}

// Evaluate implements orchestrator.Validator.
func (v *ndjsonValidator) Evaluate(ctx context.Context, task *state.Task, wt *git.Worktree, em orchestrator.Emitter) (bool, string, error) {
	diff, err := v.git.GetWorktreeDiff(ctx, wt)
	if err != nil {
		return false, "", fmt.Errorf("remote: capture worktree diff: %w", err)
	}

	req := requestEnvelope{
		Task:         mustMarshalJSON(taskRequest{ID: task.ID, Description: task.ID}),
		WorktreeDiff: diff,
		Attempt:      task.RetryCount + 1,
	}

	cmd := exec.Command(v.cmdArgs[0], v.cmdArgs[1:]...)
	var logFile *os.File
	if v.opts.logDir != "" {
		logPath := filepath.Join(v.opts.logDir, fmt.Sprintf("%s-validator.log", task.ID))
		if err := os.MkdirAll(v.opts.logDir, 0755); err == nil {
			if f, errOpen := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); errOpen == nil {
				logFile = f
				defer logFile.Close()
			}
		}
	}
	sp, err := newSubprocess(ctx, cmd, v.opts.shutdownTimeout, logFile)
	if err != nil {
		return false, "", err
	}
	defer sp.shutdown()

	if err := sp.enc.Encode(requestToEnvelope(req)); err != nil {
		return false, "", fmt.Errorf("remote: write validator request: %w", err)
	}

	for {
		env, err := sp.dec.Decode()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return false, "", fmt.Errorf("remote: validator subprocess exited before verdict event")
			}
			if ctx.Err() != nil {
				return false, "", ctx.Err()
			}
			return false, "", fmt.Errorf("remote: validator decode: %w", err)
		}

		if env.Kind != ndjson.KindEvent || env.Event == nil {
			continue
		}

		switch env.Event.Type {
		case "error":
			payload, _ := decodePayload(env.Event.Payload)
			return false, "", fmt.Errorf("remote: validator error: %v", payload["error"])

		case "validator.examining":
			if em != nil {
				payload, _ := decodePayload(env.Event.Payload)
				var criteria []string
				if rawCriteria, ok := payload["criteria"]; ok {
					if list, ok := rawCriteria.([]any); ok {
						for _, item := range list {
							if s, ok := item.(string); ok {
								criteria = append(criteria, s)
							}
						}
					}
				}
				em.Publish(&bus.ValidatorExamining{
					TaskID:    task.ID,
					Criteria:  criteria,
					Timestamp: time.Now(),
				})
			}

		case "validator.criterion_result":
			if em != nil {
				payload, _ := decodePayload(env.Event.Payload)
				criterion, _ := payload["criterion"].(string)
				passed, _ := payload["passed"].(bool)
				detail, _ := payload["detail"].(string)
				em.Publish(&bus.ValidatorCriterionResult{
					TaskID:    task.ID,
					Criterion: criterion,
					Passed:    passed,
					Detail:    detail,
					Timestamp: time.Now(),
				})
			}

		case "validator.verdict":
			payload, _ := decodePayload(env.Event.Payload)
			passes, _ := payload["passes"].(bool)
			feedback, _ := payload["feedback"].(string)

			if em != nil {
				em.Publish(&bus.ValidatorVerdict{
					TaskID:    task.ID,
					Passes:    passes,
					Feedback:  feedback,
					Timestamp: time.Now(),
				})
			}
			return passes, feedback, nil
		}
	}
}

// --- helpers ---------------------------------------------------------------

// requestToEnvelope wraps a requestEnvelope in an ndjson.Envelope of
// kind "event" (the Go side sends requests as event envelopes so the
// Python side can receive them with a single decoder read).
func requestToEnvelope(req requestEnvelope) *ndjson.Envelope {
	return &ndjson.Envelope{
		Kind: ndjson.KindEvent,
		Event: &ndjson.EventEnvelope{
			Type:      "request",
			TaskID:    "",
			Payload:   mustMarshalJSON(req),
			Timestamp: time.Now().UnixMilli(),
		},
	}
}

// readResultEvent reads exactly one envelope from the decoder and
// returns the decoded result payload.  Error events are translated into
// Go errors.
func readResultEvent(dec *ndjson.Decoder) (map[string]any, error) {
	env, err := dec.Decode()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("remote: subprocess exited before result event")
		}
		return nil, fmt.Errorf("remote: decode result: %w", err)
	}

	if env.Kind != ndjson.KindEvent || env.Event == nil {
		return nil, fmt.Errorf("remote: expected event envelope, got kind=%q", env.Kind)
	}

	if env.Event.Type == "error" {
		payload, _ := decodePayload(env.Event.Payload)
		return nil, fmt.Errorf("remote: subprocess error: %v", payload["error"])
	}

	return decodePayload(env.Event.Payload)
}

// decodePayload unmarshals a JSON raw message into a map, returning an
// empty map on failure.
func decodePayload(raw json.RawMessage) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return map[string]any{}, err
	}
	return m, nil
}

// writeToolError writes a tool_response with ok=false and the error
// string, then returns the error as a Go error.
func writeToolError(enc *ndjson.Encoder, id, errMsg string) error {
	_ = enc.EncodeToolResponse(&ndjson.ToolResponse{
		ID:    id,
		OK:    false,
		Error: errMsg,
	})
	return fmt.Errorf("remote: %s", errMsg)
}

// mustMarshalJSON is a test/initialisation-only helper that panics on
// marshal failure.  It is safe because all inputs are structs with only
// JSON-serializable fields.
func mustMarshalJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("remote: json marshal: %v", err))
	}
	return json.RawMessage(data)
}

// Compile-time interface satisfaction checks.
var (
	_ orchestrator.Router    = (*ndjsonRouter)(nil)
	_ orchestrator.Worker    = (*ndjsonWorker)(nil)
	_ orchestrator.Validator = (*ndjsonValidator)(nil)
)
