package remote

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/denialbb/limen/internal/bus"
	"github.com/denialbb/limen/internal/git"
	"github.com/denialbb/limen/internal/state"
)

func TestParseVerdictSentinel(t *testing.T) {
	tests := []struct {
		name       string
		output     string
		wantErr    bool
		wantPasses bool
		wantFb     string
	}{
		{
			name:       "single valid sentinel",
			output:     "I ran the tests, they pass.\nLIMEN_VERDICT: {\"passes\":true,\"feedback\":\"all green\"}",
			wantPasses: true,
			wantFb:     "all green",
		},
		{
			name:       "last sentinel wins",
			output:     "LIMEN_VERDICT: {\"passes\":true,\"feedback\":\"first\"}\nreconsidered...\nLIMEN_VERDICT: {\"passes\":false,\"feedback\":\"final\"}",
			wantPasses: false,
			wantFb:     "final",
		},
		{
			name:       "sentinel with surrounding whitespace",
			output:     "prose\n   LIMEN_VERDICT: {\"passes\":true,\"feedback\":\"ok\"}   \ntrailing prose\n",
			wantPasses: true,
			wantFb:     "ok",
		},
		{
			name:    "missing sentinel is an error",
			output:  "The change looks fine but I forgot the sentinel.",
			wantErr: true,
		},
		{
			name:    "garbage json after prefix is an error",
			output:  "LIMEN_VERDICT: not-json-at-all",
			wantErr: true,
		},
		{
			name:    "empty output is an error",
			output:  "",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v, err := parseVerdictSentinel(tc.output)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got verdict %#v", v)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if v.Passes != tc.wantPasses || v.Feedback != tc.wantFb {
				t.Fatalf("verdict = %#v, want passes=%v feedback=%q", v, tc.wantPasses, tc.wantFb)
			}
		})
	}
}

func TestAgentValidatorInjectedCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake shell script fixture requires a POSIX shell")
	}

	cases := []struct {
		name        string
		body        string
		wantPasses  bool
		wantFb      string
		wantErr     bool
		wantVerdict bool // whether a ValidatorVerdict event should be emitted
	}{
		{
			name:        "approval",
			body:        `echo 'ran tests, all pass'; echo 'LIMEN_VERDICT: {"passes":true,"feedback":"tests pass"}'`,
			wantPasses:  true,
			wantFb:      "tests pass",
			wantVerdict: true,
		},
		{
			name:        "rejection returns feedback",
			body:        `echo 'test_add failed'; echo 'LIMEN_VERDICT: {"passes":false,"feedback":"add() returns wrong value"}'`,
			wantPasses:  false,
			wantFb:      "add() returns wrong value",
			wantVerdict: true,
		},
		{
			name:    "no sentinel is a transport error",
			body:    `echo 'I could not decide'`,
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			script := filepath.Join(dir, "fake-validator.sh")
			fixture := "#!/bin/sh\n" + tc.body + "\n"
			if err := os.WriteFile(script, []byte(fixture), 0755); err != nil {
				t.Fatalf("write fixture: %v", err)
			}

			v := newAgentValidatorCmd(agyValidatorDialect(), []string{"/bin/sh", script}, defaultOptions())
			rec := bus.NewRecorderEmitter()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			passes, fb, err := v.Evaluate(ctx, &state.Task{ID: "vt", Prompt: "implement add"}, &git.Worktree{Path: dir}, rec)

			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected transport error, got passes=%v fb=%q", passes, fb)
				}
				// A transport failure must NOT emit a verdict (would burn retry budget).
				if len(rec.EventsByKind("ValidatorVerdict")) != 0 {
					t.Fatalf("transport error must not emit ValidatorVerdict")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if passes != tc.wantPasses || fb != tc.wantFb {
				t.Fatalf("got passes=%v fb=%q, want passes=%v fb=%q", passes, fb, tc.wantPasses, tc.wantFb)
			}
			if len(rec.EventsByKind("ValidatorExamining")) != 1 {
				t.Fatalf("expected one ValidatorExamining event")
			}
			if got := len(rec.EventsByKind("ValidatorVerdict")); got != 1 {
				t.Fatalf("expected one ValidatorVerdict event, got %d", got)
			}
			vv := rec.EventsByKind("ValidatorVerdict")[0].(*bus.ValidatorVerdict)
			if vv.Passes != tc.wantPasses || vv.Feedback != tc.wantFb {
				t.Fatalf("ValidatorVerdict = %#v, want passes=%v fb=%q", vv, tc.wantPasses, tc.wantFb)
			}
		})
	}
}

func TestRenderValidatorPromptContract(t *testing.T) {
	got := renderValidatorPrompt(&state.Task{ID: "v9", Prompt: "make add work"})
	for _, want := range []string{"v9", "make add work", "git diff", "LIMEN_VERDICT:", "test"} {
		if !strings.Contains(got, want) {
			t.Fatalf("validator prompt missing %q:\n%s", want, got)
		}
	}
}

func TestAgyValidatorRealBinary(t *testing.T) {
	if os.Getenv("LIMEN_E2E_REAL_AGENTS") != "1" {
		t.Skip("set LIMEN_E2E_REAL_AGENTS=1 to run real-agent e2e")
	}
	if _, err := exec.LookPath("agy"); err != nil {
		t.Skip("agy not found in PATH")
	}

	dir := t.TempDir()
	// A trivially-correct "change": nothing to run, just exercise the sentinel path.
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("noop\n"), 0644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	v := NewAgyValidator()
	rec := bus.NewRecorderEmitter()
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	passes, fb, err := v.Evaluate(ctx, &state.Task{
		ID:     "e2e-agy-val",
		Prompt: "There is a file note.txt containing the word noop. This is acceptable; approve it.",
	}, &git.Worktree{Path: dir}, rec)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	t.Logf("agy validator verdict: passes=%v feedback=%q", passes, fb)
	if len(rec.EventsByKind("ValidatorVerdict")) != 1 {
		t.Fatalf("expected one ValidatorVerdict event")
	}
}
