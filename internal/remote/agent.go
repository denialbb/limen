package remote

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/denialbb/limen/internal/bus"
	"github.com/denialbb/limen/internal/git"
	"github.com/denialbb/limen/internal/orchestrator"
	"github.com/denialbb/limen/internal/retrieval"
	"github.com/denialbb/limen/internal/state"
)

// decodeResult is the outcome of decoding one line of an agent CLI's output
// stream: the bus events to emit and whether the line ends the run. It is the
// generic analogue of piDecodeResult, shared by every dialect.
type decodeResult struct {
	events []bus.Event
	end    bool
}

// dialect encapsulates one headless coding-agent CLI's wire protocol behind the
// generic agentWorker driver. Two families are expressed through the same
// fields:
//
//   - RPC family (pi, claude stream-json): promptViaArgv=false; the rendered
//     prompt is written to stdin via encodeStdin, the process stays alive, and
//     decode reports an explicit end event.
//   - One-shot family (opencode, agy): promptViaArgv=true; the prompt is an argv
//     element, stdin is closed immediately, and the driver scans stdout to EOF
//     (decode never reports end).
type dialect struct {
	// name identifies the dialect in errors and logs.
	name string
	// baseArgs builds the launch argv (binary + flags) from the options,
	// excluding the prompt. One-shot dialects have the rendered prompt appended
	// by the driver; RPC dialects deliver it over stdin.
	baseArgs func(o *options) []string
	// promptViaArgv selects the one-shot family: the rendered prompt is appended
	// to argv and stdin is closed immediately.
	promptViaArgv bool
	// encodeStdin renders the stdin prompt envelope for the RPC family. Unused
	// (and may be nil) when promptViaArgv is true.
	encodeStdin func(taskID, promptText string) []byte
	// decode translates one output line into bus events and an end signal. It is
	// a pure function over the raw line, task ID, and timestamp.
	decode func(line, taskID string, now time.Time) decodeResult
	// constraints is the dialect-specific constraint bullet block injected into
	// the worker prompt. Empty for dialects whose edit tools work; the shared
	// ready-for-review contract is added by renderWorkerPrompt regardless.
	constraints string
	// emitBreadcrumbs opts the dialect into git-poll breadcrumbs (PRD #13): the
	// driver polls `git status --porcelain` in the worktree while the CLI runs
	// and publishes the changed-file delta. It is true only for eventless
	// dialects (agy), which surface no native stream between WorkerStarted and
	// WorkerFinished. Event-rich dialects (pi/claude/opencode) leave it false —
	// their WorkerToolCall/WorkerFileEdit/WorkerAgentMessage streams already
	// carry the activity, and polling would only double the noise.
	emitBreadcrumbs bool
}

// agentWorker is the generic worker driver. It launches an agent CLI in the
// worktree, delivers the prompt per the dialect's family, tees stdout+stderr to
// a per-task log, and republishes the dialect's decoded events onto the bus. It
// satisfies orchestrator.Worker for every dialect.
type agentWorker struct {
	dialect dialect
	cmdArgs []string
	opts    *options
	// breadcrumbReader overrides the git-poll seam for tests. Nil in production:
	// ProduceSolution then builds gitStatusReader(wt.Path), binding the poll to
	// the worktree the CLI was launched in.
	breadcrumbReader breadcrumbReader
	// breadcrumbInterval overrides the poll cadence for tests. Zero means the
	// breadcrumbInterval default (~1.5s, PRD #13).
	breadcrumbInterval time.Duration
}

// pollInterval resolves the breadcrumb cadence: the injected override when a
// test set one, otherwise the PRD #13 default.
func (w *agentWorker) pollInterval() time.Duration {
	if w.breadcrumbInterval > 0 {
		return w.breadcrumbInterval
	}
	return breadcrumbInterval
}

// newAgentWorker builds a driver from a dialect, resolving its launch argv from
// the options.
func newAgentWorker(d dialect, o *options) *agentWorker {
	return &agentWorker{dialect: d, cmdArgs: d.baseArgs(o), opts: o}
}

// newAgentWorkerCmd builds a driver backed by an explicit argv, letting tests
// point a dialect at a fake script that emits its wire format.
func newAgentWorkerCmd(d dialect, args []string, o *options) *agentWorker {
	return &agentWorker{dialect: d, cmdArgs: args, opts: o}
}

// renderWorkerPrompt renders the shared worker prompt contract: task ID, task
// prompt, the retrieval context manifest (ADR 0007), the dialect's own
// constraint block, and the driver-neutral ready-for-review instruction. Prior
// validator feedback is appended when present.
func renderWorkerPrompt(task *state.Task, feedback, constraints string) string {
	contextSection := ""
	if task.ContextSnapshot != "" {
		if manifest, err := retrieval.ParseManifest(task.ContextSnapshot); err == nil && len(manifest.Chunks) > 0 {
			contextSection = "\n\n" + retrieval.RenderContextSection(manifest)
		}
	}

	prompt := fmt.Sprintf(
		"Task ID: %s\n\nTask: %s%s\n\nIMPORTANT CONSTRAINTS:\n%s"+
			"- When you are finished, you MUST run: `limen ready-for-review --task-id %s --summary \"<summary>\"`. Wait for the verdict. If approved, you can finish. If rejected with feedback, revise your work and call ready-for-review again.",
		task.ID, task.Prompt, contextSection, constraints, task.ID,
	)
	if feedback != "" {
		prompt += fmt.Sprintf("\n\nPrevious feedback:\n%s", feedback)
	}
	return prompt
}

