package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// testPolicy returns a policy rooted at a fresh worktree.
func testPolicy(t *testing.T) Policy {
	t.Helper()
	wt := t.TempDir()
	if err := os.WriteFile(filepath.Join(wt, "solution.txt"), []byte("hi\n"), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return Policy{Worktree: wt, Provider: Provider{Name: "pi", Hosts: []string{"api.example.test"}}}
}

// runInSandbox wraps and runs argv, returning combined output.
func runInSandbox(t *testing.T, p Policy, argv ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(argv[0], argv[1:]...)
	if err := Wrap(cmd, p); err != nil {
		t.Fatalf("Wrap: %v", err)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestWrapFailsClosedWithoutRuntime(t *testing.T) {
	old := bwrapBinary
	bwrapBinary = "limen-missing-bwrap"
	t.Cleanup(func() { bwrapBinary = old })

	if err := Available(); err == nil {
		t.Fatal("Available succeeded without bubblewrap")
	}
	cmd := exec.Command("/bin/true")
	err := Wrap(cmd, testPolicy(t))
	if err == nil {
		t.Fatal("Wrap succeeded without a usable sandbox runtime")
	}
	if !strings.Contains(err.Error(), "sandbox") {
		t.Fatalf("error should name the sandbox: %v", err)
	}
}

func TestSandboxHidesHostHomeAndSecrets(t *testing.T) {
	p := testPolicy(t)
	if err := Available(); err != nil {
		t.Skipf("sandbox unavailable: %v", err)
	}
	out, err := runInSandbox(t, p, "/bin/sh", "-c",
		`test ! -e /home && echo no-home; test ! -e "$REAL_HOME/.ssh" && echo no-ssh`)
	// REAL_HOME is not in the sandbox environment, so the second check is
	// trivially true; the meaningful assertion is that /home does not exist and
	// that the host environment was not inherited.
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "no-home") {
		t.Fatalf("host /home visible inside sandbox: %s", out)
	}
}

func TestSandboxEnvIsNotInherited(t *testing.T) {
	if err := Available(); err != nil {
		t.Skipf("sandbox unavailable: %v", err)
	}
	t.Setenv("LIMEN_TEST_SECRET", "host-secret-value")
	cmd := exec.Command("/bin/sh", "-c", "env")
	p := testPolicy(t)
	if err := Wrap(cmd, p); err != nil {
		t.Fatalf("Wrap: %v", err)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "host-secret-value") {
		t.Fatalf("host environment leaked into sandbox:\n%s", out)
	}
	if strings.Contains(string(out), "HOME=/home/") || strings.Contains(string(out), "HOME=/root") {
		t.Fatalf("host HOME leaked into sandbox:\n%s", out)
	}
}

func TestSandboxWritesConfinedToWorktree(t *testing.T) {
	if err := Available(); err != nil {
		t.Skipf("sandbox unavailable: %v", err)
	}
	p := testPolicy(t)
	out, err := runInSandbox(t, p, "/bin/sh", "-c",
		`echo inside > inside.txt && echo wrote-inside
		 echo outside > /tmp/../escape.txt 2>/dev/null || echo escape-blocked
		 touch /usr/escape.txt 2>/dev/null || echo system-ro-blocked`)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "wrote-inside") {
		t.Fatalf("worktree write failed: %s", out)
	}
	if !strings.Contains(out, "system-ro-blocked") {
		t.Fatalf("system path was writable: %s", out)
	}
	if _, err := os.Stat(filepath.Join(p.Worktree, "inside.txt")); err != nil {
		t.Fatalf("worktree write did not land on the host: %v", err)
	}
}

func TestSandboxHasNoNetwork(t *testing.T) {
	if err := Available(); err != nil {
		t.Skipf("sandbox unavailable: %v", err)
	}
	p := testPolicy(t)
	out, err := runInSandbox(t, p, "/bin/sh", "-c",
		`python3 -c "import socket,sys
s=socket.socket(); s.settimeout(2)
try:
    s.connect(('1.1.1.1',80)); print('CONNECTED')
except OSError as e:
    print('BLOCKED', e.__class__.__name__)"`)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if strings.Contains(out, "CONNECTED") {
		t.Fatalf("sandbox reached the network: %s", out)
	}
}

func TestWrapRejectsEgressWithoutAllowlist(t *testing.T) {
	p := testPolicy(t)
	p.BridgeAddr = "127.0.0.1:9000"
	p.BrokerSocket = filepath.Join(t.TempDir(), "broker.sock")
	p.Provider = Provider{Name: "agy"}
	cmd := exec.Command("/bin/true")
	err := Wrap(cmd, p)
	if err == nil {
		t.Fatal("expected an error for unbounded egress")
	}
	if !strings.Contains(err.Error(), "allowlisted hosts") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWrapRejectsRelativeWorktree(t *testing.T) {
	p := testPolicy(t)
	p.Worktree = "relative/path"
	if err := Wrap(exec.Command("/bin/true"), p); err == nil {
		t.Fatal("expected an error for a relative worktree")
	}
}

func TestSandboxEnvExposesBrokerWithoutCredentials(t *testing.T) {
	p := testPolicy(t)
	p.BridgeAddr = "127.0.0.1:9000"
	p.BrokerSocket = "/tmp/broker.sock"
	p.Provider = Provider{Name: "claude", BaseURLEnv: []string{"ANTHROPIC_BASE_URL"}}
	env := strings.Join(sandboxEnv(p), "\n")
	for _, want := range []string{"ANTHROPIC_BASE_URL=http://127.0.0.1:9000", "HTTP_PROXY=http://127.0.0.1:9000"} {
		if !strings.Contains(env, want) {
			t.Fatalf("missing %q in sandbox env:\n%s", want, env)
		}
	}
	if strings.Contains(env, "sk-") || strings.Contains(env, "REAL") {
		t.Fatalf("sandbox env carries a real credential:\n%s", env)
	}
}
