// Package tui implements the interactive terminal user interface for Limen
// using the Charmbracelet Bubble Tea + lipgloss + bubbles stack.
//
// The TUI subscribes to the in-process event bus (see internal/bus), pumps
// bus.Event values into tea.Msg values via a blocking tea.Cmd, and renders a
// slim persistent status header plus four switchable tabs (Router, Worker,
// Validator, Timeline) from the live event stream.
//
// Threading contract: all model state mutations occur on the Bubble Tea
// update goroutine. The event channel is read by a tea.Cmd; no goroutine
// other than the Bubble Tea runtime ever touches Model fields.
package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/denialbb/limen/internal/bus"
	"github.com/denialbb/limen/internal/state"
	"github.com/denialbb/limen/internal/tui/tabs"
)

// tabID identifies one of the four switchable tabs.
type tabID int

const (
	tabRouter tabID = iota
	tabWorker
	tabValidator
	tabTimeline
	tabCount // sentinel; must remain last
)

// layoutMode describes whether the TUI uses tabbed or split-column rendering.
type layoutMode int

const (
	layoutTab   layoutMode = iota // single-tab view (terminal below threshold)
	layoutSplit                   // side-by-side columns (terminal above threshold)
)

// splitFocusArea tracks which region of the split layout has keyboard focus.
type splitFocusArea int

const (
	splitFocusTimeline    splitFocusArea = iota // top-right: timeline (default)
	splitFocusWorkers                           // bottom-right: workers panel
	splitFocusWorkerDetail                      // top-right: per-worker detail (after Enter)
)

// busEventMsg wraps a bus.Event delivered from the subscription channel.
// It is the bridge between the Go channel transport and Bubble Tea's
// message system. The model routes the wrapped event to the appropriate
// tab(s) via tabs.EventMsg.
type busEventMsg struct {
	event bus.Event
}

// busChannelClosedMsg is delivered when the subscription channel has been
// closed (the orchestrator finished and tore the bus down). On receipt the
// model marks itself finalized and stops the event pump.
type busChannelClosedMsg struct{}

// Model is the top-level Bubble Tea model for the Limen interactive TUI.
type Model struct {
	taskID string
	bus    bus.EventBus

	currentTab tabID
	width      int
	height     int

	// Sub-components are held by value. Each follows the bubbles value
	// convention: Update returns a modified value plus a tea.Cmd, so the
	// value-receiver methods on Model can rebuild themselves by reassigning
	// the updated sub-component.
	header    Header
	tabStrip  TabStrip
	router    tabs.RouterTab
	worker    tabs.WorkerTab
	validator tabs.ValidatorTab
	timeline  tabs.TimelineTab

	eventCh         <-chan bus.Event
	spinner         spinner.Model
	finalized       bool
	finalState      state.TaskState
	flashTickActive bool

	// Split layout state.
	layout          layoutMode
	splitFocus      splitFocusArea
	workersPanel    WorkersPanel
	workerDetail    WorkerDetail
	currentWorkerID string

	quitting bool
}

// NewModel constructs the top-level model. It subscribes to the provided
// event bus immediately so no event published between construction and the
// first tea.Cmd pump is lost (the channel is buffered to SubscriberBufferSize).
func NewModel(taskID string, eventBus bus.EventBus) Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot

	return Model{
		taskID:       taskID,
		bus:          eventBus,
		currentTab:   tabRouter,
		header:       NewHeader(taskID),
		tabStrip:     NewTabStrip(),
		router:       tabs.NewRouterTab(),
		worker:       tabs.NewWorkerTab(),
		validator:    tabs.NewValidatorTab(),
		timeline:     tabs.NewTimelineTab(),
		eventCh:      eventBus.Subscribe(),
		spinner:      sp,
		workersPanel: NewWorkersPanel(),
		workerDetail: NewWorkerDetail(),
	}
}

// Init starts the event pump and the spinner tick. The two commands are
// batched so they run concurrently.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, waitForEvent(m.eventCh))
}

// waitForEvent blocks on a single receive from the event channel and returns
// the event wrapped as a busEventMsg. When the channel is closed (orchestrator
// teardown), it returns busChannelClosedMsg so the model can finalize without
// spinning on a closed channel.
func waitForEvent(ch <-chan bus.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return busChannelClosedMsg{}
		}
		return busEventMsg{event: ev}
	}
}

