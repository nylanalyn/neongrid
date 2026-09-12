package game

import (
	"math/rand"
	"sort"
	"strings"
	"testing"
	"time"
)

type memoryRepo struct {
	players map[string]*Player
	world   WorldState
	rares   map[string]string
}

func newMemoryRepo() *memoryRepo {
	return &memoryRepo{players: map[string]*Player{}, rares: map[string]string{}}
}

func (r *memoryRepo) LoadAll() ([]*Player, error) {
	var out []*Player
	for _, p := range r.players {
		out = append(out, clonePlayer(p))
	}
	return out, nil
}
func (r *memoryRepo) Save(p *Player) error    { r.players[p.Identity] = clonePlayer(p); return nil }
func (r *memoryRepo) Delete(key string) error { delete(r.players, key); return nil }
func (r *memoryRepo) ClaimRareItem(name, owner string) (bool, error) {
	if _, ok := r.rares[name]; ok {
		return false, nil
	}
	r.rares[name] = owner
	return true, nil
}
func (r *memoryRepo) MigrateGuest(guestKey, accountKey, account, nick string) (*Player, error) {
	p := r.players[guestKey]
	if p == nil {
		p = r.players[accountKey]
		if p == nil {
			p = newPlayer(accountKey, nick, account, time.Now())
		}
	} else {
		delete(r.players, guestKey)
	}
	p.Identity, p.Account, p.Nick, p.Guest = accountKey, account, nick, false
	r.players[accountKey] = clonePlayer(p)
	return clonePlayer(p), nil
}
func (r *memoryRepo) Top(limit int) ([]*Player, error) {
	var out []*Player
	for _, p := range r.players {
		out = append(out, clonePlayer(p))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Level > out[j].Level })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (r *memoryRepo) LoadWorldState() (WorldState, error) { return r.world, nil }
func (r *memoryRepo) SaveWorldState(w WorldState) error   { r.world = w; return nil }

func testRules() Rules {
	return Rules{
		GameChannel: "#neongrid", BaseLevelSeconds: 60, LevelStepSeconds: 10,
		SpeechBaseSeconds: 20, SpeechPerLevelSeconds: 5, SpeechPerCharacterSeconds: 1,
		ActionBaseSeconds: 30, ActionPerLevelSeconds: 5, ActionPerCharacterSeconds: 1,
		NickPenaltySeconds: 40, PartPenaltySeconds: 50, QuitPenaltySeconds: 60, KickPenaltySeconds: 70,
		EncounterInterval: time.Hour, CityEventInterval: time.Hour, DistrictInterval: time.Hour, PirateDuration: time.Minute,
		GuestRetention: 24 * time.Hour,
	}
}

