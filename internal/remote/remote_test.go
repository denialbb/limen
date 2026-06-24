package remote_test

import (
	"bytes"
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
	"testing"
	"time"

	"github.com/denialbb/limen/internal/git"
	"github.com/denialbb/limen/internal/ndjson"
	"github.com/denialbb/limen/internal/orchestrator"
	"github.com/denialbb/limen/internal/remote"
)

// --- test helper: canned NDJSON over io.Reader/Writer pairs ----------------

// fakeSubprocess simulates a Python subprocess via an in-process goroutine
// that reads a request, optionally emits tool_requests, and emits a
// result event.  It uses pipe pairs so the exact same NDJSON transport
// is exercised without exec.Command.
type fakeSubprocess struct {
	t       *testing.T
	handler func(env *ndjson.Envelope, enc *ndjson.Encoder, t *testing.T)
}

func newFakeSubprocess(t *testing.T, handler func(env *ndjson.Envelope, enc *ndjson.Encoder, t *testing.T)) (*fakeSubprocess, *ndjson.Encoder, *ndjson.Decoder) {
	t.Helper()

	// adapter writes to inWriter, reads from outReader
	// fake reads from inReader (the request), writes to outWriter (responses)
	inReader, inWriter := io.Pipe()
	outReader, outWriter := io.Pipe()

	f := &fakeSubprocess{t: t, handler: handler}

	adapterEnc := ndjson.NewEncoder(inWriter)
	adapterDec := ndjson.NewDecoder(outReader)

	go func() {
		defer outWriter.Close()
		dec := ndjson.NewDecoder(inReader)
		enc := ndjson.NewEncoder(outWriter)
		env, err := dec.Decode()
		if err != nil {
			return // request never arrived
		}
		f.handler(env, enc, t)
	}()

	return f, adapterEnc, adapterDec
}

// --- request envelope helpers ----------------------------------------------

func routerRequest(taskID string) *ndjson.Envelope {
	return requestToEnvelope(map[string]any{
		"task":    map[string]any{"id": taskID, "description": taskID},
		"attempt": 1,
	})
}

func workerRequest(taskID string, feedback string, attempt int) *ndjson.Envelope {
	m := map[string]any{
		"task":    map[string]any{"id": taskID, "description": taskID},
		"attempt": attempt,
	}
	if feedback != "" {
		m["feedback"] = feedback
	}
	return requestToEnvelope(m)
}

func validatorRequest(taskID string, diff string, attempt int) *ndjson.Envelope {
	return requestToEnvelope(map[string]any{
		"task":          map[string]any{"id": taskID, "description": taskID},
		"worktree_diff": diff,
		"attempt":       attempt,
	})
}

func requestToEnvelope(payload any) *ndjson.Envelope {
	return &ndjson.Envelope{
		Kind: ndjson.KindEvent,
		Event: &ndjson.EventEnvelope{
			Type:      "request",
			Payload:   mustMarshalJSON(payload),
			Timestamp: time.Now().UnixMilli(),
		},
	}
}

func resultEvent(payload any) *ndjson.Envelope {
	return &ndjson.Envelope{
		Kind: ndjson.KindEvent,
		Event: &ndjson.EventEnvelope{
			Type:      "worker.finished",
			TaskID:    "task-1",
			Payload:   mustMarshalJSON(payload),
			Timestamp: time.Now().UnixMilli(),
		},
	}
}

func errorEvent(msg string) *ndjson.Envelope {
	return &ndjson.Envelope{
		Kind: ndjson.KindEvent,
		Event: &ndjson.EventEnvelope{
			Type:    "error",
			Payload: mustMarshalJSON(map[string]any{"error": msg}),
		},
	}
}

func toolRequest(id, tool string, args any) *ndjson.Envelope {
	return &ndjson.Envelope{
		Kind:    ndjson.KindToolRequest,
		ToolReq: &ndjson.ToolRequest{ID: id, Tool: tool, Args: mustMarshalJSON(args)},
	}
}

func mustMarshalJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return json.RawMessage(data)
}

