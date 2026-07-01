package tui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// WorkerDetail renders the event log for a single selected worker attempt.
// In split mode it occupies the main content area of the right column when
// the user has selected a worker from the WorkersPanel.
// Follows Bubble Tea value-receiver conventions.
//
// NOTE: history is a map (reference type). Callers must always reassign the
// returned WorkerDetail (m.workerDetail = m.workerDetail.AppendLine(...)) and
// must not hold snapshots of WorkerDetail across AppendLine calls.
type WorkerDetail struct {
	viewport viewport.Model
	workerID string              // ID of the currently displayed worker
	history  map[string][]string // workerID -> accumulated lines
	width    int
	height   int
}

func NewWorkerDetail() WorkerDetail {
	d := WorkerDetail{
		history: make(map[string][]string),
	}
	d.viewport = viewport.New(1, 1)
	return d
}

// SetSize resizes the viewport.
func (d WorkerDetail) SetSize(width, height int) WorkerDetail {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	d.width = width
	d.height = height
	d.viewport.Width = width
	d.viewport.Height = height
	if d.workerID != "" {
		lines := d.history[d.workerID]
		d.viewport.SetContent(wrapDetailLines(lines, width))
	}
	return d
}

// SetWorker switches the displayed worker to id, reloading the viewport.
func (d WorkerDetail) SetWorker(id string) WorkerDetail {
	if d.history == nil {
		d.history = make(map[string][]string)
	}
	d.workerID = id
	lines := d.history[id]
	d.viewport.SetContent(wrapDetailLines(lines, d.width))
	d.viewport.GotoBottom()
	return d
}

// AppendLine adds a line to the specified worker's history. If workerID
// matches the currently displayed worker, the viewport is refreshed.
func (d WorkerDetail) AppendLine(workerID, line string) WorkerDetail {
	if d.history == nil {
		d.history = make(map[string][]string)
	}
	d.history[workerID] = append(d.history[workerID], line)
	if workerID == d.workerID {
		d.viewport.SetContent(wrapDetailLines(d.history[workerID], d.width))
		d.viewport.GotoBottom()
	}
	return d
}

// Update handles scroll keys.
func (d WorkerDetail) Update(msg tea.Msg) (WorkerDetail, tea.Cmd) {
	if km, ok := msg.(tea.KeyMsg); ok {
		d.viewport, _ = d.viewport.Update(km)
	}
	return d, nil
}

// View renders the worker detail viewport.
func (d WorkerDetail) View() string {
	if d.workerID == "" {
		return lipgloss.NewStyle().Faint(true).
			Width(d.width).Height(d.height).
			Render("  (select a worker with Enter)")
	}
	return d.viewport.View()
}

var detailAnsiEscape = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// wrapDetailLines wraps each line to width for viewport rendering, applying a
// hanging indent to continuation lines so wrapped text aligns under the body
// (past the "[HH:MM:SS] " timestamp prefix).
func wrapDetailLines(lines []string, width int) string {
	if width <= 0 {
		return strings.Join(lines, "\n")
	}
	const hangIndent = 11 // len("[HH:MM:SS] ")
	indent := strings.Repeat(" ", hangIndent)
	wrapped := make([]string, len(lines))
	for i, line := range lines {
		rendered := lipgloss.NewStyle().Width(width).Render(line)
		parts := strings.Split(rendered, "\n")
		if len(parts) > 1 && strings.HasPrefix(detailAnsiEscape.ReplaceAllString(line, ""), "[") {
			for j := 1; j < len(parts); j++ {
				parts[j] = indent + strings.TrimLeft(parts[j], " ")
			}
		}
		wrapped[i] = strings.Join(parts, "\n")
	}
	return strings.Join(wrapped, "\n")
}
