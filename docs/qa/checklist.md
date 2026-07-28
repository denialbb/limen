# QA checklist

The pre-PR and pre-release verification pass for limen. Work top to bottom: the
steps are ordered cheapest-first, so a formatting mistake fails in seconds
instead of after the race suite.

To run the whole list unattended, use the script that automates it:

```bash
bash scripts/qa-check.sh          # every step, summary table, exit 0 only if all pass
bash scripts/qa-check.sh --list   # show the step names
bash scripts/qa-check.sh --help   # options
```

Invoking it through `bash` means it works even if the executable bit did not
survive checkout — the same reason `Makefile` calls `run-gates.sh` that way. Once
`chmod +x scripts/qa-check.sh` has been applied, `./scripts/qa-check.sh` works
too.

The script is the executable form of this document. If the two ever disagree,
the script is what CI-adjacent tooling actually runs — fix the drift rather than
following one and ignoring the other.

Useful selections:

```bash
bash scripts/qa-check.sh --only build,gofmt,vet   # fast pre-commit sanity
bash scripts/qa-check.sh --skip mutation,tui-e2e  # skip the slow steps
bash scripts/qa-check.sh --fail-fast              # stop at the first failure
bash scripts/qa-check.sh --strict                 # missing tools fail instead of skipping
```

## Where the gates are defined

Steps 1–5 are the **canonical gate** and live in `scripts/run-gates.sh`. CI
(`.github/workflows/test.yml`) and `make check` both delegate to that script, and
so does `qa-check.sh` — one definition of the gate, not three. Do not
re-implement `go build ./...` and friends in new places; call the gate.

Steps 6–10 are QA-only: they are slower, need extra tooling, or drive the TUI,
so CI does not run them on every push.

---

## 1. Build

```bash
bash scripts/run-gates.sh build     # go build ./...
```

**Expect:** no output, exit 0.

**On failure:** a compile error. Nothing below this line will be meaningful
until it builds; fix and restart the checklist.

## 2. Format

```bash
bash scripts/run-gates.sh gofmt     # gofmt -l . must emit nothing
```

**Expect:** `OK: all Go files are gofmt'd`.

**On failure:** the gate prints every unformatted file. Fix with `gofmt -w .`.
This runs before the test suite on purpose — it is the fastest gate and the
most common trivial CI failure.

## 3. Vet

```bash
bash scripts/run-gates.sh vet       # go vet ./...
```

**Expect:** no output, exit 0.

**On failure:** vet findings are almost never false positives in this repo. Fix
the code rather than silencing the check.

## 4. Test with the race detector

```bash
bash scripts/run-gates.sh test      # go test -short -race -count=1 ./...
```

**Expect:** every package `ok` or `[no test files]`. Writes the coverage profile
to `coverage.out` for step 5.

**Notes:**

- `-short` skips the long e2e and real-git stress tests, which gate themselves
  on `testing.Short()`. Step 8 covers the integration suite without `-short`.
- `-count=1` defeats the test cache, so a green run means the tests actually
  ran.
- A **race report is a failure even if the tests pass.** Do not retry until it
  goes away; an intermittent race is still a race.

## 5. Coverage floor

```bash
bash scripts/run-gates.sh coverage  # total >= 54%
```

**Expect:** `OK: coverage NN% >= 54% floor`.

**Notes:**

- Reuses the profile from step 4 when present, and regenerates it otherwise.
- The floor is **54%**, defined in `Makefile` (`COVERAGE_FLOOR`), mirrored in
  `scripts/run-gates.sh` and `.github/workflows/test.yml`. Do not lower it to
  make a branch pass. Raising it is a deliberate, separate change that must
  update all three places together.

## 6. Acceptance scenarios (Gherkin)

```bash
go test -count=1 ./internal/acceptance/
```

**Expect:** `ok`, with all **6 scenarios** in `features/task_lifecycle.feature`
passing: happy path to COMMITTED, zero-coverage escalation, validator rejection
and revision, router expand loop, expand-budget exhaustion, retry-budget
exhaustion.