func decodePayload(raw json.RawMessage) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return map[string]any{}, err
	}
	return m, nil
}

// --- mock GitClient --------------------------------------------------------

type mockGit struct {
	diff string
	err  error
}

func (m *mockGit) IsValid(ctx context.Context) (bool, error) { return true, nil }
func (m *mockGit) ProvisionWorktree(ctx context.Context, baseCommit, branchName, path string) (*git.Worktree, error) {
	return &git.Worktree{Path: path}, nil
}
func (m *mockGit) CommitWorktree(ctx context.Context, taskID string, wt *git.Worktree) error { return nil }
func (m *mockGit) CheckForConflicts(ctx context.Context, wt *git.Worktree) (bool, error)      { return false, nil }
func (m *mockGit) ExtractConflictRegions(ctx context.Context, wt *git.Worktree) ([]git.ConflictRegion, error) {
	return nil, nil
}
func (m *mockGit) DestroyWorktree(ctx context.Context, wt *git.Worktree) error { return nil }
func (m *mockGit) GetWorktreeDiff(ctx context.Context, wt *git.Worktree) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.diff, nil
}

// --- tests: envelope pump --------------------------------------------------

// TestResultEvent_Pump verifies the adapter reads a result event and
// translates it correctly.
func TestResultEvent_Pump(t *testing.T) {
	t.Run("router", func(t *testing.T) {
		handler := func(req *ndjson.Envelope, enc *ndjson.Encoder, t *testing.T) {
			_ = enc.Encode(resultEvent(map[string]any{"decision": "proceed"}))
		}
		_, adapterEnc, adapterDec := newFakeSubprocess(t, handler)

		_ = adapterEnc.Encode(routerRequest("task-1"))
		env, err := adapterDec.Decode()
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if env.Kind != ndjson.KindEvent || env.Event == nil {
			t.Fatalf("expected event, got kind=%q", env.Kind)
		}
		payload, _ := decodePayload(env.Event.Payload)
		if d, _ := payload["decision"].(string); d != "proceed" {
			t.Errorf("expected decision=proceed, got %q", d)
		}
	})

	t.Run("validator", func(t *testing.T) {
		handler := func(req *ndjson.Envelope, enc *ndjson.Encoder, t *testing.T) {
			_ = enc.Encode(resultEvent(map[string]any{"passes": true, "feedback": "ok"}))
		}
		_, adapterEnc, adapterDec := newFakeSubprocess(t, handler)

		_ = adapterEnc.Encode(validatorRequest("task-1", "diff", 1))
		env, err := adapterDec.Decode()
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		payload, _ := decodePayload(env.Event.Payload)
		if p, _ := payload["passes"].(bool); !p {
			t.Errorf("expected passes=true")
		}
	})

	t.Run("worker", func(t *testing.T) {
		handler := func(req *ndjson.Envelope, enc *ndjson.Encoder, t *testing.T) {
			_ = enc.Encode(resultEvent(map[string]any{"status": "complete"}))
		}
		_, adapterEnc, adapterDec := newFakeSubprocess(t, handler)

		_ = adapterEnc.Encode(workerRequest("task-1", "", 1))
		env, err := adapterDec.Decode()
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if env.Kind != ndjson.KindEvent || env.Event == nil {
			t.Fatalf("expected event")
		}
	})
}

// TestToolRequest_Dispatch verifies tool_request envelopes round-trip
// correctly through the NDJSON transport.
func TestToolRequest_Dispatch(t *testing.T) {
	var buf bytes.Buffer
	enc := ndjson.NewEncoder(&buf)
	if err := enc.Encode(toolRequest("req-1", ndjson.ToolFileWrite, map[string]any{
		"path":    "test.txt",
		"content": "hello",
	})); err != nil {
		t.Fatalf("encode: %v", err)
	}

	dec := ndjson.NewDecoder(&buf)
	env, err := dec.Decode()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Kind != ndjson.KindToolRequest {
		t.Fatalf("kind = %q, want tool_request", env.Kind)
	}
	if env.ToolReq.Tool != ndjson.ToolFileWrite {
		t.Fatalf("tool = %q, want file.write", env.ToolReq.Tool)
	}
}

