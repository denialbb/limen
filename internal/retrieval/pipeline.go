// Package retrieval implements the progressive-retrieval pipeline that emits
// the Context Manifest consumed by the Router. See docs/adr/0001 through 0008.
package retrieval

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Manifest is the retrieval output contract (ADR 0004). The sole channel from
// Retriever to Router, persisted verbatim in the Task's context_snapshot.
type Manifest struct {
	QueryID      string  `json:"query_id"`
	Chunks       []Chunk `json:"chunks"`
	Confidence   float64 `json:"confidence"`
	CoverageHint float64 `json:"coverage_hint"`
}

// Chunk is a bounded unit of retrievable text (ADR 0004).
type Chunk struct {
	Path      string `json:"path"`
	LineStart int    `json:"line_start"`
	LineEnd   int    `json:"line_end"`
	Text      string `json:"text"`
}

// Query is the retrieval request: the task's prompt text and the task ID used
// to assemble the manifest's QueryID.
type Query struct {
	Text   string
	TaskID string
}

// ExpandState carries the EXPAND iteration counter into Retrieve (ADR 0006).
type ExpandState struct {
	Iteration int
}

// StageQuery is the query view passed to every Stage. Terms is the full
// analyzed term set (whole + subtokens, deduped) for lexical matching;
// Whole is the distinct lowercased whole-token forms for structural matching
// (ADR 0003's whole-token notion).
type StageQuery struct {
	Terms []string
	Whole []string
}

// File is one unit of the Corpus: a path and its full content.
type File struct {
	Path    string
	Content []byte
}

// Corpus is the set of files retrieval operates over.
type Corpus []File

// CorpusLoader is the seam over corpus construction. Production adapter reads
// git ls-files; tests use StaticCorpus. Two adapters, so a real seam.
type CorpusLoader interface {
	Load(ctx context.Context, repoPath string) (Corpus, error)
}

// StaticCorpus is a test CorpusLoader over a fixed set of files.
type StaticCorpus []File

// Load implements CorpusLoader.
func (s StaticCorpus) Load(ctx context.Context, repoPath string) (Corpus, error) {
	return Corpus(s), nil
}

const (
	defaultCandidateFloor = 100
	defaultExpandAlpha    = 0.5
)

// Pipeline is the deep module: composable Stages, a Chunker, an Analyzer and a
// CorpusLoader behind a one-method external interface (Retrieve).
type Pipeline struct {
	loader         CorpusLoader
	chunker        Chunker
	analyzer       Analyzer
	stages         []Stage
	candidateFloor int
	expandAlpha    float64
}

// Option configures a Pipeline.
type Option func(*Pipeline)

// WithCorpusLoader injects a CorpusLoader (test seam).
func WithCorpusLoader(cl CorpusLoader) Option {
	return func(p *Pipeline) { p.loader = cl }
}

// WithChunker injects a Chunker (test seam).
func WithChunker(c Chunker) Option {
	return func(p *Pipeline) { p.chunker = c }
}

// WithAnalyzer injects an Analyzer (test seam).
func WithAnalyzer(a Analyzer) Option {
	return func(p *Pipeline) { p.analyzer = a }
}

// WithCandidateFloor sets the BM25 gating pool size N (ADR 0006). On expand
// iteration i the gate keeps top K = N·(1+α)^i candidates. Default 100.
func WithCandidateFloor(n int) Option {
	return func(p *Pipeline) { p.candidateFloor = n }
}

// WithStages injects the ranking Stages (test seam). Default is BM25 + Structural.
func WithStages(s ...Stage) Option {
	return func(p *Pipeline) { p.stages = s }
}

// NewPipeline constructs a Pipeline with the given options.
func NewPipeline(opts ...Option) *Pipeline {
	p := &Pipeline{
		chunker:        LineWindowChunker{Window: 50, Overlap: 10},
		analyzer:       SplitPreserveAnalyzer{},
		stages:         []Stage{BM25Stage{K1: 1.2, B: 0.75}, StructuralStage{Boost: 1.0}},
		candidateFloor: defaultCandidateFloor,
		expandAlpha:    defaultExpandAlpha,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Retrieve builds the manifest for the query at the given expand iteration.
func (p *Pipeline) Retrieve(ctx context.Context, q Query, es ExpandState) (Manifest, error) {
	corpus, err := p.loader.Load(ctx, "")
	if err != nil {
		return Manifest{}, err
	}

	var candidates []Chunk
	for _, f := range corpus {
		candidates = append(candidates, p.chunker.Chunk(f.Path, f.Content)...)
	}

	queryTerms := p.analyzer.Analyze(q.Text)
	if len(queryTerms) == 0 {
		return Manifest{QueryID: p.queryID(q, es)}, nil
	}

	// Whole-token forms (lowercased) for structural matching.
	wholeSeen := map[string]bool{}
	var whole []string
	for _, f := range strings.Fields(q.Text) {
		w := strings.ToLower(f)
		if !wholeSeen[w] {
			wholeSeen[w] = true
			whole = append(whole, w)
		}
	}
	stageQuery := StageQuery{Terms: queryTerms, Whole: whole}

	k := float64(p.candidateFloor) * math.Pow(1+p.expandAlpha, float64(es.Iteration))
	if k > 0 && int(k) < len(candidates) {
		for _, stage := range p.stages {
			if s, ok := stage.(BM25Stage); ok {
				scored, err := s.Rank(stageQuery, candidates)
				if err != nil {
					return Manifest{}, err
				}
				sort.Slice(scored, func(i, j int) bool { return scored[i].Score > scored[j].Score })
				n := int(k)
				candidates = make([]Chunk, n)
				for i := 0; i < n; i++ {
					candidates[i] = scored[i].Chunk
				}
				break
			}
		}
	}

	scores := make(map[string]float64, len(candidates))
	key := func(c Chunk) string { return c.Path + ":" + strconv.Itoa(c.LineStart) }
	for _, stage := range p.stages {
		scored, err := stage.Rank(stageQuery, candidates)
		if err != nil {
			return Manifest{}, err
		}
		for _, sc := range scored {
			scores[key(sc.Chunk)] += sc.Score
		}
	}

	type ranked struct {
		c     Chunk
		score float64
	}
	var rankedChunks []ranked
	for _, c := range candidates {
		s := scores[key(c)]
		if s <= 0 {
			continue
		}
		rankedChunks = append(rankedChunks, ranked{c: c, score: s})
	}
	sort.Slice(rankedChunks, func(i, j int) bool {
		if rankedChunks[i].score != rankedChunks[j].score {
			return rankedChunks[i].score > rankedChunks[j].score
		}
		if rankedChunks[i].c.Path != rankedChunks[j].c.Path {
			return rankedChunks[i].c.Path < rankedChunks[j].c.Path
		}
		return rankedChunks[i].c.LineStart < rankedChunks[j].c.LineStart
	})

	const topK = 10
	if len(rankedChunks) > topK {
		rankedChunks = rankedChunks[:topK]
	}
	chunks := make([]Chunk, len(rankedChunks))
	for i, r := range rankedChunks {
		chunks[i] = r.c
	}
	var topScore float64
	if len(rankedChunks) > 0 {
		topScore = rankedChunks[0].score
	}
	return Manifest{
		QueryID:      p.queryID(q, es),
		Chunks:       chunks,
		Confidence:   confidence(topScore, queryTerms, candidates),
		CoverageHint: coverageHint(q.Text, chunks),
	}, nil
}

func (p *Pipeline) queryID(q Query, es ExpandState) string {
	return fmt.Sprintf("task-%s:#%d", q.TaskID, es.Iteration)
}