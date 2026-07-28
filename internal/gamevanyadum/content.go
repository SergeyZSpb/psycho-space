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
	Blurb string `json:"blurb"`
}

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
		Blurb:  "Заливаешь — и панчи сами идут. Пока просто копится.",
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

// Level generation. A run's whole geometry is a function of these numbers and a
// seed, so a level is reproducible from the two and is never stored.
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
	Pickups  []PickupKind `json:"pickups"`
	Surfaces []Surface    `json:"surfaces"`
	Sim      SimConfig    `json:"sim"`
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
		Pickups:  Pickups,
		Surfaces: Surfaces,
		Sim: SimConfig{
			Hz:             SimHz,
			SnapshotHz:     int(time.Second / SnapshotInterval),
			InputHz:        InputHz,
			MaxCommands:    MaxCommandsPerFrame,
			MaxStepSeconds: MaxStepSeconds,
		},
	}
}
