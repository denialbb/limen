# Arch #3 — Typed Verdict + split Signaler off Store

**Strength:** Strong · **Package:** `internal/state`, `internal/orchestrator`, `cmd/limen`

## Problem
1. The ready/verdict handshake is stringly-typed. `{"passes":%t,"feedback":%q}` is
   hand-built via `fmt.Sprintf` in TWO places (`orchestrator.go:~381`, `main.go:~711`)
   and never unmarshalled back into a struct on the Go side.
2. `state.Store` (`internal/state/state.go:52-107`, 16 methods) fuses the audited
   state-machine ops (`TransitionState`, `IncrementRetry`, `RecordValidationDecision`)
   with the cross-process signalling table (`WriteCallbackSignal`, `PollCallbackSignal`,
   `GetPendingCallback`, `WriteCallbackVerdict`).

## Goal
1. Introduce `type Verdict struct { Passes bool; Feedback string }` (in `internal/state`)
   with one `Marshal`/`Unmarshal` (or use `encoding/json` directly through it). Replace
   BOTH `fmt.Sprintf` sites with it. Where the verdict string is consumed, unmarshal
   through the type.
2. Split the callback/signalling methods off `state.Store` into a separate `Signaler`
   interface (still backed by the same sqlite impl). `Store` keeps only state-machine ops.
   Update call sites so state-only consumers depend on `Store`, signalling consumers on `Signaler`.

## Acceptance
- `state.Verdict` type exists; both former `fmt.Sprintf` sites use it; verdict is unmarshalled through the type where read.
- `state.Store` no longer exposes the callback-signalling methods; a `Signaler` interface does. The sqlite struct satisfies both.
- Round-trip unit test: `Verdict` marshals and unmarshals identically; assert both former producers yield the same shape.
- `go build ./...`, `go vet ./...`, existing tests pass.

## Constraints
- No behavior change to the wire format Pi reads (the JSON string must stay byte-compatible: keys `passes`, `feedback`).
- Match surrounding style. No historical comments.
