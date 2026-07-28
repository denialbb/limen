package integration_test

// Cross-layer agreement tests (CDD "High Semantic Stability & Cross-Layer
// Agreement" pillar).
//
// The task state machine is described in four places that can drift apart:
//
//	1. .agents/docs/state_machine.md   — the Mermaid graph + State Definitions
//	2. internal/state/state.go         — the TaskState constants and
//	                                     IsValidTransition, the executable
//	                                     encoding of the graph
//	3. features/task_lifecycle.feature — the Gherkin acceptance scenarios
//	4. internal/bus/bus.go             — the RouterDecision constants
//
// These tests parse the prose sources and compare them against the live Go
// definitions. They are read-only: nothing here imports the orchestrator's
// internals or mutates state. A disagreement fails loudly and names the layers
// that disagree — it is never skipped.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/denialbb/limen/internal/bus"
	"github.com/denialbb/limen/internal/state"
)

// transition is a directed edge of the state machine.
type transition struct {
	From string
	To   string
}

func (tr transition) String() string { return tr.From + " -> " + tr.To }

// mermaidPseudoState marks the Mermaid diagram's start/end node, which is not a
// real task state.
const mermaidPseudoState = "[*]"

// readRepoFile reads a path relative to the module root. repoRoot is the
// helper already defined in integration_test.go (same package).
func readRepoFile(t *testing.T, rel string) string {
	t.Helper()

	full := filepath.Join(repoRoot(t), rel)
	raw, err := os.ReadFile(full) //nolint:gosec // fixed in-repo path, test-only
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(raw)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// missingFrom returns the members of want that are absent from got.
func missingFrom(want, got map[string]bool) []string {
	var out []string
	for k := range want {
		if !got[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// Source 1: the Markdown specification
// ---------------------------------------------------------------------------

var (
	// "    CREATED --> CONTEXT_BUILDING : expand"
	mermaidEdgeRe = regexp.MustCompile(`^\s*(\[\*\]|[A-Z_]+)\s*-->\s*(\[\*\]|[A-Z_]+)\s*(?::.*)?$`)
	// "- **`CREATED`**: The task is initialized..."
	stateDefRe = regexp.MustCompile("^-\\s+\\*\\*`([A-Z_]+)`\\*\\*:")
)

// specStateMachine is the state machine as described by state_machine.md.
type specStateMachine struct {
	// DiagramStates are the states appearing in the Mermaid graph.
	DiagramStates map[string]bool
	// DefinedStates are the states given a "## State Definitions" entry.
	DefinedStates map[string]bool
	// Transitions are the real edges, with the [*] pseudo-state removed.
	Transitions map[transition]bool
	// Terminals are the states with an edge to [*].
	Terminals map[string]bool
	// Initial are the states reached from [*].
	Initial map[string]bool
}

// parseSpec extracts the state machine from the Markdown spec.
func parseSpec(t *testing.T) specStateMachine {
	t.Helper()

	src := readRepoFile(t, ".agents/docs/state_machine.md")

	spec := specStateMachine{
		DiagramStates: map[string]bool{},
		DefinedStates: map[string]bool{},
		Transitions:   map[transition]bool{},
		Terminals:     map[string]bool{},
		Initial:       map[string]bool{},
	}

	inMermaid := false
	for line := range strings.SplitSeq(src, "\n") {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "```") {
			// Toggle on the fence that opens the mermaid block and off on its close.
			if !inMermaid && strings.Contains(trimmed, "mermaid") {
				inMermaid = true
			} else if inMermaid {
				inMermaid = false
			}
			continue
		}

		if inMermaid {
			m := mermaidEdgeRe.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			from, to := m[1], m[2]

			switch {
			case from == mermaidPseudoState && to == mermaidPseudoState:
				// Meaningless; ignore.
			case from == mermaidPseudoState:
				spec.Initial[to] = true
				spec.DiagramStates[to] = true
			case to == mermaidPseudoState:
				spec.Terminals[from] = true
				spec.DiagramStates[from] = true
			default:
				spec.DiagramStates[from] = true
				spec.DiagramStates[to] = true
				spec.Transitions[transition{From: from, To: to}] = true
			}
			continue
		}

		if m := stateDefRe.FindStringSubmatch(trimmed); m != nil {
			spec.DefinedStates[m[1]] = true
		}
	}

	if len(spec.DiagramStates) == 0 || len(spec.Transitions) == 0 {
		t.Fatalf("parsed no states/transitions from state_machine.md; the parser and the document have diverged")
	}
	return spec
}

// ---------------------------------------------------------------------------
// Source 2: the Go constants
// ---------------------------------------------------------------------------

// parseGoStringConsts returns the string values of every constant declared with
// the given named type in a Go source file, keyed by value, with the Go
// identifier as the map value.
func parseGoStringConsts(t *testing.T, rel, typeName string) map[string]string {
	t.Helper()

	full := filepath.Join(repoRoot(t), rel)
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, full, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", rel, err)
	}

	out := map[string]string{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}

		// Within one const block the type may be stated once and carried
		// forward by later specs.
		current := ""
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			if ident, ok := vs.Type.(*ast.Ident); ok {
				current = ident.Name
			}
			if current != typeName || len(vs.Values) == 0 || len(vs.Names) == 0 {
				continue
			}
			lit, ok := vs.Values[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			value, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Fatalf("unquote %s in %s: %v", lit.Value, rel, err)
			}
			out[value] = vs.Names[0].Name
		}
	}

	if len(out) == 0 {
		t.Fatalf("found no %s constants in %s; the parser and the source have diverged", typeName, rel)
	}
	return out
}

