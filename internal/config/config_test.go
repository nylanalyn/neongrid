package config

import (
	"testing"
	"time"
)

func TestTLS12OnlyEnvironmentOverride(t *testing.T) {
	t.Setenv("NEONGRID_TLS12_ONLY", "true")
	t.Setenv("NEONGRID_EVENTS_DISTRICT_HOURS", "8")
	t.Setenv("NEONGRID_EVENTS_HEAT_DECAY_MINUTES", "12")
	t.Setenv("NEONGRID_EVENTS_CONTRACT_HOURS", "6")
	t.Setenv("NEONGRID_EVENTS_CONTRACT_PARTICIPANTS", "3")
	t.Setenv("NEONGRID_EVENTS_COLLISION_MINUTES", "45")
	t.Setenv("NEONGRID_WEB_LISTEN", "127.0.0.1:9090")
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
	if got := cfg.Rules().ContractDuration; got != 6*time.Hour {
		t.Fatalf("contract duration = %s, want 6h", got)
	}
	if got := cfg.Rules().ContractMaxParticipants; got != 3 {
		t.Fatalf("contract participants = %d, want 3", got)
	}
	if got := cfg.Rules().CollisionInterval; got != 45*time.Minute {
		t.Fatalf("collision interval = %s, want 45m", got)
	}
	if cfg.WebListen != "127.0.0.1:9090" {
		t.Fatalf("web listen = %q, want 127.0.0.1:9090", cfg.WebListen)
	}
}

func TestSecurityDefaultsAndAdminEnvironment(t *testing.T) {
	cfg := Defaults()
	if !cfg.NickServ.SASL || cfg.TLSLegacyFallback {
		t.Fatalf("defaults: sasl=%v legacy fallback=%v", cfg.NickServ.SASL, cfg.TLSLegacyFallback)
	}
	t.Setenv("NEONGRID_ADMIN_ACCOUNTS", " root, ,gridadmin ")
	t.Setenv("NEONGRID_TLS_LEGACY_FALLBACK", "true")
	if err := applyEnv(&cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.AdminAccounts) != 2 || cfg.AdminAccounts[0] != "root" || cfg.AdminAccounts[1] != "gridadmin" {
		t.Fatalf("admin accounts = %#v", cfg.AdminAccounts)
	}
	if !cfg.TLSLegacyFallback {
		t.Fatal("legacy fallback was not enabled by environment")
	}
}

func TestNegativeTuningIsRejected(t *testing.T) {
	t.Setenv("NEONGRID_PENALTY_SPEECH_BASE_SECONDS", "-30")
	if _, err := Load(""); err == nil {
		t.Fatal("negative speech penalty was accepted")
	}
}

func TestArtifactOfflineDays(t *testing.T) {
	if got := Defaults().Rules().ArtifactOfflineRelease; got != 7*24*time.Hour {
		t.Fatalf("default artifact release = %s, want 7 days", got)
	}
	t.Setenv("NEONGRID_EVENTS_ARTIFACT_OFFLINE_DAYS", "3")
	cfg := Defaults()
	if err := applyEnv(&cfg); err != nil {
		t.Fatal(err)
	}
	if got := cfg.Rules().ArtifactOfflineRelease; got != 3*24*time.Hour {
		t.Fatalf("artifact release = %s, want 3 days", got)
	}
}
