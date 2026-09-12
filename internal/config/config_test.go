package config

import (
	"testing"
	"time"
)

func TestTLS12OnlyEnvironmentOverride(t *testing.T) {
	t.Setenv("NEONGRID_TLS12_ONLY", "true")
	t.Setenv("NEONGRID_EVENTS_DISTRICT_HOURS", "8")
	t.Setenv("NEONGRID_EVENTS_HEAT_DECAY_MINUTES", "12")
	cfg := Defaults()
	if err := applyEnv(&cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.TLS12Only {
		t.Fatal("TLS12Only was not enabled by environment")
	}
	if got := cfg.Rules().DistrictInterval; got != 8*time.Hour {
		t.Fatalf("district interval = %s, want 8h", got)
	}
	if got := cfg.Rules().HeatDecayInterval; got != 12*time.Minute {
		t.Fatalf("heat decay interval = %s, want 12m", got)
	}
}
