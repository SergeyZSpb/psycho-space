package config

import "testing"

// Yandex credentials are all-or-none. A provider with one of three values set
// is always a mistake rather than a choice, and failing at startup is kinder
// than a login button that appears and then answers 503.
func TestYandexPartiallyConfigured(t *testing.T) {
	full := Yandex{ClientID: "id", ClientSecret: "secret", RedirectURI: "https://example/redirect"}
	if full.PartiallyConfigured() || !full.Configured() {
		t.Error("a fully configured Yandex should be Configured and not partial")
	}
	none := Yandex{}
	if none.PartiallyConfigured() || none.Configured() {
		t.Error("an empty Yandex should be neither configured nor partial")
	}
	for name, y := range map[string]Yandex{
		"id only":            {ClientID: "id"},
		"secret only":        {ClientSecret: "secret"},
		"redirect only":      {RedirectURI: "https://example/redirect"},
		"missing the secret": {ClientID: "id", RedirectURI: "https://example/redirect"},
		"missing the id":     {ClientSecret: "secret", RedirectURI: "https://example/redirect"},
	} {
		if !y.PartiallyConfigured() {
			t.Errorf("%s: want partially configured", name)
		}
		if y.Configured() {
			t.Errorf("%s: must not count as configured", name)
		}
	}
}
