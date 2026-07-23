# coverage_hint is whole-token query-term recall with a survival exclusion

## Status

accepted

## Context

`coverage_hint` drives the **EXPAND** branch of the coverage-gates-first
Router (`internal/orchestrator/orchestrator.go:~250`, `DecisionExpand`):
low coverage → re-run retrieval wider, up to `maxExpandIterations = 5`,
burning the budget for nothing if it can never be satisfied.

The analyzer (locked earlier this session) is **split-and-preserve**:
`getUserName` emits `{getusername, get, user, name}` — the whole token
*and* its subtokens. This is the load-bearing fact for coverage: it makes a
naive "fraction of query terms matched by any chunk" unsafe.

The contract text
(`.agents/docs/retrieval_contract.md:34`) still defines `coverage_hint` as
"percentage of the call graph successfully traversed" — a stale echo of the
deferred graph layer. This ADR replaces that definition; the contract doc
must be cleaned up in the aftermath (out of scope for this decision).

## Decision

```
coverage_hint = |covered Q-terms||  /  |surviving Q-terms|
```

- **Covered** = the query term's *whole-token* form appears in the analyzed
  term-set of some retrieved chunk. **Subtoken matches do not count.**
- **Surviving** = the query term passes stopword removal **and** has a
  non-trivial whole-token form (i.e. is not a single letter, pure-digit
  fragment, or other degenerate residue of splitting).
- Terms with no resolvable whole-token form are **excluded from both
  numerator and denominator**, not counted as uncovered.

## Considered Options

- **Naive recall** (`|t ∈ chunk| / |Q|`, subtokens allowed). Rejected: a query
  for `getUserName` reports near-1.0 the moment any chunk contains a `get` or
  `user` — both pervade a Go codebase. Coverage trivially saturates and EXPAND
  never fires; the whole gate is inert.
- **Whole-token recall without the survival exclusion** (the Q5 draft). Rejected
  as incomplete: a degenerate query term (single letter, pure-digit fragment)
  is *permanently uncovered*, dragging `coverage_hint` down on perfect
  retrievals and burning the `maxExpandIterations` budget indefinitely on a
  term that could never be found. The survival exclusion is what keeps the
  metric honest as "did the retrieval find the things that could be found."

## Consequences

- A camelCase / snake_case identifier is covered only on its whole-token (or
  lowercased-whole-token) form — the semantic the retriever actually wants.
- `coverage_hint` and `confidence`'s `Σ IDF` denominator (ADR 0002) now share
  the same survival rule: degenerate query terms are excluded from both. One
  definition of "what is a queryable term", two projections. Coherent.
- Stale contract text at `.agents/docs/retrieval_contract.md:34` ("call graph
  traversed") must be rewritten to this definition — tracked as cleanup, not
  an open question.