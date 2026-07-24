package router_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/denialbb/limen/internal/bus"
	"github.com/denialbb/limen/internal/orchestrator"
	"github.com/denialbb/limen/internal/retrieval"
	"github.com/denialbb/limen/internal/router"
	"github.com/denialbb/limen/internal/state"
)

// testManifest builds a manifest JSON string with the given values.
func testManifest(coverageHint, confidence float64) string {
	m := retrieval.Manifest{
		QueryID:      "task-test:#0",
		Chunks:       []retrieval.Chunk{},
		Confidence:   confidence,
		CoverageHint: coverageHint,
	}
	raw, err := json.Marshal(m)
	if err != nil {
		panic("failed to marshal test manifest: " + err.Error())
	}
	return string(raw)
}

// TestRouter_Evaluate_Proceed sets floors at zero so any valid manifest proceeds.
func TestRouter_Evaluate_Proceed(t *testing.T) {
	r := &router.Router{
		CoverageFloor:   0,
		ConfidenceFloor: 0,
	}
	rec := bus.NewRecorderEmitter()
	task := &state.Task{
		ID:              "task-test",
		ContextSnapshot: testManifest(0.5, 0.5),
	}

	decision, err := r.Evaluate(context.Background(), task, rec)
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if decision != orchestrator.DecisionProceed {
		t.Fatalf("expected PROCEED, got %s", decision)
	}

	// Verify emitted events.
	examined := rec.EventsByKind("RouterExamining")
	if len(examined) != 1 {
		t.Fatalf("expected 1 RouterExamining event, got %d", len(examined))
	}

	decisionEvents := rec.EventsByKind("RouterDecision")
	if len(decisionEvents) != 1 {
		t.Fatalf("expected 1 RouterDecision event, got %d", len(decisionEvents))
	}
	rde := decisionEvents[0].(*bus.RouterDecisionEvent)
	if rde.Decision != bus.DecisionProceed {
		t.Fatalf("expected RouterDecisionEvent.Decision = PROCEED, got %s", rde.Decision)
	}
}

// TestRouter_Evaluate_Expand sets a high CoverageFloor so low coverage triggers EXPAND.
func TestRouter_Evaluate_Expand(t *testing.T) {
	r := &router.Router{
		CoverageFloor:   0.99,
		ConfidenceFloor: 0,
	}
	rec := bus.NewRecorderEmitter()
	task := &state.Task{
		ID:              "task-test",
		ContextSnapshot: testManifest(0.5, 0.5),
	}

	decision, err := r.Evaluate(context.Background(), task, rec)
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if decision != orchestrator.DecisionExpand {
		t.Fatalf("expected EXPAND, got %s", decision)
	}

	decisionEvents := rec.EventsByKind("RouterDecision")
	if len(decisionEvents) != 1 {
		t.Fatalf("expected 1 RouterDecision event, got %d", len(decisionEvents))
	}
	rde := decisionEvents[0].(*bus.RouterDecisionEvent)
	if rde.Decision != bus.DecisionExpand {
		t.Fatalf("expected RouterDecisionEvent.Decision = EXPAND, got %s", rde.Decision)
	}
}

// TestRouter_Evaluate_EscalateOnZeroCoverage verifies the escape hatch:
// coverage_hint == 0 forces ESCALATE irrespective of floors.
func TestRouter_Evaluate_EscalateOnZeroCoverage(t *testing.T) {
	r := &router.Router{
		CoverageFloor:   0.1,
		ConfidenceFloor: 0.1,
	}
	rec := bus.NewRecorderEmitter()
	task := &state.Task{
		ID:              "task-test",
		ContextSnapshot: testManifest(0, 0.9),
	}

	decision, err := r.Evaluate(context.Background(), task, rec)
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if decision != orchestrator.DecisionEscalate {
		t.Fatalf("expected ESCALATE, got %s", decision)
	}

	decisionEvents := rec.EventsByKind("RouterDecision")
	if len(decisionEvents) != 1 {
		t.Fatalf("expected 1 RouterDecision event, got %d", len(decisionEvents))
	}
	rde := decisionEvents[0].(*bus.RouterDecisionEvent)
	if rde.Decision != bus.DecisionEscalate {
		t.Fatalf("expected RouterDecisionEvent.Decision = ESCALATE, got %s", rde.Decision)
	}
}

// TestRouter_Evaluate_EscalateOnLowConfidence sets CoverageFloor low enough to
// pass, but ConfidenceFloor high enough to force ESCALATE on low confidence.
func TestRouter_Evaluate_EscalateOnLowConfidence(t *testing.T) {
	r := &router.Router{
		CoverageFloor:   0.1,
		ConfidenceFloor: 0.99,
	}
	rec := bus.NewRecorderEmitter()
	task := &state.Task{
		ID:              "task-test",
		ContextSnapshot: testManifest(0.5, 0.5),
	}

	decision, err := r.Evaluate(context.Background(), task, rec)
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if decision != orchestrator.DecisionEscalate {
		t.Fatalf("expected ESCALATE, got %s", decision)
	}

	decisionEvents := rec.EventsByKind("RouterDecision")
	if len(decisionEvents) != 1 {
		t.Fatalf("expected 1 RouterDecision event, got %d", len(decisionEvents))
	}
	rde := decisionEvents[0].(*bus.RouterDecisionEvent)
	if rde.Decision != bus.DecisionEscalate {
		t.Fatalf("expected RouterDecisionEvent.Decision = ESCALATE, got %s", rde.Decision)
	}
}

// TestRouter_Evaluate_EmptySnapshot verifies backward compat: an empty
// ContextSnapshot defaults to PROCEED.
func TestRouter_Evaluate_EmptySnapshot(t *testing.T) {
	r := &router.Router{
		CoverageFloor:   0.60,
		ConfidenceFloor: 0.50,
	}
	rec := bus.NewRecorderEmitter()
	task := &state.Task{
		ID:              "task-test",
		ContextSnapshot: "",
	}

	decision, err := r.Evaluate(context.Background(), task, rec)
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if decision != orchestrator.DecisionProceed {
		t.Fatalf("expected PROCEED for empty snapshot, got %s", decision)
	}
}

// TestNewRouter_Defaults verifies that zero values are replaced with the
// starter defaults from ADR 0005.
func TestNewRouter_Defaults(t *testing.T) {
	r := router.NewRouter(0, 0)
	if r.CoverageFloor != 0.60 {
		t.Fatalf("expected CoverageFloor=0.60, got %v", r.CoverageFloor)
	}
	if r.ConfidenceFloor != 0.50 {
		t.Fatalf("expected ConfidenceFloor=0.50, got %v", r.ConfidenceFloor)
	}
}

// TestNewRouter_CustomValues verifies that non-zero values are preserved.
func TestNewRouter_CustomValues(t *testing.T) {
	r := router.NewRouter(0.75, 0.80)
	if r.CoverageFloor != 0.75 {
		t.Fatalf("expected CoverageFloor=0.75, got %v", r.CoverageFloor)
	}
	if r.ConfidenceFloor != 0.80 {
		t.Fatalf("expected ConfidenceFloor=0.80, got %v", r.ConfidenceFloor)
	}
}
