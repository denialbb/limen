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

// TestLineWindowChunker_DegenerateConfigsNormalizeToExactWindows pins what each
// normalization rule normalizes *to*, not merely that some chunking happens.
// The property suite already asserts the shape invariants (every line covered,
// no chunk exceeds the window), and those hold for almost any step size — so
// they cannot see the default window drift by a line, a negative overlap being
// honored as a negative number, or an overlap >= window collapsing the step to
// 1 instead of the full window.
func TestLineWindowChunker_DegenerateConfigsNormalizeToExactWindows(t *testing.T) {
	type span struct{ start, end int }

	numberedLines := func(n int) []byte {
		lines := make([]string, n)
		for i := range lines {
			lines[i] = "line"
		}
		return []byte(strings.Join(lines, "\n") + "\n")
	}

	tests := []struct {
		name    string
		chunker retrieval.LineWindowChunker
		lines   int
		want    []span
	}{
		{
			// Window <= 0 falls back to the ADR 0004 default of 50 lines.
			name:    "zero window uses the 50-line default",
			chunker: retrieval.LineWindowChunker{Window: 0, Overlap: 0},
			lines:   60,
			want:    []span{{1, 50}, {51, 60}},
		},
		{
			name:    "negative window uses the 50-line default",
			chunker: retrieval.LineWindowChunker{Window: -5, Overlap: 0},
			lines:   60,
			want:    []span{{1, 50}, {51, 60}},
		},
		{
			// A negative overlap becomes 0, so the step is the full window.
			// Honoring -1 literally would widen the step to window+1.
			name:    "negative overlap becomes zero",
			chunker: retrieval.LineWindowChunker{Window: 10, Overlap: -1},
			lines:   25,
			want:    []span{{1, 10}, {11, 20}, {21, 25}},
		},
		{
			// overlap == window would make the step 0 and the chunker would
			// crawl one line at a time; it must reset the overlap to 0 instead.
			name:    "overlap equal to the window becomes zero",
			chunker: retrieval.LineWindowChunker{Window: 10, Overlap: 10},
			lines:   25,
			want:    []span{{1, 10}, {11, 20}, {21, 25}},
		},
		{
			name:    "overlap larger than the window becomes zero",
			chunker: retrieval.LineWindowChunker{Window: 10, Overlap: 12},
			lines:   25,
			want:    []span{{1, 10}, {11, 20}, {21, 25}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			chunks := tc.chunker.Chunk("f.txt", numberedLines(tc.lines))
			if len(chunks) != len(tc.want) {
				t.Fatalf("got %d chunks, want %d: %v", len(chunks), len(tc.want), chunks)
			}
			for i, w := range tc.want {
				if chunks[i].LineStart != w.start || chunks[i].LineEnd != w.end {
					t.Errorf("chunk %d = %d-%d, want %d-%d", i, chunks[i].LineStart, chunks[i].LineEnd, w.start, w.end)
				}
			}
		})
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
