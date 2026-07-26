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

// Stat keys.
//
// Two of them are NEEDS the player acts on, and one is the CONSEQUENCE of
// neglecting them. That split is the shape of the game: you cannot touch health
// directly, you keep him watered and let him out, and health follows.
const (
	// StatHP is the consequence. It barely rots on its own; what kills him is
	// an empty beer and a full bladder, through the penalties below.
	StatHP = "hp"
	// StatBeer is how much beer is in Ваня. It drains, and running dry hurts.
	StatBeer = "beer"
	// StatBladder is the other half of the pair. It FILLS rather than drains,
	// which is why every rate here is signed rather than a magnitude: one
	// expression covers both directions and there is no second code path for a
	// stat that goes the other way.
	StatBladder = "bladder"
)

// Action keys.
const (
	// ActionDrink is the one that helps: it tops him up, cheers him up, and
	// fills his bladder, which is what makes the second verb necessary.
	ActionDrink = "drink"
	// ActionRelieve empties the bladder. It is the other half of the loop
	// drinking creates.
	ActionRelieve = "relieve"
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
	// Penalties are the extra drain this stat suffers while OTHER stats sit in a
	// bad range — the coupling that turns two needs into one consequence.
	//
	// Only a stat that no other stat depends on may appear as a driver, and only
	// a stat with no penalties may drive: the dependency graph is one layer deep
	// on purpose, because that is what keeps the decay closed-form and exact.
	// See decay.go, and ADR-040 for why that is not a detail.
	Penalties []Penalty `json:"penalties,omitempty"`
}

// StatDelta is one stat moved by one amount, before clamping.
//
// It exists because an action stopped being able to move a single stat: drinking
// raises the beer, cheers him up AND fills his bladder, which is the whole joke
// and also the reason the relief verb has anything to do. One value type plus
// one loop that applies a slice of them is the entire mechanism — the design
// note that predicted this seam also predicted it would be a struct and a
// function rather than a framework, and it is.
type StatDelta struct {
	StatKey string  `json:"stat_key"`
	Delta   float64 `json:"delta"`
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
	// Effects is what it moves, in order, each clamped against its own stat's
	// bounds. A delta larger than the range is the idiomatic way to say "reset":
	// relieving himself sends the bladder down by the whole scale and the clamp
	// makes that exactly its floor.
	Effects []StatDelta `json:"effects"`
	// Done is the line shown for a moment after it lands.
	Done string `json:"done"`
	// RevivesFatal: allowed on, and undoes, a death. Deliberately not true of
	// every action — a dead Ваня cannot go to the toilet, and that is what makes
	// the refusal path real rather than theoretical. See Service.Act.
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

// NPC is somebody in the yard who is not a player.
//
// THREE INDEPENDENT AXES, and keeping them independent is what stops NPCs
// multiplying: what he LOOKS like is Art, how he MOVES is a Pattern key resolved
// against the motion table, and what happens when you touch him will be an
// interaction key resolved against a registry that does not exist yet and lands
// with its second use. Because the three are separate, N characters × M ways of
// moving costs N + M rather than N × M — and a character reusing an existing
// pattern with different numbers costs one entry here and no code at all.
//
// NPCS HAVE NO ROWS AND NEED NONE. Appearance is catalogue and position is a
// pure function of (params, elapsed), so there is nothing about one to store and
// adding one therefore cannot require a migration. Anything about an NPC that IS
// mutable is not the NPC: the vendor's beer stock is a world object with a count,
// and the vendor is the stateless thing standing next to it. Keeping that split
// is what preserves "a new character is a Go-file change".
type NPC struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	// Art is a catalogue key the client resolves exactly as it resolves a pet's
	// skin — which is why an NPC needs no client work at all: to the browser it
	// is one more entity in the roster.
	Art     string       `json:"art"`
	Pattern PatternKey   `json:"-"`
	Params  MotionParams `json:"-"`
}

