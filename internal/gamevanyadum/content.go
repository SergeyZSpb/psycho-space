package gamevanyadum

import "time"

// The catalogue. Every tuning constant, every pickup, every surface and every
// generation parameter lives here and nowhere else, and the whole of it is
// served to the browser at GET /api/game-vanyadum/config.
//
// WHY IT IS A GO CATALOGUE RATHER THAN ROWS. The same reasoning as ADR-039: a
// new pickup or a new surface is an entry here, a backend deploy, and no
// migration — where a Postgres enum would make each one an ALTER TYPE, i.e. a
// permanent migration, for a joke game whose realistic future is deletion.
//
// WHY IT IS SERVED. Everything the client knows about this game's content, it
// was told. The renderer iterates surfaces generically, the HUD iterates
// pickups generically, and the splash screen's rules cheatsheet is BUILT from
// what is below rather than typed out beside it — so retuning a number here
// changes what the player is told, with no client change and no chance of the
// two drifting apart. Only what cannot be derived (which finger goes where) is
// prose on the client, and it is marked there as the part a rules change must
// come back and edit.

// Movement. Metres, seconds, radians. These are the numbers that decide how the
// game feels, and they are the first things to reach for when it feels wrong.
const (
	// PlayerRadius is the disc the player occupies on the floor plane. A
	// doorway must be comfortably wider than twice this or nobody fits through
	// it; DoorWidth below is checked against it by an invariant test.
	PlayerRadius = 0.35

	// EyeHeight is where the camera sits above the floor of the sector the
	// player is standing in.
	EyeHeight = 1.65

	// BodyHeight is how tall a man is for the purpose of being shot: the hit
	// test stands a cylinder of PlayerRadius on the floor of his sector and
	// gives it this height (hit.go).
	//
	// THE SAME DISC THE COLLISION RESOLVER USES, deliberately, because a body
	// that is fatter to shoot at than it is to walk with is a body you can hit
	// through a doorway you could not fit through. One radius, one meaning.
	//
	// Taller than EyeHeight so that a man's head is above his own eyes — aim
	// level at somebody the same height as you and the shot lands, which is the
	// only property of this number anybody will ever notice.
	BodyHeight = 1.8

	// WalkSpeed is metres per second on the flat. Doom ran about twice this and
	// Quake faster still; this is deliberately nearer a fast walk, because the
	// levels are small and a phone screen in portrait shows very little of them.
	WalkSpeed = 5.0

	// MaxStep is the tallest rise the player walks up without noticing. It is
	// what makes stairs work without a jump, and in this iteration the level
	// generator clamps every floor change to it so that everything generated is
	// reachable — a drop you cannot climb back out of arrives with the lifts.
	MaxStep = 0.6

	// MaxPitch clamps looking up and down, just short of straight up. Beyond it
	// the view matrix degenerates and the horizon rolls.
	MaxPitch = 1.5
)

// Health, and what a barrel takes off it.
const (
	MaxHealth   = 100
	StartHealth = 100

	// BarrelDamage is what one barrel does to whoever it lands on.
	//
	// A FULL GUN IS EXACTLY ONE KILL, which is the whole of why this number is
	// half of MaxHealth rather than a third or a fifth. The обрез holds Barrels
	// and takes ReloadSeconds to fill, so a fight is decided by whether the two
	// shots you already have land — not by who walked over more bottles. It also
	// makes the arithmetic something a player works out in one go and never
	// thinks about again: two in the chest, наглухо.
	//
	// There is no falloff and no spread. A single hitscan ray is the simplest
	// thing that satisfies "the shot went where you aimed it" (hit.go), and a
	// range at which the gun stops working is a rule nobody has asked for —
	// interest management already stops a shot at the rooms you can see into,
	// which is a shorter leash than any number typed here would be.
	BarrelDamage = 50
)

// DownTime is how long you lie on the floor before the заброшка gives you back.
//
// THREE SECONDS IS LONG ENOUGH TO BE A PUNISHMENT AND SHORT ENOUGH TO BE A JOKE.
// Nothing here ends, so a death costs exactly this and the walk back — there is
// no round to sit out and no score to lose, because the only score is the one
// this iteration adds and it belongs to whoever shot you. Longer and a phone
// screen shows a corpse for the length of a loading screen; shorter and the man
// who killed you is still standing there when you get up, which is what the
// spawn protection below is for.
//
// A DEATH COSTS NOTHING ELSE, and that is a decision rather than an omission.
// The bag survives, so the beer somebody toured the building for is not taken
// off him by one unlucky corner — losing it would make dying cost a second walk
// as well as the three seconds, and would compound whoever is already winning.
const DownTime = 3 * time.Second

