package gamevanyagotchi

import "time"

// THE CATALOGUE.
//
// Every piece of content this game has lives in this one file: the stats and
// their rates, the actions that move them, the skins, the locations. It is
// served whole to the SPA by GET /api/game-vanyagotchi/config, so the client
// hardcodes no key, no label and no number — which is what makes adding a stat,
// an action or a location a backend-only change with no migration and no client
// deploy. That property is the entire reason this file exists rather than the
// values being scattered across the service, and it is worth defending: a
// constant that ends up in the Vue source has quietly cost a deploy, and one
// that ends up in a Postgres enum has quietly cost a migration.
//
// The rule for what belongs here: content is anything a person would change by
// feel. Rates, labels, emoji, bounds, which stat an action restores. What does
// NOT belong here is behaviour — a genuinely new verb is a service method, a
// handler and a route, and no catalogue can make that free.

// GameKey identifies this game in shared, multi-game storage. Today that is the
// art blob store, which is keyed on (game_key, art_key) — infrastructure rather
// than this game's property, which is why the value appears here and the table
// does not appear in this game's migration.
//
// It is a VALUE, not a name: the game's own tables carry their identity in their
// names, so nothing in this package's schema has a game_key column.
const GameKey = "vanyagotchi"

// Stat keys. Two of them, and two is the minimum that earns a tall stats table:
// with one stat, a column would have been the honest choice.
const (
	// StatHP is the decay loop — the timer at the centre of the game.
	StatHP = "hp"
	// StatBladder is the relief loop's half of the pair. It FILLS rather than
	// drains, which is why the rate below is signed rather than a magnitude:
	// one expression covers both directions and there is no second code path
	// for a stat that goes the other way.
	StatBladder = "bladder"
)

// Action keys.
const (
	// ActionHeal restores hp — the timely dose that keeps him going.
	ActionHeal = "heal"
)

// Skin and location keys.
const (
	// SkinVanya is the only pet skin: he is дядя Ваня, which is the joke.
	SkinVanya = "vanya"
	// LocationYard is двор — the shared plane, and for now the only place there
	// is. A location is deliberately NOT a realtime room: rooms are a closed set
	// owned by the platform, so making locations rooms would mean a game teaching
	// a platform file a new name, and it would split a yard of five people across
	// six empty places.
	LocationYard = "yard"
)

// Stat is one thing about a pet that changes with time on its own.
//
// The whole model is (Value, AsOf) in the database plus this definition:
//
//	current = clamp(value − DecayPerHour × hoursSince(asOf), Min, Max)
//
// evaluated on read. Nothing ticks, nothing accumulates, and a value read after
// a month away costs exactly what a value read a second later costs.
type Stat struct {
	Key   string  `json:"key"`
	Label string  `json:"label"`
	Emoji string  `json:"emoji"`
	Min   float64 `json:"min"`
	Max   float64 `json:"max"`
	// Start is the value a freshly-created pet gets.
	Start float64 `json:"start"`
	// DecayPerHour is SIGNED: positive drains towards Min, negative fills
	// towards Max, zero is a lifetime counter that only actions move.
	//
	// One signed rate rather than a direction flag plus a magnitude, because the
	// subtraction then has no cases in it — and a stat with no cases in its
	// arithmetic is a stat that cannot decay wrongly in one direction only.
	DecayPerHour float64 `json:"decay_per_hour"`
	// GoodHigh says which end of the scale is the happy one, so the client can
	// colour a bar without knowing what the stat means.
	GoodHigh bool `json:"good_high"`
	// WarnAt is the value at which the stat starts reading as trouble: below it
	// when GoodHigh, above it otherwise. Here rather than in the stylesheet for
	// the same reason the rate is here — it is a number somebody will want to
	// move by feel.
	WarnAt float64 `json:"warn_at"`
	// Fatal: reaching Min kills him. Exactly one stat is fatal today, and the
	// flag exists rather than the service naming StatHP directly so that "which
	// stat can kill" stays a property of content.
	Fatal bool `json:"fatal"`
}

// Action is a verb that moves one stat by a fixed amount.
//
// Every action here is the SAME mechanic — apply a delta, clamp it against the
// stat's bounds, write it down — which is why they can be catalogue rows served
// through one endpoint rather than a route each. A genuinely different verb (the
// relief that also writes a deposit into the world, the claim that races other
// players for a key) is new behaviour and gets its own route; nothing about this
// list is trying to be a plugin system for those.
type Action struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Emoji string `json:"emoji"`
	// StatKey is what it moves; Delta is by how much, signed, before clamping.
	StatKey string  `json:"stat_key"`
	Delta   float64 `json:"delta"`
	// Done is the line shown for a moment after it lands.
	Done string `json:"done"`
	// RevivesFatal: allowed on, and undoes, a death. See Service.Act.
	RevivesFatal bool `json:"revives_fatal"`
}

// Skin is one look for a pet: an art key resolved against the shared blob store,
// with an emoji-over-gradient placeholder for as long as no image is uploaded —
// the identical resolution the first game already does for its arts. It is what
// lets the art land in a later iteration as an upload rather than as code.
type Skin struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Emoji    string `json:"emoji"`
	Gradient string `json:"gradient"`
	Image    string `json:"image,omitempty"`
}

// Location is a place a pet can be. Only двор exists today; лес, лифт, кусты and
// заброшка arrive with the search minigame, as catalogue entries.
type Location struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	// Entry is where a pet stands on arriving, in the same normalised 0..1
	// coordinates the plane uses.
	Entry Point `json:"entry"`
}

