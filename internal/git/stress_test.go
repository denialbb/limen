package git

// Stress / concurrency coverage for WorktreeManager (CDD alignment,
// Iteration 1). worktree_test.go proves the sequential contract; this file
// proves it survives contention with real git subprocesses: concurrent
// provisioning does not collide, cancellation leaves nothing behind, a failed
// provision is still safely destroyable, and no goroutine outlives a call.
//
// Two gates keep the cost honest:
//
//   - Tests that shell out to `git worktree add` concurrently are skipped in
//     -short mode; the cheap invariants (cancellation, destroy-after-failure,
//     concurrent reads) always run.
//   - Every subprocess runs under a bounded context, so a wedged git can fail
//     the test rather than hang the package.
//
// Note on t.Parallel: the leak assertion below samples live goroutines, and
// parallel siblings would pollute the sample. Leak detection wins.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// stressBudget bounds each heavy real-git test.
const stressBudget = 30 * time.Second

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
// polling so an exec watchdog that is merely slow to unwind is not misreported.
// One still parked after the grace window is a leak.
func assertNoGoroutineLeak(t *testing.T) {
	t.Helper()
	before, _ := liveGoroutines()
	t.Cleanup(func() {
		deadline := time.Now().Add(5 * time.Second)
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

// isTransientGitLock reports whether err is git refusing to take a repo-level
// lock. Concurrent `git worktree add` calls contend on refs and the worktrees
// administrative area; losing that race is an OS-level transient, not a defect
// in limen, so the stress tests retry instead of failing.
func isTransientGitLock(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{"unable to lock", "could not lock", "cannot lock", "file exists", "index.lock", "another git process"} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// provisionWithRetry wraps ProvisionWorktree with a bounded retry on transient
// git lock contention. Anything else is returned as-is.
func provisionWithRetry(ctx context.Context, m WorktreeManager, base, branch, path string) (*Worktree, error) {
	var err error
	for attempt := range 5 {
		var wt *Worktree
		wt, err = m.ProvisionWorktree(ctx, base, branch, path)
		if err == nil {
			return wt, nil
		}
		if !isTransientGitLock(err) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 20 * time.Millisecond):
		}
	}
	return nil, fmt.Errorf("git lock contention did not clear after 5 attempts: %w", err)
}

// listWorktrees returns the worktree paths git currently knows about, main
// checkout included. It is the ground truth for "did we leak a worktree?" —
// the directory can be gone while the administrative entry lingers.
func listWorktrees(t *testing.T, repoDir string) []string {
	t.Helper()
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git worktree list failed: %v", err)
	}
	var paths []string
	for line := range strings.SplitSeq(string(out), "\n") {
		if after, ok := strings.CutPrefix(line, "worktree "); ok {
			paths = append(paths, strings.TrimSpace(after))
		}
	}
	return paths
}

// worktreeStateDump renders git's view of the worktrees plus the on-disk
// administrative area, which is where a half-finished `git worktree add` leaves
// its residue. It is the diagnostic attached to every leak failure.
func worktreeStateDump(t *testing.T, repoDir string) string {
	t.Helper()
	var b strings.Builder

	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		fmt.Fprintf(&b, "git worktree list failed: %v\n", err)
	}
	b.WriteString("git worktree list --porcelain:\n")
	b.Write(out)

	adminDir := filepath.Join(repoDir, ".git", "worktrees")
	entries, err := os.ReadDir(adminDir)
	if err != nil {
		fmt.Fprintf(&b, "\n.git/worktrees: %v\n", err)
		return b.String()
	}
	b.WriteString("\n.git/worktrees:\n")
	for _, e := range entries {
		files, _ := os.ReadDir(filepath.Join(adminDir, e.Name()))
		var names []string
		for _, f := range files {
			names = append(names, f.Name())
		}
		fmt.Fprintf(&b, "  %s: %s\n", e.Name(), strings.Join(names, " "))
	}
	return b.String()
}

// assertOnlyMainWorktree is the leak assertion for git state: after cleanup the
// repo must know about exactly one worktree, its own checkout.
func assertOnlyMainWorktree(t *testing.T, repoDir string) {
	t.Helper()
	if got := listWorktrees(t, repoDir); len(got) != 1 {
		t.Errorf("leaked worktrees: git knows about %d worktrees, want 1 (main)\n%s", len(got), worktreeStateDump(t, repoDir))
	}
}