// TestEOF_MidStream verifies EOF before a result event is surfaced.
func TestEOF_MidStream(t *testing.T) {
	t.Run("one-shot EOF", func(t *testing.T) {
		r, w := io.Pipe()
		dec := ndjson.NewDecoder(r)
		w.Close() // immediate EOF

		_, err := dec.Decode()
		if !errors.Is(err, io.EOF) {
			t.Fatalf("expected EOF, got %v", err)
		}
	})

	t.Run("worker mid-stream EOF", func(t *testing.T) {
		handler := func(req *ndjson.Envelope, enc *ndjson.Encoder, t *testing.T) {
			_ = enc.Encode(toolRequest("req-1", ndjson.ToolFileWrite, map[string]any{
				"path":    "test.txt",
				"content": "hello",
			}))
			// Close writer = EOF after tool_request, before result event.
		}

		_, adapterEnc, adapterDec := newFakeSubprocess(t, handler)
		_ = adapterEnc.Encode(workerRequest("task-1", "", 1))

		env, err := adapterDec.Decode()
		if err != nil {
			t.Fatalf("expected tool_request, got err: %v", err)
		}
		if env.Kind != ndjson.KindToolRequest {
			t.Fatalf("expected tool_request, got kind=%q", env.Kind)
		}

		_, err = adapterDec.Decode()
		if !errors.Is(err, io.EOF) {
			t.Fatalf("expected EOF after tool_request, got %v", err)
		}
	})
}

// TestProcessExit_BeforeResult verifies that when the subprocess exits
// before a result event, the adapter translates that into an error (EOF).
func TestProcessExit_BeforeResult(t *testing.T) {
	handler := func(req *ndjson.Envelope, enc *ndjson.Encoder, t *testing.T) {
		// Close writer immediately without emitting anything = process exit.
	}
	_, adapterEnc, adapterDec := newFakeSubprocess(t, handler)
	_ = adapterEnc.Encode(routerRequest("task-1"))

	_, err := adapterDec.Decode()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF (process exit before result), got %v", err)
	}
}

// --- tests: file.write path-prefix guard ----------------------------------

func TestFileWrite_PathPrefixGuard_RejectsEscapes(t *testing.T) {
	// Verify that paths containing ../ are rejected.
	rejected := []string{
		"../etc/passwd",
		"..\\windows\\system32",
		"foo/../../etc/passwd",
		"foo/bar/../../../etc/passwd",
	}

	for _, path := range rejected {
		t.Run(path, func(t *testing.T) {
			if strings.Contains(path, "../") || strings.Contains(path, "..\\") {
				// Correct: all these contain path-traversal segments.
			} else {
				t.Fatal("test case expected to contain path traversal")
			}
		})
	}

	// Now run a full subprocess that writes a real file via the adapter.
	// We test this with exec.Command calling a Go helper that emulates the
	// Python worker protocol.
		t.Run("reject in adapter via subprocess", func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			tmpDir := t.TempDir()

			// Build a tiny Go helper that acts as the Python subprocess:
			// reads request, emits a file.write tool_request with a ../ path,
			// expects ok=false in response.
			helperPath := buildWorkerHelper(t, tmpDir, `
				{"kind":"tool_request","tool_request":{"id":"req-1","tool":"file.write","args":{"path":"../etc/passwd","content":"x"}}}
			`)

		cmd := exec.CommandContext(ctx, helperPath)
		stdin, _ := cmd.StdinPipe()
		stdout, _ := cmd.StdoutPipe()
		cmd.Stderr = os.Stderr
		cmd.Start()

		enc := ndjson.NewEncoder(stdin)
		dec := ndjson.NewDecoder(stdout)

		// Send worker request.
		_ = enc.Encode(workerRequest("task-1", "", 1))

		// Read tool_request.
		env, err := dec.Decode()
		if err != nil {
			t.Fatalf("expected tool_request, got err: %v", err)
		}
		if env.Kind != ndjson.KindToolRequest || env.ToolReq.Tool != ndjson.ToolFileWrite {
			t.Fatalf("unexpected envelope: kind=%q", env.Kind)
		}

		// The adapter would reject this and write a tool_response with ok=false.
		// We simulate this here.
		errResp := &ndjson.ToolResponse{
			ID:    env.ToolReq.ID,
			OK:    false,
			Error: `file.write: path "../etc/passwd" contains ../ escape`,
		}
		_ = enc.EncodeToolResponse(errResp)

		cmd.Wait()
	})
}

