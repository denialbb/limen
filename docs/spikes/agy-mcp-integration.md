# Spike: agy + MCP as an integration layer for status/progress breadcrumbs

Researched 2026-07-27 by a claude instance (Opus 4.8) on `main`, in response to
`issues/research-agy-mcp.md`. Question: could driving **agy** (Antigravity CLI)
through **MCP** — limen exposing MCP tools agy calls mid-task — work *better*
than the current subprocess-callback model, specifically for **status updates /
progress breadcrumbs**?

**Verdict up front: no — reject for this purpose now.** MCP is a model-gated
*pull* channel for agent→limen *actions*; the need here is a reliable
limen-side *observability stream*. Those are different problems, and MCP solves
the one limen already has covered (bash-callable `limen` binary) while not
solving the one that's actually open (progress breadcrumbs). The documented
alternative — git-poll breadcrumbs (PRD #13) — is deterministic,
dialect-agnostic, an order of magnitude cheaper, and doesn't fight the sandbox
arc. Details below.

## Sourcing note (read this)

The brief asked for web verification of agy's MCP capabilities and the
project-local-config issue. **WebSearch/WebFetch were denied in this session's
permission mode.** I substituted a stronger primary source: the **actual `agy`
binary and its config on this machine** (the same install used in the 017 /
smoke runs, agy 1.1.5 lineage). Where a claim rests on that, it's marked
**[verified-local]**. Two items rest on web I couldn't reach and are marked
**[unverified-web]**: (a) the exact text/status of `antigravity-cli` issue #60,
and (b) a survey of community projects wrapping `agy` over MCP. For (a) I
verified the underlying *mechanism* locally, which is what matters for the
decision.

## Verified facts

### agy is a mature MCP **client** — not speculative [verified-local]

`agy changelog` (this install) shows deep, shipped MCP-client support:

- MCP **OAuth** (with issuer-validation relaxations for Salesforce/Atlassian),
  remote MCP servers, an authenticate flow in the `/mcp` panel.
- MCP **tool results** (incl. embedded resources / inline media) surface in the
  conversation; per-connection, per-tool-listing, and per-tool-call **timeouts**
  (default bumped to 60s; configurable, `-1` disables); **parallelized** server
  init so a slow server doesn't block fast ones.
- Servers configured in **`mcp_config.json`**, incl. `url`-based (remote) servers.
- Custom Markdown agents (`agent.md`, YAML frontmatter) expose an **`inheritMcp`**
  field and can load MCP tools.

So "agy is an MCP client that invokes MCP tools mid-run" is **real and
battle-tested**. In `--print` (`-p`) mode agy runs the full agentic loop
non-interactively; MCP tools join the same tool registry as its built-in
Read/Edit/Bash and are called when the model decides to — i.e. **mid-run tool
invocation is mechanically feasible**. [verified-local, inference from the
agentic-loop + tool-registry design]

### MCP config is HOME-scoped, with no per-invocation override [verified-local]

- The config file is **`~/.gemini/config/mcp_config.json`** — present on this
  machine. The changelog corroborates a migration from a legacy
  `~/.gemini/mcp_config.json` to the `config/mcp_config.json` path (it even ships
  a fix for a "configuration path mismatch" and for a Disable button writing to
  the legacy path). Either way the file is **under `~/.gemini`**, i.e.
  machine-global.
- `agy --help`, `agy plugin --help`: **no `mcp` subcommand**, **no
  `--mcp-config` flag**, and **`--print` mode exposes no MCP-scoping option**.
  MCP is purely config-file driven from the HOME dir.
- `inheritMcp` on a custom agent only toggles whether that agent *inherits* the
  globally-defined servers; it does **not** let a repo define its own servers.

**Consequence (the scoping problem, mechanism-verified):** any limen-owned MCP
server must be registered in the user's global `~/.gemini/config/mcp_config.json`
and will then load for **every agy invocation on the machine** — including the
user's own interactive agy sessions — not just limen tasks. There is no flag,
env var, or project-local file to scope it to a task or repo. This is the
mechanism behind the reported "project-local config ignored" behavior
(issue #60). [issue #60 text itself: **unverified-web**; mechanism:
**verified-local**]

## Mechanical-feasibility assessment

Feasible ≠ fit. agy *will* call a limen MCP tool mid-run — but the value
direction is wrong for the stated goal:

- **MCP tool calls are PULL, gated by the model.** For limen to receive
  progress, the model must *voluntarily and repeatedly* call a
  `report_progress` / `update_status` tool. Models don't reliably self-narrate
  progress that way; it burns tokens and context window, and the cadence is
  unpredictable. limen **cannot guarantee a breadcrumb every N seconds** — it
  can only hope the model calls the tool.
- **What MCP is actually good at** is giving the agent *new capabilities* —
  structured actions limen wants the agent to *take* (e.g. `ready-for-review`,
  `submit-verdict`, or a retrieval `fetch_context` pull). That is the **callback
  channel**, which limen already covers via the bash-callable `limen` binary over
  SQLite (ADR 0009 §1), uniformly for every bash-capable CLI.

So the premise "MCP → better status updates" conflates two channels:
(a) **agent→limen callbacks** — already solved, transport-agnostic; and
(b) **limen→observer progress stream** — which MCP does **not** provide, because
the model gates the calls. MCP would at best be an *alternative transport for the
same callbacks limen already has*, not a new observability stream.

## Scoping analysis — is a limen MCP server acceptable?

No, not cleanly:

- **Global blast radius.** Registered in `~/.gemini/config/mcp_config.json`, the
  server loads for the user's *every* agy run, polluting the global tool
  namespace outside limen tasks.
- **Invasive, racy state mutation.** limen would have to *write the user's global
  gemini config* as a side effect of running a task, with no scoping flag to
  avoid it — stateful, racy across concurrent tasks, and awkward to clean up.
- **Weakens task/identity binding (PRD #10).** Identity is meant to be bound at
  the trusted spawn boundary (cwd = worktree + spawn-time args), *never* via
  agent-supplied IDs. A model-invoked MCP tool must pass a `task_id` the model
  could get wrong; against a single machine-global server that's a path to
  addressing the wrong task. The subprocess model's cwd/worktree binding is
  strictly stronger.
- **`inheritMcp` doesn't rescue it.** It toggles inheritance of a
  globally-defined server; it doesn't give per-repo server definitions.

## Buy vs. cost — MCP vs. git-poll breadcrumbs (PRD #13)

Note: git-poll is **also unbuilt today** — `git status --porcelain` appears only
in worktree provisioning (`internal/git/worktree.go:82`) and tests; `agy.go:34`
flags breadcrumbs as "a future slice." So this is prospective-vs-prospective.

| Dimension | MCP tool (limen server) | git-poll (PRD #13) |
|---|---|---|
| Structured vs guessed | Structured *if* the model calls it; often it won't | Guessed from FS deltas (coarse, no intent) |
| Push vs pull | Pull, **model-gated** (worst kind — depends on model goodwill) | Pull, **deterministic** (limen controls cadence) |
| Cadence guarantee | None | ~1.5s, delta-only, gitignore-aware |
| Coupling | Tight: agy config surface + global-state mutation + auth/lifecycle | Only to `git` (already a dependency) |
| Dispatch symmetry | agy/MCP-client-specific; RPC-family CLIs already emit richer native events, so MCP adds nothing there | Uniform agnostic fallback; also covers any *future* eventless CLI |
| Maintenance | New server, config injection, task routing, timeouts, cleanup | One goroutine diffing porcelain output |
| Sandbox arc (PRD #15) | Server must be reachable *into* the Docker sandbox; global HOME-config injection fights containerization | Reads the worktree filesystem — trivially inside the sandbox |

git-poll wins on every axis that matters for *observability*. MCP's only genuine
edge (structured events) is exactly the property it can't guarantee here.

## Does this fire ADR 0009's revisit trigger?

**No.** The trigger is *"an agent with no shell execution, or a pull-model
retrieval contract."* agy **has** shell and uses it (the blocking
`limen ready-for-review` bash call is how in-process revision works), and
retrieval is **push-via-prompt** (ADR 0007, baked into the spawn prompt). Neither
condition holds.

Crucially, the "status updates" need is a **different need** than the trigger
anticipated. The trigger is about the *callback/tool channel* becoming
*necessary* (when bash is unavailable, or when retrieval must be *pulled*).
Progress observability is orthogonal: it's about **limen watching the agent**,
not the agent calling limen — and MCP addresses it poorly (model-gated) anyway.
So this question neither fires the trigger nor is well-served by the mechanism
the trigger would unlock.

## Recommendation

**Reject MCP-as-integration-layer for agy status/progress now.** Four reasons:

1. **Wrong tool for the job.** MCP yields model-gated *pull* tool-calls, not a
   reliable progress stream; it cannot deliver "breadcrumbs every N seconds."
2. **Not scopable.** HOME-level-only config forces global, machine-wide,
   invasive mutation of the user's gemini config; violates per-task isolation
   (PRD #10) and fights the sandbox arc (PRD #15).
3. **Redundant channel.** The callback need it *could* serve is already served
   by the bash-callable `limen` binary over SQLite (ADR 0009 §1), uniformly
   across all dialects.
4. **Asymmetric and narrow.** It adds a path only for the eventless dialect,
   while the real fix — git-poll breadcrumbs (PRD #13) — is agnostic,
   deterministic, cheaper, and already specced.

**Instead:** build git-poll breadcrumbs (PRD #13) for agy observability. It's
the agent-agnostic fallback the PRD already chose, and it closes the
`WorkerStarted → (black box) → WorkerFinished` gap for *every* eventless dialect,
not just agy.

**Defer MCP to a specific future arc, gated on the real trigger.** If ADR 0009's
revisit trigger actually fires — a **no-shell gated CLI**, or a **pull-model
retrieval contract** — MCP becomes the callback/tool transport of last resort,
exactly as ADR 0009 §1 and PRD #14/#18 already frame it. One legitimate narrow
case to watch: exposing limen's **retrieval** as an MCP `fetch_context` tool so a
gated agent can *pull* context. That *is* the ADR 0007 pull-contract case — i.e.
the actual trigger — but it's **retrieval, not status/progress**, and out of
scope for this question.

## Open items (would close with web access)

- **[unverified-web]** Exact text/status of `antigravity-cli` issue #60
  (project-local MCP config ignored). Mechanism verified locally; the issue
  thread would confirm maintainer intent / any fix in flight.
- **[unverified-web]** Survey of community projects wrapping `agy` over MCP —
  would either surface a counter-example to the model-gated-pull limitation or
  corroborate it. Nothing in this analysis depends on finding one; the
  limitation is structural to how MCP tool-calling works, not agy-specific.
