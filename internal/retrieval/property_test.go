package retrieval

import (
	"encoding/json"
	"math"
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"testing/quick"
)

// Property-based tests for the retrieval package's pure math domains
// (CDD alignment, Iteration 1). Each test states one invariant that must hold
// across randomly generated inputs, rather than one worked example.
//
// Conventions used throughout this file:
//
//   - Every quick.Check runs with a fixed seed (see propConfig) so a failure
//     reproduces exactly on the next run. quick.CheckError's message carries
//     the failing input, so t.Fatalf(%v) is enough to surface a counterexample.
//   - Generators are bounded (short identifiers, small corpora). BM25 over a
//     large random corpus is quadratic-ish and would dominate the suite runtime
//     without buying additional confidence.
//   - Where an invariant only holds under a precondition (e.g. a chunker
//     configuration that the production normalizer would otherwise rewrite),
//     the generator produces only inputs satisfying it, and a separate
//     table-driven test pins the degenerate cases.

// propSeed is the fixed seed behind every property in this file. Changing it
// re-rolls the entire corpus of generated inputs, so leave it alone unless you
// intend to search a different slice of the input space.
const propSeed = 0x5EED

// propConfig returns a quick.Config with a deterministic source. Each call
// site passes a distinct offset so different properties explore different
// input sequences while each stays individually reproducible.
func propConfig(offset int64, maxCount int) *quick.Config {
	return &quick.Config{
		MaxCount: maxCount,
		Rand:     rand.New(rand.NewSource(propSeed + offset)),
	}
}

// identChars is the alphabet used to build generated identifiers: ASCII
// letters and digits (which splitIdentifier treats as content), the separators
// it splits on, and a few non-ASCII runes, which splitIdentifier classifies as
// separators via its default branch.
var identChars = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789___-.:/éü日")

// identifier is a generated source-code-like identifier: 1-24 runes drawn from
// identChars. It stands in for one whitespace-delimited field of a query or a
// chunk of source text.
type identifier string

// Generate implements quick.Generator.
func (identifier) Generate(r *rand.Rand, size int) reflect.Value {
	n := 1 + r.Intn(24)
	var b strings.Builder
	for range n {
		b.WriteRune(identChars[r.Intn(len(identChars))])
	}
	return reflect.ValueOf(identifier(b.String()))
}

// wsChars are the whitespace runes used to join generated fields. All are
// recognized by strings.Fields, so the field count is predictable.
var wsChars = []rune(" \t\n")

// identText is generated free text: 0-8 identifiers joined by random
// whitespace, optionally with leading/trailing whitespace. It stands in for a
// query string or a chunk body.
type identText string

// Generate implements quick.Generator.
func (identText) Generate(r *rand.Rand, size int) reflect.Value {
	n := r.Intn(9)
	var b strings.Builder
	if r.Intn(4) == 0 {
		b.WriteRune(wsChars[r.Intn(len(wsChars))])
	}
	for i := range n {
		if i > 0 {
			b.WriteRune(wsChars[r.Intn(len(wsChars))])
		}
		id := identifier("").Generate(r, size).Interface().(identifier)
		b.WriteString(string(id))
	}
	if r.Intn(4) == 0 {
		b.WriteRune(wsChars[r.Intn(len(wsChars))])
	}
	return reflect.ValueOf(identText(b.String()))
}

// --- Slice 1: Analyzer (SplitPreserveAnalyzer) -----------------------------

// TestAnalyzer_NoEmptyTokens asserts Analyze never emits an empty token, for
// any input. An empty token would silently inflate document length in BM25 and
// pollute the term-frequency map with a term no query can ever match.
func TestAnalyzer_NoEmptyTokens(t *testing.T) {
	a := SplitPreserveAnalyzer{}
	f := func(text identText) bool {
		for _, tok := range a.Analyze(string(text)) {
			if tok == "" {
				return false
			}
		}
		return true
	}
	if err := quick.Check(f, propConfig(1, 300)); err != nil {
		t.Fatalf("Analyze emitted an empty token: %v", err)
	}
}

// TestAnalyzer_AllTokensAreLowercased asserts every emitted token equals its
// own lowercase form. Query and chunk text both flow through this analyzer, so
// a single non-lowercased token on either side would make matching
// case-sensitive by accident.
func TestAnalyzer_AllTokensAreLowercased(t *testing.T) {
	a := SplitPreserveAnalyzer{}
	f := func(text identText) bool {
		for _, tok := range a.Analyze(string(text)) {
			if tok != strings.ToLower(tok) {
				return false
			}
		}
		return true
	}
	if err := quick.Check(f, propConfig(2, 300)); err != nil {
		t.Fatalf("Analyze emitted a non-lowercased token: %v", err)
	}
}

// TestAnalyzer_WholeFieldSurvivesAsToken asserts the "preserve" half of
// SplitPreserveAnalyzer: every whitespace-delimited field of the input appears
// verbatim (lowercased) in the output, alongside its split pieces. This is the
// property ADR 0001 relies on — an exact-identifier query must still match the
// identifier even though the analyzer also splits it.
//
// NOTE: the invariant is stated at *field* granularity, not at the granularity
// of every non-separator run. For a field like "a-UserName" the runs "User"
// and "Name" appear, but the run-level whole "username" does not — only the
// full field "a-username" and the camel pieces do. Field-level is the contract
// the code actually implements and that ADR 0001 describes.
func TestAnalyzer_WholeFieldSurvivesAsToken(t *testing.T) {
	a := SplitPreserveAnalyzer{}
	f := func(text identText) bool {
		got := map[string]bool{}
		for _, tok := range a.Analyze(string(text)) {
			got[tok] = true
		}
		for _, field := range strings.Fields(string(text)) {
			if !got[strings.ToLower(field)] {
				return false
			}
		}
		return true
	}
	if err := quick.Check(f, propConfig(3, 300)); err != nil {
		t.Fatalf("a whitespace field did not survive as a whole token: %v", err)
	}
}

// TestAnalyzer_SplitPiecesAreAlphanumeric asserts splitIdentifier only ever
// emits runs of ASCII letters and digits: separators are consumed as
// boundaries, never carried into a piece. BM25 term keys depend on this — a
// piece carrying a stray separator would never match the query-side token.
func TestAnalyzer_SplitPiecesAreAlphanumeric(t *testing.T) {
	f := func(id identifier) bool {
		for _, piece := range splitIdentifier(string(id)) {
			if piece == "" {
				return false
			}
			for _, r := range piece {
				alnum := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
				if !alnum {
					return false
				}
			}
		}
		return true
	}
	if err := quick.Check(f, propConfig(4, 300)); err != nil {
		t.Fatalf("splitIdentifier emitted a non-alphanumeric piece: %v", err)
	}
}

