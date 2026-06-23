package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/denialbb/limen/internal/bus"
	"github.com/denialbb/limen/internal/state"
)

type Header struct {
	taskID       string
	state        state.TaskState
	retryCount   int
	expandCount  int
	spinnerView  string
	finalized    bool
	width        int
}

func NewHeader(taskID string) *Header {
	return &Header{
		taskID:      taskID,
		state:       state.StateCreated,
		spinnerView: "",
	}
}

func (h *Header) SetSpinnerView(view string) {
	h.spinnerView = view
}

func (h *Header) SetWidth(width int) {
	h.width = width
}

func (h *Header) Update(msg tea.Msg) {
	switch ev := msg.(type) {
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

func (h *Header) View() string {
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

func (h *Header) stateName() string {
	name := string(h.state)
	if name == "" {
		return "UNKNOWN"
	}
	return name
}
