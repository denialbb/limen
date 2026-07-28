package router_test

// Property-based tests for the Router cascade (ADR 0005). The Router is a pure
// projection over (coverage_hint, confidence) against (CoverageFloor,
// ConfidenceFloor); these tests assert the cascade invariants across the whole
// [0,1]^4 input region rather than the handful of points pinned by the example
// tests in router_test.go.
//
// All generators are seeded deterministically so a failure reproduces exactly.

import (
	"context"
	"math/rand"
	"reflect"
	"testing"
	"testing/quick"

	"github.com/denialbb/limen/internal/bus"
	"github.com/denialbb/limen/internal/orchestrator"
	"github.com/denialbb/limen/internal/router"
	"github.com/denialbb/limen/internal/state"
)

// quickSeed fixes the generator seed so property failures are reproducible.
const quickSeed = 42

// quickConfig returns a quick.Config with a fresh deterministic source.
func quickConfig(maxCount int) *quick.Config {
	return &quick.Config{
		MaxCount: maxCount,
		Rand:     rand.New(rand.NewSource(quickSeed)), //nolint:gosec // deterministic test input, not crypto
	}
}

// evaluateAt runs the Router over a manifest built from (cov, conf) with the
// given floors, using the same wiring as the example tests.
func evaluateAt(t *testing.T, covFloor, confFloor, cov, conf float64) (orchestrator.RouterDecision, error) {
	t.Helper()
	r := &router.Router{CoverageFloor: covFloor, ConfidenceFloor: confFloor}
	task := &state.Task{ID: "task-prop", ContextSnapshot: testManifest(cov, conf)}
	return r.Evaluate(context.Background(), task, bus.NewRecorderEmitter())
}

// mustEvaluate fails the test on an unexpected error and returns the decision.
func mustEvaluate(t *testing.T, covFloor, confFloor, cov, conf float64) orchestrator.RouterDecision {
	t.Helper()
	d, err := evaluateAt(t, covFloor, confFloor, cov, conf)
	if err != nil {
		t.Fatalf("Evaluate(cov=%v, conf=%v, covFloor=%v, confFloor=%v) error: %v",
			cov, conf, covFloor, confFloor, err)
	}
	return d
}

// sampleUnit draws a value in [0,1], over-sampling the exact endpoints so the
// escape hatch (coverage == 0) and the saturated floors get real coverage.
func sampleUnit(rnd *rand.Rand) float64 {
	switch rnd.Intn(6) {
	case 0:
		return 0
	case 1:
		return 1
	default:
		return rnd.Float64()
	}
}

// sampleAround draws a value in [0,1] biased toward the interesting points for
// a given floor: exactly 0 (escape hatch), exactly the floor (the >= boundary),
// and otherwise a plain unit sample.
func sampleAround(rnd *rand.Rand, floor float64) float64 {
	switch rnd.Intn(5) {
	case 0:
		return 0
	case 1:
		return floor
	default:
		return sampleUnit(rnd)
	}
}

// cascadeCase is a generated (coverage, confidence, floors) tuple. All four
// components are constrained to [0,1]: the Router's documented operating range.
type cascadeCase struct {
	Cov       float64
	Conf      float64
	CovFloor  float64
	ConfFloor float64
}

// Generate implements quick.Generator.
func (cascadeCase) Generate(rnd *rand.Rand, _ int) reflect.Value {
	c := cascadeCase{
		CovFloor:  sampleUnit(rnd),
		ConfFloor: sampleUnit(rnd),
	}
	c.Cov = sampleAround(rnd, c.CovFloor)
	c.Conf = sampleAround(rnd, c.ConfFloor)
	return reflect.ValueOf(c)
}

