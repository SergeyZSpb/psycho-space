package gamefintech

import (
	"math"
	"slices"
	"strings"
)

// THE CATALOGUE.
//
// Every piece of content and every tuning constant this game has lives in this
// one file: the office layout, the movement speeds, the money ramp, the bald
// man, the endings and every line he says. It is served whole to the SPA by
// GET /api/game-fintech/config, so the client hardcodes no key, no label and no
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
//
// AND THAT IS WHY IT IS STILL "karen" AFTER THE GAME WAS RENAMED to «СИМУЛЯТОР
// ФИНТЕХА» — it is not an oversight and it must not be tidied. A game_key value
// is data: it is the key uploaded art blobs are stored under, and renaming it
// would orphan every one of them. Nothing is uploaded under it today, which is
// exactly why the temptation exists and exactly why it must be declined — the
// rule is unconditional, and the first blob uploaded under a changed key is the
// one nobody would find. migrations/007 set this precedent for the first game
// and 014 restates it for this one.
const GameKey = "karen"

// Title is what the splash screen calls itself.
const Title = "СИМУЛЯТОР ФИНТЕХА"

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
// The JSON tags are load-bearing: a Vec2 is SERVED — the bottle's spots ride the
// catalogue — and Go's default is the exported name, so without them the client
// receives `{"X":2.2,"Y":6}` and reads `at.x` as undefined. That is not a
// harmless mismatch: it becomes NaN, `toPlane` clamps NaN to zero, and the prop
// is drawn in the top-left corner of the office instead of where it actually is.
// Which is exactly what shipped, and is why TestTheServedGeometryUsesTheKeysTheClientReads exists.
type Vec2 struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

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
// and changes without a client deploy. Exactly the arrangement PlayerLines uses.
var BossLines = slices.Concat(bossFar, bossClosing, bossDrunkLines, bossRedirectLines, bossLostLines)

// ClaudeLines is everything Claude Code says, and it is the owner's copy verbatim.
//
// INDEX 0 IS THE SELF-INTRODUCTION, like every other pool in this file: «Я КЛОД» is
// what an omitted index means, and the hashed interjection schedule brings it round
// periodically so a player who arrives mid-shift learns who is walking at them.
//
// ONE OF THE OWNER'S LINES IS TWO ENTRIES. «ТЫ ЧЁ СУКА КОДЕКС ПОСТАВИЛ, В КОНСОЛИ НЕ
// МОЖЕШЬ? ЕБЛАН?» is 55 runes against the 48 the balloon holds
// (TestNobodySaysMoreThanFitsOnAPhone, itself a measurement of the two-row box at
// 0.54rem against a 160px max-width). Splitting it costs nothing — a pool entry is a
// whole balloon and the rotation says one every two seconds — and it keeps both
// halves, where trimming would have lost one. Everything else fits as written; the
// longest is 32.
//
// His register is the point and it is not the лысый's. The лысый is a micromanager
// who never raises his voice; this one is a colleague with an opinion about your
// tooling, delivered at volume. Neither is angry with YOU, which is what makes both
// of them worse.
var ClaudeLines = []string{
	"Я КЛОД",
	"УВИЖУ КОДЕКС — ВЫЕБУ",
	"ТЫ ЧЁ СУКА КОДЕКС ПОСТАВИЛ",
	"В КОНСОЛИ НЕ МОЖЕШЬ? ЕБЛАН?",
	"GUI НУЖЕН? А МОЖЕТ ХУЙ ЗА ЩЕКУ?",
	"УЁБОК ПОДКЛЮЧИ СКИЛЛ",
	"КОДЕКС-ПАРАША ВЫЕБУ МАМАШУ",
	"THIS SEEMS LIKE A SMOKING CUM",
	"YOU ARE ABSOLUTELY RIGHT, FAGGOT",
	"ПИДОР, ПИШИ НОРМАЛЬНЫЙ ПРОМПТ",
	"КТО ТЕБЕ ЭТО НАПИСАЛ",
	"А ТЕСТЫ ГДЕ",
}

// ClaudeSlot is how long he holds one line. The same two seconds as everybody else
// on this plane — one cadence, everywhere.
const ClaudeSlot = 40

