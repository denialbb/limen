package retrieval

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
)

// ParseManifest is the contract-format owner for the manifest JSON
// (ADR 0004). The Router and any consumer parse through this function so the
// wire format lives in one package.
func ParseManifest(raw string) (Manifest, error) {
	var m Manifest
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// langFor infers a fenced-code-block language from a file path. Defaults to
// empty (plain ``` fence) when unknown.
func langFor(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".js":
		return "javascript"
	case ".ts":
		return "typescript"
	case ".sh":
		return "bash"
	case ".md":
		return "markdown"
	case ".json":
		return "json"
	case ".sql":
		return "sql"
	default:
		return ""
	}
}

// RenderContextSection renders the manifest's top-k chunks into a `## Context`
// markdown section for the worker spawn prompt (ADR 0007). Per chunk:
//
//	<path>:<line_start>-<line_end>
//	```<lang>
//	<text>
//	```
//
// confidence / coverage_hint / query_id are NOT surfaced to the worker
// (Router-internal). Returns "" when the manifest has no chunks.
func RenderContextSection(m Manifest) string {
	if len(m.Chunks) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Context\n\n")
	for _, c := range m.Chunks {
		b.WriteString(c.Path)
		b.WriteString(":")
		b.WriteString(strconv.Itoa(c.LineStart))
		b.WriteString("-")
		b.WriteString(strconv.Itoa(c.LineEnd))
		b.WriteByte('\n')
		b.WriteString("```")
		b.WriteString(langFor(c.Path))
		b.WriteByte('\n')
		b.WriteString(strings.TrimSuffix(c.Text, "\n"))
		b.WriteString("\n```\n\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}
