# Mutation testing campaign — `internal/retrieval`

CDD alignment, Iteration 2a. Tracks issue `issues/cdd-mutation-retrieval.md`.

Mutation testing is the test-quality firewall: coverage says a line ran, a
surviving mutant says nothing checked what it did. This log records every
mutant that survived the retrieval suite, what was done about it, and — for the
ones still standing — why they cannot or need not be killed.

**No production `.go` file was modified during this campaign.** All changes are
in test files.

## Tooling note (deviation from the issue)

The issue specifies `go-mutesting`. `go install
github.com/zimmski/go-mutesting/cmd/go-mutesting@latest` is blocked in the
implementer's environment — the sandbox denies commands that reach the network
or write outside the repository, so the binary could not be installed or even
probed for.

The campaign was run instead with a purpose-built AST mutation driver that
mirrors go-mutesting's operator set. It copies the package into a scratch
module, applies one mutant at a time to the copy, runs `go test` against it, and
records pass (survived) / fail (killed). The repository itself is never mutated.
Driver source: `<scratch>/mutcampaign/main.go` (session scratchpad, not
committed — it is a throwaway harness, and the numbers below are what matters).

Operators applied, per mutation site:

| Operator | Mutations |
| --- | --- |
| comparison boundary | `<`↔`<=`, `>`↔`>=` |
| comparison negation | `==`↔`!=`, `<`→`>`, `>`→`<` |
| arithmetic | `+`→`-`, `-`→`+`, `*`→`/`, `/`→`*` |
| assignment | `+=`→`-=`, `-=`→`+=`, `*=`→`/=`, `/=`→`*=` |
| logical | `&&`→`||`, `||`→`&&` |
| numeric literal | `n`→`n+1`, `n`→`n-1` |
| branch condition | negate every `if` condition |
| statement removal | drop expression / assignment / inc-dec / break / continue statements |

A mutant that fails to compile counts as killed (the suite cannot pass), and is
reported separately. Reproduce with:

```
go run . -src <repo>/internal/retrieval -work <tmp> -workers 8 -out survivors.txt
```

## Results

| Stage | Total | Killed | Survived | Score |
| --- | --- | --- | --- | --- |
| Baseline | 435 | 282 (27 by compile error) | **153** | 64.8% |
| After BM25 strengthening | 435 | — | 129 | — |
| After chunker strengthening | 435 | — | 122 | — |
| After analyzer strengthening | 435 | — | 113 | — |
| After confidence + coverage strengthening | 435 | — | 109 | — |
| After corpus / manifest / structural / pipeline | 435 | 391 (27 by compile error) | **44** | **89.9%** |

Per-file, core math first (the issue's acceptance target):

| File | Baseline survivors | Final | Non-equivalent remaining |
| --- | --- | --- | --- |
| `bm25.go` | 28 | 4 | **0** |
| `chunker.go` | 17 | 10 | **0** |
| `analyzer.go` | 9 | 0 | **0** |
| `confidence.go` | 13 | 10 | **0** |
| `coverage.go` | 2 | 1 | **0** |
| `corpus.go` | 34 | 4 | 1 |
| `manifest.go` | 3 | 0 | 0 |
| `structural.go` | 4 | 1 | 0 |
| `pipeline.go` | 42 | 14 | 1 |

**Core math (BM25, chunker, analyzer, confidence, coverage): 69 → 25 survivors,
all 25 provably equivalent. Zero weak-test survivors.**

Package-wide: 44 survivors — 42 equivalent, 2 non-equivalent but impractical to
observe (documented below).

## What the survivors revealed

Four themes accounted for nearly every weak-test survivor. They are worth
knowing because they are what "81% coverage" was hiding:

1. **Parameter normalization was never exercised.** No test constructed a
   `BM25Stage` or `StructuralStage` with an out-of-range parameter, so every
   guard and fallback constant in the normalization blocks was free to change.
   A stage that ignored its configuration entirely passed the whole suite.
2. **Relations were asserted, magnitudes were not.** Every BM25 test asserted an
   ordering (`b > a`, `rare > common`). Orderings survive most changes to the
   formula — swapping the length-normalization multiply for a divide preserves
   the ranking while changing every number. Nothing pinned an absolute score.
3. **Self-referential constants.** `TestIsBinaryClampsToBinaryCheckLen` built
   its fixtures from `binaryCheckLen` itself, so the constant and the data moved
   together and the test could not see the window change size.
4. **Whole-token masking in the analyzer.** `Analyze` emits the whole lowercased
   field *and* its split pieces, so a mis-split or dropped piece is invisible to
   any assertion made on the token set as a whole.

## Kills, by slice

### BM25 (28 → 4)

- `TestBM25Stage_ScoresMatchTheClosedFormBM25` (new, `bm25_test.go`) — asserts
  the exact score for a two-document corpus of differing lengths, derived by
  hand from the BM25 closed form rather than from the implementation. Kills the
  length-normalization arithmetic mutant that every ordering-based test missed.
- `TestBM25Stage_NormalizesOutOfRangeParameters` (new) — table over
  K1 ∈ {-1, 0, 0.5, 2} and B ∈ {-1, -0.5, 0, 0.5, 1, 1.5, 2}, asserting which
  values are normalized to the documented defaults and which are honored.
  Kills all 20 mutants in the two normalization blocks.
- `TestBM25Stage_EmptyDocumentScoresZeroNotNaN` (new) — B=1 over an empty
  document is the only input that drives the denominator to 0. Kills the three
  zero-denominator-guard mutants; without the guard the chunk scores NaN, and
  since the pipeline drops chunks scoring <= 0, a NaN silently discards context.

### Chunker (17 → 10)

- `TestLineWindowChunker_DegenerateConfigsNormalizeToExactWindows` (new,
  `chunker_test.go`) — five degenerate configurations asserted down to exact
  line spans. The property suite's shape invariants (every line covered, no
  chunk exceeds the window) hold for almost any step size, so they could not see
  the default window drift, a negative overlap being honored, or an
  `overlap >= window` collapsing the step to 1.

