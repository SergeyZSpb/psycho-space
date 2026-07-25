package gamevanyagotchi

import "testing"

// The catalogue is data, and data has no compiler.
//
// Every property in this file is one that a typo in content.go breaks in
// production rather than at build time: a duplicated key, an action pointing at
// a stat that was renamed, a default skin nothing resolves to, a fatal stat that
// cannot actually kill. The whole point of the catalogue is that content changes
// without a client deploy and without a migration — which means these checks are
// the only gate a content change passes through at all.

// TestTheCatalogueIsPopulated is first because everything below it would pass
// vacuously against an empty catalogue: a range over nothing asserts nothing,
// and the suite would go green describing a game with no content in it.
func TestTheCatalogueIsPopulated(t *testing.T) {
	c := Content()
	for _, group := range []struct {
		what string
		n    int
	}{
		{"stats", len(c.Stats)},
		{"actions", len(c.Actions)},
		{"skins", len(c.Skins)},
		{"locations", len(c.Locations)},
	} {
		if group.n == 0 {
			t.Errorf("the catalogue has no %s", group.what)
		}
	}
	if c.GameKey != GameKey {
		t.Errorf("catalogue game key = %q; want %q — the shared asset store is keyed on it", c.GameKey, GameKey)
	}
}

// TestEveryKindOfKeyIsUnique guards the lookups. StatByKey and ActionByKey
// return the FIRST match, so a duplicated key does not fail loudly: it makes the
// second entry unreachable content, and — worse for an action — can silently
// resolve a verb onto the wrong stat.
func TestEveryKindOfKeyIsUnique(t *testing.T) {
	c := Content()
	statKeys := make([]string, 0, len(c.Stats))
	for _, s := range c.Stats {
		statKeys = append(statKeys, s.Key)
	}
	actionKeys := make([]string, 0, len(c.Actions))
	for _, a := range c.Actions {
		actionKeys = append(actionKeys, a.Key)
	}
	skinKeys := make([]string, 0, len(c.Skins))
	for _, s := range c.Skins {
		skinKeys = append(skinKeys, s.Key)
	}
	locationKeys := make([]string, 0, len(c.Locations))
	for _, l := range c.Locations {
		locationKeys = append(locationKeys, l.Key)
	}

	for _, group := range []struct {
		what string
		keys []string
	}{
		{"stat", statKeys},
		{"action", actionKeys},
		{"skin", skinKeys},
		{"location", locationKeys},
	} {
		seen := make(map[string]bool, len(group.keys))
		for _, k := range group.keys {
			if k == "" {
				t.Errorf("a %s has an empty key; nothing can address it", group.what)
			}
			if seen[k] {
				t.Errorf("%s key %q appears twice; the second entry is unreachable and a lookup may resolve onto the wrong one", group.what, k)
			}
			seen[k] = true
		}
	}
}

// TestEveryStatHasAUsableRange checks the three numbers a bar is drawn from. A
// stat whose start sits outside its own bounds is created already clamped —
// which for a fatal stat is the difference between a new pet and a dead one —
// and a warning threshold outside them is a warning that is either always or
// never showing.
func TestEveryStatHasAUsableRange(t *testing.T) {
	for _, s := range Content().Stats {
		t.Run(s.Key, func(t *testing.T) {
			if s.Min >= s.Max {
				t.Fatalf("min %v is not below max %v; the bar has no width and the decay has nowhere to go", s.Min, s.Max)
			}
			if s.Start < s.Min || s.Start > s.Max {
				t.Errorf("start %v is outside [%v, %v]; a new pet is created already clamped", s.Start, s.Min, s.Max)
			}
			if s.WarnAt < s.Min || s.WarnAt > s.Max {
				t.Errorf("warn_at %v is outside [%v, %v]; the warning is either always or never on", s.WarnAt, s.Min, s.Max)
			}
		})
	}
}

// TestEveryActionMovesAStatThatExists is the catalogue agreeing with itself.
// An action naming a stat that has been renamed or removed is a button that
// appears to work: Act answers ErrUnknownStat, which is a 500 rather than
// anything the player did wrong.
func TestEveryActionMovesAStatThatExists(t *testing.T) {
	for _, a := range Content().Actions {
		t.Run(a.Key, func(t *testing.T) {
			s, ok := StatByKey(a.StatKey)
			if !ok {
				t.Fatalf("action moves stat %q, which is not in the catalogue", a.StatKey)
			}
			if got := s.Clamp(s.Start + a.Delta); got < s.Min || got > s.Max {
				t.Fatalf("applying delta %v to a fresh %q lands at %v, outside [%v, %v]", a.Delta, s.Key, got, s.Min, s.Max)
			}
		})
	}
}

// TestTheDefaultsAPetIsCreatedWithResolve covers the two keys every pet is
// stamped with on creation. A default that resolves to nothing is a pet the
// client cannot draw and a location the plane cannot place — for every account,
// from its very first read.
func TestTheDefaultsAPetIsCreatedWithResolve(t *testing.T) {
	c := Content()

	skin := false
	for _, s := range c.Skins {
		if s.Key == c.DefaultSkin {
			skin = true
		}
	}
	if !skin {
		t.Errorf("default skin %q is in no skin in the catalogue", c.DefaultSkin)
	}

	location := false
	for _, l := range c.Locations {
		if l.Key == c.DefaultLocation {
			location = true
		}
	}
	if !location {
		t.Errorf("default location %q is in no location in the catalogue", c.DefaultLocation)
	}
}

