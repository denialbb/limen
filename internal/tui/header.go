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

type Header struct {
	taskID      string
	state       state.TaskState
	retryCount  int
	expandCount int
	spinner     spinner.Model
	finalized   bool
	width       int
}

func NewHeader(taskID string) *Header {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	return &Header{
		taskID: taskID,
		state:  state.StateCreated,
		spinner: sp,
	}
}

func (h *Header) SetSpinner(sp spinner.Model) {
	h.spinner = sp
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
	brand, field, state, count := theme.HeaderStyles()

	stateName := string(h.state)
	if stateName == "" {
		stateName = "UNKNOWN"
	}

	spinnerOrDone := h.spinner.View()
	if h.finalized {
		spinnerOrDone = "done"
	}

	leftGroup := strings.Join([]string{
		brand.Render("limen"),
		field.Render("task " + h.taskID),
	}, " ")

	rightGroup := strings.Join([]string{
		state.Render(stateName),
		count.Render(fmt.Sprintf("r:%d e:%d", h.retryCount, h.expandCount)),
		field.Render(spinnerOrDone),
	}, " ")

	leftWidth := lipgloss.Width(leftGroup)
	rightWidth := lipgloss.Width(rightGroup)

	if h.width > 0 && leftWidth+rightWidth < h.width {
		gap := strings.Repeat(" ", h.width-leftWidth-rightWidth)
		return leftGroup + gap + rightGroup
	}

	return leftGroup + " " + rightGroup
}