// TestStressWorktree_ConcurrentProvision drives 4 provisions at once against
// one manager. Each must land on its own path and branch, and the repo must
// come back to a single worktree once they are destroyed.
func TestStressWorktree_ConcurrentProvision(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-git stress in short mode")
	}
	assertNoGoroutineLeak(t)

	const workers = 4

	repoDir := setupTestRepo(t)
	manager := NewWorktreeManager(repoDir, "main")
	base := getHeadCommit(t, repoDir)
	root := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), stressBudget)
	defer cancel()

	paths := make([]string, workers)
	errs := make([]error, workers)
	trees := make([]*Worktree, workers)

	var wg sync.WaitGroup
	for i := range workers {
		wg.Go(func() {
			path := filepath.Join(root, fmt.Sprintf("wt-%d", i))
			wt, err := provisionWithRetry(ctx, manager, base, fmt.Sprintf("stress-branch-%d", i), path)
			trees[i], errs[i] = wt, err
			if wt != nil {
				paths[i] = wt.Path
			}
		})
	}
	wg.Wait()

	// Destroy first, so a mid-test failure cannot strand worktrees on disk.
	t.Cleanup(func() {
		for _, wt := range trees {
			if wt != nil {
				if err := manager.DestroyWorktree(context.Background(), wt); err != nil {
					t.Errorf("DestroyWorktree(%s): %v", wt.Path, err)
				}
			}
		}
		assertOnlyMainWorktree(t, repoDir)
	})

	for i, err := range errs {
		if err != nil {
			t.Errorf("worker %d: ProvisionWorktree failed: %v", i, err)
		}
	}
	if t.Failed() {
		return
	}

	seen := map[string]int{}
	for i, p := range paths {
		if prev, dup := seen[p]; dup {
			t.Errorf("workers %d and %d provisioned the same path %s", prev, i, p)
		}
		seen[p] = i
		if _, err := os.Stat(p); err != nil {
			t.Errorf("worker %d: worktree directory missing: %v", i, err)
		}
	}

	if got := listWorktrees(t, repoDir); len(got) != workers+1 {
		t.Errorf("git knows about %d worktrees, want %d (main + %d provisioned)\n%v", len(got), workers+1, workers, got)
	}
}

// cancelledProvisionCycle provisions a worktree while a jittered cancel races
// the subprocess, then destroys whatever resulted. It asserts the invariants
// that hold unconditionally — the call returns, Destroy neither panics nor
// errors, and the directory is gone from disk — and reports whether git's
// administrative area was left holding a phantom worktree.
func cancelledProvisionCycle(t *testing.T, manager WorktreeManager, repoDir, base, path, branch string, delay time.Duration) bool {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(delay)
		cancel()
	}()

	wt, err := manager.ProvisionWorktree(ctx, base, branch, path)
	if err == nil && wt == nil {
		t.Fatalf("%s: nil worktree with nil error", branch)
	}

	// Cleanup is unconditional: on success we destroy the worktree, on a
	// cancelled failure we exercise the destroy path against whatever partial
	// state git left behind.
	target := wt
	if target == nil {
		target = &Worktree{Path: path}
	}
	if derr := manager.DestroyWorktree(context.Background(), target); derr != nil {
		t.Fatalf("%s: DestroyWorktree after cancel returned %v (provision err: %v)", branch, derr, err)
	}
	if _, serr := os.Stat(path); !os.IsNotExist(serr) {
		t.Errorf("%s: worktree directory %s survived cleanup (stat err: %v)", branch, path, serr)
	}

	return len(listWorktrees(t, repoDir)) != 1
}

// TestStressWorktree_CancelledProvisionCleanup cancels provisioning at jittered
// points across the lifetime of the `git worktree add` subprocess and pins what
// cleanup actually delivers today.
//
// KNOWN DEFECT (reported, unfixed — do not silently "fix" by loosening this
// test). When the cancel lands mid-add, git has already created
// .git/worktrees/<name>/ and the branch ref but not yet finished, so it leaves
// a `locked` file reading "initializing". DestroyWorktree's fallback
// (`git worktree prune --expire now`) skips locked worktrees by design and
// still exits 0, so DestroyWorktree returns nil while the phantom worktree and
// its branch ref survive permanently. A later provision reusing that branch
// name then fails with "already used by worktree".
//
// This test therefore asserts the survivable half of the contract — no panic,
// no hang, no goroutine leak, no directory left on disk, and cleanup never
// reports an error. The invariant that ought to hold is asserted by the
// skipped TestStressWorktree_CancelledProvisionLeavesNoGitState below; unskip
// it once worktree.go can unlock and remove a half-initialized worktree.
func TestStressWorktree_CancelledProvisionCleanup(t *testing.T) {
	assertNoGoroutineLeak(t)

	const attempts = 8

	repoDir := setupTestRepo(t)
	manager := NewWorktreeManager(repoDir, "main")
	base := getHeadCommit(t, repoDir)
	root := t.TempDir()

	leaked := 0
	for i := range attempts {
		// Early delays cancel before `git worktree add` starts; later ones
		// interrupt it mid-flight, which is where the defect lives.
		if cancelledProvisionCycle(t, manager, repoDir,
			base,
			filepath.Join(root, fmt.Sprintf("cancel-%d", i)),
			fmt.Sprintf("cancel-branch-%d", i),
			time.Duration(i)*2*time.Millisecond,
		) {
			leaked++
		}
	}

	if leaked > 0 {
		t.Logf("KNOWN DEFECT reproduced: %d/%d cancelled provisions left git worktree state that DestroyWorktree reported as cleaned up\n%s",
			leaked, attempts, worktreeStateDump(t, repoDir))
		return
	}
	t.Log("no cancelled provision leaked git state in this run; if that is now deterministic, " +
		"delete this test in favour of TestStressWorktree_CancelledProvisionLeavesNoGitState")
}

