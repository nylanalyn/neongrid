package game

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"
)

func featureEngine(t *testing.T, now time.Time) (*Engine, *memoryRepo) {
	t.Helper()
	rules := testRules()
	// Keep background events out of the way unless a test asks for them.
	rules.EncounterInterval, rules.DistrictInterval, rules.CollisionInterval = 30*24*time.Hour, 30*24*time.Hour, 30*24*time.Hour
	rules.CityEventInterval = 30 * 24 * time.Hour
	rules.BaseLevelSeconds = 24 * 3600
	repo := newMemoryRepo()
	e, err := New(repo, rules, rand.New(rand.NewSource(1)), now)
	if err != nil {
		t.Fatal(err)
	}
	return e, repo
}

func joinAll(t *testing.T, e *Engine, now time.Time, nicks ...string) {
	t.Helper()
	for _, nick := range nicks {
		if _, err := e.Join("", nick, nick, now); err != nil {
			t.Fatal(err)
		}
	}
}

func containing(messages []string, fragment string) []string {
	var out []string
	for _, message := range messages {
		if strings.Contains(message, fragment) {
			out = append(out, message)
		}
	}
	return out
}

func TestAllFlavorTemplatesRender(t *testing.T) {
	fill := []string{"runner", "R", "ice", "I", "place", "P", "item", "X", "from", "F", "to", "T", "winner", "W", "loser", "L", "gain", "G", "district", "D"}
	pools := [][]string{iceWinTemplates, iceUniqueTemplates, iceLossTemplates, driftTemplates}
	pools = append(pools, collisionTemplates...)
	for _, texts := range cityEventTexts {
		pools = append(pools, texts)
	}
	for _, pool := range pools {
		if len(pool) == 0 {
			t.Fatal("empty flavor pool")
		}
		for _, template := range pool {
			if got := render(template, fill...); strings.ContainsAny(got, "{}") {
				t.Errorf("unfilled placeholder in %q", got)
			}
		}
	}
	for _, district := range districts {
		if len(districtPlaces[district]) == 0 {
			t.Errorf("district %s has no places", district)
		}
	}
	if len(collisionTemplates) != 4 {
		t.Fatalf("collision kinds = %d, want 4", len(collisionTemplates))
	}
}

func TestCollisionPrefersSameDistrict(t *testing.T) {
	now := time.Unix(40000, 0)
	e, _ := featureEngine(t, now)
	joinAll(t, e, now, "a", "b", "c")
	a, b, c := e.users[AccountKey("a")], e.users[AccountKey("b")], e.users[AccountKey("c")]
	a.District, b.District, c.District = DistrictFloodline, DistrictFloodline, DistrictNeonMarket
	for seed := int64(0); seed < 100; seed++ {
		e.rng = rand.New(rand.NewSource(seed))
		if got := e.pickOpponentLocked(a); got != b {
			t.Fatalf("seed %d picked %s over the runner in the same district", seed, got.Nick)
		}
	}
	// Alone in a district, anyone will do.
	if got := e.pickOpponentLocked(c); got == nil {
		t.Fatal("no opponent when alone in a district")
	}
}

func TestRepeatCollisionsBecomeARivalry(t *testing.T) {
	now := time.Unix(41000, 0)
	e, _ := featureEngine(t, now)
	joinAll(t, e, now, "a", "b")
	var messages []string
	for i := 0; i < rivalryBouts; i++ {
		e.users[AccountKey("a")].NextCollisionAt = now
		message, err := e.collisionLocked(now)
		if err != nil {
			t.Fatal(err)
		}
		messages = append(messages, message)
	}
	last := len(messages) - 1
	if strings.HasPrefix(messages[last-1], "[GRID] RIVALRY") || !strings.HasPrefix(messages[last], fmt.Sprintf("[GRID] RIVALRY, round %d: ", rivalryBouts)) {
		t.Fatalf("collision messages = %#v", messages)
	}
	if topRival(e.users[AccountKey("a")]) != AccountKey("b") {
		t.Fatal("rival was not recorded")
	}
}

