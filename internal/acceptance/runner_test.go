package acceptance

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// The tests in this file cover the Gherkin runner itself — the harness that
// every behavior scenario depends on. A silently broken runner (skipping steps,
// binding the wrong definition, ignoring the Background) would make the feature
// file look green while asserting nothing, so the harness is tested before it
// is trusted.

const sampleFeature = `# a leading comment
Feature: Sample
  Free text describing the feature.

  Background:
    Given a background step

  Scenario: First
    When something happens
    Then an outcome follows
    And another outcome follows

  Scenario: Second
    Given a precondition
    But a caveat applies
`

func TestParseFeature_StructureAndSections(t *testing.T) {
	f, err := ParseFeature(sampleFeature)
	if err != nil {
		t.Fatalf("ParseFeature: %v", err)
	}
	if f.Name != "Sample" {
		t.Errorf("Name = %q, want %q", f.Name, "Sample")
	}
	if len(f.Description) != 1 || !strings.HasPrefix(f.Description[0], "Free text") {
		t.Errorf("Description = %#v, want the single free-text line", f.Description)
	}
	if len(f.Background) != 1 || f.Background[0].Text != "a background step" {
		t.Errorf("Background = %#v, want one step", f.Background)
	}
	if len(f.Scenarios) != 2 {
		t.Fatalf("got %d scenarios, want 2", len(f.Scenarios))
	}
	if f.Scenarios[0].Name != "First" || f.Scenarios[1].Name != "Second" {
		t.Errorf("scenario names = %q, %q", f.Scenarios[0].Name, f.Scenarios[1].Name)
	}
	if n := len(f.Scenarios[0].Steps); n != 3 {
		t.Errorf("first scenario has %d steps, want 3", n)
	}
}

// TestParseFeature_AndButInheritKeyword pins the one piece of Gherkin
// semantics that is easy to get wrong: And/But are not their own keywords, they
// continue whichever of Given/When/Then came before.
func TestParseFeature_AndButInheritKeyword(t *testing.T) {
	f, err := ParseFeature(sampleFeature)
	if err != nil {
		t.Fatalf("ParseFeature: %v", err)
	}
	first := f.Scenarios[0].Steps
	if first[0].Keyword != KeywordWhen {
		t.Errorf("step 0 keyword = %q, want When", first[0].Keyword)
	}
	if first[1].Keyword != KeywordThen {
		t.Errorf("step 1 keyword = %q, want Then", first[1].Keyword)
	}
	if first[2].Keyword != KeywordThen {
		t.Errorf("And after Then = %q, want Then (inherited)", first[2].Keyword)
	}
	second := f.Scenarios[1].Steps
	if second[1].Keyword != KeywordGiven {
		t.Errorf("But after Given = %q, want Given (inherited)", second[1].Keyword)
	}
}

// TestParseFeature_RecordsLineNumbers asserts steps carry their source line, so
// a failing scenario points at the feature file rather than at the harness.
func TestParseFeature_RecordsLineNumbers(t *testing.T) {
	f, err := ParseFeature(sampleFeature)
	if err != nil {
		t.Fatalf("ParseFeature: %v", err)
	}
	step := f.Scenarios[0].Steps[0]
	if step.Line == 0 {
		t.Fatalf("step %q recorded no line number", step.Text)
	}
	lines := strings.Split(sampleFeature, "\n")
	if got := strings.TrimSpace(lines[step.Line-1]); got != "When something happens" {
		t.Errorf("line %d is %q, want the step's own source line", step.Line, got)
	}
}

