# Issue 017: TUI E2E — Headless CI Gate, Dialect Matrix, Interaction Goldens

Status: Ready for implementation
Plan author: pi (planner session, w3:p1), directed by denial
Implementer: fresh claude session (herdr pane w4:p1)

## Context

Issue 016 (merged to main) proved an agent can drive the limen TUI
autonomously: harness hardened (`tui-start.sh` PYTHONPATH fix, `tui-wait.sh`),
one real pi worker run to COMMITTED, `WorkerToolCall`/`WorkerAgentMessage`
rendering verified. Spike: `docs/spikes/016-autonomous-tui-testing.md`.

This arc extends that to the full follow-up list: a deterministic headless CI
gate, the remaining worker dialects (claude, opencode, agy) through the TUI,
real test repos with real prompts, and golden-capture coverage of the golden
path and the other TUI interactions.

## Facts the plan relies on (verified by planner)

- `scripts/tui-*.sh` harness: start (220x50 → split layout), wait, capture,
  send, stop. Split keys: `w`/`Enter`/`Esc`/`j`/`k`/`q`. Tab keys (narrow):
  `1`-`4`, `[`/`]`, `j`/`k`, `q`.
- `interactive_tui.md` decision table lists a `?` help overlay, but
  `internal/tui/model.go` `handleKey` has **no `?` case** — likely missing.
  Verify; if absent, implement TDD (small: render keybinding list overlay,
  component test in `internal/tui/`).
- Mock validator transcripts are verdict lists consumed in order
  (`src/limen/mock/transcripts/spike.json`). An all-fail transcript gives a
  **deterministic, zero-token FAILED_ESCALATED run** (retries exhaust at
  MaxRetries, default 3).
- TTY fallback: piped stdout skips Bubble Tea and emits log-style lines
  (`runTaskOneShot`). This is the no-tmux headless path.
- CI today = `.github/workflows/gofmt.yml` only (gofmt gate, go 1.25).
- **gofmt version skew**: local gofmt (1.25.3) flags 18 pre-existing files in
  `internal/retrieval/` + `internal/router/` (comment-list alignment). NOT this
  arc's problem. Gate = `gofmt -l` on **changed files only**. Do not reformat
  untouched files.
- `cmd/limen/main.go` invokes mock via `python -m limen.mock.*`; ubuntu CI has
  `python3`, maybe not `python` — the CI job must provide `python`
  (setup-python or symlink) since the Go side hardcodes `python`.
- Token budget: **≤ 5 real worker runs** for the whole arc. Mock first always.

## Scope

### Slice 1 — headless CI gate (deterministic, no tokens)

- `scripts/test-tui-e2e.sh`: drives the mock lifecycle headlessly and asserts:
  - **Pipe mode** (no tmux): `./limen --task-id … --repo-path … --mock=true
    --coverage-floor=0 --confidence-floor=0 | tee` the output; assert state
    transitions through to `COMMITTED`/`FINALIZED` appear as log lines.
  - **tmux mode** (when tmux present): full `tui-start`/`tui-wait`/capture
    loop; assert `COMMITTED` header + key timeline lines; `q` + stop.
  - Exit non-zero with a clear message on any failed assertion.
- `.github/workflows/tui-e2e.yml`: ubuntu-latest, go 1.25, python available
  as `python`, build `./limen`, run the script. Push/PR to main, same as
  gofmt.yml. Do not modify gofmt.yml.
- Mock backend only in CI — no agent CLIs, no tokens, no network beyond
  actions/checkout+setup.

### Slice 2 — dialect matrix TUI passes (3 real runs)

One real run per remaining worker dialect against the buggy-add fixture
(`./scripts/reset-test-repo.sh tmp/test-repo`), validator
`--validator-backend shell --validator-cmd "go test ./..."`,
`--coverage-floor=0 --confidence-floor=0`:

- `--worker-backend claude`
- `--worker-backend opencode`
- `--worker-backend agy` — plain-text one-shot dialect; expected rendering is
  worker Started/Finished breadcrumbs only (no tool-call stream). Assert what
  the dialect contract actually provides; document what renders.

For each: block on `tui-wait.sh`, capture full screen + worker detail
(`w`/`Enter`), save text captures to `docs/spikes/017-captures/<dialect>.txt`,
assert COMMITTED (or document precisely why not). If a failure is a **limen
bug** (dialect wiring, event mapping, render crash), fix TDD and re-run — that
re-run does not count against the token budget. If it's **model behavior**
(agent didn't finish the task), document and move on.

### Slice 3 — real repos, real prompts, other paths & interactions

- **Fixture B (new, real)**: extend `scripts/reset-test-repo.sh` (or add a
  sibling script) to scaffold a small **Python** repo (buggy function +
  pytest test). One real run (pick pi worker — already proven, keeps dialect
  variable fixed), validator `--validator-cmd "python -m pytest -x"`, natural
  prompt. Assert COMMITTED + rendering. (1 real run.)
- **FAILED_ESCALATED (mock, free)**: new transcript
  `src/limen/mock/transcripts/always-fail.json` (all verdicts fail). Run via
  tmux harness; assert header shows retries climbing to MaxRetries, terminal
  state `FAILED_ESCALATED`, timeline shows the full reject chain, `q` cleanup
  works (no commit lands on the fixture).
- **Tab layout goldens (mock, free)**: spawn at 100x28 (below the 120/30
  split thresholds), drive `1`-`4`/`[`/`]`/`j`/`k`, capture each tab.
- **Split layout goldens (mock, free)**: golden path capture set — header
  states, workers panel `✗`/`✓` across the reject→revise run, worker detail,
  timeline.
- **`?` help overlay**: verify presence; if missing, implement TDD in
  `internal/tui/` (both layouts) with component-test coverage, then capture
  it in both layouts.
- **Component-test goldens**: freeze the captures as golden tests where they
  belong (`internal/tui/` for rendered models; script-level goldens under
  `docs/spikes/017-captures/` for the live runs). Follow existing test style
  (`component_test.go`, `tui_test.go`).

### Slice 4 — report + close

- `docs/spikes/017-tui-e2e-matrix.md`: matrix results per dialect (with
  capture references), fixture-B result, escalation/tab/help findings,
  headless CI gate description, remaining follow-ups.
- Update `.agents/skills/limen-tui/SKILL.md` only if the loop changed
  (new script, CI gate, fixtures).
- Move this issue to `issues/done/` in the final commit.

## Constraints

- Branch `feat/tui-e2e-matrix` off **main** (015+016 are merged; main HEAD
  `1fc5576`).
- TDD, one commit per slice (fixture script + transcript may ride with slice
  3). `go test ./...` green + `gofmt -l` clean on changed files before each
  commit.
- Do NOT touch: `README.md` (uncommitted user changes), orchestrator,
  pre-existing gofmt-skewed files (`internal/retrieval/`, `internal/router/`).
- Do NOT commit `tmp/`, `limen.db*`, fixture repos.
- Real runs: mock-first always; ≤ 5 real worker runs total; one dialect
  variable per run; record every real run (task-id, dialect, outcome) in the
  spike doc.
- If spec contradicts observed code, or a fix would touch the orchestrator /
  dialect seam semantics, stop and ask the planner.

## Out of scope

- Push/PR (planner holds), validator-dialect matrix (agy/claude as L3),
  multi-task TUI, v2 control features, gofmt-skew cleanup.