// Update dispatches every message to the appropriate handler.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.WindowSizeMsg:
		return m.handleResize(msg)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		m.header = m.header.SetSpinnerView(m.spinner.View())
		m.workersPanel = m.workersPanel.SetSpinner(m.spinner.View())
		return m, cmd

	case tabFlashTickMsg:
		m.flashTickActive = false
		anyFlashing := false
		for i := 0; i < len(m.tabStrip.flashFrames); i++ {
			if m.tabStrip.flashFrames[i] > 0 {
				m.tabStrip.flashFrames[i]--
				if m.tabStrip.flashFrames[i] > 0 {
					anyFlashing = true
				}
			}
		}
		if anyFlashing {
			m.flashTickActive = true
			return m, tickTabFlash()
		}
		return m, nil

	case busEventMsg:
		return m.handleBusEvent(msg)

	case busChannelClosedMsg:
		if !m.finalized {
			m.finalized = true
			setCurrentTab(&m, tabTimeline)
		}
		return m, nil
	}

	return m, nil
}

// handleKey routes keyboard input. In split mode, navigation is region-based.
// In tab mode, number keys jump to a tab, brackets cycle, j/k scroll, and q /
// Ctrl+C quit.
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Split-mode specific navigation.
	if m.layout == layoutSplit {
		switch msg.String() {
		case "w":
			m.splitFocus = splitFocusWorkers
			m.workersPanel = m.workersPanel.SetFocused(true)
			return m, nil
		case "enter":
			if m.splitFocus == splitFocusWorkers {
				id := m.workersPanel.SelectedID()
				if id != "" {
					m.workerDetail = m.workerDetail.SetWorker(id)
					m.splitFocus = splitFocusWorkerDetail
					m.workersPanel = m.workersPanel.SetFocused(false)
				}
			}
			return m, nil
		case "esc":
			m.splitFocus = splitFocusTimeline
			m.workersPanel = m.workersPanel.SetFocused(false)
			return m, nil
		case "j", "down":
			switch m.splitFocus {
			case splitFocusWorkers:
				m.workersPanel = m.workersPanel.CursorDown()
				return m, nil
			case splitFocusWorkerDetail:
				m.workerDetail, _ = m.workerDetail.Update(msg)
				return m, nil
			default:
				m.timeline, _ = m.timeline.Update(msg)
				return m, nil
			}
		case "k", "up":
			switch m.splitFocus {
			case splitFocusWorkers:
				m.workersPanel = m.workersPanel.CursorUp()
				return m, nil
			case splitFocusWorkerDetail:
				m.workerDetail, _ = m.workerDetail.Update(msg)
				return m, nil
			default:
				m.timeline, _ = m.timeline.Update(msg)
				return m, nil
			}
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		}
		return m, nil
	}

	// Tab mode navigation (original behavior).
	switch msg.String() {
	case "1":
		setCurrentTab(&m, tabRouter)
	case "2":
		setCurrentTab(&m, tabWorker)
	case "3":
		setCurrentTab(&m, tabValidator)
	case "4":
		setCurrentTab(&m, tabTimeline)
	case "]":
		next := (int(m.currentTab) + 1) % int(tabCount)
		setCurrentTab(&m, tabID(next))
	case "[":
		prev := (int(m.currentTab) - 1 + int(tabCount)) % int(tabCount)
		setCurrentTab(&m, tabID(prev))
	case "j", "down":
		return m.forwardKeyToActiveTab(msg)
	case "k", "up":
		return m.forwardKeyToActiveTab(msg)
	case "q", "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	}
	return m, nil
}

// setCurrentTab is the single mutator for the active tab index. It writes
// both currentTab and the tab strip's active index on a *Model so callers can
// use it mid-method without losing the SetActive reassignment.
func setCurrentTab(m *Model, id tabID) {
	m.currentTab = id
	m.tabStrip = m.tabStrip.SetActive(int(id))
}

// forwardKeyToActiveTab dispatches a scroll key to the active viewport. It
// returns the new Model and a (typically nil) command so the result can be
// threaded straight back through Update.
func (m Model) forwardKeyToActiveTab(msg tea.KeyMsg) (Model, tea.Cmd) {
	var cmd tea.Cmd
	switch m.currentTab {
	case tabRouter:
		m.router, cmd = m.router.Update(msg)
	case tabWorker:
		m.worker, cmd = m.worker.Update(msg)
	case tabValidator:
		m.validator, cmd = m.validator.Update(msg)
	case tabTimeline:
		m.timeline, cmd = m.timeline.Update(msg)
	}
	return m, cmd
}

