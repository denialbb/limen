package sandbox

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/denialbb/limen/internal/state"
)

type fakeSignal struct {
	taskID    string
	summary   string
	verdict   string
	completed bool
}

type fakeCallbackStore struct {
	mu       sync.Mutex
	nextID   int64
	signals  map[int64]*fakeSignal
	writeErr error
	pollErr  error
}

func newFakeCallbackStore() *fakeCallbackStore {
	return &fakeCallbackStore{
		signals: make(map[int64]*fakeSignal),
	}
}

func (f *fakeCallbackStore) WriteCallbackSignal(taskID, summary string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.writeErr != nil {
		return 0, f.writeErr
	}

	f.nextID++
	id := f.nextID
	f.signals[id] = &fakeSignal{
		taskID:  taskID,
		summary: summary,
	}
	return id, nil
}

func (f *fakeCallbackStore) PollCallbackSignal(id int64) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.pollErr != nil {
		return "", false, f.pollErr
	}

	sig, ok := f.signals[id]
	if !ok {
		return "", false, errors.New("callback signal not found")
	}

	return sig.verdict, sig.completed, nil
}

func (f *fakeCallbackStore) SetVerdict(id int64, verdict string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if sig, ok := f.signals[id]; ok {
		sig.verdict = verdict
		sig.completed = true
	}
}

