# Spike 017: TUI E2E — Headless CI Gate, Dialect Matrix, Interaction Goldens

Captured 2026-07-25 by a claude instance (Opus 4.8) on branch
`feat/tui-e2e-matrix` (off `main` HEAD `1fc5576`). Extends spike 016 (an agent
can drive the limen TUI autonomously) to the full follow-up list: a
deterministic headless CI gate, the remaining worker dialects through the TUI,
a real Python repo/prompt, and golden captures of the escalation path, both
layouts, and a new `?` help overlay.

Text captures referenced below live in `docs/spikes/017-captures/`.

Tool versions: pi 0.82.1, claude 2.1.195, opencode 1.18.4, agy 1.1.5,
go 1.25.3, python 3.14.6, pytest 9.1.1, tmux 3.7b.

## Real worker-run log (budget: ≤ 5; used 4)

| # | task-id | dialect | fixture / validator | outcome | capture |
|---|---|---|---|---|---|
| 1 | `tui-claude-1`   | claude   | Go / shell `go test ./...`   | COMMITTED, 0 retries | `claude.txt` |
| 2 | `tui-opencode-1` | opencode | Go / shell `go test ./...`   | COMMITTED, 0 retries | `opencode.txt` |
| 3 | `tui-agy-1`      | agy      | Go / shell `go test ./...`   | COMMITTED, 0 retries | `agy.txt` |
| 4 | `tui-pi-py-1`    | pi       | Python / shell `python -m pytest -x` | COMMITTED, 0 retries | `fixture-b-python.txt` |

No limen bug surfaced in any run, so no re-run was needed and one real run
remains unspent. All escalation/layout/help evidence below is mock (zero-token).

## Dialect matrix (slice 2)

One real run per remaining worker dialect against the buggy-`add` Go fixture
(`reset-test-repo.sh tmp/test-repo`), validator `--validator-backend shell
--validator-cmd "go test ./..."`, floors=0. Each was driven to a terminal state
via `tui-wait.sh`, then the worker detail was opened (`w` → `Enter`) and the
full screen captured.

- **claude** (RPC-family, stream-json): renders the complete worker stream —
  `agent:` messages and `tool call: Read/Edit/Bash {…}` with the tool's JSON
  args. Reached COMMITTED with 0 retries.
- **opencode** (RPC-family, stream-json): same shape, but tool names are
  lowercase (`read/edit/bash`) and args carry opencode's own keys
  (`filePath/newString/oldString/workdir`). Worker turn ~2m10s; COMMITTED.
- **agy** (one-shot family, plain text): as the dialect contract specifies,
  `decodeAgyEvent` emits no bus events, so **only the worker Started/Finished
  breadcrumbs surface in the timeline** — there is no tool-call/agent-message
  stream. The worker-detail pane holds only the validator criterion + verdict
  (which the model appends to the selected worker). This is the documented
  behavior, **not a limen bug**; git-poll breadcrumbs remain a future slice.
  COMMITTED with 0 retries.

Takeaway: the render path (worker detail + timeline) is dialect-correct across
all three. An agent asserting on a run must key off the dialect: expect a
tool-call stream for claude/opencode/pi, only breadcrumbs for agy.

## Fixture B — real Python repo, real prompt (slice 3)

`reset-test-repo.sh <path> python` scaffolds `calc.py` (buggy `add`) +
`test_calc.py`. One real **pi** run (dialect held fixed — proven in 016 — so the
new variable is language + validator), validator `python -m pytest -x`, natural
prompt. pi patched `calc.py` with `sed`, called `limen ready-for-review`, pytest
reported `1 passed`, and the run reached COMMITTED (0 retries). The shell
validator's PASS verdict embeds the full pytest session output. Capture:
`fixture-b-python.txt`.

Env caveat: Arch's system Python is PEP-668 externally-managed and shipped no
pytest, so pytest was installed to the **user site**
(`pip install --user --break-system-packages pytest`) — no system packages
touched — after which `python -m pytest` resolves. CI installs pytest via
`setup-python`/pip instead; this only affects local fixture-B runs.

## FAILED_ESCALATED path (slice 3, mock)

