package sandbox

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// buildLimenBinary builds the CLI so the sandbox can run its bridge entry point.
func buildLimenBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "limen")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/limen")
	cmd.Dir = "../.."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build limen: %v\n%s", err, out)
	}
	return bin
}

// echoListener accepts connections and records that it was reached.
func echoListener(t *testing.T) (string, <-chan struct{}) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	reached := make(chan struct{}, 1)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			select {
			case reached <- struct{}{}:
			default:
			}
			_ = conn.Close()
		}
	}()
	return ln.Addr().String(), reached
}

// connectViaBridge runs a sandboxed process that asks the in-sandbox bridge to
// CONNECT to target, and returns the proxy's status line.
func connectViaBridge(t *testing.T, bin string, p Policy, target string) string {
	t.Helper()
	script := fmt.Sprintf(
		`import socket
s = socket.create_connection(("127.0.0.1", 4711), 5)
s.sendall(b"CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n")
print(s.recv(128).decode().splitlines()[0])`, target, target)

	cmd := exec.Command("/bin/sh", "-c", `python3 -c '`+script+`'`)
	p.LimenBinary = bin
	if err := Wrap(cmd, p); err != nil {
		t.Fatalf("Wrap: %v", err)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sandboxed connect: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestSandboxWorkerReachesAllowlistedProviderThroughBroker(t *testing.T) {
	if err := Available(); err != nil {
		t.Skipf("sandbox unavailable: %v", err)
	}
	bin := buildLimenBinary(t)
	upstream, reached := echoListener(t)

	dir := t.TempDir()
	sock := filepath.Join(dir, "broker.sock")
	broker, err := NewBroker(sock, Provider{Name: "pi", Hosts: []string{upstream}}, nil, false)
	if err != nil {
		t.Fatalf("NewBroker: %v", err)
	}
	go func() { _ = broker.Serve() }()
	t.Cleanup(func() { _ = broker.Close() })
	waitForSocket(t, sock)

	p := Policy{
		Worktree:     t.TempDir(),
		Provider:     Provider{Name: "pi", Hosts: []string{upstream}},
		BridgeAddr:   "127.0.0.1:4711",
		BrokerSocket: sock,
	}

	line := connectViaBridge(t, bin, p, upstream)
	if !strings.Contains(line, "200") {
		t.Fatalf("allowlisted CONNECT refused: %q", line)
	}
	select {
	case <-reached:
	default:
		t.Fatal("broker did not reach the allowlisted upstream")
	}
}

func TestSandboxWorkerCannotReachUnlistedHost(t *testing.T) {
	if err := Available(); err != nil {
		t.Skipf("sandbox unavailable: %v", err)
	}
	bin := buildLimenBinary(t)
	upstream, _ := echoListener(t)

	dir := t.TempDir()
	sock := filepath.Join(dir, "broker.sock")
	broker, err := NewBroker(sock, Provider{Name: "pi", Hosts: []string{upstream}}, nil, false)
	if err != nil {
		t.Fatalf("NewBroker: %v", err)
	}
	go func() { _ = broker.Serve() }()
	t.Cleanup(func() { _ = broker.Close() })
	waitForSocket(t, sock)

	p := Policy{
		Worktree:     t.TempDir(),
		Provider:     Provider{Name: "pi", Hosts: []string{upstream}},
		BridgeAddr:   "127.0.0.1:4711",
		BrokerSocket: sock,
	}

	line := connectViaBridge(t, bin, p, "127.0.0.2:1")
	if !strings.Contains(line, "403") {
		t.Fatalf("unlisted host was not refused: %q", line)
	}
}

// waitForSocket blocks until the broker is accepting connections.
func waitForSocket(t *testing.T, path string) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if _, err := os.Stat(path); err == nil {
			if conn, err := net.Dial("unix", path); err == nil {
				_ = conn.Close()
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("broker socket %s never became ready", path)
}
