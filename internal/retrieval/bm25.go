package retrieval

import "math"

// ScoredChunk is a chunk plus its stage score. Internal to the pipeline — the
// manifest drops scores (ADR 0004). Exported only for Stage-impl tests.
type ScoredChunk struct {
	Chunk
	Score float64
}

// Stage is one composable ranking step in the retrieval pipeline (ADR 0001).
// Production adapters: BM25Stage, structural boost stage. Test adapters: fakes
// returning known scores. Two adapters, real seam.
type Stage interface {
	Rank(q StageQuery, candidates []Chunk) ([]ScoredChunk, error)
}

// BM25Stage ranks candidates by Okapi BM25 over the candidate set treated as
// the document corpus (ADR 0001).
type BM25Stage struct {
	K1 float64
	B  float64
}

// Rank implements Stage.
func (s BM25Stage) Rank(q StageQuery, candidates []Chunk) ([]ScoredChunk, error) {
	k1 := s.K1
	if k1 <= 0 {
		k1 = 1.2
	}
	b := s.B
	if b < 0 || b > 1 {
		b = 0.75
	}

	a := SplitPreserveAnalyzer{}
	docs := make([][]string, len(candidates))
	df := map[string]int{}
	for i, c := range candidates {
		terms := a.Analyze(c.Text)
		docs[i] = terms
		seen := map[string]bool{}
		for _, t := range terms {
			if !seen[t] {
				seen[t] = true
				df[t]++
			}
		}
	}

	var totalLen int
	for _, d := range docs {
		totalLen += len(d)
	}
	avgdl := 0.0
	if len(docs) > 0 {
		avgdl = float64(totalLen) / float64(len(docs))
	}

	N := len(candidates)
	// BM25 sums over DISTINCT query terms; the analyzer emits whole-token +
	// split even when they coincide (e.g. "foo" → [foo, foo]), so dedupe.
	seenQ := map[string]bool{}
	var distinctQ []string
	for _, qt := range q.Terms {
		if !seenQ[qt] {
			seenQ[qt] = true
			distinctQ = append(distinctQ, qt)
		}
	}

	scored := make([]ScoredChunk, len(candidates))
	for i, c := range candidates {
		tf := map[string]int{}
		for _, t := range docs[i] {
			tf[t]++
		}
		dl := float64(len(docs[i]))
		var score float64
		for _, qt := range distinctQ {
			n := df[qt]
			if n == 0 {
				continue
			}
			idf := math.Log(1 + (float64(N)-float64(n)+0.5)/(float64(n)+0.5))
			f := float64(tf[qt])
			denom := f + k1*(1-b+b*dl/avgdl)
			if denom == 0 {
				continue
			}
			score += idf * (f * (k1 + 1)) / denom
		}
		scored[i] = ScoredChunk{Chunk: c, Score: score}
	}
	return scored, nil
}