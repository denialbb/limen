Type: AFK

## Parent PRD

`docs/prd/spike_ndjson_demo.md`

## What to build

Build the in-process Python mock LLM runtime and the spike transcript, per PRD §"Mock LLM: lives in Python, transcript as a JSON file" and §"Python package layout". The mock returns scripted responses from a transcript file; swapping the mock for a real LLM is a Python-internal change that never touches the Go-Python contract.

`src/limen/mock/` contains:

- `runtime.py` — NDJSON stdin/stdout loop, transcript loader, `request_tool(name, args)` callback that emits a `tool_request` and blocks on the matching `tool_response`.
- `router.py` / `worker.py` / `validator.py` — per-role cognitive logic (which transcript key to read, what envelope to emit). Pure functions from `(transcript, request-envelope) -> result-envelope`, testable without subprocesses.
- `__main__.py` per role (three thin entrypoints, `python -m limen.mock.router` etc.), each calling `runtime.run_role("<role>", cognitive_fn)`.
- `transcripts/spike.json` — the spike transcript (shape per PRD §"Transcript shape"): one `router` proceed entry, two `worker` entries (initial buggy `file.write` to `solution.txt`, then corrected one), two `validator` entries (fail with off-by-one feedback, then pass).

Each role's list is consumed sequentially; the Nth invocation of a role returns the Nth entry. Transcript exhaustion fails loud via an error envelope (not silent repeat, not nonzero exit). The worker's bidirectional `file.write` loop lives inside `runtime.run_role`'s loop: the cognitive function emits tool-requests via `runtime.request_tool("file.write", {path, content})`, which blocks until Go responds.

Layer 2 unit tests in `tests/mock/` (no subprocess) drive the runtime via fake `sys.stdin`/`sys.stdout` pairs per PRD §"Layer 2: `tests/mock/`".

## Acceptance criteria

- [ ] `src/limen/mock/runtime.py` implements NDJSON loop, transcript loader, and `request_tool`
- [ ] `src/limen/mock/router.py` / `worker.py` / `validator.py` are pure `(transcript, request) -> result` functions
- [ ] Three role entrypoints (`python -m limen.mock.router` / `.worker` / `.validator`) each call `runtime.run_role`
- [ ] `src/limen/mock/transcripts/spike.json` matches the PRD §"Transcript shape" spec exactly
- [ ] Transcript exhaustion emits an error envelope (not silent repeat, not nonzero exit)
- [ ] `request_tool` emits a `tool_request`, blocks on the matching `tool_response`, returns the result
- [ ] `tests/mock/` covers: NDJSON loop reads result event and returns; `request_tool` round-trip; transcript loader parses + sequences per role + fails loud on exhaustion; per-role cognitive functions return correct envelope shape
- [ ] `pytest` passes

## Blocked by

- Blocked by `issues/001-cleanup-pull-model-surface.md` (avoid touching files slated for deletion; keep mock author from coordinating with cleanup)

## User stories addressed

- PRD §"Mock LLM: lives in Python, transcript as a JSON file"
- PRD §"Python package layout"
- PRD §"Layer 2: `tests/mock/`"

## Verify

```
pytest tests/mock/
python -m limen.mock.router < /dev/null
```