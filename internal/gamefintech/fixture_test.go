package gamefintech

import (
	"sync"
	"time"
)

// THE OFFICE THE TESTS ARE WRITTEN AGAINST.
//
// The floor is data now, so a test that wants furniture has to say which
// furniture. Almost all of them want the one this game shipped on — the two
// columns of four with a clear central lane — because that is the office every
// distance, every walk and every chase in this file was tuned against, and
// re-tuning them against a floor that varies would be measuring the fixture
// rather than the code.
//
// So: the STARTING layout is the fixture. It is production content today and it
// stops being production content the day the generator lands; when that happens
// this file inherits it verbatim, and nothing else in the suite moves.
//
// What deliberately does NOT use it is the generator's own sweep, which builds
// its own floors, and the golden vectors, which pin the resolver against a
// hand-made torture fixture rather than against any office (see golden_test.go).
var (
	testLayout = StartingLayout.WithID()
	testRects  = testLayout.Rects()

	// testFloor is that layout as the boot path hands it over: the geometry plus
	// the provenance the admin section reads.
	testFloor = StoredLayout{Layout: testLayout, Source: SourceStarting, InstalledAt: testInstalledAt}

	// testPlan is built once for the whole package: a plan owns a 1408-cell
	// navigation grid, and rebuilding one per call turned a table test into a
	// flood fill per row.
	testPlan = sync.OnceValue(func() *Plan { return NewPlan(testLayout) })
)

// testInstalledAt is when the fixture floor was «installed». A fixed instant
// rather than time.Now(), so a test that renders it renders the same string on
// every run.
var testInstalledAt = time.Date(2026, 8, 4, 18, 0, 0, 0, time.UTC)

// newTestOffice opens an office on the fixture floor.
func newTestOffice() *Office { return NewOffice(testPlan()) }

// emptyPlan is a floor with nothing on it, for the tests whose subject is a body
// against a wall rather than a body against furniture.
var emptyPlan = sync.OnceValue(func() *Plan { return NewPlan(Layout{}.WithID()) })