// Config is the whole catalogue as the SPA receives it.
type Config struct {
	GameKey string   `json:"game_key"`
	Title   string   `json:"title"`
	Stats   []Stat   `json:"stats"`
	Actions []Action `json:"actions"`
	Skins   []Skin   `json:"skins"`
	// NPCs are published so the client can resolve their art, and for no other
	// reason: where they are arrives in the roster like everything else. Their
	// Pattern and Params are json:"-" — how a character moves is nobody's
	// business but the server's, and sending it would invite a second
	// implementation in TypeScript.
	NPCs      []NPC      `json:"npcs"`
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
// hp barely rots on its own — one point an hour, so perfect care still wilts him
// over about four days and nobody is ever quite finished. What actually kills him
// is neglect, expressed as two penalties of six an hour each: an empty beer and a
// full bladder. Ignore one and he loses seven an hour; ignore both and he loses
// thirteen, which takes a full Ваня down in under eight hours. That is the whole
// causal story of the game, and it is legible from the bars alone: the number you
// cannot press is driven by the two you can.
//
// beer: 4 an hour from 60, so a new Ваня is dry in ten hours and starts taking
// damage there. Drinking puts 40 back.
//
// bladder: 5 an hour on its own, plus 25 every drink — which is what stops the
// two loops being independent chores. Drink to keep him alive, and drinking is
// what makes him need the toilet.
//
// A fresh, entirely untended pet therefore dies at roughly seventeen hours: ten
// on one point an hour, then seven an hour once the beer runs out. Skip a day and
// he is gone; and death is one tap to undo, which is deliberate. The research
// this design rests on is blunt about the ceiling — mildly annoying and cheaply
// reversible keeps people coming back, irreversible loses them, and a friend
// group is a place where churn is somebody you will see socially.
//
// Every number here is meant to be moved by feel, and moving one is a backend
// deploy with no migration and no client change.
const (
	statMax = 100.0

	// The consequence.
	hpDecayPerHour = 1.0
	hpStart        = 65.0
	// hpPenaltyPerHour is what EACH unmet need adds to that drain.
	hpPenaltyPerHour = 6.0

	// The needs.
	beerDrainPerHour   = 4.0
	beerStart          = 60.0
	beerEmptyAt        = 20.0
	bladderFillPerHour = 5.0
	bladderFullAt      = 80.0

	// What the verbs do.
	drinkBeer    = 40.0
	drinkHP      = 15.0
	drinkBladder = 25.0
)

// How a Ваня crosses the yard.
//
// Before this the position WAS the tap: the server clamped it and broadcast it,
// and the client slid the dot over 220 ms whatever the distance — so the far
// side of the plane was 220 ms away and distance meant nothing. Distance has to
// mean something, because the beer delivery is a race to ARRIVE.
const (
	// walkSpeed is in plane-widths per second, which is why the plane has a
	// fixed 3:4 shape: a speed in plane-widths only means the same thing to two
	// players if a plane-width does.
	//
	// A fifth of the yard a second, so crossing it corner to corner takes about
	// seven — long enough to be a journey, short enough that nobody is bored.
	walkSpeed = 0.2

	// tiredFor is how long he sits getting his breath back after giving up.
	tiredFor = 4 * time.Second

	// tiredFrom is the distance below which he never gives up at all: a short
	// hop always works, and only an ambitious tap can fail.
	tiredFrom = 0.45
	// tiredChance is the probability of giving up on a tap right across the
	// yard, scaling down to nothing at tiredFrom.
	//
	// Raised from 0.35 on the owner's instruction to make him give up more
	// often. It is deliberately the only knob turned for that: lowering
	// tiredFrom would have done it too, but tiredFrom is the guarantee that a
	// short hop ALWAYS works, which is what stops a Ваня ever being stuck — and
	// several tests depend on being able to move somebody reliably by staying
	// inside it. Frequency is a probability, not a threshold.
	tiredChance = 0.7
	// tiredEarliest / tiredLatest bound where he stops, as a fraction of the
	// journey. Never so early that the tap looks ignored, never so late that
	// giving up is indistinguishable from arriving.
	tiredEarliest = 0.35
	tiredLatest   = 0.8
)

// worldEpoch is when this world started. Every closed-form position is measured
// from it, so it must be a FIXED instant rather than process start: two
// processes — or the same one after a deploy — have to agree about where an NPC
// is, and a per-process epoch would teleport the whole cast on every restart.
var worldEpoch = time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC)

// maxDistance is the longest walk the plane allows, corner to corner. Used to
// scale the tiredness roll, so "how ambitious is this tap" means the same thing
// whatever the numbers around it become.
var maxDistance = distance(Point{X: 0, Y: 0}, Point{X: 1, Y: 1})

// tiredSays is what he announces when he gives up part way.
//
// It converts a limitation into content, which is the whole point: a speed cap
// alone reads as a tax, whereas a Ваня who sits down halfway across the yard and
// says his leg is falling off IS the game. Same mechanical effect, opposite
// feeling — and a pool rather than one line, because the joke was wearing out
// after the third time anybody read the same word.
//
// Which line a given giving-up uses is decided once, with the walk, and carried
// on it. Not re-picked per tick, which would flicker through the whole list
// while he sat there, and not picked by the client, which would show two people
// different words for the same event.
var tiredSays = []string{
	"устал",
	"нога отваливается",
	"спина болит",
	"перекур",
	"чёт я сдох",
	"дыхалки нет",
	"ноги не идут",
	"колено щёлкает",
	"надо было такси",
	"я не спортсмен",
}