func TestCollisionCanStealAnArtifact(t *testing.T) {
	now := time.Unix(42000, 0)
	for seed := int64(0); seed < 500; seed++ {
		e, repo := featureEngine(t, now)
		e.rng = rand.New(rand.NewSource(seed))
		joinAll(t, e, now, "thief", "mark")
		thief, mark := e.users[AccountKey("thief")], e.users[AccountKey("mark")]
		thief.Level, mark.Level = 20, 10
		mark.Equipment[SlotDeck] = Item{Name: "Blackglass Deck", Rating: 7, Unique: true, BreaksAt: 13}
		repo.rares["Blackglass Deck"] = mark.Identity
		thief.NextCollisionAt = now
		message, err := e.collisionLocked(now)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(message, "walked off with") {
			continue
		}
		winner, loser := thief, mark
		if strings.Contains(message, "mark walked off") {
			t.Fatalf("an artifact holder stole their own artifact: %q", message)
		}
		deck := winner.Equipment[SlotDeck]
		if deck.Name != "Blackglass Deck" || !deck.Unique || deck.BreaksAt != winner.Level+3 {
			t.Fatalf("stolen deck = %+v, want 3 levels of life left", deck)
		}
		if loser.Equipment[SlotDeck].Unique || repo.rares["Blackglass Deck"] != winner.Identity || winner.Stats.Thefts != 1 {
			t.Fatalf("theft bookkeeping: loser=%+v owner=%q thefts=%d", loser.Equipment[SlotDeck], repo.rares["Blackglass Deck"], winner.Stats.Thefts)
		}
		return
	}
	t.Fatal("no theft in 500 seeded collisions")
}

func TestStanceTradesOddsForRewards(t *testing.T) {
	now := time.Unix(43000, 0)
	e, _ := featureEngine(t, now)
	joinAll(t, e, now, "runner")
	p := e.users[AccountKey("runner")]
	normal, normalLoot := iceWinChance(p, ""), rareLootChance(p)
	if _, err := e.SetStance(AccountKey("runner"), "runner", "HOT", now); err != nil {
		t.Fatal(err)
	}
	if iceWinChance(p, "") >= normal || rareLootChance(p) <= normalLoot || stanceReward(p, 100) <= 100 || stanceHeat(p, 8) <= 8 {
		t.Fatal("hot stance did not trade odds for rewards")
	}
	if _, err := e.SetStance(AccountKey("runner"), "runner", "cold", now); err != nil {
		t.Fatal(err)
	}
	if iceWinChance(p, "") <= normal || rareLootChance(p) >= normalLoot || stanceReward(p, 100) >= 100 || stanceHeat(p, 8) >= 8 {
		t.Fatal("cold stance did not trade rewards for safety")
	}
	if _, err := e.SetStance(AccountKey("runner"), "runner", "normal", now); err != nil || p.Stance != "" {
		t.Fatalf("normal stance = %q, %v", p.Stance, err)
	}
	if _, err := e.SetStance(AccountKey("runner"), "runner", "reckless", now); err == nil {
		t.Fatal("unknown stance was accepted")
	}
}

func TestFactionWeekCrownsChampion(t *testing.T) {
	now := time.Unix(44000, 0)
	e, _ := featureEngine(t, now)
	joinAll(t, e, now, "runner")
	p := e.users[AccountKey("runner")]
	p.Faction = FactionChrome
	e.scoreFactionLocked(p, 5)
	e.world.FactionWeek.Scores[FactionNomad] = 2
	if got := e.factionWeekTickLocked(now); got != "" {
		t.Fatalf("faction week ended early: %q", got)
	}
	end := e.world.FactionWeek.EndsAt
	message := e.factionWeekTickLocked(end)
	if !strings.Contains(message, "Chrome takes it (Chrome 5 · Nomad 2 · Ghostline 0)") || e.world.FactionWeek.Champion != FactionChrome {
		t.Fatalf("faction week result = %q, champion %q", message, e.world.FactionWeek.Champion)
	}
	if iceWinChance(p, FactionChrome) != iceWinChance(p, "")+championEdge {
		t.Fatal("champion faction did not get its edge")
	}
	if len(e.world.FactionWeek.Scores) != 0 || !e.world.FactionWeek.EndsAt.After(end) {
		t.Fatal("faction week did not reset")
	}
	e.world.FactionWeek.Scores = map[string]int{FactionGhostline: 3, FactionNomad: 3}
	if message := e.factionWeekTickLocked(e.world.FactionWeek.EndsAt); !strings.Contains(message, "dead heat") || e.world.FactionWeek.Champion != "" {
		t.Fatalf("tied week = %q, champion %q", message, e.world.FactionWeek.Champion)
	}
	if message := e.factionWeekTickLocked(e.world.FactionWeek.EndsAt); message != "" {
		t.Fatalf("scoreless week announced %q", message)
	}
}