// TestRouterCascade_Regime asserts the ADR 0005 cascade regime table over
// random (coverage, confidence, covFloor, confFloor) tuples. The four regimes
// are mutually exclusive and exhaustive over [0,1]^4:
//
//	R1 escape hatch : cov == 0 && covFloor > 0            → ESCALATE
//	R2 low coverage : 0 < cov < covFloor                  → EXPAND
//	R3 low conf     : cov >= covFloor && conf < confFloor → ESCALATE
//	R4 sufficient   : cov >= covFloor && conf >= confFloor → PROCEED
//
// Note the precedence: coverage gates first (ADR 0002, ADR 0005 cascade step 1),
// so when coverage AND confidence are both below their floors with cov > 0, the
// decision is EXPAND — not ESCALATE.
func TestRouterCascade_Regime(t *testing.T) {
	// Regime hit counts guard against a vacuous pass: a generator that never
	// produced, say, coverage == 0 would leave the escape hatch untested while
	// still reporting green.
	hits := map[string]int{}

	property := func(c cascadeCase) bool {
		got, err := evaluateAt(t, c.CovFloor, c.ConfFloor, c.Cov, c.Conf)
		if err != nil {
			t.Errorf("unexpected error for %+v: %v", c, err)
			return false
		}

		var want orchestrator.RouterDecision
		var regime string
		switch {
		case c.Cov == 0 && c.CovFloor > 0:
			want, regime = orchestrator.DecisionEscalate, "R1 escape hatch"
		case c.Cov < c.CovFloor:
			want, regime = orchestrator.DecisionExpand, "R2 low coverage"
		case c.Conf < c.ConfFloor:
			want, regime = orchestrator.DecisionEscalate, "R3 low confidence"
		default:
			want, regime = orchestrator.DecisionProceed, "R4 sufficient"
		}

		hits[regime]++
		if regime == "R2 low coverage" && c.Conf < c.ConfFloor {
			hits["R2 both below floors"]++
		}

		if got != want {
			t.Errorf("%s: cov=%v conf=%v covFloor=%v confFloor=%v: got %s, want %s",
				regime, c.Cov, c.Conf, c.CovFloor, c.ConfFloor, got, want)
			return false
		}
		return true
	}

	if err := quick.Check(property, quickConfig(2000)); err != nil {
		t.Fatalf("cascade regime property failed: %v", err)
	}

	// Every branch of the cascade must have been exercised, and the
	// both-floors-violated sub-case of R2 (which is where the coverage-gates-
	// first precedence actually bites) along with it.
	const minHits = 20
	for _, regime := range []string{
		"R1 escape hatch",
		"R2 low coverage",
		"R2 both below floors",
		"R3 low confidence",
		"R4 sufficient",
	} {
		if hits[regime] < minHits {
			t.Errorf("regime %q exercised only %d times (want >= %d); generator is not covering the input space",
				regime, hits[regime], minHits)
		}
	}
}

// TestRouterCascade_ExactlyOneDecision asserts completeness: every well-formed
// input yields exactly one of the three decision constants and never an error.
func TestRouterCascade_ExactlyOneDecision(t *testing.T) {
	property := func(c cascadeCase) bool {
		got, err := evaluateAt(t, c.CovFloor, c.ConfFloor, c.Cov, c.Conf)
		if err != nil {
			t.Errorf("unexpected error for %+v: %v", c, err)
			return false
		}
		switch got {
		case orchestrator.DecisionProceed, orchestrator.DecisionExpand, orchestrator.DecisionEscalate:
			return true
		default:
			t.Errorf("%+v: decision %q is not one of PROCEED/EXPAND/ESCALATE", c, got)
			return false
		}
	}

	if err := quick.Check(property, quickConfig(1000)); err != nil {
		t.Fatalf("exactly-one-decision property failed: %v", err)
	}
}

// sampleSigned draws a floor argument in [-2,2], over-sampling the values that
// matter to NewRouter: the negative sentinel region, exact zero, and one.
func sampleSigned(rnd *rand.Rand) float64 {
	switch rnd.Intn(8) {
	case 0:
		return 0
	case 1:
		return 1
	case 2:
		return -1
	case 3:
		return 2
	default:
		return rnd.Float64()*4 - 2
	}
}

// floorArgs is a generated pair of NewRouter arguments in [-2,2].
type floorArgs struct {
	Coverage   float64
	Confidence float64
}

// Generate implements quick.Generator.
func (floorArgs) Generate(rnd *rand.Rand, _ int) reflect.Value {
	return reflect.ValueOf(floorArgs{Coverage: sampleSigned(rnd), Confidence: sampleSigned(rnd)})
}