// TestAnalyzer_SecondPassIsSuperset asserts the token set stabilizes: feeding
// the analyzer's own output back through it reproduces at least the original
// token set. Re-analysis is therefore never lossy, which is what makes the
// analyzer safe to apply at both corpus-build and query time.
func TestAnalyzer_SecondPassIsSuperset(t *testing.T) {
	a := SplitPreserveAnalyzer{}
	f := func(text identText) bool {
		first := a.Analyze(string(text))
		second := a.Analyze(strings.Join(first, " "))
		got := map[string]bool{}
		for _, tok := range second {
			got[tok] = true
		}
		for _, tok := range first {
			if !got[tok] {
				return false
			}
		}
		return true
	}
	if err := quick.Check(f, propConfig(5, 300)); err != nil {
		t.Fatalf("re-analyzing the analyzer's output lost a token: %v", err)
	}
}

// TestSplitIdentifier_BoundaryClassification pins the exact split for each
// character-class transition splitIdentifier recognizes, and for the alphabet
// edges of each class.
//
// The properties above constrain the token *set* (lowercased, non-empty,
// alphanumeric, a superset on re-analysis) and Analyze always emits the whole
// lowercased field alongside the pieces, so a piece that is mis-split or
// dropped entirely is invisible to them: the whole-token form still carries the
// text. These cases assert the pieces themselves.
//
// The alphabet edges matter because the classifier uses inclusive range
// comparisons ('a' <= r <= 'z' and friends). Narrowing any bound to an
// exclusive one reclassifies exactly one character as a separator, which
// silently deletes it from every piece it appears in.
func TestSplitIdentifier_BoundaryClassification(t *testing.T) {
	tests := []struct {
		id   string
		want []string
	}{
		// Separator splitting, including single-character pieces: a piece of
		// length 1 is still a piece.
		{"a", []string{"a"}},
		{"a_b", []string{"a", "b"}},
		{"parse.Http2Request", []string{"parse", "Http2", "Request"}},

		// camelCase: an upper-after-lower transition splits...
		{"getU", []string{"get", "U"}},
		{"UserName", []string{"User", "Name"}},
		// ...and a lower-after-upper transition splits only when it leaves a
		// non-empty acronym behind, so an initial capital stays attached.
		{"Abc", []string{"Abc"}},
		{"ABc", []string{"AB", "c"}},
		{"ABC", []string{"ABC"}},

		// Digits attach to the run they appear in, and an upper-after-digit
		// transition splits.
		{"a0", []string{"a0"}},
		{"a9", []string{"a9"}},
		{"x1Y2", []string{"x1", "Y2"}},

		// Alphabet edges of each class: 'a', 'z', 'A', 'Z', '0' and '9' are all
		// content characters, never separators.
		{"az", []string{"az"}},
		{"AZ", []string{"AZ"}},
		{"Zoo", []string{"Zoo"}},
		{"z0", []string{"z0"}},
		{"a09z", []string{"a09z"}},
	}

	for _, tc := range tests {
		got := splitIdentifier(tc.id)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("splitIdentifier(%q) = %#v, want %#v", tc.id, got, tc.want)
		}
	}
}

// --- Slice 2: Chunker (LineWindowChunker) ----------------------------------

// chunkerInput is a generated (content, window, overlap) triple in the
// configuration range the chunker treats as already-valid: window >= 1 and
// 0 <= overlap < window. Constraining the generator this way keeps the
// properties below stated against the caller's configuration directly. The
// degenerate configurations the production code normalizes (window <= 0,
// negative overlap, overlap >= window) are pinned separately by
// TestChunker_DegenerateConfigsAreNormalized.
type chunkerInput struct {
	Lines   int
	Content []byte
	Window  int
	Overlap int
}

// Generate implements quick.Generator.
func (chunkerInput) Generate(r *rand.Rand, size int) reflect.Value {
	nLines := r.Intn(60) // 0 means empty content
	var b strings.Builder
	for i := range nLines {
		if i > 0 {
			b.WriteByte('\n')
		}
		// Line bodies are irrelevant to the windowing invariants; keep them
		// short and never empty-with-trailing-newline ambiguity.
		b.WriteString("line")
		b.WriteString(strings.Repeat("x", r.Intn(4)))
	}
	if nLines > 0 && r.Intn(2) == 0 {
		b.WriteByte('\n') // trailing newline is trimmed by the chunker
	}
	window := 1 + r.Intn(20)
	overlap := r.Intn(window) // strictly less than window
	return reflect.ValueOf(chunkerInput{
		Lines:   nLines,
		Content: []byte(b.String()),
		Window:  window,
		Overlap: overlap,
	})
}

// TestChunker_EmptyContentYieldsNoChunks asserts content with no lines
// produces no chunks at all, rather than one empty chunk. An empty chunk would
// reach BM25 as a zero-length document and skew average document length.
func TestChunker_EmptyContentYieldsNoChunks(t *testing.T) {
	f := func(window, overlap uint8) bool {
		c := LineWindowChunker{Window: int(window%20) + 1, Overlap: int(overlap % 20)}
		for _, empty := range [][]byte{nil, {}, []byte("\n")} {
			if got := c.Chunk("f.go", empty); len(got) != 0 {
				return false
			}
		}
		return true
	}
	if err := quick.Check(f, propConfig(10, 100)); err != nil {
		t.Fatalf("empty content produced chunks: %v", err)
	}
}

// TestChunker_EveryLineIsCovered asserts the union of the chunk ranges covers
// every line of the input exactly once or more — no line falls through a gap
// between windows. A dropped line is silently unretrievable context, the worst
// failure mode this package has.
func TestChunker_EveryLineIsCovered(t *testing.T) {
	f := func(in chunkerInput) bool {
		c := LineWindowChunker{Window: in.Window, Overlap: in.Overlap}
		chunks := c.Chunk("f.go", in.Content)
		if in.Lines == 0 {
			return len(chunks) == 0
		}
		covered := make([]bool, in.Lines)
		for _, ch := range chunks {
			// LineStart is 1-based inclusive; LineEnd is 1-based inclusive.
			for ln := ch.LineStart; ln <= ch.LineEnd; ln++ {
				if ln < 1 || ln > in.Lines {
					return false // range escaped the file
				}
				covered[ln-1] = true
			}
		}
		for _, ok := range covered {
			if !ok {
				return false
			}
		}
		return true
	}
	if err := quick.Check(f, propConfig(11, 300)); err != nil {
		t.Fatalf("a line was not covered by any chunk: %v", err)
	}
}

// TestChunker_ShortFileIsExactlyOneChunk asserts a file no longer than the
// window becomes exactly one chunk spanning the whole file — the chunker never
// fragments a file it could carry whole.
func TestChunker_ShortFileIsExactlyOneChunk(t *testing.T) {
	f := func(in chunkerInput) bool {
		if in.Lines == 0 || in.Lines > in.Window {
			return true // precondition not met; vacuously true
		}
		c := LineWindowChunker{Window: in.Window, Overlap: in.Overlap}
		chunks := c.Chunk("f.go", in.Content)
		return len(chunks) == 1 &&
			chunks[0].LineStart == 1 &&
			chunks[0].LineEnd == in.Lines
	}
	if err := quick.Check(f, propConfig(12, 300)); err != nil {
		t.Fatalf("a file within one window was not a single whole chunk: %v", err)
	}
}

