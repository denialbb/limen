package remote

import (
	"context"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/denialbb/limen/internal/bus"
	"github.com/denialbb/limen/internal/orchestrator"
)

// breadcrumbInterval is the git-poll cadence for eventless dialects (PRD #13:
// "~1.5s"). One short-lived `git status --porcelain` subprocess per tick keeps
// the cost bounded; the delta filter keeps the bus quiet between real changes.
const breadcrumbInterval = 1500 * time.Millisecond

// breadcrumbReader is the injectable seam between the poller and git. Tests
// supply a scripted sequence; the real driver supplies gitStatusReader(dir).
// This mirrors the dialect seam, where decode is a pure function over a line:
// the poller likewise splits into a pure delta (diffBreadcrumbSets) plus an
// injected effectful read.
type breadcrumbReader func(ctx context.Context) (map[string]string, error)

// diffBreadcrumbSets returns the delta between the previous and current
// porcelain snapshots: entries that are newly present or whose XY status code
// changed. Entries that disappeared (file reverted/staged away) are
// intentionally NOT emitted — the breadcrumb is a "what changed now" signal,
// not a ledger. Equal sets return nil (delta-only noise suppression, PRD #13).
//
// The result is sorted by Path so a given snapshot pair always produces the
// same event, keeping breadcrumb streams replayable in test assertions.
func diffBreadcrumbSets(prev, curr map[string]string) []bus.BreadcrumbFile {
	var out []bus.BreadcrumbFile
	for path, status := range curr {
		if old, ok := prev[path]; ok && old == status {
			continue
		}
		out = append(out, bus.BreadcrumbFile{Path: path, Status: status})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// pollBreadcrumbs periodically calls read, computes the delta against the
// previous snapshot, and publishes a WorkerBreadcrumb for each non-empty delta.
// It returns when ctx is done, so the caller's cancel is the only stop signal
// it needs.
//
// Everything here is observability exhaust and treated as such: a reader error
// is swallowed (a transient git hiccup emits nothing, keeps the previous
// snapshot, and never interrupts the worker run), a nil emitter is a no-op, and
// nothing the poller sees can influence a state transition. Breadcrumbs never
// reach the canonical SQLite (determinism boundary §2).
//
// The first poll compares against an empty snapshot, so a worktree that is
// already dirty when the worker starts surfaces as one initial breadcrumb.
// Worktrees are provisioned clean, so in practice the first delta is the
// worker's own first edit.
func pollBreadcrumbs(ctx context.Context, interval time.Duration, read breadcrumbReader, taskID string, em orchestrator.Emitter) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	prev := map[string]string{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		curr, err := read(ctx)
		if err != nil {
			// Non-fatal: keep prev so the next successful poll reports the delta
			// against the last known-good snapshot rather than replaying it whole.
			continue
		}
		delta := diffBreadcrumbSets(prev, curr)
		prev = curr
		if len(delta) == 0 || em == nil {
			continue
		}
		em.Publish(&bus.WorkerBreadcrumb{
			TaskID:    taskID,
			Files:     delta,
			Timestamp: time.Now(),
		})
	}
}

// gitStatusReader builds the real breadcrumb reader: one short-lived
// `git -C <dir> status --porcelain --untracked-files=normal` per call. The
// porcelain form respects .gitignore (so build artifacts stay out) while still
// surfacing untracked worker-created files. The command is bound to the poll
// context so cancellation kills it promptly.
//
// git failures are returned to the caller, which treats them as a no-op poll —
// a dirty index lock or a worktree torn down mid-run must never fail the run.
func gitStatusReader(dir string) breadcrumbReader {
	return func(ctx context.Context) (map[string]string, error) {
		cmd := exec.CommandContext(ctx, "git", "-C", dir, "status", "--porcelain", "--untracked-files=normal")
		out, err := cmd.Output()
		if err != nil {
			return nil, err
		}
		return parsePorcelain(out), nil
	}
}

// parsePorcelain turns `git status --porcelain` output into a {path -> XY}
// snapshot. Each line is "XY<space><path>", with repo-relative paths.
//
// CAVEAT (v1): paths are taken verbatim from column 4 onward. Porcelain quotes
// and octal-escapes paths containing spaces or non-ASCII bytes (`"a\303\251"`),
// and renders renames as `old -> new`; neither is decoded here. The effect is
// cosmetic — a breadcrumb line shows the raw porcelain path — and the delta
// still fires, because the escaped form is stable across polls. Upgrade to
// `-z` + NUL splitting if breadcrumbs ever need exact paths.
func parsePorcelain(out []byte) map[string]string {
	snap := make(map[string]string)
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if len(line) < 4 {
			continue
		}
		snap[line[3:]] = line[:2]
	}
	return snap
}
