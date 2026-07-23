# Router thresholds: two-cutpoint cascade with a zero-coverage escape hatch

## Status

accepted

## Context

The Router's only inputs are the manifest (`confidence`, `coverage_hint`,
parsed back out of `context_snapshot`; ADRs 0002, 0004), the `expandCount`,
and `maxExpandIterations = 5` (`internal/orchestrator/orchestrator.go:36`,
252-259). Three outcomes: `PROCEED`, `EXPAND`, `ESCALATE`
(`orchestrator.go:24-30`). Coverage-gates-first semantics locked in ADR 0002.

This decision fixes the cutpoint *meta-rule*, starter values, and three
sub-decisions the threshold logic requires. EXPAND's *widening* behavior is
the EXPAND-semantics decision (Q8); the threshold logic is what *lights*
EXPAND.

## Decision

**Meta-rule (the load-bearing part):** cutpoints are two scalars
`(coverage_floor, confidence_floor)`, **Stage-impl configuration** loaded
once, not compile-time constants the Router hard-codes. The Router is a pure
projection over `(confidence, coverage_hint)` plus the cutpoints. This keeps Stage tuning out of the Router and lets the acceptance
test (Q10) set cutpoints to extreme values to force each branch
deterministically — the only way to test the Router without a full retrieval
corpus.

**Starter values** (defaults, justified):

| cutpoint | value | why |
|---|---|---|
| `coverage_floor` | 0.60 | ~3 of 5 surviving query terms covered; below this the query names things no chunk touched |
| `confidence_floor` | 0.50 | `topChunkBM25 / ΣIDF ≥ 0.5`: the best chunk captured ≥ half the query's discriminative mass |

**Cascade (with escape hatch):**

```
0. coverage_hint == 0                       → ESCALATE   (escape hatch; no EXPAND)
1. coverage_hint < coverage_floor (> 0)     → EXPAND
2. else, confidence < confidence_floor       → ESCALATE
3. else                                      → PROCEED
```

**Exhaustion is orchestrator-side, not Router-side.** The Router never sees
`expandCount` or `maxExpandIterations`; it emits `EXPAND` whenever rule 1
applies. The orchestrator honors `EXPAND` only if `expandCount <
maxExpandIterations`, else escalates — already implemented at
`internal/orchestrator/orchestrator.go:252-257`. The Router's inputs are
purely `(confidence, coverage_hint)` parsed from `task.ContextSnapshot`.

**Three sub-decisions:**

1. **EXPAND exhausted, coverage still below floor — ESCALATE.** Falling through
   to PROCEED would silently run the worker on known-incomplete context —
   exactly the failure ESCALATE exists to flag. PROCEED-after-exhaustion would
   make the cap a licence to give up.
2. **Escape hatch trigger: `coverage_hint == 0`.** Zero coverage after a
   retrieve pass means *no surviving query term matched any retrieved chunk's
   whole-token form* — the query names things the corpus doesn't contain in
   any recognizable form, and widening the candidate search cannot conjure
   terms that aren't there. EXPAND on `coverage == 0` burns the
   `maxExpandIterations` budget for nothing then escalates anyway. The escape
   hatch fires EXPAND only when `0 < coverage_hint < floor` — the regime
   where widening has a real chance. Trigger is `coverage_hint == 0`, **not**
   `len(chunks) == 0`: the structural stage can pad the top-k with
   zero-coverage chunks, so `len(chunks) == 0` misses real all-OOB cases.
3. **Cutpoints are absolute, not corpus-relative.** ADR 0002's confidence is
   *query*-normalized (`topChunkBM25 / Σ IDF(query)`): both numerator and
   denominator scale with the query's rarity, so corpus-size drift mostly
   cancels. Absolute cutpoints ride that normalization.

## Considered Options

- **No escape hatch (the original Q7 draft).** Rejected: `coverage == 0` would
  EXPAND 5 times then escalate — a degenerate walk through EXPAND on a query
  whose terms don't exist anywhere in the corpus. Conflates "wrong-corpus /
  typo query" with "low-but-real coverage," and wastes the budget.
- **`len(chunks) == 0` escape hatch.** Rejected: weaker than
  `coverage_hint == 0`. The structural stage can surface zero-coverage chunks,
  so `len(chunks) > 0` does *not* imply `coverage_hint > 0`. The semantic
  invariant ("EXPAND is futile") is `coverage_hint == 0`, and `len` is only an
  occasional side-effect of it.
- **Variable / corpus-relative cutpoints.** Rejected: ADR 0002's query
  normalization already removes the corpus-size axis; recomputing cutpoints
  per corpus would undo that simplification.

## Consequences

- EXPAND fires only when `0 < coverage_hint < coverage_floor` AND
  `expandCount < maxExpandIterations` — precisely the regime where widening
  has a real, non-futile chance.
- The all-OOB / typo-query / wrong-corpus case short-circuits to ESCALATE on
  the first pass, skipping the EXPAND loop entirely.
- The Router remains a pure function of `(confidence, coverage_hint,
  coverage_floor, confidence_floor)` — no `expandCount`, no corpus
  inspection, no special-case knowledge of Stage internals. Acceptance tests
  pin cutpoints to force branches; exhaustion is tested through the
  orchestrator, not the Router.
- `cliRouter` (`cmd/limen/main.go:30-49`, currently constant `PROCEED`) is
  replaced by this cascade; cutpoints arrive via config.