// TestChunker_ChunkNeverExceedsWindow asserts no chunk spans more lines than
// the configured window. The window is the bound the manifest's size budget is
// reasoned about in (ADR 0004), so an oversized chunk breaks that budget.
func TestChunker_ChunkNeverExceedsWindow(t *testing.T) {
	f := func(in chunkerInput) bool {
		c := LineWindowChunker{Window: in.Window, Overlap: in.Overlap}
		for _, ch := range c.Chunk("f.go", in.Content) {
			span := ch.LineEnd - ch.LineStart + 1
			if span < 1 || span > in.Window {
				return false
			}
		}
		return true
	}
	if err := quick.Check(f, propConfig(13, 300)); err != nil {
		t.Fatalf("a chunk spanned more than the window: %v", err)
	}
}

// TestChunker_ConsecutiveOverlapIsBounded asserts neighbouring chunks share at
// most the configured overlap. More overlap than configured means duplicated
// text inflating the manifest; the overlap exists to avoid splitting a symbol
// across a boundary (ADR 0004), not to pad the output.
func TestChunker_ConsecutiveOverlapIsBounded(t *testing.T) {
	f := func(in chunkerInput) bool {
		c := LineWindowChunker{Window: in.Window, Overlap: in.Overlap}
		chunks := c.Chunk("f.go", in.Content)
		for i := 1; i < len(chunks); i++ {
			prev, curr := chunks[i-1], chunks[i]
			if curr.LineStart <= prev.LineStart {
				return false // must advance strictly
			}
			shared := prev.LineEnd - curr.LineStart + 1
			if shared > in.Overlap {
				return false
			}
		}
		return true
	}
	if err := quick.Check(f, propConfig(14, 300)); err != nil {
		t.Fatalf("consecutive chunks overlapped by more than the configured overlap: %v", err)
	}
}

// TestChunker_ChunkCountIsMonotonicInLineCount asserts adding lines to a file
// never reduces its chunk count, for a fixed configuration. Monotonicity is
// what makes the candidate-pool size predictable as the corpus grows.
func TestChunker_ChunkCountIsMonotonicInLineCount(t *testing.T) {
	build := func(n int) []byte {
		if n == 0 {
			return nil
		}
		lines := make([]string, n)
		for i := range lines {
			lines[i] = "line"
		}
		return []byte(strings.Join(lines, "\n") + "\n")
	}
	f := func(a, b uint8, window, overlap uint8) bool {
		w := int(window%20) + 1
		o := int(overlap) % w
		c := LineWindowChunker{Window: w, Overlap: o}
		na, nb := int(a)%80, int(b)%80
		if na < nb {
			na, nb = nb, na
		}
		return len(c.Chunk("f.go", build(na))) >= len(c.Chunk("f.go", build(nb)))
	}
	if err := quick.Check(f, propConfig(15, 300)); err != nil {
		t.Fatalf("chunk count was not monotonic in line count: %v", err)
	}
}

// TestChunker_DegenerateConfigsAreNormalized pins the configurations the
// property generators deliberately exclude, where the chunker rewrites the
// caller's numbers rather than trusting them. Table-driven because there are
// only a handful of cases and each has a specific expected normalization.
func TestChunker_DegenerateConfigsAreNormalized(t *testing.T) {
	content := []byte(strings.Repeat("line\n", 120))
	tests := []struct {
		name    string
		window  int
		overlap int
	}{
		{"zero window falls back to 50", 0, 10},
		{"negative window falls back to 50", -5, 10},
		{"negative overlap clamps to zero", 20, -3},
		{"overlap equal to window resets to zero", 20, 20},
		{"overlap larger than window resets to zero", 20, 40},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := LineWindowChunker{Window: tc.window, Overlap: tc.overlap}
			chunks := c.Chunk("f.go", content)
			if len(chunks) == 0 {
				t.Fatalf("degenerate config produced no chunks")
			}
			// Whatever normalization applied, the file must still be fully
			// covered and every chunk must be a sane forward range.
			covered := make([]bool, 120)
			for _, ch := range chunks {
				if ch.LineStart < 1 || ch.LineEnd > 120 || ch.LineStart > ch.LineEnd {
					t.Fatalf("invalid chunk range %d-%d", ch.LineStart, ch.LineEnd)
				}
				for ln := ch.LineStart; ln <= ch.LineEnd; ln++ {
					covered[ln-1] = true
				}
			}
			for i, ok := range covered {
				if !ok {
					t.Fatalf("line %d uncovered under degenerate config", i+1)
				}
			}
		})
	}
}

// --- Slice 3: BM25 (BM25Stage.Rank) ----------------------------------------

// bm25Corpus is a generated (candidates, query) pair. Sizes are capped
// deliberately: Rank is O(docs x terms) and a larger corpus buys no additional
// coverage of the scoring algebra, only runtime.
type bm25Corpus struct {
	Candidates []Chunk
	Query      StageQuery
}

// Generate implements quick.Generator. Query terms are drawn from the same
// small vocabulary the documents are built from, so terms actually hit — a
// query of purely absent terms would make every score trivially zero and the
// properties vacuous.
func (bm25Corpus) Generate(r *rand.Rand, size int) reflect.Value {
	vocab := []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta", "theta"}

	nDocs := 1 + r.Intn(12)
	candidates := make([]Chunk, nDocs)
	for i := range candidates {
		nWords := r.Intn(15)
		words := make([]string, nWords)
		for j := range words {
			words[j] = vocab[r.Intn(len(vocab))]
		}
		candidates[i] = Chunk{
			Path:      "f" + string(rune('a'+i%26)) + ".go",
			LineStart: 1 + i,
			LineEnd:   1 + i,
			Text:      strings.Join(words, " "),
		}
	}

	nTerms := 1 + r.Intn(4)
	terms := make([]string, nTerms)
	for i := range terms {
		terms[i] = vocab[r.Intn(len(vocab))]
	}
	return reflect.ValueOf(bm25Corpus{
		Candidates: candidates,
		Query:      StageQuery{Terms: terms, Whole: terms},
	})
}

// scoreByChunk keys a Rank result by chunk identity so results can be compared
// across input permutations, where positional comparison is meaningless.
func scoreByChunk(scored []ScoredChunk) map[string]float64 {
	out := make(map[string]float64, len(scored))
	for _, sc := range scored {
		out[sc.Chunk.Path+":"+sc.Chunk.Text] = sc.Score
	}
	return out
}

