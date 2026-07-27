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

	// StatBeersDrunk and StatShitsTaken are LIFETIME COUNTERS, and they are
	// ordinary stats with a decay rate of zero.
	//
	// That is the whole trick, and it is the reason this game needs no runs
	// table and no leaderboard schema (ADR-039): a stat is a `(value, as_of)`
	// pair integrated against a rate, so a rate of nought is a number that only
	// ever moves when a verb moves it. They cost no migration, they are seeded
	// for every existing pet by the read path that already seeds a stat the
	// catalogue has gained, and they ride the same wire the bars do.
	//
	// Not `Fatal`, obviously, and `GoodHigh` so a big number reads as an
	// achievement rather than a warning. The third counter, keys_found, arrives
	// with the thing that can increment it.
	StatBeersDrunk = "beers_drunk"
	StatShitsTaken = "shits_taken"
	// StatKeysFound is the third tally, and it arrives with the thing that can
	// increment it.
	StatKeysFound = "keys_found"
)

// Action keys.
const (
	// ActionDrink is the one that helps: it tops him up, cheers him up, and
	// fills his bladder, which is what makes the second verb necessary.
	ActionDrink = "drink"
	// ActionRelieve empties the bladder. It is the other half of the loop
	// drinking creates.
	ActionRelieve = "relieve"
	// ActionRevive is «восстать из мертвых» — and it is the ONLY way back from
	// a death.
	//
	// Beer used to do it, which made dying almost unnoticeable: the verb you
	// were pressing anyway also happened to undo it. Giving death its own verb
	// makes it a thing that happened to you rather than a bar that dipped, and
	// it costs nothing to recover from, which is the balance the reference-game
	// research settled on — mildly annoying and cheaply reversible, never
	// irreversible.
	ActionRevive = "revive"
	// ActionClaim is finding the key — the first CONTESTED verb, where the
	// database rather than the hub decides who won.
	ActionClaim = "claim"
)

// World-object kinds.
const (
	// KindRelief is what a Ваня leaves behind him. Player-created, many live at
	// once, and gone after a while — which is why it is the kind that earns the
	// TTL and NOT the singleton invariant: deposits are never exhausted, so an
	// "at most one active" index keyed on kind alone would have forbidden a
	// second player from relieving himself.
	KindRelief = "relief"
	// KindKey is the lost key exactly one player can find. A SINGLETON kind: at
	// most one is active in the whole world, enforced by a partial unique index
	// rather than by a rule in Go, so two concurrent restarts cannot produce two
	// keys.
	KindKey = "key"
	// KindCrate is the ящик of beer the vendor stands beside — the other
	// singleton, and the first thing in this world that is used up a little at a
	// time rather than all at once. It carries a stock, every drink takes one,
	// and the draw that empties it exhausts the row and puts a fresh crate out in
	// the same transaction.
	//
	// IT IS THE CRATE THAT IS A ROW AND NOT THE VENDOR, and that split is the
	// whole reason a new character stays a Go-file change: everything mutable
	// about the beer store belongs to the thing with a count, and the man
	// standing beside it has nothing about him to store.
	KindCrate = "beer_crate"
)

// ContestKind names how a world object is fought over.
//
// TWO DISCIPLINES, AND THIS FIELD IS WHAT ROUTES BETWEEN THEM — a kind picks
// one instead of the service growing a case per kind. Both are decided by
// PostgreSQL rather than by the hub: each is a conditional UPDATE whose WHERE
// clause is the entire rule, so two players pressing in the same millisecond are
// resolved by the database and a forged client claim cannot beat either.
//
// The field is earned rather than anticipated. While the key was the only
// contested thing, "which verb races for which kind" was one `if` written out in
// world.go, because a table with a single row is a table nobody reads. The beer
// crate is the second, so the `if` became this.
//
// An alias rather than a defined type, exactly as PatternKey is: it is a
// catalogue string, and giving it a type of its own would buy a compile error
// against a typo that a lookup already turns into a plain refusal.
type ContestKind = string

