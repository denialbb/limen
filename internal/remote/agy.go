package remote

import (
	"time"

	"github.com/denialbb/limen/internal/orchestrator"
)

// agyPrintTimeout is the wait bound passed to `agy --print-timeout`, shared by
// the agy worker and the agy validator. It is a Go duration string; agy's own
// default is 5m0s, widened here for real runs. If the validator's timeout is
// ever split out from the worker's, keep it >= the worker's — validation runs
// the project's tests, which take longer than a worker's edit turn.
const agyPrintTimeout = "10m"

// agyCommandArgs builds the argv to launch agy in headless print mode.
// `--print` takes the prompt as its VALUE, so it must be the last flag: the
// driver appends the rendered prompt as the final argv element.
// `--dangerously-skip-permissions` auto-approves tools. agy has no event stream
// (plain text only), so this is the thinnest dialect.
func agyCommandArgs(o *options) []string {
	args := []string{"agy", "--dangerously-skip-permissions", "--print-timeout", agyPrintTimeout}
	if o.workerModel != "" {
		args = append(args, "--model", o.workerModel)
	}
	// --print must stay last; the driver appends the prompt as its value.
	args = append(args, "--print")
	return args
}

// agyDialect describes agy's `--print` behavior for the generic agentWorker
// driver. agy is the thinnest one-shot-family dialect: the prompt is `--print`'s
// value, output is plain text with no event stream, and the driver scans to EOF.
// Only WorkerStarted/WorkerFinished surface (git-poll breadcrumbs are a future
// slice). agy's edit tool works, so no constraint block. The blocking
// ready-for-review bash call keeps the process alive across verdict rounds.
func agyDialect() dialect {
	return dialect{
		name:          "agy",
		baseArgs:      agyCommandArgs,
		promptViaArgv: true,
		encodeStdin:   nil,
		decode:        decodeAgyEvent,
		constraints:   "",
	}
}

// NewAgyWorker constructs an agy-backed worker on the generic agentWorker
// driver.
func NewAgyWorker(opts ...Option) orchestrator.Worker {
	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}
	return newAgentWorker(agyDialect(), o)
}

// decodeAgyEvent is agy's line decoder. agy emits only plain text (the final
// assistant message), so every line decodes to no events and never signals end;
// the driver relies on EOF. Text is still teed to the per-task log by the
// driver.
func decodeAgyEvent(line, taskID string, now time.Time) decodeResult {
	return decodeResult{}
}