// TestBM25Stage_ScoresAreNonNegativeAndFinite asserts every score is a
// non-negative real number. BM25 sums non-negative IDF-weighted saturations, so
// a negative score, a NaN or an Inf would mean the algebra broke — and since
// the pipeline drops chunks scoring <= 0, a NaN would silently discard context.
func TestBM25Stage_ScoresAreNonNegativeAndFinite(t *testing.T) {
	s := BM25Stage{K1: 1.2, B: 0.75}
	f := func(in bm25Corpus) bool {
		scored, err := s.Rank(in.Query, in.Candidates)
		if err != nil {
			return false
		}
		for _, sc := range scored {
			if math.IsNaN(sc.Score) || math.IsInf(sc.Score, 0) || sc.Score < 0 {
				return false
			}
		}
		return true
	}
	if err := quick.Check(f, propConfig(20, 200)); err != nil {
		t.Fatalf("BM25 produced a negative, NaN or infinite score: %v", err)
	}
}

// TestBM25Stage_RankIsDeterministic asserts repeated calls on identical input
// return identical scores in identical positions. The pipeline sorts on these
// scores, so any run-to-run wobble would make retrieval non-reproducible —
// which the determinism boundary forbids for a ranking input.
func TestBM25Stage_RankIsDeterministic(t *testing.T) {
	s := BM25Stage{K1: 1.2, B: 0.75}
	f := func(in bm25Corpus) bool {
		first, err1 := s.Rank(in.Query, in.Candidates)
		second, err2 := s.Rank(in.Query, in.Candidates)
		if err1 != nil || err2 != nil || len(first) != len(second) {
			return false
		}
		for i := range first {
			if first[i].Score != second[i].Score || first[i].Chunk != second[i].Chunk {
				return false
			}
		}
		return true
	}
	if err := quick.Check(f, propConfig(21, 200)); err != nil {
		t.Fatalf("BM25 was not deterministic across identical calls: %v", err)
	}
}

// TestBM25Stage_ScoreIsPermutationInvariant asserts a chunk's score depends on
// its content and the corpus statistics, never on its index in the input
// slice. The pipeline builds the candidate slice by iterating a map-free but
// loader-ordered corpus; if position leaked into the score, retrieval results
// would depend on filesystem enumeration order.
func TestBM25Stage_ScoreIsPermutationInvariant(t *testing.T) {
	s := BM25Stage{K1: 1.2, B: 0.75}
	r := rand.New(rand.NewSource(propSeed + 22))
	f := func(in bm25Corpus) bool {
		original, err := s.Rank(in.Query, in.Candidates)
		if err != nil {
			return false
		}
		shuffled := append([]Chunk(nil), in.Candidates...)
		r.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		permuted, err := s.Rank(in.Query, shuffled)
		if err != nil {
			return false
		}
		want, got := scoreByChunk(original), scoreByChunk(permuted)
		if len(want) != len(got) {
			return false
		}
		for k, v := range want {
			if other, ok := got[k]; !ok || math.Abs(other-v) > 1e-9 {
				return false
			}
		}
		return true
	}
	if err := quick.Check(f, propConfig(22, 200)); err != nil {
		t.Fatalf("BM25 score depended on input ordering: %v", err)
	}
}

// TestBM25Stage_ScoreIsMonotonicInTermFrequency asserts that, holding document
// length and the corpus fixed, a document containing more occurrences of the
// query term never scores lower than one containing fewer.
//
// Document length is held constant on purpose: BM25 normalizes by length, so
// simply appending occurrences would confound "more matches" with "longer
// document" and the property would be false for reasons that are not a bug.
// Here the extra occurrences displace filler words instead.
func TestBM25Stage_ScoreIsMonotonicInTermFrequency(t *testing.T) {
	s := BM25Stage{K1: 1.2, B: 0.75}
	f := func(hiRaw, loRaw, lenRaw uint8) bool {
		total := 2 + int(lenRaw)%12 // document length in words
		hi := int(hiRaw) % (total + 1)
		lo := int(loRaw) % (total + 1)
		if hi < lo {
			hi, lo = lo, hi
		}
		build := func(count int) string {
			words := make([]string, total)
			for i := range words {
				if i < count {
					words[i] = "alpha"
				} else {
					words[i] = "filler"
				}
			}
			return strings.Join(words, " ")
		}
		docHi := Chunk{Path: "hi.go", LineStart: 1, LineEnd: 1, Text: build(hi)}
		docLo := Chunk{Path: "lo.go", LineStart: 1, LineEnd: 1, Text: build(lo)}

		scored, err := s.Rank(StageQuery{Terms: []string{"alpha"}}, []Chunk{docHi, docLo})
		if err != nil || len(scored) != 2 {
			return false
		}
		// Equal-length docs, so the only difference is term frequency.
		return scored[0].Score >= scored[1].Score-1e-12
	}
	if err := quick.Check(f, propConfig(23, 300)); err != nil {
		t.Fatalf("BM25 score was not monotonic in term frequency at fixed length: %v", err)
	}
}

// TestBM25Stage_RarerTermCarriesHigherIDF asserts the IDF half of the formula:
// a term occurring in fewer corpus documents contributes more per occurrence
// than a common one. The comparison holds term frequency and document length
// fixed, so the score gap is attributable to IDF alone.
//
// This is what makes retrieval discriminative — without it, a query full of
// ubiquitous tokens would rank as confidently as one naming a rare symbol.
func TestBM25Stage_RarerTermCarriesHigherIDF(t *testing.T) {
	s := BM25Stage{K1: 1.2, B: 0.75}
	f := func(nRaw uint8) bool {
		n := 2 + int(nRaw)%10 // corpus size; needs >= 2 docs to differentiate

		// doc0 contains both terms with identical term frequency and length.
		// Every other doc contains only the common term.
		candidates := make([]Chunk, n)
		candidates[0] = Chunk{Path: "d0.go", LineStart: 1, LineEnd: 1, Text: "rare common"}
		for i := 1; i < n; i++ {
			candidates[i] = Chunk{
				Path:      "d" + string(rune('a'+i%26)) + ".go",
				LineStart: 1,
				LineEnd:   1,
				Text:      "common other",
			}
		}

		rareScored, err := s.Rank(StageQuery{Terms: []string{"rare"}}, candidates)
		if err != nil {
			return false
		}
		commonScored, err := s.Rank(StageQuery{Terms: []string{"common"}}, candidates)
		if err != nil {
			return false
		}
		// Same doc, same tf, same length: the rare term must weigh more.
		return rareScored[0].Score > commonScored[0].Score
	}
	if err := quick.Check(f, propConfig(24, 200)); err != nil {
		t.Fatalf("a rarer term did not carry a higher IDF weight: %v", err)
	}
}

// --- Slice 4: Confidence ---------------------------------------------------

// confidenceInput is a generated (topScore, queryTerms, candidates) tuple.
// topScore is drawn across the interesting range — negative, zero, and
// positive up to well past the IDF sum — so the clamp on both ends is exercised.
type confidenceInput struct {
	TopScore   float64
	QueryTerms []string
	Candidates []Chunk
}

