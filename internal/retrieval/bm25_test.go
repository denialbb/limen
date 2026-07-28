package retrieval_test

import (
	"math"
	"testing"

	"github.com/denialbb/limen/internal/retrieval"
)

// lengthSplitCorpus is a two-document corpus whose documents differ in length,
// so the BM25 length-normalization term (b*dl/avgdl) is not 1 and therefore
// cannot be confused with its own absence. Analyze emits the whole token plus
// its split pieces, so "alpha" contributes two tokens and "alpha beta" four:
// dl(a)=2, dl(b)=4, avgdl=3.
func lengthSplitCorpus() []retrieval.Chunk {
	return []retrieval.Chunk{
		{Path: "a", Text: "alpha"},
		{Path: "b", Text: "alpha beta"},
	}
}

// rankScores returns the scores of a Rank call keyed by chunk path.
func rankScores(t *testing.T, stage retrieval.BM25Stage, candidates []retrieval.Chunk) map[string]float64 {
	t.Helper()
	scored, err := stage.Rank(retrieval.StageQuery{Terms: []string{"alpha"}, Whole: []string{"alpha"}}, candidates)
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	out := make(map[string]float64, len(scored))
	for _, sc := range scored {
		out[sc.Path] = sc.Score
	}
	return out
}

// sameScores reports whether two score maps agree within floating-point noise.
func sameScores(x, y map[string]float64) bool {
	if len(x) != len(y) {
		return false
	}
	for k, xv := range x {
		yv, ok := y[k]
		if !ok || math.Abs(xv-yv) > 1e-12 {
			return false
		}
	}
	return true
}

func TestBM25Stage_TermFrequencyBoostsMoreFrequent(t *testing.T) {
	stage := retrieval.BM25Stage{K1: 1.2, B: 0.75}
	query := retrieval.StageQuery{Terms: []string{"fox"}, Whole: []string{"fox"}}
	candidates := []retrieval.Chunk{
		{Path: "a", Text: "the fox\n"},
		{Path: "b", Text: "the fox fox fox\n"},
	}
	scored, err := stage.Rank(query, candidates)
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	scoreOf := func(p string) float64 {
		for _, sc := range scored {
			if sc.Path == p {
				return sc.Score
			}
		}
		return -1
	}
	if scoreOf("b") <= scoreOf("a") {
		t.Errorf("expected b > a (tf boosts), got a=%v b=%v", scoreOf("a"), scoreOf("b"))
	}
}

func TestBM25Stage_RareTermScoresHigherPerOccurrence(t *testing.T) {
	stage := retrieval.BM25Stage{K1: 1.2, B: 0.75}
	// "rare" appears in 1 of 3 docs; "common" in 3 of 3 (IDF distinction).
	candidates := []retrieval.Chunk{
		{Path: "a", Text: "common rare\n"},
		{Path: "b", Text: "common\n"},
		{Path: "c", Text: "common\n"},
	}
	scored, err := stage.Rank(retrieval.StageQuery{Terms: []string{"rare"}, Whole: []string{"rare"}}, candidates)
	if err != nil {
		t.Fatalf("Rank rare: %v", err)
	}
	rareScore := scored[0].Score

	scoredCommon, err := stage.Rank(retrieval.StageQuery{Terms: []string{"common"}, Whole: []string{"common"}}, candidates)
	if err != nil {
		t.Fatalf("Rank common: %v", err)
	}
	commonScore := scoredCommon[0].Score

	// A single occurrence of the rare term must out-score a single occurrence
	// of the common term in an equally-sized doc (b is single-occurrence for
	// common; a is single-occurrence for rare).
	if rareScore <= commonScore {
		t.Errorf("expected rare IDF boost: rare=%v common=%v", rareScore, commonScore)
	}
}