// TestNewRouter_Clamping_Property generalizes TestNewRouter_Defaults,
// TestNewRouter_ZeroFloors and TestNewRouter_CustomValues over random arguments
// in [-2,2], pinning NewRouter's documented normalization:
//
//   - a negative argument is replaced by its ADR 0005 starter default
//     (0.60 coverage, 0.50 confidence);
//   - any non-negative argument is preserved verbatim — zero included, which
//     is a deliberate bypass of the corresponding cascade check, not an
//     "unset" sentinel;
//   - the result is always non-negative and repeatable.
//
// NewRouter has no upper clamp: arguments above 1 are preserved. See
// TestNewRouter_AboveOneFloorsSaturate for what those degenerate floors mean at
// evaluation time.
func TestNewRouter_Clamping_Property(t *testing.T) {
	property := func(a floorArgs) bool {
		r := router.NewRouter(a.Coverage, a.Confidence)

		wantCov := a.Coverage
		if wantCov < 0 {
			wantCov = 0.60
		}
		wantConf := a.Confidence
		if wantConf < 0 {
			wantConf = 0.50
		}

		if r.CoverageFloor != wantCov || r.ConfidenceFloor != wantConf {
			t.Errorf("NewRouter(%v, %v) = (%v, %v), want (%v, %v)",
				a.Coverage, a.Confidence, r.CoverageFloor, r.ConfidenceFloor, wantCov, wantConf)
			return false
		}
		if r.CoverageFloor < 0 || r.ConfidenceFloor < 0 {
			t.Errorf("NewRouter(%v, %v) produced a negative floor: (%v, %v)",
				a.Coverage, a.Confidence, r.CoverageFloor, r.ConfidenceFloor)
			return false
		}

		// Repeatability: construction is pure.
		again := router.NewRouter(a.Coverage, a.Confidence)
		if *again != *r {
			t.Errorf("NewRouter(%v, %v) not repeatable: %+v vs %+v",
				a.Coverage, a.Confidence, *again, *r)
			return false
		}
		return true
	}

	if err := quick.Check(property, quickConfig(1000)); err != nil {
		t.Fatalf("NewRouter normalization property failed: %v", err)
	}
}

// TestNewRouter_AboveOneFloorsSaturate documents the evaluation-time meaning of
// the un-clamped above-one floors NewRouter permits: a CoverageFloor > 1 makes
// every non-zero coverage EXPAND (and zero coverage ESCALATE via the escape
// hatch), and a ConfidenceFloor > 1 makes every passing-coverage manifest
// ESCALATE. Degenerate but coherent — and pinned so it cannot drift unnoticed.
func TestNewRouter_AboveOneFloorsSaturate(t *testing.T) {
	property := func(p unitPair) bool {
		r := router.NewRouter(2, 2)

		want := orchestrator.DecisionExpand
		if p.Cov == 0 {
			want = orchestrator.DecisionEscalate
		}
		if got := mustEvaluate(t, r.CoverageFloor, r.ConfidenceFloor, p.Cov, p.Conf); got != want {
			t.Errorf("floors (2,2) with cov=%v conf=%v: got %s, want %s", p.Cov, p.Conf, got, want)
			return false
		}

		// A saturated confidence floor alone escalates whenever coverage passes.
		if got := mustEvaluate(t, 0, 2, p.Cov, p.Conf); got != orchestrator.DecisionEscalate {
			t.Errorf("floors (0,2) with cov=%v conf=%v: got %s, want ESCALATE", p.Cov, p.Conf, got)
			return false
		}
		return true
	}

	if err := quick.Check(property, quickConfig(500)); err != nil {
		t.Fatalf("saturated-floor property failed: %v", err)
	}
}