// Generate implements quick.Generator.
func (confidenceInput) Generate(r *rand.Rand, size int) reflect.Value {
	vocab := []string{"alpha", "beta", "gamma", "delta", "epsilon"}

	nDocs := r.Intn(10) // zero is meaningful: the empty-corpus case
	candidates := make([]Chunk, nDocs)
	for i := range candidates {
		nWords := r.Intn(10)
		words := make([]string, nWords)
		for j := range words {
			words[j] = vocab[r.Intn(len(vocab))]
		}
		candidates[i] = Chunk{
			Path:      "f" + string(rune('a'+i%26)) + ".go",
			LineStart: 1,
			LineEnd:   1,
			Text:      strings.Join(words, " "),
		}
	}

	nTerms := r.Intn(4)
	terms := make([]string, nTerms)
	for i := range terms {
		terms[i] = vocab[r.Intn(len(vocab))]
	}

	// Spread topScore over negative, zero and positive magnitudes.
	topScore := (r.Float64() * 12) - 2
	if r.Intn(6) == 0 {
		topScore = 0
	}
	return reflect.ValueOf(confidenceInput{
		TopScore:   topScore,
		QueryTerms: terms,
		Candidates: candidates,
	})
}

// sumIDFOver mirrors the denominator confidence divides by: the summed IDF of
// the distinct query terms over the candidate corpus. It exists so the
// saturation property can be stated in terms of the theoretical maximum rather
// than a magic number.
func sumIDFOver(queryTerms []string, candidates []Chunk) float64 {
	distinct := map[string]bool{}
	for _, t := range queryTerms {
		distinct[t] = true
	}
	a := SplitPreserveAnalyzer{}
	docTerms := make([]map[string]bool, len(candidates))
	for i, c := range candidates {
		set := map[string]bool{}
		for _, t := range a.Analyze(c.Text) {
			set[t] = true
		}
		docTerms[i] = set
	}
	n := len(candidates)
	var sum float64
	for term := range distinct {
		df := 0
		for _, d := range docTerms {
			if d[term] {
				df++
			}
		}
		if df == 0 {
			continue
		}
		sum += math.Log(1 + (float64(n)-float64(df)+0.5)/(float64(df)+0.5))
	}
	return sum
}

// TestConfidence_AlwaysInUnitRange asserts confidence never escapes [0, 1] for
// any input, including negative and enormous top scores. The Router thresholds
// (ADR 0005) compare this value against fixed cutoffs; a value outside the unit
// range would make those comparisons meaningless.
func TestConfidence_AlwaysInUnitRange(t *testing.T) {
	f := func(in confidenceInput) bool {
		c := confidence(in.TopScore, in.QueryTerms, in.Candidates)
		return c >= 0 && c <= 1 && !math.IsNaN(c)
	}
	if err := quick.Check(f, propConfig(30, 300)); err != nil {
		t.Fatalf("confidence escaped [0,1]: %v", err)
	}
}

// TestConfidence_ZeroWithoutEvidence asserts confidence is exactly zero
// whenever there is nothing to be confident about: an empty corpus, a
// non-positive top score, or a query with no terms. Each is an absence of
// evidence, and ADR 0002 defines confidence as best-match magnitude — with no
// match there is no magnitude.
func TestConfidence_ZeroWithoutEvidence(t *testing.T) {
	f := func(in confidenceInput) bool {
		if got := confidence(in.TopScore, in.QueryTerms, nil); got != 0 {
			return false
		}
		if got := confidence(in.TopScore, in.QueryTerms, []Chunk{}); got != 0 {
			return false
		}
		if got := confidence(0, in.QueryTerms, in.Candidates); got != 0 {
			return false
		}
		if in.TopScore > 0 {
			if got := confidence(-in.TopScore, in.QueryTerms, in.Candidates); got != 0 {
				return false
			}
		}
		if got := confidence(in.TopScore, nil, in.Candidates); got != 0 {
			return false
		}
		// A query the corpus cannot answer at all: the terms occur in no
		// candidate, so every per-term IDF is skipped and the summed IDF stays
		// 0. That zero is a denominator — without the guard the ratio is
		// +Inf, which saturates to a confident 1.0 on evidence that does not
		// exist. NUL cannot appear in an analyzed token, so the term is
		// guaranteed absent whatever the generator produced.
		if len(in.Candidates) > 0 {
			absent := []string{"\x00absent-from-every-chunk"}
			if got := confidence(math.Abs(in.TopScore)+1, absent, in.Candidates); got != 0 {
				return false
			}
		}
		return true
	}
	if err := quick.Check(f, propConfig(31, 300)); err != nil {
		t.Fatalf("confidence was non-zero without evidence: %v", err)
	}
}

// TestConfidence_SaturatesAtTheIDFCeiling asserts confidence reaches exactly
// 1.0 once the top score meets or exceeds the summed IDF of the query terms —
// the theoretical maximum BM25 mass the query can attract. This is the
// saturation half of ADR 0002: beyond the ceiling, more score is not more
// confidence.
func TestConfidence_SaturatesAtTheIDFCeiling(t *testing.T) {
	f := func(in confidenceInput) bool {
		ceiling := sumIDFOver(in.QueryTerms, in.Candidates)
		if ceiling <= 0 {
			return true // no discriminative mass; saturation is not defined
		}
		if got := confidence(ceiling, in.QueryTerms, in.Candidates); math.Abs(got-1) > 1e-9 {
			return false
		}
		// Anything past the ceiling stays pinned at 1.
		if got := confidence(ceiling*2, in.QueryTerms, in.Candidates); got != 1 {
			return false
		}
		return true
	}
	if err := quick.Check(f, propConfig(32, 300)); err != nil {
		t.Fatalf("confidence did not saturate at the IDF ceiling: %v", err)
	}
}

// TestConfidence_IsMonotonicInTopScore asserts that, holding the query and
// corpus fixed, a better top match never yields lower confidence. Without this
// the Router's PROCEED/EXPAND cascade could be driven backwards by an
// improvement in retrieval quality.
func TestConfidence_IsMonotonicInTopScore(t *testing.T) {
	f := func(in confidenceInput, deltaRaw uint8) bool {
		delta := float64(deltaRaw) / 32 // non-negative increment
		lo := confidence(in.TopScore, in.QueryTerms, in.Candidates)
		hi := confidence(in.TopScore+delta, in.QueryTerms, in.Candidates)
		return hi >= lo-1e-12
	}
	if err := quick.Check(f, propConfig(33, 300)); err != nil {
		t.Fatalf("confidence decreased as the top score rose: %v", err)
	}
}

// TestConfidence_IgnoresCandidateOrdering asserts confidence depends only on
// the multiset of candidates, not their order. Document frequency is a set
// property, so any order sensitivity would mean corpus enumeration order
// leaking into a Router threshold decision.
func TestConfidence_IgnoresCandidateOrdering(t *testing.T) {
	r := rand.New(rand.NewSource(propSeed + 34))
	f := func(in confidenceInput) bool {
		original := confidence(in.TopScore, in.QueryTerms, in.Candidates)
		shuffled := append([]Chunk(nil), in.Candidates...)
		r.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		permuted := confidence(in.TopScore, in.QueryTerms, shuffled)
		return math.Abs(original-permuted) < 1e-12
	}
	if err := quick.Check(f, propConfig(34, 300)); err != nil {
		t.Fatalf("confidence depended on candidate ordering: %v", err)
	}
}

