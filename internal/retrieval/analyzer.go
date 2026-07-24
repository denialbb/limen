package retrieval

import "strings"

// Analyzer is the seam over text→term-set transformation. Production default
// is SplitPreserveAnalyzer; tests inject fakes. Two adapters, real seam.
type Analyzer interface {
	Analyze(text string) []string
}

// SplitPreserveAnalyzer splits each whitespace-delimited identifier on
// non-alphanumeric and camelCase boundaries AND emits the whole lowercased
// identifier (ADR 0001). Stopword removal and survival exclusion
// (ADR 0003) are query-side coverage filters, applied there.
type SplitPreserveAnalyzer struct{}

// Analyze implements Analyzer. It returns the token list WITH duplicates
// (BM25 builds term frequency from the multiset); dedup is the caller's
// concern (coverage dedupes distinct query terms; membership is set-based).
func (SplitPreserveAnalyzer) Analyze(text string) []string {
	var out []string
	for _, raw := range strings.Fields(text) {
		out = append(out, strings.ToLower(raw))
		for _, piece := range splitIdentifier(raw) {
			p := strings.ToLower(piece)
			if p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

// splitIdentifier splits a single identifier on non-alphanumeric and camelCase
// boundaries, preserving case for boundary detection.
func splitIdentifier(id string) []string {
	var pieces []string
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			pieces = append(pieces, b.String())
			b.Reset()
		}
	}
	var prevClass int // 0=none,1=lower,2=upper,3=digit
	const (
		cNone = iota
		cLower
		cUpper
		cDigit
	)
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z':
			if prevClass == cUpper && b.Len() > 1 {
				// camel boundary: lower-after-upper-prev ( UserName -> User|Name )
				flush()
			}
			b.WriteRune(r)
			prevClass = cLower
		case r >= 'A' && r <= 'Z':
			if prevClass == cLower || prevClass == cDigit {
				// camel boundary: upper-after-lower ( getU -> get|U )
				flush()
			}
			b.WriteRune(r)
			prevClass = cUpper
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			prevClass = cDigit
		default:
			flush()
			prevClass = cNone
		}
	}
	flush()
	return pieces
}