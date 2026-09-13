package web

import (
	"math/rand"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"neongrid/internal/game"
	"neongrid/internal/storage"
)

func TestBuildPageSortsRunnersAndCountsDistricts(t *testing.T) {
	now := time.Unix(2000, 0)
	players := []*game.Player{
		{Nick: "zeta", Level: 2, ProgressSeconds: 3, District: game.DistrictFloodline, Connected: true},
		{Nick: "alpha", Alias: "chicken-licker", Level: 3, District: game.DistrictNeonMarket, Connected: true},
	}
	data := buildPage(players, game.WorldState{NextCityEventAt: now.Add(time.Hour)}, "#neongrid", game.Rules{BaseLevelSeconds: 60}, now)
	if data.Active != 2 || data.Known != 2 || len(data.ActiveRunners) != 2 {
		t.Fatalf("runner counts = %#v", data)
	}
	if data.Leaderboard[0].Nick != "chicken-licker" {
		t.Fatalf("leaderboard = %#v", data.Leaderboard)
	}
	for _, district := range data.Districts {
		if district.Name == game.DistrictFloodline && district.Active != 1 {
			t.Fatalf("floodline count = %#v", district)
		}
	}
	if !strings.Contains(data.World, "City event window") {
		t.Fatalf("world summary = %q", data.World)
	}
}

func TestHandlerRendersReadOnlyObserver(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "neongrid.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now()
	e, err := game.New(store, game.Rules{GameChannel: "#neongrid", BaseLevelSeconds: 60, PirateDuration: time.Minute}, rand.New(rand.NewSource(1)), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Join("", "runner", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err = e.ForcePirate(now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	server := New(e, "#neongrid")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest("GET", "http://observer/", nil))
	if recorder.Code != 200 {
		t.Fatalf("observer status = %d", recorder.Code)
	}
	body := recorder.Body.String()
	for _, want := range []string{"runner", "PIRATE FREQUENCY", "District map", "Recent incidents"} {
		if !strings.Contains(body, want) {
			t.Fatalf("observer page does not contain %q", want)
		}
	}

	health := httptest.NewRecorder()
	server.Handler().ServeHTTP(health, httptest.NewRequest("GET", "http://observer/healthz", nil))
	if health.Code != 200 || health.Body.String() != "ok\n" {
		t.Fatalf("health response = %d %q", health.Code, health.Body.String())
	}
	readOnly := httptest.NewRecorder()
	server.Handler().ServeHTTP(readOnly, httptest.NewRequest("POST", "http://observer/", nil))
	if readOnly.Code != 405 {
		t.Fatalf("observer POST status = %d, want 405", readOnly.Code)
	}
}
