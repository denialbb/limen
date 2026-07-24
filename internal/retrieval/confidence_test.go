package retrieval_test

import (
	"context"
	"testing"

	"github.com/denialbb/limen/internal/retrieval"
)

func TestPipeline_Confidence_SaturatesToOne(t *testing.T) {
	// Corpus ["foo", "bar"]; query "foo" fully matches the top chunk → ratio 1.375 → saturates to 1.0.
	corpus := retrieval.StaticCorpus{
		{Path: "a", Content: []byte("foo\n")},
		{Path: "b", Content: []byte("bar\n")},
	}
	p := retrieval.NewPipeline(retrieval.WithCorpusLoader(corpus))
	m, err := p.Retrieve(context.Background(), retrieval.Query{Text: "foo", TaskID: "t"}, retrieval.ExpandState{})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if !floatEq(m.Confidence, 1.0) {
		t.Errorf("Confidence: got %v, want 1.0 (saturated)", m.Confidence)
	}
}

func TestPipeline_Confidence_PartialBelowOne(t *testing.T) {
	// Corpus ["foo", "other"]; query "foo other" — top chunk matches only "foo".
	// IDF(foo)=IDF(other)=log(2); top score = IDF*1.375; ΣIDF = 2*IDF → ratio 0.6875.
	corpus := retrieval.StaticCorpus{
		{Path: "a", Content: []byte("foo\n")},
		{Path: "b", Content: []byte("other\n")},
	}
	p := retrieval.NewPipeline(retrieval.WithCorpusLoader(corpus))
	m, err := p.Retrieve(context.Background(), retrieval.Query{Text: "foo other", TaskID: "t"}, retrieval.ExpandState{})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if !floatEq(m.Confidence, 0.6875) {
		t.Errorf("Confidence: got %v, want 0.6875", m.Confidence)
	}
}

func TestPipeline_Confidence_ZeroOnNoChunks(t *testing.T) {
	// Zero-coverage query → no chunks surfaced → confidence 0.
	corpus := retrieval.StaticCorpus{
		{Path: "a", Content: []byte("foo\n")},
	}
	p := retrieval.NewPipeline(retrieval.WithCorpusLoader(corpus))
	m, err := p.Retrieve(context.Background(), retrieval.Query{Text: "frobnicate缠绕", TaskID: "t"}, retrieval.ExpandState{})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if m.Confidence != 0 {
		t.Errorf("Confidence: got %v, want 0", m.Confidence)
	}
}