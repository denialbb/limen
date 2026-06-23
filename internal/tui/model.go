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

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

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
	// Configuration.
	taskID string
	bus    bus.EventBus

	// State.
	currentTab tabID
	width       int
	height      int

	// Sub-components. Held as pointers so value-receiver Update methods on
	// Model can still mutate their internal state.
	header    *Header
	tabStrip  *TabStrip
	router    *tabs.RouterTab
	worker    *tabs.WorkerTab
	validator *tabs.ValidatorTab
	timeline  *tabs.TimelineTab

	// Event pump.
	eventCh    <-chan bus.Event
	spinner    spinner.Model
	finalized  bool
	finalState state.TaskState

	// Quit flag, set when the user presses q or Ctrl+C.
	quitting bool
}

// NewModel constructs the top-level model. It subscribes to the provided
// event bus immediately so no event published between construction and the
// first tea.Cmd pump is lost (the channel is buffered to SubscriberBufferSize).
func NewModel(taskID string, eventBus bus.EventBus) Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot

	return Model{
		taskID:    taskID,
		bus:       eventBus,
		currentTab: tabRouter,
		header:    NewHeader(taskID),
		tabStrip:  NewTabStrip(),
		router:    tabs.NewRouterTab(),
		worker:    tabs.NewWorkerTab(),
		validator: tabs.NewValidatorTab(),
		timeline:  tabs.NewTimelineTab(),
		eventCh:   eventBus.Subscribe(),
		spinner:   sp,
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
//
// NOTE: This function is invoked as a tea.Cmd by the Bubble Tea runtime on a
// dedicated goroutine; the blocking receive here is intentional and does not
// stall the UI.
func waitForEvent(ch <-chan bus.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return busChannelClosedMsg{}
		}
		return busEventMsg{event: ev}
	}
}

// Update dispatches every message to the appropriate handler. It returns the
// updated model (value semantics) and a command chain that keeps the event
// pump and spinner running.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.WindowSizeMsg:
		return m.handleResize(msg)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		m.header.SetSpinner(m.spinner)
		return m, cmd

	case busEventMsg:
		return m.handleBusEvent(msg)

	case busChannelClosedMsg:
		// NOTE: Bus closed; drain is complete. Mark finalized if an explicit
		// TaskFinalized event was never received (defensive: covers a producer
		// that tears down without emitting the terminal event).
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
		m.forwardKeyToActiveTab(msg)
	case "k", "up":
		m.forwardKeyToActiveTab(msg)
	case "q", "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	default:
		// TODO: "?" help overlay keybinding is not implemented; deferred per
		// the TUI review. Implementing it requires a modal/overlay layer that
		// suspends the active tab's rendering while open, which is out of
		// scope for the v1 observe-only shell.
	}
	return m, nil
}

// setCurrentTab updates the active tab in both the model and the tab strip.
// It takes a pointer so the value-typed currentTab field on Model is mutated
// on the caller's copy rather than on a transient local.
func setCurrentTab(m *Model, id tabID) {
	m.currentTab = id
	m.tabStrip.SetActive(int(id))
}

// forwardKeyToActiveTab sends a scroll key to the viewport of the active tab
// so per-tab scroll position is preserved independently.
func (m Model) forwardKeyToActiveTab(msg tea.KeyMsg) {
	switch m.currentTab {
	case tabRouter:
		m.router.Update(msg)
	case tabWorker:
		m.worker.Update(msg)
	case tabValidator:
		m.validator.Update(msg)
	case tabTimeline:
		m.timeline.Update(msg)
	}
}