// idleSays is what a Ваня mutters to himself while standing about.
//
// The yard is mostly people doing nothing — that is what a yard is — so without
// this it is a still life. The lines are the point: not status, not feedback on
// anything the player did, just the ambient noise of somebody who has been
// standing outside for a while.
var idleSays = []string{
	"где ключи",
	"я вообще норм",
	"кто взял мою зажигалку",
	"так, где я",
	"пивка бы",
	"мамка звонила",
	"курить хочется",
	"да я в порядке",
	"чё стоим",
	"вроде дождь собирается",
	"я на пять минут вышел",
	"кладмен мудак",
	"зачем я вышел",
	"телефон сел",
	"холодно чёт",
}

// How the idle muttering is timed, and why there is no timer.
//
// Time is cut into fixed slots measured from worldEpoch. Within one slot an
// account either says something or does not, decided by hashing (account, slot),
// and the line is shown for the first idleSayFor of it. That is the same
// discipline as everything else that moves here — a pure function of absolute
// time (ADR-042) — and it buys three things a scheduler would not. Nothing is
// stored, so a balloon costs no memory and cannot leak. Nothing expires, so
// nothing has to be cleaned up: the slot simply ends. And every client computes
// the identical answer, so two people watching the same Ваня see the same words
// appear and disappear together, without a message being sent to say so.
const (
	// idlePeriod is one slot: at most one remark per account per slot.
	idlePeriod = 12 * time.Second
	// idleSayFor is how long the remark stays up, at the start of its slot.
	// Long enough to read at a glance, short enough that the yard is quiet more
	// often than it is talking.
	idleSayFor = 4 * time.Second
	// idleChance is how often a slot produces anything at all. At a quarter, a
	// given Ваня speaks about once a minute — a murmur rather than a chorus,
	// which matters because thirty sleepers do not talk but a dozen present
	// players might.
	idleChance = 0.25
)

// sayMax is the longest line the server will put over a Ваня's head.
//
// The client caps at the same number by code point. Capping here as well means
// the server never sends a line that would be silently shortened on the way in:
// a message the player half receives is worse than one written to fit.
const sayMax = 24

// maxBatch is how many verbs one message may carry.
//
// Eight is far past what a person presses at once and far short of what makes a
// single frame an interesting way to load the database. The cap exists because
// a verb writes and movement does not: the socket's ten-messages-a-second bound
// is sized for taps, and without a length limit one frame inside that bound
// could ask for any number of transactions' worth of work.
const maxBatch = 8

