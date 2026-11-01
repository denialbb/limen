package sandbox

import (
	"bufio"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestServeBridgeForwardsBytes(t *testing.T) {
	sockDir := t.TempDir()
	unixSockPath := filepath.Join(sockDir, "target.sock")

	unixLn, err := net.Listen("unix", unixSockPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	defer unixLn.Close()

	// Echo server on unix socket
	go func() {
		for {
			conn, err := unixLn.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()

	// Allocate a free TCP loopback port
	tcpProbe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe tcp: %v", err)
	}
	listenAddr := tcpProbe.Addr().String()
	_ = tcpProbe.Close()

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- ServeBridge(unixSockPath, listenAddr)
	}()

	// Wait for bridge listener to be registered
	var ln net.Listener
	for start := time.Now(); time.Since(start) < 2*time.Second; {
		if val, ok := activeBridgeListeners.Load(listenAddr); ok {
			ln = val.(net.Listener)
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if ln == nil {
		t.Fatal("bridge listener did not start in time")
	}

	tcpConn, err := net.Dial("tcp", listenAddr)
	if err != nil {
		t.Fatalf("dial bridge tcp: %v", err)
	}
	defer tcpConn.Close()

	payload := "bridge-roundtrip-test\n"
	if _, err := tcpConn.Write([]byte(payload)); err != nil {
		t.Fatalf("write payload to bridge: %v", err)
	}

	reader := bufio.NewReader(tcpConn)
	reply, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read from bridge: %v", err)
	}
	if reply != payload {
		t.Fatalf("got %q, want %q", reply, payload)
	}

	// Close listener and verify ServeBridge returns nil
	if err := ln.Close(); err != nil {
		t.Fatalf("close bridge listener: %v", err)
	}

	select {
	case err := <-serveErr:
		if err != nil {
			t.Fatalf("ServeBridge returned error after close: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ServeBridge timed out waiting to exit after listener closed")
	}
}
