package realtime

import "testing"

// TestEncodeBye pins the wire shape. A client dispatches on "t", so the field
// names and the discriminator are a contract: renaming one silently breaks every
// deployed client, and the SPA is cached.
func TestEncodeBye(t *testing.T) {
	got := string(encodeBye(CloseGoingAway, "restart"))
	want := `{"t":"bye","code":1001,"reason":"restart"}`
	if got != want {
		t.Errorf("encodeBye = %s, want %s", got, want)
	}
}