// TestRouterCascade_BoundaryAnchors ties the property tests back to the example
// tests in router_test.go: the same cardinal points, evaluated through the
// property harness, must still produce the established decisions. If a
// generator or helper here ever drifts, these anchors fail first.
func TestRouterCascade_BoundaryAnchors(t *testing.T) {
	cases := []struct {
		name                string
		covFloor, confFloor float64
		cov, conf           float64
		want                orchestrator.RouterDecision
	}{
		{"proceed/zero floors", 0, 0, 0.5, 0.5, orchestrator.DecisionProceed},
		{"expand/high coverage floor", 0.99, 0, 0.5, 0.5, orchestrator.DecisionExpand},
		{"escalate/zero coverage", 0.1, 0.1, 0, 0.9, orchestrator.DecisionEscalate},
		{"escalate/low confidence", 0.1, 0.99, 0.5, 0.5, orchestrator.DecisionEscalate},
		{"proceed/zero coverage with zero floor", 0, 0, 0, 0, orchestrator.DecisionProceed},

		// The ADR 0005 starter defaults against their own cutpoints.
		{"defaults/at both floors", 0.60, 0.50, 0.60, 0.50, orchestrator.DecisionProceed},
		{"defaults/just under coverage floor", 0.60, 0.50, 0.59, 0.99, orchestrator.DecisionExpand},
		{"defaults/just under confidence floor", 0.60, 0.50, 0.99, 0.49, orchestrator.DecisionEscalate},
		{"defaults/both under, coverage gates first", 0.60, 0.50, 0.59, 0.49, orchestrator.DecisionExpand},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mustEvaluate(t, tc.covFloor, tc.confFloor, tc.cov, tc.conf); got != tc.want {
				t.Fatalf("cov=%v conf=%v covFloor=%v confFloor=%v: got %s, want %s",
					tc.cov, tc.conf, tc.covFloor, tc.confFloor, got, tc.want)
			}
		})
	}
}

// unitPair is a generated (coverage, confidence) manifest pair in [0,1]^2,
// independent of any floor.
type unitPair struct {
	Cov  float64
	Conf float64
}

// Generate implements quick.Generator.
func (unitPair) Generate(rnd *rand.Rand, _ int) reflect.Value {
	return reflect.ValueOf(unitPair{Cov: sampleUnit(rnd), Conf: sampleUnit(rnd)})
}

// TestRouterCascade_ZeroFloorsAlwaysProceed generalizes TestNewRouter_ZeroFloors
// and TestRouter_Evaluate_ProceedOnZeroCoverageWithZeroFloor: with both floors
// at zero the cascade is fully bypassed, so every manifest in [0,1]^2 —
// including coverage == 0, which would otherwise trip the escape hatch —
// PROCEEDs.
func TestRouterCascade_ZeroFloorsAlwaysProceed(t *testing.T) {
	property := func(p unitPair) bool {
		got := mustEvaluate(t, 0, 0, p.Cov, p.Conf)
		if got != orchestrator.DecisionProceed {
			t.Errorf("zero floors with cov=%v conf=%v: got %s, want PROCEED", p.Cov, p.Conf, got)
			return false
		}
		return true
	}

	if err := quick.Check(property, quickConfig(1000)); err != nil {
		t.Fatalf("zero-floor property failed: %v", err)
	}
}

// TestRouterCascade_ZeroCoverageEscalatesWheneverFloorPositive isolates the
// escape hatch (ADR 0005 sub-decision 2): coverage == 0 with any positive
// CoverageFloor escalates immediately regardless of confidence — widening
// cannot conjure query terms the corpus does not contain.
func TestRouterCascade_ZeroCoverageEscalatesWheneverFloorPositive(t *testing.T) {
	property := func(c cascadeCase) bool {
		covFloor := c.CovFloor
		if covFloor <= 0 {
			// Steer the generated case into the regime under test; the
			// covFloor == 0 branch is covered by the zero-floor property.
			covFloor = 0.01
		}
		got := mustEvaluate(t, covFloor, c.ConfFloor, 0, c.Conf)
		if got != orchestrator.DecisionEscalate {
			t.Errorf("cov=0 covFloor=%v confFloor=%v conf=%v: got %s, want ESCALATE",
				covFloor, c.ConfFloor, c.Conf, got)
			return false
		}
		return true
	}

	if err := quick.Check(property, quickConfig(1000)); err != nil {
		t.Fatalf("escape-hatch property failed: %v", err)
	}
}

