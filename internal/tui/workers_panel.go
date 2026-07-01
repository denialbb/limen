package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/denialbb/limen/internal/bus"
	"github.com/denialbb/limen/internal/state"
)

// WorkerStatus is the display state of a single worker attempt.
type WorkerStatus int

const (
	WorkerStatusWorking  WorkerStatus = iota // shows spinner frame
	WorkerStatusAwaiting                     // ○  awaiting validation
	WorkerStatusSuccess                      // ✓  validator passed
	WorkerStatusFailed                       // ✗  validator failed or escalated
)

// WorkerEntry is a single row in the workers panel.
type WorkerEntry struct {
	ID     string // "worker-{Retry}" e.g. "worker-0"
	TaskID string
	Retry  int
	Status WorkerStatus
}

// WorkersPanel renders a compact list of workers with status icons.
// In split mode it occupies the bottom-right area below the timeline.
// Follows Bubble Tea value-receiver conventions: all mutating methods return
// a new value.
type WorkersPanel struct {
	entries    []WorkerEntry
	cursor     int
	focused    bool
	width      int
	height     int
	spinnerStr string // current spinner frame, updated by model on each spinner tick
}

func NewWorkersPanel() WorkersPanel {
	return WorkersPanel{}
}

func (w WorkersPanel) SetSize(width, height int) WorkersPanel {
	w.width = width
	w.height = height
	return w
}

func (w WorkersPanel) SetFocused(b bool) WorkersPanel {
	w.focused = b
	return w
}

// SetSpinner updates the spinner frame string used for WorkerStatusWorking entries.
func (w WorkersPanel) SetSpinner(s string) WorkersPanel {
	w.spinnerStr = s
	return w
}

func (w WorkersPanel) CursorUp() WorkersPanel {
	if w.cursor > 0 {
		w.cursor--
	}
	return w
}

func (w WorkersPanel) CursorDown() WorkersPanel {
	if w.cursor < len(w.entries)-1 {
		w.cursor++
	}
	return w
}

// SelectedID returns the ID of the entry under the cursor, or "" if no entries.
func (w WorkersPanel) SelectedID() string {
	if len(w.entries) == 0 || w.cursor >= len(w.entries) {
		return ""
	}
	return w.entries[w.cursor].ID
}

// HandleEvent updates the panel based on a bus.Event. Returns a new WorkersPanel.
// Handled events: WorkerStarted, WorkerFinished, ValidatorVerdict, TaskFinalized.
func (w WorkersPanel) HandleEvent(ev bus.Event) WorkersPanel {
	switch e := ev.(type) {
	case *bus.WorkerStarted:
		entry := WorkerEntry{
			ID:     fmt.Sprintf("worker-%d", e.Retry),
			TaskID: e.TaskID,
			Retry:  e.Retry,
			Status: WorkerStatusWorking,
		}
		w.entries = append(w.entries, entry)
		w.cursor = len(w.entries) - 1

	case *bus.WorkerFinished:
		// Mark the last Working entry as Awaiting (waiting for validator).
		for i := len(w.entries) - 1; i >= 0; i-- {
			if w.entries[i].Status == WorkerStatusWorking {
				w.entries[i].Status = WorkerStatusAwaiting
				break
			}
		}

	case *bus.ValidatorVerdict:
		// Mark the last Awaiting entry with pass/fail.
		for i := len(w.entries) - 1; i >= 0; i-- {
			if w.entries[i].Status == WorkerStatusAwaiting {
				if e.Passes {
					w.entries[i].Status = WorkerStatusSuccess
				} else {
					w.entries[i].Status = WorkerStatusFailed
				}
				break
			}
		}

	case *bus.TaskFinalized:
		// Resolve any still-running or awaiting entries with the terminal state.
		for i := range w.entries {
			if w.entries[i].Status == WorkerStatusWorking || w.entries[i].Status == WorkerStatusAwaiting {
				if e.FinalState == state.StateCommitted || e.FinalState == state.StateApproved {
					w.entries[i].Status = WorkerStatusSuccess
				} else {
					w.entries[i].Status = WorkerStatusFailed
				}
			}
		}
	}
	return w
}

// View renders the workers panel.
func (w WorkersPanel) View() string {
	prefix := " ─ Workers "
	fill := max(0, w.width-lipgloss.Width(prefix))
	title := theme.SplitPanelTitleStyle().Render(prefix + strings.Repeat("─", fill))

	var sb strings.Builder
	sb.WriteString(title)

	if len(w.entries) == 0 {
		sb.WriteString("\n")
		sb.WriteString(lipgloss.NewStyle().Faint(true).Render("  (no workers yet)"))
		return sb.String()
	}

	for i, e := range w.entries {
		sb.WriteString("\n")
		icon := w.statusIcon(e)
		cursor := " "
		if w.focused && i == w.cursor {
			cursor = ">"
		}
		line := cursor + " " + icon + "  " + e.ID + "  " + e.TaskID
		if w.focused && i == w.cursor {
			line = lipgloss.NewStyle().Bold(true).Render(line)
		}
		sb.WriteString(line)
	}
	return sb.String()
}

func (w WorkersPanel) statusIcon(e WorkerEntry) string {
	switch e.Status {
	case WorkerStatusWorking:
		frame := w.spinnerStr
		if frame == "" {
			frame = "⠙"
		}
		return lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Render(frame)
	case WorkerStatusAwaiting:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("○")
	case WorkerStatusSuccess:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Render("✓")
	case WorkerStatusFailed:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render("✗")
	}
	return "?"
}
