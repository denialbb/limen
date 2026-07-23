# Acceptance test: three forced-branch e2e variants plus isolated retrieval unit tests

## Status

accepted

## Context

`internal/integration/e2e_worker_validator_test.go` asserts the full
worker→validator→COMMITTED loop reaches `COMMITTED` after two validation
decisions. The handoff called for "extending" it; re-reading it surfaces a
load-bearing fact the handoff missed: its prompt is the literal string
`"test prompt"` and its only corpus is `math.txt` containing the single token
`"empty"`.

When `cliRetriever` returns a real manifest (this arc's whole point), zero
query terms match the corpus (Q5's whole-token rule: `"test"` and `"prompt"`
have whole-token forms, neither appears in `"empty"`) → `coverage_hint == 0`
→ the Q7 escape hatch (ADR 0005) fires on pass 1 → ESCALATE → no worker runs
→ no COMMITTED. The existing e2e breaks the moment retrieval goes live. Q10
must grapple with that, not just "add tests."

Q7's "cutpoints as Stage-impl config" (ADR 0005) is the keystone: tests can
force each Router branch deterministically by setting cutpoints to extreme
values, without machining the corpus.

## Decision

**Three forced-branch e2e variants**, pinned via `--coverage-floor` /
`--confidence-floor` flags on `run-task`. The existing e2e becomes the
PROCEED variant.

- **`TestEndToEnd_ProceedOnLowFloors`** (the existing e2e repurposed): floors
  `=0,0` → any retrieval passes → worker runs → COMMITTED. Asserts the
  existing two-validation-decision loop still works AND that the manifest
  reached the worker prompt (Q9 — inspect the pi mock's logged stdin for
  the `## Context` section rendered by ADR 0007).
- **`TestEndToEnd_EscalateOnZeroCoverage`** (the escape-hatch regression
  test): defaults floors; prompt `"test prompt"` vs the `math.txt`/`empty`
  corpus → `coverage_hint == 0` → escape hatch fires on pass 1, **no EXPAND
  iterations**. Asserts: exactly one `ContextBuilt` event, task ends in
  `StateFailedEscalated`, never reaches the worker. Regression test for the
  escape hatch the user insisted on in Q7.
- **`TestEndToEnd_ExpandUntilConvergence`**: prompt partially matches the
  corpus (e.g. `"add a and b in math.txt then frobnicate"` — `add`/`a`/`b`/
  `math` match, `frobnicate` doesn't); set `coverage_floor` between actual
  coverage and 1 so EXPAND fires. Asserts: more than one `ContextBuilt`
  event (expand iterations occurred); **primary assertion on `query_id`'s
  `#<expand-iteration>` counter** in the final manifest, read back from
  `task.context_snapshot`; the loop converges or exhausts.

**Isolated unit tests in `internal/retrieval/`** (separate package), running
in `go test ./...` short mode:

- BM25 scoring against a handcrafted corpus+query (precise score assertions
  — the math the e2e cannot isolate).
- Analyzer split-and-preserve: `getUserName → {getusername, get, user,
  name}`; stopword removal; survival exclusion (ADR 0003).
- Chunker line-window + ~10-line overlap; dedup rule (ADR 0004).
- `confidence` saturation `min(1, topChunkBM25 / ΣIDF)` and `coverage_hint`
  whole-token recall, with the survival exclusion (ADRs 0002, 0003).

**Update the existing e2e this arc** so `go test ./...` stays green through
the retrieval landing. The existing test would break the moment
`cliRetriever` returns a real manifest; leaving it broken means a red test
suite from the moment retrieval lands.

## Considered Options

- **Table-driven / parameterized single test.** Rejected: each variant's
  fixture setup differs (EXPAND needs a corpus-matching prompt; PROCEED can
  stay `"test prompt"` with floors=0). A table forces a
  lowest-common-denominator fixture and hides per-branch intent. Three
  distinct `TestEndToEnd_*` functions read as documentation of each branch's
  contract.
- **EXPAND detection via event count alone.** Rejected as *primary*
  assertion: `ContextBuilt` event count conflates "passes occurred" with
  "EXPAND widened and re-retrieved." The precise EXPAND-occurred signal is
  the `query_id`'s `#<iter>` counter (ADR 0004). Cross-check on event count;
  assert primarily on `query_id`.
- **e2e-only; defer unit tests to the implement-arc.** Rejected: the e2e
  proves integration (retrieval + router + worker-consumption) but is a
  10s+ test skipped in `testing.Short()`. It cannot fail in BM25-specific
  or analyzer-specific ways — the places where real bugs hide. Unit tests
  give precise control over the math and run in short mode. The repo's
  testing guideline ("tests that can't fail are not tests") favours tests
  that *can* fail narrowly.
- **Leave the existing e2e broken until retrieval lands.** Rejected: a red
  `go test ./...` from the moment `cliRetriever` returns a real manifest is
  the wrong signal — it obscures whether a new retrieval commit broke
  something or the test was already known-broken. Updating it in-arc keeps
  the suite green as a continuous invariant.

## Consequences

- `run-task` gains `--coverage-floor` / `--confidence-floor` flags, plumbed
  to the Router's cutpoint config (ADR 0005). The flags are test
  infrastructure; defaults remain `(coverage_floor=0.60,
  confidence_floor=0.50)` per ADR 0005.
- The existing `TestEndToEndWorkerValidatorLoop` is renamed
  `TestEndToEnd_ProceedOnLowFloors` and extended with an assertion that the
  pi mock's logged stdin contains the `## Context` section (ADR 0007).
- New `internal/retrieval/` package (the implementation arc's home, per the
  handoff's post-design recommendation) carries unit tests that pin the
  Stage math independently of the e2e.
- The acceptance gates for this arc, in order of grilling: PROCEED variant
  green; ESCALATE-on-zero-coverage variant green; EXPAND-iterations variant
  green; unit tests for the four Stage-class concerns green; `go test
  ./...` clean in both short and long modes.