// ProduceSolution implements orchestrator.Worker. The control flow mirrors the
// original piWorker: launch in the worktree with the limen binary on PATH, tee
// stdout+stderr to <logDir>/<task>-worker.log, close the stdout pipe on
// cancellation (exec kills only the direct child), publish
// WorkerStarted/WorkerFinished around the dialect's decoded events, and — for
// the RPC family — close stdin on the end event so the CLI exits cleanly.
func (w *agentWorker) ProduceSolution(ctx context.Context, task *state.Task, wt *git.Worktree, feedback string, em orchestrator.Emitter) error {
	promptText := renderWorkerPrompt(task, feedback, w.dialect.constraints)

	args := append([]string(nil), w.cmdArgs...)
	if w.dialect.promptViaArgv {
		args = append(args, promptText)
	}

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = wt.Path

	// Prepend the limen binary's directory to PATH so the agent can call
	// `limen ready-for-review` without knowing the absolute path.
	if selfPath, err := os.Executable(); err == nil {
		selfDir := filepath.Dir(selfPath)
		cmd.Env = append(os.Environ(), fmt.Sprintf("PATH=%s:%s", selfDir, os.Getenv("PATH")))
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("%s worker: stdin pipe: %w", w.dialect.name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("%s worker: stdout pipe: %w", w.dialect.name, err)
	}

	// Tee the agent's stdout to the log file so raw events are captured for
	// debugging. Its stderr also goes to the log file (not os.Stderr) so it
	// doesn't bleed through the alt screen TUI and corrupt the display.
	var reader io.Reader = stdout
	if w.opts.logDir != "" {
		logPath := filepath.Join(w.opts.logDir, fmt.Sprintf("%s-worker.log", task.ID))
		if err := os.MkdirAll(w.opts.logDir, 0755); err == nil {
			if f, err2 := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err2 == nil {
				defer f.Close()
				reader = io.TeeReader(stdout, f)
				cmd.Stderr = f
			}
		}
	}
	if cmd.Stderr == nil {
		cmd.Stderr = io.Discard
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s worker: start: %w", w.dialect.name, err)
	}

	// Close the stdout pipe when the context is cancelled so the scanner below
	// is unblocked even if the agent's bash child processes are still alive and
	// holding the write end of the pipe open (exec.CommandContext only kills the
	// direct child, not its descendants).
	stopPipeClose := context.AfterFunc(ctx, func() { stdout.Close() })

	if em != nil {
		em.Publish(&bus.WorkerStarted{
			TaskID:       task.ID,
			WorktreePath: wt.Path,
			BaseCommit:   wt.BaseCommit,
			Retry:        task.RetryCount,
			Timestamp:    time.Now(),
		})
	}

	// Start git-poll breadcrumbs for eventless dialects (PRD #13), after
	// WorkerStarted: they are only meaningful while the CLI is running. The
	// poller is fire-and-forget observability — it publishes onto the bus and
	// can never fail ProduceSolution. stopBreadcrumbs is deferred so every exit
	// path (including the error returns below) tears the goroutine down.
	stopBreadcrumbs := func() {}
	if w.dialect.emitBreadcrumbs {
		read := w.breadcrumbReader
		if read == nil {
			read = gitStatusReader(wt.Path)
		}
		pollCtx, cancelPoll := context.WithCancel(ctx)
		pollDone := make(chan struct{})
		go func() {
			defer close(pollDone)
			pollBreadcrumbs(pollCtx, w.pollInterval(), read, task.ID, em)
		}()
		var stopOnce sync.Once
		stopBreadcrumbs = func() {
			stopOnce.Do(func() {
				cancelPoll()
				// Wait for the goroutine so no stale breadcrumb can land after
				// WorkerFinished, but only briefly: a wedged reader must not hold
				// the worker's completion hostage.
				select {
				case <-pollDone:
				case <-time.After(breadcrumbStopGrace):
				}
			})
		}
		defer stopBreadcrumbs()
	}

	// Deliver the prompt per family. One-shot: prompt is in argv, so close stdin
	// immediately. RPC: write the stdin envelope and keep the pipe open until the
	// end event.
	if w.dialect.promptViaArgv {
		stdin.Close()
	} else {
		if _, err := stdin.Write(w.dialect.encodeStdin(task.ID, promptText)); err != nil {
			return fmt.Errorf("%s worker: write prompt: %w", w.dialect.name, err)
		}
	}

	scanner := bufio.NewScanner(reader)
	// Agent event lines (e.g. claude's stream-json init) can exceed the default
	// 64KiB token limit; grow the buffer to tolerate large lines.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		res := w.dialect.decode(scanner.Text(), task.ID, time.Now())
		if res.end {
			// NOTE: any res.events on the end line are intentionally dropped — no
			// dialect emits both events and end on one line today (pi's agent_end /
			// claude's result carry no worker events). A future dialect that does
			// must publish res.events here before breaking.
			// Signal EOF so the RPC CLI can exit cleanly rather than blocking on
			// stdin.
			stdin.Close()
			break
		}
		if em == nil {
			continue
		}
		for _, ev := range res.events {
			em.Publish(ev)
		}
	}

	stopPipeClose() // no-op if context already fired; prevents goroutine leak on clean exit

	if err := scanner.Err(); err != nil && err != io.EOF {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%s worker: read stdout: %w", w.dialect.name, err)
	}

	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%s worker: wait: %w", w.dialect.name, err)
	}

	// Stop the poller before WorkerFinished so the breadcrumb stream cannot
	// trail past the end of the run.
	stopBreadcrumbs()

	if em != nil {
		em.Publish(&bus.WorkerFinished{
			TaskID:    task.ID,
			Timestamp: time.Now(),
		})
	}

	return nil
}

var _ orchestrator.Worker = (*agentWorker)(nil)