// Config is the whole catalogue as the SPA receives it.
type Config struct {
	GameKey   string     `json:"game_key"`
	Title     string     `json:"title"`
	Stats     []Stat     `json:"stats"`
	Actions   []Action   `json:"actions"`
	Skins     []Skin     `json:"skins"`
	Locations []Location `json:"locations"`
	// DefaultSkin and DefaultLocation are what a new pet is created with.
	DefaultSkin     string `json:"default_skin"`
	DefaultLocation string `json:"default_location"`
}

// The rates below are ours and are not copied from anything.
//
// Worth stating plainly because it is the sort of number a later reader assumes
// was inherited: there is no authentic Tamagotchi depletion rate to be faithful
// to — Bandai never published one and no Connection-era ROM has been recovered
// far enough to know. Every per-version figure on a guide site is folklore. So
// these are chosen for the rhythm this particular group of friends will actually
// play at, and they are meant to be moved by feel.
//
// hp: 3 per hour from 100, so a full Ваня reaches zero in about 33 hours. That
// makes checking in once a day comfortable and forgetting for a day and a half
// fatal — and fatal here is one tap to undo, which is deliberate. The research
// this design rests on is blunt about the ceiling: mildly annoying and cheaply
// reversible keeps people coming back, irreversible loses them, and a friend
// group is a place where churn is somebody you will see socially.
//
// bladder: 5 per hour, filling, so he is bursting after 20 hours. Nothing
// relieves it yet — that verb writes a deposit into the shared world and arrives
// with the world objects — so for now it is a stat that fills and waits, which
// is honest rather than a placeholder.
const (
	hpDecayPerHour     = 3.0
	bladderFillPerHour = 5.0
	healDelta          = 35.0
	statMax            = 100.0
	// hpStart is below statMax on purpose — see the Start field's comment. It
	// also means a new pet is about 22 hours from death rather than 33, which is
	// still comfortably more than a day of not looking.
	hpStart = 65.0
)

// catalogue is the single instance. Package-level and read-only after
// initialisation — no init() and nothing mutates it, so it can be handed out
// without copying anything defensively except the slices Config exposes.
var catalogue = Config{
	GameKey: GameKey,
	Title:   "Ванягоччи",
	Stats: []Stat{
		{
			Key:   StatHP,
			Label: "здоровье",
			Emoji: "❤️",
			Min:   0,
			Max:   statMax,
			// Deliberately BELOW the maximum, and this is the one number here
			// chosen for how the game feels on the first screen rather than for
			// pacing. Starting a pet at full health makes the very first press of
			// the only action a clamped no-op: the player taps «поправить
			// здоровье», the bar does not move, and the game looks broken on the
			// one interaction they were invited to try. Meeting дядя Ваня already
			// a bit rough is also the truer fiction, and it gives the first tap
			// something to do.
			Start:        hpStart,
			DecayPerHour: hpDecayPerHour,
			GoodHigh:     true,
			WarnAt:       30,
			Fatal:        true,
		},
		{
			Key:   StatBladder,
			Label: "мочевой пузырь",
			Emoji: "🚽",
			Min:   0,
			Max:   statMax,
			Start: 0,
			// Negative: it fills. See Stat.DecayPerHour.
			DecayPerHour: -bladderFillPerHour,
			GoodHigh:     false,
			WarnAt:       70,
			Fatal:        false,
		},
	},
	Actions: []Action{
		{
			Key:   ActionHeal,
			Label: "поправить здоровье",
			Emoji: "💊",
			// The wording is deliberately neutral and is expected to change: the
			// mechanic the owner specified is «приём веществ вовремя», and this
			// file is where that call gets made. Changing it is one string and a
			// backend deploy, with no client change — which is the point.
			StatKey: StatHP,
			Delta:   healDelta,
			Done:    "полегчало",
			// Also the way back from a death, which is why death is a scare
			// rather than an ending. See Service.Act.
			RevivesFatal: true,
		},
	},
	Skins: []Skin{
		{
			Key:      SkinVanya,
			Label:    "дядя Ваня",
			Emoji:    "🫃",
			Gradient: "linear-gradient(160deg, #6b4a2f, #2f4a6b)",
		},
	},
	Locations: []Location{
		{Key: LocationYard, Label: "двор", Entry: spawn},
	},
	DefaultSkin:     SkinVanya,
	DefaultLocation: LocationYard,
}

// Content returns the catalogue as served to the SPA.
//
// A copy of the struct, with fresh slices, so a handler that decorates it — as
// the config handler does, filling in art URLs for skins that have an uploaded
// blob — cannot write through into the package's own copy and leave every later
// request looking at a mutated catalogue. The first game learned this by having
// exactly that shape and needing exactly this care.
func Content() Config {
	c := catalogue
	c.Stats = append([]Stat(nil), catalogue.Stats...)
	c.Actions = append([]Action(nil), catalogue.Actions...)
	c.Skins = append([]Skin(nil), catalogue.Skins...)
	c.Locations = append([]Location(nil), catalogue.Locations...)
	return c
}

// StatByKey looks a stat up in the catalogue.
func StatByKey(key string) (Stat, bool) {
	for _, s := range catalogue.Stats {
		if s.Key == key {
			return s, true
		}
	}
	return Stat{}, false
}

// ActionByKey looks an action up in the catalogue.
func ActionByKey(key string) (Action, bool) {
	for _, a := range catalogue.Actions {
		if a.Key == key {
			return a, true
		}
	}
	return Action{}, false
}

// Stats returns the catalogue's stat definitions, in catalogue order. The order
// is the display order: it is content too.
func Stats() []Stat { return append([]Stat(nil), catalogue.Stats...) }

// hour is the unit every rate in this file is expressed in. Named so the
// conversion appears once, next to the catalogue that depends on it, rather than
// as a bare float64(time.Hour) somewhere in the decay arithmetic.
const hour = float64(time.Hour)
