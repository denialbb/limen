package tabs

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/bubbles/viewport"

	"github.com/denialbb/limen/internal/bus"
)

// ValidatorTab renders Validator activity: the criteria being examined, the
// per-criterion pass/fail results, and the overall verdict with feedback.
type ValidatorTab struct {
	viewport viewport.Model
	lines    []string
}

// NewValidatorTab constructs an empty ValidatorTab with a default 1x1 footprint.
func NewValidatorTab() *ValidatorTab {
	v := &ValidatorTab{}
	v.viewport = viewport.New(1, 1)
	return v
}

// SetSize resizes the Validator viewport.
func (v *ValidatorTab) SetSize(width, height int) {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	v.viewport.Width = width
	v.viewport.Height = height
}

// Update ingests either an EventMsg carrying a validator-relevant bus event or
// a tea.KeyMsg for scroll.
func (v *ValidatorTab) Update(msg tea.Msg) {
	switch m := msg.(type) {
	case EventMsg:
		v.handleEvent(m.Event)
	case tea.KeyMsg:
		v.viewport, _ = v.viewport.Update(m)
	}
}

// handleEvent formats and appends a validator-relevant event line.
func (v *ValidatorTab) handleEvent(ev bus.Event) {
	switch e := ev.(type) {
	case *bus.ValidatorExamining:
		appendLine(&v.lines, &v.viewport, e.Timestamp,
			fmt.Sprintf("Validator examining: %d criterion(s)", criterionCount(e.Criteria)))
	case *bus.ValidatorCriterionResult:
		verdict := "FAIL"
		if e.Passed {
			verdict = "PASS"
		}
		body := fmt.Sprintf("Criterion %q: %s", e.Criterion, verdict)
		if e.Detail != "" {
			body += " — " + e.Detail
		}
		appendLine(&v.lines, &v.viewport, e.Timestamp, body)
	case *bus.ValidatorVerdict:
		appendLine(&v.lines, &v.viewport, e.Timestamp, formatVerdict(e))
	}
}

// criterionCount safely reports the number of validator criteria.
func criterionCount(criteria []string) int {
	return len(criteria)
}

// formatVerdict renders the overall verdict line as "Verdict: PASS — feedback".
func formatVerdict(e *bus.ValidatorVerdict) string {
	verdict := "FAIL"
	if e.Passes {
		verdict = "PASS"
	}
	body := "Verdict: " + verdict
	if e.Feedback != "" {
		body += " — " + strings.TrimSpace(e.Feedback)
	}
	return body
}

// View renders the accumulated lines through the viewport.
func (v *ValidatorTab) View() string {
	if v.viewport.Height <= 0 {
		return ""
	}
	return v.viewport.View()
}

// Lines returns a defensive copy of the accumulated output lines.
func (v *ValidatorTab) Lines() []string {
	out := make([]string, len(v.lines))
	copy(out, v.lines)
	return out
}