// downTicks is that interval as a whole number of simulation steps, which is the
// unit World.downUntil is expressed in. Derived rather than typed out, so the two
// can never disagree.
const downTicks = int64(DownTime / SimStep)

// SpawnProtectSeconds is how long a man standing on the spawn cannot be hurt AND
// cannot shoot.
//
// BOTH HALVES, OR IT IS A WEAPON. One заброшка, one spawn point and killable
// friends make standing on the spawn with a loaded обрез the obvious grief, and
// this is the two-line rule that removes it. Protection you can fire from does
// not remove it — it hands it to whoever died last, which is worse than the
// thing it was fixing.
//
// EVERYBODY WHO APPEARS AT THE SPAWN GETS IT, which is a man who has just got up
// (world.go, rise) and equally a man who has just walked in (world.go, Join).
// The argument does not distinguish them: both materialise at the one place
// everybody in the building knows about, and friendly fire is on. A newcomer who
// arrived unprotected would be the easier of the two to camp, because he is the
// one whose browser is still loading the building.
//
// TWO SECONDS IS TEN METRES AT WalkSpeed, which is across a room and out of the
// doorway: enough to leave, not enough to cross the building and arrive
// somewhere untouchable. It is a float64 of seconds ON THE PLAYER because the
// browser counts it down too — the client has to run the same refusal to know
// whether to draw a muzzle flash, exactly as it does for the gun's own timers
// (sim.go) — and a tick deadline in the WORLD because seconds counted down by a
// client's own commands are seconds that client controls (world.go, protect).
const SpawnProtectSeconds = 2.0

// protectTicks is that window as a whole number of simulation steps, which is
// the unit World.protectedUntil is expressed in. Derived rather than typed out,
// so the two can never disagree — the same relationship downTicks has to
// DownTime, and stated in this shape because SpawnProtectSeconds is seconds
// where DownTime is a duration.
const protectTicks = int64(SpawnProtectSeconds * float64(time.Second) / float64(SimStep))

// BetrayalsTitle is what the standings column is called, in Russian.
//
// Friendly fire is on and the нейрослопы have not arrived, so every kill in this
// building is a friend's — the counter is not a score, it is a confession. It is
// published in the catalogue rather than typed into the client for the reason
// every other word here is: the splash screen's rules cheatsheet is generated
// from what is served, so this is the one place the joke is written down.
const BetrayalsTitle = "предательства"

// The gun: a double-barrelled обрез, and пиво is what goes in it. The bottles
// the building has been scattering since the first iteration finally spend on
// something, which is what makes walking to one worth the walk.
//
// SECONDS AS A FLOAT64 COUNTDOWN, AND NOT A TICK DEADLINE — the opposite of the
// choice made for the respawn below, so the difference is worth stating rather
// than looking like an inconsistency. Both are countdowns; they belong to
// different things. A respawn is the WORLD's, the world owns the tick, and an
// integer tick is exact where subtracting dt from a float accumulates the error
// of SimStep's binary expansion. The gun is the PLAYER's, and a player is
// advanced by Step — which is pure, reads no clock and knows no tick, because
// the browser runs it too (the golden vectors). A deadline would be a tick
// number the client does not have and cannot compute, so the only
// representation both ends can agree on is seconds counted down by the
// command's own dt.
//
// That costs less than it sounds. The two ends fold in the SAME commands in the
// same order — whatever is predicted is exactly what is sent — and both run
// IEEE754 binary64, so the accumulated value is not approximately equal on the
// two sides, it is bit-for-bit equal. What drift there is, both ends have.
const (
	// Barrels is how many shots a full gun holds.
	Barrels = 2

	// FireCooldownSeconds is how long the gun is busy after a shot. It is the
	// gap between the two barrels, so it wants to be short enough that a double
	// tap feels like one decision and long enough that a thumb mashing glass
	// cannot empty the gun by accident.
	FireCooldownSeconds = 0.35

	// ReloadSeconds is how long a reload takes, and it is the game's only real
	// punishment for missing: a second and a half standing in a заброшка with
	// nothing in your hands. Doom's super shotgun is the reference and it is the
	// right one — the reload is the weapon's whole character.
	ReloadSeconds = 1.5

	// ReloadCost is how much of AmmoCounter one reload spends. A single bottle
	// fills both barrels, so beer is counted in shots-times-Barrels and the cap
	// on the counter (PickupKind.Max) is what bounds how much anybody can hold.
	ReloadCost = 1
)

