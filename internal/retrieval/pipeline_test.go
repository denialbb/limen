package retrieval_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/denialbb/limen/internal/retrieval"
)

// uniformCorpus returns n single-line files whose contents are identical, so
// every chunk scores identically and the ranking is decided purely by the
// tie-break. The files are appended in descending path order so corpus order
// and sorted order disagree — otherwise "sorted" and "unsorted" would look the
// same.
func uniformCorpus(n int) retrieval.StaticCorpus {
	c := make(retrieval.StaticCorpus, 0, n)
	for i := n - 1; i >= 0; i-- {
		c = append(c, retrieval.File{Path: fmt.Sprintf("f%02d.txt", i), Content: []byte("alpha\n")})
	}
	return c
}

// retrieveOrFail runs one retrieval and fails the test on error.
func retrieveOrFail(t *testing.T, p *retrieval.Pipeline, text string, iteration int) retrieval.Manifest {
	t.Helper()
	m, err := p.Retrieve(context.Background(),
		retrieval.Query{Text: text, TaskID: "t"},
		retrieval.ExpandState{Iteration: iteration})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	return m
}

// chunkPaths lists the manifest's chunk paths in manifest order.
func chunkPaths(m retrieval.Manifest) []string {
	out := make([]string, len(m.Chunks))
	for i, c := range m.Chunks {
		out[i] = c.Path
	}
	return out
}

func TestPipeline_Retrieve_ReturnsMatchingChunksRanked(t *testing.T) {
	corpus := retrieval.StaticCorpus{
		{Path: "match.txt", Content: []byte("the quick brown fox jumps\n")},
		{Path: "nomatch.txt", Content: []byte("nothing here at all\n")},
	}
	p := retrieval.NewPipeline(retrieval.WithCorpusLoader(corpus))

	m, err := p.Retrieve(context.Background(), retrieval.Query{Text: "quick fox", TaskID: "t1"}, retrieval.ExpandState{Iteration: 0})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(m.Chunks) == 0 {
		t.Fatal("expected chunks, got none")
	}
	if m.Chunks[0].Path != "match.txt" {
		t.Errorf("expected first chunk from match.txt, got %s", m.Chunks[0].Path)
	}
	if m.QueryID != "task-t1:#0" {
		t.Errorf("expected QueryID task-t1:#0, got %s", m.QueryID)
	}
}

// fakeChunker is a Chunker seam double: it emits one chunk per file with a
// signature line range no real chunker would produce.
type fakeChunker struct{}

func (fakeChunker) Chunk(path string, content []byte) []retrieval.Chunk {
	return []retrieval.Chunk{{Path: path, LineStart: 700, LineEnd: 701, Text: string(content)}}
}

// fakeAnalyzer is an Analyzer seam double: it maps every input to one fixed
// term, so retrieval succeeds only if the pipeline actually routed through it.
type fakeAnalyzer struct{}

func (fakeAnalyzer) Analyze(text string) []string { return []string{"sentinel"} }

// fixedStage is a Stage seam double: it scores one nominated path above all
// others regardless of the query.
type fixedStage struct{ winner string }

func (s fixedStage) Rank(q retrieval.StageQuery, candidates []retrieval.Chunk) ([]retrieval.ScoredChunk, error) {
	out := make([]retrieval.ScoredChunk, len(candidates))
	for i, c := range candidates {
		score := 1.0
		if c.Path == s.winner {
			score = 99.0
		}
		out[i] = retrieval.ScoredChunk{Chunk: c, Score: score}
	}
	return out, nil
}

