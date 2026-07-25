# Live Smoke Run: Multi-Driver Dialects

Run a real, committed end-to-end test of the multi-driver dialect backends.

## Steps

1. **Build the binary:**

   ```bash
   go build -o bin/limen ./cmd/limen
   ```

2. **Run a smoke task** — use a small fixture with real agent backends:

   ```bash
   ./bin/limen run-task \
     --mock=false \
     --worker-backend=claude \
     --validator-backend=agy \
     --coverage-floor=0 \
     --confidence-floor=0
   ```

3. **Report results:**
   - Did the binary build cleanly?
   - Did the task reach `COMMITTED`?
   - Any errors, stalls, or unexpected behavior?
   - How long did it take?

## Context

- Branch: `main` (multi-driver dialects already merged)
- All gated e2e tests pass (`LIMEN_E2E_REAL_AGENTS=1 go test ./internal/remote/ -run RealBinary`)
- This is the first actual committed run with real backends (previous tests used fakes or mocks)
- See `docs/adr/0009` for the dialect architecture

## Verification

- [ ] Binary builds without error
- [ ] Task completes end-to-end (reaches COMMITTED state)
- [ ] No crashes, hangs, or unexpected errors