// handleResize propagates window size changes to every sub-component. The
// content viewport accounts for the two lines consumed by the header and the
// tab strip.
func (m Model) handleResize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.width = msg.Width
	m.height = msg.Height

	const reservedLines = 2 // header + tab strip
	contentHeight := msg.Height - reservedLines
	if contentHeight < 1 {
		contentHeight = 1
	}

	m.router.SetSize(msg.Width, contentHeight)
	m.worker.SetSize(msg.Width, contentHeight)
	m.validator.SetSize(msg.Width, contentHeight)
	m.timeline.SetSize(msg.Width, contentHeight)
	m.width = msg.Width
	m.header.SetWidth(msg.Width)
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
		// NOTE: Defensive: the bus already drops nil publishes, but a stray
		// nil here would panic downstream on kind(); keep pumping regardless.
		return m, waitForEvent(m.eventCh)
	}

	m.header.Update(ev)
	m.timeline.Update(tabs.EventMsg{Event: ev})

	switch ev := ev.(type) {
	case *bus.TaskStateChanged:
		// State transitions also flow to the header; no tab-specific action.

	case *bus.ContextBuilt:
		m.router.Update(tabs.EventMsg{Event: ev})

	case *bus.RouterExamining:
		m.router.Update(tabs.EventMsg{Event: ev})

	case *bus.RouterDecisionEvent:
		m.router.Update(tabs.EventMsg{Event: ev})

	case *bus.WorkerStarted:
		m.worker.Update(tabs.EventMsg{Event: ev})

	case *bus.WorkerToolCall:
		m.worker.Update(tabs.EventMsg{Event: ev})

	case *bus.WorkerFileEdit:
		m.worker.Update(tabs.EventMsg{Event: ev})

	case *bus.WorkerFinished:
		m.worker.Update(tabs.EventMsg{Event: ev})

	case *bus.ConflictDetected:
		m.worker.Update(tabs.EventMsg{Event: ev})

	case *bus.ValidatorExamining:
		m.validator.Update(tabs.EventMsg{Event: ev})

	case *bus.ValidatorCriterionResult:
		m.validator.Update(tabs.EventMsg{Event: ev})

	case *bus.ValidatorVerdict:
		m.validator.Update(tabs.EventMsg{Event: ev})

	case *bus.TaskFinalized:
		m.finalized = true
		m.finalState = ev.FinalState
		setCurrentTab(&m, tabTimeline)

	case *bus.OrchestratorError:
		// Routed to the Timeline tab only; it is the canonical "all activity"
		// view and already received the event above. No dedicated error tab
		// exists in the v1 observe-only shell.
	}

	// NOTE: The pump re-arms here even after TaskFinalized. This intentionally
	// deviates from the design doc's literal "stops the event pump" wording:
	// the orchestrator may still flush residual buffered events (e.g. a
	// trailing WorkerFinished or a state echo) before closing the bus
	// channel. Re-arming lets those drain cleanly; the pump stops for good
	// when the channel close resolves to busChannelClosedMsg, at which point
	// Update returns a nil command and the runtime idles.
	return m, waitForEvent(m.eventCh)
}

// View renders the persistent header, the tab strip, and the content of the
// active tab. The layout is vertical: header on top, tab strip beneath it,
// then the tab content fills the remaining viewport.
func (m Model) View() string {
	if m.quitting {
		return ""
	}

	padV := theme.SeparatorPadV
	sepLine := theme.SeparatorStyle().Render(strings.Repeat(theme.SeparatorRune, m.width))
	sep := strings.Repeat("\n", padV) + sepLine + strings.Repeat("\n", padV)
	content := m.activeTabView()
	return strings.Join([]string{m.header.View(), sep, content, m.tabStrip.View(m.width)}, "\n")
}

// activeTabView returns the rendered content of the currently selected tab.
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
		// NOTE: Unreachable for a valid tabID; defensive fallback.
		return ""
	}
}

// Finalized reports whether the orchestrator has reached a terminal state
// (or the bus was closed without an explicit finalize). Exposed for the CLI
// layer to print a final summary after the program exits.
func (m Model) Finalized() bool { return m.finalized }

// FinalState returns the terminal state captured from TaskFinalized, or the
// zero value if no finalize event was received.
func (m Model) FinalState() state.TaskState { return m.finalState }

// TaskID returns the task ID the TUI is observing.
func (m Model) TaskID() string { return m.taskID }

// String returns a compact one-line summary of the final state, suitable for
// post-program CLI output.
func (m Model) String() string {
	if !m.finalized {
		return fmt.Sprintf("task %s: not finalized", m.taskID)
	}
	return fmt.Sprintf("task %s: finalized state=%s", m.taskID, m.finalState)
}

// Compile-time interface check.
var _ tea.Model = Model{}