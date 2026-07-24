package retrieval_test

import (
	"context"
	"testing"

	"github.com/denialbb/limen/internal/retrieval"
)

func TestPipeline_CoverageHint_PartialMatch(t *testing.T) {
	corpus := retrieval.StaticCorpus{
		{Path: "math.txt", Content: []byte("add subtract here\n")},
	}
	p := retrieval.NewPipeline(retrieval.WithCorpusLoader(corpus))
	m, err := p.Retrieve(context.Background(), retrieval.Query{Text: "add subtract frobnicate xyz", TaskID: "t"}, retrieval.ExpandState{})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	// Surviving query terms: {add, subtract, frobnicate, xyz}; covered: {add, subtract}.
	want := 0.5
	if !floatEq(m.CoverageHint, want) {
		t.Errorf("CoverageHint: got %v, want %v", m.CoverageHint, want)
	}
}

func TestPipeline_CoverageHint_SurvivalExclusion(t *testing.T) {
	corpus := retrieval.StaticCorpus{
		{Path: "math.txt", Content: []byte("add here\n")},
	}
	p := retrieval.NewPipeline(retrieval.WithCorpusLoader(corpus))
	m, err := p.Retrieve(context.Background(), retrieval.Query{Text: "add a 3 frobnicate", TaskID: "t"}, retrieval.ExpandState{})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	// "a" (single letter) and "3" (pure digit) excluded; surviving {add, frobnicate}; covered {add}.
	if !floatEq(m.CoverageHint, 0.5) {
		t.Errorf("CoverageHint: got %v, want 0.5", m.CoverageHint)
	}
}

func TestPipeline_CoverageHint_StopwordRemoved(t *testing.T) {
	corpus := retrieval.StaticCorpus{
		{Path: "math.txt", Content: []byte("add here\n")},
	}
	p := retrieval.NewPipeline(retrieval.WithCorpusLoader(corpus))
	m, err := p.Retrieve(context.Background(), retrieval.Query{Text: "the add", TaskID: "t"}, retrieval.ExpandState{})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	// "the" stopword removed; surviving {add}; covered {add} → 1.0.
	if !floatEq(m.CoverageHint, 1.0) {
		t.Errorf("CoverageHint: got %v, want 1.0", m.CoverageHint)
	}
}

func TestPipeline_CoverageHint_ZeroOnNoMatch(t *testing.T) {
	corpus := retrieval.StaticCorpus{
		{Path: "math.txt", Content: []byte("add here\n")},
	}
	p := retrieval.NewPipeline(retrieval.WithCorpusLoader(corpus))
	m, err := p.Retrieve(context.Background(), retrieval.Query{Text: "frobnicate缠绕", TaskID: "t"}, retrieval.ExpandState{})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	// Corpus has no whole-token "frobnicate"; coverage 0.0 → escape-hatch trigger.
	if m.CoverageHint != 0 {
		t.Errorf("CoverageHint: got %v, want 0", m.CoverageHint)
	}
	if len(m.Chunks) != 0 {
		t.Errorf("expected no chunks on zero-coverage query, got %d", len(m.Chunks))
	}
}

func floatEq(a, b float64) bool {
	const eps = 1e-9
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < eps
}