const (
	// ContestNone is a thing nobody races anybody for. A relief deposit belongs
	// to whoever left it and there is nothing about it to win.
	ContestNone ContestKind = ""
	// ContestSingleWinner is all or nothing — the lost key. `UPDATE … WHERE
	// claimed_by IS NULL` hands it to exactly one account and exhausts it in the
	// same statement, and zero rows affected means you lost.
	ContestSingleWinner ContestKind = "single_winner"
	// ContestStock is drawn down one at a time — the crate of beer. `UPDATE …
	// WHERE remaining > 0 RETURNING remaining` cannot oversell however many
	// players press at once: the row is locked for the length of each decrement,
	// so the one behind re-evaluates its own guard against the value the one in
	// front committed rather than against the value it read on the way in.
	ContestStock ContestKind = "stock"
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
	// Counter marks a LIFETIME TALLY rather than a bar: how many beers he has
	// ever drunk, not how much beer is in him.
	//
	// An explicit flag rather than "DecayPerHour is zero", even though the two
	// coincide today. A rate is arithmetic and this is a rendering decision, and
	// the client must be told rather than left to infer meaning from a number —
	// the same rule that keeps every other content key out of the SPA. It is
	// what stops a total being drawn as a bar filling towards a million, and it
	// is why Troubled never fires on one: a tally has no bad end of the scale,
	// so WarnAt is not consulted and is set to Min purely to stay in range.
	Counter bool `json:"counter"`
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
	// RevivesFatal: allowed on, and undoes, a death. Deliberately true of
	// exactly ONE action — a dead Ваня can do nothing else at all, and that is
	// what makes the refusal path real rather than theoretical. See Service.Do.
	RevivesFatal bool `json:"revives_fatal"`
	// StartsOver puts every stat back to its catalogue Start, ignoring Effects.
	//
	// A RESET IS NOT A DELTA, which is why this is a flag and not a clever list
	// of numbers. Coming back from the dead means coming back as a new Ваня —
	// hp 65, beer 60, an empty bladder — and none of those is reachable by
	// adding a fixed amount to whatever he happened to die holding. A delta
	// large enough to clamp gets you the stat's bound (100), not its start.
	//
	// It is deliberately not a third kind of StatDelta ("set to N"): the numbers
	// it would carry are already in the catalogue, one field down, and a second
	// place to write a starting value is a second place for it to be wrong. A
	// counter is exempt — see the loop in apply — because a lifetime total that
	// death reset would not be a lifetime total.
	StartsOver bool `json:"starts_over"`
	// Leaves is the world-object kind this verb puts on the ground where he is
	// standing, or empty for a verb that leaves nothing.
	//
	// A catalogue key rather than a case in the service, so "this verb leaves
	// something behind" is content like every other axis of this game — and so
	// the sentence in the rules cheatsheet can be derived rather than typed.
	Leaves string `json:"leaves,omitempty"`
	// NeedsStat and NeedsAtLeast gate the verb on one of the pet's own numbers:
	// there is no point going to the toilet on an empty bladder.
	//
	// Served so the CLIENT can grey the button out — a control that is visibly
	// unavailable is kinder than one that looks ready and then refuses — but the
	// SERVER enforces it regardless, because a greyed button is a suggestion and
	// anything a client can decline to honour is not a rule. Empty means the
	// verb is always available.
	NeedsStat    string  `json:"needs_stat,omitempty"`
	NeedsAtLeast float64 `json:"needs_at_least,omitempty"`
	// NeedsNear gates the verb on WHERE HE IS STANDING rather than on what he
	// holds: beer comes out of the crate, so you have to walk to the crate.
	//
	// The first rule in this game about position, and the one ADR-043 reserved a
	// place for. It is checked in Service.Do against the yard's own in-memory
	// placement at the instant the batch is folded, and NEVER inside `apply` —
	// which has to stay a pure function of (Snapshot, Event) or a replay would
	// need to know where everybody was standing last March, and would have to ask
	// a database to find out.
	//
	// A world-object KIND rather than a coordinate, so the store can be moved by
	// editing where it stands and nothing else. Served for the same reason
	// NeedsStat is, and the client resolves the kind against `object_kinds`
	// exactly as it already resolves `leaves`.
	NeedsNear string `json:"needs_near,omitempty"`
	// Contests is the world-object kind this verb races other players for, or
	// empty for a verb nobody can take from you.
	//
	// A catalogue key rather than a case in the service, exactly as Leaves is:
	// THE VERB NAMES THE KIND AND THE KIND NAMES THE DISCIPLINE, so the service
	// routes on ObjectKind.Contest alone and a second contested verb is two
	// catalogue entries and no code. This replaced an `if verb == ActionClaim`
	// written out in world.go, which was the right shape while the key was the
	// only contested thing in the world and stopped being it the moment the crate
	// arrived.
	Contests string `json:"contests,omitempty"`
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

// ObjectKind is a kind of durable thing that can stand on the plane.
//
// The catalogue half of `game_vanyagotchi_world_objects.kind`: the row carries
// the key and the invariants, this carries what the key MEANS. Adding a kind is
// therefore an entry here — no migration, and no client deploy either, because
// the browser resolves `Art` against the config exactly as it does for a pet or
// an NPC and never learns that object kinds exist.
type ObjectKind struct {
	Key string `json:"key"`
	// Art is a catalogue art key, resolved client-side like any other.
	Art string `json:"art"`
	// Label is what it is called. Empty for a thing nobody needs named — a
	// deposit on the ground is self-explanatory and a caption would only be one
	// more thing to draw.
	Label string `json:"label,omitempty"`
	// Lifetime is how long one lasts. Zero means forever.
	//
	// Filtered on READ rather than swept, exactly as `sessions.expires_at`
	// already is: rows accumulate and are ignored, because a sweeper would be
	// the background timer this design does not have.
	Lifetime time.Duration `json:"-"`
	// LifetimeSeconds is Lifetime on the wire, because a Go duration marshals as
	// a nanosecond count that no client should have to know how to read.
	LifetimeSeconds float64 `json:"lifetime_seconds"`
	// Singleton: at most one of this kind may be active in the whole world.
	//
	// Written into the row at insert time so the partial unique index can
	// express the invariant WITHOUT naming a kind in DDL — which is what keeps
	// "a new singleton kind" a catalogue edit rather than a migration. It is also
	// what `ensureWorld` iterates: a singleton kind is one the yard always holds
	// exactly one of, so a hello puts a missing one back.
	Singleton bool `json:"-"`
	// Contest is how this kind is fought over, and it is what routes a verb to a
	// discipline instead of the service switching on a kind key. Empty for a
	// thing nobody races anybody for.
	//
	// Server-side only. The player is told the RULE — that the crate runs out —
	// through Stock below and through the store block on the frame; the name of
	// the SQL discipline that enforces it is nobody's business but this package's,
	// and publishing it would invite a second implementation in TypeScript, the
	// same reason an NPC's motion pattern is withheld.
	Contest ContestKind `json:"-"`
	// Stock is how many draws a freshly spawned one of these carries: the row's
	// `remaining` starts here, and the draw that takes it to nought exhausts the
	// row and spawns its replacement in the same transaction. Nought for a kind
	// that is not drawn down.
	//
	// Served, unlike the two fields either side of it, because it is a NUMBER THE
	// PLAYER PLAYS AGAINST rather than a mechanism: «в ящике шесть» is a rule of
	// the game, and the splash cheatsheet derives that sentence from here instead
	// of somebody typing the six out and forgetting it after the next retune.
	Stock int `json:"stock,omitempty"`
	// At is where one of these stands, for a kind that has a pitch — nil for a
	// kind that is hidden somewhere instead.
	//
	// That nil IS the difference between a shop and a lost key, and it is the
	// whole of it: `placeFor` reads this field and draws a random hiding place
	// when there is none, so "does this kind stand somewhere in particular"
	// stopped being a case in the service the moment there were two answers.
	//
	// Server-side only, and deliberately: an object arrives in the roster with
	// its own coordinates like every other entity, and the one client that needs
	// to know where the STORE is gets told by the frame (see Roster.Store), not
	// by matching a kind key it is not supposed to hold.
	At *Point `json:"-"`
}

// stock is the `remaining` a freshly spawned object of this kind is inserted
// with — nil for a kind nobody draws down, which is what the column holds for
// every key and every deposit.
//
// Keyed on the DISCIPLINE rather than on Stock being non-zero, for the reason
// Counter is keyed on its own flag: a stocked kind retuned to hold one is still
// a stocked kind, and inferring the mechanism from the number is exactly the
// mistake this package refuses everywhere else.
func (k ObjectKind) stock() *int {
	if k.Contest != ContestStock {
		return nil
	}
	n := k.Stock
	return &n
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
	// ObjectKinds is what a durable thing standing on the plane can be. Served
	// so the client can resolve an object's art the same way it resolves a pet's
	// — it holds no kind key of its own.
	ObjectKinds []ObjectKind `json:"object_kinds"`
	// ArriveWithin is how close "beside it" is, in plane widths — the one number
	// an Action.NeedsNear gate turns on.
	//
	// Served so that the client greying a button and the server refusing a verb
	// are the SAME threshold rather than two numbers that drift apart, which is
	// the identical reason a stat's WarnAt is content rather than a stylesheet
	// value. The client has both ends of the measurement already: its own entity,
	// from the hello, and the store's place, from the frame.
	ArriveWithin float64 `json:"arrive_within"`
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

	// The ceiling on a lifetime counter. Not a game rule — a counter has no
	// natural maximum — but every stat is clamped against one, and a bound this
	// far away is unreachable by a friend group pressing a button once a second
	// for a lifetime. It exists so the clamp has something to clamp to.
	counterMax = 1_000_000.0

	// How long a deposit stays on the ground.
	//
	// Long enough that somebody else walks past it and short enough that the
	// yard is not a sewer by evening. It also BOUNDS THE WIRE: every live
	// deposit is an entity in a frame sent five times a second to everybody, so
	// the lifetime is what stops the roster growing without limit — see
	// worldLimit for the hard cap behind it.
	reliefLifetime = 10 * time.Minute

	// How full he has to be before going to the toilet is worth a button press.
	// Low enough that it is rarely in the way, high enough that «покакать» stops
	// being a thing you press absently while nothing is happening.
	relieveNeedsBladder = 15.0

	// What the verbs do.
	drinkBeer    = 40.0
	drinkHP      = 15.0
	drinkBladder = 25.0

	// How much beer a crate holds before the vendor has to fetch another.
	//
	// Six is a round for everybody who is plausibly in the yard at once, and it
	// is deliberately small enough that the number on screen VISIBLY falls — a
	// stock of fifty would be a finite resource nobody ever saw run out, which is
	// the same as no stock at all. The crate is replaced the instant it empties,
	// so this is pacing rather than scarcity: what it buys is somebody arriving
	// to «пиво кончилось» and having to wait a moment, not a yard that runs dry.
	crateStock = 6

	// How close to a thing you have to be standing for a verb gated on it.
	//
	// A little under an entity's own width — the plane draws each dot at about
	// 0.13 plane-widths — so "beside it" means overlapping it on screen, which is
	// the only reading of the rule a player can check by eye. Larger and the walk
	// stops mattering, which is the entire point of the gate; smaller and
	// arriving becomes a pixel-hunt on a phone.
	arriveWithin = 0.12
)

// Where the beer store stands, and where its vendor stands beside it.
//
// THE CRATE IS DELIBERATELY INSIDE tiredFrom OF THE ENTRANCE. It is about 0.43
// plane-widths from `spawn`, and a walk that short is never refused by the
// tiredness roll — so somebody who has just arrived can always reach the beer in
// one tap, and no Ваня is ever stuck between the door and a drink. Moving either
// of these numbers without checking that distance against tiredFrom is how the
// store quietly becomes unreachable for whoever is unlucky.
//
// The vendor is a little to its left: close enough to read as the man selling
// it, far enough that the two dots do not sit on top of each other.
var (
	cratePlace  = Point{X: 0.82, Y: 0.22}
	vendorPlace = Point{X: 0.68, Y: 0.26}
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
		// The lifetime counters. Rate nought, so nothing but a verb ever moves
		// them, and `WarnAt` below the floor so no total is ever drawn as
		// trouble — a big number here is the opposite of a warning.
		{
			Key:          StatBeersDrunk,
			Label:        "выпито пива",
			Emoji:        "🍻",
			Min:          0,
			Max:          counterMax,
			Start:        0,
			DecayPerHour: 0,
			GoodHigh:     true,
			WarnAt:       0,
			Counter:      true,
			Fatal:        false,
		},
		{
			Key:          StatKeysFound,
			Label:        "найдено ключей",
			Emoji:        "🔑",
			Min:          0,
			Max:          counterMax,
			Start:        0,
			DecayPerHour: 0,
			GoodHigh:     true,
			WarnAt:       0,
			Counter:      true,
			Fatal:        false,
		},
		{
			Key:          StatShitsTaken,
			Label:        "покакано раз",
			Emoji:        "🧻",
			Min:          0,
			Max:          counterMax,
			Start:        0,
			DecayPerHour: 0,
			GoodHigh:     true,
			WarnAt:       0,
			Counter:      true,
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
				// The lifetime tally, moved by the same mechanism as everything
				// else: a counter is a stat whose rate is zero, so counting is
				// an effect and needs no code of its own.
				{StatKey: StatBeersDrunk, Delta: 1},
			},
			Done: "хорошо пошло",
			// NO LONGER THE WAY BACK FROM A DEATH. It was, and that made dying
			// almost invisible — the verb you were pressing anyway quietly
			// undid it, so the one moment the game is about had no moment. A
			// death now has its own verb, and beer is just beer.
			RevivesFatal: false,
			// AND BEER NOW HAS TO COME FROM SOMEWHERE. Two preconditions, and
			// they refuse for different reasons on purpose: you can be at the
			// crate and find it empty, or hold the whole yard's beer at arm's
			// length and be too far to reach it. Telling the player which is the
			// difference between walking over and waiting.
			NeedsNear: KindCrate,
			Contests:  KindCrate,
		},
		{
			Key:   ActionRelieve,
			Label: "покакать",
			Emoji: "💩",
			// A delta larger than the whole scale, so the clamp lands it exactly
			// on the floor. "Reset" needs no mechanism of its own.
			Effects: []StatDelta{
				{StatKey: StatBladder, Delta: -statMax},
				{StatKey: StatShitsTaken, Delta: 1},
			},
			Done: "полегчало",
			// And it stays where he left it, for everybody to walk past.
			Leaves: KindRelief,
			// Nothing to do on an empty bladder.
			NeedsStat:    StatBladder,
			NeedsAtLeast: relieveNeedsBladder,
			// A dead Ваня does not go to the toilet.
			RevivesFatal: false,
		},
		{
			Key:   ActionClaim,
			Label: "искать ключи",
			Emoji: "🔑",
			// The tally is the only thing that moves, and only for the winner:
			// LOSING COSTS NOTHING. That is the settled ruling and it is what
			// makes another player turning up never bad news — the loser's Ваня
			// simply looks sad for a moment.
			//
			// It works out that way for free rather than by a special case: a
			// claim that loses the race returns ErrClaimLost, the whole batch is
			// refused, and a refused batch writes nothing at all.
			Effects: []StatDelta{{StatKey: StatKeysFound, Delta: 1}},
			Done:    "нашёл ключи",
			// A dead Ваня finds nothing.
			RevivesFatal: false,
			// The kind it races for. Read off the catalogue rather than switched
			// on in the service, which is what let the second contested verb
			// arrive without a second `if`.
			//
			// No NeedsNear: the keys are LOST, so looking for them is something
			// you do from wherever you are standing. Being able to search the
			// whole yard without crossing it is what keeps the hunt a race
			// between people rather than a race against the walk.
			Contests: KindKey,
		},
		{
			Key:   ActionRevive,
			Label: "восстать из мертвых",
			Emoji: "🧟",
			// NO EFFECTS, because StartsOver ignores them. Listing deltas here
			// would be two descriptions of the same outcome, and the one that
			// did nothing would be the one somebody edited.
			Done: "воскрес",
			// The only action that carries this, which is the point of the
			// verb. Every other verb is refused on a corpse, so death is a
			// state you have to deliberately leave rather than one you fall out
			// of by drinking.
			RevivesFatal: true,
			StartsOver:   true,
		},
	},
	Skins: []Skin{
		{
			Key:      SkinVanya,
			Label:    "дядя Ваня",
			Emoji:    "🫃",
			Gradient: "linear-gradient(160deg, #6b4a2f, #2f4a6b)",
		},
		// A WORLD OBJECT'S ART LIVES HERE TOO, for exactly the reason the NPCs'
		// does: the client resolves whatever art key an entity carries against
		// this one list and has no idea which entities are people. Missing it is
		// not an error, but it is worse than it sounds — the placeholder is a
		// PERSON-shaped silhouette, so a deposit with no entry would draw as
		// somebody standing there rather than as something lying on the ground.
		{
			Key:      "obj_key",
			Label:    "ключи",
			Emoji:    "🔑",
			Gradient: "linear-gradient(160deg, #7a6a2f, #3a3320)",
		},
		{
			Key:   "obj_crate",
			Label: "ящик пива",
			Emoji: "📦",
			// A crate rather than a glass: 🍺 is already the drink verb and 🍻 the
			// tally, and three beer glyphs meaning three different things on one
			// screen is how a player stops reading any of them.
			Gradient: "linear-gradient(160deg, #6b5a2f, #35301c)",
		},
		{
			Key:   "obj_relief",
			Label: "куча",
			Emoji: "💩",
			// Muddier and flatter than a person's, so it reads as ground rather
			// than as somebody in a coat.
			Gradient: "linear-gradient(160deg, #5a4632, #3a2f22)",
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
		{
			Key:      "npc_vendor",
			Label:    "продавец пива",
			Emoji:    "🧔",
			Gradient: "linear-gradient(160deg, #4a6b3a, #21301a)",
		},
	},
	ObjectKinds: []ObjectKind{
		{
			Key: KindRelief,
			Art: "obj_relief",
			// Deliberately unnamed: a caption over it would be one more thing on
			// a small screen, and nobody needs telling what it is.
			Lifetime:        reliefLifetime,
			LifetimeSeconds: reliefLifetime.Seconds(),
			// NOT a singleton. Many are live at once and none is ever exhausted,
			// which is precisely why migration 008's index is predicated on this
			// flag rather than on `exhausted_at IS NULL` alone.
			Singleton: false,
			// And nobody races anybody for one. Stated rather than left to the
			// zero value, because this field is what routes a verb to a
			// discipline and "no discipline" is an answer rather than an
			// oversight.
			Contest: ContestNone,
		},
		{
			Key:   KindKey,
			Art:   "obj_key",
			Label: "ключи",
			// Forever, until somebody claims it. A hunt that timed out would
			// need a timer, and there is none — it ends when it is won.
			Lifetime:        0,
			LifetimeSeconds: 0,
			// A SINGLETON. One active key in the world, as a database invariant:
			// the winning claim sets `exhausted_at` and inserts the replacement
			// in the same statement, so the next frame already carries a fresh
			// hunt and two racing restarts cannot make two.
			Singleton: true,
			// All or nothing. One player gets it and everybody else gets a face.
			Contest: ContestSingleWinner,
			// No At: the whole point of a key is that it is somewhere, and
			// `placeFor` draws a hiding place for a kind that names no pitch.
		},
		{
			Key:   KindCrate,
			Art:   "obj_crate",
			Label: "ящик пива",
			// Forever, like the key: a crate ends when it is empty, not when it
			// times out, and a timeout would need the timer this design has not
			// got.
			Lifetime:        0,
			LifetimeSeconds: 0,
			// The other singleton, so the yard holds exactly one beer store and
			// two racing restarts cannot stand up two.
			Singleton: true,
			// Drawn down a bottle at a time rather than won outright, which is
			// the second discipline and the reason the Contest field exists at
			// all.
			Contest: ContestStock,
			Stock:   crateStock,
			// And unlike the key it STANDS SOMEWHERE, which is what makes the
			// arrival gate mean anything: a shop you had to hunt for would be a
			// second key hunt wearing an apron.
			At: &cratePlace,
		},
	},

	Locations: []Location{
		{Key: LocationYard, Label: "двор", Entry: spawn},
	},
	// The yard's regulars. Four of them across three ways of moving, which is
	// exactly what the pattern table exists for: the last two arrived as
	// catalogue entries and nothing else — no code, no migration, no client
	// change — because each reuses a pattern that was already written.
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
		{
			Key:   "vendor",
			Label: "продавец пива",
			Art:   "npc_vendor",
			// THE FIRST IDLER, and the pattern that has been sitting unused in
			// the table since it was written waiting for exactly this character.
			// He does not move, because a shop that wandered off would make the
			// walk to it a matter of luck.
			Pattern: PatternIdle,
			// STATELESS, AND THAT IS THE WHOLE POINT OF HIM. Everything mutable
			// about the beer store — how much is left, when it was emptied, who
			// took the last one — belongs to the crate beside him, which is a row.
			// He is catalogue and arithmetic: no row, no account, no migration,
			// and adding the next character like him costs one entry here.
			Params: MotionParams{Home: vendorPlace},
		},
	},
	ArriveWithin:    arriveWithin,
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
	c.ObjectKinds = append([]ObjectKind(nil), catalogue.ObjectKinds...)
	// The one POINTER the catalogue hands out, re-pointed rather than shared.
	// Copying a slice of structs copies the pointer inside each one, so a caller
	// that wrote through `At` would move the beer store for every later request
	// in the process — the same failure the note above describes for a decorated
	// skin, one indirection further down and correspondingly harder to see.
	for i, k := range c.ObjectKinds {
		if k.At == nil {
			continue
		}
		at := *k.At
		c.ObjectKinds[i].At = &at
	}
	return c
}

// ObjectKindByKey looks a world-object kind up in the catalogue.
//
// A row whose kind has left the catalogue is unrenderable and is skipped on
// read — the correct failure for a value only content can define, and the same
// rule a stored stat key the catalogue no longer defines already follows.
func ObjectKindByKey(key string) (ObjectKind, bool) {
	for _, k := range catalogue.ObjectKinds {
		if k.Key == key {
			return k, true
		}
	}
	return ObjectKind{}, false
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