// AmmoCounter is the counter a reload spends.
//
// It is a Pickups entry's Grants value and not a name the simulation invented —
// TestTheAmmunitionIsSomethingTheBuildingActuallyScatters is what keeps the two
// pointing at each other, because a gun that spends a counter nothing grants is
// a gun that can never be reloaded and nothing else would say so.
const AmmoCounter = "beer"

// PickupKind is a thing lying on the floor that is used by walking over it.
//
// There is no use button, by design: it is the worst control on a touchscreen,
// and walking into your medicine is funnier.
type PickupKind struct {
	Key string `json:"key"`
	// Title is what the cheatsheet calls it, in Russian.
	Title string `json:"title"`
	// Icon is the HUD glyph. One emoji, because the HUD is real text.
	Icon string `json:"icon"`
	// Grants names the counter it fills and Amount is how much of it. Both are
	// read generically by the HUD, so a second pickup needs no client change.
	Grants string `json:"grants"`
	Amount int    `json:"amount"`
	// Max is the ceiling for that counter; zero means uncapped.
	Max int `json:"max"`
	// Tint is the colour the procedural mesh is built in. There is no art file
	// anywhere in this game — see the package doc.
	Tint string `json:"tint"`
	// Blurb is one line of Russian for the rules cheatsheet. This is the one
	// field on a pickup that a human wrote, because "what it is for" is a joke
	// rather than a number.
	//
	// AND IT MUST STAY FREE OF NUMBERS. The cheatsheet derives the gun's rules
	// from the served catalogue two lines above this one (vanyadumRules.ts), so
	// a blurb that spelled a constant out in prose would go stale the first time
	// anybody retuned it — and would do so on the same screen as the derived
	// line that still had it right. Say what the thing is FOR; the numbers are
	// generated.
	Blurb string `json:"blurb"`
}

// PickupRespawn is how long a collected thing takes to come back, on the spot it
// was taken from.
//
// A TUNING CONSTANT, and the first one to reach for once anybody has actually
// played this. The match is infinite and the generator scatters two or three
// bottles through nine-odd rooms, so the two failure modes are close together:
// much faster and the заброшка is a vending machine — stand on the spot, wait,
// drink, repeat, and there is no reason to walk anywhere; much slower and an
// infinite match runs dry, leaving a building with nothing in it to find. Thirty
// seconds is roughly the time it takes to cross the place and come back, which
// makes the loop "tour the building" rather than "camp the bottle".
//
// IT COMES BACK WHERE IT WAS, which is what makes it free on the wire: the
// client was sent the level once and the remaining-pickup mask is indexed into
// it, so a return is one bit changing rather than a position being re-sent.
// Camping IS worth thinking about now that the gun drinks the beer: a bottle is
// ammunition rather than a trophy, so a player who stands on one spot waiting
// for a respawn is farming shots. Thirty seconds against a reload that returns
// Barrels of them is what keeps that a poor trade compared with touring the
// building — and this is the constant to move if it ever stops being one.
const PickupRespawn = 30 * time.Second

// pickupRespawnTicks is that interval as a whole number of simulation steps,
// which is the unit World.ready is expressed in. Derived rather than typed out,
// so the two can never disagree.
const pickupRespawnTicks = int64(PickupRespawn / SimStep)

