package retrieval

import (
	"regexp"
	"strings"
)

// StructuralStage boosts chunks that contain a symbol-definition occurrence of
// a whole-token query term (ADR 0001): `func <term>`, `def <term>`,
// `type <term>`, `const <term>`, `var <term>`, `class <term>`. Cheap and
// deterministic; no AST. Parameters keep the boost additive (sums into the
// stage aggregation).
type StructuralStage struct {
	Boost float64
}

// defPattern matches a definition-line keyword followed by the term
// (word-boundary at the end so "Retrieve" doesn't match "RetrieveAll").
// It's applied per-line, per whole-token term.
var defPattern = regexp.MustCompile(`^(?:func|def|type|const|var|class)\s+(\w+)`)

// Rank implements Stage.
func (s StructuralStage) Rank(q StageQuery, candidates []Chunk) ([]ScoredChunk, error) {
	boost := s.Boost
	if boost <= 0 {
		boost = 1.0
	}

	wholeSet := map[string]bool{}
	for _, w := range q.Whole {
		wholeSet[strings.ToLower(w)] = true
	}
	if len(wholeSet) == 0 {
		out := make([]ScoredChunk, len(candidates))
		for i, c := range candidates {
			out[i] = ScoredChunk{Chunk: c}
		}
		return out, nil
	}

	scored := make([]ScoredChunk, len(candidates))
	for i, c := range candidates {
		var b float64
		for _, line := range strings.Split(c.Text, "\n") {
			m := defPattern.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			if wholeSet[strings.ToLower(m[1])] {
				b += boost
			}
		}
		scored[i] = ScoredChunk{Chunk: c, Score: b}
	}
	return scored, nil
}