// goStates returns the TaskState constant values declared in state.go.
func goStates(t *testing.T) map[string]bool {
	t.Helper()

	consts := parseGoStringConsts(t, "internal/state/state.go", "TaskState")
	out := make(map[string]bool, len(consts))
	for value := range consts {
		out[value] = true
	}
	return out
}

// goTransitions derives the transition relation from the live
// state.IsValidTransition over every ordered pair of known states. This is the
// executable encoding of the graph, not a re-listing of it.
func goTransitions(t *testing.T, states map[string]bool) map[transition]bool {
	t.Helper()

	names := sortedKeys(states)
	out := map[transition]bool{}
	for _, from := range names {
		for _, to := range names {
			if state.IsValidTransition(state.TaskState(from), state.TaskState(to)) {
				out[transition{From: from, To: to}] = true
			}
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Source 3: the Gherkin scenarios
// ---------------------------------------------------------------------------

var (
	scenarioRe = regexp.MustCompile(`^\s*Scenario(?: Outline)?:\s*(.+)$`)
	// "Then it reaches WORKER_RUNNING", "And it returns to CONTEXT_BUILDING",
	// "And the task ends in COMMITTED".
	stateStepRe = regexp.MustCompile(`\b(?:it reaches|it returns to|the task ends in)\s+([A-Z_]+)`)
	// "And the router decided PROCEED", "Given the router will decide EXPAND".
	decisionStepRe = regexp.MustCompile(`\bthe router (?:decided|will decide)\s+([A-Z]+)`)
)

// featureScenario is one scenario's ordered walk through the state machine.
type featureScenario struct {
	Name   string
	States []string
}

// parseFeature extracts each scenario's state sequence and the router decisions
// named anywhere in the file.
//
// A scenario asserts a *subsequence* of the states the engine visits — several
// scenarios jump straight to "it reaches WORKER_RUNNING" without restating the
// earlier states. Consecutive pairs are therefore evidence of a transition only
// when the pair is a real edge; non-adjacent pairs are gaps, not violations.
func parseFeature(t *testing.T) ([]featureScenario, map[string]bool) {
	t.Helper()

	src := readRepoFile(t, "features/task_lifecycle.feature")

	var (
		scenarios []featureScenario
		current   *featureScenario
	)
	decisions := map[string]bool{}

	flush := func() {
		if current != nil {
			scenarios = append(scenarios, *current)
			current = nil
		}
	}

	for line := range strings.SplitSeq(src, "\n") {
		if m := scenarioRe.FindStringSubmatch(line); m != nil {
			flush()
			// Every scenario starts from a freshly created task.
			current = &featureScenario{
				Name:   strings.TrimSpace(m[1]),
				States: []string{string(state.StateCreated)},
			}
			continue
		}

		if m := decisionStepRe.FindStringSubmatch(line); m != nil {
			decisions[m[1]] = true
		}

		if current == nil {
			continue
		}
		if m := stateStepRe.FindStringSubmatch(line); m != nil {
			next := m[1]
			// Collapse repeats ("it reaches COMMITTED" followed by "the task
			// ends in COMMITTED") so they do not look like self-edges.
			if len(current.States) == 0 || current.States[len(current.States)-1] != next {
				current.States = append(current.States, next)
			}
		}
	}
	flush()

	if len(scenarios) == 0 {
		t.Fatalf("parsed no scenarios from features/task_lifecycle.feature; the parser and the file have diverged")
	}
	return scenarios, decisions
}

// ---------------------------------------------------------------------------
// The agreement tests
// ---------------------------------------------------------------------------

// TestStateAgreement_StateEnumerationMatches asserts the spec's state list and
// the Go TaskState constants are the same set, in both directions, and that the
// spec's own two listings (Mermaid graph and State Definitions) agree.
func TestStateAgreement_StateEnumerationMatches(t *testing.T) {
	spec := parseSpec(t)
	goSet := goStates(t)

	if missing := missingFrom(spec.DiagramStates, spec.DefinedStates); len(missing) > 0 {
		t.Errorf("state_machine.md internal disagreement: states in the Mermaid graph with no ## State Definitions entry: %v", missing)
	}
	if missing := missingFrom(spec.DefinedStates, spec.DiagramStates); len(missing) > 0 {
		t.Errorf("state_machine.md internal disagreement: states defined under ## State Definitions but absent from the Mermaid graph: %v", missing)
	}

	if missing := missingFrom(spec.DefinedStates, goSet); len(missing) > 0 {
		t.Errorf("cross-layer gap: state_machine.md defines %v, which have no TaskState constant in internal/state/state.go", missing)
	}
	if missing := missingFrom(goSet, spec.DefinedStates); len(missing) > 0 {
		t.Errorf("cross-layer gap: internal/state/state.go declares %v, which state_machine.md never defines", missing)
	}

	t.Logf("spec states: %v", sortedKeys(spec.DefinedStates))
	t.Logf("Go states:   %v", sortedKeys(goSet))
}

// TestStateAgreement_TransitionsMatch asserts the Mermaid graph's edges and the
// relation encoded by state.IsValidTransition are the same set. A transition
// the code permits but the spec omits is an undocumented path; one the spec
// promises but the code rejects is an unimplemented path. Both fail.
func TestStateAgreement_TransitionsMatch(t *testing.T) {
	spec := parseSpec(t)
	goSet := goStates(t)
	goTrans := goTransitions(t, goSet)

	specOnly := make([]string, 0)
	for tr := range spec.Transitions {
		if !goTrans[tr] {
			specOnly = append(specOnly, tr.String())
		}
	}
	sort.Strings(specOnly)

	goOnly := make([]string, 0)
	for tr := range goTrans {
		if !spec.Transitions[tr] {
			goOnly = append(goOnly, tr.String())
		}
	}
	sort.Strings(goOnly)

	if len(specOnly) > 0 {
		t.Errorf("cross-layer gap: state_machine.md draws these edges but state.IsValidTransition rejects them: %v", specOnly)
	}
	if len(goOnly) > 0 {
		t.Errorf("cross-layer gap: state.IsValidTransition permits these edges but state_machine.md does not draw them: %v", goOnly)
	}

	t.Logf("%d transitions agree across spec and code", len(spec.Transitions)-len(specOnly))
}

// TestStateAgreement_TerminalStatesMatch asserts the spec's terminal states
// (those with an edge to [*]) are exactly the states from which
// state.IsValidTransition permits no onward move, and that every scenario ends
// in one of them.
func TestStateAgreement_TerminalStatesMatch(t *testing.T) {
	spec := parseSpec(t)
	goSet := goStates(t)
	goTrans := goTransitions(t, goSet)

	goTerminals := map[string]bool{}
	for name := range goSet {
		outgoing := 0
		for tr := range goTrans {
			if tr.From == name {
				outgoing++
			}
		}
		if outgoing == 0 {
			goTerminals[name] = true
		}
	}

	if missing := missingFrom(spec.Terminals, goTerminals); len(missing) > 0 {
		t.Errorf("cross-layer gap: state_machine.md marks %v terminal, but state.IsValidTransition still permits moves out of them", missing)
	}
	if missing := missingFrom(goTerminals, spec.Terminals); len(missing) > 0 {
		t.Errorf("cross-layer gap: state.IsValidTransition permits no exit from %v, but state_machine.md does not mark them terminal", missing)
	}

	// The documented terminal pair, asserted explicitly so a spec edit that
	// silently drops one is caught.
	for _, want := range []string{string(state.StateCommitted), string(state.StateFailedEscalated)} {
		if !spec.Terminals[want] {
			t.Errorf("state_machine.md no longer marks %s as terminal", want)
		}
		if !goTerminals[want] {
			t.Errorf("state.IsValidTransition no longer treats %s as terminal", want)
		}
	}

	scenarios, _ := parseFeature(t)
	for _, sc := range scenarios {
		if len(sc.States) == 0 {
			continue
		}
		last := sc.States[len(sc.States)-1]
		if !goTerminals[last] {
			t.Errorf("scenario %q ends in %s, which is not a terminal state", sc.Name, last)
		}
	}

	t.Logf("terminal states: %v", sortedKeys(goTerminals))
}

// TestStateAgreement_RouterDecisionsMatch asserts the three RouterDecision
// constants in bus.go, the decisions named in the acceptance scenarios, and the
// decisions described in the spec are the same set. The spec writes them in
// lower case prose, so the comparison is case-insensitive.
func TestStateAgreement_RouterDecisionsMatch(t *testing.T) {
	busConsts := parseGoStringConsts(t, "internal/bus/bus.go", "RouterDecision")
	busSet := map[string]bool{}
	for value := range busConsts {
		busSet[value] = true
	}

	// Validate the AST parse against the live constants: if these disagree the
	// parser is wrong, not the repo.
	for _, want := range []bus.RouterDecision{bus.DecisionProceed, bus.DecisionExpand, bus.DecisionEscalate} {
		if !busSet[string(want)] {
			t.Fatalf("parser did not recover %s from bus.go; parsed set was %v", want, sortedKeys(busSet))
		}
	}

	_, featureSet := parseFeature(t)

	specSrc := readRepoFile(t, ".agents/docs/state_machine.md")
	specLower := strings.ToLower(specSrc)
	specSet := map[string]bool{}
	for value := range busSet {
		if strings.Contains(specLower, strings.ToLower(value)) {
			specSet[value] = true
		}
	}

	if missing := missingFrom(busSet, featureSet); len(missing) > 0 {
		t.Errorf("cross-layer gap: bus.go declares router decisions %v that no scenario in features/task_lifecycle.feature names", missing)
	}
	if missing := missingFrom(featureSet, busSet); len(missing) > 0 {
		t.Errorf("cross-layer gap: features/task_lifecycle.feature names router decisions %v that bus.go does not declare", missing)
	}
	if missing := missingFrom(busSet, specSet); len(missing) > 0 {
		t.Errorf("cross-layer gap: bus.go declares router decisions %v that .agents/docs/state_machine.md never mentions", missing)
	}

	t.Logf("router decisions: bus=%v feature=%v spec=%v",
		sortedKeys(busSet), sortedKeys(featureSet), sortedKeys(specSet))
}

// exemptedTransitions are state-machine edges knowingly left without an
// acceptance scenario. Each entry records why, so an exemption is a decision on
// the record rather than a silent hole in the coverage guarantee.
//
// Keep this map as close to empty as the truth allows.
var exemptedTransitions = map[transition]string{
	{From: "REVISION_REQUESTED", To: "FAILED_ESCALATED"}: "" +
		"Unreachable in production today, so no scenario can walk it. Both retry paths in " +
		"internal/orchestrator/orchestrator.go (~line 334 and ~line 446) test RetryCount >= MaxRetries " +
		"while the task is still in AWAITING_VALIDATION and escalate straight from there; " +
		"REVISION_REQUESTED is entered only when a retry remains, and is followed immediately by " +
		"WORKER_RUNNING. That behavior matches spec invariant 5 (Fatal Validation Shortcut), but the " +
		"spec's graph still labels this edge \"Retry count == Max\", and state.IsValidTransition still " +
		"permits it.\n" +
		"\t\tTODO: tighten. Per .agents/docs/state_machine.md (State Graph + invariants 3 and 5), decide " +
		"whether retry exhaustion should escalate from REVISION_REQUESTED (then make the orchestrator " +
		"walk it and add a scenario) or from AWAITING_VALIDATION (then drop this edge from both the " +
		"spec graph and state.IsValidTransition). Removing this exemption is the definition of done.",
}

// TestStateAgreement_TransitionsCoveredByScenarios asserts every edge of the
// state machine is exercised by at least one Gherkin scenario, except those
// listed in exemptedTransitions.
//
// Scenarios assert a subsequence of the visited states, so only consecutive
// pairs that are real edges count as coverage; a pair that skips intermediate
// states is ignored rather than treated as an illegal move.
func TestStateAgreement_TransitionsCoveredByScenarios(t *testing.T) {
	spec := parseSpec(t)
	goSet := goStates(t)
	scenarios, _ := parseFeature(t)

	covered := map[transition]bool{}
	coveredBy := map[transition][]string{}

	for _, sc := range scenarios {
		for _, name := range sc.States {
			if !goSet[name] {
				t.Errorf("scenario %q names state %s, which is not a TaskState constant", sc.Name, name)
			}
		}
		for i := 0; i+1 < len(sc.States); i++ {
			tr := transition{From: sc.States[i], To: sc.States[i+1]}
			if !spec.Transitions[tr] {
				// A skipped-over span, not an assertion about adjacency.
				continue
			}
			covered[tr] = true
			coveredBy[tr] = append(coveredBy[tr], sc.Name)
		}
	}

	// An exemption that no longer names a real edge is worse than no exemption:
	// it silently stops guarding anything. Fail if one has gone stale.
	for tr, reason := range exemptedTransitions {
		if !spec.Transitions[tr] {
			t.Errorf("stale exemption: %s is exempted but is not an edge in "+
				".agents/docs/state_machine.md; delete the exemption or fix the state names", tr)
		}
		if reason == "" {
			t.Errorf("exemption for %s has no recorded reason", tr)
		}
		if covered[tr] {
			t.Errorf("stale exemption: %s IS now exercised by a scenario (%s); "+
				"delete it from exemptedTransitions so the edge stays guarded",
				tr, strings.Join(dedupe(coveredBy[tr]), ", "))
		}
	}

	var uncovered []string
	for tr := range spec.Transitions {
		if !covered[tr] && exemptedTransitions[tr] == "" {
			uncovered = append(uncovered, tr.String())
		}
	}
	sort.Strings(uncovered)

	for _, tr := range sortedTransitions(spec.Transitions) {
		switch {
		case covered[tr]:
			t.Logf("covered   %-46s by %s", tr.String(), strings.Join(dedupe(coveredBy[tr]), ", "))
		case exemptedTransitions[tr] != "":
			t.Logf("EXEMPT    %-46s %s", tr.String(), exemptedTransitions[tr])
		default:
			t.Logf("UNCOVERED %s", tr.String())
		}
	}

	if len(uncovered) > 0 {
		t.Errorf("cross-layer gap: %d of %d state-machine transitions are not exercised by any scenario in "+
			"features/task_lifecycle.feature: %v\n"+
			"Every edge in .agents/docs/state_machine.md should have an acceptance scenario that walks it.\n"+
			"Either add a scenario, remove the edge from the spec and state.IsValidTransition, or record\n"+
			"a documented entry in exemptedTransitions above.",
			len(uncovered), len(spec.Transitions), uncovered)
	}
}

// sortedTransitions returns the transitions in a stable order for logging.
func sortedTransitions(set map[transition]bool) []transition {
	out := make([]transition, 0, len(set))
	for tr := range set {
		out = append(out, tr)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// dedupe removes repeated names while preserving order.
func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