### Analyzer (9 → 0)

- `TestSplitIdentifier_BoundaryClassification` (new, `property_test.go`) — 16
  cases pinning each character-class transition and the alphabet edges
  (`a`, `z`, `A`, `Z`, `0`, `9`). Narrowing any classifier bound to an exclusive
  comparison reclassifies exactly one character as a separator, silently
  deleting it from every piece it appears in; the whole-token form kept that
  invisible to every existing assertion.

### Confidence + coverage (15 → 11)

- `TestConfidence_ZeroWithoutEvidence` (strengthened) — added the
  corpus-cannot-answer case: query terms occurring in no candidate leave the
  summed IDF at 0, and that 0 is a denominator. Without the guard the ratio is
  `+Inf`, which saturates to a fully confident 1.0 on evidence that does not
  exist.
- `TestHasNonTrivialForm_EdgeCases` (strengthened) — added `"99"` and `"90"`.
  The existing pure-digit cases (`"42"`, `"007"`) contain no `9`, so narrowing
  the digit range's upper bound went unnoticed.

### Corpus, manifest, structural, pipeline (83 → 19)

- `TestGitCorpusLoader_*` and `TestListTrackedFiles_*` (new,
  `corpus_internal_test.go`) — the loader's own logic was entirely untested.
  Drives a real git repository covering every branch of the per-file filter:
  committed text, nested paths, a one-byte file, a binary file, an empty file, a
  file staged but never committed (so `git show HEAD:` fails for it), and an
  untracked file. Plus repo-path resolution and context cancellation. Kills 30.
- `TestIsBinaryClampsToBinaryCheckLen` (strengthened) — added absolute byte
  offsets (NUL at 511 is inside the window, at 512 is outside) and index-0
  cases. Breaks the self-reference that let the constant move freely.
- `TestRenderContextSection_FencedChunks` (strengthened) +
  `TestRenderContextSection_SeparatesMultipleChunks` (new) — byte-for-byte
  golden output. The substring assertions could not see a dropped newline, and
  this text is fed to the worker verbatim.
- `TestStructuralStage_NormalizesBoost` (new) — boost fallback and honored
  values, asserted on the exact score.
- `TestPipeline_UsesInjectedSeams` (new) — fake chunker / analyzer / stage prove
  each `With*` option is actually wired in. Each option body is a single
  assignment; an option that silently did nothing left the pipeline on its
  production default, which every existing test also used.
- `TestPipeline_DefaultChunkerMatchesADR0004Windows`,
  `TestPipeline_CapsAtTenChunksInTieBreakOrder`,
  `TestPipeline_TiedChunksInOneFileOrderByLineStart`,
  `TestPipeline_GateKeepsTheHighestScoringCandidates`,
  `TestPipeline_ZeroCandidateFloorDisablesTheGate`,
  `TestPipeline_CandidateFloorGatesTheRankingPool`,
  `TestPipeline_DefaultGateConstants` (all new) — the top-K cap, both tie-break
  keys, the gate's internal sort, the disabled-gate edge, and the default
  `candidateFloor`/`expandAlpha` constants pinned by their effect.

## Surviving mutants

### Non-equivalent, impractical to observe (2)

