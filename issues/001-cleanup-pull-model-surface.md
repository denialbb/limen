Type: AFK

## Parent PRD

`docs/prd/spike_ndjson_demo.md`

## What to build

Delete the rejected pull-model Python surface and its broken tests in a single cleanup commit, per PRD §"Cleanup Commit (before spike work)". This removes files that invoke `limen worker ...` / `limen validator ...` subcommands which will never be implemented, plus the orphaned `FastMCP` prototype whose role mapping does not match L1/L2/L3 in `.agents/docs/role_boundaries.md`. Also resolves BUG #6 (`pyproject.toml` declares `dependencies = []` while `limen_mcp.py` imports `mcp` and `requests`) by removing the importing file.

## Acceptance criteria

- [ ] `src/limen/mcp_server/worker_server.py` deleted
- [ ] `src/limen/mcp_server/validator_server.py` deleted
- [ ] `src/limen/mcp_server/limen_mcp.py` deleted
- [ ] `src/limen/router/policy.py` and `src/limen/router/` deleted
- [ ] `tests/test_limen_mcp.py` deleted
- [ ] `tests/test_worker_server.py` and `tests/test_validator_server.py` deleted
- [ ] `tests/router/test_policy.py` deleted
- [ ] `tests/mcp_server/test_worker_server.py` and `tests/mcp_server/test_validator_server.py` deleted
- [ ] `src/limen/egg-info/` deleted (stale)
- [ ] `config/mpc_config.json` deleted (typo'd reference to orphan file)
- [ ] `pytest` passes with no remaining test that asserts against `NotImplementedError` stubs or shells out to missing subcommands
- [ ] `pyproject.toml` no longer references any removed module

## Blocked by

None - can start immediately.

## User stories addressed

- PRD acceptance criterion: "All deleted Python stubs and broken tests are gone; `pytest` no longer runs any test that asserts against `NotImplementedError` stubs or shells out to missing subcommands."
- BUG #6 (free via removal of `limen_mcp.py`)

## Verify

```
pytest
git status
```