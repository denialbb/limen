## Parent PRD

`docs/prd/real_agent_worker_validator.md`

## What to build

Update the architecture and design documentation in `.agents/docs/` to reflect the new state of the world following the Real-Agent Worker + Validator PRD.

## Acceptance criteria

- [ ] Update `.agents/docs/determinism_boundary.md` to note that fine-grained streams stay ephemeral by principle.
- [ ] Add the Safety/sandbox known-gap note (trusted posture this slice, Docker encapsulation later).
- [ ] Document the driver seam and process topology in `.agents/docs/current_architecture.md` (or a new file).

## Blocked by

None - can start immediately

## User stories addressed

- Doc Tasks

## Verify

`grep -r "ephemeral by principle" .agents/docs/determinism_boundary.md`
