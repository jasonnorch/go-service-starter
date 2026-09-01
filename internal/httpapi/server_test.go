package httpapi_test

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/jasonnorch/go-service-starter/internal/config"
	"github.com/jasonnorch/go-service-starter/internal/httpapi"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// freePort asks the kernel for a port and gives it straight back, which is the
// only way to learn the address of a server started by ListenAndServe.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatalf("releasing the port: %v", err)
	}
	return port
}

func serverConfig(port int, shutdown time.Duration) config.Server {
	return config.Server{
		Port:            port,
		ReadTimeout:     5 * time.Second,
		WriteTimeout:    30 * time.Second,
		IdleTimeout:     30 * time.Second,
		ShutdownTimeout: shutdown,
		BodyLimit:       1 << 20,
	}
}

// waitForServer blocks until the server answers, so a test never races the
// goroutine that starts it.
func waitForServer(t *testing.T, port int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
	}
	t.Fatalf("server on port %d did not start", port)
}

// The behavior the whole function exists for: a request already in flight when
// the signal arrives runs to completion instead of having its connection cut.
func TestServeDrainsInFlightRequests(t *testing.T) {
	port := freePort(t)
	const handlerDuration = 300 * time.Millisecond

	mux := http.NewServeMux()
	mux.HandleFunc("GET /slow", func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(handlerDuration)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("finished"))
	})

	server := httpapi.NewServer(mux, serverConfig(port, 5*time.Second))
	sigCh := make(chan os.Signal, 2)
	appCancelled := false

	served := make(chan error, 1)
	go func() {
		served <- httpapi.Serve(server, sigCh, func() { appCancelled = true }, discardLogger(), 5*time.Second)
	}()
	waitForServer(t, port)

	type result struct {
		status int
		body   string
		err    error
	}
	requestDone := make(chan result, 1)
	go func() {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/slow", port)) //nolint:noctx // the point is a request with no deadline of its own
		if err != nil {
			requestDone <- result{err: err}
			return
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		requestDone <- result{status: resp.StatusCode, body: string(body), err: err}
	}()

	// Signal while the handler is still running.
	time.Sleep(handlerDuration / 3)
	sigCh <- syscall.SIGTERM

	got := <-requestDone
	if got.err != nil {
		t.Fatalf("in-flight request failed instead of draining: %v", got.err)
	}
	if got.status != http.StatusOK || got.body != "finished" {
		t.Errorf("in-flight request = %d %q, want 200 %q", got.status, got.body, "finished")
	}

	select {
	case err := <-served:
		if err != nil {
			t.Errorf("Serve returned %v, want nil after a clean shutdown", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not return after the shutdown signal")
	}

	if !appCancelled {
		t.Error("appCancel was not called, so application resources would outlive the server")
	}
}

// An operator who has pressed Ctrl-C twice expects the second press to mean it,
// even when a handler is ignoring its context.
func TestServeSecondSignalForcesClose(t *testing.T) {
	port := freePort(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	mux := http.NewServeMux()
	mux.HandleFunc("GET /stuck", func(_ http.ResponseWriter, _ *http.Request) {
		<-release // never returns during the test: the case the timeout exists for
	})

	// A shutdown timeout long enough that reaching it would fail the test —
	// only the second signal can make this return in time.
	server := httpapi.NewServer(mux, serverConfig(port, time.Minute))
	sigCh := make(chan os.Signal, 2)

	served := make(chan error, 1)
	go func() {
		served <- httpapi.Serve(server, sigCh, func() {}, discardLogger(), time.Minute)
	}()
	waitForServer(t, port)

	go func() {
		//nolint:noctx,bodyclose // the response never arrives; the handler is stuck by design
		_, _ = http.Get(fmt.Sprintf("http://127.0.0.1:%d/stuck", port))
	}()
	time.Sleep(200 * time.Millisecond)

	sigCh <- syscall.SIGINT
	sigCh <- syscall.SIGINT

	select {
	case err := <-served:
		if err != nil {
			t.Errorf("Serve returned %v, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a second signal did not force the shutdown; it waited on the stuck handler")
	}
}

// A port already in use has to surface as an error rather than a process that
// looks started and is serving nothing.
func TestServeReportsListenFailure(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupying a port: %v", err)
	}
	defer func() { _ = l.Close() }()

	server := httpapi.NewServer(http.NewServeMux(), serverConfig(l.Addr().(*net.TCPAddr).Port, time.Second))
	// The occupied listener is bound to 127.0.0.1 while NewServer listens on
	// all interfaces, which still collides.
	appCancelled := false

	served := make(chan error, 1)
	go func() {
		served <- httpapi.Serve(server, make(chan os.Signal, 2), func() { appCancelled = true }, discardLogger(), time.Second)
	}()

	select {
	case err := <-served:
		if err == nil {
			t.Fatal("Serve returned nil when the port was already in use")
		}
		if !appCancelled {
			t.Error("appCancel was not called on a server error, leaking application resources")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not report the listen failure")
	}
}

func TestNewServerSetsEveryTimeout(t *testing.T) {
	cfg := serverConfig(8080, 15*time.Second)
	server := httpapi.NewServer(http.NewServeMux(), cfg)

	if server.Addr != ":8080" {
		t.Errorf("Addr = %q, want %q", server.Addr, ":8080")
	}
	// A zero timeout on an http.Server means "no timeout", which is how a slow
	// client holds a connection open forever.
	if server.ReadTimeout != cfg.ReadTimeout {
		t.Errorf("ReadTimeout = %s, want %s", server.ReadTimeout, cfg.ReadTimeout)
	}
	if server.WriteTimeout != cfg.WriteTimeout {
		t.Errorf("WriteTimeout = %s, want %s", server.WriteTimeout, cfg.WriteTimeout)
	}
	if server.IdleTimeout != cfg.IdleTimeout {
		t.Errorf("IdleTimeout = %s, want %s", server.IdleTimeout, cfg.IdleTimeout)
	}
}
