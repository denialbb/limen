# Confidence is query-normalized best-match magnitude, not score-distribution entropy

## Status

accepted

## Context

ADR 0001 specified `confidence` as the **BM25 top-k score-distribution
entropy** (peaked = one clear hit = confident; flat = ambiguous → expand). On
re-grilling Q4 that choice is **reversed**. This ADR records the corrected
definition and the three legs that load-bear it.

The Router is **coverage-gates-first**: low `coverage_hint` → **EXPAND**
(re-run retrieval wider, up to `maxExpandIterations = 5`); `coverage_hint` OK
but `confidence` low → **ESCALATE**; both OK → **PROCEED**
(`internal/orchestrator/orchestrator.go:~250`, `DecisionExpand`). The Retriever
delivers a **bounded set** of chunks, not one answer — the manifest is persisted
verbatim on the Task and the contract (`.agents/docs/retrieval_contract.md`)
forbids reducing it to a single hit.

## Decision

Define `confidence` as a **saturating map** of the best chunk's stage score
against the query's discriminative mass:

```
confidence = saturate( topChunkBM25 / Σ IDF(query terms) )
```

with an identity `min(1, ratio)` squash by default; the exact shape is a tuning
knob absorbed by the cutpoints decided in the router-thresholds decision.

**Not** the entropy of the top-k score distribution.

## Considered Options

- **Score-distribution entropy** (the ADR-0001 choice). Rejected on three legs:

  1. *Corpus shape (decisive leg).* Code is legitimately repetitive — "callers of
     `Retrieve`" returns many near-equal-scoring chunks. Entropy reads that flat
     distribution as "ambiguous → ESCALATE" on a *correct* retrieval. It
     conflates "genuinely ambiguous" with "correctly retrieved many similar
     sites," and the latter is the common case in code IR. Magnitude — "did the
     best chunk capture the query's discriminative mass?" — is robust to
     repetition.
  2. *Coherence with the Stage interface.* `topChunkBM25 / Σ IDF` projects the
     same axis the ranking is built on. Entropy-of-scores introduces a second,
     parallel statistic the `Stage` interface never promised, and which would
     have to be re-derived per Stage as Stages are added (structural boost, future
     reranker). One model, reused, extends cleanly.
  3. *Set-delivery tiebreaker (defeats the steelman).* The steelman for entropy:
     "the top chunk scores high but chunks #2–#k tie it → magnitude says
     *confident* while the ranking is unstable; entropy would catch that." That
     worry only matters for a *single-answer* retriever. Limen delivers a
     **bounded set** to the worker and the contract forbids collapsing it; under
     set-delivery, ties don't hurt — the worker sees all tied chunks. Entropy's
     worry is the single-answer problem; magnitude's worry is the set problem.
     The set-delivery model — already locked — selects magnitude.

- **Top-k coverage-gates-first framing alone** as justification. Insufficient:
  it only removes a *symptom* of entropy misbehaving, not the root cause. The
  corpus-shape leg is the real load-bearer.

## Consequences

- `confidence` is a projection of the ranking score, not a parallel statistic.
  Adding Stages extends the numerator naturally; no per-Stage re-derivation of a
  separate statistic.
- The denominator `Σ IDF(query terms)` is unbounded above for out-of-corpus
  terms: an unseen query term inflates it, depressing `confidence`. This is
  harmless under coverage-gates-first (that term also tanks `coverage_hint` →
  EXPAND fires first) and is resolved at Stage-impl/tuning time, not at the seam
  — recorded here as a known caveat, not an open question.
- The exact `saturate` shape and the confidence/coverage cutpoints are decided
  in the router-thresholds decision (Q7), not here.

## Supersedes

ADR 0001 lines 28-31 (the `confidence` = entropy bullet). ADR 0001's
*arc-spanning* decision (BM25 + structural stage, neural deferred) stands.