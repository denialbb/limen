package sandbox

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// CallbackStore defines the persistence operations needed to record worker
// ready-for-review signals and poll for validation verdicts.
type CallbackStore interface {
	WriteCallbackSignal(taskID, summary string) (int64, error)
	PollCallbackSignal(id int64) (string, bool, error)
}

// CallbackServer mediates 'limen ready-for-review' callbacks between sandboxed
// workers and the host orchestration store over a Unix domain socket.
type CallbackServer struct {
	socketPath   string
	store        CallbackStore
	listener     net.Listener
	ready        chan struct{}
	closeCh      chan struct{}
	closed       atomic.Bool
	mu           sync.Mutex
	conns        map[net.Conn]struct{}
	pollInterval time.Duration
}

// callbackRequest is the wire request sent by the client.
type callbackRequest struct {
	TaskID  string `json:"task_id"`
	Summary string `json:"summary"`
}

// callbackResponse is the wire response sent back to the client.
type callbackResponse struct {
	Verdict json.RawMessage `json:"verdict,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// NewCallbackServer creates a new callback server listening on socketPath.
func NewCallbackServer(socketPath string, store CallbackStore) *CallbackServer {
	return &CallbackServer{
		socketPath:   socketPath,
		store:        store,
		ready:        make(chan struct{}),
		closeCh:      make(chan struct{}),
		conns:        make(map[net.Conn]struct{}),
		pollInterval: 25 * time.Millisecond,
	}
}

// Ready returns a channel that is closed when the server has started listening.
func (s *CallbackServer) Ready() <-chan struct{} {
	return s.ready
}

// Serve starts the Unix listener and runs the accept loop until Close is called.
// It returns nil after Close has been called.
func (s *CallbackServer) Serve() error {
	if s.closed.Load() {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(s.socketPath), 0755); err != nil {
		return fmt.Errorf("sandbox: create callback socket dir: %w", err)
	}
	_ = os.Remove(s.socketPath)

	l, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("sandbox: listen callback socket: %w", err)
	}

	s.mu.Lock()
	if s.closed.Load() {
		s.mu.Unlock()
		_ = l.Close()
		_ = os.Remove(s.socketPath)
		return nil
	}
	s.listener = l
	s.mu.Unlock()

	close(s.ready)

	for {
		conn, err := l.Accept()
		if err != nil {
			if s.closed.Load() {
				return nil
			}
			return err
		}

		s.trackConn(conn, true)
		go func() {
			defer s.trackConn(conn, false)
			s.handleConn(conn)
		}()
	}
}

// Close terminates the listener, all active connections, and removes the socket file.
func (s *CallbackServer) Close() error {
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}

	close(s.closeCh)

	s.mu.Lock()
	var err error
	if s.listener != nil {
		err = s.listener.Close()
	}
	for conn := range s.conns {
		_ = conn.Close()
	}
	s.mu.Unlock()

	_ = os.Remove(s.socketPath)
	return err
}

func (s *CallbackServer) trackConn(conn net.Conn, add bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if add {
		s.conns[conn] = struct{}{}
	} else {
		delete(s.conns, conn)
	}
}

func (s *CallbackServer) handleConn(conn net.Conn) {
	defer conn.Close()

	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		resp := s.handleRequest(line)
		data, err := json.Marshal(resp)
		if err != nil {
			data = []byte(`{"error":"internal json marshal error"}`)
		}
		data = append(data, '\n')
		if _, err := conn.Write(data); err != nil {
			return
		}
	}
}

func (s *CallbackServer) handleRequest(data []byte) callbackResponse {
	var req callbackRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return callbackResponse{Error: fmt.Sprintf("invalid request json: %v", err)}
	}

	if req.TaskID == "" {
		return callbackResponse{Error: "task_id is required"}
	}

	id, err := s.store.WriteCallbackSignal(req.TaskID, req.Summary)
	if err != nil {
		return callbackResponse{Error: fmt.Sprintf("write callback signal: %v", err)}
	}

	// Poll immediately once before ticking.
	verdict, ready, err := s.store.PollCallbackSignal(id)
	if err != nil {
		return callbackResponse{Error: fmt.Sprintf("poll callback signal: %v", err)}
	}
	if ready {
		return makeVerdictResponse(verdict)
	}

	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.closeCh:
			return callbackResponse{Error: "callback server closed"}
		case <-ticker.C:
			verdict, ready, err := s.store.PollCallbackSignal(id)
			if err != nil {
				return callbackResponse{Error: fmt.Sprintf("poll callback signal: %v", err)}
			}
			if ready {
				return makeVerdictResponse(verdict)
			}
		}
	}
}

func makeVerdictResponse(verdict string) callbackResponse {
	trimmed := strings.TrimSpace(verdict)
	if json.Valid([]byte(trimmed)) {
		return callbackResponse{Verdict: json.RawMessage(trimmed)}
	}
	b, _ := json.Marshal(verdict)
	return callbackResponse{Verdict: json.RawMessage(b)}
}

// CallbackRoundTrip performs a single blocking callback round-trip over the Unix socket.
// It sends {task_id, summary} and returns the raw verdict JSON bytes unchanged.
func CallbackRoundTrip(socketPath, taskID, summary string) ([]byte, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("sandbox: connect callback socket: %w", err)
	}
	defer conn.Close()

	req := callbackRequest{
		TaskID:  taskID,
		Summary: summary,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("sandbox: marshal callback request: %w", err)
	}
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		return nil, fmt.Errorf("sandbox: write callback request: %w", err)
	}

	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return nil, fmt.Errorf("sandbox: read callback response: %w", err)
	}

	var resp callbackResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("sandbox: decode callback response: %w", err)
	}

	if resp.Error != "" {
		return nil, fmt.Errorf("callback error: %s", resp.Error)
	}

	if len(resp.Verdict) == 0 {
		return nil, errors.New("callback error: missing verdict in response")
	}

	return []byte(resp.Verdict), nil
}
