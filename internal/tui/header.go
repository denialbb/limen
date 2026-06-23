package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/denialbb/limen/internal/bus"
	"github.com/denialbb/limen/internal/state"
	"github.com/denialbb/limen/internal/tui/tabs"
)

type Header struct {
	taskID      string
	state       state.TaskState
	retryCount  int
	expandCount int
	spinnerView string
	finalized   bool
	width       int
}

func NewHeader(taskID string) Header {
	return Header{
		taskID:      taskID,
		state:       state.StateCreated,
		spinnerView: "",
	}
}

// Init satisfies the tea.Model surface; the header has no async work of its
// own, so it returns a nil command.
func (h Header) Init() tea.Cmd { return nil }

func (h Header) SetSpinnerView(view string) Header {
	h.spinnerView = view
	return h
}

func (h Header) SetWidth(width int) Header {
	h.width = width
	return h
}

// Update reacts to TUI lifecycle events wrapped as tabs.EventMsg. The inner
// bus.Event payload is unwrapped and type-switched so the header stays in sync
// with the orchestrator state machine.
func (h Header) Update(msg tea.Msg) (Header, tea.Cmd) {
	switch m := msg.(type) {
	case tabs.EventMsg:
		switch ev := m.Event.(type) {
		case *bus.TaskStateChanged:
			h.state = ev.To
		case *bus.RouterDecisionEvent:
			h.expandCount = ev.ExpandCount
		case *bus.WorkerStarted:
			h.retryCount = ev.Retry
		case *bus.TaskFinalized:
			h.finalized = true
			h.state = ev.FinalState
		}
	}
	return h, nil
}

func (h Header) View() string {
	bar, brand, field, state, count, filler := theme.HeaderStyles(h.width)

	stateName := h.stateName()
	marker := h.spinnerView
	if h.finalized {
		marker = "done"
	}

	left := lipgloss.JoinHorizontal(lipgloss.Top,
		brand.Render("limen"),
		field.Render("task "+h.taskID),
	)

	right := lipgloss.JoinHorizontal(lipgloss.Top,
		state.Render(stateName),
		count.Render(fmt.Sprintf("r:%d e:%d", h.retryCount, h.expandCount)),
		field.Render(marker),
	)

	if h.width <= 0 {
		return left + right
	}

	gapWidth := h.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gapWidth < 0 {
		gapWidth = 0
	}

	return bar.Render(lipgloss.JoinHorizontal(lipgloss.Top,
		left,
		filler.Width(gapWidth).Render(""),
		right,
	))
}

func (h Header) stateName() string {
	name := string(h.state)
	if name == "" {
		return "UNKNOWN"
	}
	return name
}