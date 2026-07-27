package gamevanyagotchi

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
)

// TestParseInbound covers every way a frame can be refused or corrected. It is a
// table because the inbound path is the one surface a client controls
// completely: the payload arrives over a socket, from a browser we do not ship
// to every user at the same version, and possibly from something that is not our
// client at all.
func TestParseInbound(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    Point
		wantErr error
	}{
		{
			name:    "a plain move",
			payload: `{"t":"vanyagotchi_move","x":0.25,"y":0.75}`,
			want:    Point{X: 0.25, Y: 0.75},
		},
		{
			name:    "the corners are inside the plane",
			payload: `{"t":"vanyagotchi_move","x":0,"y":1}`,
			want:    Point{X: 0, Y: 1},
		},
		{
			// Clamped rather than refused: a tap a little outside the plane
			// means the edge of it, and a phone's rounding should not cost a move.
			name:    "past the far edge is clamped in",
			payload: `{"t":"vanyagotchi_move","x":4.5,"y":1.0001}`,
			want:    Point{X: 1, Y: 1},
		},
		{
			name:    "past the near edge is clamped in",
			payload: `{"t":"vanyagotchi_move","x":-0.2,"y":-99}`,
			want:    Point{X: 0, Y: 0},
		},
		{
			// NaN is not an out-of-range position, it is a broken or probing
			// client. Clamping it would hide that and park somebody on an edge.
			name:    "NaN is refused, not clamped",
			payload: `{"t":"vanyagotchi_move","x":NaN,"y":0.5}`,
			wantErr: ErrMalformedMessage, // encoding/json rejects the literal itself
		},
		{
			name:    "an unknown type is ignored",
			payload: `{"t":"vanyagotchi_shout","x":0.5,"y":0.5}`,
			wantErr: ErrUnknownMessage,
		},
		{
			name:    "the transport's own frame is not ours",
			payload: `{"t":"bye","code":1001,"reason":"restart"}`,
			wantErr: ErrUnknownMessage,
		},
		{
			name:    "no type at all",
			payload: `{"x":0.5,"y":0.5}`,
			wantErr: ErrUnknownMessage,
		},
		{
			// The pointer fields exist for exactly this: a missing coordinate
			// must not be read as a deliberate 0 and teleport somebody to a corner.
			name:    "a missing coordinate is refused",
			payload: `{"t":"vanyagotchi_move","x":0.5}`,
			wantErr: ErrInvalidPosition,
		},
		{
			name:    "no coordinates at all",
			payload: `{"t":"vanyagotchi_move"}`,
			wantErr: ErrInvalidPosition,
		},
		{
			name:    "a coordinate of the wrong type",
			payload: `{"t":"vanyagotchi_move","x":"0.5","y":0.5}`,
			wantErr: ErrMalformedMessage,
		},
		{
			name:    "not JSON",
			payload: `not json at all`,
			wantErr: ErrMalformedMessage,
		},
		{
			name:    "an empty frame",
			payload: ``,
			wantErr: ErrMalformedMessage,
		},
		{
			name:    "a JSON array",
			payload: `[1,2,3]`,
			wantErr: ErrMalformedMessage,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseInbound([]byte(tc.payload))
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v; want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("point = %+v; want %+v", got, tc.want)
			}
		})
	}
}

// TestClampUnitRejectsNonFinite covers the values encoding/json will never
// produce from a literal but which arithmetic can: an infinity reaching the
// plane would put an entity at a coordinate no CSS transform can render, and a
// NaN silently fails every comparison it is used in.
func TestClampUnitRejectsNonFinite(t *testing.T) {
	for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, ok := clampUnit(v); ok {
			t.Errorf("clampUnit(%v) accepted a non-finite value", v)
		}
	}
	for _, tc := range []struct{ in, want float64 }{
		{in: -1, want: 0},
		{in: 0, want: 0},
		{in: 0.5, want: 0.5},
		{in: 1, want: 1},
		{in: 2, want: 1},
	} {
		got, ok := clampUnit(tc.in)
		if !ok || got != tc.want {
			t.Errorf("clampUnit(%v) = %v, %v; want %v, true", tc.in, got, ok, tc.want)
		}
	}
}

// TestParsingAVerbFrameRejectsEveryBadShape. The edge is here: a frame asking
// for a thousand verbs must be refused before anything reads a database, so the
// batch cap is enforced in the parser as well as in Do.
func TestParsingAVerbFrameRejectsEveryBadShape(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload string
		want    error
		why     string
	}{
		{name: "not json", payload: `{`, want: ErrMalformedMessage},
		{name: "another type", payload: `{"t":"vanyagotchi_move","x":0.5,"y":0.5}`, want: ErrUnknownMessage,
			why: "the parser is total over any frame, not only the ones the switch routes here"},
		{name: "no verbs field", payload: `{"t":"vanyagotchi_do"}`, want: ErrNoVerbs},
		{name: "empty list", payload: `{"t":"vanyagotchi_do","verbs":[]}`, want: ErrNoVerbs,
			why: "asking for nothing is a malformed frame, not a no-op worth a transaction"},
		{name: "a blank verb", payload: `{"t":"vanyagotchi_do","verbs":["drink",""]}`, want: ErrNoVerbs},
		{name: "verbs is not a list", payload: `{"t":"vanyagotchi_do","verbs":"drink"}`, want: ErrMalformedMessage},
		{name: "past the batch cap", payload: `{"t":"vanyagotchi_do","verbs":["drink","drink","drink","drink","drink","drink","drink","drink","drink"]}`,
			want: ErrBatchTooLong,
			why:  "nine verbs against a cap of eight — refused at the edge, before any storage is touched"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := parseVerbs([]byte(tc.payload)); !errors.Is(err, tc.want) {
				t.Fatalf("parseVerbs(%s) = %v; want %v — %s", tc.payload, err, tc.want, tc.why)
			}
		})
	}
}

