package irc

import (
	"crypto/tls"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/lrstanley/girc"

	"neongrid/internal/config"
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
	for _, command := range []string{"!help", "!status", "!runner", "!top now", "!gear", "!world", "!events", "!alias chicken-licker", "!title", "!title Street Samurai", "!stance hot"} {
		if !freeCommand(command, game.ActivityChat) {
			t.Errorf("freeCommand(%q) = false", command)
		}
	}
	for _, command := range []string{"hello", "!pirate"} {
		if freeCommand(command, game.ActivityChat) {
			t.Errorf("freeCommand(%q) = true", command)
		}
	}
	if freeCommand("!status", game.ActivityAction) {
		t.Error("action command was treated as free")
	}
	if freeCommand("!faction ghostline", game.ActivityChat) {
		t.Error("faction selection was treated as free")
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

func TestGearLineMarksUniqueArtifacts(t *testing.T) {
	got := gearLine(&game.Player{Equipment: map[string]game.Item{
		game.SlotDeck: {Name: "Blackglass Deck", Unique: true},
	}})
	want := "[GRID] loadout | deck: Blackglass Deck [UNIQUE]"
	if got != want {
		t.Fatalf("gearLine() = %q, want %q", got, want)
	}
}

func TestStatusLineShowsRunnerHistory(t *testing.T) {
	got := statusLine(&game.Player{
		Nick: "rumi", Alias: "chicken-licker", Level: 5, District: game.DistrictFloodline, Titles: []string{"ICEbreaker"}, Scars: []string{game.ScarGhostSignal},
	}, config.Defaults().Rules())
	for _, want := range []string{"chicken-licker", "title ICEbreaker", "scars Ghost Signal"} {
		if !strings.Contains(got, want) {
			t.Fatalf("status line %q does not contain %q", got, want)
		}
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
	if got := worldLine(game.WorldState{Contract: &game.Contract{Title: "Helix Dynamics breach", District: game.DistrictCorporateArcology, EndsAt: now.Add(3 * time.Hour)}}, now); got != "[GRID] contract active: Helix Dynamics breach in Corporate Arcology; 3h0m0s remaining." {
		t.Fatalf("contract worldLine() = %q", got)
	}
	if got := worldLine(game.WorldState{FactionSwapUntil: now.Add(24 * time.Hour)}, now); !strings.Contains(got, "system crash active") || !strings.Contains(got, "respec available") {
		t.Fatalf("system crash worldLine() = %q", got)
	}
}

func TestTLSModes(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server = "irc.example.test"
	b := New(cfg, nil, nil)

	modern := b.newClient(false, make(chan struct{}, 1))
	if got := modern.Config.TLSConfig.MaxVersion; got != 0 {
		t.Fatalf("modern TLS max version = %d, want default", got)
	}
	if len(modern.Config.TLSConfig.CipherSuites) != 0 {
		t.Fatal("modern TLS unexpectedly forced a cipher suite")
	}

	legacy := b.newClient(true, make(chan struct{}, 1))
	if got := legacy.Config.TLSConfig.MaxVersion; got != tls.VersionTLS12 {
		t.Fatalf("legacy TLS max version = %d, want TLS 1.2", got)
	}
	if len(legacy.Config.TLSConfig.CipherSuites) != 1 || legacy.Config.TLSConfig.CipherSuites[0] != tls.TLS_RSA_WITH_AES_256_CBC_SHA {
		t.Fatalf("legacy TLS ciphers = %#v", legacy.Config.TLSConfig.CipherSuites)
	}

	cfg.TLS12Only = true
	forced := New(cfg, nil, nil).newClient(false, make(chan struct{}, 1))
	if forced.Config.TLSConfig.MaxVersion != tls.VersionTLS12 {
		t.Fatal("tls12_only did not force TLS 1.2")
	}
	if !legacyTLSFailure(io.EOF) || !legacyTLSFailure(tls.AlertError(40)) || legacyTLSFailure(errors.New("connection reset by peer")) {
		t.Fatal("legacy TLS failure detection is too broad or too narrow")
	}
}

func TestAccountHandlersIgnoreBotIdentity(t *testing.T) {
	cfg := config.Defaults()
	b := New(cfg, nil, nil)
	client := b.newClient(false, make(chan struct{}, 1))
	source := &girc.Source{Name: client.GetNick()}
	b.handleAccount(client, girc.Event{Source: source, Params: []string{"bot-account"}})
	b.handleWhoisAccount(client, girc.Event{Params: []string{client.GetNick(), client.GetNick(), "bot-account"}})
}

func TestIsAdminRejectsBlankAccount(t *testing.T) {
	b := New(config.Config{AdminAccounts: []string{"", " gridadmin "}}, nil, nil)
	if b.isAdmin("") {
		t.Fatal("blank account was accepted as admin")
	}
	if !b.isAdmin("gridadmin") {
		t.Fatal("configured admin was rejected")
	}
}

func TestIsNetsplit(t *testing.T) {
	for _, reason := range []string{"*.net *.split", "hub.example.net leaf.example.org"} {
		if !isNetsplit(reason) {
			t.Errorf("isNetsplit(%q) = false", reason)
		}
	}
	for _, reason := range []string{"Quit: *.net *.split", "Ping timeout: 240 seconds", "a.b a.b", "going to bed", "", "example.net"} {
		if isNetsplit(reason) {
			t.Errorf("isNetsplit(%q) = true", reason)
		}
	}
}

func TestSASLUsesConfiguredAccount(t *testing.T) {
	cfg := config.Defaults()
	cfg.NickServ.Password = "hunter2"
	if mech, ok := New(cfg, nil, nil).saslMech().(*girc.SASLPlain); !ok || mech.User != cfg.Nick || mech.Pass != "hunter2" {
		t.Fatalf("default SASL mech = %#v", mech)
	}
	cfg.NickServ.Account = "gridbot"
	if mech := New(cfg, nil, nil).saslMech().(*girc.SASLPlain); mech.User != "gridbot" {
		t.Fatalf("SASL user = %q, want gridbot", mech.User)
	}
	cfg.NickServ.SASL = false
	if mech := New(cfg, nil, nil).saslMech(); mech != nil {
		t.Fatal("SASL was used after being disabled")
	}
	cfg.NickServ.SASL, cfg.NickServ.Password = true, ""
	if mech := New(cfg, nil, nil).saslMech(); mech != nil {
		t.Fatal("SASL was used without a password")
	}
}

func TestGearLineShowsBurnOut(t *testing.T) {
	got := gearLine(&game.Player{Equipment: map[string]game.Item{
		game.SlotDeck: {Name: "Blackglass Deck", Unique: true, BreaksAt: 14},
	}})
	if want := "[GRID] loadout | deck: Blackglass Deck [UNIQUE, burns out at Rep 14]"; got != want {
		t.Fatalf("gearLine() = %q, want %q", got, want)
	}
}

func TestTitleLineShowsOneTitleFirst(t *testing.T) {
	p := &game.Player{Level: 10, Titles: []string{"ICEbreaker", "Nine-Day Signal", "Ghost of Floodline"}}
	want := "[GRID] showing Nine-Day Signal (3 earned) | also: ICEbreaker, Ghost of Floodline | !title <name> to switch, !title auto for your best."
	if got := titleLine(p); got != want {
		t.Fatalf("titleLine() = %q, want %q", got, want)
	}
	if got := titleLine(&game.Player{}); !strings.HasPrefix(got, "[GRID] no titles earned yet") {
		t.Fatalf("empty titleLine() = %q", got)
	}
}

func TestStatusLineShowsStanceAndStreak(t *testing.T) {
	got := statusLine(&game.Player{Nick: "rumi", Level: 3, Stance: game.StanceCold, StreakDays: 4}, config.Defaults().Rules())
	if !strings.Contains(got, "faction unaffiliated | stance cold | streak 4d | title") {
		t.Fatalf("status line = %q", got)
	}
	if got := statusLine(&game.Player{Nick: "rumi", Level: 3}, config.Defaults().Rules()); strings.Contains(got, "stance") || strings.Contains(got, "streak") {
		t.Fatalf("default status line shows empty extras: %q", got)
	}
}

func TestWorldLineShowsRaidDeadDropAndFactionWeek(t *testing.T) {
	now := time.Unix(6000, 0)
	got := worldLine(game.WorldState{
		PirateUntil: now.Add(time.Minute), DeadDrop: "K7QXM",
		Raid:        &game.Raid{ICE: "KRAKEN v3", District: game.DistrictFloodline, ResolvesAt: now.Add(10 * time.Minute)},
		FactionWeek: game.FactionWeek{Scores: map[string]int{game.FactionChrome: 3}, EndsAt: now.Add(48 * time.Hour), Champion: game.FactionNomad},
	}, now)
	for _, want := range []string{"BLACKWALL: KRAKEN v3 hits Floodline in 10m0s", "dead drop unclaimed", "faction week: Chrome 3 · Ghostline 0 · Nomad 0, ends in 48h0m0s; reigning Nomad"} {
		if !strings.Contains(got, want) {
			t.Fatalf("world line %q does not contain %q", got, want)
		}
	}
	if strings.Contains(got, "K7QXM") {
		t.Fatal("world line leaked the dead drop code")
	}
}