// MaxOccupants is how many people may be in the заброшка at once.
//
// FOUR IS A MEASURED LIMIT AND NOT A PREFERENCE. What a viewer is sent is two
// things: a snapshot built for them twenty times a second which carries
// everybody else, and the standings frame once a second which carries everybody
// including them. The ceiling is 8 kB/s PER VIEWER — the number this game's
// design named as the point at which interest management stops being optional —
// and the capacity is simply the largest N whose total fits under it.
//
// THE MEASUREMENT, at the widest quantisation the wire can carry (yaw is
// wrapped rather than normalised, so it reaches five characters; positions are
// far beyond anything a generated level produces):
//
//	solo snapshot             180 bytes
//	the first peer            +63  (the entry, plus the `p` array around it)
//	each further peer         +57
//	standings with one row    104
//	each further row          +76
//
// So a viewer pays 20 × (180 + 63 + (N−2)×57) + (104 + (N−1)×76) a second:
//
//	N = 3    300 × 20 + 256 = 6256 B/s
//	N = 4    357 × 20 + 332 = 7472 B/s
//	N = 5    414 × 20 + 408 = 8688 B/s — over
//
// Four, with 528 B/s of headroom. Pinned by
// TestEverythingAFullBuildingSendsAViewerFitsTheCeiling, which fails when this
// constant is raised or when either frame grows a field.
//
// IT WAS FIVE, AND SHOOTING PEOPLE IS WHAT COST THE FIFTH PLACE. That is the
// trade the previous version of this comment named in advance — "the next field
// of that size is the one that costs a place, and the answer then is a smaller
// building or the binary codec, not a larger ceiling" — and this is it being
// paid rather than argued out of. Where it went:
//
//   - THE PEER STATE, +7 bytes on EVERY peer on EVERY tick (message.go,
//     Peer.St). That is 560 B/s at the old capacity, and it is the shape the
//     muzzle flash's own comment measured at 640 B/s and refused. It is bought
//     now because two of its four values are DURATIONS — a man is down for
//     DownTime and protected for SpawnProtectSeconds — and neither can be
//     derived from anything the frame already carries, because being shot moves
//     nobody. There is no honest duty cycle to discount by: a player killed the
//     instant his protection expires is flagged essentially always. The flag it
//     replaced was 9 bytes at three a second, so this is a 7-byte field costing
//     seven times what a 9-byte one did — the rate is everything.
//   - THE TWO SELF TIMERS, `dn` and `pr`, +20 bytes on the solo frame at the
//     pessimism this budget is measured with (they cannot both be set, and
//     neither can be set beside the gun's two).
//   - THE TWO STANDINGS COUNTERS, +23 bytes a row at six figures each, on a
//     frame that goes out once a second.
//
// THE STANDINGS FRAME IS INSIDE THE CEILING AND NOT BESIDE IT. It is traffic to
// the same viewer, so leaving it out would be moving the line rather than
// meeting it — which is how this constant came to be six before anybody measured
// a peer.
//
// INTEREST MANAGEMENT BOUGHT NONE OF IT, which is worth stating plainly rather
// than letting the two changes be credited together. Filtering peers to the
// viewer's own sector and the rooms through its doorways (level.go,
// buildVisibility) makes the TYPICAL frame much smaller, which is what a phone
// on mobile data actually experiences and is why it is worth having. But the
// budget is taken on the WORST case, and the worst case is everybody standing in
// one room — where the filter removes nothing at all. A capacity derived from
// the typical frame would be a capacity that fails the first time four people
// crowd into one doorway. The same argument covers the hold that keeps a peer on
// the frame for a moment after he leaves the set (visibleHold): it can only ADD
// to a filtered set, and the unfiltered set is what is budgeted here.
//
// GETTING THE PLACE BACK NEEDS THE BINARY CODEC. There is no further byte worth
// finding in JSON: a peer is six integers behind keys of one to three
// characters, and what is left of the entry is punctuation. The design doc
// earmarked a binary codec as an iteration of its own, and that — rather than
// another round of trimming — is what the fifth place now costs.
//
// A refusal is a refusal and never a queue: somebody who arrives at a full
// заброшка is told so (the `vanyadum_full` frame) rather than being put on hold,
// because there is nothing to hold them for — nothing here ends.
const MaxOccupants = 4

// Pickups is every kind that can be generated into a level.
//
// Iteration 1 has exactly one. The syringe and the keys are designed (see the
// living doc) and deliberately absent: a walking skeleton proves the loop with
// the simplest possible member of the family, and the second entry is then a
// catalogue line rather than a feature.
var Pickups = []PickupKind{
	{
		Key:    "beer",
		Title:  "пиво",
		Icon:   "🍺",
		Grants: "beer",
		Amount: 1,
		Max:    9,
		Tint:   "#c8892f",
		Blurb:  "Патроны для обреза. Стрелять на трезвую голову тут не принято.",
	},
}

// PickupByKey finds a kind, reporting whether it exists. Callers treat a miss as
// a programming error rather than as input: nothing outside this package names
// a pickup.
func PickupByKey(key string) (PickupKind, bool) {
	for _, p := range Pickups {
		if p.Key == key {
			return p, true
		}
	}
	return PickupKind{}, false
}

