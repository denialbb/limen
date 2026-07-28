package remote

import (
	"sort"

	"github.com/denialbb/limen/internal/bus"
)

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
