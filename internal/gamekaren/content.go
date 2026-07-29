package gamekaren

// THE CATALOGUE.
//
// Every piece of content and every tuning constant this game has lives in this
// one file: the office layout, the movement speeds, the money ramp, the bald
// man, the endings and every line he says. It is served whole to the SPA by
// GET /api/game-karen/config, so the client hardcodes no key, no label and no
// number — which is what lets the splash screen's rules cheatsheet be GENERATED
// from the served config rather than typed out, and what makes retuning the game
// a backend-only change with no migration and no client deploy.
//
// The rule for what belongs here: content is anything a person would change by
// feel. Speeds, rates, labels, the shape of the room, which ending a cause maps
// to. What does NOT belong here is behaviour — a genuinely new verb is a service
// method, a handler and a route, and no catalogue can make that free.

// GameKey identifies this game in shared, multi-game storage. Today that is the
// art blob store, which is keyed on (game_key, art_key) — infrastructure rather
// than this game's property, which is why the value appears here and the table
// does not appear in this game's migration.
//
// It is a VALUE, not a name: the game's own table carries its identity in its
// name, so nothing in this package's schema has a game_key column.
const GameKey = "karen"

// Title is what the splash screen calls itself.
const Title = "СИМУЛЯТОР КАРЕНА"

// Office geometry, in metres.
//
// STATIC, and that is the load-bearing simplification against «ВАНЯДУМ». That
// game needs a generator because a run is a fresh заброшка; this one is a single
// open-plan floor that is always the same floor, so the layout is a constant, no
// seed is stored, and nothing about the room is ever sent at shift start — it is
// already in the catalogue the client fetched to draw the splash screen.
const (
	OfficeW = 16.0
	OfficeH = 22.0

	// PlayerRadius and BossRadius are the discs the collision resolver pushes
	// out of furniture. He is slightly wider than you, which is the only
	// physical advantage he has and is not enough.
	PlayerRadius = 0.35
	BossRadius   = 0.40
)

// Rect is an axis-aligned rectangle in office metres: (X, Y) is its top-left
// corner. It is on the wire inside the catalogue, hence the tags.
type Rect struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

// Vec2 is a point in office metres.
//
// ORIGIN TOP-LEFT, +X RIGHT, +Y DOWN — the same convention the plane is drawn
// with, so there is no axis flip anywhere in the client and a position read off
// a snapshot places an element directly.
type Vec2 struct{ X, Y float64 }

// Desks are the furniture. Both the player and the bald man are pushed out of
// them and neither paths around them: bumping into a desk and sliding along it
// is correct, funny, and is the whole reason desks are tactically useful — he
// takes the long way round and you do not.
//
// Two rows of four with a clear central lane, so a chase always has somewhere to
// go. Every desk is at least BossRadius clear of the walls, which is what lets
// the push-out resolver be a single pass with no risk of shoving anybody through
// a wall; content_test pins it.
var Desks = []Rect{
	{X: 2.0, Y: 3.0, W: 3.4, H: 1.2}, {X: 2.0, Y: 6.5, W: 3.4, H: 1.2},
	{X: 2.0, Y: 10.0, W: 3.4, H: 1.2}, {X: 2.0, Y: 13.5, W: 3.4, H: 1.2},
	{X: 10.6, Y: 3.0, W: 3.4, H: 1.2}, {X: 10.6, Y: 6.5, W: 3.4, H: 1.2},
	{X: 10.6, Y: 10.0, W: 3.4, H: 1.2}, {X: 10.6, Y: 13.5, W: 3.4, H: 1.2},
}

// Movement.
//
// THESE ARE PIXELS PER SECOND ON A PHONE, NOT METRES PER SECOND ON PAPER, and
// that is the mistake the first tuning made. The plane is ~349 px wide for the
// office's 16 m, so 22 px is a metre — and the original 3.2 m/s walk was 70 px/s,
// which crosses the plane in five seconds and reads as wading. The dash was
// worse: 9 m/s for 0.22 s is 2 m, which is 43 px, barely a thumb's width and
// only 1.7x the radius it has to break. It looked decisive in metres and did
// nothing on glass.
//
// The player's numbers are raised and the BOSS'S ARE NOT, deliberately: his
// approach takes 6.5 s against a 6 s ramp, and that margin is the whole tension
// of the game (design 12.1). Raising his speed would close it and make the x3
// unreachable, so "make it feel faster" is a change to how the player moves, not
// to the clock he is racing.
const (
	// WalkSpeed is how fast you move when you have decided to stop earning.
	WalkSpeed = 6.4 // m/s

	// DashSpeed, DashSeconds and DashCooldown are the one verb iteration 1 has.
	// It is deliberately short and deliberately expensive to repeat: the dash is
	// the answer to being caught, not a way to travel.
	DashSpeed    = 20.0 // m/s
	DashSeconds  = 0.26
	DashCooldown = 2.2

	// IdleThreshold is how small a movement vector has to be before the
	// simulation calls it standing still. It is not zero because an analogue
	// stick on a phone never returns exactly zero, and a player whose thumb is
	// resting on the stick is standing still as far as anybody watching is
	// concerned.
	IdleThreshold = 0.05
)

