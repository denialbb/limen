package remote

// Stress / jitter coverage for the breadcrumb poller (CDD alignment,
// Iteration 1). breadcrumb_test.go proves the delta logic and the happy-path
// lifecycle; this file proves the concurrency contract the poller lives under:
// it is a goroutine the driver cancels, so cancellation must be prompt and
// total, the pure helpers must stay pure under parallel load, and nothing may
// outlive the cancel.
//
// The invariant that matters most operationally: no breadcrumb may land after
// pollBreadcrumbs returns. The driver waits at most breadcrumbStopGrace for the
// poller to exit before declaring the worker finished, so a poller that emits
// after return would push a breadcrumb past WorkerFinished and violate the
// ephemeral-stream ordering the TUI relies on.
//
// Note on t.Parallel: the leak assertion samples live goroutines, and parallel
// siblings would pollute the sample. Leak detection wins; the file runs well
// under 15s serially.

import (
	"context"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/denialbb/limen/internal/bus"
)

// runtimeOwnedFrames mark goroutines owned by the Go runtime or the testing
// harness rather than by the code under test. They appear and vanish on their
// own schedule (one GC cycle permanently adds mark workers), so a raw
// NumGoroutine delta would need a tolerance wide enough to hide a real leak.
// Filtering by stack lets the assertion run at zero tolerance.
var runtimeOwnedFrames = []string{
	"runtime.gcBgMarkWorker",
	"runtime.bgsweep",
	"runtime.bgscavenge",
	"runtime.forcegchelper",
	"runtime.ensureSigM",
	"runtime.goexit0",
	"os/signal.signal_recv",
	"testing.tRunner",
	"testing.(*M).Run",
	"testing.runTests",
	"created by runtime",
}

// liveGoroutines returns the goroutines that are neither runtime- nor
// testing-owned, with their stacks for diagnostics.
func liveGoroutines() (int, string) {
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)

	count := 0
	var dump strings.Builder
	for g := range strings.SplitSeq(string(buf[:n]), "\n\n") {
		if strings.TrimSpace(g) == "" {
			continue
		}
		if slices.ContainsFunc(runtimeOwnedFrames, func(f string) bool { return strings.Contains(g, f) }) {
			continue
		}
		count++
		dump.WriteString(g)
		dump.WriteString("\n\n")
	}
	return count, dump.String()
}

