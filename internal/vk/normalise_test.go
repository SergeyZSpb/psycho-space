package vk

import "testing"

// The shared profile vocabulary is "male"/"female"/"" and ISO "YYYY-MM-DD"/"",
// and VK is the provider that has to translate into it — Yandex already speaks
// both. These are the two functions that make a VK profile and a Yandex profile
// indistinguishable downstream.

func TestNormaliseSex(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"1", "female"},
		{"2", "male"},
		{"", ""},
		{"0", ""},
		{"3", ""},
		{"male", ""}, // not VK's vocabulary — VK sends a number
	} {
		if got := normaliseSex(tc.in); got != tc.want {
			t.Errorf("normaliseSex(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormaliseBirthday(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
	}{
		{"padded", "15.05.1990", "1990-05-15"},
		// VK does not zero-pad, and the integration fixture uses this exact value.
		{"unpadded day and month", "5.5.1990", "1990-05-05"},
		{"unpadded month only", "15.5.1990", "1990-05-15"},
		{"empty", "", ""},
		{"no year", "15.05", ""},
		{"two-digit year is not a year", "15.05.90", ""},
		{"non-numeric", "15.май.1990", ""},
		{"already ISO is not VK's format", "1990-05-15", ""},
		{"too many parts", "15.05.1990.1", ""},
		{"empty part", "15..1990", ""},
		{"three-digit day", "150.5.1990", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := normaliseBirthday(tc.in); got != tc.want {
				t.Errorf("normaliseBirthday(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
