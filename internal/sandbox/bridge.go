package sandbox

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
)

// activeBridgeListeners tracks listeners created by ServeBridge.
var activeBridgeListeners sync.Map

// ServeBridge listens on listenAddr (loopback TCP) and splices bytes to the
// unix socket at socketPath. It returns nil after the listener is closed.
func ServeBridge(socketPath, listenAddr string) error {
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("bridge listen tcp %s: %w", listenAddr, err)
	}
	defer ln.Close()

	activeBridgeListeners.Store(ln.Addr().String(), ln)
	activeBridgeListeners.Store(listenAddr, ln)
	defer func() {
		activeBridgeListeners.Delete(ln.Addr().String())
		activeBridgeListeners.Delete(listenAddr)
	}()

	for {
		tcpConn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || strings.Contains(err.Error(), "use of closed network connection") {
				return nil
			}
			return err
		}

		go handleBridgeConn(tcpConn, socketPath)
	}
}

func handleBridgeConn(tcpConn net.Conn, socketPath string) {
	defer tcpConn.Close()

	unixConn, err := net.Dial("unix", socketPath)
	if err != nil {
		return
	}
	defer unixConn.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(unixConn, tcpConn)
		if uc, ok := unixConn.(*net.UnixConn); ok {
			_ = uc.CloseWrite()
		}
	}()

	_, _ = io.Copy(tcpConn, unixConn)
	if tc, ok := tcpConn.(*net.TCPConn); ok {
		_ = tc.CloseWrite()
	}
	_ = unixConn.Close()
	_ = tcpConn.Close()
	wg.Wait()
}
