package ndjson

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
)

// mustMarshal panics if encoding fails; only used for test fixtures.
func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

// roundTrip encodes env, then decodes one line from the resulting buffer.
func roundTrip(t *testing.T, env *Envelope) *Envelope {
	t.Helper()
	var buf bytes.Buffer
	enc := NewEncoder(&buf)
	if err := enc.Encode(env); err != nil {
		t.Fatalf("encode: %v", err)
	}
	dec := NewDecoder(&buf)
	got, err := dec.Decode()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return got
}

func TestEncodeDecode_RoundTrip(t *testing.T) {
	want := &Envelope{
		Kind: KindEvent,
		Event: &EventEnvelope{
			Type:      EventTypeRouterExamining,
			TaskID:    "task-7",
			Payload:   mustMarshal(t, map[string]any{"entropy": 0.42}),
			Timestamp: 1_700_000_000,
		},
	}
	got := roundTrip(t, want)

	if got.Kind != want.Kind {
		t.Fatalf("Kind = %q, want %q", got.Kind, want.Kind)
	}
	if got.Event == nil {
		t.Fatal("Event is nil")
	}
	if !bytes.Equal(got.Event.Payload, want.Event.Payload) {
		t.Fatalf("Payload = %s, want %s", got.Event.Payload, want.Event.Payload)
	}
	if got.Event.Type != want.Event.Type || got.Event.TaskID != want.Event.TaskID || got.Event.Timestamp != want.Event.Timestamp {
		t.Fatalf("Event = %+v, want %+v", got.Event, want.Event)
	}
	// Other payload pointers must remain nil for an event envelope.
	if got.ToolReq != nil || got.ToolResp != nil {
		t.Fatalf("unexpected non-event payload: req=%v resp=%v", got.ToolReq, got.ToolResp)
	}
}

func TestEncodeDecode_EventEnvelope(t *testing.T) {
	want := &EventEnvelope{
		Type:      EventTypeWorkerFileEdit,
		TaskID:    "task-edit",
		Payload:   mustMarshal(t, map[string]any{"path": "main.go", "op": "write"}),
		Timestamp: 1_700_000_001,
	}
	var buf bytes.Buffer
	enc := NewEncoder(&buf)
	if err := enc.EncodeEvent(want); err != nil {
		t.Fatalf("EncodeEvent: %v", err)
	}

	dec := NewDecoder(&buf)
	got, err := dec.Decode()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Kind != KindEvent {
		t.Fatalf("Kind = %q, want %q", got.Kind, KindEvent)
	}
	if got.Event == nil {
		t.Fatal("Event is nil")
	}
	if got.Event.Type != want.Type || got.Event.TaskID != want.TaskID ||
		got.Event.Timestamp != want.Timestamp || !bytes.Equal(got.Event.Payload, want.Payload) {
		t.Fatalf("Event = %+v, want %+v", got.Event, want)
	}
}

func TestEncodeDecode_ToolRequest(t *testing.T) {
	want := &ToolRequest{
		ID:   "req-1",
		Tool: ToolStateTransition,
		Args: mustMarshal(t, map[string]any{"to": "WORKER_RUNNING"}),
	}
	var buf bytes.Buffer
	enc := NewEncoder(&buf)
	if err := enc.EncodeToolRequest(want); err != nil {
		t.Fatalf("EncodeToolRequest: %v", err)
	}

	dec := NewDecoder(&buf)
	got, err := dec.Decode()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Kind != KindToolRequest {
		t.Fatalf("Kind = %q, want %q", got.Kind, KindToolRequest)
	}
	if got.ToolReq == nil {
		t.Fatal("ToolReq is nil")
	}
	if got.ToolReq.ID != want.ID || got.ToolReq.Tool != want.Tool || !bytes.Equal(got.ToolReq.Args, want.Args) {
		t.Fatalf("ToolReq = %+v, want %+v", got.ToolReq, want)
	}
}

func TestEncodeDecode_ToolResponse_OK(t *testing.T) {
	want := &ToolResponse{
		ID:     "req-1",
		OK:     true,
		Result: mustMarshal(t, map[string]any{"state": "WORKER_RUNNING"}),
	}
	var buf bytes.Buffer
	enc := NewEncoder(&buf)
	if err := enc.EncodeToolResponse(want); err != nil {
		t.Fatalf("EncodeToolResponse: %v", err)
	}

	dec := NewDecoder(&buf)
	got, err := dec.Decode()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Kind != KindToolResponse {
		t.Fatalf("Kind = %q, want %q", got.Kind, KindToolResponse)
	}
	if got.ToolResp == nil {
		t.Fatal("ToolResp is nil")
	}
	if got.ToolResp.ID != want.ID || got.ToolResp.OK != want.OK ||
		got.ToolResp.Error != "" || !bytes.Equal(got.ToolResp.Result, want.Result) {
		t.Fatalf("ToolResp = %+v, want %+v", got.ToolResp, want)
	}
}

