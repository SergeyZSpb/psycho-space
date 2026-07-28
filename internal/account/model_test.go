package account

import "testing"

// ProfileURL is the one place a provider still shows through to the client, so
// it is worth pinning: VK accounts link to a VK profile, Yandex accounts link
// nowhere (Yandex ID has no public profile page), and a forgotten account of
// either provider links nowhere either because its id was overwritten.
func TestProfileURL(t *testing.T) {
	for _, tc := range []struct {
		name string
		acc  Account
		want string
	}{
		{"vk", Account{Provider: ProviderVK, ProviderUserID: "12345"}, "https://vk.com/id12345"},
		{"yandex has no public profile", Account{Provider: ProviderYandex, ProviderUserID: "12345"}, ""},
		{"forgotten vk", Account{Provider: ProviderVK, ProviderUserID: ""}, ""},
		{"forgotten yandex", Account{Provider: ProviderYandex, ProviderUserID: ""}, ""},
		{"unknown provider links nowhere", Account{Provider: "telegram", ProviderUserID: "12345"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.acc.ProfileURL(); got != tc.want {
				t.Errorf("ProfileURL() = %q, want %q", got, tc.want)
			}
		})
	}
}

// A nameless account falls back to its handle rather than to anything the
// provider supplied. This is why the Yandex client deliberately does not read
// `display_name`, which is usually the login: the fallback is the same for both
// providers, and it discloses nothing.
func TestDisplayNameFallsBackToHandle(t *testing.T) {
	a := Account{Handle: "deadbeef"}
	if got := a.DisplayName(); got != "psycho-deadbeef" {
		t.Errorf("DisplayName() = %q, want psycho-deadbeef", got)
	}
	named := Account{FirstName: "Иван", LastName: "Петров", Handle: "deadbeef"}
	if got := named.DisplayName(); got != "Иван Петров" {
		t.Errorf("DisplayName() = %q, want Иван Петров", got)
	}
}
