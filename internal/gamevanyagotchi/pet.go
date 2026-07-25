package gamevanyagotchi

import "time"

// The durable half of the game: one Ваня per account, and the stats that change
// on their own while nobody is looking.
//
// Nothing here is presence. The plane's positions live in memory and die with
// the process (see Service); everything in this file outlives a deploy, which is
// exactly why it could not land until a revoked session could no longer keep a
// socket open.

// Pet is one player's дядя Ваня.
//
// There is no "run" in this game — the pet is persistent rather than a bounded
// play-through — which is why nothing here resembles the first game's runs
// table, and why this game's records will be zero-decay stat rows rather than a
// second runs table.
type Pet struct {
	// AccountID never goes on the wire. The pet is only ever fetched by its
	// owner, so it would tell that owner nothing they did not already know, and
	// a durable account identifier is not something this project puts in a
	// response body out of habit.
	AccountID string `json:"-"`

	ID string `json:"id"`
	// Name is set in a dialog and is nil until then. Deliberately not editable
	// on the play surface: the on-screen keyboard is the one case the
	// never-scroll layout handles badly, so the whole bug class is designed out
	// by putting every text input behind a dialog.
	Name        *string `json:"name"`
	SkinKey     string  `json:"skin_key"`
	LocationKey string  `json:"location_key"`
	// DiedAt is the moment hp reached zero, written exactly once by the first
	// read that observed it. nil means alive.
	DiedAt    *time.Time `json:"died_at"`
	CreatedAt time.Time  `json:"created_at"`
}

// Alive reports whether the pet is currently not dead.
func (p Pet) Alive() bool { return p.DiedAt == nil }

// StatRow is a stat exactly as it is stored: the pair the decay reads.
type StatRow struct {
	Key   string
	Value float64
	AsOf  time.Time
}

// StatValue is a stat as the client receives it — decayed to the instant of the
// read, with the pair it was decayed from carried alongside.
//
// Both are sent on purpose. Value is what to draw right now; (Value, AsOf)
// together with the rate the client already has from the catalogue let it keep
// the bar moving between fetches without asking again. That interpolation is
// DISPLAY ONLY: the client never sends a stat value back, and every action is
// answered with the server's own recomputed state, so a client that drifts is
// corrected the next time it does anything at all.
type StatValue struct {
	Key   string    `json:"key"`
	Value float64   `json:"value"`
	AsOf  time.Time `json:"as_of"`
	// RatePerHour is the drain this stat is suffering right now, penalties
	// included — which is generally NOT the catalogue's own rate, because health
	// falls faster while a need is unmet.
	//
	// Sent so the client can interpolate without owning a second copy of the
	// coupling: it would otherwise need the thresholds, the onset arithmetic and
	// every driver's trajectory, i.e. a full reimplementation of decay.go in
	// TypeScript, kept honest by nothing. It is correct until the next onset,
	// which is hours away, and every action answers with fresh server state.
	RatePerHour float64 `json:"rate_per_hour"`
}

// State is the whole answer to GET /state.
type State struct {
	Pet   Pet         `json:"pet"`
	Stats []StatValue `json:"stats"`
	Alive bool        `json:"alive"`
	// ServerNow is the clock everything above was computed against, so the
	// client can correct for a skewed device clock rather than assuming its own
	// Date.now() agrees. The server never reads a client's time.
	ServerNow time.Time `json:"server_now"`
}