// Surface is how a wall, floor or ceiling is textured. The client generates the
// texture from these numbers — there is no image file, and these few bytes are
// the whole of what crosses the network about how the world looks.
type Surface struct {
	Key string `json:"key"`
	// Base is the hex colour the noise is applied over.
	Base string `json:"base"`
	// Accent is the colour of the grain, the mortar or the stain.
	Accent string `json:"accent"`
	// Noise is how much grain, 0..1. Roughness is the size of its features in
	// metres, which is what keeps a texture the same physical scale on a wall
	// three metres tall and on one thirty metres long.
	Noise     float64 `json:"noise"`
	Roughness float64 `json:"roughness"`
	// Pattern selects the generator: "concrete", "brick" or "boards".
	Pattern string `json:"pattern"`
}

// Surfaces is the whole palette of the заброшка: wet grey concrete, the brick
// underneath where the plaster has gone, and boarded windows.
var Surfaces = []Surface{
	{Key: "concrete", Base: "#5b5f5e", Accent: "#3f4443", Noise: 0.55, Roughness: 0.35, Pattern: "concrete"},
	{Key: "brick", Base: "#6d4634", Accent: "#4a2f22", Noise: 0.4, Roughness: 0.25, Pattern: "brick"},
	{Key: "boards", Base: "#6b563a", Accent: "#42341f", Noise: 0.6, Roughness: 0.2, Pattern: "boards"},
	{Key: "floor", Base: "#4a4d4b", Accent: "#2e3130", Noise: 0.7, Roughness: 0.5, Pattern: "concrete"},
	{Key: "ceiling", Base: "#3a3d3c", Accent: "#242626", Noise: 0.35, Roughness: 0.6, Pattern: "concrete"},
}

// Level generation. A building's whole geometry is a function of these numbers
// and a seed, so a level is reproducible from the two and is never stored.
const (
	// RoomsMin and RoomsMax bound how many rooms a floor has.
	RoomsMin = 7
	RoomsMax = 11

	// RoomMin and RoomMax bound a room's footprint in metres, per axis.
	RoomMin = 5.0
	RoomMax = 12.0

	// CeilingHeight is the gap between a sector's floor and its ceiling.
	CeilingHeight = 3.2

	// DoorWidth is the opening in a shared wall. An invariant test asserts it
	// leaves room for the player on both sides of his own radius.
	DoorWidth = 2.0

	// MinSharedWall is how much boundary two rooms must have in common before a
	// door can be cut through it. A door needs its own width plus a jamb on
	// either side, or the generator produces openings flush with a corner that
	// the player can see through and not walk through.
	MinSharedWall = DoorWidth + 2*PlayerRadius + 0.6

	// FloorStep is the granularity of the height changes between rooms. Every
	// generated change is a whole number of these and never exceeds MaxStep.
	FloorStep = 0.3

	// GenerationAttempts bounds how hard the generator tries to place a room
	// before giving up and finishing the level short. It is a bound on work
	// rather than a tuning knob: the invariant tests assert the level is valid,
	// not that it is large.
	GenerationAttempts = 400
)

// Config is the whole catalogue as the browser receives it.
type Config struct {
	Player   PlayerConfig `json:"player"`
	Gun      GunConfig    `json:"gun"`
	Pickups  []PickupKind `json:"pickups"`
	Surfaces []Surface    `json:"surfaces"`
	Sim      SimConfig    `json:"sim"`
	World    WorldConfig  `json:"world"`
}

// GunConfig is everything about the обрез the client has to know: enough to draw
// the shell count and the reload, enough to run the same trigger rule the server
// runs, and enough to say all of it in Russian on the splash screen without a
// number being typed out twice.
//
// Ammo is the counter a reload spends, published so the cheatsheet can JOIN it
// against Pickups rather than being told the name twice: the entry whose Grants
// matches is the thing to walk to, with its own title, icon and blurb already on
// it. That join is what lets a second ammunition ever be a catalogue line.
type GunConfig struct {
	Barrels             int     `json:"barrels"`
	FireCooldownSeconds float64 `json:"fire_cooldown_seconds"`
	ReloadSeconds       float64 `json:"reload_seconds"`
	ReloadCost          int     `json:"reload_cost"`
	Ammo                string  `json:"ammo"`
	// Damage is what one barrel takes off whoever it lands on, against the
	// player's own MaxHealth published below — the two together are how the
	// cheatsheet says "two in the chest and he is done" without either number
	// being typed out on the client.
	Damage int `json:"damage"`
}

