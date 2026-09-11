package irc

import (
	"testing"
	"time"

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
	for _, command := range []string{"!status", "!runner", "!top now", "!gear", "!world"} {
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

func TestGearLineUsesStableSlotOrder(t *testing.T) {
	p := &game.Player{Equipment: map[string]game.Item{
		game.SlotDrone:         {Name: "Scout drone Mk 1"},
		game.SlotWeaponRig:     {Name: "Mono-edge weapon rig Mk 1"},
		game.SlotNeuralImplant: {Name: "Neural reflex implant Mk 1"},
	}}
	got := gearLine(p)
	want := "[GRID] loadout | weapon rig: Mono-edge weapon rig Mk 1 | neural implant: Neural reflex implant Mk 1 | drone: Scout drone Mk 1"
	if got != want {
		t.Fatalf("gearLine() = %q, want %q", got, want)
	}
}

func TestWorldLineShowsPirateWindowOrNextEvent(t *testing.T) {
	now := time.Unix(6000, 0)
	if got := worldLine(game.WorldState{PirateUntil: now.Add(90 * time.Second)}, now); got != "[GRID] pirate frequency active for 1m30s; transmissions safe." {
		t.Fatalf("active worldLine() = %q", got)
	}
	if got := worldLine(game.WorldState{NextCityEventAt: now.Add(2 * time.Minute)}, now); got != "[GRID] pirate frequency dormant | next city event in 2m0s" {
		t.Fatalf("dormant worldLine() = %q", got)
	}
}
