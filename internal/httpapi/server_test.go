package httpapi_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync/atomic"
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

// requestResult is what the goroutines below report back from a request made
// while the server is shutting down.
type requestResult struct {
	status int
	body   string
	err    error
}

// get issues a request in the background and returns the channel carrying its
// outcome. The request deliberately has no deadline of its own — the point of
// these tests is what the *server* does with a request that is still running.
func get(url string) <-chan requestResult {
	done := make(chan requestResult, 1)
	go func() {
		resp, err := http.Get(url) //nolint:noctx // deliberately has no deadline of its own
		if err != nil {
			done <- requestResult{err: err}
			return
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		done <- requestResult{status: resp.StatusCode, body: string(body), err: err}
	}()
	return done
}

// awaitRequest waits for that outcome, bounded.
//
// A bare receive would be correct but slow to diagnose: the default http.Client
// has no timeout, so a drain regression that leaves the connection open blocks
// until go test's global ten-minute deadline. That reports as "the package timed
// out" rather than as the assertion that failed, ten minutes later.
func awaitRequest(t *testing.T, done <-chan requestResult) requestResult {
	t.Helper()
	select {
	case got := <-done:
		return got
	case <-time.After(10 * time.Second):
		t.Fatal("the request never completed; the server did not drain it or close it")
		return requestResult{}
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

	// Closed by the handler on entry. Waiting on it is what makes this a test of
	// draining rather than of timing: with a fixed sleep, a loaded machine can
	// signal before the request has reached the handler, and the test then
	// passes without a single in-flight request to drain.
	handlerStarted := make(chan struct{})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /slow", func(w http.ResponseWriter, _ *http.Request) {
		close(handlerStarted)
		time.Sleep(handlerDuration)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("finished"))
	})

	server := httpapi.NewServer(mux, serverConfig(port, 5*time.Second))
	sigCh := make(chan os.Signal, 2)
	appCancelled := false

	served := make(chan error, 1)
	go func() {
		served <- httpapi.Serve(t.Context(), server, sigCh, func() { appCancelled = true }, discardLogger(), 5*time.Second)
	}()
	waitForServer(t, port)

	requestDone := get(fmt.Sprintf("http://127.0.0.1:%d/slow", port))

	// Signal only once the handler is provably running.
	select {
	case <-handlerStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("the request never reached the handler")
	}
	sigCh <- syscall.SIGTERM

	got := awaitRequest(t, requestDone)
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

	// Without this the test can send both signals before the request reaches
	// the handler, in which case there is nothing stuck to force past and the
	// test passes without exercising the forced close at all.
	handlerStarted := make(chan struct{})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /stuck", func(_ http.ResponseWriter, _ *http.Request) {
		close(handlerStarted)
		<-release // never returns during the test: the case the timeout exists for
	})

	// A shutdown timeout long enough that reaching it would fail the test —
	// only the second signal can make this return in time.
	server := httpapi.NewServer(mux, serverConfig(port, time.Minute))
	sigCh := make(chan os.Signal, 2)

	served := make(chan error, 1)
	go func() {
		served <- httpapi.Serve(t.Context(), server, sigCh, func() {}, discardLogger(), time.Minute)
	}()
	waitForServer(t, port)

	go func() {
		//nolint:noctx,bodyclose // the response never arrives; the handler is stuck by design
		_, _ = http.Get(fmt.Sprintf("http://127.0.0.1:%d/stuck", port))
	}()
	select {
	case <-handlerStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("the request never reached the stuck handler")
	}

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