func TestPirateDeadDrop(t *testing.T) {
	now := time.Unix(45000, 0)
	e, _ := featureEngine(t, now)
	joinAll(t, e, now, "fast", "slow")
	message, err := e.ForcePirate(now)
	if err != nil {
		t.Fatal(err)
	}
	code := e.world.DeadDrop
	if len(code) != deadDropLength || strings.Contains(message, code) || !strings.Contains(message, "DEAD DROP") {
		t.Fatalf("pirate message %q with code %q", message, code)
	}
	for _, attempt := range []struct{ channel, text string }{{"#neongrid", "nope"}, {"#elsewhere", code}} {
		if claim, err := e.ClaimDeadDrop("", "fast", attempt.channel, attempt.text, now); err != nil || claim != "" {
			t.Fatalf("claim %+v = %q, %v", attempt, claim, err)
		}
	}
	claim, err := e.ClaimDeadDrop(AccountKey("fast"), "fast", "#neongrid", " "+strings.ToLower(code)+" ", now.Add(time.Second))
	if err != nil || !strings.HasPrefix(claim, "[GRID] DEAD DROP claimed: fast") {
		t.Fatalf("claim = %q, %v", claim, err)
	}
	if fast := e.users[AccountKey("fast")]; fast.ProgressSeconds < 60 || fast.Stats.DeadDrops != 1 {
		t.Fatalf("claimer = %+v", fast)
	}
	if claim, _ := e.ClaimDeadDrop(AccountKey("slow"), "slow", "#neongrid", code, now.Add(2*time.Second)); claim != "" {
		t.Fatalf("dead drop was claimed twice: %q", claim)
	}
	if _, err = e.ForcePirate(now); err != nil {
		t.Fatal(err)
	}
	messages, err := e.Tick(e.world.PirateUntil)
	if err != nil || len(containing(messages, "decayed back into static")) != 1 || e.world.DeadDrop != "" {
		t.Fatalf("expiry messages = %#v, %v", messages, err)
	}
}

func TestBlackwallRaidTestsEveryoneLinked(t *testing.T) {
	now := time.Unix(46000, 0)
	e, _ := featureEngine(t, now)
	joinAll(t, e, now, "solo")
	if _, started := e.startRaidLocked(now); started {
		t.Fatal("a raid started with one runner linked")
	}
	joinAll(t, e, now, "duo")
	message, started := e.startRaidLocked(now)
	if !started || !strings.Contains(message, "BLACKWALL ALERT") {
		t.Fatalf("raid start = %q", message)
	}
	if result, _ := e.resolveRaidLocked(now.Add(raidWarning - time.Second)); result != "" {
		t.Fatalf("raid resolved early: %q", result)
	}
	for _, p := range e.users {
		p.Level = 30
		p.Equipment[SlotDeck] = Item{Name: "big", Rating: 200}
	}
	result, err := e.resolveRaidLocked(now.Add(raidWarning))
	if err != nil || !strings.HasPrefix(result, "[GRID] BLACKWALL DOWN: 2 runners") {
		t.Fatalf("strong team result = %q, %v", result, err)
	}
	for _, p := range e.users {
		if p.Stats.RaidWins != 1 {
			t.Fatalf("%s raid wins = %d", p.Nick, p.Stats.RaidWins)
		}
		p.Equipment = map[string]Item{}
	}
	e.startRaidLocked(now)
	result, err = e.resolveRaidLocked(now.Add(raidWarning))
	if err != nil || !strings.HasPrefix(result, "[GRID] BLACKWALL HOLDS") {
		t.Fatalf("ungeared team result = %q, %v", result, err)
	}
}