// --- Slice 5: Coverage hint ------------------------------------------------

// coverageVocab is the word pool coverage properties draw from. It mixes
// surviving terms, stopwords, single characters and pure digits so the
// exclusion rules of ADR 0003 are actually exercised rather than assumed.
var coverageVocab = []string{
	"alpha", "beta", "gamma", "delta", // surviving
	"the", "of", "and", "for", // stopwords
	"x", "q", // single char: no non-trivial form
	"42", "7", // pure digits: no non-trivial form
}

// coverageInput is a generated (query, chunks) pair built from coverageVocab,
// so query terms genuinely hit chunk text often enough for the properties to
// be non-vacuous.
type coverageInput struct {
	Query  string
	Chunks []Chunk
}

// Generate implements quick.Generator.
func (coverageInput) Generate(r *rand.Rand, size int) reflect.Value {
	nQ := r.Intn(6)
	qWords := make([]string, nQ)
	for i := range qWords {
		qWords[i] = coverageVocab[r.Intn(len(coverageVocab))]
	}

	nChunks := r.Intn(6)
	chunks := make([]Chunk, nChunks)
	for i := range chunks {
		nW := r.Intn(8)
		words := make([]string, nW)
		for j := range words {
			words[j] = coverageVocab[r.Intn(len(coverageVocab))]
		}
		chunks[i] = Chunk{
			Path:      "f" + string(rune('a'+i%26)) + ".go",
			LineStart: 1,
			LineEnd:   1,
			Text:      strings.Join(words, " "),
		}
	}
	return reflect.ValueOf(coverageInput{
		Query:  strings.Join(qWords, " "),
		Chunks: chunks,
	})
}

// TestCoverageHint_AlwaysInUnitRange asserts coverage is a proper ratio for any
// query and chunk set. Like confidence it feeds the Router cascade (ADR 0005),
// so a value outside [0,1] would corrupt a threshold comparison.
func TestCoverageHint_AlwaysInUnitRange(t *testing.T) {
	f := func(in coverageInput) bool {
		c := coverageHint(in.Query, in.Chunks)
		return c >= 0 && c <= 1 && !math.IsNaN(c)
	}
	if err := quick.Check(f, propConfig(40, 300)); err != nil {
		t.Fatalf("coverageHint escaped [0,1]: %v", err)
	}
}

// TestCoverageHint_ZeroWhenNothingMatches asserts coverage is exactly zero when
// no chunk contains any surviving query term — including the degenerate case of
// a query made entirely of stopwords and trivial forms, where the denominator
// itself is empty.
func TestCoverageHint_ZeroWhenNothingMatches(t *testing.T) {
	f := func(in coverageInput) bool {
		// No chunks at all: nothing can be covered.
		if got := coverageHint(in.Query, nil); got != 0 {
			return false
		}
		// Chunks that share no vocabulary with the query.
		disjoint := []Chunk{{Path: "z.go", LineStart: 1, LineEnd: 1, Text: "zzz yyy www"}}
		if got := coverageHint(in.Query, disjoint); got != 0 {
			return false
		}
		// A query with no surviving terms has no denominator, so zero.
		if got := coverageHint("the of and x 42", in.Chunks); got != 0 {
			return false
		}
		return true
	}
	if err := quick.Check(f, propConfig(41, 300)); err != nil {
		t.Fatalf("coverageHint was non-zero with nothing matched: %v", err)
	}
}

// TestCoverageHint_OneWhenEverySurvivingTermIsPresent asserts coverage reaches
// exactly 1 when a single chunk contains every surviving query term. Built by
// construction: the chunk text is the surviving set itself.
func TestCoverageHint_OneWhenEverySurvivingTermIsPresent(t *testing.T) {
	f := func(in coverageInput) bool {
		surviving := survivingQueryTerms(in.Query)
		if len(surviving) == 0 {
			return true // no denominator; full coverage undefined
		}
		terms := make([]string, 0, len(surviving))
		for term := range surviving {
			terms = append(terms, term)
		}
		full := []Chunk{{Path: "all.go", LineStart: 1, LineEnd: 1, Text: strings.Join(terms, " ")}}
		return coverageHint(in.Query, full) == 1
	}
	if err := quick.Check(f, propConfig(42, 300)); err != nil {
		t.Fatalf("coverageHint did not reach 1 with every surviving term present: %v", err)
	}
}

// TestCoverageHint_ExcludesStopwordsAndTrivialForms asserts the ADR 0003
// filters are applied to the denominator: appending stopwords and trivial
// forms to a query cannot change its coverage, because they were never
// counted. Without this, padding a prompt with articles would dilute coverage
// and push the Router toward EXPAND for no reason.
func TestCoverageHint_ExcludesStopwordsAndTrivialForms(t *testing.T) {
	f := func(in coverageInput) bool {
		base := coverageHint(in.Query, in.Chunks)
		padded := in.Query + " the of and a x q 42 7"
		return math.Abs(coverageHint(padded, in.Chunks)-base) < 1e-12
	}
	if err := quick.Check(f, propConfig(43, 300)); err != nil {
		t.Fatalf("stopwords or trivial forms changed coverage: %v", err)
	}
}

// TestCoverageHint_IsMonotonicUnderAddedChunks asserts adding a chunk never
// lowers coverage. Coverage is recall over surviving terms, so more context can
// only cover more of the query — a decrease would mean the metric punishes
// retrieving more.
func TestCoverageHint_IsMonotonicUnderAddedChunks(t *testing.T) {
	f := func(in coverageInput, extra uint8) bool {
		base := coverageHint(in.Query, in.Chunks)
		added := append(append([]Chunk(nil), in.Chunks...), Chunk{
			Path:      "extra.go",
			LineStart: 1,
			LineEnd:   1,
			Text:      coverageVocab[int(extra)%len(coverageVocab)],
		})
		return coverageHint(in.Query, added) >= base-1e-12
	}
	if err := quick.Check(f, propConfig(44, 300)); err != nil {
		t.Fatalf("adding a chunk lowered coverage: %v", err)
	}
}

// TestCoverageHint_IgnoresChunkOrdering asserts coverage depends on the set of
// chunks, not their order — it is recall, not a ranked measure.
func TestCoverageHint_IgnoresChunkOrdering(t *testing.T) {
	r := rand.New(rand.NewSource(propSeed + 45))
	f := func(in coverageInput) bool {
		original := coverageHint(in.Query, in.Chunks)
		shuffled := append([]Chunk(nil), in.Chunks...)
		r.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		return math.Abs(coverageHint(in.Query, shuffled)-original) < 1e-12
	}
	if err := quick.Check(f, propConfig(45, 300)); err != nil {
		t.Fatalf("coverageHint depended on chunk ordering: %v", err)
	}
}

// --- Slice 6: wholeTokens / survivingQueryTerms / hasNonTrivialForm / isBinary