// handleResize propagates window size changes to every sub-component,
// selecting split or tab layout based on terminal dimensions.
func (m Model) handleResize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.width = msg.Width
	m.height = msg.Height

	headerH := 1
	sepH := 1 + 2*theme.SeparatorPadV
	hintH := 1

	m.header = m.header.SetWidth(msg.Width)

	if msg.Width >= theme.SplitWidthThreshold && msg.Height >= theme.SplitHeightThreshold {
		m.layout = layoutSplit

		contentH := msg.Height - headerH - sepH - hintH
		if contentH < 1 {
			contentH = 1
		}

		leftW := int(float64(msg.Width) * theme.SplitLeftWidthPct)
		if leftW < 1 {
			leftW = 1
		}
		rightW := msg.Width - leftW - 1 // 1 for the │ divider
		if rightW < 1 {
			rightW = 1
		}

		// splitView renders renderPanelTitle once above Router and once above
		// Validator in the left column, each consuming one row. Subtract them
		// before distributing viewport heights so the left column's total
		// height equals contentH and doesn't overflow the terminal.
		const panelTitleRows = 2
		routerH := int(float64(contentH-panelTitleRows) * theme.SplitRouterHeightPct)
		if routerH < 1 {
			routerH = 1
		}
		validatorH := contentH - panelTitleRows - routerH
		if validatorH < 1 {
			validatorH = 1
		}

		workersH := int(float64(contentH) * theme.SplitWorkersHeightPct)
		if workersH < 1 {
			workersH = 1
		}
		timelineH := contentH - workersH
		if timelineH < 1 {
			timelineH = 1
		}

		m.router = m.router.SetSize(leftW, routerH)
		m.validator = m.validator.SetSize(leftW, validatorH)
		m.timeline = m.timeline.SetSize(rightW, timelineH)
		m.workersPanel = m.workersPanel.SetSize(rightW, workersH)
		m.workerDetail = m.workerDetail.SetSize(rightW, timelineH)
		m.tabStrip = m.tabStrip.SetSize(msg.Width)
	} else {
		m.layout = layoutTab

		tabstripH := 1
		contentH := msg.Height - headerH - sepH - tabstripH - hintH
		if contentH < 1 {
			contentH = 1
		}

		m.router = m.router.SetSize(msg.Width, contentH)
		m.worker = m.worker.SetSize(msg.Width, contentH)
		m.validator = m.validator.SetSize(msg.Width, contentH)
		m.timeline = m.timeline.SetSize(msg.Width, contentH)
		m.tabStrip = m.tabStrip.SetSize(msg.Width)
	}
	return m, nil
}