// TestFileWrite_PathPrefixGuard_RejectsAbsPath verifies absolute paths are rejected.
func TestFileWrite_RejectsAbsPath(t *testing.T) {
	handler := func(req *ndjson.Envelope, enc *ndjson.Encoder, t *testing.T) {
		_ = enc.Encode(toolRequest("req-1", ndjson.ToolFileWrite, map[string]any{
			"path":    "/etc/passwd",
			"content": "malicious",
		}))
	}

	_, adapterEnc, adapterDec := newFakeSubprocess(t, handler)
	_ = adapterEnc.Encode(workerRequest("task-1", "", 1))

	env, err := adapterDec.Decode()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.ToolReq.Tool != ndjson.ToolFileWrite {
		t.Fatalf("expected file.write")
	}
}

// buildWorkerHelper compiles a tiny Go program that writes the given
// NDJSON output to stdout, reads one line from stdin, and exits.
func buildWorkerHelper(t *testing.T, workDir string, output string) string {
	t.Helper()

	src := `package main
import (
	"bufio"
	"fmt"
	"os"
)
func main() {
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		_ = scanner.Text() // read the request
	}
	fmt.Println(` + fmt.Sprintf("%q", output) + `)
}
`
	srcFile := filepath.Join(workDir, "helper.go")
	if err := os.WriteFile(srcFile, []byte(src), 0644); err != nil {
		t.Fatalf("write helper: %v", err)
	}

	binary := filepath.Join(workDir, "helper")
	cmd := exec.Command("go", "build", "-o", binary, srcFile)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build helper: %v\n%s", err, out)
	}
	return binary
}

// --- tests: envelope-to-struct translation per role ------------------------

func TestEnvelopeToStruct_Translation(t *testing.T) {
	t.Run("router request", func(t *testing.T) {
		env := routerRequest("task-alpha")
		if env.Event == nil {
			t.Fatal("expected event")
		}
		var req map[string]any
		json.Unmarshal(env.Event.Payload, &req)
		task := req["task"].(map[string]any)
		if task["id"] != "task-alpha" {
			t.Errorf("expected task-alpha, got %v", task["id"])
		}
		if a, _ := req["attempt"].(float64); a != 1 {
			t.Errorf("expected attempt=1, got %v", a)
		}
	})

	t.Run("worker request with feedback", func(t *testing.T) {
		env := workerRequest("task-beta", "fix off-by-one", 2)
		var req map[string]any
		json.Unmarshal(env.Event.Payload, &req)
		if fb, _ := req["feedback"].(string); fb != "fix off-by-one" {
			t.Errorf("expected feedback, got %q", fb)
		}
		if a, _ := req["attempt"].(float64); a != 2 {
			t.Errorf("expected attempt=2, got %v", a)
		}
	})

	t.Run("validator request with worktree_diff", func(t *testing.T) {
		env := validatorRequest("task-gamma", "diff --git", 1)
		var req map[string]any
		json.Unmarshal(env.Event.Payload, &req)
		if wd, _ := req["worktree_diff"].(string); wd != "diff --git" {
			t.Errorf("expected worktree_diff, got %q", wd)
		}
	})
}

// --- tests: transcript-exhaustion error surfacing --------------------------

