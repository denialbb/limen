package sandbox

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBrokerAllowlistedConnectTunnels(t *testing.T) {
	echoLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp: %v", err)
	}
	defer echoLn.Close()

	go func() {
		for {
			conn, err := echoLn.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()

	targetAddr := echoLn.Addr().String()
	sockPath := filepath.Join(t.TempDir(), "broker.sock")

	p := Provider{
		Name:  "test-provider",
		Hosts: []string{targetAddr},
	}

	broker, err := NewBroker(sockPath, p, nil, false)
	if err != nil {
		t.Fatalf("NewBroker: %v", err)
	}

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- broker.Serve()
	}()
	<-broker.ready
	defer func() {
		_ = broker.Close()
		if err := <-serveErr; err != nil {
			t.Fatalf("broker.Serve returned error: %v", err)
		}
	}()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial broker unix socket: %v", err)
	}
	defer conn.Close()

	connectReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", targetAddr, targetAddr)
	if _, err := conn.Write([]byte(connectReq)); err != nil {
		t.Fatalf("write CONNECT: %v", err)
	}

	reader := bufio.NewReader(conn)
	statusLine, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read CONNECT status: %v", err)
	}
	if !strings.HasPrefix(statusLine, "HTTP/1.1 200 Connection established") {
		t.Fatalf("unexpected CONNECT status: %q", statusLine)
	}
	emptyLine, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read empty line: %v", err)
	}
	if emptyLine != "\r\n" {
		t.Fatalf("expected \\r\\n, got %q", emptyLine)
	}

	payload := "ping-via-tunnel-1234\n"
	if _, err := conn.Write([]byte(payload)); err != nil {
		t.Fatalf("write payload: %v", err)
	}

	received, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read payload: %v", err)
	}
	if received != payload {
		t.Fatalf("got %q, want %q", received, payload)
	}
}

func TestBrokerNonAllowlistedConnect403(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "broker.sock")
	p := Provider{
		Name:  "test-provider",
		Hosts: []string{"allowed.example.test"},
	}

	broker, err := NewBroker(sockPath, p, nil, false)
	if err != nil {
		t.Fatalf("NewBroker: %v", err)
	}

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- broker.Serve()
	}()
	<-broker.ready
	defer func() {
		_ = broker.Close()
		if err := <-serveErr; err != nil {
			t.Fatalf("broker.Serve returned error: %v", err)
		}
	}()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial broker: %v", err)
	}
	defer conn.Close()

	req := "CONNECT forbidden.example.test:443 HTTP/1.1\r\nHost: forbidden.example.test:443\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write CONNECT: %v", err)
	}

	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden, got %d", resp.StatusCode)
	}
}

func TestBrokerTrustedAddsRegistries(t *testing.T) {
	echoLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp: %v", err)
	}
	defer echoLn.Close()

	go func() {
		for {
			conn, err := echoLn.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()

	targetAddr := echoLn.Addr().String()

	t.Run("untrusted blocks registry", func(t *testing.T) {
		sockPath := filepath.Join(t.TempDir(), "broker-untrusted.sock")
		p := Provider{Name: "test-provider"}
		broker, err := NewBroker(sockPath, p, []string{targetAddr}, false)
		if err != nil {
			t.Fatalf("NewBroker: %v", err)
		}

		serveErr := make(chan error, 1)
		go func() {
			serveErr <- broker.Serve()
		}()
		<-broker.ready
		defer func() {
			_ = broker.Close()
			<-serveErr
		}()

		conn, err := net.Dial("unix", sockPath)
		if err != nil {
			t.Fatalf("dial broker: %v", err)
		}
		defer conn.Close()

		req := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", targetAddr, targetAddr)
		if _, err := conn.Write([]byte(req)); err != nil {
			t.Fatalf("write CONNECT: %v", err)
		}

		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			t.Fatalf("ReadResponse: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden for untrusted registry, got %d", resp.StatusCode)
		}
	})

	t.Run("trusted allows registry", func(t *testing.T) {
		sockPath := filepath.Join(t.TempDir(), "broker-trusted.sock")
		p := Provider{Name: "test-provider"}
		broker, err := NewBroker(sockPath, p, []string{targetAddr}, true)
		if err != nil {
			t.Fatalf("NewBroker: %v", err)
		}

		serveErr := make(chan error, 1)
		go func() {
			serveErr <- broker.Serve()
		}()
		<-broker.ready
		defer func() {
			_ = broker.Close()
			<-serveErr
		}()

		conn, err := net.Dial("unix", sockPath)
		if err != nil {
			t.Fatalf("dial broker: %v", err)
		}
		defer conn.Close()

		req := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", targetAddr, targetAddr)
		if _, err := conn.Write([]byte(req)); err != nil {
			t.Fatalf("write CONNECT: %v", err)
		}

		reader := bufio.NewReader(conn)
		statusLine, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read CONNECT status: %v", err)
		}
		if !strings.HasPrefix(statusLine, "HTTP/1.1 200 Connection established") {
			t.Fatalf("expected 200 Connection established for trusted registry, got %q", statusLine)
		}
	})
}

