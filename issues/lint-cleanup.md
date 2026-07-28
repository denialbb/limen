# Issue: codebase lint / hygiene cleanup (real findings only)

Status: Ready for implementation
Plan author: pi (planner session), approved by denial
Implementer: claude instance (herdr pane w4:p1), TDD where a test exists, one commit per slice
Scope: the genuine, safe findings from the project-wide diagnostic sweep. The
false positives are enumerated below — **do not touch them.**

## Background

A full `pi-lens` project scan surfaced a handful of real lint/hygiene findings
plus several ast-grep false positives from generic rules misfiring on
`database/sql`, `json.Unmarshal`, and integer/float `+=`. This issue fixes only
the real ones and explicitly leaves the false positives alone.

## Slice A — CI workflow security hardening

**Files:** `.github/workflows/gofmt.yml`, `.github/workflows/tui-e2e.yml`

Both workflows trigger the same zizmor findings (overly broad permissions,
artipacked credential persistence, unpinned action references). Harden them
identically so the convention is consistent:

1. Add an explicit `permissions:` block at the job (or workflow) level
   restricting to the minimum needed. These workflows only run read-only
   checks + build; nothing writes back to the repo, so:

   ```yaml
   permissions:
     contents: read
   ```

   Place it at the top level (applies to all jobs). Justify in a one-line
   comment that the jobs are read-only.

2. Pin every `uses:` action to its immutable commit SHA, with a comment
   recording the tag it pins. Replace:
   - `actions/checkout@v4` → `actions/checkout@11d5960a326750d5838078e36cf38b85af677262 # v4`
   - `actions/setup-go@v5` → `actions/setup-go@40f1582b2485089dde7abd97c1529aa768e1baff # v5`
   - `actions/setup-python@v5` → `actions/setup-python@a26af69be951a213d495a4c3e4e4022e16d87065 # v5`

3. Suppress the artipacked credential-persistence finding: add
   `persist-credentials: false` to every `actions/checkout` step (neither
   workflow needs a persisted GITHUB_TOKEN after checkout).

**Verification:** re-run `pi-lens` diagnostics on both files (or
`zizmor` if available) and confirm the four findings clear on `gofmt.yml` and
the same class clears on `tui-e2e.yml`. `yamllint`-clean on both. Do not change
job `run:` steps, triggers, or go/python versions.

## Slice B — `interface{}` → `any` modernization sweep

**Files:** `internal/remote/claude.go`, `internal/remote/opencode.go`,
`internal/remote/pi.go`, `internal/tui/tabs/common.go`

16 occurrences of `interface{}` in non-test code, all on JSON-unmarshalled maps
(`map[string]interface{}`, `[]interface{}`, `... .(map[string]interface{})`).
Go 1.18+ aliases `any` to `interface{}` and the module is on `go 1.25.0`, so
this is a pure, behavior-preserving modernization for consistency.

Replace every `interface{}` with `any` in those four files — both type literals
and type-assertion operands. Do NOT touch `_test.go` files in this slice (they
already use `any` in the new code; mixing is fine and not the target).

**Verification:** `go build ./...`, `go test ./...`, and `gofmt -l` clean
on all four files; `git diff` shows only `interface{}` → `any` token swaps (no
whitespace churn, no logic change). A grep for `interface{}` in non-test
`internal/`+`cmd/` must return zero after the sweep.

## Slice C — `min()` builtin modernization

**File:** `internal/retrieval/corpus.go` (the `isBinary` helper, ~line 97)

```go
func isBinary(data []byte) bool {
 n := len(data)
 if n > binaryCheckLen {
  n = binaryCheckLen
 }
 return bytes.IndexByte(data[:n], 0) >= 0
}
```

Replace the `if` clamp with the Go 1.21+ `min` builtin:

```go
n := min(len(data), binaryCheckLen)
return bytes.IndexByte(data[:n], 0) >= 0
```

Equivalent behavior, fewer lines. (Existing `isBinary` tests in
`internal/retrieval` cover the clamp boundary — confirm they still pass; if no
test exercises `len(data) > binaryCheckLen`, add one tiny table case before the
change so the modernization is red-green, not untested.)

**Verification:** `go test ./internal/retrieval/` green; `gofmt -l` clean.

## Hard exclusions — these are FALSE POSITIVES, do NOT change

The scan also flagged these; they are generic ast-grep rules misfiring. Leave
them exactly as they are. If a future reviewer asks, the disposition is
"false-positive — the rule does not apply here."

- **`gorm-n-plus-one` on `internal/state/sqlite.go:135,317,550,584` and
  `internal/retrieval/corpus.go:76`** — the lint tag says GORM, but the code
  uses `database/sql` (`db.Query` + `rows.Next()` scan loop) and git
  subprocesses, NOT GORM. SQLite migration ALTER loop (L135) and standard
  rows-iteration (L317/550/584) are not N+1. `corpus.go:76` spawns `git show`
  per file — a deliberate best-effort read loop, not a query, and batching it
  (e.g. `git cat-file --batch`) is an unrelated performance optimization, out
  of scope here. Do NOT refactor.
- **`nil-map-assignment` on `internal/retrieval/manifest_test.go:24` and
  `internal/tui/tabs/common.go:86`** — `var m map[string]any` followed by
  `json.Unmarshal(raw, &m)` is safe: `json.Unmarshal` allocates a nil map. Not a
  bug. `common.go:86`'s real finding (the `interface{}`) is fixed in Slice B.
- **`string-concat-in-loop` on `internal/retrieval/bm25.go:53` and
  `internal/retrieval/pipeline.go:190`** — both are `+=` on `int`/`float64`
  (`totalLen += len(d)`, `scores[...] += sc.Score`), not string concatenation.
  The rule matched `+=` over-eagerly. Do NOT introduce `strings.Builder`.
- **`go-test-functions` advisory (~90 occurrences)** — this rule fires on every
  `func TestXxx` to "verify the naming convention"; it is an advisory, not a
  violation, and the repo's test naming already follows Go convention. Do NOT
  rename or touch any test.
- **`minmax:default` re-fires on `corpus.go:97`** — that IS the Slice C target;
  fixing it in Slice C also clears this. No separate action.

## Conventions

- Branch `chore/lint-cleanup` off `main`.
- One commit per slice, prefixed `chore(lint):` (e.g.
  `chore(lint): pin workflow actions to SHAs + restrict permissions`).
- `go test ./...` + `gofmt -l internal/ cmd/` green before each commit.
- Do NOT touch any `internal/orchestrator/`, `internal/state/sqlite.go` query
  bodies, or `_test.go` files (Slice B targets non-test only; Slice C may add
  one tiny `isBinary` table case only if coverage is missing).

## Acceptance

- Slice A: both workflow files pass zizmor (or the equivalent pi-lens scan) with
  zero `excessive-permissions`/`artipacked`/`unpinned-uses` findings; pinned
  SHAs match the tags above; `persist-credentials: false` on every checkout.
- Slice B: `rg 'interface\{\}' internal/ cmd/ --glob '!*_test.go'` returns
  nothing; `go test ./...` green; diff is token-only `interface{}` → `any`.
- Slice C: `isBinary` uses `min`; retrieval tests green; gofmt clean.
- No false-positive site was modified.
