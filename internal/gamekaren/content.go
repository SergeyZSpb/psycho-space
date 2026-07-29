package gamekaren

import "slices"

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
	{X: 2.8, Y: 3.0, W: 2.6, H: 1.0}, {X: 2.8, Y: 7.0, W: 2.6, H: 1.0},
	{X: 2.8, Y: 11.0, W: 2.6, H: 1.0}, {X: 2.8, Y: 15.0, W: 2.6, H: 1.0},
	{X: 10.6, Y: 3.0, W: 2.6, H: 1.0}, {X: 10.6, Y: 7.0, W: 2.6, H: 1.0},
	{X: 10.6, Y: 11.0, W: 2.6, H: 1.0}, {X: 10.6, Y: 15.0, W: 2.6, H: 1.0},
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

// The balloons. Both figures always have one over their head, and WHICH one is
// the server's decision — which is what makes two people in the same office see
// the same words. The wire carries an INDEX into these pools and never the text
// (ADR-037): a snapshot repeats ten times a second, per viewer, forever, and a
// Cyrillic sentence on it is forty bytes of something the client already
// fetched once with the catalogue.
//
// INDEX 0 IS LOAD-BEARING IN BOTH POOLS. The field is `omitempty`, so an absent
// index means zero — which makes the first line of each pool the default, the
// one drawn whenever nothing more interesting is true. Prepending a line to
// either of these silently changes what everybody says when nothing is
// happening, so `TestTheDefaultLinesAreFirst` pins both.

// BossLines is what he says.
//
// The first is who he is; the rest are the eight sentences that end an
// afternoon, and he starts using them once he is close enough to be a problem.
// BossLines is the flat pool the wire indexes into, built from two named runs so
// the browser never learns the layout — which run a state maps to is server-side
// and changes without a client deploy. Exactly the arrangement KarenLines uses.
var BossLines = append(append([]string{}, bossFar...), bossClosing...)

// bossFar is what he says from across the office, and it EXISTS so that he
// rotates while he is far away.
//
// He used to say «Я ЛЫСЫЙ» and nothing else until his grin started, which meant
// the balloon over the only other figure on the plane was frozen for most of a
// shift. One line is not a run, so rotating required writing more of them —
// same voice, but aimed at nobody in particular: a man across the room being
// pleased with the day rather than pleased with you.
var bossFar = []string{
	"Я ЛЫСЫЙ",
	"ХОРОШО СИДИМ",
	"НАДО БЫ СИНХРОНИЗИРОВАТЬСЯ",
	"У МЕНЯ ОКНО ПОСЛЕ ОБЕДА",
	"Я ПРОСТО ПОСМОТРЕТЬ",
	"КОМАНДА, Я ЗДЕСЬ",
	"КАКИЕ У НАС ПЛАНЫ",
}

// bossClosing is what he says once the grin has started: he is a micromanager,
// so almost everything is a request for a status disguised as a favour. None of
// it is angry and none of it is an order — that is the whole character. It is
// always just a minute, always just a quick sync, and it never, ever stops.
var bossClosing = []string{
	"А ГДЕ?",
	"НУ ЧТО, ПОСМОТРЕЛ?",
	"ЗАЙДИ НА МИНУТКУ",
	"ЭТО ЖЕ НА ПЯТЬ МИНУТ",
	"ДАВАЙ ДО КОНЦА СПРИНТА",
	"Я ТЕБЕ В ЛИЧКУ НАПИСАЛ",
	"ТЫ ЖЕ ГОВОРИЛ ЧТО СДЕЛАЛ",
	"ПРОСТО ПОСМОТРИ, НИЧЕГО НЕ ДЕЛАЙ",
	"ЕСТЬ МИНУТКА?",
	"СИНХРОНИЗИРУЕМСЯ?",
	"НА КАКОМ ЭТАПЕ?",
	"Я НЕ ТОРОПЛЮ, НО КОГДА?",
	"ДАВАЙ БЫСТРЕНЬКО СОЗВОН",
	"НЕ СРОЧНО. НО СЕГОДНЯ",
	"ТЫ ЖЕ ПОМНИШЬ ПРО ДЕМО",
	"Я ПРОСТО МИМО ПРОХОДИЛ",
	"МЕЛОЧЬ, НО ПЕРЕДЕЛАЙ",
	"ДОБАВИЛ ТЕБЯ В ЕЩЁ ОДИН ЧАТ",
	"ПОСТАВИЛ ВСТРЕЧУ НА 19:00",
	"ЭТО НЕ КОНТРОЛЬ, ЭТО ЗАБОТА",
	"ПРОСТО ДЕРЖИ МЕНЯ В КУРСЕ",
	"КИНЬ В ТРЕД СКРИНШОТ",
	"А ОЦЕНКУ ДАШЬ?",
	"ОБСУДИМ НА ДЕЙЛИКЕ",
	"Я УЖЕ СКАЗАЛ ЧТО ГОТОВО",
	"ТЫ ТОЛЬКО НЕ ОТВЛЕКАЙСЯ",
}

