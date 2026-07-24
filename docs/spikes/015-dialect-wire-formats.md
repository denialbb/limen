# Spike 015-0: Multi-Driver Dialect Wire Formats

Throwaway de-risking spike for issue 015 (slice 0). Captured live 2026-07-24
against the CLIs installed on this machine. Records the exact wire formats each
driver dialect must decode, so slices 1–5 can be written test-first against
faithful fixtures.

Versions: claude 2.1.195, opencode 1.18.4, agy 1.1.5, pi 0.81.1.

## claude — `-p --output-format stream-json --verbose` (RPC family)

Flags proven: `--output-format stream-json --verbose --permission-mode
bypassPermissions`. `--verbose` is REQUIRED alongside stream-json under `-p`.
Auth: subscription/OAuth worked with `apiKeySource:"none"` — do NOT pass
`--bare` (would force ANTHROPIC_API_KEY), matching the spec warning.

Output is NDJSON, one JSON object per line. Observed sequence for a prompt that
ran one bash tool then replied:

- `{"type":"system","subtype":"hook_started",...}` / `"hook_response"` — hook
  noise, SKIP.
- `{"type":"system","subtype":"init","cwd":...,"model":...,"tools":[...]}` —
  session start. (cwd honours `cmd.Dir`; in prod we set it to the worktree.)
- `{"type":"assistant","message":{"role":"assistant","content":[...]}}` — the
  content array carries parts, one part kind per event in practice but iterate
  the array like piWorker's `turn_end`:
  - `{"type":"thinking","thinking":"...","signature":"..."}` → WorkerAgentMessage(kind=thinking)
  - `{"type":"text","text":"..."}` → WorkerAgentMessage(kind=message)
  - `{"type":"tool_use","id":...,"name":"Bash","input":{...}}` → WorkerToolCall(tool=name, args=json(input))
- `{"type":"user","message":{"content":[{"type":"tool_result",...}]}}` — tool
  output echo, SKIP.
- `{"type":"rate_limit_event",...}` — SKIP.
- `{"type":"result","subtype":"success","result":"done",...}` — END event
  (analogue of pi's `agent_end`). `result` field is the final text.

### Prompt-delivery caveat (stdin stream-json)

`--input-format stream-json` with a single user envelope delivered via a file
redirect (`< file.jsonl`) emitted only the SessionStart hook lines then exited —
the instant EOF from the file was mishandled (no init/result). This is a
file-redirect artifact, NOT a format problem: production keeps the stdin pipe
OPEN (StdinPipe) and closes it only after the `result` event, exactly as
piWorker does on `agent_end`. The output wire format above is identical
regardless of how the prompt is delivered.

Resolved (2026-07-24): the file-EOF failure was an artifact of the file
redirect, not the format. The generic driver keeps the stdin pipe open and
closes on `result`, and the gated real-binary suite passed against claude
2.1.195 (stdin keep-open confirmed end-to-end). The argv fallback below was not
needed.

Fallback if stdin-keep-open proves flaky in slice 2: deliver the prompt as argv
(`claude -p "<prompt>" --output-format stream-json --verbose`). This was proven
end-to-end here (init → assistant thinking → tool_use → tool_result → assistant
text → result), and the blocking `limen ready-for-review` bash call keeps the
process alive across verdict rounds either way (the tool call blocks inside the
agent loop; prompt-delivery mechanism is orthogonal).

User envelope shape for stdin delivery:
`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"..."}]}}`

## opencode — `run --format json "<prompt>"` (one-shot family)

Permission bypass: `--auto`. Prompt is argv. Output is NDJSON, one object per
line. No explicit end event → scan stdout to EOF. Observed:

- `{"type":"step_start","part":{...}}` — SKIP.
- `{"type":"tool_use","part":{"tool":"bash","state":{"status":"completed","input":{"command":"echo hi"},"output":"hi\n",...}}}`
  → WorkerToolCall(tool=part.tool, args=json(part.state.input)).
- `{"type":"text","part":{"text":"done",...}}` → WorkerAgentMessage(kind=message).
- `{"type":"step_finish","part":{"reason":"tool-calls"|"stop",...}}` — SKIP.
  `reason:"stop"` marks the final step but EOF is the driver's end signal.

No `thinking`/reasoning parts surface in `--format json` (reasoning tokens are
counted in `step_finish.tokens` but not emitted as parts). Prompt/argv delivery;
the blocking `ready-for-review` bash call keeps `run` alive across rounds.

## agy — `--print "<prompt>" --print-timeout <dur>` (one-shot, no events)

Permission bypass: `--dangerously-skip-permissions`. `--print-timeout` is a Go
duration (default 5m0s). Output is PLAIN TEXT — the final assistant message only,
no event stream. `Reply with exactly: done` produced `done\n` on stdout, empty
stderr.

Driver: thinnest. Run to EOF, tee to the log, emit WorkerStarted/WorkerFinished
only (no per-event breadcrumbs; git-poll breadcrumbs are a future slice). The
agent invokes `limen ready-for-review` via its own bash tool mid-run.

## Validator (agy first, slice 5)

agy's plain-text stdout is the substrate for the LIMEN_VERDICT sentinel: the
validator prompt instructs the agent to end its final message with exactly one
`LIMEN_VERDICT: {"passes":...,"feedback":"..."}` line; Evaluate parses the LAST
such line from captured stdout. No event stream needed.

## Conclusions vs. the plan

No contradictions requiring a design change. The RPC-vs-one-shot split holds:
claude joins pi in the RPC family (rich events, explicit `result` end); opencode
and agy are one-shot (scan to EOF). The one open item is claude stdin-keep-open,
tracked above with a proven argv fallback.