// Canceling the context that Serve was given stops it, with the same drain as a
// signal. A Serve that watched only signals would accept a context and quietly
// ignore it, leaving any caller holding a cancelable one blocked until somebody
// reached for the keyboard.
func TestServeStopsWhenTheContextIsCanceled(t *testing.T) {
	port := freePort(t)
	ctx, cancel := context.WithCancel(t.Context())

	// Counts requests this server actually served, which is what makes the
	// "stopped serving" assertion below independent of the port.
	var served32 atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ping", func(w http.ResponseWriter, _ *http.Request) {
		served32.Add(1)
		w.WriteHeader(http.StatusOK)
	})

	server := httpapi.NewServer(mux, serverConfig(port, 5*time.Second))
	appCancelled := false

	served := make(chan error, 1)
	go func() {
		served <- httpapi.Serve(ctx, server, make(chan os.Signal, 2), func() { appCancelled = true }, discardLogger(), 5*time.Second)
	}()
	waitForServer(t, port)

	// Prove the server is serving before asking whether it stopped, or the
	// check below passes just as well against a server that never worked.
	if got := awaitRequest(t, get(fmt.Sprintf("http://127.0.0.1:%d/ping", port))); got.status != http.StatusOK {
		t.Fatalf("the server did not answer before cancellation: status=%d err=%v", got.status, got.err)
	}
	if served32.Load() != 1 {
		t.Fatalf("handler ran %d times before cancellation, want 1", served32.Load())
	}

	cancel()

	select {
	case err := <-served:
		if err != nil {
			t.Errorf("Serve returned %v, want nil — cancellation is a clean shutdown, not a failure", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve ignored its context and kept serving")
	}

	if !appCancelled {
		t.Error("appCancel was not called, so application resources would outlive the server")
	}

	// It really stopped serving, rather than merely reporting that it had.
	//
	// Asserted by counting what this handler served, not by dialing the port
	// and requiring nothing to answer. freePort releases an ephemeral port
	// before the server claims it, so anything else on the machine may hold it
	// by now — that assertion tested the host's port table rather than Serve,
	// and duly failed once on a busy CI runner. Whoever owns the port now, our
	// handler must not run again.
	_ = awaitRequest(t, get(fmt.Sprintf("http://127.0.0.1:%d/ping", port)))
	if got := served32.Load(); got != 1 {
		t.Errorf("the handler served %d requests, want 1 — the server is still serving after Serve returned", got)
	}
}

// After a context-initiated shutdown, the operator has pressed nothing yet — so
// the first signal to arrive means "shut down", which is already happening, and
// must not abandon the drain. Counting it as the second press would cut every
// in-flight request on a single Ctrl-C, and on the routine SIGTERM an
// orchestrator sends during a rollout.
func TestServeAbsorbsTheFirstSignalAfterContextShutdown(t *testing.T) {
	port := freePort(t)
	ctx, cancel := context.WithCancel(t.Context())
	const handlerDuration = 400 * time.Millisecond

	handlerStarted := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /slow", func(w http.ResponseWriter, _ *http.Request) {
		close(handlerStarted)
		time.Sleep(handlerDuration)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("finished"))
	})

	server := httpapi.NewServer(mux, serverConfig(port, 10*time.Second))
	sigCh := make(chan os.Signal, 2)

	served := make(chan error, 1)
	go func() {
		served <- httpapi.Serve(ctx, server, sigCh, func() {}, discardLogger(), 10*time.Second)
	}()
	waitForServer(t, port)

	requestDone := get(fmt.Sprintf("http://127.0.0.1:%d/slow", port))

	select {
	case <-handlerStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("the request never reached the handler")
	}

	// Context first, then a single signal — the ordering that used to force a
	// close and cut the request off mid-response.
	cancel()
	sigCh <- syscall.SIGTERM

	got := awaitRequest(t, requestDone)
	if got.err != nil {
		t.Fatalf("in-flight request was cut off instead of draining: %v", got.err)
	}
	if got.status != http.StatusOK || got.body != "finished" {
		t.Errorf("in-flight request = %d %q, want 200 %q", got.status, got.body, "finished")
	}

	select {
	case err := <-served:
		if err != nil {
			t.Errorf("Serve returned %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return")
	}
}

// ...but the second one still means it, whatever started the shutdown.
func TestServeForcesCloseOnTheSecondSignalAfterContextShutdown(t *testing.T) {
	port := freePort(t)
	ctx, cancel := context.WithCancel(t.Context())
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	handlerStarted := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /stuck", func(_ http.ResponseWriter, _ *http.Request) {
		close(handlerStarted)
		<-release
	})

	// A grace period long enough that reaching it would fail the test: only the
	// second signal can make this return in time.
	server := httpapi.NewServer(mux, serverConfig(port, time.Minute))
	sigCh := make(chan os.Signal, 2)

	served := make(chan error, 1)
	go func() {
		served <- httpapi.Serve(ctx, server, sigCh, func() {}, discardLogger(), time.Minute)
	}()
	waitForServer(t, port)

	go func() {
		//nolint:noctx,bodyclose // the response never arrives; the handler is stuck by design
		_, _ = http.Get(fmt.Sprintf("http://127.0.0.1:%d/stuck", port))
	}()
	select {
	case <-handlerStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("the request never reached the stuck handler")
	}

	cancel()
	sigCh <- syscall.SIGTERM // absorbed
	sigCh <- syscall.SIGTERM // insists

	select {
	case err := <-served:
		if err != nil {
			t.Errorf("Serve returned %v, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("two signals after a context shutdown did not force the close")
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
		served <- httpapi.Serve(t.Context(), server, make(chan os.Signal, 2), func() { appCancelled = true }, discardLogger(), time.Second)
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