// TestBM25Stage_ScoresMatchTheClosedFormBM25 pins the absolute score, not just
// the ordering. Every other BM25 test here asserts a relation (b > a, rare >
// common), and relations survive most changes to the formula: scaling the
// saturation term, swapping the length-normalization multiply for a divide, or
// dropping avgdl entirely all preserve the ranking while changing the number.
//
// The expectation is derived by hand for this fixture rather than recomputed
// from the implementation's expression, so it is an independent check:
//
//	N = 2, df(alpha) = 2  =>  idf = ln(1 + (2-2+0.5)/(2+0.5)) = ln(1.2)
//	both docs: f = 2 (whole token + split piece), k1 = 1.2, b = 0.75
//	doc a: dl = 2, avgdl = 3  =>  denom = 2 + 1.2*(1-0.75+0.75*2/3) = 2.9
//	doc b: dl = 4, avgdl = 3  =>  denom = 2 + 1.2*(1-0.75+0.75*4/3) = 3.5
//	score = idf * (f*(k1+1)) / denom = ln(1.2) * 4.4 / denom
func TestBM25Stage_ScoresMatchTheClosedFormBM25(t *testing.T) {
	stage := retrieval.BM25Stage{K1: 1.2, B: 0.75}
	got := rankScores(t, stage, lengthSplitCorpus())

	want := map[string]float64{
		"a": math.Log(1.2) * 4.4 / 2.9,
		"b": math.Log(1.2) * 4.4 / 3.5,
	}
	for path, w := range want {
		if math.Abs(got[path]-w) > 1e-12 {
			t.Errorf("score(%s) = %.15f, want %.15f", path, got[path], w)
		}
	}
	// The shorter document must win: length normalization is the whole point
	// of the denominator, and a mutation that neutralizes it makes these equal.
	if got["a"] <= got["b"] {
		t.Errorf("expected the shorter document to score higher: a=%v b=%v", got["a"], got["b"])
	}
}

// TestBM25Stage_NormalizesOutOfRangeParameters pins the documented defaults:
// K1 <= 0 falls back to 1.2 and B outside [0,1] falls back to 0.75, while
// in-range values are honored. Without this, every guard and constant in the
// normalization block is free to change: nothing else in the suite ever
// constructs a BM25Stage with an out-of-range parameter, so a stage that
// ignored its configuration entirely would still pass.
func TestBM25Stage_NormalizesOutOfRangeParameters(t *testing.T) {
	corpus := lengthSplitCorpus()

	t.Run("K1", func(t *testing.T) {
		base := rankScores(t, retrieval.BM25Stage{K1: 1.2, B: 0.75}, corpus)
		for _, k1 := range []float64{-1, 0} {
			if got := rankScores(t, retrieval.BM25Stage{K1: k1, B: 0.75}, corpus); !sameScores(got, base) {
				t.Errorf("K1=%v was not normalized to the 1.2 default: got %v, want %v", k1, got, base)
			}
		}
		for _, k1 := range []float64{0.5, 2.0} {
			if got := rankScores(t, retrieval.BM25Stage{K1: k1, B: 0.75}, corpus); sameScores(got, base) {
				t.Errorf("in-range K1=%v was overridden by the default: scores match K1=1.2 (%v)", k1, base)
			}
		}
	})

	t.Run("B", func(t *testing.T) {
		base := rankScores(t, retrieval.BM25Stage{K1: 1.2, B: 0.75}, corpus)
		for _, b := range []float64{-1, -0.5, 1.5, 2} {
			if got := rankScores(t, retrieval.BM25Stage{K1: 1.2, B: b}, corpus); !sameScores(got, base) {
				t.Errorf("B=%v was not normalized to the 0.75 default: got %v, want %v", b, got, base)
			}
		}
		// 0 and 1 are the inclusive ends of the valid range: 0 disables length
		// normalization, 1 applies it fully. Both must survive the guard.
		for _, b := range []float64{0, 0.5, 1} {
			if got := rankScores(t, retrieval.BM25Stage{K1: 1.2, B: b}, corpus); sameScores(got, base) {
				t.Errorf("in-range B=%v was overridden by the default: scores match B=0.75 (%v)", b, base)
			}
		}
	})
}

// TestBM25Stage_EmptyDocumentScoresZeroNotNaN exercises the one input that
// drives the BM25 denominator to zero: full length normalization (B=1) over a
// document with no terms, where f = 0 and dl = 0 collapse f + k1*(1-b+b*dl/avgdl)
// to 0. The zero-denominator guard is what keeps that 0/0 out of the score; if
// it stops firing, the chunk scores NaN, and because the pipeline drops chunks
// scoring <= 0, a NaN silently discards context instead of ranking it last.
func TestBM25Stage_EmptyDocumentScoresZeroNotNaN(t *testing.T) {
	stage := retrieval.BM25Stage{K1: 1.2, B: 1}
	candidates := []retrieval.Chunk{
		{Path: "empty", Text: ""},
		{Path: "full", Text: "alpha"},
	}
	got := rankScores(t, stage, candidates)

	if math.IsNaN(got["empty"]) || math.IsInf(got["empty"], 0) {
		t.Fatalf("empty document scored %v; the zero-denominator guard did not fire", got["empty"])
	}
	if got["empty"] != 0 {
		t.Errorf("empty document scored %v, want exactly 0", got["empty"])
	}
	if !(got["full"] > 0) {
		t.Errorf("matching document scored %v, want > 0", got["full"])
	}
}