// handleBusEvent routes a single bus.Event to the header and the appropriate
// tab(s) and split-mode panels. Routing follows the taxonomy in the design
// document:
//
//   - Timeline receives ALL events.
//   - Router receives ContextBuilt, RouterExamining, RouterDecision.
//   - Worker receives WorkerStarted, WorkerToolCall, WorkerFileEdit,
//     WorkerFinished, ConflictDetected.
//   - Validator receives ValidatorExamining, ValidatorCriterionResult,
//     ValidatorVerdict.
//   - TaskStateChanged updates the header state and the timeline.
//   - TaskFinalized flips the finalized flag, records the final state, and
//     auto-switches to the Timeline tab for review (tab mode only).
func (m Model) handleBusEvent(msg busEventMsg) (tea.Model, tea.Cmd) {
	ev := msg.event
	if ev == nil {
		return m, waitForEvent(m.eventCh)
	}

	// Header and timeline see every event.
	m.header, _ = m.header.Update(tabs.EventMsg{Event: ev})
	m.timeline, _ = m.timeline.Update(tabs.EventMsg{Event: ev})

	var tabToFlash int = -1
	switch e := ev.(type) {
	case *bus.TaskStateChanged:

	case *bus.ContextBuilt:
		m.router, _ = m.router.Update(tabs.EventMsg{Event: e})
		tabToFlash = int(tabRouter)

	case *bus.RouterExamining:
		m.router, _ = m.router.Update(tabs.EventMsg{Event: e})
		tabToFlash = int(tabRouter)

	case *bus.RouterDecisionEvent:
		m.router, _ = m.router.Update(tabs.EventMsg{Event: e})
		tabToFlash = int(tabRouter)

	case *bus.WorkerStarted:
		m.worker, _ = m.worker.Update(tabs.EventMsg{Event: e})
		m.workersPanel = m.workersPanel.HandleEvent(e)
		m.currentWorkerID = m.workersPanel.SelectedID()
		m.workerDetail = m.workerDetail.SetWorker(m.currentWorkerID)
		tabToFlash = int(tabWorker)

	case *bus.WorkerToolCall:
		m.worker, _ = m.worker.Update(tabs.EventMsg{Event: e})
		if m.currentWorkerID != "" {
			line := fmt.Sprintf("tool call: %s %s", e.Tool, e.Args)
			if tabs.EventFormatter != nil {
				line = tabs.EventFormatter(e.Time(), line)
			}
			m.workerDetail = m.workerDetail.AppendLine(m.currentWorkerID, line)
		}
		tabToFlash = int(tabWorker)

	case *bus.WorkerFileEdit:
		m.worker, _ = m.worker.Update(tabs.EventMsg{Event: e})
		if m.currentWorkerID != "" {
			line := fmt.Sprintf("file edit: %s (%s)", e.Path, e.Op)
			if tabs.EventFormatter != nil {
				line = tabs.EventFormatter(e.Time(), line)
			}
			m.workerDetail = m.workerDetail.AppendLine(m.currentWorkerID, line)
			if e.DiffHunk != "" {
				faintStyle := lipgloss.NewStyle().Faint(true).Foreground(lipgloss.Color("240"))
				for _, dl := range strings.Split(e.DiffHunk, "\n") {
					m.workerDetail = m.workerDetail.AppendLine(m.currentWorkerID, faintStyle.Render("  "+dl))
				}
			}
		}
		tabToFlash = int(tabWorker)

	case *bus.WorkerFinished:
		m.worker, _ = m.worker.Update(tabs.EventMsg{Event: e})
		m.workersPanel = m.workersPanel.HandleEvent(e)
		tabToFlash = int(tabWorker)

	case *bus.ConflictDetected:
		m.worker, _ = m.worker.Update(tabs.EventMsg{Event: e})
		tabToFlash = int(tabWorker)

	case *bus.ValidatorExamining:
		m.validator, _ = m.validator.Update(tabs.EventMsg{Event: e})
		tabToFlash = int(tabValidator)

	case *bus.ValidatorCriterionResult:
		m.validator, _ = m.validator.Update(tabs.EventMsg{Event: e})
		if m.currentWorkerID != "" {
			verdict := "FAIL"
			if e.Passed {
				verdict = "PASS"
			}
			line := fmt.Sprintf("criterion %q: %s", e.Criterion, verdict)
			if tabs.EventFormatter != nil {
				line = tabs.EventFormatter(e.Time(), line)
			}
			m.workerDetail = m.workerDetail.AppendLine(m.currentWorkerID, line)
		}
		tabToFlash = int(tabValidator)

	case *bus.ValidatorVerdict:
		m.validator, _ = m.validator.Update(tabs.EventMsg{Event: e})
		m.workersPanel = m.workersPanel.HandleEvent(e)
		if m.currentWorkerID != "" {
			v := "FAIL"
			if e.Passes {
				v = "PASS"
			}
			line := fmt.Sprintf("verdict: %s — %s", v, strings.TrimSpace(e.Feedback))
			if tabs.EventFormatter != nil {
				line = tabs.EventFormatter(e.Time(), line)
			}
			m.workerDetail = m.workerDetail.AppendLine(m.currentWorkerID, line)
		}
		tabToFlash = int(tabValidator)

	case *bus.TaskFinalized:
		m.finalized = true
		m.finalState = e.FinalState
		m.workersPanel = m.workersPanel.HandleEvent(e)
		if m.layout == layoutTab {
			setCurrentTab(&m, tabTimeline)
		}
		tabToFlash = int(tabTimeline)

	case *bus.OrchestratorError:
		tabToFlash = int(tabTimeline)
	}

	var flashCmd tea.Cmd
	if tabToFlash != -1 && m.layout == layoutTab {
		m.tabStrip = m.tabStrip.Flash(tabID(tabToFlash))
		if !m.flashTickActive {
			m.flashTickActive = true
			flashCmd = tickTabFlash()
		}
	}

	return m, tea.Batch(waitForEvent(m.eventCh), flashCmd)
}