func TestBrokerCredentialInjectionAndDummyScrubbing(t *testing.T) {
	const realSecret = "real-super-secret-token"
	const dummySecret = "limen-sandbox-dummy"
	const credEnvKey = "LIMEN_TEST_REAL_CRED"

	t.Setenv(credEnvKey, realSecret)

	var upstreamSeenHeader string
	var upstreamSeenDummy bool
	var upstreamSeenBody []byte
	var upstreamSeenPath string

	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamSeenHeader = r.Header.Get("Authorization")
		upstreamSeenPath = r.URL.RequestURI()

		for k, vv := range r.Header {
			for _, v := range vv {
				if strings.Contains(k, dummySecret) || strings.Contains(v, dummySecret) {
					upstreamSeenDummy = true
				}
			}
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		upstreamSeenBody = body

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Custom-Echo", "verified")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"result":"ok"}`))
	}))
	defer ts.Close()

	upstreamHost := ts.Listener.Addr().String()
	sockPath := filepath.Join(t.TempDir(), "broker.sock")

	p := Provider{
		Name:       "claude",
		Hosts:      []string{upstreamHost},
		CredEnv:    credEnvKey,
		CredHeader: "Authorization",
		CredScheme: "Bearer",
	}

	broker, err := NewBroker(sockPath, p, nil, false)
	if err != nil {
		t.Fatalf("NewBroker: %v", err)
	}

	// Route through the test server certificate pool.
	broker.client = ts.Client()
	broker.client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- broker.Serve()
	}()
	<-broker.ready
	defer func() {
		_ = broker.Close()
		<-serveErr
	}()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial broker: %v", err)
	}
	defer conn.Close()

	requestBody := `{"prompt":"hello limen"}`
	clientReq := fmt.Sprintf(
		"POST http://127.0.0.1:9999/v1/messages?model=test-model HTTP/1.1\r\n"+
			"Host: 127.0.0.1:9999\r\n"+
			"Authorization: Bearer %s\r\n"+
			"X-Dummy-Header: %s\r\n"+
			"Content-Type: application/json\r\n"+
			"Content-Length: %d\r\n"+
			"\r\n"+
			"%s",
		dummySecret, dummySecret, len(requestBody), requestBody,
	)

	if _, err := conn.Write([]byte(clientReq)); err != nil {
		t.Fatalf("write client request: %v", err)
	}

	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status code: %d", resp.StatusCode)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	if string(respBody) != `{"result":"ok"}` {
		t.Fatalf("unexpected response body: %q", string(respBody))
	}
	if resp.Header.Get("X-Custom-Echo") != "verified" {
		t.Fatalf("missing upstream response header: %v", resp.Header)
	}

	wantAuth := "Bearer " + realSecret
	if upstreamSeenHeader != wantAuth {
		t.Fatalf("upstream Authorization header: got %q, want %q", upstreamSeenHeader, wantAuth)
	}
	if upstreamSeenDummy {
		t.Fatal("upstream saw dummy credential in headers")
	}
	if upstreamSeenPath != "/v1/messages?model=test-model" {
		t.Fatalf("upstream path: got %q, want /v1/messages?model=test-model", upstreamSeenPath)
	}
	if !bytes.Equal(upstreamSeenBody, []byte(requestBody)) {
		t.Fatalf("upstream body: got %q, want %q", upstreamSeenBody, requestBody)
	}
}

func TestBrokerCloseReturnsNilAndCleansSocket(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "broker.sock")
	b, err := NewBroker(sockPath, Provider{Name: "pi"}, nil, false)
	if err != nil {
		t.Fatalf("NewBroker: %v", err)
	}

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- b.Serve()
	}()
	<-b.ready

	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case err := <-serveErr:
		if err != nil {
			t.Fatalf("Serve returned non-nil after Close: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve timed out waiting to exit after Close")
	}
}
