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

// Sentinels for a verb — what Do decided about one, rather than what the parser
// made of a frame.
//
// They reach a client no more directly than the ones above do: a verb has no
// response body to be answered in. What reaches the PLAYER is a line over his
// Ваня's head, chosen from the sentinel by refusalLine and carried there by the
// next full-state frame — or, for a refusal he could not act on and did not
// cause, deliberately no line at all.
var (
	// ErrUnknownAction means the requested verb is not in the catalogue. A
	// client that asks for one is either stale or probing, and neither has
	// anything useful to be told: both are answered with silence.
	ErrUnknownAction = errors.New("gamevanyagotchi: unknown action")
	// ErrUnknownStat means the catalogue disagrees with itself — an action that
	// moves a stat which no longer exists. That is a content bug rather than
	// anything the player did, and nothing he could press would help, so it earns
	// the same silence as a verb that was never in the catalogue.
	ErrUnknownStat = errors.New("gamevanyagotchi: unknown stat")
	// ErrPetDead means the action needs a living pet and this one is not. Only
	// an action that can revive him is allowed through, and this is the refusal
	// the player most needs to read — «он не встаёт» — because being told is also
	// the hint about which verb to press instead.
	ErrPetDead = errors.New("gamevanyagotchi: pet is dead")
	// ErrClaimLost means somebody else got there first. Not a failure and not
	// the player's fault — it is the ordinary outcome of a contested claim for
	// everybody but one person, which is why it costs nothing but a sad face.
	ErrClaimLost = errors.New("gamevanyagotchi: somebody claimed it first")
	// ErrTooFar means the verb needs him STANDING BESIDE something and he is not.
	// The first refusal in this game about where he is rather than what he holds,
	// and it is deliberately distinct from the one below it: being too far from
	// the crate is fixed by walking over, and finding it empty is fixed by
	// waiting. Telling the player which is the whole value of having two.
	ErrTooFar = errors.New("gamevanyagotchi: too far from it")
	// ErrOutOfStock means the thing he was drawing from has nothing left. It is
	// the Stock discipline's version of ErrClaimLost — somebody else got there
	// first — and it costs exactly as little: the draw is inside the transaction,
	// so a batch refused by it writes nothing at all.
	//
	// A player should meet it rarely, because the crate is replaced the instant
	// it empties and the frame carries the count so the button can grey itself.
	// It exists because a greyed button is a suggestion, and because a count on a
	// frame is always a moment out of date.
	ErrOutOfStock = errors.New("gamevanyagotchi: nothing left in it")
	// ErrNoSpot means a search verb arrived naming no place to search — or naming
	// something that is not a place in the location its pet is standing in.
	//
	// The two are ONE SENTINEL on purpose. To the server they are the same
	// failure: nothing was named that could be looked in. A client that sends a
	// spot key from another location, or a key that has left the catalogue, or no
	// key at all, is in every case a client that has not asked a question — and
	// splitting them would mean inventing a second line for a case only a stale or
	// hostile client reaches, which is a sentence written for nobody.
	ErrNoSpot = errors.New("gamevanyagotchi: no place named to search")
	// ErrNothingHere means he searched a real place, he was standing in it, and
	// the thing he was looking for was somewhere else.
	//
	// THE ONLY REFUSAL IN THIS GAME THAT IS A MOVE IN IT. The other two a search
	// can produce tell the player he has not asked properly yet — name a place,
	// walk over — and cost him nothing. This one is the answer: he looked, and it
	// was not there. It is deliberately distinct from ErrTooFar for exactly the
	// reason ErrTooFar is distinct from ErrOutOfStock — the three want different
	// things from him, and one sentence covering them would tell him to do none.
	ErrNothingHere = errors.New("gamevanyagotchi: nothing hidden there")
	// ErrNotYet means the verb needs one of his own numbers to be further along
	// — there is nothing to do on an empty bladder. The client greys the button,
	// so a player should rarely meet this; it exists because a greyed button is
	// a suggestion and the rule has to live somewhere that cannot be declined.
	ErrNotYet = errors.New("gamevanyagotchi: not yet")
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
