Type: HITL

## Parent PRD

`docs/prd/spike_ndjson_demo.md`

## What to build

Human-in-the-loop verification that the TUI displays the full spike event sequence when the same `limen run-task` command is run interactively, per PRD acceptance criterion "The TUI displays the full spike event sequence (router proceed, worker file edit, validator fail, worker retry, validator pass, commit) when the same command is run interactively."

The TUI is already implemented; this slice is a manual demo verification step against the wired-up spike backend. If the TUI does not render the expected sequence, file follow-up issues for specific TUI gaps (do not block the spike's contract claim on TUI polish).

## Acceptance criteria

- [ ] `limen run-task --task-id spike-demo --mock --mock-transcript src/limen/mock/transcripts/spike.json` run interactively displays the TUI
- [ ] TUI shows router proceed event
- [ ] TUI shows worker file edit event (`solution.txt`)
- [ ] TUI shows validator fail event (off-by-one feedback)
- [ ] TUI shows worker retry event
- [ ] TUI shows validator pass event
- [ ] TUI shows commit event (task `COMMITTED`)

## Blocked by

- Blocked by `issues/006-end-to-end-integration-test.md` (the binary must run end-to-end before a human can watch the TUI)

## User stories addressed

- PRD acceptance criterion: "The TUI displays the full spike event sequence (router proceed, worker file edit, validator fail, worker retry, validator pass, commit) when the same command is run interactively."

## Verify

```
limen run-task --task-id spike-demo --mock --mock-transcript src/limen/mock/transcripts/spike.json
```