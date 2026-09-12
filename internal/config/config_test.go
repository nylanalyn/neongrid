package config

import "testing"

func TestTLS12OnlyEnvironmentOverride(t *testing.T) {
	t.Setenv("NEONGRID_TLS12_ONLY", "true")
	cfg := Defaults()
	if err := applyEnv(&cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.TLS12Only {
		t.Fatal("TLS12Only was not enabled by environment")
	}
}