// sleeperLimit is how many absent Ваняs the yard renders lying about.
//
// The point of them is that a solo visit is a place rather than a menu, and
// thirty bodies is far past the number at which that reads. It also bounds the
// frame: without a cap, every pet who ever played would be in every frame
// forever, and the roster would grow with the age of the game rather than with
// the size of the group.
const sleeperLimit = 30

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
			// Deliberately BELOW the maximum, so the very first press of a verb
			// is not a clamped no-op: a bar that does not move on the one
			// interaction a new player was invited to try reads as a broken game.
			// Meeting дядя Ваня already a bit rough is the truer fiction anyway.
			Start:        hpStart,
			DecayPerHour: hpDecayPerHour,
			GoodHigh:     true,
			WarnAt:       30,
			Fatal:        true,
			// The two needs, and the reason health is a consequence rather than
			// a chore of its own. Each threshold is the SAME number as the
			// driving stat's WarnAt, on purpose: the bar turns amber at exactly
			// the moment it starts costing him health, so the warning colour
			// means something instead of being decoration.
			Penalties: []Penalty{
				{WhenKey: StatBeer, Threshold: beerEmptyAt, Above: false, RatePerHour: hpPenaltyPerHour},
				{WhenKey: StatBladder, Threshold: bladderFullAt, Above: true, RatePerHour: hpPenaltyPerHour},
			},
		},
		{
			Key:          StatBeer,
			Label:        "пиво",
			Emoji:        "🍺",
			Min:          0,
			Max:          statMax,
			Start:        beerStart,
			DecayPerHour: beerDrainPerHour,
			GoodHigh:     true,
			WarnAt:       beerEmptyAt,
			Fatal:        false,
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
			WarnAt:       bladderFullAt,
			Fatal:        false,
		},
	},
	Actions: []Action{
		{
			Key:   ActionDrink,
			Label: "выпить пива",
			Emoji: "🍺",
			// Three stats, and the third one is the joke: the drink that keeps
			// him alive is also what sends him looking for a bush. Without it the
			// two loops would be unrelated chores rather than one system.
			Effects: []StatDelta{
				{StatKey: StatBeer, Delta: drinkBeer},
				{StatKey: StatHP, Delta: drinkHP},
				{StatKey: StatBladder, Delta: drinkBladder},
			},
			Done: "хорошо пошло",
			// The way back from a death, which is why death is a scare rather
			// than an ending — and it is in character that beer is what does it.
			RevivesFatal: true,
		},
		{
			Key:   ActionRelieve,
			Label: "покакать",
			Emoji: "💩",
			// A delta larger than the whole scale, so the clamp lands it exactly
			// on the floor. "Reset" needs no mechanism of its own.
			Effects: []StatDelta{{StatKey: StatBladder, Delta: -statMax}},
			Done:    "полегчало",
			// A dead Ваня does not go to the toilet. This is the action that
			// makes the refusal real rather than theoretical — and it is why the
			// screen has to say what to do instead.
			RevivesFatal: false,
		},
	},
	Skins: []Skin{
		{
			Key:      SkinVanya,
			Label:    "дядя Ваня",
			Emoji:    "🫃",
			Gradient: "linear-gradient(160deg, #6b4a2f, #2f4a6b)",
		},
		// The NPCs' art lives in the SAME list as the pets', because to the
		// client there is no such thing as an NPC: it resolves whatever art key
		// an entity carries against this one catalogue. A skin missing here is
		// not an error — the browser draws its placeholder — but it does mean a
		// character with no face, so an NPC ships with one.
		{
			Key:      "npc_sahur",
			Label:    "Тунг Тунг Сахур",
			Emoji:    "🪵",
			Gradient: "linear-gradient(160deg, #7a5b3a, #3a2a1a)",
		},
		{
			Key:      "npc_ballerina",
			Label:    "Балерина Каппучина",
			Emoji:    "🩰",
			Gradient: "linear-gradient(160deg, #d8b08c, #6b4a2f)",
		},
		{
			Key:   "npc_67man",
			Label: "Сиксти Севен Мэн",
			// Not an emoji, and that is allowed on purpose: the field is a short
			// string the client centres inside the entity's own circular dot, so
			// "67" renders as the roundel the character is named after. Nothing
			// downstream assumes a single glyph — `resolveArt` hands the string
			// through untouched — which is what makes a text badge available as
			// art without a sprite, an upload or a schema change.
			Emoji:    "67",
			Gradient: "linear-gradient(160deg, #e0762b, #6d2f0c)",
		},
	},
	Locations: []Location{
		{Key: LocationYard, Label: "двор", Entry: spawn},
	},
	// The yard's regulars. Three of them across two ways of moving, which is
	// exactly what the pattern table exists for: the third arrived as a
	// catalogue entry and nothing else — no code, no migration, no client
	// change — because he reuses a pattern that was already written.
	NPCs: []NPC{
		{
			Key:     "sahur",
			Label:   "Тунг Тунг Сахур",
			Art:     "npc_sahur",
			Pattern: PatternWander,
			// Ambling the top half of the yard, going nowhere in particular over
			// about a minute and a half.
			Params: MotionParams{
				Home:   Point{X: 0.5, Y: 0.3},
				Spread: Point{X: 0.32, Y: 0.16},
				Period: 95 * time.Second,
			},
		},
		{
			Key:     "ballerina",
			Label:   "Балерина Каппучина",
			Art:     "npc_ballerina",
			Pattern: PatternPatrol,
			// A repeating figure with a pause at each corner, which is the joke:
			// a ballerina doing the same four steps forever.
			Params: MotionParams{
				Home:   Point{X: 0.2, Y: 0.7},
				Period: 40 * time.Second,
				Route: []Point{
					{X: 0.12, Y: 0.62},
					{X: 0.34, Y: 0.58},
					{X: 0.40, Y: 0.82},
					{X: 0.16, Y: 0.86},
				},
				Phase: 0.25,
			},
		},
		{
			Key:     "67man",
			Label:   "Сиксти Севен Мэн",
			Art:     "npc_67man",
			Pattern: PatternWander,
			// The bottom-right corner, on a shorter cycle than Сахур and half a
			// turn out of phase with him, so the two wanderers never read as the
			// same animation played twice.
			Params: MotionParams{
				Home:   Point{X: 0.72, Y: 0.72},
				Spread: Point{X: 0.22, Y: 0.18},
				Period: 61 * time.Second,
				Phase:  0.5,
			},
		},
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
	c.NPCs = append([]NPC(nil), catalogue.NPCs...)
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
