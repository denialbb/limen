package bus

// Stress / jitter coverage for the concurrent surface of ChannelBus and
// RecorderEmitter (CDD alignment, Iteration 1). The existing bus_test.go proves
// the single-threaded contract; this file proves the contract survives
// contention: no lost events, no send-on-closed-channel panic, no deadlock, and
// no leaked goroutines.
//
// Contracts pinned here, read off bus.go and asserted rather than assumed:
//
//   - Publish BLOCKS (it does not drop) when a subscriber's buffer is full.
//     Backpressure is the documented v1 policy, so every stress test below
//     keeps a draining consumer alive for as long as a publisher is running;
//     a test that abandoned a subscriber mid-stream would wedge the bus by
//     design, not by defect.
//   - Publish holds the bus mutex for the whole fan-out, so Close cannot
//     interleave with an in-flight send. Publish after Close is a silent no-op.
//   - Subscribe after Close returns an already-closed channel.
//
// Note on t.Parallel: these tests measure runtime.NumGoroutine deltas to catch
// leaks, and parallel siblings would pollute that count. Leak detection wins;
// the whole file is bounded well under 15s serially.

import (
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// runtimeOwnedFrames mark goroutines that belong to the Go runtime or the
// testing harness rather than to the code under test. They come and go on their
// own schedule (a GC cycle started mid-test permanently adds mark workers), so
// counting raw runtime.NumGoroutine would need a tolerance large enough to hide
// a real one- or two-goroutine leak. Filtering by stack instead lets the
// assertion run at zero tolerance.
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
// testing-owned, together with their stacks for diagnostics.
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

// assertNoGoroutineLeak samples the live (non-runtime) goroutines now and
// re-checks at test end, polling so a goroutine that is merely slow to unwind
// is not misreported. One still parked after the grace window is a leak.
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

// waitWithin fails the test if fn has not returned within d. It converts a
// deadlock into a diagnosable failure instead of a hung `go test`.
func waitWithin(t *testing.T, d time.Duration, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(d):
		buf := make([]byte, 1<<16)
		n := runtime.Stack(buf, true)
		t.Fatalf("%s did not complete within %s (deadlock?)\n%s", what, d, buf[:n])
	}
}

// TestStressBus_ConcurrentPublishersNoEventLoss drives 8 publishers x 500
// events through one subscriber. The total (4000) deliberately exceeds
// SubscriberBufferSize so the run exercises the blocking-publish path, and the
// consumer must still see every event: blocking backpressure means zero loss.
func TestStressBus_ConcurrentPublishersNoEventLoss(t *testing.T) {
	assertNoGoroutineLeak(t)

	const (
		publishers = 8
		perPub     = 500
		total      = publishers * perPub
	)
	if total <= SubscriberBufferSize {
		t.Fatalf("test is miscalibrated: %d events fit in the %d buffer, so backpressure is never exercised", total, SubscriberBufferSize)
	}

	b := NewChannelBus()
	sub := b.Subscribe()

	var received atomic.Int64
	consumerDone := make(chan struct{})
	go func() {
		defer close(consumerDone)
		for range sub {
			received.Add(1)
		}
	}()

	waitWithin(t, 15*time.Second, "concurrent publish", func() {
		var wg sync.WaitGroup
		for p := range publishers {
			wg.Go(func() {
				for i := range perPub {
					b.Publish(&ContextBuilt{TaskID: "task", SnapshotSize: p*perPub + i, Timestamp: time.Now()})
				}
			})
		}
		wg.Wait()
		b.Close()
		<-consumerDone
	})

	if got := received.Load(); got != total {
		t.Errorf("lost events: got %d, want %d", got, total)
	}
}