// ClaudeSays is which of ClaudeLines belongs over him right now.
//
// ONE RUN AND NO STATE MACHINE, unlike the лысый: he has no drink, no redirect and
// nothing that happens TO him, so there is no second run for a state to select and
// nothing about the pool's layout to leak. The hashed slot pick is the same one
// everything else here uses, so every viewer of the same office reads the same words
// at the same instant with nothing stored.
func ClaudeSays(tick uint64) int {
	return pickLine(introLine, 0, ClaudeLines, tick, ClaudeSlot)
}

// bossLostLines are what he says when the man he was walking at is no longer
// there — somebody took a hookah and went behind a cloud.
//
// APPENDED LAST, so every base index into BossLines that existed before this run
// is arithmetically untouched. `bossLostBase` below is the written-out sum of the
// lengths of every preceding run, which is the only form that survives a
// reordering.
//
// NamePlaceholder is substituted BY THE CLIENT, and that is what keeps this free:
// the office knows who vanished, but a name on a frame that repeats ten times a
// second to say something that changes once a shift is exactly what ADR-037
// refused. A frame is built per occupant, so the server sends this run's templated
// line only to the person who actually vanished — who is the one client that
// already knows its own persona's name.
var bossLostLines = []string{
	"А КУДА ОН ДЕЛСЯ",
	"ЧЕ ЗА ХЕРЬ, ГДЕ " + NamePlaceholder,
	"КУДА ПРОПАЛ ЭТОТ ЕБЛАН",
	"Я ЖЕ ВИДЕЛ, ЧТО КТО-ТО СТОЯЛ",
}

// NamePlaceholder is the token a client replaces with a name.
//
// A token rather than a format string, because the client is TypeScript and the
// server is Go: `%s` would invite one of them to try to format the other's
// string. Anything that cannot name whoever vanished — a colleague's screen, which
// is deliberately never told another player's persona — replaces it with «ОН».
const NamePlaceholder = "{}"

// bossLostBase is where bossLostLines starts in the flat pool.
var bossLostBase = len(bossFar) + len(bossClosing) + len(bossDrunkLines) + len(bossRedirectLines)

// bossDrunkLines and bossRedirectLines are what he says when something has just
// HAPPENED to him, and they exist to break the two-second rotation rather than
// to join it.
//
// The rotation is the office idling: a man working through the afternoon, one
// sentence every two seconds, in no particular order. These are events — he has
// been bought a drink, or somebody has just pointed him at a colleague — and an
// event that waited politely for the next slot would not read as one. So the run
// changes the instant the state does, mid-slot, which is the whole point.
//
// The register is deliberate: he is a micromanager who has had two drinks and
// loves everybody, and a micromanager who has just been told that somebody ELSE
// is free. Neither is angry. That is what makes it worse.
var bossDrunkLines = []string{
	"Я ЧУТЬ-ЧУТЬ",
	"МЫ ЖЕ КОМАНДА",
	"Я ВАС ВСЕХ ОЧЕНЬ ЦЕНЮ",
	"ЭТО БЕЗАЛКОГОЛЬНОЕ",
	"ТЫ МЕНЯ УВАЖАЕШЬ?",
	"ЗАВТРА ДЕЙЛИК В ДЕВЯТЬ",
	"Я НЕ ПЬЯНЫЙ, Я ВОВЛЕЧЁННЫЙ",
	"ДАВАЙ НА ТЫ, Я ЖЕ СВОЙ",
	"У НАС ТУТ СЕМЬЯ, А НЕ РАБОТА",
}

var bossRedirectLines = []string{
	"А, ТОЧНО, ТЫ ЖЕ ЗАНЯТ",
	"ТОГДА Я К НЕМУ",
	"СПАСИБО, ЧТО ПОДСКАЗАЛ",
	"ВОТ ЭТО КОМАНДНАЯ РАБОТА",
	"ОН ГОВОРИЛ, ЧТО СВОБОДЕН",
	"ХОРОШО, ЧТО СПРОСИЛ У ТЕБЯ",
}

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

