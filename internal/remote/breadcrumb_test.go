package remote

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/denialbb/limen/internal/bus"
)

func TestDiffBreadcrumbSets(t *testing.T) {
	tests := []struct {
		name string
		prev map[string]string
		curr map[string]string
		want []bus.BreadcrumbFile
	}{
		{
			name: "empty to empty emits nothing",
			prev: map[string]string{},
			curr: map[string]string{},
			want: nil,
		},
		{
			name: "nil to nil emits nothing",
			prev: nil,
			curr: nil,
			want: nil,
		},
		{
			name: "one new file",
			prev: map[string]string{},
			curr: map[string]string{"a.go": " M"},
			want: []bus.BreadcrumbFile{{Path: "a.go", Status: " M"}},
		},
		{
			name: "status code change is emitted",
			prev: map[string]string{"a.go": "??"},
			curr: map[string]string{"a.go": " M"},
			want: []bus.BreadcrumbFile{{Path: "a.go", Status: " M"}},
		},
		{
			name: "equal sets emit nothing (delta-only)",
			prev: map[string]string{"a.go": " M", "b.go": "??"},
			curr: map[string]string{"a.go": " M", "b.go": "??"},
			want: nil,
		},
		{
			name: "unchanged entries suppressed, changed one emitted",
			prev: map[string]string{"a.go": " M", "b.go": "??"},
			curr: map[string]string{"a.go": " M", "b.go": "A "},
			want: []bus.BreadcrumbFile{{Path: "b.go", Status: "A "}},
		},
		{
			name: "disappeared entries are not emitted (signal, not ledger)",
			prev: map[string]string{"a.go": " M", "gone.go": "??"},
			curr: map[string]string{"a.go": " M"},
			want: nil,
		},
		{
			name: "multiple deltas sorted by path",
			prev: map[string]string{},
			curr: map[string]string{"z.go": "??", "a.go": " M", "m.go": "A "},
			want: []bus.BreadcrumbFile{
				{Path: "a.go", Status: " M"},
				{Path: "m.go", Status: "A "},
				{Path: "z.go", Status: "??"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := diffBreadcrumbSets(tc.prev, tc.curr)
			if len(got) != len(tc.want) {
				t.Fatalf("diffBreadcrumbSets() = %#v, want %#v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("diffBreadcrumbSets() = %#v, want %#v", got, tc.want)
				}
			}
		})
	}
}

// scriptedReader hands out one snapshot per call, in order. Once the script is
// exhausted it closes done and repeats the final snapshot forever, so a live
// poller settles into emitting nothing and the test can cancel deterministically.
// A nil snapshot entry makes that call return an error instead (non-fatal path).
type scriptedReader struct {
	snapshots []map[string]string
	mu        sync.Mutex
	calls     int
	done      chan struct{}
	closed    bool
}

func newScriptedReader(snapshots ...map[string]string) *scriptedReader {
	return &scriptedReader{snapshots: snapshots, done: make(chan struct{})}
}

func (s *scriptedReader) read(ctx context.Context) (map[string]string, error) {
	s.mu.Lock()
	i := s.calls
	s.calls++
	if i >= len(s.snapshots) {
		if !s.closed {
			s.closed = true
			close(s.done)
		}
		i = len(s.snapshots) - 1
	}
	s.mu.Unlock()
	snap := s.snapshots[i]
	if snap == nil {
		return nil, errors.New("scripted git failure")
	}
	return snap, nil
}

// callCount reports how many times the reader has been invoked.
func (s *scriptedReader) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// waitExhausted blocks until the script has been fully consumed.
func (s *scriptedReader) waitExhausted(t *testing.T) {
	t.Helper()
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
		t.Fatalf("scripted reader never exhausted (calls=%d)", s.callCount())
	}
}

// breadcrumbDeltas flattens the recorded breadcrumbs into their file deltas so
// the scripted sequence can be asserted exactly.
func breadcrumbDeltas(t *testing.T, rec *bus.RecorderEmitter) [][]bus.BreadcrumbFile {
	t.Helper()
	var out [][]bus.BreadcrumbFile
	for _, ev := range rec.EventsByKind("WorkerBreadcrumb") {
		bc, ok := ev.(*bus.WorkerBreadcrumb)
		if !ok {
			t.Fatalf("EventsByKind returned %T, want *bus.WorkerBreadcrumb", ev)
		}
		out = append(out, bc.Files)
	}
	return out
}

