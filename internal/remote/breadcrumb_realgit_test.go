package remote

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/denialbb/limen/internal/bus"
	"github.com/denialbb/limen/internal/git"
	"github.com/denialbb/limen/internal/state"
)

// setupBreadcrumbRepo creates a real git repo with one committed file and a
// .gitignore, so the gitignore-aware claim can be asserted rather than assumed.
func setupBreadcrumbRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}

	run("init", "-b", "main")
	run("config", "user.name", "Test User")
	run("config", "user.email", "test@example.com")

	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("base content\n"), 0644); err != nil {
		t.Fatalf("write tracked.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.log\n"), 0644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	run("add", "tracked.txt", ".gitignore")
	run("commit", "-m", "initial commit")

	return dir
}

// TestBreadcrumbsRealGitWorktree drives the whole breadcrumb path against a
// real worktree and the real `git status --porcelain` subprocess — no injected
// reader. It is the only test that exercises gitStatusReader end-to-end, so it
// is gated behind LIMEN_E2E_REAL_AGENTS=1 (matching the RealBinary suites) to
// keep CI fast and free of subprocess timing.
//
// Run with:
//
//	LIMEN_E2E_REAL_AGENTS=1 go test ./internal/remote/ -run RealGitWorktree
func TestBreadcrumbsRealGitWorktree(t *testing.T) {
	if os.Getenv("LIMEN_E2E_REAL_AGENTS") != "1" {
		t.Skip("set LIMEN_E2E_REAL_AGENTS=1 to run the real-git breadcrumb e2e")
	}
	if runtime.GOOS == "windows" {
		t.Skip("fake shell script fixture requires a POSIX shell")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}

	dir := setupBreadcrumbRepo(t)

	// An eventless CLI that prints nothing while it works: it creates an
	// untracked file, modifies a tracked one, and drops a gitignored artifact,
	// with pauses long enough for several polls to land between the changes.
	script := filepath.Join(dir, "fake-eventless.sh")
	fixture := `#!/bin/sh
sleep 0.4
echo 'new content' > new_file.txt
sleep 0.4
echo 'more' >> tracked.txt
echo 'noise' > build.log
sleep 0.4
echo 'I made the change.'
`
	if err := os.WriteFile(script, []byte(fixture), 0755); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	w := newAgentWorkerCmd(agyDialect(), []string{"/bin/sh", script}, defaultOptions())
	// Real reader (no injection); only the cadence is shortened so the test does
	// not have to run for multiples of the 1.5s production interval.
	w.breadcrumbInterval = 100 * time.Millisecond

	rec := bus.NewRecorderEmitter()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := w.ProduceSolution(ctx, &state.Task{ID: "task-real"}, &git.Worktree{Path: dir}, "", rec); err != nil {
		t.Fatalf("ProduceSolution: %v", err)
	}

	crumbs := rec.EventsByKind("WorkerBreadcrumb")
	if len(crumbs) == 0 {
		t.Fatalf("no breadcrumbs from a real worktree; events %v", kindsOf(rec.Events()))
	}

	// Collect the final status seen for each path across every breadcrumb.
	seen := map[string]string{}
	for _, ev := range crumbs {
		for _, f := range ev.(*bus.WorkerBreadcrumb).Files {
			seen[f.Path] = f.Status
		}
	}

	// The worker's new file surfaces as untracked...
	if got, ok := seen["new_file.txt"]; !ok || got != "??" {
		t.Fatalf("new_file.txt = %q (present=%v), want %q; saw %#v", got, ok, "??", seen)
	}
	// ...and its edit to a tracked file surfaces as modified.
	if got, ok := seen["tracked.txt"]; !ok || got != " M" {
		t.Fatalf("tracked.txt = %q (present=%v), want %q; saw %#v", got, ok, " M", seen)
	}
	// The gitignored artifact must never appear: porcelain honours .gitignore,
	// which is what keeps the poll cheap and the stream signal-only.
	if _, ok := seen["build.log"]; ok {
		t.Fatalf("gitignored build.log leaked into breadcrumbs; saw %#v", seen)
	}
	// The script itself is untracked and legitimately visible; assert nothing
	// about it beyond that it did not break the deltas above.

	// The run still terminates normally, with the breadcrumb stream fully
	// contained between start and finish.
	kinds := kindsOf(rec.Events())
	if kinds[0] != "WorkerStarted" {
		t.Fatalf("first event = %q, want WorkerStarted (kinds %v)", kinds[0], kinds)
	}
	if last := kinds[len(kinds)-1]; last != "WorkerFinished" {
		t.Fatalf("last event = %q, want WorkerFinished (kinds %v)", last, kinds)
	}
}