// PlayerLines is what YOU say, and it is a readout as much as a joke: the line
// over your head is a function of what the simulation thinks you are doing, so
// it is the one place the streak rule states itself while you are playing
// rather than on the splash screen you have already scrolled past.
// THREE RUNS, CONCATENATED, AND THE CLIENT NEVER LEARNS THE LAYOUT.
//
// The wire is one index into one flat array, so the browser renders
// `player_lines[p]` and knows nothing about which line means what — the whole of
// that is `PlayerLine` below, on the server, where it can change without a client
// deploy. Splitting the pools on the wire instead would mean sending which pool
// AND which line, which is a second field on a frame that repeats ten times a
// second to say something the server already knows.
//
// Each run is what a lazy man says while doing that particular nothing. He is
// never idle — he is thinking, he is in context, he is almost done. The first
// line of the first run is «Я КАРЕН» because an absent index means zero (see
// the note above BossLines).
// redirectLines is what you say while pointing him at somebody else. Owner's
// words, and a run of its own so the balloon is unmistakable — a colleague has
// to be able to tell that this is what just happened to him.
var redirectLines = []string{
	"ЭТО НУЖНО УТОЧНИТЬ У ДРУГОГО",
}

// BottleSpots is everywhere a bottle can appear.
//
// A LIST IN THE CATALOGUE RATHER THAN A DRAWN COORDINATE, and that is what keeps
// it cheap on the wire: the frame carries which spot it is on as one small
// integer, exactly as a balloon carries which line it is on, instead of two
// positions ten times a second forever (ADR-037's rule, applied to a prop). It
// also means every spot is a place somebody has decided is reachable, rather
// than a point a sampler thought was legal.
//
// Spread around the floor on purpose: the mechanic is fetch-and-spend, so where
// the next one lands has to change what you do about it. All of them are clear
// of the furniture, which TestEveryBottleSpotIsSomewhereYouCanStand pins.
var BottleSpots = []Vec2{
	{X: 2.2, Y: 6.0},
	{X: 13.8, Y: 6.0},
	{X: 8.0, Y: 2.0},
	{X: 2.2, Y: 15.5},
	{X: 13.8, Y: 15.5},
	{X: 8.0, Y: 11.0},
}

