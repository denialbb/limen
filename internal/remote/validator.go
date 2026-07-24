package remote

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/denialbb/limen/internal/bus"
	"github.com/denialbb/limen/internal/git"
	"github.com/denialbb/limen/internal/orchestrator"
	"github.com/denialbb/limen/internal/state"
)

// verdictSentinelPrefix marks the validator's machine-readable verdict line. The
// agent is instructed to end its final message with exactly one such line; the
// JSON that follows is a state.Verdict.
const verdictSentinelPrefix = "LIMEN_VERDICT:"

// validatorDialect describes one agent CLI's launch surface when used as a
// Level-3 validator. Unlike worker dialects it needs no event decoder: the
// verdict is carried by the LIMEN_VERDICT stdout sentinel, so the driver only
// needs the argv (flags; the rendered validator prompt is appended by Evaluate).
type validatorDialect struct {
	name     string
	baseArgs func(o *options) []string
}

// agyValidatorArgs builds the argv to run agy as a validator. Mirrors the agy
// worker flags but keyed on validatorModel, and (like the worker) keeps --print
// last so the appended prompt is its value.
func agyValidatorArgs(o *options) []string {
	args := []string{"agy", "--dangerously-skip-permissions", "--print-timeout", agyPrintTimeout}
	if o.validatorModel != "" {
		args = append(args, "--model", o.validatorModel)
	}
	args = append(args, "--print")
	return args
}

// agyValidatorDialect is the agy Level-3 validator dialect.
func agyValidatorDialect() validatorDialect {
	return validatorDialect{name: "agy", baseArgs: agyValidatorArgs}
}

// claudeValidatorArgs runs claude as a validator in plain-text print mode (no
// stream-json), so the LIMEN_VERDICT sentinel appears as a raw stdout line. -p
// takes the prompt as its trailing positional; the driver appends it.
func claudeValidatorArgs(o *options) []string {
	args := []string{"claude", "-p", "--permission-mode", "bypassPermissions"}
	if o.validatorModel != "" {
		args = append(args, "--model", o.validatorModel)
	}
	return args
}

// claudeValidatorDialect is the claude Level-3 validator dialect.
func claudeValidatorDialect() validatorDialect {
	return validatorDialect{name: "claude", baseArgs: claudeValidatorArgs}
}

// opencodeValidatorArgs runs opencode as a validator in its default (text)
// output mode so the sentinel appears in stdout; the driver appends the prompt
// as the positional message.
func opencodeValidatorArgs(o *options) []string {
	args := []string{"opencode", "run", "--auto"}
	if o.validatorModel != "" {
		args = append(args, "-m", o.validatorModel)
	}
	return args
}

// opencodeValidatorDialect is the opencode Level-3 validator dialect.
func opencodeValidatorDialect() validatorDialect {
	return validatorDialect{name: "opencode", baseArgs: opencodeValidatorArgs}
}

// NewClaudeValidator constructs a claude-backed Level-3 validator.
func NewClaudeValidator(opts ...Option) orchestrator.Validator {
	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}
	return newAgentValidator(claudeValidatorDialect(), o)
}

// NewOpencodeValidator constructs an opencode-backed Level-3 validator.
func NewOpencodeValidator(opts ...Option) orchestrator.Validator {
	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}
	return newAgentValidator(opencodeValidatorDialect(), o)
}

// agentValidator implements orchestrator.Validator by spawning a headless agent
// CLI in the throwaway validator worktree with a Level-3 prompt (inspect the
// diff, run the tests) and parsing the LIMEN_VERDICT sentinel from its stdout.
//
// The stdout sentinel — not `limen submit-verdict` — is used deliberately: in
// the synchronous Evaluate flow the orchestrator owns verdict recording, and a
// callback write would race its bookkeeping (issue 015). The agent only reports
// over stdout; the orchestrator records the decision.
type agentValidator struct {
	dialect validatorDialect
	cmdArgs []string
	opts    *options
}

// newAgentValidator builds a validator from a dialect, resolving its argv from
// the options.
func newAgentValidator(d validatorDialect, o *options) *agentValidator {
	return &agentValidator{dialect: d, cmdArgs: d.baseArgs(o), opts: o}
}

// newAgentValidatorCmd builds a validator backed by an explicit argv, letting
// tests point a dialect at a fake script that prints a sentinel.
func newAgentValidatorCmd(d validatorDialect, args []string, o *options) *agentValidator {
	return &agentValidator{dialect: d, cmdArgs: args, opts: o}
}

// NewAgyValidator constructs an agy-backed Level-3 validator.
func NewAgyValidator(opts ...Option) orchestrator.Validator {
	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}
	return newAgentValidator(agyValidatorDialect(), o)
}