func startTestServer(t *testing.T, store CallbackStore) (*CallbackServer, string) {
	t.Helper()

	socketPath := filepath.Join(t.TempDir(), "cb.sock")
	srv := NewCallbackServer(socketPath, store)

	serveErrCh := make(chan error, 1)
	go func() {
		serveErrCh <- srv.Serve()
	}()

	select {
	case <-srv.Ready():
	case err := <-serveErrCh:
		t.Fatalf("serve exited early: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for callback server to be ready")
	}

	t.Cleanup(func() {
		_ = srv.Close()
		select {
		case err := <-serveErrCh:
			if err != nil {
				t.Errorf("srv.Serve() error on close: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("timed out waiting for srv.Serve() to exit after close")
		}
	})

	return srv, socketPath
}

func TestCallbackRoundTrip_ReturnsVerdictBytes(t *testing.T) {
	store := newFakeCallbackStore()
	_, socketPath := startTestServer(t, store)

	wantVerdict := `{"passes":true,"fatal":false,"feedback":"all tests passing"}`

	// Simulate validator resolving the pending callback asynchronously.
	go func() {
		for {
			store.mu.Lock()
			count := len(store.signals)
			store.mu.Unlock()
			if count > 0 {
				store.SetVerdict(1, wantVerdict)
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	gotBytes, err := CallbackRoundTrip(socketPath, "task-101", "implemented solution")
	if err != nil {
		t.Fatalf("unexpected CallbackRoundTrip error: %v", err)
	}

	if string(gotBytes) != wantVerdict {
		t.Fatalf("verdict bytes mismatch: got %q, want %q", string(gotBytes), wantVerdict)
	}

	// Verify caller can parse the returned raw bytes with state.UnmarshalVerdict.
	v, err := state.UnmarshalVerdict(gotBytes)
	if err != nil {
		t.Fatalf("state.UnmarshalVerdict failed on returned bytes: %v", err)
	}
	if !v.Passes || v.Feedback != "all tests passing" {
		t.Fatalf("unmarshaled verdict unexpected: %+v", v)
	}
}

func TestCallbackRoundTrip_RejectsEmptyTaskID(t *testing.T) {
	store := newFakeCallbackStore()
	_, socketPath := startTestServer(t, store)

	_, err := CallbackRoundTrip(socketPath, "", "summary without task id")
	if err == nil {
		t.Fatal("expected error for empty task_id, got nil")
	}

	if !strings.Contains(err.Error(), "task_id is required") {
		t.Fatalf("expected error mentioning task_id is required, got: %v", err)
	}
}

func TestCallbackRoundTrip_StoreErrorSurfaces(t *testing.T) {
	t.Run("write error", func(t *testing.T) {
		store := newFakeCallbackStore()
		store.writeErr = errors.New("simulated database write failure")
		_, socketPath := startTestServer(t, store)

		_, err := CallbackRoundTrip(socketPath, "task-store-err", "some summary")
		if err == nil {
			t.Fatal("expected error on store write failure, got nil")
		}
		if !strings.Contains(err.Error(), "simulated database write failure") {
			t.Fatalf("expected error mentioning database write failure, got: %v", err)
		}
	})

	t.Run("poll error", func(t *testing.T) {
		store := newFakeCallbackStore()
		_, socketPath := startTestServer(t, store)

		// Set pollErr after write succeeds.
		go func() {
			for {
				store.mu.Lock()
				count := len(store.signals)
				store.mu.Unlock()
				if count > 0 {
					store.mu.Lock()
					store.pollErr = errors.New("simulated database poll failure")
					store.mu.Unlock()
					return
				}
				time.Sleep(5 * time.Millisecond)
			}
		}()

		_, err := CallbackRoundTrip(socketPath, "task-poll-err", "some summary")
		if err == nil {
			t.Fatal("expected error on store poll failure, got nil")
		}
		if !strings.Contains(err.Error(), "simulated database poll failure") {
			t.Fatalf("expected error mentioning database poll failure, got: %v", err)
		}
	})
}

func TestCallbackServer_SurvivesBadRequestAndServesNext(t *testing.T) {
	store := newFakeCallbackStore()
	_, socketPath := startTestServer(t, store)

	// 1. Send malformed non-JSON payload over raw socket.
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial error: %v", err)
	}
	if _, err := conn.Write([]byte("not valid json\n")); err != nil {
		t.Fatalf("write malformed line: %v", err)
	}
	respLine, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatalf("read malformed response line: %v", err)
	}
	var errResp callbackResponse
	if err := json.Unmarshal(respLine, &errResp); err != nil {
		t.Fatalf("unmarshal error response: %v", err)
	}
	if !strings.Contains(errResp.Error, "invalid request json") {
		t.Fatalf("expected invalid request json error, got: %q", errResp.Error)
	}
	_ = conn.Close()

	// 2. Send request with empty task_id.
	_, err = CallbackRoundTrip(socketPath, "", "missing task id")
	if err == nil {
		t.Fatal("expected error for empty task_id, got nil")
	}

	// 3. Server must still be healthy and serve a subsequent valid request.
	wantVerdict := `{"passes":false,"fatal":true,"feedback":"fatal validator error"}`
	go func() {
		for {
			store.mu.Lock()
			sig, ok := store.signals[1]
			store.mu.Unlock()
			if ok && sig.taskID == "task-healthy" {
				store.SetVerdict(1, wantVerdict)
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	gotBytes, err := CallbackRoundTrip(socketPath, "task-healthy", "valid follow-up summary")
	if err != nil {
		t.Fatalf("server failed to serve follow-up request: %v", err)
	}
	if string(gotBytes) != wantVerdict {
		t.Fatalf("verdict mismatch on follow-up request: got %q, want %q", string(gotBytes), wantVerdict)
	}
}

func TestCallbackServer_CloseReturnsNilAndRemovesSocket(t *testing.T) {
	store := newFakeCallbackStore()
	socketPath := filepath.Join(t.TempDir(), "cb-close.sock")
	srv := NewCallbackServer(socketPath, store)

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve()
	}()

	select {
	case <-srv.Ready():
	case err := <-errCh:
		t.Fatalf("serve exited early: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for server ready")
	}

	if _, err := os.Stat(socketPath); err != nil {
		t.Fatalf("expected socket file to exist: %v", err)
	}

	if err := srv.Close(); err != nil {
		t.Fatalf("srv.Close() error: %v", err)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected Serve() to return nil after Close(), got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Serve() to exit")
	}

	// Second Close() should be idempotent.
	if err := srv.Close(); err != nil {
		t.Fatalf("second Close() error: %v", err)
	}

	// Socket file should be removed.
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("expected socket file to be removed, stat err: %v", err)
	}
}

func TestCallbackRoundTrip_ConnectError(t *testing.T) {
	nonExistentSocket := filepath.Join(t.TempDir(), "nonexistent.sock")
	_, err := CallbackRoundTrip(nonExistentSocket, "t1", "summary")
	if err == nil {
		t.Fatal("expected connect error, got nil")
	}
	if !strings.Contains(err.Error(), "connect callback socket") {
		t.Fatalf("expected connect callback socket error, got: %v", err)
	}
}
