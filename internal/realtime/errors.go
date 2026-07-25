package realtime

import "errors"

var (
	// ErrHubClosed is returned when the hub's context has been cancelled, which
	// happens on shutdown.
	ErrHubClosed = errors.New("realtime: hub closed")
	// ErrTooManyConnections is returned when an account or the process is
	// already at its connection cap.
	ErrTooManyConnections = errors.New("realtime: too many connections")
)
