package retrieval_test

import (
	"context"
	"testing"

	"github.com/denialbb/limen/internal/retrieval"
)

func TestStructuralStage_DefinitionChunkBoostsOverCall(t *testing.T) {
	stage := retrieval.StructuralStage{}
	query := retrieval.StageQuery{Whole: []string{"retrieve"}, Terms: []string{"retrieve"}}
	candidates := []retrieval.Chunk{
		{Path: "def.go", Text: "func Retrieve(ctx) error {\n\treturn nil\n}\n"},
		{Path: "call.go", Text: "x := Retrieve(ctx)\n"},
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
	if scoreOf("def.go") <= 0 {
		t.Errorf("expected def chunk boost > 0, got %v", scoreOf("def.go"))
	}
	if scoreOf("call.go") != 0 {
		t.Errorf("expected call chunk boost 0, got %v", scoreOf("call.go"))
	}
}

// TestStructuralStage_NormalizesBoost pins the boost actually applied: a
// non-positive Boost falls back to exactly 1.0, and a positive one is used
// as given. The existing tests only assert "> 0" for a definition chunk, which
// holds for any boost value at all — including one that ignores the field.
func TestStructuralStage_NormalizesBoost(t *testing.T) {
	query := retrieval.StageQuery{Whole: []string{"retrieve"}, Terms: []string{"retrieve"}}
	candidates := []retrieval.Chunk{
		{Path: "def.go", Text: "func Retrieve(ctx) error {\n\treturn nil\n}\n"},
	}

	tests := []struct {
		name  string
		boost float64
		want  float64
	}{
		{"zero boost falls back to 1.0", 0, 1.0},
		{"negative boost falls back to 1.0", -3, 1.0},
		{"fractional boost is honored", 0.5, 0.5},
		{"boost above one is honored", 2, 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scored, err := retrieval.StructuralStage{Boost: tc.boost}.Rank(query, candidates)
			if err != nil {
				t.Fatalf("Rank: %v", err)
			}
			if scored[0].Score != tc.want {
				t.Errorf("Boost=%v produced score %v, want %v", tc.boost, scored[0].Score, tc.want)
			}
		})
	}
}

func TestPipeline_StructuralStage_BreaksBM25Tie(t *testing.T) {
	// Two chunks where BM25 ties (both mention Retrieve once) but structural
	// boost ranks the definition first.
	corpus := retrieval.StaticCorpus{
		{Path: "call.go", Content: []byte("x := Retrieve(ctx)\n")},
		{Path: "def.go", Content: []byte("func Retrieve(ctx) error {\n\treturn nil\n}\n")},
	}
	p := retrieval.NewPipeline(retrieval.WithCorpusLoader(corpus))
	m, err := p.Retrieve(context.Background(), retrieval.Query{Text: "Retrieve", TaskID: "t"}, retrieval.ExpandState{})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(m.Chunks) == 0 || m.Chunks[0].Path != "def.go" {
		t.Errorf("expected def.go first, got %v", topPaths(m.Chunks))
	}
}

func topPaths(cs []retrieval.Chunk) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Path
	}
	return out
}
