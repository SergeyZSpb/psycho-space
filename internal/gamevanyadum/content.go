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

// BetrayalsTitle and KillsTitle are what the two standings columns are called,
// in Russian.
//
// THE PAIR IS THE JOKE, and it only became one when the нейрослопы arrived. A
// слоп is worth a kill; a friend is worth nothing at all and is published under
// his own heading, so the board says in two columns what the game thinks of you.
// Before there was anything here but friends there was only the confession.
//
// Both are published in the catalogue rather than typed into the client for the
// reason every other word here is: the splash screen's rules cheatsheet is
// generated from what is served, so this is the one place either word is written
// down.
const (
	BetrayalsTitle = "предательства"
	KillsTitle     = "слопы"
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
	// Heals is how much health the thing puts back and InjectSeconds is how long
	// it takes to put it back. Zero on both means the thing is not medicine,
	// which is every kind but the шприц.
	//
	// A PAIR AND NOT A SINGLE NUMBER, because the time is the whole point: an
	// instant heal is a number going up, where a heal with a duration is a
	// decision about where you are standing (see SyringeSeconds). They are two
	// fields rather than one because either could be retuned without the other,
	// and both are served so the splash screen's cheatsheet is generated from
	// them rather than typed out beside them.
	//
	// GRANTS IS EMPTY FOR MEDICINE, and that is not an omission. A bottle fills a
	// counter in the bag and the HUD draws it; an ampoule is used the instant it
	// is walked over and never carried, so there is nothing to put in a bag and
	// nothing for the HUD to count. The two fields below are what a kind that is
	// used rather than kept says about itself.
	Heals         int     `json:"heals,omitempty"`
	InjectSeconds float64 `json:"inject_seconds,omitempty"`
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

// The шприц: the second thing the building scatters, and the first thing in it
// that cannot be used instantly.
//
// IT IS USED BY WALKING OVER IT, exactly as the bottle is, and for exactly the
// reason stated on PickupKind: there is no use button in this game and there is
// not going to be one. So the decision an ampoule asks for is not "when do I
// press it" but "when do I walk onto it" — and a man at full health leaves it
// lying there (world.go, collect), because taking something you cannot use is
// how a resource is wasted by accident. That turns the thing into a LANDMARK:
// you see where it is, you take the beating somewhere else, and you come back
// for it. Which is the same loop the beer already asks for, bought a second time
// with no new control on a touchscreen.
//
// AND WALKING ONTO IT COMMITS YOU, which is the whole iteration. For
// SyringeSeconds the man cannot walk and cannot shoot: the hand comes up, the
// needle goes into the forearm, the plunger goes down, and the health arrives
// while it empties rather than the instant it is touched. The kind rule — heal
// on contact, keep walking — costs nothing and is therefore worth nothing to
// decide about. This one is a decision every time, because the cost of being
// wrong is standing perfectly still in a заброшка with two нейрослопы in it.
//
// ONLY BEING HURT STOPS IT, and everything else is refused rather than
// interrupting: the trigger is refused while it runs and so is every step
// (sim.go, Step and stepGun), so there is nothing left for the player himself to
// interrupt it WITH. That is the harsher of the two available rules and it is
// chosen deliberately — the kind one lets a panicking man cancel and run, which
// makes the window free and therefore not a window at all.
//
// AN INTERRUPTED AMPOULE IS GONE, AND WHAT WENT IN STAYS IN. The health already
// delivered is his, because it is already in him and taking it back would be the
// simulation undoing something the player watched happen; the remainder is lost
// with the ampoule, which is lying on the floor of the room he took it from and
// coming back in PickupRespawn like everything else. So being caught halfway
// costs the rest of the heal AND the walk back, and that is the price that makes
// picking one up a decision rather than a reflex.
const (
	// SyringeHeal is how much health one ampoule holds, and it is EXACTLY
	// BarrelDamage.
	//
	// One ampoule undoes one barrel, which is the shortest true sentence the
	// cheatsheet can print about it and the only relationship anybody has to
	// remember. It is half of MaxHealth, so a man shot once is whole again and a
	// man shot twice is on the floor and never gets to use it — the ampoule
	// answers the first mistake and never the second, which is what keeps a
	// firefight decided by the second barrel rather than by who is standing
	// nearer the medicine.
	//
	// IT IS NOT A TRADE FOR THE MAN WHO SHOT YOU. He spends a barrel and a
	// cadence to take 50 off you; you spend SyringeSeconds of complete
	// helplessness to put it back, which is longer than the reload that gives him
	// both barrels again. So healing under fire is a losing move by construction,
	// and the ampoule is for BETWEEN fights — which is the behaviour worth
	// rewarding in a building nobody can leave.
	SyringeHeal = 50

	// SyringeSeconds is how long the injection takes, and the number is chosen
	// against the two other waits this game already has rather than in the
	// abstract: it is longer than ReloadSeconds and shorter than DownTime.
	//
	// LONGER THAN A RELOAD, so it is never the cheaper thing to do when somebody
	// is in the room — a reload gets you both barrels back in 1.5 s and the
	// ampoule gets you nothing you can shoot with in 2.5. SHORTER THAN A DEATH,
	// so the worst case of misjudging it (caught halfway, killed) costs about the
	// same again on top, and the two together are not so long that a phone screen
	// is showing a man doing nothing for the length of a loading screen.
	//
	// IN METRES IT IS EIGHT OF THEM AT SlopSpeed: a нейрослоп anywhere in the
	// room with you arrives before the plunger is down. That is the number to
	// read this constant by — "am I far enough from it" is the question the
	// injection asks, and this is the answer in the only unit that matters.
	SyringeSeconds = 2.5
)

// The нейрослоп: the first thing in this заброшка that is not a friend.
//
// It walks at the nearest man, room by room, and hurts whoever it reaches. It
// has no gun, no plan and no line of sight beyond "where is he" — which is the
// whole joke, and is also why the numbers below are the entire creature.
const (
	// SlopTitle is what it is called, in Russian. Published so the cheatsheet
	// names it without the word being typed on the client.
	SlopTitle = "нейрослоп"

	// SlopBlurb is one line of Russian about what it is for. Prose rather than a
	// derived line, and NUMBER-FREE for the same reason a pickup's blurb is: the
	// cheatsheet generates the numbers from what is served, so a constant spelled
	// out here would go stale beside a generated line that had it right.
	SlopBlurb = "Ходит на тебя. Ничего не говорит, потому что сказать ему нечего."

	// SlopSpeed is metres per second, and it is deliberately BELOW WalkSpeed.
	// Running away has to be an answer that always works — the заброшка is small,
	// there is one spawn, and a creature you cannot outrun in a building you
	// cannot leave is not a game. Two thirds of a walk is slow enough to escape
	// and fast enough that ignoring one costs you the room you were standing in.
	SlopSpeed = 3.2

	// SlopHealth is what one is worth, and it is EXACTLY BarrelDamage because
	// nothing on the wire could say otherwise.
	//
	// A слоп is drawn from the position on a snapshot and nothing else — no
	// health, no state field, not a byte beyond where it is (message.go, Foe). So
	// the only acknowledgement a hit can have is the creature ceasing to be on
	// the frame, and that is the truth only while one barrel is the whole of it.
	// Raise this above BarrelDamage and shooting a слоп becomes an action with no
	// acknowledgement at all, which this project treats as an unfinished action —
	// and the field that would fix it costs a place in the building
	// (MaxOccupants). Pinned by
	// TestASlopDiesToOneBarrelBecauseNothingOnTheWireCouldSayOtherwise.
	SlopHealth = BarrelDamage

	// SlopDamage is what touching one costs, and SlopTouchInterval is how often
	// the same слоп may charge it.
	//
	// FOUR TOUCHES IS A DEATH, spread over three seconds of standing in one. The
	// cooldown is the whole of what makes contact survivable: without it a
	// creature that overlaps you is twenty hits a second, so walking into one
	// would be indistinguishable from walking into a wall that kills you. With
	// it, being caught is a warning first and a death fourth — long enough to
	// fire two barrels, which is exactly what the gun holds.
	//
	// TWO OF THEM HALVE IT, deliberately: the interval is per слоп rather than
	// per victim, so being cornered by the pair is twice as expensive as being
	// caught by one. That is what makes the second one worth shooting first.
	SlopDamage        = 25
	SlopTouchInterval = time.Second

	// SlopReach is how close it has to be to hurt you: the two discs touching.
	//
	// A слоп is THE SAME DISC AS A MAN — PlayerRadius, walked with the player's
	// own collision resolver and shot with the player's own body model. One
	// radius, one meaning, exactly as BodyHeight argues for the hit test: what
	// fits through a doorway is what can be shot through it, and now also what
	// can follow you through it.
	//
	// IT IS NOT WHAT KEEPS ONE OUT OF THE NEXT ROOM, and it must not become that
	// again. Two discs flush against opposite sides of a shared wall are pushed
	// PlayerRadius clear of it each (sim.go, pushOut), so they stand exactly this
	// distance apart — and a reach test on its own therefore calls that a touch,
	// through concrete. What refuses it is the wall sweep contact damage now runs
	// (slop.go, touches), which is the same sweep that stops a barrel. So this
	// number is free to be retuned for how it FEELS: raise it and a слоп reaches
	// further into the room it is standing in, and no further into the one next
	// door.
	//
	// IT IS ALSO HOW FAR APART TWO СЛОПЫ ARE KEPT (slop.go, separate), and that is
	// the same quantity rather than a second constant borrowed for a second job:
	// two discs of PlayerRadius stop overlapping at exactly this distance,
	// whichever pair of them it is. Two numbers here would be two numbers to keep
	// equal by hand, and the day they diverged one слоп would be standing inside
	// the reach of another.
	SlopReach = 2 * PlayerRadius

	// SlopPopulation is how many нейрослопы the building holds.
	//
	// IT IS A WIRE BUDGET AND NOT A DIFFICULTY KNOB. Everything visible is on the
	// frame and everything in this building can become visible at once — they all
	// walk at the same man, so they converge on him by construction — which makes
	// the population the worst case rather than a typical one. Two is what is left
	// of the 8 kB/s ceiling once three people are in the room; see MaxOccupants
	// for the arithmetic and for what raising either would cost.
	//
	// It is also why there is no second constant for "how many may be drawn": a
	// cap on the frame below the population would be a creature that can hurt you
	// without being drawn, which is the one thing this game's visibility rules
	// exist to forbid.
	SlopPopulation = 2

	// SlopSpawnInterval is how long the building waits between нейрослопы.
	//
	// ONE AT A TIME, AND THE FIRST ONE IS NOT FREE: the deadline starts full, so a
	// заброшка somebody has just walked into is empty for this long. Nothing
	// materialises on the tick a socket says hello — the client is still building
	// its meshes, and being killed by something you have not been drawn yet is the
	// worst first impression a game can make.
	//
	// It is also the whole replacement rate. Kill both and the building is quiet
	// for this long, then one arrives, then the other: clearing a room buys time
	// rather than ending anything, which is the only shape available in a match
	// that does not end.
	SlopSpawnInterval = 8 * time.Second
)

// slopTouchTicks and slopSpawnTicks are those intervals as whole numbers of
// simulation steps, which is the unit the world keeps both deadlines in. Derived
// rather than typed out, so they can never disagree with the seconds above.
const (
	slopTouchTicks = int64(SlopTouchInterval / SimStep)
	slopSpawnTicks = int64(SlopSpawnInterval / SimStep)
)

// doorwayStep is how far past a doorway a слоп aims when it is walking into the
// next room.
//
// IT AIMS PAST THE OPENING AND NOT AT IT, and that is a correctness rule rather
// than a nicety. A portal lies ON the boundary the two rooms share, and a shared
// boundary belongs to the lower-numbered room (level.go, SectorAt) — so a слоп
// that arrived exactly at the middle of a doorway would still be in the room it
// came from, would be told to head for the same point it is already standing on,
// and would stand in the doorway for ever. Aiming a body's width into the room
// beyond it is the smallest offset that cannot round back.
const doorwayStep = PlayerRadius

// MaxOccupants is how many people may be in the заброшка at once.
//
// THREE IS A MEASURED LIMIT AND NOT A PREFERENCE. What a viewer is sent is three
// things: a snapshot built for them twenty times a second which carries everybody
// else AND every нейрослоп they can see, the standings frame once a second which
// carries everybody including them, and the events he is handed as he collects
// things. The ceiling is 8 kB/s PER VIEWER — the number this game's design named
// as the point at which interest management stops being optional — and the
// capacity is simply the largest N whose total fits under it once the слопы have
// been paid for.
//
// THE MEASUREMENT, at the widest quantisation the wire can carry (yaw is wrapped
// rather than normalised, so it reaches five characters; positions are far beyond
// anything a generated level produces):
//
//	solo snapshot             180 bytes
//	the first peer            +63  (the entry, plus the `p` array around it)
//	each further peer         +57
//	the first слоп            +44  (the entry, plus the `f` array around it)
//	each further слоп         +38
//	standings with one row    115
//	each further row          +87
//	events, sustained         39 B/s (the densest heap a level can hold, once
//	                          per PickupRespawn each — it does not grow with the
//	                          building, since a man collects only what he walks on)
//
// So a viewer pays 20 × the snapshot + the standings + the events, and the whole
// grid of answers is this:
//
//	3 people, 0 слопы    300 × 20 + 289 + 39 = 6328 B/s
//	3 people, 2 слопы    382 × 20 + 289 + 39 = 7968 B/s   ← what ships
//	3 people, 3 слопы    420 × 20 + 289 + 39 = 8728 B/s — over
//	4 people, 0 слопы    357 × 20 + 376 + 39 = 7555 B/s
//	4 people, 1 слоп     401 × 20 + 376 + 39 = 8435 B/s — over
//
// Three people and two слопы, with 32 B/s of headroom. Pinned by
// TestEverythingAFullBuildingSendsAViewerFitsTheCeiling, which fails when either
// constant is raised or when any of the three frames grows a field.
//
// IT WAS FOUR, AND THE НЕЙРОСЛОПЫ COST THE FOURTH PLACE. Read the grid again and
// the crunch is stark: FOUR PEOPLE AND ONE СЛОП DO NOT FIT — the building is over
// the ceiling before the antagonist has arrived at all. So this is not a case of
// trimming a field until it fits. What went, and what it bought:
//
//   - THE FOURTH OCCUPANT, −57 bytes a snapshot and −87 a standings row: 1227 B/s,
//     which is what the слопы are bought with.
//   - THE KILL COLUMN, +11 bytes a standings row at six figures, on a frame that
//     goes out once a second. It is not optional: a kill counter that is not
//     published is a kill counter nobody has.
//
// AND A СЛОП IS ALREADY THE CHEAPEST ENTITY THIS WIRE CAN CARRY — 37 bytes against
// a peer's 56, because it has no yaw (its facing IS its direction of travel, which
// two consecutive frames give for free) and no state field (it never fires, never
// falls and is never protected). See message.go, Foe, for what each omission cost
// and why it was available. There is no third field to take off it: what is left
// is an id, two coordinates, a room, and punctuation.
//
// THE POPULATION IS THE WORST CASE AND NOT A TYPICAL ONE. Every слоп walks at the
// nearest man, so they converge on him by construction — a filter that helps when
// they are spread out helps least at exactly the moment they are all in the room
// with you. The same argument covers the people: the worst case is everybody
// standing in one room, where interest management removes nothing at all
// (level.go, buildVisibility). A capacity derived from the typical frame is a
// capacity that fails the first time the game gets interesting.
//
// WHAT THE ALTERNATIVES WERE, since a place in a building is not a small thing to
// spend. A cap on the frame BELOW the population — drawing only the nearest two of
// three слопы — buys a place and creates a creature that can hurt you without
// being drawn, which is the one thing this game's visibility rules exist to forbid
// (Snapshot.Peers, targetsFor). Raising the ceiling is not available: it is a
// phone on RU mobile data, and the number is the design's own trigger. So the
// honest choices were a smaller building or the binary codec, and this is the
// smaller building.
//
// 32 B/s IS A BYTE AND A HALF OF A SNAPSHOT, AND THAT IS THE FINDING. JSON is
// exhausted here: there is no further byte worth trimming — a peer is six integers
// behind keys of one to three characters and a слоп is four, and the rest is
// punctuation — so the NEXT field of any size at all, on any of the three frames,
// costs the third occupant. Getting a place back, or a third слоп, needs the
// binary codec the design doc earmarked as an iteration of its own. That is not a
// thing to discover from somebody's data allowance; it is written here, and the
// test above is what makes it impossible to walk past.
//
// A refusal is a refusal and never a queue: somebody who arrives at a full
// заброшка is told so (the `vanyadum_full` frame) rather than being put on hold,
// because there is nothing to hold them for — nothing here ends.
const MaxOccupants = 3

// Pickups is every kind that can be generated into a level.
//
// THE GENERATOR SCATTERS ONE OF EVERY KIND BEFORE IT SCATTERS A SECOND OF
// ANYTHING (level.go, placePickups), so the order here is not cosmetic: a
// building generated with no ammunition is a gun that cannot be reloaded and one
// generated with no medicine is this whole iteration invisible, and a uniform
// draw over two kinds produces the first about one building in eight.
//
// THE KEYS ARE ASCII AND SHORT, and that is a wire decision rather than a
// stylistic one: a key rides the pickup event (message.go, Event), and the
// budget prices that array at the widest key the catalogue holds
// (message_test.go, worstEventCost). The Russian is in Title, which is served
// once and never repeats.
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
	{
		Key:           "med",
		Title:         "шприц",
		Icon:          "💉",
		Heals:         SyringeHeal,
		InjectSeconds: SyringeSeconds,
		Tint:          "#9fd6c8",
		Blurb:         "Чинит. Медленно, стоя на месте и с открытым ртом — так что выбирай, где именно тебе не страшно.",
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
	Slop     SlopConfig   `json:"slop"`
	Pickups  []PickupKind `json:"pickups"`
	Surfaces []Surface    `json:"surfaces"`
	Sim      SimConfig    `json:"sim"`
	World    WorldConfig  `json:"world"`
}

// SlopConfig is everything about the нейрослоп a player has to be told before he
// walks in, and it is served for the reason the gun's numbers are: the splash
// screen's rules cheatsheet is GENERATED from this, so retuning a constant
// changes what the player is told with no client change and no chance of the two
// drifting apart.
//
// Barrels is not here and is deliberately absent. How many shots a слоп takes is
// Health against the gun's own Damage, both already published, and the cheatsheet
// divides — a third number saying the same thing is a third number to keep in
// step by hand.
type SlopConfig struct {
	Title string `json:"title"`
	Blurb string `json:"blurb"`
	// Population is how many are in the building at once, Health is what one is
	// worth against GunConfig.Damage, Damage is what being reached costs, and
	// TouchSeconds is how often the same слоп may charge it.
	Population   int     `json:"population"`
	Health       int     `json:"health"`
	Damage       int     `json:"damage"`
	TouchSeconds float64 `json:"touch_seconds"`
	// Speed is metres per second, published against PlayerConfig.WalkSpeed so the
	// cheatsheet can say the one thing that actually matters about it: you are
	// faster, so you can always leave.
	Speed float64 `json:"speed"`
	// SpawnSeconds is how long the building waits between them, which is both the
	// quiet a new arrival gets and the replacement rate after a kill.
	SpawnSeconds float64 `json:"spawn_seconds"`
	// KillsTitle is what to call the standings column that counts them. See the
	// constant for why it and BetrayalsTitle are a pair.
	KillsTitle string `json:"kills_title"`
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
		Slop: SlopConfig{
			Title:        SlopTitle,
			Blurb:        SlopBlurb,
			Population:   SlopPopulation,
			Health:       SlopHealth,
			Damage:       SlopDamage,
			TouchSeconds: SlopTouchInterval.Seconds(),
			Speed:        SlopSpeed,
			SpawnSeconds: SlopSpawnInterval.Seconds(),
			KillsTitle:   KillsTitle,
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