func TestParseFeature_Errors(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"no feature line", "Scenario: orphan\n  Given a step\n", "before any Feature"},
		{"empty source", "", "no Feature: line"},
		{"feature without scenarios", "Feature: Bare\n", "defines no scenarios"},
		{"scenario without steps", "Feature: F\n  Scenario: Empty\n", "has no steps"},
		{"step outside any block", "Feature: F\n  Given a step\n  Scenario: S\n    Given ok\n", "outside any Background: or Scenario:"},
		{"unrecognized text inside a scenario", "Feature: F\n  Scenario: S\n    Given ok\n    loose prose here\n", "not a step or a recognized section"},
		{"and with nothing to inherit", "Feature: F\n  Scenario: S\n    And orphaned\n", "no preceding Given/When/Then"},
		{"unnamed scenario", "Feature: F\n  Scenario:\n    Given a step\n", "has no name"},
		{"duplicate feature", "Feature: A\n  Scenario: S\n    Given x\nFeature: B\n", "second Feature"},
		{"scenario outline rejected", "Feature: F\n  Scenario Outline: S\n    Given x\n", "not supported"},
		{"data table rejected", "Feature: F\n  Scenario: S\n    Given x\n    | a | b |\n", "not supported"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseFeature(tc.src)
			if err == nil {
				t.Fatalf("ParseFeature(%q) succeeded, want an error", tc.src)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

// TestSuite_RunsBackgroundThenScenarioStepsInOrder asserts the execution
// contract the whole behavior suite rests on: background first, then the
// scenario's own steps, each exactly once, in file order.
func TestSuite_RunsBackgroundThenScenarioStepsInOrder(t *testing.T) {
	f, err := ParseFeature(sampleFeature)
	if err != nil {
		t.Fatalf("ParseFeature: %v", err)
	}
	var executed []string
	s := NewSuite()
	for _, pattern := range []string{
		"a background step", "something happens", "an outcome follows",
		"another outcome follows",
	} {
		s.Step(pattern, func(args []string) error {
			executed = append(executed, pattern)
			return nil
		})
	}
	if err := s.RunScenario(f.Scenarios[0], f.Background); err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	want := []string{"a background step", "something happens", "an outcome follows", "another outcome follows"}
	if strings.Join(executed, "|") != strings.Join(want, "|") {
		t.Fatalf("execution order = %v, want %v", executed, want)
	}
}

// TestSuite_BeforeScenarioHooksRunFirst asserts hooks fire ahead of the
// Background, which is what lets each scenario start from a fresh world.
func TestSuite_BeforeScenarioHooksRunFirst(t *testing.T) {
	var order []string
	s := NewSuite()
	s.BeforeScenario(func() { order = append(order, "hook") })
	s.Step("a background step", func(args []string) error {
		order = append(order, "background")
		return nil
	})
	s.Step("a scenario step", func(args []string) error {
		order = append(order, "scenario")
		return nil
	})

	background := []Step{{Keyword: KeywordGiven, Text: "a background step", Line: 1}}
	sc := Scenario{Name: "hooks", Steps: []Step{{Keyword: KeywordWhen, Text: "a scenario step", Line: 2}}}
	if err := s.RunScenario(sc, background); err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	want := []string{"hook", "background", "scenario"}
	if strings.Join(order, "|") != strings.Join(want, "|") {
		t.Fatalf("order = %v, want %v (hook before background)", order, want)
	}
}

// TestSuite_UndefinedStepIsAnError asserts an unbound step fails loudly. This
// is the property that makes the feature file a contract: adding a scenario
// line with no implementation cannot quietly pass.
func TestSuite_UndefinedStepIsAnError(t *testing.T) {
	s := NewSuite()
	s.Step("a known step", func(args []string) error { return nil })
	sc := Scenario{Name: "x", Steps: []Step{
		{Keyword: KeywordGiven, Text: "a known step", Line: 3},
		{Keyword: KeywordThen, Text: "a step nobody implemented", Line: 4},
	}}
	err := s.RunScenario(sc, nil)
	if err == nil {
		t.Fatal("RunScenario succeeded, want an undefined-step error")
	}
	if !errors.Is(err, ErrUndefinedStep) {
		t.Fatalf("error = %v, want ErrUndefinedStep", err)
	}
	if !strings.Contains(err.Error(), "line 4") {
		t.Errorf("error %q should name the offending line", err)
	}
}

// TestSuite_AmbiguousStepIsAnError asserts two definitions matching one step is
// rejected rather than resolved by registration order, which would make
// behavior depend on the order step definitions happen to be written in.
func TestSuite_AmbiguousStepIsAnError(t *testing.T) {
	s := NewSuite()
	s.Step("the router decides .*", func(args []string) error { return nil })
	s.Step(".* decides PROCEED", func(args []string) error { return nil })
	sc := Scenario{Name: "x", Steps: []Step{
		{Keyword: KeywordThen, Text: "the router decides PROCEED", Line: 7},
	}}
	err := s.RunScenario(sc, nil)
	if err == nil {
		t.Fatal("RunScenario succeeded, want an ambiguous-step error")
	}
	if !errors.Is(err, ErrAmbiguousStep) {
		t.Fatalf("error = %v, want ErrAmbiguousStep", err)
	}
}

// TestSuite_PatternsAreAnchored asserts a definition cannot match a longer step
// that merely contains it — "it reaches APPROVED" must not satisfy
// "it reaches APPROVED and then some".
func TestSuite_PatternsAreAnchored(t *testing.T) {
	s := NewSuite()
	s.Step("it reaches APPROVED", func(args []string) error { return nil })
	sc := Scenario{Name: "x", Steps: []Step{
		{Keyword: KeywordThen, Text: "it reaches APPROVED eventually", Line: 2},
	}}
	if err := s.RunScenario(sc, nil); !errors.Is(err, ErrUndefinedStep) {
		t.Fatalf("error = %v, want the longer step to stay unbound", err)
	}
}

// TestSuite_CaptureGroupsArePassedAsArgs asserts regex captures reach the step
// implementation in order, which is how one definition serves every state name.
func TestSuite_CaptureGroupsArePassedAsArgs(t *testing.T) {
	var got []string
	s := NewSuite()
	s.Step(`it reaches ([A-Z_]+) after (\d+) retries`, func(args []string) error {
		got = args
		return nil
	})
	sc := Scenario{Name: "x", Steps: []Step{
		{Keyword: KeywordThen, Text: "it reaches WORKER_RUNNING after 2 retries", Line: 1},
	}}
	if err := s.RunScenario(sc, nil); err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	if len(got) != 2 || got[0] != "WORKER_RUNNING" || got[1] != "2" {
		t.Fatalf("captures = %#v, want [WORKER_RUNNING 2]", got)
	}
}

// TestSuite_StepErrorStopsTheScenario asserts a failing step aborts the
// scenario rather than letting later assertions run against a broken world, and
// that the reported error names the step.
func TestSuite_StepErrorStopsTheScenario(t *testing.T) {
	ran := 0
	s := NewSuite()
	s.Step("the failing step", func(args []string) error {
		ran++
		return fmt.Errorf("boom")
	})
	s.Step("the later step", func(args []string) error {
		ran++
		return nil
	})
	sc := Scenario{Name: "x", Steps: []Step{
		{Keyword: KeywordWhen, Text: "the failing step", Line: 5},
		{Keyword: KeywordThen, Text: "the later step", Line: 6},
	}}
	err := s.RunScenario(sc, nil)
	if err == nil {
		t.Fatal("RunScenario succeeded, want the step error")
	}
	if !strings.Contains(err.Error(), "boom") || !strings.Contains(err.Error(), "the failing step") {
		t.Errorf("error = %q, want it to name the step and its cause", err)
	}
	if ran != 1 {
		t.Errorf("%d steps ran, want execution to stop at the failure", ran)
	}
}

// TestSuite_InvalidPatternPanics asserts a malformed step pattern is treated as
// a defect in the suite rather than a scenario failure.
func TestSuite_InvalidPatternPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("registering an invalid pattern did not panic")
		}
	}()
	NewSuite().Step("([unclosed", func(args []string) error { return nil })
}

// TestFeaturePath_ResolvesFromSourceLocation asserts feature discovery does not
// depend on the working directory the test was invoked from.
func TestFeaturePath_ResolvesFromSourceLocation(t *testing.T) {
	path, err := FeaturePath("task_lifecycle.feature")
	if err != nil {
		t.Fatalf("FeaturePath: %v", err)
	}
	if !strings.HasSuffix(path, "features/task_lifecycle.feature") {
		t.Errorf("FeaturePath = %q, want it under features/", path)
	}
	if !filepath.IsAbs(path) {
		t.Errorf("FeaturePath = %q, want an absolute path", path)
	}
	// Readability of the real feature file is asserted by the behavior suite
	// itself, which parses it.
}
