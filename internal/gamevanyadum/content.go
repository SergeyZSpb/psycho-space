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

// Health. A player starts full and, in this iteration, has nothing that can
// hurt him — the numbers exist so the HUD is real rather than a placeholder,
// and so the shape is already right when the slop arrives.
const (
	MaxHealth   = 100
	StartHealth = 100
)

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
// FIVE IS A MEASURED LIMIT AND NOT A PREFERENCE. What a viewer is sent is two
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
//	solo snapshot             160 bytes
//	the first peer            +56  (the entry, plus the `p` array around it)
//	each further peer         +50
//	standings with one row     81
//	each further row          +53
//
// So a viewer pays 20 × (160 + 56 + (N−2)×50) + (81 + (N−1)×53) a second:
//
//	N = 4    316 × 20 + 240 = 6560 B/s
//	N = 5    366 × 20 + 293 = 7613 B/s
//	N = 6    416 × 20 + 346 = 8666 B/s — over
//
// PLUS A THIRD TERM THAT IS NOT ON EVERY FRAME. A peer's muzzle flash rides the
// tick its owner pulled the trigger and no other (message.go, Peer.Fired), so it
// is priced at the gun's cadence rather than at the snapshot rate: 9 bytes × 3
// shots a second × (N−1) peers, which is 108 B/s at five and 135 at six. That
// takes the two rows that matter to 7721 and 8801.
//
// Five, with 279 B/s of headroom. Pinned by
// TestEverythingAFullBuildingSendsAViewerFitsTheCeiling, which fails when this
// constant is raised or when either frame grows a field.
//
// THE GUN TOOK THE SOLO FRAME FROM 137 TO 160 AND HALVED THAT HEADROOM. The
// barrel count rides every frame (6 bytes) and the two timers ride the frames
// where the gun is busy (8 and 9); the measurement above pessimistically counts
// both at once, which cannot happen. Five still fits and six still does not, so
// the capacity is unmoved — but the next field of that size is the one that costs
// a place, and the answer then is a smaller building or the binary codec, not a
// larger ceiling.
//
// MAKING THE SHOT VISIBLE TO EVERYBODY ELSE TOOK ANOTHER 108 OF WHAT WAS LEFT,
// and it is worth saying which shape it did NOT take: a per-peer cooldown would
// have been the duration this project prefers, and at 8 bytes on every one of
// twenty ticks a second it is 640 B/s — comfortably past what there was. The
// flag fits because the cadence bounds how often it can be sent, which is
// exactly the property that keeps it out of the twenty-times-a-second column.
//
// THE STANDINGS FRAME IS INSIDE THE CEILING AND NOT BESIDE IT. It is traffic to
// the same viewer, so leaving it out would be moving the line rather than
// meeting it — which is how this constant came to be six before anybody measured
// a peer.
//
// FOUR WAS THE SAME ARITHMETIC OVER A FATTER PEER. The entry lost its 19-byte
// pseudonym for a slot index, its eye height for a sector index, and a pose enum
// that nothing has ever set — 71 bytes to 49 (message.go, Peer) — and that is
// the whole of what bought the fifth place.
//
// INTEREST MANAGEMENT BOUGHT NONE OF IT, which is worth stating plainly rather
// than letting the two changes be credited together. Filtering peers to the
// viewer's own sector and the rooms through its doorways (level.go,
// buildVisibility) makes the TYPICAL frame much smaller, which is what a phone
// on mobile data actually experiences and is why it is worth having. But the
// budget is taken on the WORST case, and the worst case is everybody standing in
// one room — where the filter removes nothing at all. A capacity derived from
// the typical frame would be a capacity that fails the first time five people
// crowd into one doorway. The same argument covers the hold that keeps a peer on
// the frame for a moment after he leaves the set (visibleHold): it can only ADD
// to a filtered set, and the unfiltered set is what is budgeted here.
//
// THE NEXT STEP UP NEEDS THE BINARY CODEC. There is no further byte worth
// finding in JSON: a peer is five integers behind keys of one and three
// characters, and what is left of the entry is punctuation. The design doc
// earmarked a binary codec as an iteration of its own, and that — rather than
// another round of trimming — is what a sixth place costs.
//
// A refusal is a refusal and never a queue: somebody who arrives at a full
// заброшка is told so (the `vanyadum_full` frame) rather than being put on hold,
// because there is nothing to hold them for — nothing here ends.
const MaxOccupants = 5

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
}

// WorldConfig is what the client needs to describe the building itself: how many
// people fit in it, and how long a thing takes to come back once somebody has
// taken it. Both are rules a player has to be told, so both are derived from the
// constants above rather than typed out on the splash screen.
type WorldConfig struct {
	MaxOccupants   int     `json:"max_occupants"`
	RespawnSeconds float64 `json:"respawn_seconds"`
}

// PlayerConfig is what the client needs to draw and describe the player.
type PlayerConfig struct {
	Radius      float64 `json:"radius"`
	EyeHeight   float64 `json:"eye_height"`
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
		},
	}
}