// renderValidatorPrompt renders the Level-3 validator prompt: inspect the diff,
// run the tests, and end the final message with exactly one LIMEN_VERDICT
// sentinel line. The validator never sees the worker's reasoning (epistemic
// isolation); it only sees the artifact in the worktree.
func renderValidatorPrompt(task *state.Task) string {
	return fmt.Sprintf(
		"You are validating a code change for task %s.\n\n"+
			"Task: %s\n\n"+
			"Inspect the change with `git diff`, then run the project's tests to verify\n"+
			"the change is correct and complete. Do not modify the solution — you are a\n"+
			"read-only reviewer.\n\n"+
			"When you are done, end your FINAL message with EXACTLY ONE line in this\n"+
			"format, and nothing after it:\n"+
			"%s {\"passes\":true,\"feedback\":\"<concise reason>\"}\n\n"+
			"Set passes to true only if the change is correct and the tests pass;\n"+
			"otherwise set passes to false and give actionable feedback the worker can\n"+
			"use to revise.",
		task.ID, task.Prompt, verdictSentinelPrefix,
	)
}

// Evaluate implements orchestrator.Validator. It runs the validator CLI in the
// worktree, captures stdout, and returns the verdict parsed from the last
// LIMEN_VERDICT sentinel. A missing sentinel is a transport error (not a
// correctness verdict) so it does not burn the retry budget; no verdict event is
// emitted in that case.
func (v *agentValidator) Evaluate(ctx context.Context, task *state.Task, wt *git.Worktree, em orchestrator.Emitter) (bool, string, error) {
	prompt := renderValidatorPrompt(task)
	args := append(append([]string(nil), v.cmdArgs...), prompt)

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = wt.Path

	// Prepend the limen binary dir to PATH for parity with workers (harmless;
	// the validator reports via the stdout sentinel, not a limen callback).
	if selfPath, err := os.Executable(); err == nil {
		selfDir := filepath.Dir(selfPath)
		cmd.Env = append(os.Environ(), fmt.Sprintf("PATH=%s:%s", selfDir, os.Getenv("PATH")))
	}

	var out strings.Builder
	var stdout io.Writer = &out
	if v.opts.logDir != "" {
		logPath := filepath.Join(v.opts.logDir, fmt.Sprintf("%s-validator.log", task.ID))
		if err := os.MkdirAll(v.opts.logDir, 0755); err == nil {
			if f, err2 := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err2 == nil {
				defer f.Close()
				stdout = io.MultiWriter(&out, f)
				cmd.Stderr = f
			}
		}
	}
	cmd.Stdout = stdout
	if cmd.Stderr == nil {
		cmd.Stderr = io.Discard
	}

	if em != nil {
		em.Publish(&bus.ValidatorExamining{
			TaskID:    task.ID,
			Criteria:  []string{"inspect diff", "run tests"},
			Timestamp: time.Now(),
		})
	}

	runErr := cmd.Run()

	// The sentinel is authoritative even if the CLI exited non-zero. Only when no
	// sentinel is present do we surface a transport error.
	verdict, parseErr := parseVerdictSentinel(out.String())
	if parseErr != nil {
		if ctx.Err() != nil {
			return false, "", ctx.Err()
		}
		if runErr != nil {
			return false, "", fmt.Errorf("%s validator: run failed and no verdict sentinel: %w", v.dialect.name, runErr)
		}
		return false, "", fmt.Errorf("%s validator: %w", v.dialect.name, parseErr)
	}

	if em != nil {
		em.Publish(&bus.ValidatorVerdict{
			TaskID:    task.ID,
			Passes:    verdict.Passes,
			Feedback:  verdict.Feedback,
			Timestamp: time.Now(),
		})
	}

	return verdict.Passes, verdict.Feedback, nil
}

// parseVerdictSentinel scans validator output for LIMEN_VERDICT lines and
// returns the verdict from the LAST one (an agent may reconsider mid-message).
// A missing sentinel or unparseable JSON is an error — the caller treats it as a
// transport failure, not a correctness verdict.
func parseVerdictSentinel(output string) (state.Verdict, error) {
	var last string
	found := false
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, verdictSentinelPrefix) {
			last = line
			found = true
		}
	}
	if !found {
		return state.Verdict{}, fmt.Errorf("no %s sentinel in validator output", verdictSentinelPrefix)
	}

	jsonPart := strings.TrimSpace(strings.TrimPrefix(last, verdictSentinelPrefix))
	verdict, err := state.UnmarshalVerdict([]byte(jsonPart))
	if err != nil {
		return state.Verdict{}, fmt.Errorf("parse verdict sentinel %q: %w", last, err)
	}
	return verdict, nil
}

var _ orchestrator.Validator = (*agentValidator)(nil)