func TestGhostProtocolStreak(t *testing.T) {
	now := time.Unix(47000, 0)
	e, _ := featureEngine(t, now)
	joinAll(t, e, now, "idler")
	p := e.users[AccountKey("idler")]
	day := now.Add(streakDay)
	messages, err := e.Tick(day)
	if err != nil || len(containing(messages, "GHOST PROTOCOL: idler has held the link for 1 day straight")) != 1 {
		t.Fatalf("day one = %#v, %v", messages, err)
	}
	if messages, _ = e.Tick(day.Add(time.Minute)); len(containing(messages, "GHOST PROTOCOL")) != 0 {
		t.Fatal("streak paid twice for the same day")
	}
	// A netsplit and a quick rejoin keep the streak.
	if _, err = e.Disconnect("idler", ActivityNetsplit, day.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	joinAll(t, e, day.Add(time.Hour+10*time.Minute), "idler")
	messages, _ = e.Tick(now.Add(2 * streakDay))
	if len(containing(messages, "for 2 days straight")) != 1 || p.Stats.LongestStreak != 2 {
		t.Fatalf("day two = %#v, longest %d", messages, p.Stats.LongestStreak)
	}
	// Quitting ends it.
	if _, err = e.Disconnect("idler", ActivityQuit, now.Add(2*streakDay+time.Minute)); err != nil {
		t.Fatal(err)
	}
	joinAll(t, e, now.Add(2*streakDay+2*time.Minute), "idler")
	if p.StreakDays != 0 || p.Stats.LongestStreak != 2 {
		t.Fatalf("after quitting: streak %d, longest %d", p.StreakDays, p.Stats.LongestStreak)
	}
}

func TestDailyBulletin(t *testing.T) {
	now := time.Unix(48000, 0)
	e, _ := featureEngine(t, now)
	joinAll(t, e, now, "climber", "brawler")
	climber, brawler := e.users[AccountKey("climber")], e.users[AccountKey("brawler")]
	climber.ProgressSeconds = 3 * 24 * 3600
	e.advanceLocked(climber, now)
	brawler.Heat = 40
	brawler.Faction = FactionNomad
	e.noteBulletinLocked(&e.world.Bulletin.CollisionWins, brawler.Identity, 2)
	e.scoreFactionLocked(brawler, 2)
	if got := e.bulletinTickLocked(now); got != "" {
		t.Fatalf("bulletin ran early: %q", got)
	}
	got := e.bulletinTickLocked(e.world.Bulletin.NextAt)
	want := "[GRID] DAILY BULLETIN: top climber climber (+2 Rep) · most wanted brawler (Heat 40) · street champ brawler (2 collision wins) · faction week: Nomad 2 · Ghostline 0 · Chrome 0"
	if got != want {
		t.Fatalf("bulletin = %q\nwant %q", got, want)
	}
	if len(e.world.Bulletin.Climbs) != 0 || len(e.world.Bulletin.CollisionWins) != 0 {
		t.Fatal("bulletin counters did not reset")
	}
}

func TestContractFailureIsImmediateAndNamed(t *testing.T) {
	now := time.Unix(49000, 0)
	e, _ := featureEngine(t, now)
	joinAll(t, e, now, "steady", "flaky")
	if _, err := e.SetAlias(AccountKey("flaky"), "flaky", "Butterfingers", now); err != nil {
		t.Fatal(err)
	}
	if _, started, err := e.startContractLocked(now); err != nil || !started {
		t.Fatalf("contract did not start: %v", err)
	}
	if _, err := e.Disconnect("flaky", ActivityQuit, now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	messages, err := e.Tick(now.Add(11 * time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	failed := containing(messages, "CONTRACT FAILED")
	if len(failed) != 1 || !strings.Contains(failed[0], "when Butterfingers's signal dropped") || e.world.Contract != nil {
		t.Fatalf("messages = %#v, contract %+v", messages, e.world.Contract)
	}
}
