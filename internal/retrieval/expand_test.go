package retrieval_test

import (
	"context"
	"testing"

	"github.com/denialbb/limen/internal/retrieval"
)

func TestPipeline_ExpandIteration_WidensCandidatePool(t *testing.T) {
	// a, b rank highest by BM25 for "foo"; c has the structural definition but
	// the lowest BM25, so it only surfaces once the gate pool is wide enough.
	corpus := retrieval.StaticCorpus{
		{Path: "a", Content: []byte("foo foo foo foo\n")},
		{Path: "b", Content: []byte("foo foo foo\n")},
		{Path: "c", Content: []byte("func foo\n")},
	}
	p := retrieval.NewPipeline(
		retrieval.WithCorpusLoader(corpus),
		retrieval.WithCandidateFloor(2),
	)

	m0, err := p.Retrieve(context.Background(), retrieval.Query{Text: "foo", TaskID: "t"}, retrieval.ExpandState{Iteration: 0})
	if err != nil {
		t.Fatalf("Retrieve iter0: %v", err)
	}
	if hasChunk(m0.Chunks, "c") {
		t.Errorf("iter0: expected c excluded from gate pool (N=2), got %v", topPaths(m0.Chunks))
	}

	m1, err := p.Retrieve(context.Background(), retrieval.Query{Text: "foo", TaskID: "t"}, retrieval.ExpandState{Iteration: 1})
	if err != nil {
		t.Fatalf("Retrieve iter1: %v", err)
	}
	if len(m1.Chunks) == 0 || m1.Chunks[0].Path != "c" {
		t.Errorf("iter1: expected c to surface first via structural boost over widened pool, got %v", topPaths(m1.Chunks))
	}
	if m1.QueryID != "task-t:#1" {
		t.Errorf("QueryID: got %s, want task-t:#1", m1.QueryID)
	}
}

func hasChunk(cs []retrieval.Chunk, path string) bool {
	for _, c := range cs {
		if c.Path == path {
			return true
		}
	}
	return false
}