// TestStressBus_SlowSubscriberBackpressure pins the buffer contract: a
// subscriber that stalls past SubscriberBufferSize stalls its publisher rather
// than losing events, and once it resumes the full stream is delivered.
func TestStressBus_SlowSubscriberBackpressure(t *testing.T) {
	assertNoGoroutineLeak(t)

	const total = SubscriberBufferSize + 256

	b := NewChannelBus()
	fast := b.Subscribe()
	slow := b.Subscribe()

	var fastCount, slowCount atomic.Int64
	release := make(chan struct{})
	fastDone := make(chan struct{})
	slowDone := make(chan struct{})

	go func() {
		defer close(fastDone)
		for range fast {
			fastCount.Add(1)
		}
	}()
	go func() {
		defer close(slowDone)
		<-release // stall until the publisher is provably blocked
		for range slow {
			slowCount.Add(1)
		}
	}()

	pubDone := make(chan struct{})
	go func() {
		defer close(pubDone)
		for i := range total {
			b.Publish(&ContextBuilt{TaskID: "slow", SnapshotSize: i, Timestamp: time.Now()})
		}
	}()

	// The publisher must be wedged on the stalled subscriber, not spinning
	// ahead of it: prove Publish blocks rather than drops.
	select {
	case <-pubDone:
		t.Fatal("publisher completed while a subscriber was stalled: events were dropped, contradicting the blocking-publish contract")
	case <-time.After(200 * time.Millisecond):
	}

	close(release)
	waitWithin(t, 15*time.Second, "backpressure drain", func() {
		<-pubDone
		b.Close()
		<-fastDone
		<-slowDone
	})

	if got := fastCount.Load(); got != total {
		t.Errorf("fast subscriber: got %d events, want %d", got, total)
	}
	if got := slowCount.Load(); got != total {
		t.Errorf("slow subscriber: got %d events, want %d", got, total)
	}
}

// TestStressBus_SubscriberChurnUnderLoad joins subscribers at arbitrary points
// in a live stream. Late joiners see a suffix of the stream (never more than
// was published), no publisher panics, and every channel handed out is closed
// by Close so no consumer is stranded.
func TestStressBus_SubscriberChurnUnderLoad(t *testing.T) {
	assertNoGoroutineLeak(t)

	const (
		publishers = 8
		perPub     = 200
		joiners    = 16
		total      = publishers * perPub
	)

	b := NewChannelBus()
	counts := make([]int64, joiners)

	var consumers sync.WaitGroup
	var publish sync.WaitGroup

	for p := range publishers {
		publish.Go(func() {
			for i := range perPub {
				b.Publish(&ContextBuilt{TaskID: "churn", SnapshotSize: p*perPub + i, Timestamp: time.Now()})
			}
		})
	}

	for j := range joiners {
		consumers.Go(func() {
			// Jittered join: each subscriber lands at a different point in
			// the stream, racing Subscribe against in-flight Publish calls.
			time.Sleep(time.Duration(j) * time.Millisecond)
			ch := b.Subscribe()
			// Each goroutine owns one slice element, so the counters need no
			// synchronisation of their own.
			for range ch {
				counts[j]++
			}
		})
	}

	waitWithin(t, 15*time.Second, "subscriber churn", func() {
		publish.Wait()
		// Every joiner must have subscribed before Close, otherwise it would
		// receive a closed channel and the suffix assertion would be vacuous.
		time.Sleep(time.Duration(joiners) * time.Millisecond)
		b.Close()
		consumers.Wait()
	})

	for j, got := range counts {
		if got > total {
			t.Errorf("subscriber %d received %d events, more than the %d published", j, got, total)
		}
	}
}