// TestWholeTokens_AreLowercasedFieldsWithoutDuplicates asserts wholeTokens is
// exactly the deduplicated, lowercased whitespace-field set of its input — the
// "whole-token form" ADR 0003 defines coverage over. Unlike the analyzer it
// must NOT split on camel or separator boundaries.
func TestWholeTokens_AreLowercasedFieldsWithoutDuplicates(t *testing.T) {
	f := func(text identText) bool {
		got := wholeTokens(string(text))
		want := map[string]bool{}
		for _, field := range strings.Fields(string(text)) {
			want[strings.ToLower(field)] = true
		}
		if len(got) != len(want) {
			return false
		}
		for term := range want {
			if !got[term] {
				return false
			}
		}
		for term := range got {
			if term == "" || term != strings.ToLower(term) {
				return false
			}
		}
		return true
	}
	if err := quick.Check(f, propConfig(50, 300)); err != nil {
		t.Fatalf("wholeTokens was not the lowercased field set: %v", err)
	}
}

// TestSurvivingQueryTerms_IsFilteredSubsetOfWholeTokens asserts the survival
// filter only ever removes: every surviving term is a whole token, no stopword
// survives, and every survivor has a non-trivial form. Coverage divides by this
// set, so a term appearing here that is absent from wholeTokens would make the
// ratio unreachable by construction.
func TestSurvivingQueryTerms_IsFilteredSubsetOfWholeTokens(t *testing.T) {
	f := func(text identText) bool {
		whole := wholeTokens(string(text))
		surviving := survivingQueryTerms(string(text))
		for term := range surviving {
			if !whole[term] {
				return false // not a subset
			}
			if queryStopwords[term] {
				return false // stopword survived
			}
			if !hasNonTrivialForm(term) {
				return false // trivial form survived
			}
		}
		// Nothing that should have survived was dropped.
		for term := range whole {
			if queryStopwords[term] || !hasNonTrivialForm(term) {
				continue
			}
			if !surviving[term] {
				return false
			}
		}
		return true
	}
	if err := quick.Check(f, propConfig(51, 300)); err != nil {
		t.Fatalf("survivingQueryTerms was not the correctly filtered subset: %v", err)
	}
}

// TestSurvivingQueryTerms_NeverContainsAStopword pins the stopword rule
// directly against the production stopword set, including a query built purely
// from stopwords.
func TestSurvivingQueryTerms_NeverContainsAStopword(t *testing.T) {
	stopwords := make([]string, 0, len(queryStopwords))
	for w := range queryStopwords {
		stopwords = append(stopwords, w)
	}
	f := func(text identText, pick uint8) bool {
		injected := string(text) + " " + stopwords[int(pick)%len(stopwords)]
		for term := range survivingQueryTerms(injected) {
			if queryStopwords[term] {
				return false
			}
		}
		// A query of nothing but stopwords survives as the empty set.
		return len(survivingQueryTerms(strings.Join(stopwords, " "))) == 0
	}
	if err := quick.Check(f, propConfig(52, 300)); err != nil {
		t.Fatalf("a stopword survived query-term filtering: %v", err)
	}
}

// TestHasNonTrivialForm_MatchesLengthAndDigitRule asserts hasNonTrivialForm is
// exactly "at least two bytes and not purely digits", the rule its doc comment
// and ADR 0003 state.
//
// NOTE: the issue text speculated this predicate keys off letter case (true for
// mixed/upper case, false for lowercase-only). It does not — case plays no part
// in the implementation, and the two-byte/all-digit rule below is what ADR 0003
// describes. The property is written against the implemented contract.
func TestHasNonTrivialForm_MatchesLengthAndDigitRule(t *testing.T) {
	f := func(id identifier) bool {
		term := strings.ToLower(string(id))
		allDigit := len(term) > 0
		for _, r := range term {
			if r < '0' || r > '9' {
				allDigit = false
				break
			}
		}
		want := len(term) >= 2 && !allDigit
		return hasNonTrivialForm(term) == want
	}
	if err := quick.Check(f, propConfig(53, 300)); err != nil {
		t.Fatalf("hasNonTrivialForm diverged from the length/digit rule: %v", err)
	}
}

// TestHasNonTrivialForm_EdgeCases pins the boundary inputs the random
// generator is unlikely to produce often enough to trust.
func TestHasNonTrivialForm_EdgeCases(t *testing.T) {
	tests := []struct {
		term string
		want bool
	}{
		{"", false},        // empty
		{"x", false},       // single letter
		{"7", false},       // single digit
		{"42", false},      // pure digits
		{"007", false},     // pure digits with leading zeros
		{"99", false},      // pure digits at the top of the digit range
		{"90", false},      // both ends of the digit range in one term
		{"go", true},       // two letters
		{"x1", true},       // letter plus digit
		{"1x", true},       // digit plus letter
		{"userName", true}, // ordinary identifier
	}
	for _, tc := range tests {
		if got := hasNonTrivialForm(tc.term); got != tc.want {
			t.Errorf("hasNonTrivialForm(%q) = %v, want %v", tc.term, got, tc.want)
		}
	}
}

// TestIsBinary_MatchesNulByteInPrefix asserts isBinary is exactly "a NUL byte
// occurs within the first binaryCheckLen bytes", restated independently of the
// implementation's clamp arithmetic.
func TestIsBinary_MatchesNulByteInPrefix(t *testing.T) {
	f := func(data []byte) bool {
		limit := len(data)
		if limit > binaryCheckLen {
			limit = binaryCheckLen
		}
		want := false
		for _, b := range data[:limit] {
			if b == 0 {
				want = true
				break
			}
		}
		return isBinary(data) == want
	}
	if err := quick.Check(f, propConfig(54, 300)); err != nil {
		t.Fatalf("isBinary diverged from the NUL-in-prefix rule: %v", err)
	}
}

// TestIsBinary_DependsOnlyOnThePrefix asserts bytes past binaryCheckLen cannot
// change the verdict: two slices agreeing on the first binaryCheckLen bytes
// always classify identically, however they differ afterwards. This is what
// bounds the cost of the check on large files.
func TestIsBinary_DependsOnlyOnThePrefix(t *testing.T) {
	f := func(prefixSeed, tailA, tailB []byte) bool {
		// Build a prefix of exactly binaryCheckLen bytes so the tails lie
		// entirely beyond the inspected window.
		prefix := make([]byte, binaryCheckLen)
		for i := range prefix {
			if len(prefixSeed) > 0 {
				prefix[i] = prefixSeed[i%len(prefixSeed)]
			} else {
				prefix[i] = 'a'
			}
		}
		a := append(append([]byte(nil), prefix...), tailA...)
		b := append(append([]byte(nil), prefix...), tailB...)
		return isBinary(a) == isBinary(b)
	}
	if err := quick.Check(f, propConfig(55, 200)); err != nil {
		t.Fatalf("isBinary was influenced by bytes past the prefix: %v", err)
	}
}