**On failure:** the failing scenario names the step and the feature-file line,
and prints the full state-transition history the engine actually produced. Read
that history first — it usually shows immediately which transition changed.

**These scenarios are a behavior contract.** A change that makes one fail is a
change in observable lifecycle behavior. Update the feature file only when the
new behavior is intended, not to make red go green.

## 7. Lint (optional tool)

```bash
golangci-lint run                   # config: .golangci.yml
```

**Expect:** no findings.

**If not installed:** `make lint-install` installs the pinned version
(`GOLANGCI_LINT_VERSION` in the Makefile). `qa-check.sh` reports this step as
SKIP rather than failing when the binary is absent — see "Skips" below.

## 8. Integration suite (no `-short`)

```bash
go test -count=1 ./internal/integration/
```

**Expect:** `ok`. This is where the ADR 0008 forced-branch e2e variants run —
the ones step 4 skips under `-short`.

**Notes:** these tests build the `limen` binary and drive real git worktrees, so
they are slower than the unit suite and need `git` on PATH.

## 9. TUI end-to-end

```bash
./scripts/test-tui-e2e.sh
```

**Expect:** `ALL PASS` (or `ALL PASS (pipe only)` when tmux is absent).

Two modes run back to back — pipe mode (non-TTY fallback) and tmux mode (the
real interactive TUI). Both drive the zero-token Python mock backend, so this
step costs no API calls. See `docs/qa/tui-testing.md` for how the harness works
and how to drive it by hand when a scenario needs investigating.

**On failure:** the script prints `FAIL: <what>` and, on a tmux timeout, dumps
the captured screen. Reproduce interactively with the recipe in
`docs/qa/tui-testing.md`.

## 10. Mutation testing (optional tool, slow)

```bash
go-mutesting ./internal/retrieval/...
```

**Expect:** at most **2 surviving mutants**, and every survivor accounted for as
equivalent (a mutation that cannot change observable behavior).

**Notes:**

- Install with
  `go install github.com/zimmski/go-mutesting/cmd/go-mutesting@latest`; it lands
  in `~/go/bin`, which must be on PATH.
- This is minutes, not seconds — it compiles and runs the suite once per mutant.
  It is a release-time and retrieval-change gate, not a per-commit one.
- A **new** survivor means a test gap in the retrieval math. Prefer adding a
  targeted case to the existing property tests in
  `internal/retrieval/property_test.go` over writing a new test function.

---

## Skips

`qa-check.sh` marks a step SKIP when its tool is not installed (steps 7 and 10)
and still exits 0, so a fresh clone can run the checklist without installing
everything. A skip is **not** a pass — the summary prints it distinctly and the
final line names every skipped step.

Availability is checked *before* the tool runs, never inferred from its exit
code: `golangci-lint` and `go-mutesting` both use non-zero exits to mean
"findings", and treating those as "not installed" would turn a real failure into
a silent skip.

For release QA, run everything for real:

```bash
bash scripts/qa-check.sh --strict   # SKIP is treated as FAIL
```

## Before opening a PR

- [ ] Steps 1–6 green (`bash scripts/qa-check.sh --skip lint,mutation,tui-e2e` covers them)
- [ ] Step 9 green if the change touches the TUI, the orchestrator, or the mock backend
- [ ] Step 8 green if the change touches the orchestrator, git, or state layers
- [ ] Step 10 green if the change touches `internal/retrieval/`
- [ ] Coverage did not drop below the floor
- [ ] No new file skips the gate (e.g. a `//nolint` added without a reason)

## Before tagging a release

- [ ] `bash scripts/qa-check.sh --strict` exits 0 — every step, nothing skipped
- [ ] The acceptance feature file still describes the intended lifecycle
- [ ] `CHANGELOG` / release notes mention any behavior change an acceptance
      scenario had to be updated for