// TestStressBus_SubscribeRacesClose hammers Subscribe against a concurrent
// Close. Whichever side wins, the returned channel must terminate: a subscriber
// that raced in before Close gets it closed by Close, and one that raced in
// after gets an already-closed channel.
func TestStressBus_SubscribeRacesClose(t *testing.T) {
	assertNoGoroutineLeak(t)

	const subscribers = 32

	b := NewChannelBus()
	start := make(chan struct{})
	var wg sync.WaitGroup

	for range subscribers {
		wg.Go(func() {
			<-start
			// Drain to completion; the assertion is that the loop terminates.
			for range b.Subscribe() {
			}
		})
	}
	wg.Go(func() {
		<-start
		b.Close()
	})

	close(start)
	waitWithin(t, 10*time.Second, "subscribe/close race", wg.Wait)

	// Post-close Subscribe is unambiguous: closed immediately, no blocking.
	late := b.Subscribe()
	select {
	case ev, ok := <-late:
		if ok {
			t.Fatalf("subscribe after close yielded an event: %v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("subscribe after close returned an open channel")
	}
}

// TestStressBus_PublishRacesCloseIsNoOp is the send-on-closed-channel guard:
// Publish holds the mutex across the fan-out, so Close can never close a
// channel mid-send. Publishers that lose the race no-op silently.
func TestStressBus_PublishRacesCloseIsNoOp(t *testing.T) {
	assertNoGoroutineLeak(t)

	const publishers = 16

	b := NewChannelBus()
	sub := b.Subscribe()

	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for range sub {
		}
	}()

	start := make(chan struct{})
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for range publishers {
		wg.Go(func() {
			<-start
			for {
				select {
				case <-stop:
					return
				default:
				}
				// A panic here (send on closed channel) fails the test by
				// crashing the binary with the offending stack.
				b.Publish(&WorkerFinished{TaskID: "race", Timestamp: time.Now()})
			}
		})
	}

	close(start)
	time.Sleep(50 * time.Millisecond)
	b.Close()

	// Publishers keep publishing into the closed bus for a beat: after Close
	// the calls must remain harmless no-ops, not panics.
	time.Sleep(50 * time.Millisecond)
	close(stop)

	waitWithin(t, 10*time.Second, "publish/close race", func() {
		wg.Wait()
		<-drained
	})
}

// TestStressBus_FanOutPreservesPublisherOrder pins the ordering contract for a
// single publisher: Publish fans out under one mutex and each subscriber
// channel is FIFO, so every subscriber observes that publisher's events in
// publication order. (Across concurrent publishers, only per-publisher order
// is guaranteed; interleaving is not.)
func TestStressBus_FanOutPreservesPublisherOrder(t *testing.T) {
	assertNoGoroutineLeak(t)

	const (
		events      = 500
		subscribers = 4
	)

	b := NewChannelBus()
	subs := make([]<-chan Event, subscribers)
	for i := range subs {
		subs[i] = b.Subscribe()
	}

	var wg sync.WaitGroup
	for i, ch := range subs {
		wg.Go(func() {
			want := 0
			for ev := range ch {
				got := ev.(*ContextBuilt).SnapshotSize
				if got != want {
					t.Errorf("subscriber %d: out-of-order event %d, want %d", i, got, want)
					return
				}
				want++
			}
			if want != events {
				t.Errorf("subscriber %d: received %d events, want %d", i, want, events)
			}
		})
	}

	waitWithin(t, 15*time.Second, "ordered fan-out", func() {
		for i := range events {
			b.Publish(&ContextBuilt{TaskID: "order", SnapshotSize: i, Timestamp: time.Now()})
		}
		b.Close()
		wg.Wait()
	})
}

// TestStressBus_NoDeadlockUnderFullLoad is the saturation case: 32 publishers
// fanning out to 32 draining subscribers as fast as the mutex allows for one
// second. The bus must keep making progress and shut down cleanly.
func TestStressBus_NoDeadlockUnderFullLoad(t *testing.T) {
	assertNoGoroutineLeak(t)

	const (
		publishers  = 32
		subscribers = 32
		load        = time.Second
	)

	b := NewChannelBus()

	var consumers sync.WaitGroup
	var delivered atomic.Int64
	for range subscribers {
		ch := b.Subscribe()
		consumers.Go(func() {
			for range ch {
				delivered.Add(1)
			}
		})
	}

	stop := make(chan struct{})
	var producers sync.WaitGroup
	var published atomic.Int64
	for range publishers {
		producers.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				b.Publish(&WorkerToolCall{TaskID: "load", Tool: "t", Timestamp: time.Now()})
				published.Add(1)
			}
		})
	}

	time.Sleep(load)
	close(stop)

	waitWithin(t, 20*time.Second, "saturation load", func() {
		producers.Wait()
		b.Close()
		consumers.Wait()
	})

	pub := published.Load()
	if pub == 0 {
		t.Fatal("no events published under load: the bus made no progress")
	}
	if got, want := delivered.Load(), pub*subscribers; got != want {
		t.Errorf("fan-out lost events: delivered %d, want %d (%d published x %d subscribers)", got, want, pub, subscribers)
	}
}

