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

	quitting bool
}

// NewModel constructs the top-level model. It subscribes to the provided
// event bus immediately so no event published between construction and the
// first tea.Cmd pump is lost (the channel is buffered to SubscriberBufferSize).
func NewModel(taskID string, eventBus bus.EventBus) Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot

	return Model{
		taskID:     taskID,
		bus:        eventBus,
		currentTab: tabRouter,
		header:     NewHeader(taskID),
		tabStrip:   NewTabStrip(),
		router:     tabs.NewRouterTab(),
		worker:     tabs.NewWorkerTab(),
		validator:  tabs.NewValidatorTab(),
		timeline:   tabs.NewTimelineTab(),
		eventCh:    eventBus.Subscribe(),
		spinner:    sp,
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

// handleKey routes keyboard input. Number keys jump to a tab, brackets cycle,
// j/k scroll the active viewport, and q / Ctrl+C quit.
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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

// handleResize propagates window size changes to every sub-component. The
// content viewport accounts for the fixed-height chrome: header, separator,
// and tab strip.
func (m Model) handleResize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.width = msg.Width
	m.height = msg.Height

	headerH := 1
	sepH := 1 + 2*theme.SeparatorPadV
	tabstripH := 1
	hintH := 1
	contentHeight := msg.Height - headerH - sepH - tabstripH - hintH
	if contentHeight < 1 {
		contentHeight = 1
	}

	m.router = m.router.SetSize(msg.Width, contentHeight)
	m.worker = m.worker.SetSize(msg.Width, contentHeight)
	m.validator = m.validator.SetSize(msg.Width, contentHeight)
	m.timeline = m.timeline.SetSize(msg.Width, contentHeight)
	m.header = m.header.SetWidth(msg.Width)
	m.tabStrip = m.tabStrip.SetSize(msg.Width)
	return m, nil
}

// handleBusEvent routes a single bus.Event to the header and the appropriate
// tab(s). Routing follows the taxonomy in the design document:
//
//   - Timeline receives ALL events.
//   - Router receives ContextBuilt, RouterExamining, RouterDecision.
//   - Worker receives WorkerStarted, WorkerToolCall, WorkerFileEdit,
//     WorkerFinished, ConflictDetected.
//   - Validator receives ValidatorExamining, ValidatorCriterionResult,
//     ValidatorVerdict.
//   - TaskStateChanged updates the header state and the timeline.
//   - TaskFinalized flips the finalized flag, records the final state, and
//     auto-switches to the Timeline tab for review.
func (m Model) handleBusEvent(msg busEventMsg) (tea.Model, tea.Cmd) {
	ev := msg.event
	if ev == nil {
		return m, waitForEvent(m.eventCh)
	}

	// The header and timeline both see every event; their Update methods
	// ignore anything they don't care about.
	m.header, _ = m.header.Update(tabs.EventMsg{Event: ev})
	m.timeline, _ = m.timeline.Update(tabs.EventMsg{Event: ev})

	var tabToFlash int = -1
	switch ev := ev.(type) {
	case *bus.TaskStateChanged:

	case *bus.ContextBuilt:
		m.router, _ = m.router.Update(tabs.EventMsg{Event: ev})
		tabToFlash = int(tabRouter)

	case *bus.RouterExamining:
		m.router, _ = m.router.Update(tabs.EventMsg{Event: ev})
		tabToFlash = int(tabRouter)

	case *bus.RouterDecisionEvent:
		m.router, _ = m.router.Update(tabs.EventMsg{Event: ev})
		tabToFlash = int(tabRouter)

	case *bus.WorkerStarted:
		m.worker, _ = m.worker.Update(tabs.EventMsg{Event: ev})
		tabToFlash = int(tabWorker)

	case *bus.WorkerToolCall:
		m.worker, _ = m.worker.Update(tabs.EventMsg{Event: ev})
		tabToFlash = int(tabWorker)

	case *bus.WorkerFileEdit:
		m.worker, _ = m.worker.Update(tabs.EventMsg{Event: ev})
		tabToFlash = int(tabWorker)

	case *bus.WorkerFinished:
		m.worker, _ = m.worker.Update(tabs.EventMsg{Event: ev})
		tabToFlash = int(tabWorker)

	case *bus.ConflictDetected:
		m.worker, _ = m.worker.Update(tabs.EventMsg{Event: ev})
		tabToFlash = int(tabWorker)

	case *bus.ValidatorExamining:
		m.validator, _ = m.validator.Update(tabs.EventMsg{Event: ev})
		tabToFlash = int(tabValidator)

	case *bus.ValidatorCriterionResult:
		m.validator, _ = m.validator.Update(tabs.EventMsg{Event: ev})
		tabToFlash = int(tabValidator)

	case *bus.ValidatorVerdict:
		m.validator, _ = m.validator.Update(tabs.EventMsg{Event: ev})
		tabToFlash = int(tabValidator)

	case *bus.TaskFinalized:
		m.finalized = true
		m.finalState = ev.FinalState
		setCurrentTab(&m, tabTimeline)
		tabToFlash = int(tabTimeline)

	case *bus.OrchestratorError:
		tabToFlash = int(tabTimeline)
	}

	var flashCmd tea.Cmd
	if tabToFlash != -1 {
		m.tabStrip = m.tabStrip.Flash(tabID(tabToFlash))
		if !m.flashTickActive {
			m.flashTickActive = true
			flashCmd = tickTabFlash()
		}
	}

	return m, tea.Batch(waitForEvent(m.eventCh), flashCmd)
}

// View renders the header, separator, active tab content, and tab strip in a
// vertical stack using lipgloss.JoinVertical for proper width alignment.
func (m Model) View() string {
	if m.quitting {
		return ""
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
		padded = append(padded, blocks[0]) // header
		for i := 0; i < theme.SeparatorPadV; i++ {
			padded = append(padded, pad)
		}
		padded = append(padded, blocks[1]) // separator
		for i := 0; i < theme.SeparatorPadV; i++ {
			padded = append(padded, pad)
		}
		padded = append(padded, blocks[2:]...) // content + tabs
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