| Location | Mutation | Why it stands |
| --- | --- | --- |
| `corpus.go:45` | remove `paths = paths[:maxCorpusFiles]` | Observable only with more than 10,000 tracked files in a test repository. The setup cost (10k files through `git add`) is out of proportion to a truncation whose only failure mode is loading a larger corpus than intended. |
| `pipeline.go:125` | `StructuralStage{Boost: 1.0}` → `Boost: 2` | The default boost's *magnitude* changes the ranking only where a structural boost and a BM25 gap straddle each other. Killing it needs a corpus tuned so that boost 1 and boost 2 land on opposite sides of a rank boundary — a fixture whose calibration would itself be fragile. The boost's presence, its normalization, and its tie-breaking effect are all covered. |

### Equivalent (42)

These change the code without changing any observable behaviour. Grouped by
cause:

**Unreachable defensive branches** — the guard cannot fire given its callers, so
nothing distinguishes the mutant.

- `chunker.go:40,41` (6 mutants) — `if step <= 0 { step = 1 }`. Overlap is
  normalized below the window immediately above, so `step >= 1` always.
- `chunker.go:45` — `start < n` → `start <= n`. The loop breaks when `end == n`,
  which happens while `start < n` because `window >= step`; `start` never
  reaches `n`.
- `confidence.go:55,56` (4) — `if ratio < 0 { return 0 }`. Line 14 guarantees
  `topScore > 0` and line 48 guarantees `sumIDF > 0`, so `ratio > 0` always.
- `corpus.go:79` — `remove continue` in the empty-line skip. `git ls-files`
  piped through `bufio.Scanner` never yields an empty line.

**No-op self-assignment** — the mutated guard admits a value that is then
assigned to itself.

- `chunker.go:33` (2) — `overlap < 0` widened to include `overlap == 0`, which
  assigns 0 over 0.
- `chunker.go:47` — `end > n` widened to `end >= n`, which assigns `n` over `n`.
- `corpus.go:44` — `len(paths) > maxCorpusFiles` widened to `>=`, which
  re-slices to the same length.
- `pipeline.go:165` — `int(k) < len(candidates)` widened to `<=`, which gates to
  the pool's existing size.
- `pipeline.go:218` — `len(rankedChunks) > topK` widened to `>=`, which
  truncates to the existing length.

**Equal case already excluded** — a comparator boundary that only matters for
equal operands, inside a branch that has already established inequality.

- `pipeline.go:209` (`>`→`>=`), `pipeline.go:212` (`<`→`<=`),
  `pipeline.go:214` (`<`→`<=`) — each sits under an enclosing `!=` check.
- `pipeline.go:172` — the gate's comparator; distinct scores order identically
  and tied scores are left to an unstable sort either way.
- `confidence.go:52` — `ratio > 1` → `>=`; at exactly 1 both return 1.

**Result is zero on every path** — the mutated branch changes control flow but
not the value produced.

- `bm25.go:83,84` (2) — `if n == 0 { continue }`. A term in no document has
  `f == 0`, so its contribution is `idf * 0 / denom == 0` whether or not the
  guard fires.
- `bm25.go:57` (2) — `if len(docs) > 0 { avgdl = ... }`. With no candidates the
  scoring loop never runs, so a NaN `avgdl` is never read.
- `confidence.go:14` (4), `confidence.go:21` — every mutated form of the early
  guard falls through to a path that returns 0 anyway (empty candidates give
  `sumIDF == 0`; `topScore <= 0` gives `ratio <= 0`).
- `structural.go:33` — with an empty whole-token set the scoring loop produces
  all-zero scores, identical to the early return it replaces.
- `coverage.go:50` — `remove break` from the all-digit scan; `allDigit` is
  already false, so the break is a short-circuit, not a decision.
- `corpus.go:57` — `remove continue` after a failed `git show`; execution falls
  into the `len(content) == 0` guard, which continues anyway.
- `pipeline.go:167` — negating the `stage.(BM25Stage)` assertion makes the gate
  rank with a zero-valued `BM25Stage`, which normalizes to exactly the same
  K1/B defaults, producing the same gate.
- `pipeline.go:178` — `remove break` after gating; later stages fail the type
  assertion, so the loop does nothing more.
- `pipeline.go:125` (3) — `B: 0.75` → `1.75` / `-0.25` and `Boost: 1.0` → `0`
  are all out of range and are normalized straight back by the stages' own
  guards.
- `pipeline.go:158` — `remove wholeSeen[w] = true`; duplicate whole tokens
  collapse into a set in `StructuralStage`.
- `pipeline.go:148` — `len(queryTerms) == 0` → `== -1`; the fall-through builds
  an empty (non-nil) chunk slice instead of a nil one, with the same length,
  confidence and coverage.

## Acceptance

- Core math (BM25, chunker, analyzer, confidence, coverage): **zero
  non-equivalent survivors**.
- Package-wide: 391/435 killed (89.9%); 44 survivors = 42 equivalent + 2
  documented as impractical.
- `go test -race -count=1 ./internal/retrieval/` passes.
- `go vet ./internal/retrieval/` clean.
- No production `.go` file modified.
