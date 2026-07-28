package remote

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/denialbb/limen/internal/bus"
	"github.com/denialbb/limen/internal/git"
	"github.com/denialbb/limen/internal/state"
)

// writeWaitScript writes a fake one-shot CLI that prints nothing until a
// sentinel file appears, then prints a plain-text line and exits — the shape of
// an eventless dialect (agy) doing silent work. Gating the exit on the sentinel
// lets the test hold the run open until a known number of breadcrumb polls have
// happened, with no sleep-based racing.
func writeWaitScript(t *testing.T, dir string) string {
	t.Helper()
	script := filepath.Join(dir, "fake-eventless.sh")
	fixture := `#!/bin/sh
sentinel="$1"
i=0
while [ ! -f "$sentinel" ] && [ "$i" -lt 600 ]; do
  sleep 0.05
  i=$((i+1))
done
echo 'I made the change.'
`
	if err := os.WriteFile(script, []byte(fixture), 0755); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return script
}

// sentinelReader is a scripted breadcrumb reader that releases the fake CLI (by
// creating the sentinel file) only after it has served enough snapshots to
// guarantee the asserted deltas were polled during the run.
type sentinelReader struct {
	t         *testing.T
	sentinel  string
	snapshots []map[string]string

	mu    sync.Mutex
	calls int
}

func (s *sentinelReader) read(ctx context.Context) (map[string]string, error) {
	s.mu.Lock()
	i := s.calls
	s.calls++
	s.mu.Unlock()

	if i >= len(s.snapshots) {
		// Script consumed: let the fake CLI finish, and report the final
		// snapshot so no further delta is produced.
		if err := os.WriteFile(s.sentinel, []byte("go"), 0644); err != nil {
			s.t.Errorf("write sentinel: %v", err)
		}
		return s.snapshots[len(s.snapshots)-1], nil
	}
	return s.snapshots[i], nil
}

func (s *sentinelReader) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// TestAgentWorkerEmitsBreadcrumbsForEventlessDialect proves the gate is on for
// agy: the poller runs during the run and its breadcrumbs land on the bus
// between WorkerStarted and WorkerFinished.
func TestAgentWorkerEmitsBreadcrumbsForEventlessDialect(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake shell script fixture requires a POSIX shell")
	}
	dir := t.TempDir()
	script := writeWaitScript(t, dir)
	sentinel := filepath.Join(dir, "release")

	reader := &sentinelReader{
		t:        t,
		sentinel: sentinel,
		snapshots: []map[string]string{
			{},
			{"a.go": "??"},
			{"a.go": " M"},
		},
	}

	w := newAgentWorkerCmd(agyDialect(), []string{"/bin/sh", script, sentinel}, defaultOptions())
	w.breadcrumbReader = reader.read
	w.breadcrumbInterval = 5 * time.Millisecond

	rec := bus.NewRecorderEmitter()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := w.ProduceSolution(ctx, &state.Task{ID: "task-a"}, &git.Worktree{Path: dir}, "", rec); err != nil {
		t.Fatalf("ProduceSolution: %v", err)
	}

	kinds := kindsOf(rec.Events())
	if len(kinds) < 3 {
		t.Fatalf("expected breadcrumbs between start and finish, got %v", kinds)
	}
	if kinds[0] != "WorkerStarted" {
		t.Fatalf("first event = %q, want WorkerStarted (kinds %v)", kinds[0], kinds)
	}
	if last := kinds[len(kinds)-1]; last != "WorkerFinished" {
		t.Fatalf("last event = %q, want WorkerFinished (kinds %v)", last, kinds)
	}
	for _, k := range kinds[1 : len(kinds)-1] {
		if k != "WorkerBreadcrumb" {
			t.Fatalf("unexpected mid-run event %q in %v", k, kinds)
		}
	}

	// The scripted deltas must be the ones observed, in order.
	want := [][]bus.BreadcrumbFile{
		{{Path: "a.go", Status: "??"}},
		{{Path: "a.go", Status: " M"}},
	}
	got := breadcrumbDeltas(t, rec)
	if len(got) != len(want) {
		t.Fatalf("breadcrumb deltas = %#v, want %#v", got, want)
	}
	for i := range want {
		if len(got[i]) != len(want[i]) || got[i][0] != want[i][0] {
			t.Fatalf("breadcrumb deltas = %#v, want %#v", got, want)
		}
	}
}

// TestAgentWorkerNoBreadcrumbsForEventRichDialects proves the gate holds: a
// dialect that has not opted in never polls and never publishes a breadcrumb,
// even with a reader injected.
func TestAgentWorkerNoBreadcrumbsForEventRichDialects(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake shell script fixture requires a POSIX shell")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-eventrich.sh")
	fixture := `#!/bin/sh
sleep 0.2
echo 'TOOL bash'
`
	if err := os.WriteFile(script, []byte(fixture), 0755); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	for _, d := range []dialect{testDialectOneShot(), piDialect(), claudeDialect(), opencodeDialect()} {
		t.Run(d.name, func(t *testing.T) {
			if d.emitBreadcrumbs {
				t.Fatalf("dialect %s must not opt into breadcrumbs (only the eventless agy does)", d.name)
			}

			// Force the one-shot family so the fixture script drives every dialect
			// uniformly; the gate under test is emitBreadcrumbs, not the family.
			d.promptViaArgv = true
			d.encodeStdin = nil

			var polled atomic.Int32
			reader := func(ctx context.Context) (map[string]string, error) {
				polled.Add(1)
				return map[string]string{"a.go": "??"}, nil
			}

			w := newAgentWorkerCmd(d, []string{"/bin/sh", script}, defaultOptions())
			w.breadcrumbReader = reader
			w.breadcrumbInterval = time.Millisecond

			rec := bus.NewRecorderEmitter()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			if err := w.ProduceSolution(ctx, &state.Task{ID: "task-a"}, &git.Worktree{Path: dir}, "", rec); err != nil {
				t.Fatalf("ProduceSolution: %v", err)
			}

			if n := len(rec.EventsByKind("WorkerBreadcrumb")); n != 0 {
				t.Fatalf("dialect %s published %d breadcrumbs, want 0 (gate must hold)", d.name, n)
			}
			if n := polled.Load(); n != 0 {
				t.Fatalf("dialect %s polled git %d times, want 0 (poller must not start)", d.name, n)
			}
		})
	}
}

// TestAgyDialectOptsIntoBreadcrumbs pins the gate's only true case.
func TestAgyDialectOptsIntoBreadcrumbs(t *testing.T) {
	if !agyDialect().emitBreadcrumbs {
		t.Fatalf("agy is the eventless dialect and must opt into git-poll breadcrumbs")
	}
}

// TestBreadcrumbReaderDefaultsToWorktree checks the driver falls back to the
// real git reader (bound to the worktree) when no reader was injected.
func TestBreadcrumbReaderDefaultsToWorktree(t *testing.T) {
	w := newAgentWorkerCmd(agyDialect(), []string{"/bin/true"}, defaultOptions())
	if w.breadcrumbReader != nil {
		t.Fatalf("breadcrumbReader must default to nil so the driver builds gitStatusReader(wt.Path)")
	}
	if got := w.pollInterval(); got != breadcrumbInterval {
		t.Fatalf("pollInterval() = %v, want the %v default", got, breadcrumbInterval)
	}
	w.breadcrumbInterval = 7 * time.Millisecond
	if got := w.pollInterval(); got != 7*time.Millisecond {
		t.Fatalf("pollInterval() = %v, want the injected 7ms", got)
	}
}
