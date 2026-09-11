package storage

import (
	"path/filepath"
	"testing"
	"time"

	"neongrid/internal/game"
)

func TestSQLiteRoundTripAndGuestMigration(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "neongrid.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Unix(1000, 0)
	guest := &game.Player{
		Identity: game.GuestKey("runner"), Nick: "runner", Guest: true, Level: 3,
		ProgressSeconds: 12, LastProgressAt: now, LastSeenAt: now, Connected: true,
		Equipment: map[string]game.Item{game.SlotWeaponRig: {Name: "Mono-edge Mk 1", Rating: 1}},
	}
	if err := store.Save(guest); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadAll()
	if err != nil || len(loaded) != 1 {
		t.Fatalf("load = %d, %v", len(loaded), err)
	}
	if loaded[0].Equipment[game.SlotWeaponRig].Rating != 1 {
		t.Fatal("equipment did not round-trip")
	}

	bound, err := store.MigrateGuest(game.GuestKey("runner"), game.AccountKey("nylan"), "nylan", "runner")
	if err != nil {
		t.Fatal(err)
	}
	if bound.Identity != game.AccountKey("nylan") || bound.Guest || bound.Level != 3 {
		t.Fatalf("bound = %+v", bound)
	}
	loaded, err = store.LoadAll()
	if err != nil || len(loaded) != 1 || loaded[0].Identity != game.AccountKey("nylan") {
		t.Fatalf("post-migration = %+v, %v", loaded, err)
	}
}