func TestPollBreadcrumbsScriptedSequence(t *testing.T) {
	reader := newScriptedReader(
		map[string]string{},                           // baseline: nothing changed yet
		map[string]string{"a.go": "??"},               // new untracked file
		map[string]string{"a.go": " M"},               // status code change
		map[string]string{"a.go": " M", "b.go": "??"}, // second file appears
		map[string]string{"a.go": " M", "b.go": "??"}, // identical: delta-only suppression
	)
	rec := bus.NewRecorderEmitter()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		pollBreadcrumbs(ctx, time.Millisecond, reader.read, "task-a", rec)
	}()

	reader.waitExhausted(t)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("pollBreadcrumbs did not return promptly after cancel")
	}

	want := [][]bus.BreadcrumbFile{
		{{Path: "a.go", Status: "??"}},
		{{Path: "a.go", Status: " M"}},
		{{Path: "b.go", Status: "??"}},
	}
	got := breadcrumbDeltas(t, rec)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("breadcrumb deltas = %#v, want %#v", got, want)
	}

	// Every published event carries the task ID and a timestamp.
	for _, ev := range rec.EventsByKind("WorkerBreadcrumb") {
		bc := ev.(*bus.WorkerBreadcrumb)
		if bc.TaskID != "task-a" {
			t.Fatalf("breadcrumb TaskID = %q, want %q", bc.TaskID, "task-a")
		}
		if bc.Timestamp.IsZero() {
			t.Fatalf("breadcrumb Timestamp is zero")
		}
	}
}

func TestPollBreadcrumbsStopsOnCancel(t *testing.T) {
	reader := newScriptedReader(
		map[string]string{"a.go": "??"},
		map[string]string{"a.go": " M"},
	)
	rec := bus.NewRecorderEmitter()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		pollBreadcrumbs(ctx, time.Millisecond, reader.read, "task-a", rec)
	}()

	reader.waitExhausted(t)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("pollBreadcrumbs did not return after cancel")
	}

	// The reader must go quiet: no further polls and no further events once the
	// poller has returned (no goroutine left behind).
	before := len(rec.EventsByKind("WorkerBreadcrumb"))
	callsBefore := reader.callCount()
	time.Sleep(50 * time.Millisecond) // many poll intervals
	if after := len(rec.EventsByKind("WorkerBreadcrumb")); after != before {
		t.Fatalf("breadcrumbs published after cancel: %d -> %d", before, after)
	}
	if after := reader.callCount(); after != callsBefore {
		t.Fatalf("reader still polled after cancel: %d -> %d calls", callsBefore, after)
	}
}

func TestPollBreadcrumbsReaderErrorIsNonFatal(t *testing.T) {
	// A transient git failure (nil snapshot) emits nothing, leaves the previous
	// snapshot intact, and never stops the poller.
	reader := newScriptedReader(
		map[string]string{"a.go": "??"},
		nil, // git hiccup
		map[string]string{"a.go": "??", "b.go": " M"},
	)
	rec := bus.NewRecorderEmitter()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		pollBreadcrumbs(ctx, time.Millisecond, reader.read, "task-a", rec)
	}()

	reader.waitExhausted(t)
	cancel()
	<-done

	want := [][]bus.BreadcrumbFile{
		{{Path: "a.go", Status: "??"}},
		{{Path: "b.go", Status: " M"}},
	}
	got := breadcrumbDeltas(t, rec)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("breadcrumb deltas across a reader error = %#v, want %#v", got, want)
	}
}

func TestPollBreadcrumbsNilEmitter(t *testing.T) {
	// Breadcrumbs are fire-and-forget observability: a nil emitter is a no-op,
	// never a panic.
	reader := newScriptedReader(map[string]string{"a.go": "??"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		pollBreadcrumbs(ctx, time.Millisecond, reader.read, "task-a", nil)
	}()

	reader.waitExhausted(t)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("pollBreadcrumbs did not return after cancel")
	}
}

func TestParsePorcelain(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want map[string]string
	}{
		{"empty output", "", map[string]string{}},
		{"only newlines", "\n\n", map[string]string{}},
		{
			name: "modified, untracked and staged entries",
			out:  " M internal/remote/agent.go\n?? notes.txt\nA  cmd/limen/new.go\n",
			want: map[string]string{
				"internal/remote/agent.go": " M",
				"notes.txt":                "??",
				"cmd/limen/new.go":         "A ",
			},
		},
		{
			name: "no trailing newline",
			out:  " M a.go",
			want: map[string]string{"a.go": " M"},
		},
		{
			name: "short or garbage lines are skipped",
			out:  "x\n M a.go\n",
			want: map[string]string{"a.go": " M"},
		},
		{
			name: "CRLF line endings are tolerated",
			out:  " M a.go\r\n?? b.txt\r\n",
			want: map[string]string{"a.go": " M", "b.txt": "??"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parsePorcelain([]byte(tc.out))
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parsePorcelain(%q) = %#v, want %#v", tc.out, got, tc.want)
			}
		})
	}
}
