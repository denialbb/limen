package retrieval_test

import (
	"context"
	"testing"

	"github.com/denialbb/limen/internal/retrieval"
)

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
