package retrieval_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/denialbb/limen/internal/retrieval"
)

func TestManifest_MarshalOmitsPerChunkScores(t *testing.T) {
	m := retrieval.Manifest{
		QueryID:      "task-t1:#0",
		Confidence:   0.8,
		CoverageHint: 0.5,
		Chunks: []retrieval.Chunk{
			{Path: "a.go", LineStart: 1, LineEnd: 3, Text: "foo\nbar\nbaz\n"},
		},
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("Unmarshal map: %v", err)
	}
	for _, key := range []string{"query_id", "chunks", "confidence", "coverage_hint"} {
		if _, ok := generic[key]; !ok {
			t.Errorf("expected top-level key %q, missing in %s", key, string(raw))
		}
	}
	if _, ok := generic["sources"]; ok {
		t.Errorf("sources must be dropped (ADR 0004): %s", string(raw))
	}
	chunks, _ := generic["chunks"].([]any)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	c0, _ := chunks[0].(map[string]any)
	for _, forbidden := range []string{"score", "stage", "id"} {
		if _, ok := c0[forbidden]; ok {
			t.Errorf("chunk must not carry %q (ADR 0004): %s", forbidden, string(raw))
		}
	}
	for _, key := range []string{"path", "line_start", "line_end", "text"} {
		if _, ok := c0[key]; !ok {
			t.Errorf("chunk missing %q: %s", key, string(raw))
		}
	}
}

func TestParseManifest_RoundTrips(t *testing.T) {
	m := retrieval.Manifest{
		QueryID:      "task-t1:#2",
		Confidence:   0.6875,
		CoverageHint: 0.5,
		Chunks: []retrieval.Chunk{
			{Path: "a.go", LineStart: 1, LineEnd: 50, Text: "func foo() {}\n"},
		},
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got, err := retrieval.ParseManifest(string(raw))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if got.QueryID != m.QueryID || !floatEq(got.Confidence, m.Confidence) ||
		!floatEq(got.CoverageHint, m.CoverageHint) || len(got.Chunks) != 1 ||
		got.Chunks[0].Path != "a.go" || got.Chunks[0].LineStart != 1 || got.Chunks[0].LineEnd != 50 {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestRenderContextSection_FencedChunks(t *testing.T) {
	m := retrieval.Manifest{
		Chunks: []retrieval.Chunk{
			{Path: "a.go", LineStart: 1, LineEnd: 2, Text: "func foo() {\n}\n"},
		},
	}
	s := retrieval.RenderContextSection(m)
	if !strings.HasPrefix(s, "## Context") {
		t.Errorf("expected leading ## Context, got %q", s)
	}
	if !strings.Contains(s, "a.go:1-2") {
		t.Errorf("expected path:line_start-line_end, got %q", s)
	}
	if !strings.Contains(s, "```go") || !strings.Contains(s, "func foo()") {
		t.Errorf("expected fenced ```go block with text, got %q", s)
	}

	// The substring checks above are each satisfied by many different layouts:
	// they cannot see a missing newline between the header and the fence, or a
	// dropped blank line between chunks. The rendered section is fed to the
	// worker verbatim, so pin it byte for byte.
	want := "## Context\n\na.go:1-2\n```go\nfunc foo() {\n}\n```\n"
	if s != want {
		t.Errorf("rendered section mismatch:\n got %q\nwant %q", s, want)
	}
}

// TestRenderContextSection_SeparatesMultipleChunks pins the separator between
// chunks: each block ends with a blank line, and only the final trailing
// newline is trimmed.
func TestRenderContextSection_SeparatesMultipleChunks(t *testing.T) {
	m := retrieval.Manifest{
		Chunks: []retrieval.Chunk{
			{Path: "a.go", LineStart: 1, LineEnd: 1, Text: "one\n"},
			{Path: "b.py", LineStart: 4, LineEnd: 4, Text: "two\n"},
		},
	}
	want := "## Context\n\na.go:1-1\n```go\none\n```\n\nb.py:4-4\n```python\ntwo\n```\n"
	if got := retrieval.RenderContextSection(m); got != want {
		t.Errorf("rendered section mismatch:\n got %q\nwant %q", got, want)
	}
}
