package httpapi

import (
	"net/http"
	"time"

	"github.com/SergeyZSpb/psycho-space/internal/observability"
)

// Timeouts bounds an HTTP server's I/O. Extracted from main so a test can build
// a server with short timeouts and prove that a hijacked connection is not
// killed by them — see TestHijackedConnOutlivesServerWriteTimeout. Waiting out the
// production 30 s in a test is not an option, and the integration harness uses
// httptest.NewServer, which sets no timeouts at all and so cannot catch the
// regression either.
type Timeouts struct {
	ReadHeader time.Duration
	Read       time.Duration
	Write      time.Duration
	Idle       time.Duration
}

// DefaultTimeouts are the production values.
func DefaultTimeouts() Timeouts {
	return Timeouts{
		ReadHeader: 10 * time.Second,
		Read:       30 * time.Second,
		Write:      30 * time.Second,
		Idle:       120 * time.Second,
	}
}

// NewHTTPServer builds the production HTTP server around a handler.
//
// The folklore around golang/go#8296 says these Read/Write timeouts are
// inherited by a hijacked connection, so an upgrading handler must clear the
// deadlines itself. On current Go that is no longer true: net/http's
// hijackLocked clears the deadline as part of the hijack, so handleRealtime
// deliberately does NOT clear it again, and clearing it there would be dead
// code. TestHijackedConnOutlivesServerWriteTimeout pins that as a negative
// control, so a regression in either direction is caught rather than assumed.
func NewHTTPServer(addr string, h http.Handler, t Timeouts) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           observability.WrapHandler(h, "http.server"),
		ReadHeaderTimeout: t.ReadHeader,
		ReadTimeout:       t.Read,
		WriteTimeout:      t.Write,
		IdleTimeout:       t.Idle,
	}
}