func TestEncodeDecode_ToolResponse_Error(t *testing.T) {
	want := &ToolResponse{
		ID:    "req-2",
		OK:    false,
		Error: "invalid transition",
	}
	var buf bytes.Buffer
	enc := NewEncoder(&buf)
	if err := enc.EncodeToolResponse(want); err != nil {
		t.Fatalf("EncodeToolResponse: %v", err)
	}

	dec := NewDecoder(&buf)
	got, err := dec.Decode()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ToolResp == nil {
		t.Fatal("ToolResp is nil")
	}
	if got.ToolResp.ID != want.ID || got.ToolResp.OK != want.OK || got.ToolResp.Error != want.Error {
		t.Fatalf("ToolResp = %+v, want %+v", got.ToolResp, want)
	}
	// Error responses must omit Result (omitempty).
	if len(got.ToolResp.Result) != 0 {
		t.Fatalf("Result = %s, want omitted", got.ToolResp.Result)
	}
}

func TestDecoder_MalformedJSON(t *testing.T) {
	in := []byte("{not valid json}\n")
	dec := NewDecoder(bytes.NewReader(in))
	_, err := dec.Decode()
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
	if !strings.Contains(err.Error(), "{not valid json}") {
		t.Fatalf("error %q does not contain offending line", err.Error())
	}
	// A subsequent call after a malformed line should yield EOF since the
	// scanner consumed the line and nothing remains.
	if _, err := dec.Decode(); !errors.Is(err, io.EOF) {
		t.Fatalf("expected io.EOF after malformed line, got %v", err)
	}
}

func TestDecoder_MalformedJSON_DoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Decode panicked on malformed input: %v", r)
		}
	}()
	dec := NewDecoder(bytes.NewReader([]byte("}{}\n")))
	_, _ = dec.Decode()
}

func TestDecoder_LineExceedsBuffer(t *testing.T) {
	// Build a single line longer than the 1 MiB scanner limit. The content is
	// valid JSON so the failure is purely about size, not syntax.
	big := strings.Repeat("a", 1024*1024+100)
	line := []byte(`{"x":"` + big + `"}` + "\n")

	dec := NewDecoder(bytes.NewReader(line))
	_, err := dec.Decode()
	if err == nil {
		t.Fatal("expected error for line exceeding 1 MiB buffer, got nil")
	}
	if !errors.Is(err, bufio.ErrTooLong) {
		t.Fatalf("expected error wrapping bufio.ErrTooLong, got %v", err)
	}
}

func TestDecoder_EmptyLineSkipped(t *testing.T) {
	// Blank and whitespace-only lines between valid envelopes must be skipped
	// rather than rejected as malformed JSON ("unexpected end of JSON input").
	env := &Envelope{
		Kind:  KindEvent,
		Event: &EventEnvelope{Type: EventTypeWorkerStarted, TaskID: "t", Payload: []byte(`{}`), Timestamp: 1},
	}
	var buf bytes.Buffer
	enc := NewEncoder(&buf)
	if err := enc.Encode(env); err != nil {
		t.Fatalf("encode 1: %v", err)
	}
	// Insert blank and whitespace-only lines between envelopes.
	buf.WriteString("\n")
	buf.WriteString("   \n")
	buf.WriteString("\t\n")
	if err := enc.Encode(env); err != nil {
		t.Fatalf("encode 2: %v", err)
	}
	// Trailing blank lines should be tolerated too, not treated as EOF early.
	buf.WriteString("\n\n")

	dec := NewDecoder(&buf)
	for i := 0; i < 2; i++ {
		got, err := dec.Decode()
		if err != nil {
			t.Fatalf("decode %d: %v", i, err)
		}
		if got.Kind != KindEvent || got.Event == nil {
			t.Fatalf("decode %d: got %+v, want event envelope", i, got)
		}
		if got.Event.TaskID != "t" {
			t.Fatalf("decode %d: TaskID = %q, want %q", i, got.Event.TaskID, "t")
		}
	}
	// After both valid envelopes, trailing blank lines must yield io.EOF, not a
	// malformed-JSON error.
	if _, err := dec.Decode(); !errors.Is(err, io.EOF) {
		t.Fatalf("expected io.EOF after trailing blanks, got %v", err)
	}
}

func TestDecoder_EOF(t *testing.T) {
	dec := NewDecoder(bytes.NewReader(nil))
	_, err := dec.Decode()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected io.EOF, got %v", err)
	}
}

