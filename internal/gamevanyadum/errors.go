package gamevanyadum

import "errors"

// Package sentinels. Handlers map these to a stable error code and never to
// err.Error() — the client is told `run_in_progress`, never a sentence.
var (
	// ErrRunInProgress is returned when an account asks to start a run while it
	// already has one. It is a refusal rather than a silent replacement:
	// dropping the old arena would throw away a run somebody is in the middle of
	// on their other tab, and this game has no way of asking which one they
	// meant.
	ErrRunInProgress = errors.New("gamevanyadum: a run is already in progress")

	// ErrNoRun is returned when something asks about a run that is not there —
	// usually a socket saying hello after its arena was ended or abandoned.
	ErrNoRun = errors.New("gamevanyadum: no run in progress")

	// ErrTooManyArenas is returned when the process is already simulating as
	// many runs as it is willing to. It is a capacity limit rather than a rate
	// limit: one arena is one 20 Hz simulation, and this game runs on the same
	// single box as everything else.
	ErrTooManyArenas = errors.New("gamevanyadum: too many runs in progress")
)
