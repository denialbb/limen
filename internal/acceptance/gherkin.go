// Package acceptance holds the executable behavior contract for the Limen
// orchestration engine: Gherkin feature files under features/, a minimal
// parser and runner for them, and the step definitions that bind each step to
// the same Go-level infrastructure the integration tests drive.
//
// The runner is deliberately small and dependency-free. It covers the subset of
// Gherkin the behavior contract actually uses — Feature, Background, Scenario,
// and Given/When/Then/And/But steps — and rejects constructs it does not
// implement rather than silently mis-parsing them.
package acceptance

import (
	"fmt"
	"os"
	"strings"
)

// Keyword is the resolved step keyword. And/But steps inherit the keyword of
// the step above them, so every parsed step carries a concrete Given, When or
// Then regardless of how it was written.
type Keyword string

const (
	// KeywordGiven marks a precondition step.
	KeywordGiven Keyword = "Given"
	// KeywordWhen marks the action under test.
	KeywordWhen Keyword = "When"
	// KeywordThen marks an assertion step.
	KeywordThen Keyword = "Then"
)

// Step is one Given/When/Then line, with its source line number retained so a
// failure can point back at the feature file rather than at the runner.
type Step struct {
	Keyword Keyword
	Text    string
	Line    int
}

// String renders the step the way it appeared in the feature file.
func (s Step) String() string {
	return string(s.Keyword) + " " + s.Text
}

// Scenario is a named sequence of steps. Background steps are not copied in
// here; the runner prepends them at execution time so a scenario's own step
// list stays faithful to the file.
type Scenario struct {
	Name  string
	Steps []Step
	Line  int
}

// Feature is one parsed .feature file.
type Feature struct {
	Name        string
	Description []string
	Background  []Step
	Scenarios   []Scenario
}

// ParseFeatureFile reads and parses a .feature file from disk.
func ParseFeatureFile(path string) (*Feature, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read feature file: %w", err)
	}
	f, err := ParseFeature(string(raw))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}

// section tracks which block the parser is currently filling.
type section int

const (
	sectionNone section = iota
	sectionFeature
	sectionBackground
	sectionScenario
)

// ParseFeature parses Gherkin source into a Feature.
//
// Recognized: a single "Feature:" line, an optional free-text description, an
// optional "Background:", and any number of "Scenario:" (or "Example:") blocks.
// Blank lines and "#" comments are ignored anywhere. Constructs this runner does
// not implement — Scenario Outline, Examples tables, doc strings, data tables —
// are rejected with an explicit error so they cannot be silently dropped.
func ParseFeature(src string) (*Feature, error) {
	f := &Feature{}
	current := sectionNone
	var lastKeyword Keyword

	for i, raw := range strings.Split(src, "\n") {
		lineNo := i + 1
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		switch {
		case strings.HasPrefix(line, "Feature:"):
			if f.Name != "" {
				return nil, fmt.Errorf("line %d: a second Feature: line (one feature per file)", lineNo)
			}
			f.Name = strings.TrimSpace(strings.TrimPrefix(line, "Feature:"))
			if f.Name == "" {
				return nil, fmt.Errorf("line %d: Feature: has no name", lineNo)
			}
			current = sectionFeature
			lastKeyword = ""

		case strings.HasPrefix(line, "Background:"):
			if f.Name == "" {
				return nil, fmt.Errorf("line %d: Background: before any Feature:", lineNo)
			}
			if f.Background != nil {
				return nil, fmt.Errorf("line %d: a second Background: block", lineNo)
			}
			f.Background = []Step{}
			current = sectionBackground
			lastKeyword = ""

		case strings.HasPrefix(line, "Scenario Outline:"), strings.HasPrefix(line, "Examples:"),
			strings.HasPrefix(line, "Scenario Template:"), strings.HasPrefix(line, "|"),
			strings.HasPrefix(line, `"""`):
			return nil, fmt.Errorf("line %d: %q is not supported by this runner (no outlines, tables or doc strings)", lineNo, firstWord(line))

		case strings.HasPrefix(line, "Scenario:"), strings.HasPrefix(line, "Example:"):
			if f.Name == "" {
				return nil, fmt.Errorf("line %d: Scenario: before any Feature:", lineNo)
			}
			name := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "Scenario:"), "Example:"))
			if name == "" {
				return nil, fmt.Errorf("line %d: Scenario: has no name", lineNo)
			}
			f.Scenarios = append(f.Scenarios, Scenario{Name: name, Line: lineNo})
			current = sectionScenario
			lastKeyword = ""

		default:
			keyword, text, ok := splitStep(line)
			if !ok {
				// Not a step: free text under Feature: is the description;
				// anywhere else it is a malformed line.
				if current == sectionFeature {
					f.Description = append(f.Description, line)
					continue
				}
				return nil, fmt.Errorf("line %d: %q is not a step or a recognized section header", lineNo, line)
			}

			if current != sectionBackground && current != sectionScenario {
				return nil, fmt.Errorf("line %d: step %q outside any Background: or Scenario:", lineNo, line)
			}

			resolved := keyword
			if keyword == "" { // And / But
				if lastKeyword == "" {
					return nil, fmt.Errorf("line %d: %q has no preceding Given/When/Then to inherit from", lineNo, firstWord(line))
				}
				resolved = lastKeyword
			}
			lastKeyword = resolved

			step := Step{Keyword: resolved, Text: text, Line: lineNo}
			if current == sectionBackground {
				f.Background = append(f.Background, step)
			} else {
				last := len(f.Scenarios) - 1
				f.Scenarios[last].Steps = append(f.Scenarios[last].Steps, step)
			}
		}
	}

	if f.Name == "" {
		return nil, fmt.Errorf("no Feature: line found")
	}
	if len(f.Scenarios) == 0 {
		return nil, fmt.Errorf("feature %q defines no scenarios", f.Name)
	}
	for _, sc := range f.Scenarios {
		if len(sc.Steps) == 0 {
			return nil, fmt.Errorf("line %d: scenario %q has no steps", sc.Line, sc.Name)
		}
	}
	return f, nil
}

// splitStep splits a step line into its keyword and remaining text. An And/But
// line returns an empty keyword, signalling the caller to inherit. The bool
// reports whether the line was a step at all.
func splitStep(line string) (Keyword, string, bool) {
	for _, k := range []Keyword{KeywordGiven, KeywordWhen, KeywordThen} {
		if rest, ok := strings.CutPrefix(line, string(k)+" "); ok {
			return k, strings.TrimSpace(rest), true
		}
	}
	for _, cont := range []string{"And ", "But ", "* "} {
		if rest, ok := strings.CutPrefix(line, cont); ok {
			return "", strings.TrimSpace(rest), true
		}
	}
	return "", "", false
}

// firstWord returns the leading word of a line, for error messages.
func firstWord(line string) string {
	if i := strings.IndexAny(line, " \t"); i > 0 {
		return line[:i]
	}
	return line
}
