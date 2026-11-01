package sandbox

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Broker is the host-side egress boundary for sandboxed workers.
type Broker struct {
	socketPath   string
	provider     Provider
	allowedHosts map[string]struct{}
	trusted      bool

	mu        sync.Mutex
	listener  net.Listener
	conns     map[net.Conn]struct{}
	closed    bool
	ready     chan struct{}
	readyOnce sync.Once

	client *http.Client
}

// NewBroker creates a broker for socketPath and provider p.
func NewBroker(socketPath string, p Provider, registries []string, trusted bool) (*Broker, error) {
	if socketPath == "" {
		return nil, errors.New("sandbox: broker socket path required")
	}

	allowed := make(map[string]struct{})
	for _, h := range p.Hosts {
		addAllowedHost(allowed, h)
	}
	for _, h := range p.ExtraHosts {
		addAllowedHost(allowed, h)
	}
	if trusted {
		for _, h := range registries {
			addAllowedHost(allowed, h)
		}
	}

	tr := &http.Transport{
		Proxy:              nil,
		DisableCompression: true,
	}

	return &Broker{
		socketPath:   socketPath,
		provider:     p,
		allowedHosts: allowed,
		trusted:      trusted,
		conns:        make(map[net.Conn]struct{}),
		ready:        make(chan struct{}),
		client: &http.Client{
			Transport: tr,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func addAllowedHost(m map[string]struct{}, host string) {
	host = strings.TrimSpace(host)
	if host == "" {
		return
	}
	m[strings.ToLower(host)] = struct{}{}
	if h, _, err := net.SplitHostPort(host); err == nil {
		m[strings.ToLower(h)] = struct{}{}
	}
}

func (b *Broker) isAllowed(target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	if _, ok := b.allowedHosts[strings.ToLower(target)]; ok {
		return true
	}
	if h, _, err := net.SplitHostPort(target); err == nil {
		if _, ok := b.allowedHosts[strings.ToLower(h)]; ok {
			return true
		}
	}
	return false
}

// Serve listens on a unix socket at socketPath and handles requests.
// It returns nil after Close is called.
func (b *Broker) Serve() error {
	if dir := filepath.Dir(b.socketPath); dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("create broker socket dir: %w", err)
		}
	}
	_ = os.Remove(b.socketPath)

	ln, err := net.Listen("unix", b.socketPath)
	if err != nil {
		b.readyOnce.Do(func() { close(b.ready) })
		return fmt.Errorf("listen broker unix socket %s: %w", b.socketPath, err)
	}

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		_ = ln.Close()
		_ = os.Remove(b.socketPath)
		b.readyOnce.Do(func() { close(b.ready) })
		return nil
	}
	b.listener = ln
	b.readyOnce.Do(func() { close(b.ready) })
	b.mu.Unlock()

	defer func() {
		_ = ln.Close()
		_ = os.Remove(b.socketPath)
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			b.mu.Lock()
			closed := b.closed
			b.mu.Unlock()
			if closed {
				return nil
			}
			return err
		}

		b.mu.Lock()
		if b.closed {
			b.mu.Unlock()
			_ = conn.Close()
			return nil
		}
		b.conns[conn] = struct{}{}
		b.mu.Unlock()

		go func(c net.Conn) {
			defer func() {
				b.mu.Lock()
				delete(b.conns, c)
				b.mu.Unlock()
				_ = c.Close()
			}()
			b.handleConn(c)
		}(conn)
	}
}

// Close closes the broker listener and all active connections.
func (b *Broker) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	ln := b.listener
	conns := make([]net.Conn, 0, len(b.conns))
	for c := range b.conns {
		conns = append(conns, c)
	}
	b.readyOnce.Do(func() { close(b.ready) })
	b.mu.Unlock()

	var firstErr error
	if ln != nil {
		if err := ln.Close(); err != nil {
			firstErr = err
		}
	}
	for _, c := range conns {
		_ = c.Close()
	}
	_ = os.Remove(b.socketPath)
	return firstErr
}

func (b *Broker) handleConn(conn net.Conn) {
	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		return
	}

	if req.Method == http.MethodConnect {
		b.handleConnect(conn, reader, req)
		return
	}

	b.handleHTTP(conn, req)
}

func (b *Broker) handleConnect(conn net.Conn, reader *bufio.Reader, req *http.Request) {
	target := req.URL.Host
	if target == "" {
		target = req.Host
	}
	if target == "" {
		target = req.RequestURI
	}

	if !b.isAllowed(target) {
		_, _ = conn.Write([]byte("HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"))
		return
	}

	upstream, err := net.Dial("tcp", target)
	if err != nil {
		_, _ = conn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"))
		return
	}
	defer upstream.Close()

	if _, err := conn.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n")); err != nil {
		return
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(upstream, reader)
		if tc, ok := upstream.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
	}()

	_, _ = io.Copy(conn, upstream)
	_ = upstream.Close()
	_ = conn.Close()
	wg.Wait()
}

func (b *Broker) handleHTTP(conn net.Conn, req *http.Request) {
	if len(b.provider.Hosts) == 0 {
		_, _ = io.WriteString(conn, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
		return
	}

	host := strings.TrimPrefix(strings.TrimPrefix(b.provider.Hosts[0], "https://"), "http://")
	pathQuery := req.URL.RequestURI()
	if pathQuery == "" || !strings.HasPrefix(pathQuery, "/") {
		pathQuery = "/" + pathQuery
	}
	upstreamURL := "https://" + host + pathQuery

	outReq, err := http.NewRequestWithContext(req.Context(), req.Method, upstreamURL, req.Body)
	if err != nil {
		_, _ = io.WriteString(conn, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
		return
	}

	outReq.ContentLength = req.ContentLength

	for k, vv := range req.Header {
		for _, v := range vv {
			outReq.Header.Add(k, v)
		}
	}

	// Never forward the client's dummy credential.
	for k, vv := range outReq.Header {
		for _, v := range vv {
			if strings.Contains(v, "limen-sandbox-dummy") {
				outReq.Header.Del(k)
				break
			}
		}
	}

	if b.provider.CredHeader != "" {
		outReq.Header.Del(b.provider.CredHeader)
		var cred string
		if b.provider.CredEnv != "" {
			cred = os.Getenv(b.provider.CredEnv)
		}
		if cred != "" {
			if b.provider.CredScheme == "Bearer" && !strings.HasPrefix(cred, "Bearer ") {
				cred = "Bearer " + cred
			}
			outReq.Header.Set(b.provider.CredHeader, cred)
		}
	}

	outReq.Host = host

	resp, err := b.client.Do(outReq)
	if err != nil {
		_, _ = io.WriteString(conn, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
		return
	}
	defer resp.Body.Close()

	_ = resp.Write(conn)
}