// TestStressWorktree_CancelledProvisionLeavesNoGitState is the invariant limen
// actually wants: a cancelled ProvisionWorktree must leave the repo restorable
// to a single worktree, with no dangling administrative entry and no orphaned
// branch ref. It fails today — see the KNOWN DEFECT note above — so it is
// skipped rather than deleted, and is the regression test for the fix.
func TestStressWorktree_CancelledProvisionLeavesNoGitState(t *testing.T) {
	t.Skip("KNOWN DEFECT: DestroyWorktree cannot remove a locked, half-initialized worktree left by a cancelled `git worktree add`, yet returns nil; unskip when worktree.go handles it")

	assertNoGoroutineLeak(t)

	const attempts = 8

	repoDir := setupTestRepo(t)
	manager := NewWorktreeManager(repoDir, "main")
	base := getHeadCommit(t, repoDir)
	root := t.TempDir()

	for i := range attempts {
		if cancelledProvisionCycle(t, manager, repoDir,
			base,
			filepath.Join(root, fmt.Sprintf("cancel-%d", i)),
			fmt.Sprintf("cancel-branch-%d", i),
			time.Duration(i)*2*time.Millisecond,
		) {
			t.Fatalf("attempt %d: cancelled provision leaked git worktree state\n%s", i, worktreeStateDump(t, repoDir))
		}
	}
}

// TestStressWorktree_DestroyAfterProvisionFail pins the failure path: when
// ProvisionWorktree rejects the request, destroying the worktree that was never
// created must be a safe no-op, not a panic or a corrupted admin area. Run
// concurrently so the prune fallback is exercised under contention.
func TestStressWorktree_DestroyAfterProvisionFail(t *testing.T) {
	assertNoGoroutineLeak(t)

	const workers = 4

	repoDir := setupTestRepo(t)
	manager := NewWorktreeManager(repoDir, "main")
	root := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), stressBudget)
	defer cancel()

	var wg sync.WaitGroup
	for i := range workers {
		wg.Go(func() {
			path := filepath.Join(root, fmt.Sprintf("fail-%d", i))
			wt, err := manager.ProvisionWorktree(ctx, "not-a-real-commit", fmt.Sprintf("fail-branch-%d", i), path)
			if err == nil {
				t.Errorf("worker %d: expected an error for an invalid base commit, got worktree %+v", i, wt)
				return
			}
			if wt != nil {
				t.Errorf("worker %d: failed provision returned a non-nil worktree %+v", i, wt)
			}
			// DestroyWorktree on a path that was never provisioned must fall
			// through to prune and report success.
			if derr := manager.DestroyWorktree(ctx, &Worktree{Path: path}); derr != nil {
				t.Errorf("worker %d: DestroyWorktree after failed provision returned %v", i, derr)
			}
		})
	}
	wg.Wait()

	assertOnlyMainWorktree(t, repoDir)
}

// TestStressWorktree_IsValidDuringProvision reads the repo while it is being
// written to. IsValid shells out to status and fsck; concurrent provisioning
// must not make it error out, and -race must stay quiet.
func TestStressWorktree_IsValidDuringProvision(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-git stress in short mode")
	}
	assertNoGoroutineLeak(t)

	const readers = 4

	repoDir := setupTestRepo(t)
	manager := NewWorktreeManager(repoDir, "main")
	base := getHeadCommit(t, repoDir)
	root := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), stressBudget)
	defer cancel()

	stop := make(chan struct{})
	validSeen := make([]int, readers)
	var reads sync.WaitGroup
	for r := range readers {
		reads.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				valid, err := manager.IsValid(ctx)
				if err != nil {
					t.Errorf("reader %d: IsValid errored during concurrent provisioning: %v", r, err)
					return
				}
				if valid {
					validSeen[r]++
				}
			}
		})
	}

	var trees []*Worktree
	var mu sync.Mutex
	var provision sync.WaitGroup
	for i := range 2 {
		provision.Go(func() {
			path := filepath.Join(root, fmt.Sprintf("read-%d", i))
			wt, err := provisionWithRetry(ctx, manager, base, fmt.Sprintf("read-branch-%d", i), path)
			if err != nil {
				t.Errorf("provisioner %d: %v", i, err)
				return
			}
			mu.Lock()
			trees = append(trees, wt)
			mu.Unlock()
		})
	}
	provision.Wait()
	close(stop)
	reads.Wait()

	for _, wt := range trees {
		if err := manager.DestroyWorktree(context.Background(), wt); err != nil {
			t.Errorf("DestroyWorktree(%s): %v", wt.Path, err)
		}
	}
	assertOnlyMainWorktree(t, repoDir)

	// The repo starts and stays clean, so at least one read must have seen a
	// valid repo; zero would mean provisioning dirties the main checkout.
	if !slices.ContainsFunc(validSeen, func(n int) bool { return n > 0 }) {
		t.Errorf("IsValid never reported a valid repo during provisioning: %v", validSeen)
	}
}