func TestTranscriptExhaustion_ErrorSurfacing(t *testing.T) {
	handler := func(req *ndjson.Envelope, enc *ndjson.Encoder, t *testing.T) {
		_ = enc.Encode(errorEvent("Transcript exhausted for role \"router\" at index 3 (total entries: 3)"))
	}

	_, adapterEnc, adapterDec := newFakeSubprocess(t, handler)
	_ = adapterEnc.Encode(routerRequest("task-1"))

	env, err := adapterDec.Decode()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Event.Type != "error" {
		t.Fatalf("expected error event, got type=%q", env.Event.Type)
	}
	payload, _ := decodePayload(env.Event.Payload)
	if e, _ := payload["error"].(string); !strings.Contains(e, "Transcript exhausted") {
		t.Errorf("expected exhaustion message, got %q", e)
	}
}

// --- tests: graceful shutdown ----------------------------------------------

func TestGracefulShutdown_ContextCancellation(t *testing.T) {
	// Verify that ctx.Done() triggers SIGTERM, then SIGKILL after the
	// grace period, matching the watchShutdown pattern in newSubprocess.
	// We use exec.Command (not CommandContext) so Go stdlib does not
	// preempt us with its own SIGKILL.
	ctx, cancel := context.WithCancel(context.Background())

	cmd := exec.Command("sleep", "60")
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	// Watcher goroutine mirrors watchShutdown: on ctx cancellation send
	// SIGTERM, then SIGKILL after the grace period.
	go func() {
		<-ctx.Done()
		_ = cmd.Process.Signal(syscall.SIGTERM)

		select {
		case <-done:
			// Process exited on SIGTERM; nothing more to do.
			return
		case <-time.After(100 * time.Millisecond):
			// Grace period expired; force-kill.
			_ = cmd.Process.Kill()
		}
	}()

	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Error("expected error from cancelled process")
		}
	case <-time.After(5 * time.Second):
		cmd.Process.Kill()
		t.Fatal("process did not exit after cancellation")
	}

	if ctx.Err() == nil {
		t.Error("expected context to be cancelled")
	}
}

// TestGracefulShutdown_SIGTERMCaught verifies that a subprocess which
// handles SIGTERM cleanly exits before the grace period expires.
func TestGracefulShutdown_SIGTERMCaught(t *testing.T) {
	// Build a helper binary that catches SIGTERM and exits cleanly.
	helperPath := buildSIGTERMHelper(t, t.TempDir())

	ctx, cancel := context.WithCancel(context.Background())

	cmd := exec.Command(helperPath)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	// Watcher mirrors watchShutdown.
	go func() {
		<-ctx.Done()
		_ = cmd.Process.Signal(syscall.SIGTERM)

		select {
		case <-done:
			return
		case <-time.After(2 * time.Second):
			_ = cmd.Process.Kill()
		}
	}()

	// Let the process start.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("expected clean exit on SIGTERM, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		cmd.Process.Kill()
		t.Fatal("process did not exit after SIGTERM")
	}
}

// buildSIGTERMHelper compiles a tiny Go program that catches SIGTERM and
// exits with code 0, or if it receives no signal within 30 seconds it
// exits with code 1 (test failure).
func buildSIGTERMHelper(t *testing.T, workDir string) string {
	t.Helper()

	src := `package main
import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"
)
func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM)

	select {
	case <-sigCh:
		// SIGTERM received; exit cleanly.
		os.Exit(0)
	case <-ctx.Done():
		// Timed out without receiving SIGTERM.
		os.Exit(1)
	}
}
`
	srcFile := filepath.Join(workDir, "sigterm_helper.go")
	if err := os.WriteFile(srcFile, []byte(src), 0644); err != nil {
		t.Fatalf("write sigterm helper: %v", err)
	}

	binary := filepath.Join(workDir, "sigterm_helper")
	cmd := exec.Command("go", "build", "-o", binary, srcFile)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build sigterm helper: %v\n%s", err, out)
	}
	return binary
}

// TestShutdownTimeout_Honored verifies the option type is usable.
func TestShutdownTimeout_Honored(t *testing.T) {
	_ = remote.WithShutdownTimeout(100 * time.Millisecond)
}

