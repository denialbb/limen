# Sandbox untrusted repository agents

Status: Approved for implementation by denial (2026-11-01)
Implementer: Herdr agy workers; planner/reviewer: pi

## Goal

Confine every model-backed worker and validator to a per-task sandbox so that an
untrusted repository cannot read host secrets, write outside its worktree, or
reach the network except through an allowlisted, credential-brokering host
process.

## Locked behavior

- Repositories are untrusted by default. Trust is granted only by the operator on
  the command line (`--trust-repo`), never by content inside the repository.
  There is no flag that disables sandboxing.
- A sandboxed process tree:
  - sees only a minimal read-only system tree, its own worktree (read-write), a
    private `/tmp`, and a private HOME;
  - has no host secrets: no `$HOME` contents, no `~/.ssh`, `~/.aws`,
    `~/.config/gh`, no provider credential files or credential env vars;
  - cannot write outside its worktree or private `/tmp`;
  - has an isolated network namespace with loopback only.
- Egress leaves the sandbox only through a host-side broker reached over a
  bind-mounted unix socket, bridged to loopback inside the sandbox. The broker
  allowlists destinations by host:
  - workers may reach only their configured provider endpoints;
  - workers of a trusted repository may additionally reach the explicitly
    allowlisted package registries;
  - validators and any test command they run get no broker at all (zero egress).
- Provider credentials stay on the host. Broker-capable backends receive a dummy
  credential and a base URL pointing at the in-sandbox bridge; the broker
  replaces the dummy with the host credential. Backends that cannot be pointed at
  a base URL (agy) get a read-only mount of their own credential file and are
  limited to their provider hosts by the CONNECT allowlist.
- Untrusted repositories must vendor their dependencies. A missing dependency is
  a hard validation failure naming the command that failed; there is no
  host-side dependency pre-fetch.
- Linux only. If bubblewrap or unprivileged user namespaces are unavailable, the
  run fails closed with an actionable error. Other platforms are reported
  unsupported.
- The `mock` backend is a test double, runs no model and no network, and is not
  sandboxed. Every real agent backend is.

## Sandbox shape

`internal/sandbox` owns the whole boundary; callers describe *what* runs and
*how much* egress it gets, not how to invoke bubblewrap.

- `Policy` — worktree path, trust class, registry allowlist, provider descriptor,
  extra read-only mounts, callback socket.
- `Available()` — verifies bubblewrap and unprivileged user namespaces; returns
  an actionable error otherwise.
- `Wrap(*exec.Cmd, Policy)` — rewrites the command into the bubblewrap invocation
  and scrubs the environment. The worker/validator code keeps using `exec.Cmd`
  and does not learn bubblewrap's flags.
- `Broker` — host-side unix-socket HTTP endpoint. Two request shapes:
  - absolute-URI plain HTTP (base-URL backends): inject the host credential and
    forward to the provider endpoint over TLS;
  - `CONNECT` (proxy-honoring backends): allowlist the destination host, tunnel.
- `Bridge` — sandbox-side listener that forwards loopback TCP to the broker
  socket. Started inside the sandbox's network namespace.
- `CallbackServer` / `CallbackClient` — mediates `limen ready-for-review`. The
  sandbox holds a socket, not the orchestration database, so a worker cannot
  forge verdicts or corrupt state.

## Backends

| backend  | credential handling | egress |
|----------|--------------------|--------|
| pi       | broker: generated `models.json` base URL + `--api-key` dummy | provider only |
| claude   | broker: `ANTHROPIC_BASE_URL` + dummy token | provider only |
| opencode | broker: generated config base URL + dummy key | provider only |
| agy      | read-only `~/.gemini/oauth_creds.json` mount | agy Google hosts only |
| mock     | none | none, unsandboxed (test double) |

A backend whose brokering cannot be expressed fails closed with a clear error
rather than silently running with host credentials.

## Wiring

- `internal/remote/agent.go` — wrap the worker launch; no inherited environment.
- `internal/remote/validator.go` — wrap the validator launch with zero egress.
- `cmd/limen/main.go` — flags (`--trust-repo`, `--allow-registry`), broker and
  callback lifecycle around a task, `limen sandbox-bridge` entry point, and the
  callback client in `ready-for-review`.
- Throwaway validation worktrees stay under `<repo>/.limen/worktrees/` so the
  sandbox root is stable.

## Tests

- Host secrets unreadable; worktree writes succeed; out-of-worktree writes fail.
- Validator process cannot open a TCP connection at all.
- Worker reaches an allowlisted provider through the broker with the dummy
  credential replaced; a non-allowlisted host is refused.
- Trusted-repository worker reaches an allowlisted registry; untrusted does not.
- The broker socket never exposes the credential to the sandboxed process
  (`/proc/<pid>/environ` and mounted files contain only the dummy).
- `ready-for-review` round-trips through the callback server without the sandbox
  touching the database.
- `Available()` fails closed with an actionable message when bubblewrap is
  missing.

## Workflow and verification

- Branch `feat/sandbox-untrusted-agents`, separate PR from
  `fix/fatal-verdict-retry-transition`. TDD, one vertical slice per commit.
- `go test ./...`, `bash scripts/qa-check.sh`, and the sandbox tests must pass
  with bubblewrap present; the suite must also pass where bubblewrap is absent by
  exercising the fail-closed path rather than skipping silently.
- Do not commit `.herdr/` progress artifacts.
