package gamevanyagotchi

import (
	"errors"
	"math"
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