// severity ranks the decisions by how much they hold execution back:
// PROCEED (0) < EXPAND (1) < ESCALATE (2).
func severity(d orchestrator.RouterDecision) int {
	switch d {
	case orchestrator.DecisionProceed:
		return 0
	case orchestrator.DecisionExpand:
		return 1
	case orchestrator.DecisionEscalate:
		return 2
	default:
		return -1
	}
}

// floorPair is a generated ordered pair of floors, lo <= hi, plus the manifest
// values held fixed while one floor is raised.
type floorPair struct {
	Cov      float64
	Conf     float64
	Lo       float64
	Hi       float64
	OtherFlr float64
}

// Generate implements quick.Generator, emitting lo <= hi so the monotonicity
// direction is well defined without the property having to sort.
func (floorPair) Generate(rnd *rand.Rand, _ int) reflect.Value {
	a, b := sampleUnit(rnd), sampleUnit(rnd)
	if a > b {
		a, b = b, a
	}
	p := floorPair{Lo: a, Hi: b, OtherFlr: sampleUnit(rnd)}
	p.Cov = sampleAround(rnd, a)
	p.Conf = sampleAround(rnd, p.OtherFlr)
	return reflect.ValueOf(p)
}

// TestRouterCascade_MonotonicConfidenceFloor asserts full severity monotonicity
// in ConfidenceFloor: with the manifest and CoverageFloor fixed, raising
// ConfidenceFloor can only move the decision toward ESCALATE, never back toward
// EXPAND or PROCEED. This holds because the confidence check is the last
// cascade step — the escape hatch and the coverage gate ignore ConfidenceFloor.
func TestRouterCascade_MonotonicConfidenceFloor(t *testing.T) {
	property := func(p floorPair) bool {
		covFloor := p.OtherFlr
		low := mustEvaluate(t, covFloor, p.Lo, p.Cov, p.Conf)
		high := mustEvaluate(t, covFloor, p.Hi, p.Cov, p.Conf)
		if severity(high) < severity(low) {
			t.Errorf("confFloor %v→%v (cov=%v conf=%v covFloor=%v): severity dropped %s→%s",
				p.Lo, p.Hi, p.Cov, p.Conf, covFloor, low, high)
			return false
		}
		return true
	}

	if err := quick.Check(property, quickConfig(1000)); err != nil {
		t.Fatalf("confidence-floor monotonicity property failed: %v", err)
	}
}

// TestRouterCascade_MonotonicCoverageFloor asserts the monotonicity that
// actually holds for CoverageFloor: PROCEED is only ever lost as the floor
// rises, never gained. Formally, decision(loFloor) != PROCEED implies
// decision(hiFloor) != PROCEED.
//
// Full severity monotonicity does NOT hold on this axis — see
// TestRouterCascade_CoverageFloorCanDowngradeEscalateToExpand for the pinned
// counterexample and why it is correct per ADR 0002/0005 (coverage gates first).
func TestRouterCascade_MonotonicCoverageFloor(t *testing.T) {
	// The implication is vacuously true when the low-floor decision is already
	// PROCEED; count the cases with a real antecedent.
	var nonTrivial int

	property := func(p floorPair) bool {
		confFloor := p.OtherFlr
		low := mustEvaluate(t, p.Lo, confFloor, p.Cov, p.Conf)
		high := mustEvaluate(t, p.Hi, confFloor, p.Cov, p.Conf)
		if low != orchestrator.DecisionProceed {
			nonTrivial++
		}
		if low != orchestrator.DecisionProceed && high == orchestrator.DecisionProceed {
			t.Errorf("covFloor %v→%v (cov=%v conf=%v confFloor=%v): regained PROCEED from %s",
				p.Lo, p.Hi, p.Cov, p.Conf, confFloor, low)
			return false
		}
		return true
	}

	if err := quick.Check(property, quickConfig(1000)); err != nil {
		t.Fatalf("coverage-floor monotonicity property failed: %v", err)
	}
	if nonTrivial < 100 {
		t.Errorf("only %d non-PROCEED antecedents in 1000 cases; the property is close to vacuous", nonTrivial)
	}
}