// View renders the TUI. In split mode it delegates to splitView; in tab mode
// it renders the header, separator, active tab content, tab strip, and hint
// in a vertical stack using lipgloss.JoinVertical.
func (m Model) View() string {
	if m.quitting {
		return ""
	}
	if m.layout == layoutSplit {
		return m.splitView()
	}

	sepWidth := m.width
	if sepWidth <= 0 {
		sepWidth = 0
	}
	sepLine := theme.SeparatorStyle().Render(strings.Repeat(theme.SeparatorRune, sepWidth))

	hint := lipgloss.NewStyle().Faint(true).Foreground(lipgloss.Color("245")).Render(
		"  [1-4] tab  [j/k] scroll  [q] quit",
	)

	blocks := []string{
		m.header.View(),
		sepLine,
		m.activeTabView(),
		m.tabStrip.View(),
		hint,
	}

	if theme.SeparatorPadV > 0 {
		pad := strings.Repeat(" ", max(m.width, 1))
		padded := make([]string, 0, len(blocks)+2*theme.SeparatorPadV)
		padded = append(padded, blocks[0])
		for i := 0; i < theme.SeparatorPadV; i++ {
			padded = append(padded, pad)
		}
		padded = append(padded, blocks[1])
		for i := 0; i < theme.SeparatorPadV; i++ {
			padded = append(padded, pad)
		}
		padded = append(padded, blocks[2:]...)
		blocks = padded
	}

	return lipgloss.JoinVertical(lipgloss.Left, blocks...)
}

// splitView renders the side-by-side split layout: left column (Router +
// Validator stacked) and right column (Timeline or WorkerDetail + Workers).
func (m Model) splitView() string {
	sepWidth := m.width
	if sepWidth <= 0 {
		sepWidth = 0
	}
	sepLine := theme.SeparatorStyle().Render(strings.Repeat(theme.SeparatorRune, sepWidth))

	headerH := 1
	sepH := 1 + 2*theme.SeparatorPadV
	hintH := 1
	contentH := m.height - headerH - sepH - hintH
	if contentH < 1 {
		contentH = 1
	}

	leftW := int(float64(m.width) * theme.SplitLeftWidthPct)
	if leftW < 1 {
		leftW = 1
	}

	// Left column: Router panel title + Router view + Validator panel title + Validator view.
	leftContent := lipgloss.JoinVertical(lipgloss.Left,
		renderPanelTitle("Router", leftW),
		m.router.View(),
		renderPanelTitle("Validator", leftW),
		m.validator.View(),
	)

	// Right column: top area (Timeline or WorkerDetail) + Workers panel.
	var topRight string
	if m.splitFocus == splitFocusWorkerDetail {
		topRight = m.workerDetail.View()
	} else {
		topRight = m.timeline.View()
	}
	rightContent := lipgloss.JoinVertical(lipgloss.Left,
		topRight,
		m.workersPanel.View(),
	)

	mainArea := splitColumns(leftContent, rightContent, contentH)

	hint := lipgloss.NewStyle().Faint(true).Foreground(lipgloss.Color("245")).Render(
		"  [j/k] scroll  [w] workers  [Enter] select  [Esc] back  [q] quit",
	)

	blocks := []string{
		m.header.View(),
		sepLine,
		mainArea,
		hint,
	}

	if theme.SeparatorPadV > 0 {
		pad := strings.Repeat(" ", max(m.width, 1))
		padded := make([]string, 0, len(blocks)+2*theme.SeparatorPadV)
		padded = append(padded, blocks[0])
		for i := 0; i < theme.SeparatorPadV; i++ {
			padded = append(padded, pad)
		}
		padded = append(padded, blocks[1])
		for i := 0; i < theme.SeparatorPadV; i++ {
			padded = append(padded, pad)
		}
		padded = append(padded, blocks[2:]...)
		blocks = padded
	}

	return lipgloss.JoinVertical(lipgloss.Left, blocks...)
}

func (m Model) activeTabView() string {
	switch m.currentTab {
	case tabRouter:
		return m.router.View()
	case tabWorker:
		return m.worker.View()
	case tabValidator:
		return m.validator.View()
	case tabTimeline:
		return m.timeline.View()
	default:
		return ""
	}
}

func (m Model) Finalized() bool       { return m.finalized }
func (m Model) FinalState() state.TaskState { return m.finalState }
func (m Model) TaskID() string        { return m.taskID }

func (m Model) String() string {
	if !m.finalized {
		return fmt.Sprintf("task %s: not finalized", m.taskID)
	}
	sepWidth := m.width
	if sepWidth <= 0 {
		sepWidth = 80
	}
	sepLine := theme.SeparatorStyle().Render(strings.Repeat(theme.SeparatorRune, sepWidth))
	return lipgloss.JoinVertical(lipgloss.Left,
		m.header.View(),
		sepLine,
		m.timeline.View(),
	)
}

type tabFlashTickMsg struct{}

func tickTabFlash() tea.Cmd {
	return tea.Tick(50*time.Millisecond, func(t time.Time) tea.Msg {
		return tabFlashTickMsg{}
	})
}

var _ tea.Model = Model{}