// KarenLines is what YOU say, and it is a readout as much as a joke: the line
// over your head is a function of what the simulation thinks you are doing, so
// it is the one place the streak rule states itself while you are playing
// rather than on the splash screen you have already scrolled past.
// THREE RUNS, CONCATENATED, AND THE CLIENT NEVER LEARNS THE LAYOUT.
//
// The wire is one index into one flat array, so the browser renders
// `karen_lines[p]` and knows nothing about which line means what — the whole of
// that is `KarenLine` below, on the server, where it can change without a client
// deploy. Splitting the pools on the wire instead would mean sending which pool
// AND which line, which is a second field on a frame that repeats ten times a
// second to say something the server already knows.
//
// Each run is what a lazy man says while doing that particular nothing. He is
// never idle — he is thinking, he is in context, he is almost done. The first
// line of the first run is «Я КАРЕН» because an absent index means zero (see
// the note above BossLines).
var karenStill = []string{
	"Я КАРЕН",
	"Я ДУМАЮ",
	"Я В КОНТЕКСТЕ",
	"ЗАГРУЖАЮСЬ",
	"ЭТО СЛОЖНАЯ ЗАДАЧА",
	"АНАЛИЗИРУЮ",
	"ПОЧТИ ГОТОВО",
	"ДА Я УЖЕ ДЕЛАЮ",
	"ТУТ НАДО ПОДУМАТЬ",
	"Я В ПОТОКЕ",
	"ЖДУ ОТВЕТА",
	"ЭТО НЕ КО МНЕ",
}

var karenMoving = []string{
	"Я ПРОСТО ВОДЫ ПОПИТЬ",
	"Я НА МИНУТКУ",
	"МНЕ ПОЗВОНИЛИ",
	"Я ЗА КОФЕ",
	"Я СЕЙЧАС ВЕРНУСЬ",
	"ЭТО ПО РАБОТЕ",
	"НАДО РАЗМЯТЬСЯ",
}

var karenDashing = []string{
	"Я НА ВСТРЕЧУ",
	"МЕНЯ ЗОВУТ",
	"У МЕНЯ ДЕЙЛИК",
	"Я НЕ УБЕГАЮ",
	"ЭТО НЕ Я",
	"СРОЧНО НАДО",
}

// KarenLines is the three runs above, in order, as the catalogue serves them.
var KarenLines = slices.Concat(karenStill, karenMoving, karenDashing)

// BossSlot is how long he holds one of his sentences before moving to the next.
//
// Derived from the TICK rather than from a timer or a random draw, so every
// viewer of the same office computes the same line for the same instant without
// anything being stored or synchronised — the tick is already on every frame.
// TWO SECONDS, owner-directed, and it is the same number everybody on the plane
// uses now. Long enough to read on a phone while somebody is walking at you, and
// short enough that the office never looks frozen.
const BossSlot = 40 // ticks, at SimHz — 2 s

// BossQuiet is how small his grin has to be before he goes back to introducing
// himself. It is the same number the client's «far» face uses, so what he says
// and what his face does change together rather than a beat apart.
const BossQuiet = 0.35

// KarenSlot is how long he holds one line before moving to the next, in ticks.
//
// THE SAME AS THE BALD MAN'S, at the owner's direction. It was twice his, on the
// reasoning that yours is the line you stare at while standing still and that
// changing it often would be movement on a screen whose point is that nothing
// moves. In practice the office read as frozen rather than as calm — the plane
// holds several figures now, and two of them holding a sentence for four seconds
// is a still photograph. One cadence, everywhere, two seconds.
const KarenSlot = 40 // ticks, at SimHz — 2 s

// KarenLine is which of KarenLines belongs over a player right now.
//
// The STATE picks the run and the TICK picks the line within it — the same
// closed form the bald man uses, so nothing is stored, nothing expires, and two
// people watching the same office read the same words at the same instant.
//
// PURE, AND DELIBERATELY NOT IN sim.go. `Step` is pinned to its TypeScript port
// by the golden vectors, and a balloon is neither predicted nor simulated — it
// is read off the state `Step` has already produced. Putting it on `Player`
// would force a vector regeneration and a client change for a value the
// simulation never reads.
func KarenLine(p Player, tick uint64) int {
	switch {
	case p.DashLeft > 0:
		return pickLine(len(karenStill)+len(karenMoving), karenDashing, tick, KarenSlot)
	case p.MoveGrace > 0:
		// He has moved inside the grace window, so the streak is at risk even
		// though it has not gone yet. That is exactly when it is worth saying.
		return pickLine(len(karenStill), karenMoving, tick, KarenSlot)
	default:
		return pickLine(0, karenStill, tick, KarenSlot)
	}
}

// IntroEvery is roughly how many slots pass between somebody saying who they
// are — «Я КАРЕН», «Я ЛЫСЫЙ».
//
// ROUGHLY, and that is the point (owner-directed): a fixed period would make the
// introduction a metronome, and the whole reason it is worth interjecting is
// that it lands when you were not expecting it. So it is a hash of the slot
// rather than a modulus of it, and at five it comes round about every ten
// seconds without ever being ON the ten seconds.
const IntroEvery = 5