// TestStressBus_RecorderEmitterConcurrentRecordAndRead proves the recorder's
// mutex actually covers the reads: Events and EventsByKind return defensive
// copies, so a reader can never observe a slice being appended to underneath
// it. Readers also assert monotonic growth — the history is append-only.
func TestStressBus_RecorderEmitterConcurrentRecordAndRead(t *testing.T) {
	assertNoGoroutineLeak(t)

	const (
		writers = 8
		perW    = 500
		readers = 8
		total   = writers * perW
	)

	rec := NewRecorderEmitter()
	stop := make(chan struct{})
	var write, read sync.WaitGroup

	for w := range writers {
		write.Go(func() {
			for i := range perW {
				if i%2 == 0 {
					rec.Publish(&WorkerFinished{TaskID: "rec", Timestamp: time.Now()})
					continue
				}
				rec.Publish(&ContextBuilt{TaskID: "rec", SnapshotSize: w*perW + i, Timestamp: time.Now()})
			}
		})
	}

	for range readers {
		read.Go(func() {
			prev := 0
			for {
				select {
				case <-stop:
					return
				default:
				}
				all := rec.Events()
				if len(all) < prev {
					t.Errorf("recorded history shrank: %d then %d", prev, len(all))
					return
				}
				prev = len(all)
				// Touch the copies: a shared backing array would surface as a
				// race here, and kind() on a torn element would panic.
				for _, ev := range all {
					_ = ev.kind()
				}
				_ = rec.EventsByKind("WorkerFinished")
			}
		})
	}

	waitWithin(t, 15*time.Second, "recorder contention", func() {
		// Readers spin until every write has landed, so the reads below are
		// contended for the whole run and the final count is exact.
		write.Wait()
		close(stop)
		read.Wait()
	})

	if got := len(rec.Events()); got != total {
		t.Errorf("recorded %d events, want %d", got, total)
	}
	if got, want := len(rec.EventsByKind("WorkerFinished")), total/2; got != want {
		t.Errorf("recorded %d WorkerFinished events, want %d", got, want)
	}
}

// TestStressBus_CloseAndDrain asserts the shutdown tail: events published
// before Close survive in the buffer, the channel closes exactly once the
// buffer is drained, and nothing arrives afterwards.
func TestStressBus_CloseAndDrain(t *testing.T) {
	assertNoGoroutineLeak(t)

	const buffered = 128

	b := NewChannelBus()
	sub := b.Subscribe()

	for i := range buffered {
		b.Publish(&ContextBuilt{TaskID: "drain", SnapshotSize: i, Timestamp: time.Now()})
	}
	b.Close()
	// Post-close publishes must not resurrect the stream.
	b.Publish(&ContextBuilt{TaskID: "after-close", SnapshotSize: -1, Timestamp: time.Now()})

	drained := 0
	for ev := range sub {
		cb := ev.(*ContextBuilt)
		if cb.TaskID != "drain" {
			t.Fatalf("event published after Close was delivered: %+v", cb)
		}
		if cb.SnapshotSize != drained {
			t.Fatalf("buffered event out of order: got %d, want %d", cb.SnapshotSize, drained)
		}
		drained++
	}
	if drained != buffered {
		t.Errorf("drained %d buffered events, want %d", drained, buffered)
	}

	select {
	case ev, ok := <-sub:
		if ok {
			t.Fatalf("channel yielded %v after being drained and closed", ev)
		}
	default:
		t.Fatal("drained channel is not closed")
	}
}
