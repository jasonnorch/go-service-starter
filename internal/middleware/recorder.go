package middleware

import "net/http"

// recorder wraps a ResponseWriter to observe what was written to it.
//
// http.ResponseWriter deliberately offers no way to read back the status that
// was sent, so anything that reports on a response after the fact — an access
// log, a metric, a recover handler deciding whether it is too late to send a
// 500 — has to intercept it on the way out.
type recorder struct {
	http.ResponseWriter

	status  int
	written int64
	wrote   bool
}

func newRecorder(w http.ResponseWriter) *recorder {
	// 200 is what net/http sends if a handler writes a body without ever
	// calling WriteHeader, so it is the correct value before anything happens
	// rather than a placeholder.
	return &recorder{ResponseWriter: w, status: http.StatusOK}
}

func (r *recorder) WriteHeader(status int) {
	// A second WriteHeader is a bug in the handler; net/http logs and ignores
	// it. Recording the first keeps the log agreeing with what the client got.
	if r.wrote {
		return
	}
	r.status = status
	r.wrote = true
	r.ResponseWriter.WriteHeader(status)
}

func (r *recorder) Write(b []byte) (int, error) {
	if !r.wrote {
		r.status = http.StatusOK
		r.wrote = true
	}
	n, err := r.ResponseWriter.Write(b)
	r.written += int64(n)
	return n, err
}

// Unwrap exposes the underlying writer to http.ResponseController, which is how
// a handler reaches Flush, Hijack, or the per-request deadlines through a
// wrapper. Without it, wrapping a ResponseWriter silently disables streaming
// and WebSocket upgrades — the classic cost of middleware that wraps the
// writer, and the reason ResponseController exists.
func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// Status returns the status sent to the client.
func (r *recorder) Status() int { return r.status }

// Written reports whether the response has begun. Once it has, the status
// cannot be changed — which is what Recover needs to know.
func (r *recorder) Written() bool { return r.wrote }

// BytesWritten returns the size of the body written so far.
func (r *recorder) BytesWritten() int64 { return r.written }

// responseObserver is the part of recorder that other middleware needs. It is
// declared as an interface so Recover can work with whatever wrapper is in
// play — this one, or a future replacement — instead of being coupled to a
// concrete type it does not create.
type responseObserver interface {
	Status() int
	Written() bool
	BytesWritten() int64
}
