package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/denialbb/limen/internal/bus"
	"github.com/denialbb/limen/internal/state"
)

// headerStyle applies a single-line, low-saturation treatment to the persistent
// status line. Kept deliberately minimal so the cognitive components' tabs get
// the visual focus.
var (
	headerStyle      = lipgloss.NewStyle().Bold(true).Background(lipgloss.Color("63")).Foreground(lipgloss.Color("15")).Padding(0, 1)
	headerFieldStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	headerStateStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("213"))
	headerCountStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
)

// Header renders the slim persistent status line. It tracks the task ID, the
// most recent state, retry and expand counts, and a spinner. The state and
// counts are updated from bus events; the spinner ticks with the model.
type Header struct {
	taskID      string
	state       state.TaskState
	retryCount   int
	expandCount int
	spinner     spinner.Model
	finalized   bool
}

// NewHeader constructs a Header seeded with the CREATED state.
func NewHeader(taskID string) *Header {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	return &Header{
		taskID: taskID,
		state:  state.StateCreated,
		spinner: sp,
	}
}

// SetSpinner copies the parent model's spinner into the header so its View()
// reflects the latest tick. The header does not own tick scheduling.
func (h *Header) SetSpinner(sp spinner.Model) {
	h.spinner = sp
}

// Update ingests bus events that influence the status line: state transitions
// set the current state, the router decision carries the expand count, and the
// worker started event carries the current retry count.
func (h *Header) Update(msg tea.Msg) {
	switch ev := msg.(type) {
	case *bus.TaskStateChanged:
		// The header shows the authoritative latest state terminal node.
		h.state = ev.To

	case *bus.RouterDecisionEvent:
		// Expand count is carried on the decision event per the taxonomy.
		h.expandCount = ev.ExpandCount

	case *bus.WorkerStarted:
		// The worker restates its retry index on every pass.
		h.retryCount = ev.Retry

	case *bus.TaskFinalized:
		h.finalized = true
		h.state = ev.FinalState
	}
}

// View renders the one-line status bar. Format mirrors the design document:
//
//	limen | task <id> | <STATE> | r:<retry> e:<expand> | <spinner>
func (h *Header) View() string {
	stateName := string(h.state)
	if stateName == "" {
		stateName = "UNKNOWN"
	}
	return strings.Join([]string{
		headerStyle.Render("limen"),
		headerFieldStyle.Render("task " + h.taskID),
		headerStateStyle.Render(stateName),
		headerCountStyle.Render(fmt.Sprintf("r:%d e:%d", h.retryCount, h.expandCount)),
		headerFieldStyle.Render(h.spinner.View()),
	}, " ")
}