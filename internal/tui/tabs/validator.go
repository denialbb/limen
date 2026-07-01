package tabs

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/denialbb/limen/internal/bus"
)

// ValidatorTab renders Validator activity: the criteria being examined, the
// per-criterion pass/fail results, and the overall verdict with feedback.
type ValidatorTab struct {
	viewport viewport.Model
	lines    []string
}

// NewValidatorTab constructs an empty ValidatorTab with a default 1x1 footprint.
func NewValidatorTab() ValidatorTab {
	v := ValidatorTab{}
	v.viewport = viewport.New(1, 1)
	return v
}

// Init satisfies the tea.Model surface; the tab has no async work of its own.
func (v ValidatorTab) Init() tea.Cmd { return nil }

// SetSize resizes the Validator viewport.
func (v ValidatorTab) SetSize(width, height int) ValidatorTab {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	v.viewport.Width = width
	v.viewport.Height = height
	v.viewport.SetContent(wrapLines(v.lines, width))
	return v
}

// Update ingests either an EventMsg carrying a validator-relevant bus event or
// a tea.KeyMsg for scroll.
func (v ValidatorTab) Update(msg tea.Msg) (ValidatorTab, tea.Cmd) {
	switch m := msg.(type) {
	case EventMsg:
		v = v.handleEvent(m.Event)
	case tea.KeyMsg:
		v.viewport, _ = v.viewport.Update(m)
	}
	return v, nil
}

// handleEvent formats and appends a validator-relevant event line. It returns
// the modified tab value so Update can thread it back under value semantics.
func (v ValidatorTab) handleEvent(ev bus.Event) ValidatorTab {
	switch e := ev.(type) {
	case *bus.ValidatorExamining:
		appendLine(&v.lines, &v.viewport, v.viewport.Width, e.Timestamp,
			fmt.Sprintf("Validator examining: %d criterion(s)", len(e.Criteria)))
	case *bus.ValidatorCriterionResult:
		verdict := "FAIL"
		if e.Passed {
			verdict = "PASS"
		}
		body := fmt.Sprintf("Criterion %q: %s", e.Criterion, verdict)
		if e.Detail != "" {
			body += " — " + e.Detail
		}
		appendLine(&v.lines, &v.viewport, v.viewport.Width, e.Timestamp, body)
	case *bus.ValidatorVerdict:
		appendLine(&v.lines, &v.viewport, v.viewport.Width, e.Timestamp, formatVerdict(e))
	}
	return v
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
func (v ValidatorTab) View() string {
	if v.viewport.Height <= 0 {
		return ""
	}
	return v.viewport.View()
}

// Lines returns a defensive copy of the accumulated output lines.
func (v ValidatorTab) Lines() []string {
	out := make([]string, len(v.lines))
	copy(out, v.lines)
	return out
}