// TestParsingAVerbFrameKeepsTheOrder. Order inside a batch is meaningful — the
// fold applies them in sequence — so a parser that returned a set would silently
// change what the player asked for.
func TestParsingAVerbFrameKeepsTheOrder(t *testing.T) {
	got, _, err := parseVerbs([]byte(`{"t":"vanyagotchi_do","verbs":["drink","relieve","drink"]}`))
	if err != nil {
		t.Fatalf("parseVerbs: %v", err)
	}
	want := []string{ActionDrink, ActionRelieve, ActionDrink}
	if len(got) != len(want) {
		t.Fatalf("got %d verbs; want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("position %d is %q; want %q — the batch order is the fold order", i, got[i], want[i])
		}
	}
}

// TestParsingAVerbFrameCarriesTheSpotThroughUntouched.
//
// The spot is the first inbound field the server has to JUDGE rather than clamp,
// and the parser's whole job is to not get in the way of that: it reads the
// string and hands it on. Every check that matters — is it a place, is it a place
// in HIS location, is he standing in it — is the service's, against the catalogue
// and the yard's own placement, and a parser that pre-filtered would be a second
// weaker copy of the lookup that has to happen anyway.
//
// Absence is the ordinary case, not a defect: every verb but a search sends no
// spot at all, so an empty string has to reach Do rather than being refused here.
func TestParsingAVerbFrameCarriesTheSpotThroughUntouched(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload string
		want    string
		why     string
	}{
		{
			name:    "a spot the catalogue has",
			payload: `{"t":"vanyagotchi_do","verbs":["claim"],"spot":"bush"}`,
			want:    "bush",
		},
		{
			name:    "no spot at all",
			payload: `{"t":"vanyagotchi_do","verbs":["drink"]}`,
			want:    "",
			why:     "every verb but a search sends none, so absence is the ordinary frame rather than a bad one",
		},
		{
			name:    "a spot no location has",
			payload: `{"t":"vanyagotchi_do","verbs":["claim"],"spot":"луна"}`,
			want:    "луна",
			why:     "the parser validates shape only; whether this names a place is decided against the catalogue for the pet's own location",
		},
		{
			name:    "a spot that is not a string",
			payload: `{"t":"vanyagotchi_do","verbs":["claim"],"spot":7}`,
			want:    "",
			why:     "a malformed spot takes the whole frame down, which is the same silence any other bad shape gets",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, spot, err := parseVerbs([]byte(tc.payload))
			if tc.name == "a spot that is not a string" {
				if !errors.Is(err, ErrMalformedMessage) {
					t.Fatalf("parseVerbs(%s) = %v; want ErrMalformedMessage — %s", tc.payload, err, tc.why)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseVerbs(%s): %v", tc.payload, err)
			}
			if spot != tc.want {
				t.Fatalf("the frame's spot came through as %q; want %q — %s", spot, tc.want, tc.why)
			}
		})
	}
}

// Which roster ids can never have a picture.
//
// It decides how long the avatar route may cache a 404, and getting that wrong
// cost a real bug: a person's miss is TRANSIENT — the picture is read when their
// owner says hello — so caching it for half an hour left a face missing long
// after it existed, and made two browsers disagree about the same handle.
func TestOnlyAPersonCanEverHaveAFace(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   string
		want bool
	}{
		{"a character the world owns", npcPrefix + "sahur", true},
		{"another character", npcPrefix + "ballerina", true},
		{"a thing lying on the ground", propPrefix + "a1b2c3d4e5f6", true},
		{"a person's pseudonym", "AV0XmddbiDyp", false},
		{"a pseudonym that merely starts with n", "npcLookAlike", false},
		{"a pseudonym that merely starts with o", "objLookAlike", false},
		{"nothing at all", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := NeverHasAFace(tc.id); got != tc.want {
				t.Errorf("NeverHasAFace(%q) = %v, want %v", tc.id, got, tc.want)
			}
		})
	}

	// And the prefixes are the ones the roster actually builds, rather than two
	// strings that happen to agree with it today.
	tr := &fakeTransport{}
	tr.setMembers(member("a"))
	svc := planeService(tr, &fakeRepo{})
	svc.load(context.Background(), accountOf("a"))
	if err := svc.broadcast(context.Background(), at(0)); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	frames := tr.frames()
	frame := frames[len(frames)-1]
	var sawNPC bool
	for _, p := range frame.Peers {
		if strings.HasPrefix(p.ID, npcPrefix) {
			sawNPC = true
			if !NeverHasAFace(p.ID) {
				t.Errorf("the roster drew %q but NeverHasAFace says it could have a picture", p.ID)
			}
		}
	}
	if !sawNPC {
		t.Error("the yard published no NPC at all, so this half of the test proved nothing")
	}
}