// Money — the core feel, and the reason the game exists.
const (
	// BasePerSecond is the salary at ×1, in rubles a second. A number chosen so
	// that a good shift is a comically large one.
	BasePerSecond = 120.0

	// RampSeconds is how long you have to stand perfectly still to reach the
	// cap, and MaxMultiplier is the cap.
	//
	// Six seconds is the tuning that makes the game: it is long enough that the
	// ramp is a thing you are PROTECTING rather than a thing you re-earn, and
	// short enough that a shift has several full ramps in it. Retuning it
	// retunes the whole game, and the splash-screen cheatsheet updates itself
	// because it is generated from the served config.
	RampSeconds   = 3.5
	MaxMultiplier = 3.0

	// GraceSeconds is how long you may move before the streak resets.
	//
	// It exists so that a twitch — a mis-tap, a stick that drifted, a step taken
	// and immediately regretted — does not cost six seconds of ramp. It is NOT
	// the dash: a dash costs nothing at all, however long it lasts, which is the
	// asymmetry the whole skill ceiling rests on.
	GraceSeconds = 0.18
)

// The bald man.
const (
	// BossSpeed is slower than a walk and faster than a stroll. He never runs
	// and he never stops, which is the joke: you can always outpace him and you
	// can never be rid of him.
	BossSpeed = 4.0 // m/s

	// CatchRadius is how close he has to get. Added to PlayerRadius, so the
	// shift ends when the two discs are about a metre apart — the distance at
	// which somebody has visibly arrived at your desk.
	CatchRadius = 0.85

	// GrinRange is how far away he starts smiling. The grin is 1 − dist/range,
	// clamped, and the client picks a face from it — so the approach is legible
	// from across the room without a health bar.
	GrinRange = 6.0

	// Spawns. He starts at the far end of the floor, more than GrinRange from
	// where you start, or the shift would be over before you had read anything.
	BossSpawnX   = 8.0
	BossSpawnY   = 20.5
	PlayerSpawnX = 8.0
	PlayerSpawnY = 4.0
)

// Endings. The cause is a plain string all the way down to the `cause` column,
// so iteration 4's «тебя раскусили» is a catalogue entry and not a migration.
const (
	// CausePromoted is what happens when he reaches you. It is the bad ending
	// and it is called a promotion, because that is what it is.
	CausePromoted = "promoted"
	// CauseLeft is walking out — the shift counts, and nobody noticed.
	CauseLeft = "left"
)

// Ending is one way a shift can end, as the over screen renders it.
type Ending struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	Sub   string `json:"sub"`
}

// Endings is the whole set. Two in iteration 1 (§1 decision 12).
var Endings = []Ending{
	{Key: CausePromoted, Title: "ТЕБЯ ПОВЫСИЛИ", Sub: "поздравляем. теперь ты за это отвечаешь."},
	{Key: CauseLeft, Title: "ТЫ ПРОСТО УШЁЛ", Sub: "смена засчитана. никто не заметил."},
}

// BossLines is what he says.
//
// Carried in the config from iteration 1 even though nothing draws a balloon
// yet, because the SHAPE of the catalogue is what the client is written against
// and adding an array to a live contract later is a coordinated deploy. They are
// the eight sentences that end an afternoon.
var BossLines = []string{
	"А ГДЕ?",
	"НУ ЧТО, ПОСМОТРЕЛ?",
	"ЗАЙДИ НА МИНУТКУ",
	"ЭТО ЖЕ НА ПЯТЬ МИНУТ",
	"ДАВАЙ ДО КОНЦА СПРИНТА",
	"Я ТЕБЕ В ЛИЧКУ НАПИСАЛ",
	"ТЫ ЖЕ ГОВОРИЛ ЧТО СДЕЛАЛ",
	"ПРОСТО ПОСМОТРИ, НИЧЕГО НЕ ДЕЛАЙ",
}