New transcript `src/limen/mock/transcripts/always-fail.json`: router PROCEEDs,
the worker writes a solution every attempt, the validator rejects every attempt.
Driven through the tmux harness with `--mock-transcript …/always-fail.json`
(`tui-wait.sh --pattern FAILED_ESCALATED`). The orchestrator escalates once
`RetryCount >= MaxRetries` (default 3), i.e. after 4 rejected attempts.

Observed (`escalation-split.txt`): header `FAILED_ESCALATED  retries:3`; the
timeline records the full reject chain (worker `retry=0..3`, each verdict FAIL,
`AWAITING_VALIDATION → REVISION_REQUESTED → WORKER_RUNNING` loop) ending
`AWAITING_VALIDATION → FAILED_ESCALATED` + `orchestrator error: validation
failed`; the Workers panel shows `worker-0..3` all `✗`. **No commit lands on the
fixture** — `tmp/test-repo` HEAD was byte-identical before and after the run
(worktree discarded on escalation). `q` cleanup works.

## Layout goldens (slice 3, mock)

- **Tab layout** (`tab-layout.txt`), terminal 100x28 (below the 120/30 split
  thresholds): header + single active tab body + tab strip + hint. Captured
  each tab via `1`-`4` (Router/Worker/Validator/Timeline) and the help overlay.
- **Split layout** (`split-layout.txt`), terminal 220x50: left column
  Router+Validator, right column Timeline/WorkerDetail over the Workers panel.
  Golden path `spike.json` reject→revise run: header `COMMITTED  retries:1`,
  Workers `✗ worker-0` / `✓ worker-1`, and both worker details (worker-0 shows
  the buggy write + FAIL, worker-1 the fix + PASS).

## `?` help overlay (slice 3, new code)

`interactive_tui.md` listed a `?` help overlay in its decision table, but
`model.go handleKey` had **no `?` case** — it was missing. Implemented TDD in
`internal/tui/`:

- `?` toggles a centered, bordered keybinding box over the content region in
  **both** layouts; `Esc` or `?` close it; quit keys still quit; while open it
  swallows other input so it can't drive the hidden UI. The box lists the keys
  that are actually live for the current layout, and both View hints now
  advertise `[?] help`.
- Component tests: `TestHelpOverlayTabLayout`, `TestHelpOverlaySplitLayout`,
  `TestHelpOverlayDoesNotQuit` (open/close semantics + the layout-specific key
  list). Live captures of the rendered overlay are in `tab-layout.txt` and
  `escalation-split.txt`.

## Headless CI gate (slice 1)

`scripts/test-tui-e2e.sh` drives the zero-token Python mock backend to a
terminal state two ways and asserts on the observable output:

- **pipe mode** (always): pipes `./limen`'s output so `isTTY()` is false, forcing
  the `runTaskOneShot` log-style fallback; asserts the lifecycle logs
  `Task completed with state: COMMITTED`.
- **tmux mode** (when tmux is present): the full `tui-start`/`tui-wait`/capture
  loop at 220x50; asserts the `COMMITTED` header + router timeline lines, then
  quits with `q` and tears the session down. Self-skips if tmux is absent.

`.github/workflows/tui-e2e.yml` runs it on push/PR to `main` (ubuntu-latest,
go 1.25, `setup-python` for the hardcoded `python` mock invocation). tmux is
preinstalled on ubuntu-latest, so both modes run in CI. `gofmt.yml` is untouched.
No agent CLIs, no tokens, no network beyond checkout/setup.

Note on the gofmt gate: this arc kept `gofmt -l` clean only on the files it
changed. The pre-existing 1.25-vs-earlier comment-alignment skew in
`internal/retrieval/` and `internal/router/` is out of scope and was left alone.

## Remaining follow-ups

- **agy git-poll breadcrumbs** (PRD #13): surface agy's file edits as events so
  its worker detail shows more than the validator criterion/verdict.
- **PNG goldens / visual diffing**: current goldens are text captures; a
  screenshot-diff gate would catch styling regressions the text can't.
- **Validator-dialect matrix**: this arc drove worker dialects only; agy/claude
  as L3 validators (`--validator-backend`) through the TUI is untested here.
- **Multi-task TUI**: all runs were single-task; concurrent tasks/sessions
  remain unexercised.
- **Component-test goldens for live layouts**: the split/tab captures are
  script-level goldens under `017-captures/`; freezing a byte-exact rendered
  frame as an `internal/tui/` golden (seeded model → `View()`) would make layout
  regressions fail `go test` directly.
