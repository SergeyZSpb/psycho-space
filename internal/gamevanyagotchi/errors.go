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

// Sentinels for the HTTP path. Unlike the ones above these DO reach a client, as
// a stable error code chosen by the handler — never as this text.
var (
	// ErrUnknownAction means the requested verb is not in the catalogue. A
	// client that asks for one is either stale or probing; both are the same
	// 404.
	ErrUnknownAction = errors.New("gamevanyagotchi: unknown action")
	// ErrUnknownStat means the catalogue disagrees with itself — an action that
	// moves a stat which no longer exists. That is a content bug rather than
	// anything the caller did, so it is a 500 and not a 404.
	ErrUnknownStat = errors.New("gamevanyagotchi: unknown stat")
	// ErrPetDead means the action needs a living pet and this one is not. Only
	// an action that can revive him is allowed through.
	ErrPetDead = errors.New("gamevanyagotchi: pet is dead")
)

// ErrBatchTooLong rejects a frame carrying more verbs than maxBatch.
//
// A batch is folded and written in one transaction, so its length is the amount
// of work one message can ask for. Uncapped, a single frame at the socket's
// permitted rate is an arbitrarily large multiplier on database writes — which
// is exactly the property movement does not have, because movement writes
// nothing.
var ErrBatchTooLong = errors.New("gamevanyagotchi: too many verbs in one batch")

// ErrNoVerbs rejects a verb frame that asks for nothing, or for a blank verb.
//
// Separate from ErrUnknownAction because it is a SHAPE failure rather than a
// content one: the frame is malformed, and no catalogue lookup would help.
var ErrNoVerbs = errors.New("gamevanyagotchi: no verbs in the frame")