// TestExactlyOneStatIsFatalAndItActuallyDrains pins the design's statement that
// there is one timer at the centre of the game.
//
// The service supports several — it records the EARLIEST derived death among
// them — so a second fatal stat is a decision rather than a bug, and this test
// failing is how that decision gets made deliberately instead of by editing a
// bool. The rate check is the sharper half: a fatal stat that does not drain can
// never reach its floor, so the death loop would be content that exists and
// never fires.
func TestExactlyOneStatIsFatalAndItActuallyDrains(t *testing.T) {
	var fatal []Stat
	for _, s := range Content().Stats {
		if s.Fatal {
			fatal = append(fatal, s)
		}
	}
	if len(fatal) != 1 {
		t.Fatalf("%d fatal stats; the design says exactly one — adding another is a deliberate change, not a flag flip", len(fatal))
	}
	if fatal[0].DecayPerHour <= 0 {
		t.Fatalf("the fatal stat %q moves at %v/hour, so it can never reach its floor and nothing can ever kill him",
			fatal[0].Key, fatal[0].DecayPerHour)
	}
}

// TestSomeActionCanUndoADeath is the shipped-to-production check. Death in this
// game is deliberately recoverable — an irreversible loss in a fifteen-person
// friend group is how a player leaves permanently — so a catalogue in which no
// action revives is a dead end that would reach a player before anyone noticed.
func TestSomeActionCanUndoADeath(t *testing.T) {
	for _, a := range Content().Actions {
		if a.RevivesFatal {
			return
		}
	}
	t.Fatal("no action sets revives_fatal: a dead pet would be unrecoverable and the account would be finished with the game")
}

// TestContentHandsOutACopy is the defect the first game learned by having it.
// The config handler decorates what Content returns — filling in art URLs for
// skins that have an uploaded blob — so a shared backing array would let one
// request write through into the package's own catalogue and leave every later
// request looking at a mutated one, for the life of the process.
func TestContentHandsOutACopy(t *testing.T) {
	// Captured as plain values rather than kept as a second Config. A Config
	// held across the mutation would be a VIEW of whatever array Content handed
	// out — so if that array were the catalogue's own, the "before" copy would
	// report the mutation too and the comparisons below would pass while the
	// catalogue was being corrupted underneath them.
	statLabel := Content().Stats[0].Label
	actionLabel := Content().Actions[0].Label
	skinImage := Content().Skins[0].Image
	locationLabel := Content().Locations[0].Label

	c := Content()
	c.Stats[0].Label = "mutated"
	c.Actions[0].Label = "mutated"
	c.Skins[0].Image = "mutated"
	c.Locations[0].Label = "mutated"

	after := Content()
	if after.Stats[0].Label != statLabel {
		t.Errorf("stat label is now %q; a caller's edit reached the catalogue", after.Stats[0].Label)
	}
	if after.Actions[0].Label != actionLabel {
		t.Errorf("action label is now %q; a caller's edit reached the catalogue", after.Actions[0].Label)
	}
	if after.Skins[0].Image != skinImage {
		t.Errorf("skin image is now %q; a caller's edit reached the catalogue", after.Skins[0].Image)
	}
	if after.Locations[0].Label != locationLabel {
		t.Errorf("location label is now %q; a caller's edit reached the catalogue", after.Locations[0].Label)
	}

	// Stats() is the same handout by a shorter route and needs the same care.
	s := Stats()
	s[0].Key = "mutated"
	if Stats()[0].Key == "mutated" {
		t.Error("Stats() shares its backing array with the catalogue")
	}
}

// TestALookupFindsWhatIsThereAndMissesWhatIsNot pins both directions on
// purpose. A lookup that always missed would pass every "unknown key is
// rejected" assertion in the suite while making the whole catalogue
// unreachable, so the positive half is what stops this test being green and
// worthless.
func TestALookupFindsWhatIsThereAndMissesWhatIsNot(t *testing.T) {
	if s, ok := StatByKey(StatHP); !ok || s.Key != StatHP {
		t.Fatalf("StatByKey(%q) = (%+v, %v); want the catalogue's own entry", StatHP, s, ok)
	}
	if a, ok := ActionByKey(ActionHeal); !ok || a.Key != ActionHeal {
		t.Fatalf("ActionByKey(%q) = (%+v, %v); want the catalogue's own entry", ActionHeal, a, ok)
	}

	// A client asking for one of these is either stale or probing; both have to
	// be a clean miss rather than a zero-valued entry that looks real. The
	// near-misses are the interesting rows — a lookup that trimmed or folded case
	// would resolve a key the config never published.
	for _, key := range []string{"", "no-such-key", " " + StatHP, "HP", ActionHeal} {
		if s, ok := StatByKey(key); ok {
			t.Errorf("StatByKey(%q) resolved to %+v; want a miss", key, s)
		} else if s != (Stat{}) {
			t.Errorf("StatByKey(%q) missed but returned %+v; want the zero value", key, s)
		}
	}
	for _, key := range []string{"", "no-such-key", " " + ActionHeal, "HEAL", StatHP} {
		if a, ok := ActionByKey(key); ok {
			t.Errorf("ActionByKey(%q) resolved to %+v; want a miss", key, a)
		} else if a != (Action{}) {
			t.Errorf("ActionByKey(%q) missed but returned %+v; want the zero value", key, a)
		}
	}
}
