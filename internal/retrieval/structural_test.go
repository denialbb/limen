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
