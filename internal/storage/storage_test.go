package storage

import (
	"database/sql"
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
		District: game.DistrictFloodline, NextDistrictAt: now.Add(time.Hour), NextCollisionAt: now.Add(2 * time.Hour),
		Heat: 42, LastHeatAt: now,
		Equipment: map[string]game.Item{game.SlotWeaponRig: {Name: "Mono-edge Mk 1", Rating: 1, Unique: true}},
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
	if !loaded[0].Equipment[game.SlotWeaponRig].Unique {
		t.Fatal("unique equipment flag did not round-trip")
	}
	if loaded[0].District != game.DistrictFloodline || !loaded[0].NextDistrictAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("district did not round-trip: %+v", loaded[0])
	}
	if loaded[0].Heat != 42 || !loaded[0].LastHeatAt.Equal(now) {
		t.Fatalf("heat did not round-trip: %+v", loaded[0])
	}
	if !loaded[0].NextCollisionAt.Equal(now.Add(2 * time.Hour)) {
		t.Fatalf("collision deadline did not round-trip: %+v", loaded[0])
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

func TestWorldEventHistoryRoundTrip(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "neongrid.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	want := game.WorldState{
		PirateUntil:     time.Unix(1000, 0),
		NextCityEventAt: time.Unix(1100, 0),
		RecentEvents:    []string{"[GRID] DATA LEAK", "[GRID] PIRATE FREQUENCY"},
		Contract: &game.Contract{
			Title: "Helix Dynamics breach", District: game.DistrictCorporateArcology,
			Participants: []string{"acct:alpha", "acct:beta"}, EndsAt: time.Unix(1200, 0),
		},
	}
	if err := store.SaveWorldState(want); err != nil {
		t.Fatal(err)
	}
	got, err := store.LoadWorldState()
	if err != nil {
		t.Fatal(err)
	}
	if got.PirateUntil != want.PirateUntil || got.NextCityEventAt != want.NextCityEventAt || len(got.RecentEvents) != 2 || got.RecentEvents[0] != want.RecentEvents[0] {
		t.Fatalf("world = %#v, want %#v", got, want)
	}
	if got.Contract == nil || got.Contract.Title != want.Contract.Title || got.Contract.District != want.Contract.District || len(got.Contract.Participants) != 2 || !got.Contract.EndsAt.Equal(want.Contract.EndsAt) {
		t.Fatalf("contract = %#v, want %#v", got.Contract, want.Contract)
	}
}

func TestMigratesV1Players(t *testing.T) {
	path := filepath.Join(t.TempDir(), "neongrid.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
CREATE TABLE players (
  identity TEXT PRIMARY KEY,
  account TEXT NOT NULL DEFAULT '',
  nick TEXT NOT NULL,
  guest INTEGER NOT NULL DEFAULT 1,
  level INTEGER NOT NULL DEFAULT 1,
  progress_seconds INTEGER NOT NULL DEFAULT 0,
  last_progress_at INTEGER NOT NULL DEFAULT 0,
  connected INTEGER NOT NULL DEFAULT 0,
  last_seen_at INTEGER NOT NULL DEFAULT 0,
  next_encounter_at INTEGER NOT NULL DEFAULT 0,
  faction TEXT NOT NULL DEFAULT '',
  equipment_json TEXT NOT NULL DEFAULT '{}'
);
CREATE TABLE world_state (key TEXT PRIMARY KEY, value TEXT NOT NULL);
INSERT INTO players(identity, nick) VALUES('acct:test', 'runner');`)
	if closeErr := db.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}

	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var version int
	if err := store.db.QueryRow("SELECT version FROM schema_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != currentSchemaVersion {
		t.Fatalf("schema version = %d, want %d", version, currentSchemaVersion)
	}
	var district string
	if err := store.db.QueryRow("SELECT district FROM players WHERE identity = ?", "acct:test").Scan(&district); err != nil {
		t.Fatal(err)
	}
	if district != game.DistrictNeonMarket {
		t.Fatalf("migrated district = %q", district)
	}
	var heat int
	if err := store.db.QueryRow("SELECT heat FROM players WHERE identity = ?", "acct:test").Scan(&heat); err != nil {
		t.Fatal(err)
	}
	if heat != 0 {
		t.Fatalf("migrated heat = %d, want 0", heat)
	}
	var nextCollision int64
	if err := store.db.QueryRow("SELECT next_collision_at FROM players WHERE identity = ?", "acct:test").Scan(&nextCollision); err != nil {
		t.Fatal(err)
	}
	if nextCollision != 0 {
		t.Fatalf("migrated collision deadline = %d, want 0", nextCollision)
	}
}

func TestRareItemClaimsAreUnique(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "neongrid.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	claimed, err := store.ClaimRareItem("Blackglass Deck", "acct:first")
	if err != nil || !claimed {
		t.Fatalf("first claim = %v, %v", claimed, err)
	}
	claimed, err = store.ClaimRareItem("Blackglass Deck", "acct:second")
	if err != nil || claimed {
		t.Fatalf("duplicate claim = %v, %v", claimed, err)
	}
}