// assertNoGoroutineLeak samples live goroutines now and re-checks at test end,
// polling so a poller that is merely slow to unwind is not misreported. One
// still parked after the grace window is a leak.
func assertNoGoroutineLeak(t *testing.T) {
	t.Helper()
	before, _ := liveGoroutines()
	t.Cleanup(func() {
		deadline := time.Now().Add(3 * time.Second)
		for {
			runtime.Gosched()
			after, dump := liveGoroutines()
			if after <= before {
				return
			}
			if time.Now().After(deadline) {
				t.Errorf("goroutine leak: %d live before, %d after\n%s", before, after, dump)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
}

// churningReader flips between two snapshots on every call, so every poll
// produces a non-empty delta. It keeps the emit path maximally busy, which is
// exactly the state a cancel has to interrupt cleanly.
type churningReader struct {
	mu    sync.Mutex
	calls int
}

func (c *churningReader) read(context.Context) (map[string]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if c.calls%2 == 0 {
		return map[string]string{"churn.go": " M"}, nil
	}
	return map[string]string{"churn.go": "??"}, nil
}

func (c *churningReader) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// TestStressBreadcrumb_PollerCancellationJitter cancels a hot poller at
// jittered points in its cycle. Whatever it was doing, it must return well
// inside breadcrumbStopGrace and must not publish anything afterwards — the
// ordering guarantee that keeps breadcrumbs strictly before WorkerFinished.
func TestStressBreadcrumb_PollerCancellationJitter(t *testing.T) {
	assertNoGoroutineLeak(t)

	// Jitter spans sub-tick to many-ticks-in, so cancels land before the first
	// tick, between ticks, and mid-emit.
	jitters := []time.Duration{
		0,
		500 * time.Microsecond,
		3 * time.Millisecond,
		17 * time.Millisecond,
		50 * time.Millisecond,
		137 * time.Millisecond,
	}

	for i, jitter := range jitters {
		reader := &churningReader{}
		rec := bus.NewRecorderEmitter()
		ctx, cancel := context.WithCancel(context.Background())

		returned := make(chan struct{})
		go func() {
			defer close(returned)
			pollBreadcrumbs(ctx, time.Millisecond, reader.read, "task-jitter", rec)
		}()

		time.Sleep(jitter)
		cancelledAt := time.Now()
		cancel()

		select {
		case <-returned:
		case <-time.After(breadcrumbStopGrace):
			t.Fatalf("jitter %v: pollBreadcrumbs did not return within the %v stop grace (reader calls=%d)",
				jitter, breadcrumbStopGrace, reader.callCount())
		}
		if elapsed := time.Since(cancelledAt); elapsed > breadcrumbStopGrace {
			t.Errorf("jitter %v: poller took %v to return, over the %v grace", jitter, elapsed, breadcrumbStopGrace)
		}

		// Nothing may be published after the return: sample, wait out several
		// poll intervals, sample again.
		settled := len(rec.Events())
		time.Sleep(50 * time.Millisecond)
		if got := len(rec.Events()); got != settled {
			t.Errorf("attempt %d (jitter %v): %d events published after pollBreadcrumbs returned", i, jitter, got-settled)
		}
	}
}

// TestStressBreadcrumb_PollerGoroutineLeak starts many pollers at once and
// cancels them all. Each must exit; none may sit parked on a ticker after its
// context is done.
func TestStressBreadcrumb_PollerGoroutineLeak(t *testing.T) {
	assertNoGoroutineLeak(t)

	const pollers = 32

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	for i := range pollers {
		reader := &churningReader{}
		rec := bus.NewRecorderEmitter()
		wg.Go(func() {
			pollBreadcrumbs(ctx, time.Millisecond, reader.read, fmt.Sprintf("task-%d", i), rec)
		})
	}

	// Cancel immediately: most pollers have not seen a single tick yet, so the
	// exit path under test is the ctx.Done arm of the select.
	cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		wg.Wait()
	}()
	select {
	case <-done:
	case <-time.After(breadcrumbStopGrace):
		_, dump := liveGoroutines()
		t.Fatalf("%d pollers did not all return within the %v stop grace\n%s", pollers, breadcrumbStopGrace, dump)
	}
}

// TestStressBreadcrumb_RapidPorcelainChanges feeds ten snapshots through the
// poller at a 1ms cadence, with duplicates interleaved. The poller must emit
// one breadcrumb per actual change and nothing for the repeats: the delta-only
// invariant has to survive changes arriving faster than the poll interval.
func TestStressBreadcrumb_RapidPorcelainChanges(t *testing.T) {
	assertNoGoroutineLeak(t)

	reader := newScriptedReader(
		map[string]string{},                                         // baseline
		map[string]string{"a.go": "??"},                             // a appears
		map[string]string{"a.go": "??"},                             // repeat: suppressed
		map[string]string{"a.go": " M"},                             // a status changes
		map[string]string{"a.go": " M", "b.go": "??"},               // b appears
		map[string]string{"a.go": " M", "b.go": "??"},               // repeat: suppressed
		map[string]string{"a.go": " M", "b.go": "A "},               // b staged
		map[string]string{"a.go": " M", "b.go": "A ", "c.go": "??"}, // c appears
		map[string]string{"b.go": "A ", "c.go": "??"},               // a reverted: not a delta
		map[string]string{"b.go": "A ", "c.go": "??", "d.go": "??"}, // d appears
	)
	rec := bus.NewRecorderEmitter()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		pollBreadcrumbs(ctx, time.Millisecond, reader.read, "task-rapid", rec)
	}()

	reader.waitExhausted(t)
	cancel()
	select {
	case <-done:
	case <-time.After(breadcrumbStopGrace):
		t.Fatalf("pollBreadcrumbs did not return within the %v stop grace", breadcrumbStopGrace)
	}

	want := [][]bus.BreadcrumbFile{
		{{Path: "a.go", Status: "??"}},
		{{Path: "a.go", Status: " M"}},
		{{Path: "b.go", Status: "??"}},
		{{Path: "b.go", Status: "A "}},
		{{Path: "c.go", Status: "??"}},
		{{Path: "d.go", Status: "??"}},
	}
	got := breadcrumbDeltas(t, rec)
	if len(got) != len(want) {
		t.Fatalf("emitted %d breadcrumbs, want %d\ngot:  %#v\nwant: %#v", len(got), len(want), got, want)
	}
	for i := range want {
		if !slices.Equal(got[i], want[i]) {
			t.Errorf("breadcrumb %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}

// TestStressBreadcrumb_ConcurrentDiff hammers diffBreadcrumbSets from many
// goroutines over the same two maps. The function only reads its inputs and
// allocates a fresh slice, so concurrent callers must never race and must all
// agree — including on ordering, which is what makes breadcrumb streams
// replayable in assertions.
func TestStressBreadcrumb_ConcurrentDiff(t *testing.T) {
	assertNoGoroutineLeak(t)

	const (
		callers    = 16
		iterations = 100
		unchanged  = 500
		changed    = 300
		added      = 200
	)

	prev := make(map[string]string, unchanged+changed)
	curr := make(map[string]string, unchanged+changed+added)
	for i := range unchanged {
		key := fmt.Sprintf("same/%04d.go", i)
		prev[key], curr[key] = " M", " M"
	}
	for i := range changed {
		key := fmt.Sprintf("changed/%04d.go", i)
		prev[key], curr[key] = "??", " M"
	}
	for i := range added {
		curr[fmt.Sprintf("added/%04d.go", i)] = "??"
	}

	// Reference computed before any concurrency, so a divergent result is
	// attributable to the parallel calls rather than to the fixture.
	want := diffBreadcrumbSets(prev, curr)
	if len(want) != changed+added {
		t.Fatalf("fixture is wrong: delta has %d entries, want %d", len(want), changed+added)
	}

	var wg sync.WaitGroup
	for c := range callers {
		wg.Go(func() {
			for range iterations {
				got := diffBreadcrumbSets(prev, curr)
				if !slices.Equal(got, want) {
					t.Errorf("caller %d: diffBreadcrumbSets diverged under concurrency (%d entries, want %d)", c, len(got), len(want))
					return
				}
			}
		})
	}
	wg.Wait()

	// The inputs are read-only by contract; prove the callers left them alone.
	if len(prev) != unchanged+changed {
		t.Errorf("prev was mutated: %d entries, want %d", len(prev), unchanged+changed)
	}
	if len(curr) != unchanged+changed+added {
		t.Errorf("curr was mutated: %d entries, want %d", len(curr), unchanged+changed+added)
	}
}

// TestStressBreadcrumb_ConcurrentParsePorcelain calls parsePorcelain in
// parallel over one shared input. It is pure — it allocates its own map per
// call — so every caller must get an identical snapshot with -race quiet.
func TestStressBreadcrumb_ConcurrentParsePorcelain(t *testing.T) {
	assertNoGoroutineLeak(t)

	const (
		callers    = 16
		iterations = 100
		lines      = 500
	)

	var sb strings.Builder
	for i := range lines {
		status := []string{" M", "??", "A ", "MM", "R "}[i%5]
		fmt.Fprintf(&sb, "%s pkg/dir%02d/file%04d.go\n", status, i%20, i)
	}
	// Trailing junk the parser must keep skipping under load: a blank line and
	// a too-short line.
	sb.WriteString("\nXY\n")
	input := []byte(sb.String())

	want := parsePorcelain(input)
	if len(want) != lines {
		t.Fatalf("fixture is wrong: parsed %d entries, want %d", len(want), lines)
	}

	var wg sync.WaitGroup
	for c := range callers {
		wg.Go(func() {
			for range iterations {
				if got := parsePorcelain(input); !maps.Equal(got, want) {
					t.Errorf("caller %d: parsePorcelain diverged under concurrency (%d entries, want %d)", c, len(got), len(want))
					return
				}
			}
		})
	}
	wg.Wait()
}

// TestStressBreadcrumb_ConcurrentGitStatusReader runs the real reader —
// short-lived `git status --porcelain` subprocesses — from several goroutines
// against one worktree. Each call owns its own subprocess and its own map, so
// concurrent callers must agree and must not race.
//
// It reads a purpose-built repo rather than the limen checkout, so the expected
// snapshot is fixed rather than whatever the developer happens to have staged.
func TestStressBreadcrumb_ConcurrentGitStatusReader(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-git stress in short mode")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	assertNoGoroutineLeak(t)

	const (
		callers    = 4
		iterations = 10
	)

	dir := setupBreadcrumbRepo(t)
	dirty := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	dirty("tracked.txt", "base content\nplus a worker edit\n")
	dirty("untracked.txt", "new file\n")
	dirty("ignored.log", "noise\n") // gitignored via *.log; must stay invisible

	read := gitStatusReader(dir)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	want, err := read(ctx)
	if err != nil {
		t.Fatalf("baseline read: %v", err)
	}
	if got := want["tracked.txt"]; got != " M" {
		t.Fatalf("fixture is wrong: tracked.txt = %q, want %q (snapshot %#v)", got, " M", want)
	}
	if _, ok := want["ignored.log"]; ok {
		t.Fatalf("fixture is wrong: gitignored file surfaced in the snapshot %#v", want)
	}

	var wg sync.WaitGroup
	for c := range callers {
		wg.Go(func() {
			for i := range iterations {
				got, err := read(ctx)
				if err != nil {
					t.Errorf("caller %d iteration %d: %v", c, i, err)
					return
				}
				if !maps.Equal(got, want) {
					t.Errorf("caller %d iteration %d: snapshot diverged under concurrency\ngot:  %#v\nwant: %#v", c, i, got, want)
					return
				}
			}
		})
	}
	wg.Wait()
}