// TestPipeline_UsesInjectedSeams asserts each With* option is actually wired
// into Retrieve. Each option's body is a single assignment, so an option that
// silently did nothing would leave the pipeline on its production default and
// every existing test — which only ever uses defaults plus a corpus — would
// still pass.
func TestPipeline_UsesInjectedSeams(t *testing.T) {
	corpus := retrieval.StaticCorpus{
		{Path: "a.txt", Content: []byte("alpha\n")},
		{Path: "b.txt", Content: []byte("alpha\n")},
	}

	t.Run("chunker", func(t *testing.T) {
		p := retrieval.NewPipeline(retrieval.WithCorpusLoader(corpus), retrieval.WithChunker(fakeChunker{}))
		m := retrieveOrFail(t, p, "alpha", 0)
		if len(m.Chunks) == 0 {
			t.Fatal("no chunks")
		}
		for _, c := range m.Chunks {
			if c.LineStart != 700 || c.LineEnd != 701 {
				t.Errorf("chunk %s spans %d-%d; the injected chunker was ignored", c.Path, c.LineStart, c.LineEnd)
			}
		}
	})

	t.Run("analyzer", func(t *testing.T) {
		// The injected analyzer maps everything to "sentinel", so the chunk
		// text must match that term rather than the query text.
		sentinelCorpus := retrieval.StaticCorpus{{Path: "s.txt", Content: []byte("sentinel\n")}}
		p := retrieval.NewPipeline(retrieval.WithCorpusLoader(sentinelCorpus), retrieval.WithAnalyzer(fakeAnalyzer{}))
		if m := retrieveOrFail(t, p, "nothing-in-the-corpus", 0); len(m.Chunks) == 0 {
			t.Error("query terms did not come from the injected analyzer: no chunk matched")
		}
	})

	t.Run("stages", func(t *testing.T) {
		p := retrieval.NewPipeline(retrieval.WithCorpusLoader(corpus), retrieval.WithStages(fixedStage{winner: "b.txt"}))
		m := retrieveOrFail(t, p, "alpha", 0)
		if len(m.Chunks) == 0 || m.Chunks[0].Path != "b.txt" {
			t.Errorf("injected stage did not decide the ranking: got %v", chunkPaths(m))
		}
	})
}

// TestPipeline_DefaultChunkerMatchesADR0004Windows pins the chunker the
// pipeline builds when none is injected: a 50-line window with 10 lines of
// overlap, so a 60-line file splits at exactly 1-50 and 41-60. Nothing else
// asserts the default window, and every shape invariant in the property suite
// holds for any window size.
func TestPipeline_DefaultChunkerMatchesADR0004Windows(t *testing.T) {
	lines := make([]string, 60)
	for i := range lines {
		lines[i] = "alpha"
	}
	corpus := retrieval.StaticCorpus{{Path: "big.txt", Content: []byte(strings.Join(lines, "\n") + "\n")}}

	m := retrieveOrFail(t, retrieval.NewPipeline(retrieval.WithCorpusLoader(corpus)), "alpha", 0)

	got := map[int]int{}
	for _, c := range m.Chunks {
		got[c.LineStart] = c.LineEnd
	}
	want := map[int]int{1: 50, 41: 60}
	if len(got) != len(want) {
		t.Fatalf("got %d chunks (%v), want %d", len(got), got, len(want))
	}
	for start, end := range want {
		if got[start] != end {
			t.Errorf("expected a chunk spanning %d-%d, got %v", start, end, got)
		}
	}
}

// TestPipeline_CapsAtTenChunksInTieBreakOrder pins the manifest's top-K cap and
// its tie-break. Every chunk here scores identically, so the order is decided
// entirely by the (path, line) tie-break — which means this test fails if the
// sort is dropped, if either tie-break key is inverted, or if the cap moves.
func TestPipeline_CapsAtTenChunksInTieBreakOrder(t *testing.T) {
	m := retrieveOrFail(t, retrieval.NewPipeline(retrieval.WithCorpusLoader(uniformCorpus(12))), "alpha", 0)

	if len(m.Chunks) != 10 {
		t.Fatalf("got %d chunks, want the top-10 cap: %v", len(m.Chunks), chunkPaths(m))
	}
	for i, path := range chunkPaths(m) {
		if want := fmt.Sprintf("f%02d.txt", i); path != want {
			t.Errorf("chunk %d = %s, want %s (ties break by path ascending): %v", i, path, want, chunkPaths(m))
		}
	}
}