func TestEncoder_NDJSONFormat(t *testing.T) {
	var buf bytes.Buffer
	enc := NewEncoder(&buf)
	env1 := &Envelope{Kind: KindEvent, Event: &EventEnvelope{Type: EventTypeWorkerStarted, TaskID: "a", Payload: []byte(`{}`), Timestamp: 1}}
	env2 := &Envelope{Kind: KindToolRequest, ToolReq: &ToolRequest{ID: "b", Tool: ToolGitGetDiff, Args: []byte(`{}`)}}
	if err := enc.Encode(env1); err != nil {
		t.Fatalf("encode 1: %v", err)
	}
	if err := enc.Encode(env2); err != nil {
		t.Fatalf("encode 2: %v", err)
	}

	out := buf.String()
	if !strings.HasSuffix(out, "\n") {
		t.Fatalf("output not newline-terminated: %q", out)
	}
	// Strip trailing newline before counting lines.
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), out)
	}
	for i, l := range lines {
		// Each line must be a single JSON object (start with '{').
		if !strings.HasPrefix(l, "{") {
			t.Fatalf("line %d not a JSON object: %q", i, l)
		}
		// Each line must itself be valid JSON.
		var v map[string]any
		if err := json.Unmarshal([]byte(l), &v); err != nil {
			t.Fatalf("line %d invalid JSON: %v", i, err)
		}
	}
}

func TestEncoder_Concurrent(t *testing.T) {
	const goroutines = 16
	const perG = 50

	var buf bytes.Buffer
	enc := NewEncoder(&buf)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	start := make(chan struct{})

	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < perG; i++ {
				env := &Envelope{
					Kind: KindToolResponse,
					ToolResp: &ToolResponse{
						ID:    GenerateID(),
						OK:    true,
						Result: mustMarshal(t, map[string]int{"g": g, "i": i}),
					},
				}
				if err := enc.Encode(env); err != nil {
					t.Errorf("encode: %v", err)
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()

	expected := goroutines * perG
	dec := NewDecoder(&buf)
	count := 0
	for {
		_, err := dec.Decode()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("decode at line %d: %v", count, err)
		}
		count++
	}
	if count != expected {
		t.Fatalf("decoded %d lines, want %d (interleaving likely fragmented lines)", count, expected)
	}
}

func TestGenerateID_Uniqueness(t *testing.T) {
	const n = 1000
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		id := GenerateID()
		if id == "" {
			t.Fatalf("GenerateID returned empty at %d", i)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate id %q at %d", id, i)
		}
		seen[id] = struct{}{}
	}
}

func TestEncoder_NilRejects(t *testing.T) {
	var buf bytes.Buffer
	enc := NewEncoder(&buf)
	if err := enc.Encode(nil); err == nil {
		t.Fatal("Encode(nil) should error")
	}
	if err := enc.EncodeEvent(nil); err == nil {
		t.Fatal("EncodeEvent(nil) should error")
	}
	if err := enc.EncodeToolRequest(nil); err == nil {
		t.Fatal("EncodeToolRequest(nil) should error")
	}
	if err := enc.EncodeToolResponse(nil); err == nil {
		t.Fatal("EncodeToolResponse(nil) should error")
	}
}

func TestConstants_Distinct(t *testing.T) {
	// Sanity: event and tool name constants must not collide within their
	// groups; a typo here would scramble dispatch downstream.
	events := []string{
		EventTypeTaskStateChanged, EventTypeContextBuilt, EventTypeRouterExamining,
		EventTypeRouterDecision, EventTypeWorkerStarted, EventTypeWorkerToolCall,
		EventTypeWorkerFileEdit, EventTypeWorkerFinished, EventTypeValidatorExamining,
		EventTypeValidatorCriterion, EventTypeValidatorVerdict, EventTypeConflictDetected,
		EventTypeTaskFinalized,
	}
	tools := []string{
		ToolStateTransition, ToolStateGetTask, ToolStateRecordTool,
		ToolGitProvision, ToolGitCheckConflict, ToolGitGetDiff,
		ToolGitCommit, ToolGitDestroy, ToolFileWrite,
	}
	assertDistinct(t, events)
	assertDistinct(t, tools)
}

func assertDistinct(t *testing.T, vals []string) {
	t.Helper()
	seen := make(map[string]int, len(vals))
	for i, v := range vals {
		if v == "" {
			t.Fatalf("empty constant at %d", i)
		}
		if prev, ok := seen[v]; ok {
			t.Fatalf("duplicate constant %q at indices %d and %d", v, prev, i)
		}
		seen[v] = i
	}
}