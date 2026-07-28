package acceptance

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// osStat is a thin alias so repoRoot's lookup reads clearly.
func osStat(path string) (fs.FileInfo, error) { return os.Stat(path) }

// StepFunc implements one step definition. args carries the regex capture
// groups of the matched step text, in order. Returning an error fails the
// scenario at that step.
type StepFunc func(args []string) error

// stepDef is one registered pattern/implementation pair.
type stepDef struct {
	pattern *regexp.Regexp
	fn      StepFunc
}

// Suite is a registry of step definitions plus the hooks needed to run
// scenarios against them. The zero value is not usable; call NewSuite.
type Suite struct {
	defs   []stepDef
	before []func()
}

// NewSuite returns an empty Suite.
func NewSuite() *Suite {
	return &Suite{}
}

// Step registers a step definition. The pattern is anchored on both ends, so
// "the router decides PROCEED" cannot accidentally match a longer step. It
// panics on an invalid pattern: a malformed step definition is a defect in the
// test suite itself, not a scenario failure to be reported at run time.
func (s *Suite) Step(pattern string, fn StepFunc) {
	anchored := pattern
	if !strings.HasPrefix(anchored, "^") {
		anchored = "^" + anchored
	}
	if !strings.HasSuffix(anchored, "$") {
		anchored += "$"
	}
	re, err := regexp.Compile(anchored)
	if err != nil {
		panic(fmt.Sprintf("acceptance: invalid step pattern %q: %v", pattern, err))
	}
	s.defs = append(s.defs, stepDef{pattern: re, fn: fn})
}

// BeforeScenario registers a hook run before each scenario's steps, ahead of
// the Background. Scenarios must not share mutable state; this is where each
// one gets a fresh world.
func (s *Suite) BeforeScenario(fn func()) {
	s.before = append(s.before, fn)
}

// ErrUndefinedStep and ErrAmbiguousStep classify the two ways a step can fail
// to bind, as opposed to failing on execution.
var (
	ErrUndefinedStep = fmt.Errorf("undefined step")
	ErrAmbiguousStep = fmt.Errorf("ambiguous step")
)

// match resolves a step to exactly one definition. Zero matches is an
// undefined step; more than one is ambiguous, which would otherwise make
// execution depend on registration order.
func (s *Suite) match(step Step) (stepDef, []string, error) {
	var found []stepDef
	var args []string
	for _, def := range s.defs {
		if m := def.pattern.FindStringSubmatch(step.Text); m != nil {
			found = append(found, def)
			args = m[1:]
		}
	}
	switch len(found) {
	case 0:
		return stepDef{}, nil, fmt.Errorf("%w: %s (line %d)", ErrUndefinedStep, step, step.Line)
	case 1:
		return found[0], args, nil
	default:
		patterns := make([]string, 0, len(found))
		for _, def := range found {
			patterns = append(patterns, def.pattern.String())
		}
		return stepDef{}, nil, fmt.Errorf("%w: %s (line %d) matches %d patterns: %s",
			ErrAmbiguousStep, step, step.Line, len(found), strings.Join(patterns, ", "))
	}
}

// RunScenario executes the background steps followed by the scenario's own
// steps, stopping at the first failure and returning it. It is separate from
// Run so the runner's own behavior (undefined steps, ambiguity, ordering, hook
// invocation) can be unit-tested without a real *testing.T failing.
func (s *Suite) RunScenario(sc Scenario, background []Step) error {
	for _, hook := range s.before {
		hook()
	}
	for _, step := range append(append([]Step(nil), background...), sc.Steps...) {
		def, args, err := s.match(step)
		if err != nil {
			return err
		}
		if err := def.fn(args); err != nil {
			return fmt.Errorf("%s (line %d): %w", step, step.Line, err)
		}
	}
	return nil
}

// Run drives every scenario in the feature as a subtest, so a failure names the
// scenario that broke and the remaining scenarios still run.
func (s *Suite) Run(t *testing.T, f *Feature) {
	t.Helper()
	for _, sc := range f.Scenarios {
		t.Run(sc.Name, func(t *testing.T) {
			if err := s.RunScenario(sc, f.Background); err != nil {
				t.Fatalf("scenario %q failed: %v", sc.Name, err)
			}
		})
	}
}

// FeaturePath resolves a path under the repository's features/ directory,
// independent of the working directory the test is invoked from.
func FeaturePath(name string) (string, error) {
	root, err := repoRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "features", name), nil
}

// repoRoot walks up from this source file to the directory holding go.mod.
// Deriving it from the compiled-in source path keeps feature discovery
// independent of the test's working directory.
func repoRoot() (string, error) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot determine caller path")
	}
	dir := filepath.Dir(thisFile)
	for {
		if _, err := osStat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found above %s", thisFile)
		}
		dir = parent
	}
}
