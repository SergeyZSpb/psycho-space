package gamefintech

import "errors"

// Package sentinels. Handlers map these to a stable error code and never to
// err.Error() — the client is told `shift_in_progress`, never a sentence.
var (
	// ErrShiftInProgress is returned when an account asks to start a shift while
	// it already has one. It is a refusal rather than a silent replacement:
	// dropping the running shift would throw away one somebody is in the middle
	// of on their other tab, and nothing here can tell which one they meant. The
	// client's answer is the quit button.
	ErrShiftInProgress = errors.New("gamefintech: a shift is already in progress")

	// ErrNoShift is returned when something asks about a shift that is not there
	// — a reload after the bald man got you, a quit button pressed twice, a
	// socket saying hello after its shift ended.
	ErrNoShift = errors.New("gamefintech: no shift in progress")

	// ErrOfficeFull is returned when the floor already holds MaxOccupants. It is
	// a capacity limit rather than a rate limit: this is one open-plan office
	// shared by everybody, not a lobby that spawns another.
	ErrOfficeFull = errors.New("gamefintech: the office is full")
)