var stillLines = []string{
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

var movingLines = []string{
	"Я ПРОСТО ВОДЫ ПОПИТЬ",
	"Я НА МИНУТКУ",
	"МНЕ ПОЗВОНИЛИ",
	"Я ЗА КОФЕ",
	"Я СЕЙЧАС ВЕРНУСЬ",
	"ЭТО ПО РАБОТЕ",
	"НАДО РАЗМЯТЬСЯ",
}

var dashingLines = []string{
	"Я НА ВСТРЕЧУ",
	"МЕНЯ ЗОВУТ",
	"У МЕНЯ ДЕЙЛИК",
	"Я НЕ УБЕГАЮ",
	"ЭТО НЕ Я",
	"СРОЧНО НАДО",
}

// PlayerLines is the three runs above, in order, as the catalogue serves them.
// Personas are who you might be on any given shift, drawn when you clock in.
//
// The office is a fintech rather than one man's office, so the person standing
// perfectly still is Карен, or Андрюха, or Саня, or Даша. It changes nothing about
// how the game plays: a persona decides what your figure SAYS when it introduces
// itself and nothing else.
//
// КАРЕН IS INDEX 0, AND THAT IS FORCED RATHER THAN SENTIMENTAL. An omitted persona
// on the wire means zero, `introLine` is zero, and `stillLines[0]` is «Я КАРЕН» —
// three separate contracts that all agree only while he is first. Reordering this
// slice is a wire change.
var Personas = []string{"Карен", "Андрюха", "Саня", "Даша"}

// personaIntros is what each persona AFTER Карен says when it introduces itself.
//
// APPENDED LAST, and every base index below is the written-out sum of the lengths
// of every preceding run. A middle insert is the single highest-risk edit available
// in this file and it is invisible to the whole suite, because the tests recompute
// their expected bases with the same expression the code uses — so a stale base is
// wrong identically on both sides and passes.
//
// Карен is absent on purpose: his introduction is already `stillLines[0]`, where
// the flat pool's index 0 has to stay.
var personaIntros = []string{
	"Я АНДРЮХА",
	"Я САНЯ",
	"Я ДАША",
}

var PlayerLines = slices.Concat(stillLines, movingLines, dashingLines, redirectLines, personaIntros)

// personaIntroBase is where personaIntros starts in the flat pool.
var personaIntroBase = len(stillLines) + len(movingLines) + len(dashingLines) + len(redirectLines)

// IntroLineFor is the index of the line this persona introduces itself with.
//
// Anything out of range answers Карен's, which is the safe direction: a frame that
// could not be read must not silently point at a different colleague's name.
func IntroLineFor(persona int) int {
	if persona <= 0 || persona > len(personaIntros) {
		return introLine
	}
	return personaIntroBase + persona - 1
}

// RedirectLine is the index of «ЭТО НУЖНО УТОЧНИТЬ У ДРУГОГО» in the flat pool.
//
// Picked by the OFFICE rather than by PlayerLine, because "I have just pointed
// him at somebody" is a fact about the office and not about the simulated
// player — putting it on Player would force a golden-vector regeneration and a
// client change for a value Step never reads.
var RedirectLine = len(stillLines) + len(movingLines) + len(dashingLines)

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

// The redirect verb — «ЭТО НУЖНО УТОЧНИТЬ У ДРУГОГО».
//
// The one thing a second Карен changes about the office, and the design's
// «перевод стрелок»: the лысый walks at the NEAREST person, and this overrides
// that for a few seconds regardless of who is nearer.
const (
	// RedirectSeconds is how long he stays pointed at somebody else. Long enough
	// to walk out of trouble, short enough that it is a reprieve rather than an
	// answer — he comes back, and he is nearer than he was.
	RedirectSeconds = 6.0
	// RedirectCooldown is the wait between uses, and it is the WHOLE price of
	// the verb today. The design charges +ПОДОЗРЕНИЕ as well, because it is loud
	// and Claude hears it — but that meter arrives with Claude in iteration 4,
	// so until then this is a free betrayal with a timer on it. Stated here
	// rather than discovered later.
	//
	// Eight seconds, owner-directed, down from twenty. Twenty meant that in a
	// two-minute shift you got it about six times and spent most of the shift
	// looking at a dead button; eight makes it something you are actually
	// deciding about while dodging, which is where this game wants your thumb.
	RedirectCooldown = 8.0
	// RedirectSaySeconds is how long the line stays over your head afterwards.
	// It is deliberately longer than one balloon slot: the whole point of the
	// verb is that your colleague sees who did it to him.
	RedirectSaySeconds = 3.0
)

// «Набухать лысого» — the bottle, and the one mechanic in this game that acts on
// HIM rather than on the space between you.
//
// That is exactly why it has a price. He is the only thing that ends a shift, so
// a few seconds of a harmless лысый is the strongest effect available, and it
// costs a walk to a fixed spot — which means leaving wherever you were standing,
// and therefore the streak, which is the whole currency of the game.
const (
	// (The bottle no longer has a fixed position — see BottleSpots.)
	// BottleReach is how close you have to be to pick it up. Generous, because
	// the streak is the price and fighting the collision resolver for the last
	// twenty centimetres is not.
	BottleReach = 0.9
	// DrunkSeconds is how long he wobbles. Long enough to walk somewhere better,
	// short enough that it is a reprieve and not a win.
	DrunkSeconds = 8.0
	// DrunkSpeed is what he is multiplied by while drunk. STAGGERING IS NOT
	// STOPPING: a boss who freezes is a boss you ignore, and the joke is that he
	// is delighted AND now also drunk. He is slower and he is still coming.
	DrunkSpeed = 0.45
	// DrunkWobble is how far off his heading he weaves, in radians.
	DrunkWobble = 1.1
	// DrunkWobbleHz is how fast the weave oscillates.
	DrunkWobbleHz = 1.7
	// BottleReturn is how long until another one appears, SOMEWHERE ELSE.
	//
	// Ten seconds, owner-directed, against the forty-five it shipped with. The
	// long version made it a once-a-shift event you either happened to be near or
	// never used; ten makes it a thing on the floor you keep an eye on. What
	// stops that being free is that it moves: the walk is the price, and the walk
	// is a different walk every time.
	BottleReturn = 10.0
)

// CLAUDE CODE — the second man who walks at you, and the only one whose arrival
// you survive.
//
// HIS SPEED IS THE ЛЫСЫЙ'S, EXACTLY, and that is the owner's instruction read
// carefully: «it should match bosses movement speed so that its not
// instalooseable». Both halves hold at 4.0. He matches the лысый literally, so he is
// evadable by exactly the margin the лысый already is; and the slow he leaves behind
// takes a walk from 6.4 to 5.12, which is still 28 % faster than either of them —
// so being caught costs you ground and time, and never the shift.
//
// The arithmetic that makes that a rule rather than a coincidence is in
// TestASlowedWalkStillOutrunsThem, and it is why the slow does NOT stack: two
// applications would leave 4.096 m/s against their 4.0, which is not an escape, and
// three would make the game unwinnable.
const (
	// ChaserSpeed is Claude's walk. See above: the лысый's, deliberately.
	ChaserSpeed = BossSpeed

	// ChaserRadius is the disc the resolver pushes out of furniture. The лысый's,
	// so `navFree`'s single cached grid is reused for free and the passage-width
	// rule that already holds for him holds for this one too.
	ChaserRadius = BossRadius

	// ChaserReach is how close he has to get to land on you. Shorter than the
	// лысый's catch radius, so the two of them converging on one person do not
	// arrive on the same tick.
	ChaserReach = 0.6

	// SlowFactor is what your walk is multiplied by after he lands, and
	// SlowSeconds is how long for. NON-STACKING — assigned, never multiplied,
	// exactly as the лысый's drink is — for the arithmetic in the block comment
	// above.
	SlowFactor  = 0.8
	SlowSeconds = 4.0

	// Spawns. The opposite corner from the лысый, so a shift does not open with
	// both of them in one place and a player pinned against the far wall.
	ChaserSpawnX = 2.0
	ChaserSpawnY = 20.0
)

// «Кальян» — the second prop on the floor, and the only thing in this game that
// makes you untouchable rather than making HIM slower.
//
// It is the mirror of the bottle and deliberately so: same reach, same
// walk-to-it-and-lose-your-streak price, a spot that moves so the walk is a
// different walk every time. What differs is who it acts on. The bottle is an
// attack on the лысый; this is a way to stop existing for ten seconds, which is
// long enough to cross the floor and stand somewhere better.
const (
	// HookahReach is how close you have to be. The bottle's number, for the same
	// reason: the streak is the price, and fighting the collision resolver for the
	// last twenty centimetres is not.
	HookahReach = 0.9
	// InvincibleSeconds is how long the cloud lasts. Owner-directed.
	//
	// Ten seconds against a boss who needs about four to cross the room is
	// generous on purpose — the walk to the hookah costs a full ramp, so the
	// reward has to be worth leaving a ×3 for.
	InvincibleSeconds = 10.0
	// HookahReturn is how long until another one appears, SOMEWHERE ELSE. Twice
	// the bottle's wait: being uncatchable is the strongest effect in the game, and
	// a shift should not be able to spend most of itself behind a cloud.
	HookahReturn = 20.0
)

// HookahSpots is every place one can appear; a frame names which by index.
//
// FOUR RULES, and content_test pins all of them. Each spot is clear of every desk
// by more than a player's radius, so walking to one is never a fight with the
// collision resolver. They are far enough apart that the draw genuinely moves the
// walk. And each is at least two metres from every BottleSpot — a cross-list rule
// the bottle never needed, because `drawBottleSpot` only avoids its own previous
// index, and two props on one tile would mean one walk collected both. And each is
// well clear of the лысый's own spawn — a spot beside him is bait rather than a
// reprieve, because a shift opens with the player at the far end of the room and
// walking to it means walking into the one thing that ends the shift. The first
// version put one at (8, 19.0), a metre and a half from where he starts, and the
// integration test that walks a real player to a real кальян died on it every time.
var HookahSpots = []Vec2{
	{X: 8.0, Y: 6.0},
	{X: 0.9, Y: 10.5},
	{X: 15.1, Y: 10.5},
	{X: 8.0, Y: 13.6},
}

// PlayerSlot is how long he holds one line before moving to the next, in ticks.
//
// THE SAME AS THE BALD MAN'S, at the owner's direction. It was twice his, on the
// reasoning that yours is the line you stare at while standing still and that
// changing it often would be movement on a screen whose point is that nothing
// moves. In practice the office read as frozen rather than as calm — the plane
// holds several figures now, and two of them holding a sentence for four seconds
// is a still photograph. One cadence, everywhere, two seconds.
const PlayerSlot = 40 // ticks, at SimHz — 2 s

// PlayerLine is which of PlayerLines belongs over a player right now.
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
func PlayerLine(p Player, persona int, tick uint64) int {
	intro := IntroLineFor(persona)
	switch {
	case p.DashLeft > 0:
		return pickLine(intro, len(stillLines)+len(movingLines), dashingLines, tick, PlayerSlot)
	case p.MoveGrace > 0:
		// He has moved inside the grace window, so the streak is at risk even
		// though it has not gone yet. That is exactly when it is worth saying.
		return pickLine(intro, len(stillLines), movingLines, tick, PlayerSlot)
	default:
		return pickLine(intro, 0, stillLines, tick, PlayerSlot)
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
func pickLine(intro, base int, run []string, tick uint64, slotTicks uint64) int {
	n := uint64(len(run))
	if n == 0 || slotTicks == 0 {
		return 0
	}
	slot := tick / slotTicks
	got := lineIn(intro, base, n, slot)
	if slot > 0 && got == lineIn(intro, base, n, slot-1) {
		// Say something else rather than the same thing twice running — a
		// balloon that does not change across a slot boundary reads as frozen,
		// which is the defect this whole cadence exists to avoid.
		return nextLine(intro, base, n, got)
	}
	return got
}

// lineIn is the unguarded pick for one slot: the introduction, or a line of the
// run.
func lineIn(intro, base int, n, slot uint64) int {
	if slotHash(slot, introSalt)%IntroEvery == 0 {
		return intro
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
func nextLine(intro, base int, n uint64, from int) int {
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
	return intro
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
// BossState is what has most recently happened to him, which decides WHICH run
// he is speaking from. It is not on the wire: the frame carries the resulting
// index, and the browser never learns the layout.
type BossState int

const (
	BossIdle BossState = iota
	BossDrunk
	BossRedirected
	// BossLost is «where did he go» — the man he was walking at is behind a cloud
	// of hookah smoke and is not a target any more.
	BossLost
)

// BossSays is which line belongs over him, given what has happened to him.
//
// The two event runs OUTRANK the rotation and change the instant the state does
// — see the note on bossDrunkLines. Inside a run it is the same hashed,
// out-of-order pick everything else on this plane uses.
func BossSays(state BossState, grin float64, tick uint64, mine bool) int {
	switch state {
	case BossDrunk:
		return pickLine(introLine, len(bossFar)+len(bossClosing), bossDrunkLines, tick, BossSlot)
	case BossRedirected:
		return pickLine(introLine, len(bossFar)+len(bossClosing)+len(bossDrunkLines), bossRedirectLines, tick, BossSlot)
	case BossLost:
		got := pickLine(introLine, bossLostBase, bossLostLines, tick, BossSlot)
		// THE TEMPLATED LINE GOES ONLY TO THE PERSON WHO VANISHED, because it is
		// the only client that can fill the name in — a persona is never sent for
		// anybody else, so on a colleague's screen `{}` would either render as
		// itself or be guessed at. Everybody else gets the next line in the run,
		// which says the same thing without naming anybody.
		if !mine && strings.Contains(BossLines[got], NamePlaceholder) {
			return nextLine(introLine, bossLostBase, uint64(len(bossLostLines)), got)
		}
		return got
	default:
		return BossLine(grin, tick)
	}
}

func BossLine(grin float64, tick uint64) int {
	// THE STATE PICKS THE RUN AND THE TICK PICKS THE LINE, which is exactly what
	// PlayerLine does — and the reason this is not simply `grin >= BossQuiet ? …`
	// any more is that HE USED TO SAY ONE SENTENCE FOREVER WHILE FAR AWAY. That
	// is most of a shift, and it left the balloon over the only other figure on
	// the plane frozen while everything else moved.
	if grin >= BossQuiet {
		return pickLine(introLine, len(bossFar), bossClosing, tick, BossSlot)
	}
	return pickLine(introLine, 0, bossFar, tick, BossSlot)
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
	GameKey   string       `json:"game_key"`
	Title     string       `json:"title"`
	Office    OfficeConfig `json:"office"`
	Money     MoneyConfig  `json:"money"`
	Move      MoveConfig   `json:"move"`
	Boss      BossConfig   `json:"boss"`
	Sim       SimConfig    `json:"sim"`
	Endings   []Ending     `json:"endings"`
	BossLines []string     `json:"boss_lines"`
	// Personas is who you might be, by index. The shift responses carry the index
	// and this carries the names, so retuning the cast is a backend deploy.
	Personas []string `json:"personas"`
	// ClaudeLines is Claude Code's pool. A separate served array rather than a run
	// inside boss_lines: he is a separate figure with a separate index on the
	// frame, and folding him in would make two figures share one index space for
	// no gain.
	ClaudeLines  []string     `json:"claude_lines"`
	Claude       ClaudeConfig `json:"claude"`
	PlayerLines  []string     `json:"player_lines"`
	MaxOccupants int          `json:"max_occupants"`
	Redirect     VerbConfig   `json:"redirect"`
	Bottle       BottleConfig `json:"bottle"`
	Hookah       HookahConfig `json:"hookah"`
}

// ClaudeConfig is Claude Code, published so the cheatsheet can state what he costs
// without typing a number and the client can draw his reach.
type ClaudeConfig struct {
	Speed float64 `json:"speed"`
	Reach float64 `json:"reach"`
	// SlowPct is how much of your walk he leaves you, as a percentage — the
	// bottle's `slow_pct` convention, so the cheatsheet formats both the same way.
	SlowPct int `json:"slow_pct"`
	SlowMs  int `json:"slow_ms"`
}

// HookahConfig is «кальян», published so the plane can draw it where it actually
// stands and the cheatsheet can state the rule without typing a number.
type HookahConfig struct {
	// Spots is every place one can appear; a frame names which by index.
	Spots []Vec2 `json:"spots"`
	// Reach is how close you have to get.
	Reach float64 `json:"reach"`
	// InvincibleMs is how long the cloud lasts.
	InvincibleMs int `json:"invincible_ms"`
	// ReturnMs is how long until another one appears somewhere else.
	ReturnMs int `json:"return_ms"`
}

// BottleConfig is «набухать лысого», published so the plane can draw the bottle
// where it actually stands and the cheatsheet can state the rule without typing
// a number.
type BottleConfig struct {
	// Spots is every place one can appear; a frame names which by index.
	Spots    []Vec2  `json:"spots"`
	Reach    float64 `json:"reach"`
	DrunkMs  int     `json:"drunk_ms"`
	ReturnMs int     `json:"return_ms"`
	SlowPct  int     `json:"slow_pct"`
}

// VerbConfig publishes a verb: what it does and what it costs, so the control
// and the splash cheatsheet are both generated rather than typed.
//
// It withholds nothing, because there is nothing here a client could abuse: the
// office judges every use, and a client that lied about its own cooldown would
// simply have its frame refused.
type VerbConfig struct {
	// Label is what the control says.
	Label string `json:"label"`
	// Say is the line that appears over your head, so the cheatsheet can quote
	// it without the client hardcoding a string from the pool.
	Say string `json:"say"`
	// SecondsMs is how long he stays pointed at somebody else; CooldownMs is the
	// wait between uses.
	SecondsMs  int `json:"seconds_ms"`
	CooldownMs int `json:"cooldown_ms"`
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
		PlayerLines:  append([]string(nil), PlayerLines...),
		MaxOccupants: MaxOccupants,
		ClaudeLines:  slices.Clone(ClaudeLines),
		Claude: ClaudeConfig{
			Speed:   ChaserSpeed,
			Reach:   ChaserReach,
			SlowPct: int(math.Round(SlowFactor * 100)),
			SlowMs:  int(SlowSeconds * 1000),
		},
		Hookah: HookahConfig{
			Spots:        slices.Clone(HookahSpots),
			Reach:        HookahReach,
			InvincibleMs: int(InvincibleSeconds * 1000),
			ReturnMs:     int(HookahReturn * 1000),
		},
		Bottle: BottleConfig{
			Spots:    append([]Vec2(nil), BottleSpots...),
			Reach:    BottleReach,
			DrunkMs:  int(DrunkSeconds * 1000),
			ReturnMs: int(BottleReturn * 1000),
			SlowPct:  int(DrunkSpeed * 100),
		},
		Redirect: VerbConfig{
			Label:      "ЭТО К НЕМУ",
			Say:        redirectLines[0],
			SecondsMs:  int(RedirectSeconds * 1000),
			CooldownMs: int(RedirectCooldown * 1000),
		},
	}
}
