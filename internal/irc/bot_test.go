package irc

import (
	"testing"

	"neongrid/internal/game"
)

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

func TestFreeCommand(t *testing.T) {
	for _, command := range []string{"!status", "!runner", "!top now"} {
		if !freeCommand(command, game.ActivityChat) {
			t.Errorf("freeCommand(%q) = false", command)
		}
	}
	for _, command := range []string{"hello", "!help"} {
		if freeCommand(command, game.ActivityChat) {
			t.Errorf("freeCommand(%q) = true", command)
		}
	}
	if freeCommand("!status", game.ActivityAction) {
		t.Error("action command was treated as free")
	}
}