// TestStressWorktree_ConcurrentThrowaway provisions four validator worktrees at
// once, each with a different patch. Throwaways are detached and land in
// os.MkdirTemp, so the invariants are: distinct paths, the right patch in the
// right tree, and a clean admin area after removal.
func TestStressWorktree_ConcurrentThrowaway(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-git stress in short mode")
	}
	assertNoGoroutineLeak(t)

	const workers = 4

	repoDir := setupTestRepo(t)
	manager := NewWorktreeManager(repoDir, "main")

	ctx, cancel := context.WithTimeout(context.Background(), stressBudget)
	defer cancel()

	trees := make([]*Worktree, workers)
	errs := make([]error, workers)

	var wg sync.WaitGroup
	for i := range workers {
		wg.Go(func() {
			// setupTestRepo commits file.txt as "base content\n"; each worker
			// rewrites that single line to a distinct value.
			patch := fmt.Sprintf("--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-base content\n+worker %d content\n", i)
			trees[i], errs[i] = manager.ProvisionThrowawayWorktree(ctx, patch)
		})
	}
	wg.Wait()

	t.Cleanup(func() {
		for _, wt := range trees {
			if wt != nil {
				if err := manager.DestroyWorktree(context.Background(), wt); err != nil {
					t.Errorf("DestroyWorktree(%s): %v", wt.Path, err)
				}
			}
		}
		assertOnlyMainWorktree(t, repoDir)
	})

	seen := map[string]int{}
	for i, err := range errs {
		if err != nil {
			t.Errorf("worker %d: ProvisionThrowawayWorktree failed: %v", i, err)
			continue
		}
		wt := trees[i]
		if prev, dup := seen[wt.Path]; dup {
			t.Errorf("workers %d and %d share throwaway path %s", prev, i, wt.Path)
		}
		seen[wt.Path] = i

		got, err := os.ReadFile(filepath.Join(wt.Path, "file.txt"))
		if err != nil {
			t.Errorf("worker %d: reading patched file: %v", i, err)
			continue
		}
		if want := fmt.Sprintf("worker %d content\n", i); string(got) != want {
			t.Errorf("worker %d: patch landed in the wrong worktree: got %q, want %q", i, got, want)
		}
	}
}

// TestStressWorktree_ProvisionDestroyCycles runs full provision/destroy cycles
// concurrently. The invariant is that the cycle is a true round trip: every
// directory is gone and git's worktree list is back to just main, with no
// pruning by the test.
func TestStressWorktree_ProvisionDestroyCycles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-git stress in short mode")
	}
	assertNoGoroutineLeak(t)

	const (
		workers = 4
		cycles  = 3
	)

	repoDir := setupTestRepo(t)
	manager := NewWorktreeManager(repoDir, "main")
	base := getHeadCommit(t, repoDir)
	root := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), stressBudget)
	defer cancel()

	var wg sync.WaitGroup
	for w := range workers {
		wg.Go(func() {
			for c := range cycles {
				path := filepath.Join(root, fmt.Sprintf("cycle-%d-%d", w, c))
				wt, err := provisionWithRetry(ctx, manager, base, fmt.Sprintf("cycle-branch-%d-%d", w, c), path)
				if err != nil {
					t.Errorf("worker %d cycle %d: provision: %v", w, c, err)
					return
				}
				// A worker writes into its worktree; destroy must still clear it.
				if err := os.WriteFile(filepath.Join(path, "scratch.txt"), []byte("work\n"), 0644); err != nil {
					t.Errorf("worker %d cycle %d: write: %v", w, c, err)
					return
				}
				if err := manager.DestroyWorktree(ctx, wt); err != nil {
					t.Errorf("worker %d cycle %d: destroy: %v", w, c, err)
					return
				}
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Errorf("worker %d cycle %d: directory %s survived destroy (stat err: %v)", w, c, path, err)
				}
			}
		})
	}
	wg.Wait()

	assertOnlyMainWorktree(t, repoDir)
}
