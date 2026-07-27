package retrieval

import "math"

// confidence computes the query-normalized best-match magnitude
// (ADR 0002): saturate(topChunkStageScore / Σ IDF of distinct query terms).
// Returns 0 when there are no chunks or no discriminative query mass.
//
// IDF is computed over the full candidate set (the corpus), not the surfaced
// top-k — the denominator is the query's discriminative mass, independent of
// which chunks surfaced. The numerator is the top chunk's aggregated stage
// score (the same axis the ranking is built on — the coherence leg of ADR 0002).
func confidence(topScore float64, queryTerms []string, candidates []Chunk) float64 {
	if len(candidates) == 0 || topScore <= 0 {
		return 0
	}
	distinct := map[string]bool{}
	for _, t := range queryTerms {
		distinct[t] = true
	}
	if len(distinct) == 0 {
		return 0
	}

	a := SplitPreserveAnalyzer{}
	docTerms := make([]map[string]bool, len(candidates))
	for i, c := range candidates {
		set := map[string]bool{}
		for _, t := range a.Analyze(c.Text) {
			set[t] = true
		}
		docTerms[i] = set
	}
	N := len(candidates)
	var sumIDF float64
	for t := range distinct {
		n := 0
		for _, d := range docTerms {
			if d[t] {
				n++
			}
		}
		if n == 0 {
			continue
		}
		sumIDF += math.Log(1 + (float64(N)-float64(n)+0.5)/(float64(n)+0.5))
	}
	if sumIDF <= 0 {
		return 0
	}
	ratio := topScore / sumIDF
	if ratio > 1 {
		return 1
	}
	if ratio < 0 {
		return 0
	}
	return ratio
}