// TestPipeline_TiedChunksInOneFileOrderByLineStart covers the second tie-break
// key: two windows of one file with identical content score identically, so
// only the line-start comparison can order them.
func TestPipeline_TiedChunksInOneFileOrderByLineStart(t *testing.T) {
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = "alpha"
	}
	corpus := retrieval.StaticCorpus{{Path: "one.txt", Content: []byte(strings.Join(lines, "\n") + "\n")}}

	m := retrieveOrFail(t, retrieval.NewPipeline(retrieval.WithCorpusLoader(corpus)), "alpha", 0)

	if len(m.Chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(m.Chunks))
	}
	// The two 50-line windows are byte-identical and therefore tied; the
	// trailing short window is a different length and scores differently.
	if m.Chunks[0].LineStart != 1 || m.Chunks[1].LineStart != 41 {
		t.Errorf("tied windows out of order: got starts %d then %d, want 1 then 41",
			m.Chunks[0].LineStart, m.Chunks[1].LineStart)
	}
}

// mixedQuery is answered by mixedCorpus below. Four terms of comparable rarity
// keep confidence off its ceiling: the top chunk carries only one of them, so
// the ratio of its score to the query's total IDF mass stays near 0.5 instead
// of saturating at 1 — where every difference would be clamped away.
const mixedQuery = "alpha beta gamma delta"

// mixedCorpus returns n files cycling through the mixedQuery terms, each with a
// distinct repetition count so that no two chunks score the same. Distinct
// scores make the gate's cutoff deterministic: which candidates survive is a
// function of the pool size alone, with no tie-breaking left to an unstable
// sort.
func mixedCorpus(n int) retrieval.StaticCorpus {
	terms := strings.Fields(mixedQuery)
	c := make(retrieval.StaticCorpus, 0, n)
	for i := range n {
		c = append(c, retrieval.File{
			Path:    fmt.Sprintf("f%03d.txt", i),
			Content: []byte(strings.Repeat(terms[i%len(terms)]+" ", i+1) + "\n"),
		})
	}
	return c
}

// TestPipeline_DefaultGateConstants pins defaultCandidateFloor and
// defaultExpandAlpha by their effect rather than by reading them back: the
// default pipeline must gate exactly like an explicitly configured one, and
// differently from its neighbours.
//
// Confidence is the observable here because the gate's size is otherwise
// invisible — it decides which candidates reach the ranking, but the manifest
// is capped at ten chunks either way, and those ten are the same chunks
// whatever the pool size. Confidence, by contrast, is computed over the whole
// gated pool, so its denominator moves with the cutoff.
func TestPipeline_DefaultGateConstants(t *testing.T) {
	corpus := mixedCorpus(260)
	withFloor := func(n int) *retrieval.Pipeline {
		return retrieval.NewPipeline(retrieval.WithCorpusLoader(corpus), retrieval.WithCandidateFloor(n))
	}
	defaults := retrieval.NewPipeline(retrieval.WithCorpusLoader(corpus))

	t.Run("candidate floor defaults to 100", func(t *testing.T) {
		base := retrieveOrFail(t, defaults, mixedQuery, 0).Confidence
		if got := retrieveOrFail(t, withFloor(100), mixedQuery, 0).Confidence; got != base {
			t.Errorf("default pipeline gated differently from an explicit floor of 100: %v vs %v", base, got)
		}
		for _, floor := range []int{99, 101} {
			if got := retrieveOrFail(t, withFloor(floor), mixedQuery, 0).Confidence; got == base {
				t.Errorf("floor %d produced the same confidence as the default (%v); the default floor is not pinned", floor, base)
			}
		}
	})

	t.Run("expand alpha defaults to 0.5", func(t *testing.T) {
		// Iteration 1 widens the pool to floor*(1+alpha) = 100*1.5 = 150.
		iter1 := retrieveOrFail(t, defaults, mixedQuery, 1).Confidence
		if got := retrieveOrFail(t, withFloor(150), mixedQuery, 0).Confidence; got != iter1 {
			t.Errorf("iteration 1 gated to something other than 150 candidates: %v vs an explicit 150 (%v)", iter1, got)
		}
		for _, floor := range []int{149, 151, 250} {
			if got := retrieveOrFail(t, withFloor(floor), mixedQuery, 0).Confidence; got == iter1 {
				t.Errorf("floor %d produced the same confidence as iteration 1 (%v); the widening factor is not pinned", floor, iter1)
			}
		}
	})
}

