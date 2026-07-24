package retrieval

import "strings"

// queryStopwords is the query-only stopword set (ADR 0001). Applied only to
// the query, never to chunk text.
var queryStopwords = map[string]bool{
	"the": true, "a": true, "an": true, "of": true, "in": true, "to": true,
	"is": true, "it": true, "and": true, "or": true, "for": true, "on": true,
	"with": true, "as": true, "by": true, "at": true, "be": true,
}

// wholeTokens returns the lowercased whitespace-delimited tokens of text —
// the "whole-token form" set used by coverage (ADR 0003). Duplicates removed.
func wholeTokens(text string) map[string]bool {
	set := map[string]bool{}
	for _, f := range strings.Fields(text) {
		set[strings.ToLower(f)] = true
	}
	return set
}

// survivingQueryTerms returns the distinct whole-token query terms that pass
// stopword removal AND have a non-trivial whole-token form (not a single
// letter, not a pure-digit fragment) — ADR 0003's survival exclusion.
func survivingQueryTerms(query string) map[string]bool {
	surviving := map[string]bool{}
	for term := range wholeTokens(query) {
		if queryStopwords[term] {
			continue
		}
		if !hasNonTrivialForm(term) {
			continue
		}
		surviving[term] = true
	}
	return surviving
}

// hasNonTrivialForm reports whether a term has a resolvable whole-token form:
// length > 1 and not purely digits (ADR 0003).
func hasNonTrivialForm(term string) bool {
	if len(term) < 2 {
		return false
	}
	allDigit := true
	for _, r := range term {
		if r < '0' || r > '9' {
			allDigit = false
			break
		}
	}
	return !allDigit
}

// coverageHint computes whole-token query-term recall over the manifest's
// chunks (ADR 0003). Returns 0 if no surviving query terms.
func coverageHint(query string, chunks []Chunk) float64 {
	surviving := survivingQueryTerms(query)
	if len(surviving) == 0 {
		return 0
	}
	covered := map[string]bool{}
	for _, c := range chunks {
		for term := range wholeTokens(c.Text) {
			if surviving[term] {
				covered[term] = true
			}
		}
	}
	return float64(len(covered)) / float64(len(surviving))
}