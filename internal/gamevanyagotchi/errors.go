package gamevanyagotchi

import "errors"

// Sentinels for the inbound path. They are compared with errors.Is and never
// reach a client: an inbound frame has no reply, so a bad one is dropped. They
// exist so the parser can be tested by cause rather than by message text.
var (
	// ErrMalformedMessage means the payload was not the JSON object every frame
	// is required to be.
	ErrMalformedMessage = errors.New("gamevanyagotchi: malformed message")
	// ErrUnknownMessage means a well-formed frame of a type this game does not
	// handle. Not a failure: the room is a shared namespace and both ends are
	// required to ignore what they do not recognise.
	ErrUnknownMessage = errors.New("gamevanyagotchi: unknown message type")
	// ErrInvalidPosition means the coordinates were missing or were not finite
	// numbers. Out-of-range is not in this class — that is clamped.
	ErrInvalidPosition = errors.New("gamevanyagotchi: invalid position")
)