// TestPipeline_GateKeepsTheHighestScoringCandidates asserts the pre-rank gate
// keeps the *best* candidates, not merely the right number of them. The gate
// sorts by BM25 before slicing; without that sort it would keep whatever
// happened to come first out of the corpus, which is why the winner here is
// loaded last.
func TestPipeline_GateKeepsTheHighestScoringCandidates(t *testing.T) {
	corpus := retrieval.StaticCorpus{}
	for i := range 10 {
		corpus = append(corpus, retrieval.File{
			Path:    fmt.Sprintf("other%02d.txt", i),
			Content: []byte("alpha zzz zzz zzz zzz zzz zzz\n"),
		})
	}
	corpus = append(corpus, retrieval.File{Path: "best.txt", Content: []byte("alpha alpha alpha alpha alpha\n")})

	p := retrieval.NewPipeline(retrieval.WithCorpusLoader(corpus), retrieval.WithCandidateFloor(1))
	m := retrieveOrFail(t, p, "alpha", 0)

	if len(m.Chunks) != 1 {
		t.Fatalf("got %d chunks, want 1: %v", len(m.Chunks), chunkPaths(m))
	}
	if m.Chunks[0].Path != "best.txt" {
		t.Errorf("gate kept %s; want best.txt, the highest-scoring candidate", m.Chunks[0].Path)
	}
}

// TestPipeline_ZeroCandidateFloorDisablesTheGate pins the disabled-gate edge:
// a floor of zero means "do not pre-rank", not "keep zero candidates". Getting
// this backwards would empty every manifest.
func TestPipeline_ZeroCandidateFloorDisablesTheGate(t *testing.T) {
	p := retrieval.NewPipeline(
		retrieval.WithCorpusLoader(uniformCorpus(12)),
		retrieval.WithCandidateFloor(0),
	)
	m := retrieveOrFail(t, p, "alpha", 0)

	if len(m.Chunks) != 10 {
		t.Errorf("got %d chunks, want the full top-10: a zero floor must disable the gate, not close it", len(m.Chunks))
	}
}

// TestPipeline_CandidateFloorGatesTheRankingPool pins the ADR 0006 gate: the
// BM25 pre-rank keeps exactly the configured number of candidates, and every
// chunk that survives is a real chunk. A gate that copied the wrong slice
// bounds would surface zero-valued chunks with empty paths instead.
func TestPipeline_CandidateFloorGatesTheRankingPool(t *testing.T) {
	for _, floor := range []int{1, 2, 5} {
		t.Run(fmt.Sprintf("floor=%d", floor), func(t *testing.T) {
			p := retrieval.NewPipeline(
				retrieval.WithCorpusLoader(uniformCorpus(12)),
				retrieval.WithCandidateFloor(floor),
			)
			m := retrieveOrFail(t, p, "alpha", 0)

			if len(m.Chunks) != floor {
				t.Errorf("got %d chunks, want %d (the gate should cap the pool): %v", len(m.Chunks), floor, chunkPaths(m))
			}
			for i, c := range m.Chunks {
				if c.Path == "" || c.Text == "" {
					t.Errorf("chunk %d is zero-valued (%+v): the gate copied the wrong elements", i, c)
				}
			}
		})
	}
}
