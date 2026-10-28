package account

import (
	"os"
	"regexp"
	"testing"
)

// TestConsentVersionMatchesTheSPA pins the two copies of the consent edition
// together. The SPA carries the edition beside the text it shows and sends it
// back at login; the server refuses anything but its own (ADR-008). Both ship in
// one binary, so a disagreement is not a skew that resolves itself after a
// deploy — it is a build in which every login answers 409.
func TestConsentVersionMatchesTheSPA(t *testing.T) {
	src, err := os.ReadFile("../../web/src/constants.ts")
	if err != nil {
		t.Fatalf("read the SPA constants: %v", err)
	}
	m := regexp.MustCompile(`(?m)^export const CONSENT_VERSION = '([^']*)';$`).FindSubmatch(src)
	if m == nil {
		t.Fatal("web/src/constants.ts no longer declares CONSENT_VERSION in the expected form")
	}
	if got := string(m[1]); got != ConsentVersion {
		t.Fatalf("the SPA serves consent edition %q, the server accepts %q", got, ConsentVersion)
	}
}
