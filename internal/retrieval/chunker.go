package retrieval

import "strings"

// Chunker is the seam over text-to-chunk splitting. Production default is
// LineWindowChunker; tests inject fakes. Two adapters, so a real seam.
type Chunker interface {
	Chunk(path string, content []byte) []Chunk
}

// LineWindowChunker splits content into fixed line-windows with overlap
// (ADR 0004: ~50-line window, ~10-line overlap).
type LineWindowChunker struct {
	Window  int
	Overlap int
}

// Chunk implements Chunker.
func (c LineWindowChunker) Chunk(path string, content []byte) []Chunk {
	text := string(content)
	trimmed := strings.TrimSuffix(text, "\n")
	if trimmed == "" {
		return nil
	}
	lines := strings.Split(trimmed, "\n")
	n := len(lines)

	window := c.Window
	if window <= 0 {
		window = 50
	}
	overlap := c.Overlap
	if overlap < 0 {
		overlap = 0
	}
	if overlap >= window {
		overlap = 0
	}
	step := window - overlap
	if step <= 0 {
		step = 1
	}

	var out []Chunk
	for start := 0; start < n; start += step {
		end := start + window
		if end > n {
			end = n
		}
		chunkText := strings.Join(lines[start:end], "\n") + "\n"
		out = append(out, Chunk{
			Path:      path,
			LineStart: start + 1,
			LineEnd:   end,
			Text:      chunkText,
		})
		if end == n {
			break
		}
	}
	return out
}