// WorldConfig is what the client needs to describe the building itself: how many
// people fit in it, and how long a thing takes to come back once somebody has
// taken it. Both are rules a player has to be told, so both are derived from the
// constants above rather than typed out on the splash screen.
type WorldConfig struct {
	MaxOccupants   int     `json:"max_occupants"`
	RespawnSeconds float64 `json:"respawn_seconds"`
	// DownSeconds is how long a dead man lies there and ProtectSeconds is how
	// long he is untouchable — and unable to shoot — after he appears at the
	// spawn, which is every time he gets up there AND the moment he first walks
	// in (world.go, protect). Both are rules a player has to be told before he
	// walks in, and both are also DURATIONS THE CLIENT RUNS: the HUD counts the
	// first down and the second is what stops a muzzle flash being drawn for a
	// trigger the server refused.
	DownSeconds    float64 `json:"down_seconds"`
	ProtectSeconds float64 `json:"protect_seconds"`
	// BetrayalsTitle is what to call the standings column that counts the
	// friends you have shot. See the constant.
	BetrayalsTitle string `json:"betrayals_title"`
}

// PlayerConfig is what the client needs to draw and describe the player.
type PlayerConfig struct {
	Radius    float64 `json:"radius"`
	EyeHeight float64 `json:"eye_height"`
	// BodyHeight is how tall a man is to be shot at, and therefore how tall the
	// figure drawn for a peer ought to be: a client that drew him shorter than
	// the server shoots at would be drawing a hitbox nobody can see.
	BodyHeight  float64 `json:"body_height"`
	WalkSpeed   float64 `json:"walk_speed"`
	MaxStep     float64 `json:"max_step"`
	MaxPitch    float64 `json:"max_pitch"`
	MaxHealth   int     `json:"max_health"`
	StartHealth int     `json:"start_health"`
}

// SimConfig tells the client the rates it has to match. The input rate is not
// advice: a client that sends faster is rate-limited by the socket, and one that
// packs more than MaxCommands into a frame has the surplus dropped.
type SimConfig struct {
	Hz             int     `json:"hz"`
	SnapshotHz     int     `json:"snapshot_hz"`
	InputHz        int     `json:"input_hz"`
	MaxCommands    int     `json:"max_commands"`
	MaxStepSeconds float64 `json:"max_step_seconds"`
	// Redundant is how many already-sent commands may ride along in a frame, so
	// one lost packet costs no input.
	Redundant int `json:"redundant"`
	// InterpDelayMs is how far in the past a peer is drawn. Served rather than
	// chosen by the client, because lag compensation rewinds by exactly this
	// number and a client that picked it could pick an advantage.
	InterpDelayMs int `json:"interp_delay_ms"`
	// CollisionPasses is how many times the resolver sweeps the wall list. The
	// client needs it because it runs the SAME collision code — see the
	// prediction port and its golden-vector conformance test.
	CollisionPasses int `json:"collision_passes"`
}

// InputHz is how often the client is expected to send a frame. It is a third of
// the socket's allowance rather than all of it, so a burst of retries or a
// clock that runs fast cannot get a player disconnected for rate abuse in the
// middle of a fight.
const InputHz = 10

// BuildConfig assembles the served catalogue. It is a pure function of the
// constants above, so there is exactly one place a number lives.
func BuildConfig() Config {
	return Config{
		Player: PlayerConfig{
			Radius:      PlayerRadius,
			EyeHeight:   EyeHeight,
			BodyHeight:  BodyHeight,
			WalkSpeed:   WalkSpeed,
			MaxStep:     MaxStep,
			MaxPitch:    MaxPitch,
			MaxHealth:   MaxHealth,
			StartHealth: StartHealth,
		},
		Gun: GunConfig{
			Barrels:             Barrels,
			FireCooldownSeconds: FireCooldownSeconds,
			ReloadSeconds:       ReloadSeconds,
			ReloadCost:          ReloadCost,
			Ammo:                AmmoCounter,
			Damage:              BarrelDamage,
		},
		Pickups:  Pickups,
		Surfaces: Surfaces,
		Sim: SimConfig{
			Hz:              SimHz,
			SnapshotHz:      int(time.Second / SnapshotInterval),
			InputHz:         InputHz,
			MaxCommands:     MaxCommandsPerFrame,
			MaxStepSeconds:  MaxStepSeconds,
			Redundant:       RedundantCommands,
			InterpDelayMs:   int(InterpolationDelay / time.Millisecond),
			CollisionPasses: collisionPasses,
		},
		World: WorldConfig{
			MaxOccupants:   MaxOccupants,
			RespawnSeconds: PickupRespawn.Seconds(),
			DownSeconds:    DownTime.Seconds(),
			ProtectSeconds: SpawnProtectSeconds,
			BetrayalsTitle: BetrayalsTitle,
		},
	}
}
