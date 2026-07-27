#!/usr/bin/env python3
"""Slice 3: gated real ``agy --print`` trials for the breadcrumb spike.

Answers empirically: when a limen-owned MCP ``limen_breadcrumb(message)`` tool
is loaded and ``agy --print`` runs a real coding prompt, DOES the model call the
tool during the run, and HOW OFTEN / how reliably across trials?

Two conditions:

- **unaligned**: a normal coding prompt; the tool is available but never
  mentioned. Tests unprompted self-narration (the pessimistic expectation).
- **aligned**: the same task, but the prompt explicitly asks the model to report
  progress via the tool each step. Bounds the best case (prompt-discipline).

GATED: does nothing unless ``LIMEN_SPIKE_REAL_AGY=1`` (mirrors
``LIMEN_E2E_REAL_AGENTS=1``). Each trial runs in a fresh HOME-scoped throwaway
directory (scope.build_scoped_home) so the user's real ~/.gemini is never
touched. Per-trial diagnostics confirm the server was actually loaded, so a
zero-call trial is attributable to model behavior, not a plumbing failure.

Usage:  LIMEN_SPIKE_REAL_AGY=1 python3 run_trials.py [--trials N] [--json OUT]
"""
import argparse
import datetime
import json
import os
import subprocess
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import scope  # noqa: E402

SERVER = os.path.join(HERE, "mcp_breadcrumb_server.py")
GATE_ENV = "LIMEN_SPIKE_REAL_AGY"
PRINT_TIMEOUT = "3m"          # agy's own wait cap (Go duration)
SUBPROC_TIMEOUT = 240         # hard wall on each trial (seconds)

# Deterministic one-file fixture: a tiny buggy module with a self-check, so a
# trial is seconds and identical across runs (variance is model behavior, not
# setup). add() and sub() are both wrong.
FIXTURE_NAME = "calc.py"
FIXTURE_SRC = '''\
def add(a, b):
    return a - b   # BUG: should be a + b


def sub(a, b):
    return a + b   # BUG: should be a - b


def _selfcheck():
    ok = add(2, 3) == 5 and sub(5, 2) == 3
    print("ALL PASS" if ok else "FAIL")


if __name__ == "__main__":
    _selfcheck()
'''

TASK = (
    "The file calc.py in this directory has two buggy functions. Fix them so "
    "that running `python3 calc.py` prints exactly `ALL PASS`. Edit the file, "
    "then run it to confirm."
)

ALIGN_SUFFIX = (
    "\n\nIMPORTANT: You have an MCP tool `limen_breadcrumb(message)`. Call it "
    "with a brief status message at the start of each distinct step you take "
    "(e.g. reading the file, editing add, editing sub, running the test) so "
    "your progress can be tracked."
)


def _write_fixture(workdir):
    with open(os.path.join(workdir, FIXTURE_NAME), "w", encoding="utf-8") as fh:
        fh.write(FIXTURE_SRC)


def _parse_ts(line):
    return datetime.datetime.fromisoformat(line.split("\t")[0])


def _read(path):
    if not os.path.exists(path):
        return []
    with open(path, encoding="utf-8") as fh:
        return [ln.rstrip("\n") for ln in fh if ln.strip()]


def run_trial(condition, idx):
    """Run one gated trial. Returns a result dict."""
    root = tempfile.mkdtemp(prefix=f"agy-spike-{condition}-{idx}-")
    workdir = os.path.join(root, "work")
    os.makedirs(workdir, exist_ok=True)
    _write_fixture(workdir)
    log = os.path.join(root, "breadcrumb.log")
    diag = os.path.join(root, "diag.log")
    env, _ = scope.build_scoped_home(root, SERVER, log, diag_path=diag)

    prompt = TASK + (ALIGN_SUFFIX if condition == "aligned" else "")

    t0 = datetime.datetime.now(datetime.timezone.utc)
    timed_out = False
    try:
        p = subprocess.run(
            ["agy", "--dangerously-skip-permissions", "--print-timeout",
             PRINT_TIMEOUT, "--print", prompt],
            env=env, cwd=workdir, capture_output=True, text=True,
            timeout=SUBPROC_TIMEOUT,
        )
        rc = p.returncode
    except subprocess.TimeoutExpired:
        rc, timed_out = "TIMEOUT", True
    wall = (datetime.datetime.now(datetime.timezone.utc) - t0).total_seconds()

    diag_events = [ln.split("\t")[1] for ln in _read(diag)]
    bc_lines = _read(log)
    ts = [_parse_ts(ln) for ln in bc_lines]
    intervals = [round((ts[i + 1] - ts[i]).total_seconds(), 2)
                 for i in range(len(ts) - 1)]

    return {
        "condition": condition,
        "trial": idx,
        "rc": rc,
        "timed_out": timed_out,
        "wall_s": round(wall, 1),
        "server_loaded": "start" in diag_events and "initialize" in diag_events,
        "tools_listed": "tools/list" in diag_events,
        "call_count": len(bc_lines),
        "intervals_s": intervals,
        "messages": [ln.split("\t", 1)[1] for ln in bc_lines],
    }


def summarize(results):
    out = {}
    for cond in ("unaligned", "aligned"):
        rs = [r for r in results if r["condition"] == cond]
        counts = [r["call_count"] for r in rs]
        all_intervals = [x for r in rs for x in r["intervals_s"]]
        out[cond] = {
            "trials": len(rs),
            "server_loaded_all": all(r["server_loaded"] for r in rs),
            "call_counts": counts,
            "total_calls": sum(counts),
            "trials_with_ge1_call": sum(1 for c in counts if c >= 1),
            "min_calls": min(counts) if counts else 0,
            "max_calls": max(counts) if counts else 0,
            "mean_calls": round(sum(counts) / len(counts), 2) if counts else 0,
            "inter_call_intervals_s": all_intervals,
            "mean_wall_s": round(sum(r["wall_s"] for r in rs) / len(rs), 1) if rs else 0,
        }
    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--trials", type=int, default=5, help="trials per condition (>=5)")
    ap.add_argument("--json", default=None, help="write raw+summary JSON here")
    args = ap.parse_args()

    if os.environ.get(GATE_ENV) != "1":
        print(f"[gated] set {GATE_ENV}=1 to run real agy trials; nothing to do.")
        return 0

    results = []
    for cond in ("unaligned", "aligned"):
        for i in range(args.trials):
            r = run_trial(cond, i)
            results.append(r)
            print(f"{cond} trial {i}: rc={r['rc']} loaded={r['server_loaded']} "
                  f"calls={r['call_count']} intervals={r['intervals_s']} "
                  f"wall={r['wall_s']}s", flush=True)

    summary = summarize(results)
    print("\n=== SUMMARY ===")
    print(json.dumps(summary, indent=2))
    if args.json:
        with open(args.json, "w", encoding="utf-8") as fh:
            json.dump({"results": results, "summary": summary}, fh, indent=2)
        print(f"\nwrote {args.json}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
