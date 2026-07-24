package retrieval_test

import (
	"testing"

	"github.com/denialbb/limen/internal/retrieval"
)

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