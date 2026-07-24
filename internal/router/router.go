// Package router implements the Router cascade per ADR 0005: a pure function
// over (confidence, coverage_hint) against configurable floors.
package router

import (
	"context"
	"fmt"
	"time"

	"github.com/denialbb/limen/internal/bus"
	"github.com/denialbb/limen/internal/orchestrator"
	"github.com/denialbb/limen/internal/retrieval"
	"github.com/denialbb/limen/internal/state"
)

// defaultCoverageFloor is the starter default for CoverageFloor per ADR 0005.
const defaultCoverageFloor = 0.60

// defaultConfidenceFloor is the starter default for ConfidenceFloor per ADR 0005.
const defaultConfidenceFloor = 0.50

// Router evaluates task context entropy against configured floors and emits a
// PROCEED / EXPAND / ESCALATE decision. It does NOT see expandCount — exhaustion
// is orchestrator-side.
type Router struct {
	// CoverageFloor is the minimum coverage_hint threshold. When coverage_hint
	// falls below this value (and is > 0), the Router signals EXPAND.
	CoverageFloor float64

	// ConfidenceFloor is the minimum confidence threshold. When confidence
	// falls below this value (after coverage passes), the Router signals
	// ESCALATE.
	ConfidenceFloor float64
}

// NewRouter constructs a Router. Zero values are replaced with starter defaults
// (coverage_floor=0.60, confidence_floor=0.50) from ADR 0005.
func NewRouter(coverageFloor, confidenceFloor float64) *Router {
	if coverageFloor <= 0 {
		coverageFloor = defaultCoverageFloor
	}
	if confidenceFloor <= 0 {
		confidenceFloor = defaultConfidenceFloor
	}
	return &Router{
		CoverageFloor:   coverageFloor,
		ConfidenceFloor: confidenceFloor,
	}
}

// Evaluate reads the task's ContextSnapshot as a retrieval manifest, applies the
// cascade logic, emits RouterExamining and RouterDecisionEvent, and returns the
// routing decision.
//
// Cascade (ADR 0005):
//
//	0. coverage_hint == 0                          → ESCALATE (escape hatch)
//	1. coverage_hint < CoverageFloor   (> 0)        → EXPAND
//	2. else, confidence < ConfidenceFloor           → ESCALATE
//	3. else                                         → PROCEED
//
// If the snapshot is empty or malformed, the Router defaults to PROCEED for
// backward compatibility with the cliRetriever stub.
func (r *Router) Evaluate(ctx context.Context, task *state.Task, em orchestrator.Emitter) (orchestrator.RouterDecision, error) {
	var (
		coverageHint float64
		confidence   float64
	)

	manifest, err := retrieval.ParseManifest(task.ContextSnapshot)
	if err != nil {
		// Malformed or empty snapshot: emit with defaults and PROCEED for
		// backward compat with stub retriever.
		return r.emitAndReturn(ctx, task.ID, task.ContextSnapshot, orchestrator.DecisionProceed, "empty or malformed context snapshot; proceeding", em)
	}

	coverageHint = manifest.CoverageHint
	confidence = manifest.Confidence

	// Cascade 0: zero coverage is the escape hatch — no EXPAND, straight to ESCALATE.
	if coverageHint == 0 {
		return r.emitAndReturn(ctx, task.ID, task.ContextSnapshot, orchestrator.DecisionEscalate,
			"zero coverage: no query terms matched any chunk (escape hatch)", em)
	}

	// Cascade 1: low coverage triggers EXPAND to widen the candidate pool.
	if coverageHint < r.CoverageFloor {
		return r.emitAndReturn(ctx, task.ID, task.ContextSnapshot, orchestrator.DecisionExpand,
			"low coverage: widening candidate pool to surface more context", em)
	}

	// Cascade 2: coverage passes but confidence is below threshold → ESCALATE.
	if confidence < r.ConfidenceFloor {
		return r.emitAndReturn(ctx, task.ID, task.ContextSnapshot, orchestrator.DecisionEscalate,
			"low confidence: best chunk below confidence threshold", em)
	}

	// Cascade 3: both coverage and confidence are sufficient → PROCEED.
	return r.emitAndReturn(ctx, task.ID, task.ContextSnapshot, orchestrator.DecisionProceed,
		"sufficient confidence and coverage", em)
}

// emitAndReturn publishes RouterExamining and RouterDecisionEvent with the given
// decision and rationale, then returns the decision.
//
// Entropy is set to 0 in RouterExamining — deprecated per ADR 0005, kept for TUI
// field compatibility.
func (r *Router) emitAndReturn(ctx context.Context, taskID, excerpt string, decision orchestrator.RouterDecision, rationale string, em orchestrator.Emitter) (orchestrator.RouterDecision, error) {
	now := time.Now()

	em.Publish(&bus.RouterExamining{
		TaskID:         taskID,
		ContextExcerpt: truncateExcerpt(excerpt),
		Entropy:        0, // deprecated per ADR 0005, kept for TUI compat
		Timestamp:      now,
	})

	em.Publish(&bus.RouterDecisionEvent{
		TaskID:      taskID,
		Decision:    bus.RouterDecision(decision),
		Rationale:   rationale,
		ExpandCount: 0,
		Timestamp:   now,
	})

	_ = ctx // context reserved for future use (e.g. cancellation-aware emits)
	return decision, nil
}

// truncateExcerpt caps the ContextExcerpt at 512 runes to avoid flooding the TUI
// with raw snapshot text while still giving the user a preview.
func truncateExcerpt(s string) string {
	if len(s) <= 512 {
		return s
	}
	return s[:512] + fmt.Sprintf("... (truncated %d bytes)", len(s)-512)
}