// TestIsBinary_EmptyInputIsNotBinary pins the empty cases: there is no NUL to
// find, so nil and the empty slice are both text.
func TestIsBinary_EmptyInputIsNotBinary(t *testing.T) {
	if isBinary(nil) {
		t.Errorf("isBinary(nil) = true, want false")
	}
	if isBinary([]byte{}) {
		t.Errorf("isBinary([]byte{}) = true, want false")
	}
}

// --- Slice 7: Manifest round-trip ------------------------------------------

// jsonSafeChars is the alphabet for generated manifest strings. Every rune is
// valid UTF-8, which matters: encoding/json replaces invalid UTF-8 bytes with
// U+FFFD, so a generator emitting arbitrary bytes would produce round-trip
// "failures" that are documented encoder behavior rather than a defect.
var jsonSafeChars = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 _-./\n\t\"\\{}[]éü日")

// jsonSafeString generates a valid-UTF-8 string of up to max runes.
func jsonSafeString(r *rand.Rand, max int) string {
	n := r.Intn(max + 1)
	var b strings.Builder
	for range n {
		b.WriteRune(jsonSafeChars[r.Intn(len(jsonSafeChars))])
	}
	return b.String()
}

// manifestInput is a generated Manifest within the contract's valid ranges
// (ADR 0004): finite confidence and coverage in [0,1], non-negative line
// ranges, valid-UTF-8 strings. Values outside those ranges are not round-trip
// failures but encoder errors (NaN/Inf cannot be represented in JSON at all),
// so the generator stays inside the contract.
type manifestInput struct {
	M Manifest
}

// Generate implements quick.Generator.
func (manifestInput) Generate(r *rand.Rand, size int) reflect.Value {
	var chunks []Chunk
	// A nil Chunks slice is a valid manifest (no results) and must survive the
	// round trip as nil, so leave it nil sometimes rather than always allocating.
	if r.Intn(4) > 0 {
		n := r.Intn(6)
		chunks = make([]Chunk, n)
		for i := range chunks {
			start := 1 + r.Intn(500)
			chunks[i] = Chunk{
				Path:      jsonSafeString(r, 20),
				LineStart: start,
				LineEnd:   start + r.Intn(50),
				Text:      jsonSafeString(r, 60),
			}
		}
	}
	return reflect.ValueOf(manifestInput{M: Manifest{
		QueryID:      jsonSafeString(r, 24),
		Chunks:       chunks,
		Confidence:   r.Float64(),
		CoverageHint: r.Float64(),
	}})
}

// TestManifest_RoundTripPreservesEverything asserts marshal→ParseManifest is
// lossless: field values, chunk order, and the nil-versus-empty distinction of
// the Chunks slice all survive. The manifest is the sole channel from Retriever
// to Router and is persisted verbatim in the task's context snapshot
// (ADR 0004), so any loss here is loss of the Router's entire input.
func TestManifest_RoundTripPreservesEverything(t *testing.T) {
	f := func(in manifestInput) bool {
		raw, err := json.Marshal(in.M)
		if err != nil {
			return false
		}
		got, err := ParseManifest(string(raw))
		if err != nil {
			return false
		}
		return reflect.DeepEqual(got, in.M)
	}
	if err := quick.Check(f, propConfig(60, 300)); err != nil {
		t.Fatalf("manifest did not survive a marshal/parse round trip: %v", err)
	}
}

// TestManifest_RoundTripPreservesChunkOrder states the ordering half of the
// round-trip property on its own, because it is the part with real
// consequences: the Router reads chunks as a ranked list (ADR 0004 fixes the
// order as top-k descending), so a reordering round trip would silently
// reshuffle relevance.
func TestManifest_RoundTripPreservesChunkOrder(t *testing.T) {
	f := func(in manifestInput) bool {
		raw, err := json.Marshal(in.M)
		if err != nil {
			return false
		}
		got, err := ParseManifest(string(raw))
		if err != nil || len(got.Chunks) != len(in.M.Chunks) {
			return false
		}
		for i := range in.M.Chunks {
			if got.Chunks[i] != in.M.Chunks[i] {
				return false
			}
		}
		return true
	}
	if err := quick.Check(f, propConfig(61, 300)); err != nil {
		t.Fatalf("chunk order changed across the round trip: %v", err)
	}
}

// TestManifest_RenderContextSectionIsDeterministic asserts rendering is a pure
// function of the manifest: the same manifest always produces byte-identical
// output, and a round-tripped manifest renders identically to the original.
// The rendered section goes into the worker's prompt (ADR 0007), so
// render instability would make worker prompts irreproducible.
func TestManifest_RenderContextSectionIsDeterministic(t *testing.T) {
	f := func(in manifestInput) bool {
		first := RenderContextSection(in.M)
		if RenderContextSection(in.M) != first {
			return false
		}
		raw, err := json.Marshal(in.M)
		if err != nil {
			return false
		}
		parsed, err := ParseManifest(string(raw))
		if err != nil {
			return false
		}
		return RenderContextSection(parsed) == first
	}
	if err := quick.Check(f, propConfig(62, 300)); err != nil {
		t.Fatalf("RenderContextSection was not deterministic: %v", err)
	}
}

// TestManifest_RenderContextSectionEmptyWithoutChunks asserts a manifest
// carrying no chunks renders to the empty string rather than a bare "## Context"
// heading — the worker prompt must not gain an empty section.
func TestManifest_RenderContextSectionEmptyWithoutChunks(t *testing.T) {
	f := func(queryID string, conf, cov float64) bool {
		m := Manifest{QueryID: queryID, Confidence: conf, CoverageHint: cov}
		if RenderContextSection(m) != "" {
			return false
		}
		m.Chunks = []Chunk{}
		return RenderContextSection(m) == ""
	}
	if err := quick.Check(f, propConfig(63, 200)); err != nil {
		t.Fatalf("a chunkless manifest rendered a non-empty section: %v", err)
	}
}

// TestManifest_ParseRejectsMalformedJSON pins the error path. It is
// table-driven rather than generated on purpose: a random string is not
// reliably invalid JSON ("null" and "{}" both parse cleanly into a manifest),
// so a "random input must error" property would be false for reasons that are
// correct behavior.
func TestManifest_ParseRejectsMalformedJSON(t *testing.T) {
	malformed := []string{
		"",                        // empty input
		"{",                       // truncated object
		"not json at all",         // bare text
		`{"chunks": }`,            // missing value
		`{"confidence": "high"}`,  // wrong type for a float field
		`{"chunks": {"a": 1}}`,    // wrong type for the chunk list
		`[1,2,3]`,                 // array where an object is required
		`{"query_id": 5}`,         // wrong type for a string field
		`{"chunks":[{"path":1}]}`, // wrong type nested inside a chunk
	}
	for _, raw := range malformed {
		t.Run(raw, func(t *testing.T) {
			got, err := ParseManifest(raw)
			if err == nil {
				t.Fatalf("ParseManifest(%q) = %+v, want an error", raw, got)
			}
			if !reflect.DeepEqual(got, Manifest{}) {
				t.Fatalf("ParseManifest(%q) returned %+v alongside its error, want the zero Manifest", raw, got)
			}
		})
	}
}