// slotHash mixes a slot number into something with no visible order.
//
// A HASH RATHER THAN A COUNTER, because `slot % n` walks a pool in the order it
// happens to be written in — which reads as a script being recited, and makes
// the next line predictable from the last. Splitmix64's finaliser is plenty: the
// requirement is that consecutive slots land far apart, not that anything here
// is unguessable.
//
// It is a pure function of the slot, so — like everything else about a balloon —
// every viewer of the same office computes the same words for the same instant
// with nothing stored, nothing synchronised and nothing on the wire.
func slotHash(slot, salt uint64) uint64 {
	x := slot*0x9E3779B97F4A7C15 + salt
	x ^= x >> 31
	x *= 0xBF58476D1CE4E5B9
	x ^= x >> 29
	return x
}

// pickLine is which line of a run belongs to the slot a tick falls in, as an
// absolute index into the flat pool.
//
// `base` is where the run starts in the pool, which is what makes the wire a
// single index into a single array however the runs are arranged.
//
// Three rules, and each is a thing that was wrong without it:
//
//   - THE SLOT, not the tick, so a line is held for the whole of it. Two seconds
//     is the cadence everybody on the plane shares.
//   - HASHED, not sequential, so the pool is not recited in the order somebody
//     wrote it in.
//   - THE INTRODUCTION IS INTERJECTED, on its own hashed schedule, so «Я КАРЕН»
//     and «Я ЛЫСЫЙ» come round periodically and OUT OF ORDER — including in the
//     runs that do not contain them at all, which is every one of the bald man's
//     closing lines.
//
// The no-immediate-repeat guard is closed form as well: it recomputes the
// previous slot's answer rather than remembering it, so this stays a pure
// function of the clock.
func pickLine(base int, run []string, tick uint64, slotTicks uint64) int {
	n := uint64(len(run))
	if n == 0 || slotTicks == 0 {
		return 0
	}
	slot := tick / slotTicks
	got := lineIn(base, n, slot)
	if slot > 0 && got == lineIn(base, n, slot-1) {
		// Say something else rather than the same thing twice running — a
		// balloon that does not change across a slot boundary reads as frozen,
		// which is the defect this whole cadence exists to avoid.
		return nextLine(base, n, got)
	}
	return got
}

// lineIn is the unguarded pick for one slot: the introduction, or a line of the
// run.
func lineIn(base int, n, slot uint64) int {
	if slotHash(slot, introSalt)%IntroEvery == 0 {
		return introLine
	}
	// The remainder is strictly less than the run's length, which is a couple of
	// dozen at most. gosec cannot see that bound and flags every uint64
	// conversion on principle.
	//nolint:gosec // bounded by n, guarded non-zero by the caller
	return base + int(slotHash(slot, runSalt)%n)
}

// nextLine is the line along from this one, used only to break an immediate
// repeat. It is guaranteed to differ from `from` whenever there is anything else
// to say — which was NOT true of the first version, and the bug is worth naming
// because it looked impossible: for the still run `base` IS the introduction, so
// "the introduction repeated, start the run instead" returned the introduction.
func nextLine(base int, n uint64, from int) int {
	//nolint:gosec // n is a slice length, so it fits an int by construction
	end := base + int(n)
	if from < base || from >= end {
		// Outside the run — the introduction interjected into a run that does
		// not contain it. The run's own first line is somewhere else to be.
		if base != from {
			return base
		}
		return base + 1
	}
	if next := from + 1; next < end {
		return next
	}
	// Wrapped off the end of the run. Its first line, unless that is where we
	// already are, in which case the introduction is the only other option.
	if base != from {
		return base
	}
	return introLine
}

// introLine is where the self-introduction lives in every pool, and introSalt /
// runSalt keep the two hashed decisions independent — without different salts,
// "is this an introduction slot" and "which line of the run" would be the same
// number and the pool would be walked in lockstep with the interjections.
const (
	introLine = 0
	introSalt = 0x5A17_1234_ABCD_0001
	runSalt   = 0x5A17_1234_ABCD_0002
)

// BossLine is which of BossLines belongs over the bald man right now.
//
// Far away he is just a man with no hair. Once he is close enough for the grin
// to have started he begins working through the afternoon-enders, one per
// BossSlot, wrapping — so what he is saying keeps changing while he closes,
// which is the whole of the tension made audible.
func BossLine(grin float64, tick uint64) int {
	// THE STATE PICKS THE RUN AND THE TICK PICKS THE LINE, which is exactly what
	// KarenLine does — and the reason this is not simply `grin >= BossQuiet ? …`
	// any more is that HE USED TO SAY ONE SENTENCE FOREVER WHILE FAR AWAY. That
	// is most of a shift, and it left the balloon over the only other figure on
	// the plane frozen while everything else moved.
	if grin >= BossQuiet {
		return pickLine(len(bossFar), bossClosing, tick, BossSlot)
	}
	return pickLine(0, bossFar, tick, BossSlot)
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
	KarenLines   []string     `json:"karen_lines"`
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
		KarenLines:   append([]string(nil), KarenLines...),
		MaxOccupants: MaxOccupants,
	}
}