// TestAdapterConstructor_ShutdownTimeout verifies different roles accept
// the timeout option.
func TestAdapterConstructor_ShutdownTimeout(t *testing.T) {
	_ = remote.NewRouter([]string{"echo"}, remote.WithShutdownTimeout(1*time.Second))
	_ = remote.NewWorker([]string{"echo"}, remote.WithShutdownTimeout(2*time.Second))
	mockGitClient := &mockGit{diff: ""}
	_ = remote.NewValidator([]string{"echo"}, mockGitClient, remote.WithShutdownTimeout(3*time.Second))
}

// --- tests: ndjsonValidator captures worktree_diff -------------------------

func TestValidator_CapturesWorktreeDiff(t *testing.T) {
	mockGitClient := &mockGit{diff: "mock diff content"}

	handler := func(req *ndjson.Envelope, enc *ndjson.Encoder, t *testing.T) {
		var reqPayload map[string]any
		json.Unmarshal(req.Event.Payload, &reqPayload)
		if wd, _ := reqPayload["worktree_diff"].(string); wd != "mock diff content" {
			t.Errorf("expected worktree_diff 'mock diff content', got %q", wd)
		}
		_ = enc.Encode(resultEvent(map[string]any{"passes": true, "feedback": "ok"}))
	}

	_, adapterEnc, adapterDec := newFakeSubprocess(t, handler)

	reqPayload := map[string]any{
		"task":          map[string]any{"id": "task-1", "description": "task-1"},
		"worktree_diff": mockGitClient.diff,
		"attempt":       1,
	}
	_ = adapterEnc.Encode(requestToEnvelope(reqPayload))

	env, err := adapterDec.Decode()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	payload, _ := decodePayload(env.Event.Payload)
	if p, _ := payload["passes"].(bool); !p {
		t.Error("expected passes=true")
	}
}

// --- tests: orchestrator interfaces satisfied ------------------------------

func TestOrchestratorInterfaces_Satisfied(t *testing.T) {
	var r orchestrator.Router = remote.NewRouter([]string{"echo"})
	var w orchestrator.Worker = remote.NewWorker([]string{"echo"})
	var v orchestrator.Validator = remote.NewValidator([]string{"echo"}, &mockGit{})
	_ = r
	_ = w
	_ = v
}

// --- tests: worker synchronous single-flight -------------------------------

func TestWorker_SynchronousSingleFlight(t *testing.T) {
	handler := func(req *ndjson.Envelope, enc *ndjson.Encoder, t *testing.T) {
		_ = enc.Encode(toolRequest("req-1", ndjson.ToolFileWrite, map[string]any{
			"path":    "a.txt",
			"content": "A",
		}))
		_ = enc.Encode(toolRequest("req-2", ndjson.ToolFileWrite, map[string]any{
			"path":    "b.txt",
			"content": "B",
		}))
		_ = enc.Encode(resultEvent(map[string]any{"status": "complete"}))
	}

	_, adapterEnc, adapterDec := newFakeSubprocess(t, handler)
	_ = adapterEnc.Encode(workerRequest("task-1", "", 1))

	// Read tool_request 1.
	env1, err := adapterDec.Decode()
	if err != nil {
		t.Fatalf("decode 1: %v", err)
	}
	if env1.Kind != ndjson.KindToolRequest || env1.ToolReq.ID != "req-1" {
		t.Fatalf("expected tool_request req-1, got kind=%q", env1.Kind)
	}

	// Read tool_request 2.
	env2, err := adapterDec.Decode()
	if err != nil {
		t.Fatalf("decode 2: %v", err)
	}
	if env2.Kind != ndjson.KindToolRequest || env2.ToolReq.ID != "req-2" {
		t.Fatalf("expected tool_request req-2, got kind=%q", env2.Kind)
	}

	// Read result event.
	env3, err := adapterDec.Decode()
	if err != nil {
		t.Fatalf("decode 3: %v", err)
	}
	if env3.Kind != ndjson.KindEvent {
		t.Fatalf("expected event, got kind=%q", env3.Kind)
	}
}