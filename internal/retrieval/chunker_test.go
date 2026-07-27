package retrieval_test

import (
	"strings"
	"testing"

	"github.com/denialbb/limen/internal/retrieval"
)

func TestLineWindowChunker_OverlapWindows(t *testing.T) {
	lines := make([]string, 60)
	for i := range lines {
		lines[i] = "line"
	}
	content := []byte(strings.Join(lines, "\n") + "\n")

	c := retrieval.LineWindowChunker{Window: 50, Overlap: 10}
	chunks := c.Chunk("f.txt", content)

	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
	if chunks[0].LineStart != 1 || chunks[0].LineEnd != 50 {
		t.Errorf("chunk0: expected 1-50, got %d-%d", chunks[0].LineStart, chunks[0].LineEnd)
	}
	if chunks[1].LineStart != 41 || chunks[1].LineEnd != 60 {
		t.Errorf("chunk1: expected 41-60, got %d-%d", chunks[1].LineStart, chunks[1].LineEnd)
	}
}

func TestLineWindowChunker_ShortFileIsSingleChunk(t *testing.T) {
	content := []byte("a\nb\nc\n")
	c := retrieval.LineWindowChunker{Window: 50, Overlap: 10}
	chunks := c.Chunk("f.txt", content)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0].LineStart != 1 || chunks[0].LineEnd != 3 {
		t.Errorf("expected 1-3, got %d-%d", chunks[0].LineStart, chunks[0].LineEnd)
	}
}
