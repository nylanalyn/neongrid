package irc

import "testing"

func TestNamesNick(t *testing.T) {
	for input, want := range map[string]string{
		"@runner":                   "runner",
		"+runner!user@example.test": "runner",
		"runner!user@example.test":  "runner",
	} {
		if got := namesNick(input); got != want {
			t.Errorf("namesNick(%q) = %q, want %q", input, got, want)
		}
	}
}