func TestProgressionAndEquipment(t *testing.T) {
	now := time.Unix(1000, 0)
	e, err := New(newMemoryRepo(), testRules(), nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Join("", "aureate", "gridacct", now); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Tick(now.Add(59 * time.Second)); err != nil {
		t.Fatal(err)
	}
	p, err := e.Status(AccountKey("gridacct"), "aureate", now.Add(59*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if p.Level != 1 {
		t.Fatalf("level before deadline = %d", p.Level)
	}
	if _, err = e.Tick(now.Add(61 * time.Second)); err != nil {
		t.Fatal(err)
	}
	p, err = e.Status(AccountKey("gridacct"), "aureate", now.Add(61*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if p.Level != 2 || p.Equipment[SlotWeaponRig].Rating != 1 {
		t.Fatalf("level-up state = %+v", p)
	}
}

func TestOnlyGameChannelIsPunishableAndPirateWindowIsSafe(t *testing.T) {
	now := time.Unix(2000, 0)
	e, err := New(newMemoryRepo(), testRules(), nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Join("", "runner", "acct", now); err != nil {
		t.Fatal(err)
	}
	penalty, safe, err := e.Activity(AccountKey("acct"), "runner", "#elsewhere", ActivityChat, 20, now)
	if err != nil || safe || penalty != 0 {
		t.Fatalf("other channel result: %d %v %v", penalty, safe, err)
	}
	if _, err = e.ForcePirate(now); err != nil {
		t.Fatal(err)
	}
	penalty, safe, err = e.Activity(AccountKey("acct"), "runner", "#neongrid", ActivityChat, 20, now)
	if err != nil || !safe || penalty != 0 {
		t.Fatalf("pirate result: %d %v %v", penalty, safe, err)
	}
	penalty, safe, err = e.Activity(AccountKey("acct"), "runner", "#neongrid", ActivityChat, 20, now.Add(2*time.Minute))
	if err != nil || safe || penalty == 0 {
		t.Fatalf("expired pirate result: %d %v %v", penalty, safe, err)
	}
}

func TestGuestMigratesToAccountWithoutLosingProgress(t *testing.T) {
	now := time.Unix(3000, 0)
	e, err := New(newMemoryRepo(), testRules(), nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Join("", "meatbag42", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Tick(now.Add(40 * time.Second)); err != nil {
		t.Fatal(err)
	}
	guest, err := e.Status("", "meatbag42", now.Add(40*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Bind("meatbag42", "nylan", now.Add(40*time.Second)); err != nil {
		t.Fatal(err)
	}
	bound, err := e.Status(AccountKey("nylan"), "meatbag42", now.Add(40*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if bound.Guest || bound.Identity != AccountKey("nylan") || bound.ProgressSeconds != guest.ProgressSeconds {
		t.Fatalf("migration lost state: guest=%+v bound=%+v", guest, bound)
	}
}

func TestPenaltyScalesWithLevel(t *testing.T) {
	rules := testRules()
	if PenaltySeconds(5, ActivityChat, 10, rules) <= PenaltySeconds(1, ActivityChat, 10, rules) {
		t.Fatal("chat penalty did not scale")
	}
	if PenaltySeconds(1, ActivityAction, 12, rules) <= PenaltySeconds(1, ActivityAction, 2, rules) {
		t.Fatal("action length was ignored")
	}
}

func TestNickChangePenalizesAndUpdatesNick(t *testing.T) {
	now := time.Unix(4000, 0)
	e, err := New(newMemoryRepo(), testRules(), nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Join("", "runner", "acct", now); err != nil {
		t.Fatal(err)
	}
	penalty, err := e.Rename("runner", "runner2", now)
	if err != nil || penalty != testRules().NickPenaltySeconds {
		t.Fatalf("rename result: penalty=%d err=%v", penalty, err)
	}
	p, err := e.Status(AccountKey("acct"), "runner2", now)
	if err != nil {
		t.Fatal(err)
	}
	if p.Nick != "runner2" || p.ProgressSeconds != -penalty {
		t.Fatalf("rename state: %+v", p)
	}
}

func TestGuestRenameRejectsOccupiedNick(t *testing.T) {
	now := time.Unix(4500, 0)
	e, err := New(newMemoryRepo(), testRules(), nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Join("", "alpha", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Join("", "beta", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Disconnect("beta", ActivityPart, now); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Rename("alpha", "beta", now); err == nil {
		t.Fatal("guest rename overwrote an offline guest")
	}
	if _, err = e.Status("", "alpha", now); err != nil {
		t.Fatal("source guest was lost after rejected rename")
	}
	if p, err := e.Status("", "beta", now); err != nil || p.Nick != "beta" {
		t.Fatalf("target guest was lost: %+v %v", p, err)
	}
}

func TestDuplicateBindAdvancesConnectedRunner(t *testing.T) {
	now := time.Unix(4750, 0)
	e, err := New(newMemoryRepo(), testRules(), nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Join("", "runner", "acct", now); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Bind("runner", "acct", now.Add(20*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Bind("runner", "acct", now.Add(40*time.Second)); err != nil {
		t.Fatal(err)
	}
	p, err := e.Status(AccountKey("acct"), "runner", now.Add(40*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if p.ProgressSeconds != 40 {
		t.Fatalf("duplicate bind progress = %d, want 40", p.ProgressSeconds)
	}
}

func TestFactionChoiceIsPermanent(t *testing.T) {
	now := time.Unix(5000, 0)
	e, err := New(newMemoryRepo(), testRules(), nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Join("", "runner", "acct", now); err != nil {
		t.Fatal(err)
	}
	p, err := e.SetFaction(AccountKey("acct"), "runner", FactionGhostline, now)
	if err != nil || p.Faction != FactionGhostline {
		t.Fatalf("faction choice: %+v %v", p, err)
	}
	if _, err = e.SetFaction(AccountKey("acct"), "runner", FactionNomad, now); err == nil {
		t.Fatal("second faction choice succeeded")
	}
	if _, err = e.SetFaction(AccountKey("acct"), "runner", "unknown", now); err == nil {
		t.Fatal("unknown faction succeeded")
	}
}

func TestCityEventProgressChangesAreMeaningful(t *testing.T) {
	for _, event := range cityEvents {
		if event.progressChange == 0 {
			t.Fatalf("city event %q has no progress effect", event.text)
		}
	}
	if got := formatProgressChange(-90); got != "-1m30s progress" {
		t.Fatalf("formatProgressChange(-90) = %q", got)
	}
}

func TestTypedCityEventEffects(t *testing.T) {
	sweep := cityEvent{kind: cityEventCorporateSweep, progressChange: -90}
	if got := cityEventProgressChange(sweep, &Player{Faction: FactionGhostline}); got != -45 {
		t.Fatalf("ghostline sweep change = %d, want -45", got)
	}
	if got := cityEventProgressChange(cityEvent{kind: cityEventBlackout, progressChange: -60}, &Player{District: DistrictOldTransit}); got != -120 {
		t.Fatalf("old transit blackout change = %d, want -120", got)
	}
	deckRunner := &Player{District: DistrictFloodline, Equipment: map[string]Item{SlotDeck: {Name: "Ghostline deck Mk 1", Rating: 1}}}
	leak := cityEvent{kind: cityEventDataLeak, progressChange: 120}
	if got := cityEventProgressChange(leak, deckRunner); got != 180 {
		t.Fatalf("floodline leak change = %d, want 180", got)
	}
	applyCityEventGear(leak, deckRunner)
	if deckRunner.Equipment[SlotDeck].Rating != 2 || !strings.Contains(deckRunner.Equipment[SlotDeck].Name, "leak-overclocked") {
		t.Fatalf("data leak gear effect = %+v", deckRunner.Equipment[SlotDeck])
	}
}

func TestRecentEventsReturnsNewestFirst(t *testing.T) {
	now := time.Unix(7000, 0)
	e, err := New(newMemoryRepo(), testRules(), nil, now)
	if err != nil {
		t.Fatal(err)
	}
	first, err := e.ForcePirate(now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.ForcePirate(now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	events := e.RecentEvents(2)
	if len(events) != 2 || events[0] != second || events[1] != first {
		t.Fatalf("recent events = %#v", events)
	}
}

func TestConnectedRunnerDriftsDistricts(t *testing.T) {
	now := time.Unix(8000, 0)
	rules := testRules()
	rules.EncounterInterval = 24 * time.Hour
	rules.CityEventInterval = 24 * time.Hour
	e, err := New(newMemoryRepo(), rules, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Join("", "runner", "acct", now); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Tick(now.Add(time.Hour + time.Minute)); err != nil {
		t.Fatal(err)
	}
	p, err := e.Status(AccountKey("acct"), "runner", now.Add(time.Hour+time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if p.District == DistrictNeonMarket || !validDistrict(p.District) || !p.NextDistrictAt.After(now) {
		t.Fatalf("district state = %+v", p)
	}
}

func TestDistrictEncounterModifiers(t *testing.T) {
	if districtEncounterBonus(DistrictCorporateArcology) <= districtEncounterBonus(DistrictNeonMarket) {
		t.Fatal("corporate arcology should improve ICE odds")
	}
	if districtEncounterBonus(DistrictGhostQuarter) >= districtEncounterBonus(DistrictNeonMarket) {
		t.Fatal("ghost quarter should worsen ICE odds")
	}
}

func TestUniqueGearSurvivesLevelUp(t *testing.T) {
	now := time.Unix(9000, 0)
	rules := testRules()
	e, err := New(newMemoryRepo(), rules, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	p := newPlayer(AccountKey("acct"), "runner", "acct", now)
	p.Connected = true
	p.Equipment[SlotWeaponRig] = Item{Name: "Prototype Mantis Rig", Rating: 9, Unique: true}
	e.advanceLocked(p, now.Add(time.Minute))
	if p.Level != 2 || p.Equipment[SlotWeaponRig].Name != "Prototype Mantis Rig" || !p.Equipment[SlotWeaponRig].Unique {
		t.Fatalf("unique gear was replaced: %+v", p)
	}
}

func TestRareLootAwardsUniqueArtifact(t *testing.T) {
	now := time.Unix(10000, 0)
	repo := newMemoryRepo()
	e, err := New(repo, testRules(), rand.New(rand.NewSource(1)), now)
	if err != nil {
		t.Fatal(err)
	}
	p := newPlayer(AccountKey("acct"), "runner", "acct", now)
	var item *Item
	for seed := int64(0); seed < 10000 && item == nil; seed++ {
		e.rng = rand.New(rand.NewSource(seed))
		item, err = e.rareLootLocked(p)
	}
	if err != nil || item == nil {
		t.Fatalf("rare loot award = %+v, %v", item, err)
	}
	if !item.Unique || item.Rating < 7 {
		t.Fatalf("rare item = %+v", item)
	}
	if repo.rares[item.Name] != p.Identity {
		t.Fatalf("rare item owner = %q, want %q", repo.rares[item.Name], p.Identity)
	}
	found := false
	for _, equipped := range p.Equipment {
		if equipped.Name == item.Name && equipped.Unique {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("rare item was not equipped: %+v", p.Equipment)
	}
}

func TestHeatRisesAndDecaysWhileConnected(t *testing.T) {
	now := time.Unix(11000, 0)
	rules := testRules()
	rules.HeatDecayInterval = 10 * time.Minute
	rules.EncounterInterval = 24 * time.Hour
	rules.CityEventInterval = 24 * time.Hour
	e, err := New(newMemoryRepo(), rules, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Join("", "runner", "acct", now); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Rename("runner", "runner2", now); err != nil {
		t.Fatal(err)
	}
	p, err := e.Status(AccountKey("acct"), "runner2", now)
	if err != nil || p.Heat != 4 {
		t.Fatalf("nick-change heat = %d, %v", p.Heat, err)
	}
	p, err = e.Status(AccountKey("acct"), "runner2", now.Add(21*time.Minute))
	if err != nil || p.Heat != 2 {
		t.Fatalf("decayed heat = %d, %v", p.Heat, err)
	}
}

func TestKickRaisesHeatAndHeatShapesLootChance(t *testing.T) {
	now := time.Unix(12000, 0)
	e, err := New(newMemoryRepo(), testRules(), nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Join("", "runner", "acct", now); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Disconnect("runner", ActivityKick, now); err != nil {
		t.Fatal(err)
	}
	p, err := e.Status(AccountKey("acct"), "runner", now)
	if err != nil || p.Heat != 15 {
		t.Fatalf("kick heat = %d, %v", p.Heat, err)
	}
	if rareLootChance(&Player{Heat: MaxHeat}) <= rareLootChance(&Player{}) {
		t.Fatal("high heat did not improve rare-loot odds")
	}
}