// Config is the whole catalogue as the browser receives it.
//
// Every field name here is the client's contract and the input to the generated
// rules cheatsheet. Renaming one is a client change; adding one is not.
//
// NOTHING IS WITHHELD in iteration 1. The bald man's behaviour is one speed and
// one radius, both of which the client draws and the server enforces, so
// publishing them costs nothing and lets the cheatsheet be derived rather than
// typed. When he gains a throw cadence or an aim model, THOSE are withheld with
// `json:"-"` — a number the client does not need is a number that tells a player
// how to beat him.
type Config struct {
	GameKey      string       `json:"game_key"`
	Title        string       `json:"title"`
	Office       OfficeConfig `json:"office"`
	Money        MoneyConfig  `json:"money"`
	Move         MoveConfig   `json:"move"`
	Boss         BossConfig   `json:"boss"`
	Sim          SimConfig    `json:"sim"`
	Endings      []Ending     `json:"endings"`
	BossLines    []string     `json:"boss_lines"`
	MaxOccupants int          `json:"max_occupants"`
}

// OfficeConfig is the room, which is all the client needs to draw the plane.
type OfficeConfig struct {
	W            float64 `json:"w"`
	H            float64 `json:"h"`
	Desks        []Rect  `json:"desks"`
	PlayerRadius float64 `json:"player_radius"`
	BossRadius   float64 `json:"boss_radius"`
}

// MoneyConfig is the ramp. Every one of these four numbers appears in the
// generated cheatsheet, which is why the grace window is published in
// milliseconds — the client formats what it is given and converts nothing.
type MoneyConfig struct {
	BasePerSecond float64 `json:"base_per_second"`
	RampSeconds   float64 `json:"ramp_seconds"`
	MaxMultiplier float64 `json:"max_multiplier"`
	GraceMs       int     `json:"grace_ms"`
}

// MoveConfig is movement, including the two rates the client must match: it
// samples at the simulation rate and sends at InputHz, and the ratio between
// them is what MaxCommands bounds.
type MoveConfig struct {
	WalkSpeed      float64 `json:"walk_speed"`
	DashSpeed      float64 `json:"dash_speed"`
	DashMs         int     `json:"dash_ms"`
	DashCooldownMs int     `json:"dash_cooldown_ms"`
	InputHz        int     `json:"input_hz"`
	MaxCommands    int     `json:"max_commands"`
}

// BossConfig is everything about him the client draws.
type BossConfig struct {
	Speed       float64 `json:"speed"`
	CatchRadius float64 `json:"catch_radius"`
	GrinRange   float64 `json:"grin_range"`
}

// SimConfig is the two rates the client runs its own clocks against.
type SimConfig struct {
	Hz         int `json:"hz"`
	SnapshotHz int `json:"snapshot_hz"`
}

// BuildConfig assembles the served catalogue. It is a pure function of the
// constants above, so the served contract and the simulation can never disagree
// about a number.
func BuildConfig() Config {
	return Config{
		GameKey: GameKey,
		Title:   Title,
		Office: OfficeConfig{
			W:            OfficeW,
			H:            OfficeH,
			Desks:        append([]Rect(nil), Desks...),
			PlayerRadius: PlayerRadius,
			BossRadius:   BossRadius,
		},
		Money: MoneyConfig{
			BasePerSecond: BasePerSecond,
			RampSeconds:   RampSeconds,
			MaxMultiplier: MaxMultiplier,
			GraceMs:       int(GraceSeconds * 1000),
		},
		Move: MoveConfig{
			WalkSpeed:      WalkSpeed,
			DashSpeed:      DashSpeed,
			DashMs:         int(DashSeconds * 1000),
			DashCooldownMs: int(DashCooldown * 1000),
			InputHz:        InputHz,
			MaxCommands:    MaxCommandsPerFrame,
		},
		Boss: BossConfig{
			Speed:       BossSpeed,
			CatchRadius: CatchRadius,
			GrinRange:   GrinRange,
		},
		Sim: SimConfig{
			Hz:         SimHz,
			SnapshotHz: SimHz / SnapshotEvery,
		},
		Endings:      append([]Ending(nil), Endings...),
		BossLines:    append([]string(nil), BossLines...),
		MaxOccupants: MaxOccupants,
	}
}