// TestRouterCascade_MonotonicJointFloors asserts the same PROCEED-monotonicity
// when both floors are raised together: tightening the Router never turns a
// non-PROCEED decision back into PROCEED.
func TestRouterCascade_MonotonicJointFloors(t *testing.T) {
	property := func(a, b cascadeCase) bool {
		covLo, covHi := a.CovFloor, b.CovFloor
		if covLo > covHi {
			covLo, covHi = covHi, covLo
		}
		confLo, confHi := a.ConfFloor, b.ConfFloor
		if confLo > confHi {
			confLo, confHi = confHi, confLo
		}

		low := mustEvaluate(t, covLo, confLo, a.Cov, a.Conf)
		high := mustEvaluate(t, covHi, confHi, a.Cov, a.Conf)
		if low != orchestrator.DecisionProceed && high == orchestrator.DecisionProceed {
			t.Errorf("floors (%v,%v)→(%v,%v) with cov=%v conf=%v: regained PROCEED from %s",
				covLo, confLo, covHi, confHi, a.Cov, a.Conf, low)
			return false
		}
		return true
	}

	if err := quick.Check(property, quickConfig(1000)); err != nil {
		t.Fatalf("joint-floor monotonicity property failed: %v", err)
	}
}

// TestRouterCascade_CoverageFloorCanDowngradeEscalateToExpand pins the
// counterexample to full severity monotonicity on the CoverageFloor axis.
//
// With cov=0.5, conf=0.1, confFloor=0.5: at covFloor=0 the coverage gate passes
// and the confidence check fires (ESCALATE); at covFloor=0.6 the coverage gate
// fires first (EXPAND). Raising the floor therefore *lowers* severity.
//
// This is intended behavior, not a defect: ADR 0005 cascade step 1 puts the
// coverage gate ahead of the confidence check ("coverage-gates-first", ADR
// 0002), so a coverage floor high enough to catch the manifest routes it to
// widening before its confidence is ever judged. This test exists so that
// ordering cannot be changed silently.
func TestRouterCascade_CoverageFloorCanDowngradeEscalateToExpand(t *testing.T) {
	const (
		cov       = 0.5
		conf      = 0.1
		confFloor = 0.5
	)

	if got := mustEvaluate(t, 0, confFloor, cov, conf); got != orchestrator.DecisionEscalate {
		t.Fatalf("covFloor=0: expected ESCALATE (confidence gate), got %s", got)
	}
	if got := mustEvaluate(t, 0.6, confFloor, cov, conf); got != orchestrator.DecisionExpand {
		t.Fatalf("covFloor=0.6: expected EXPAND (coverage gate wins), got %s", got)
	}
}

// TestRouterCascade_Deterministic asserts the Router is a pure function: the
// same (task, floors) evaluated repeatedly yields the same decision, and the
// emitter carries no state that could perturb it.
func TestRouterCascade_Deterministic(t *testing.T) {
	property := func(c cascadeCase) bool {
		first := mustEvaluate(t, c.CovFloor, c.ConfFloor, c.Cov, c.Conf)
		second := mustEvaluate(t, c.CovFloor, c.ConfFloor, c.Cov, c.Conf)
		if first != second {
			t.Errorf("%+v: non-deterministic: first %s, second %s", c, first, second)
			return false
		}

		// Same Router instance, same task, repeated Evaluate calls with a
		// shared emitter must also agree.
		r := &router.Router{CoverageFloor: c.CovFloor, ConfidenceFloor: c.ConfFloor}
		task := &state.Task{ID: "task-prop", ContextSnapshot: testManifest(c.Cov, c.Conf)}
		rec := bus.NewRecorderEmitter()
		for i := range 3 {
			d, err := r.Evaluate(context.Background(), task, rec)
			if err != nil {
				t.Errorf("%+v: repeat %d error: %v", c, i, err)
				return false
			}
			if d != first {
				t.Errorf("%+v: repeat %d drifted: got %s, want %s", c, i, d, first)
				return false
			}
		}
		return true
	}

	if err := quick.Check(property, quickConfig(500)); err != nil {
		t.Fatalf("determinism property failed: %v", err)
	}
}
