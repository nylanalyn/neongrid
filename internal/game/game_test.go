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
func (r *memoryRepo) ReleaseRareItem(name string) error { delete(r.rares, name); return nil }
func (r *memoryRepo) TransferRareItem(name, owner string) error {
	r.rares[name] = owner
	return nil
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
		ContractDuration: time.Hour, ContractMaxParticipants: 2,
		CollisionInterval: time.Hour,
		GuestRetention:    24 * time.Hour,
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
	if _, err = e.SetAlias("", "meatbag42", "chicken-licker", now); err != nil {
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
	if bound.Guest || bound.Identity != AccountKey("nylan") || bound.ProgressSeconds != guest.ProgressSeconds || bound.Alias != "chicken-licker" {
		t.Fatalf("migration lost state: guest=%+v bound=%+v", guest, bound)
	}
}

func TestAliasFollowsAccountAcrossNickChanges(t *testing.T) {
	now := time.Unix(3250, 0)
	e, err := New(newMemoryRepo(), testRules(), nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Join("", "rumi", "acct", now); err != nil {
		t.Fatal(err)
	}
	if _, err = e.SetAlias(AccountKey("acct"), "rumi", "chicken-licker", now); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Rename("rumi", "rumi2", now); err != nil {
		t.Fatal(err)
	}
	p, err := e.Status(AccountKey("acct"), "rumi2", now)
	if err != nil || p.Alias != "chicken-licker" || p.DisplayName() != "chicken-licker" {
		t.Fatalf("alias after nick change = %+v, %v", p, err)
	}
	if _, err = e.SetAlias(AccountKey("acct"), "rumi2", "bad name", now); err == nil {
		t.Fatal("invalid alias was accepted")
	}
	if _, err = e.SetAlias(AccountKey("acct"), "rumi2", "", now); err != nil {
		t.Fatal(err)
	}
	p, err = e.Status(AccountKey("acct"), "rumi2", now)
	if err != nil || p.DisplayName() != "rumi2" {
		t.Fatalf("cleared alias = %+v, %v", p, err)
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

func TestGuestRenameOntoOfflineGuestNickKeepsBothRunners(t *testing.T) {
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
	if _, err = e.Rename("alpha", "beta", now); err != nil {
		t.Fatalf("rename onto an offline guest's nick failed: %v", err)
	}
	if offline := e.users[GuestKey("beta")]; offline == nil || offline.Connected {
		t.Fatalf("offline guest was overwritten: %+v", offline)
	}
	// The renamed runner must still be found by its new nick, so its QUIT lands.
	if _, err = e.Disconnect("beta", ActivityQuit, now.Add(time.Minute)); err != nil {
		t.Fatalf("quit after rename was not applied: %v", err)
	}
	if runner := e.users[GuestKey("alpha")]; runner == nil || runner.Connected {
		t.Fatalf("renamed runner is still connected after quitting: %+v", runner)
	}
}

func TestStaleNickDoesNotShadowLiveRunner(t *testing.T) {
	// Map iteration order is random, so repeat to catch order-dependent lookups.
	for i := 0; i < 50; i++ {
		now := time.Unix(4600, 0)
		e, err := New(newMemoryRepo(), testRules(), nil, now)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = e.Join("", "bob", "alice", now); err != nil {
			t.Fatal(err)
		}
		if _, err = e.Disconnect("bob", ActivityQuit, now); err != nil {
			t.Fatal(err)
		}
		if _, err = e.Join("", "bob", "", now); err != nil {
			t.Fatal(err)
		}
		if _, err = e.SetFaction("", "bob", FactionNomad, now); err != nil {
			t.Fatal(err)
		}
		if _, err = e.SetAlias("", "bob", "imposter", now); err != nil {
			t.Fatal(err)
		}
		if alice := e.users[AccountKey("alice")]; alice.Faction != "" || alice.Alias != "" {
			t.Fatalf("guest command changed an offline account runner: %+v", alice)
		}
		if _, err = e.Disconnect("bob", ActivityQuit, now.Add(time.Minute)); err != nil {
			t.Fatalf("guest quit was not applied: %v", err)
		}
		if e.users[GuestKey("bob")].Connected {
			t.Fatal("guest stayed connected after quitting")
		}
	}
}

func TestAccountSwitchDoesNotTakeOverRunner(t *testing.T) {
	now := time.Unix(4700, 0)
	e, err := New(newMemoryRepo(), testRules(), nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Join("", "bob", "alice", now.Add(-30*time.Second)); err != nil {
		t.Fatal(err)
	}
	carol, err := e.Bind("bob", "carol", now)
	if err != nil {
		t.Fatal(err)
	}
	alice := e.users[AccountKey("alice")]
	if alice == nil || alice.Identity != AccountKey("alice") || alice.Connected {
		t.Fatalf("previous account runner = %+v", alice)
	}
	if carol.Identity != AccountKey("carol") || carol.ProgressSeconds != 0 || !carol.Connected {
		t.Fatalf("new account runner inherited state: %+v", carol)
	}
	if alice.ProgressSeconds != 30 {
		t.Fatalf("previous account progress = %d, want 30", alice.ProgressSeconds)
	}
}

func TestOfflineGuestOnlyMigratesIntoMatchingAccount(t *testing.T) {
	now := time.Unix(4800, 0)
	e, err := New(newMemoryRepo(), testRules(), nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Join("", "bob", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Tick(now.Add(50 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Disconnect("bob", ActivityQuit, now.Add(50*time.Second)); err != nil {
		t.Fatal(err)
	}
	stranger, err := e.Join("", "bob", "carol", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if stranger.ProgressSeconds != 0 || e.users[GuestKey("bob")] == nil {
		t.Fatalf("unrelated account absorbed offline guest: %+v", stranger)
	}
	if _, err = e.Disconnect("bob", ActivityQuit, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	owner, err := e.Join("", "bob", "bob", now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if owner.ProgressSeconds == 0 || e.users[GuestKey("bob")] != nil {
		t.Fatalf("nick owner did not inherit their guest runner: %+v", owner)
	}
}

func TestLevelUpsFromCommandsAreAnnouncedOnNextTick(t *testing.T) {
	now := time.Unix(4900, 0)
	e, err := New(newMemoryRepo(), testRules(), nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Join("", "rumi", "rumi", now); err != nil {
		t.Fatal(err)
	}
	later := now.Add(61 * time.Second)
	if _, err = e.Status(AccountKey("rumi"), "rumi", later); err != nil {
		t.Fatal(err)
	}
	messages, err := e.Tick(later)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0] != "[GRID] rumi hit Rep 2. Installed Mono-edge weapon rig Mk 1." {
		t.Fatalf("tick messages = %#v", messages)
	}
	if messages, _ = e.Tick(later); len(messages) != 0 {
		t.Fatalf("level-up was announced twice: %#v", messages)
	}
}

func TestSnapshotDoesNotMutateOrConsumeAnnouncements(t *testing.T) {
	now := time.Unix(4950, 0)
	repo := newMemoryRepo()
	e, err := New(repo, testRules(), nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Join("", "rumi", "rumi", now); err != nil {
		t.Fatal(err)
	}
	later := now.Add(61 * time.Second)
	players, _, err := e.Snapshot(later)
	if err != nil || len(players) != 1 || players[0].Level != 2 {
		t.Fatalf("snapshot = %+v, %v", players, err)
	}
	if e.users[AccountKey("rumi")].Level != 1 || repo.players[AccountKey("rumi")].Level != 1 {
		t.Fatal("snapshot changed engine or stored state")
	}
	messages, err := e.Tick(later)
	if err != nil || len(messages) != 1 || !strings.Contains(messages[0], "Rep 2. Installed") {
		t.Fatalf("tick after snapshot = %#v, %v", messages, err)
	}
}

func TestMultiLevelJumpIsOneAnnouncement(t *testing.T) {
	p := &Player{Nick: "rumi", Level: 1, Connected: true, LastProgressAt: time.Unix(0, 0), LastHeatAt: time.Unix(0, 0), Equipment: map[string]Item{}}
	rules := testRules()
	rules.HeatDecayInterval = time.Hour
	messages := advancePlayer(p, time.Unix(250, 0), rules)
	if p.Level != 4 || len(messages) != 1 || !strings.Contains(messages[0], "surged to Rep 4 (+3)") {
		t.Fatalf("level=%d messages=%#v", p.Level, messages)
	}
}

func TestTitlesAreAnnounced(t *testing.T) {
	now := time.Unix(4980, 0)
	e, err := New(newMemoryRepo(), testRules(), nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Join("", "rumi", "rumi", now); err != nil {
		t.Fatal(err)
	}
	e.users[AccountKey("rumi")].Heat = 80
	messages, err := e.Tick(now)
	if err != nil || len(messages) != 1 || messages[0] != "[GRID] rumi earned the title Corporate Liability." {
		t.Fatalf("tick messages = %#v, %v", messages, err)
	}
}

func TestAliasMustBeUnique(t *testing.T) {
	now := time.Unix(4990, 0)
	e, err := New(newMemoryRepo(), testRules(), nil, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, account := range []string{"one", "two"} {
		if _, err = e.Join("", account, account, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = e.SetAlias(AccountKey("one"), "one", "Razor", now); err != nil {
		t.Fatal(err)
	}
	for _, alias := range []string{"razor", "ONE"} {
		if _, err = e.SetAlias(AccountKey("two"), "two", alias, now); err == nil {
			t.Fatalf("alias %q duplicated another runner's name", alias)
		}
	}
	if _, err = e.SetAlias(AccountKey("one"), "one", "razor", now); err != nil {
		t.Fatalf("runner could not re-case their own alias: %v", err)
	}
	if _, err = e.SetAlias(AccountKey("two"), "two", "two", now); err != nil {
		t.Fatalf("runner could not use their own nick as alias: %v", err)
	}
}

func TestBotOutageAndNetsplitDoNotFailContracts(t *testing.T) {
	now := time.Unix(5000, 0)
	e, err := New(newMemoryRepo(), testRules(), nil, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, nick := range []string{"one", "two"} {
		if _, err = e.Join("", nick, nick, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, started, err := e.startContractLocked(now); err != nil || !started {
		t.Fatalf("contract did not start: %v", err)
	}
	if err = e.DisconnectAll(now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	for _, nick := range []string{"one", "two"} {
		if _, err = e.Join("", nick, nick, now.Add(2*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	penalty, err := e.Disconnect("two", ActivityNetsplit, now.Add(3*time.Minute))
	if err != nil || penalty != 0 || e.users[AccountKey("two")].Heat != 0 {
		t.Fatalf("netsplit penalty=%d heat=%d err=%v", penalty, e.users[AccountKey("two")].Heat, err)
	}
	if e.world.Contract.Failed {
		t.Fatal("bot outage or netsplit failed the contract")
	}
	if _, err = e.Join("", "two", "two", now.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	message, err := e.resolveContractLocked(now.Add(2 * time.Hour))
	if err != nil || !strings.Contains(message, "CONTRACT COMPLETE") {
		t.Fatalf("contract = %q, %v", message, err)
	}
}

func TestReconcileDropsGhostRunnersAfterGrace(t *testing.T) {
	now := time.Unix(5100, 0)
	e, err := New(newMemoryRepo(), testRules(), nil, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, nick := range []string{"here", "ghost"} {
		if _, err = e.Join("", nick, "", now); err != nil {
			t.Fatal(err)
		}
	}
	present := func(nick string) bool { return nick == "here" }
	if err = e.Reconcile(present, 30*time.Second, now); err != nil {
		t.Fatal(err)
	}
	if !e.users[GuestKey("ghost")].Connected {
		t.Fatal("runner dropped before the grace period")
	}
	if err = e.Reconcile(present, 30*time.Second, now.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	if e.users[GuestKey("ghost")].Connected || !e.users[GuestKey("here")].Connected {
		t.Fatal("reconcile dropped the wrong runners")
	}
	if e.users[GuestKey("ghost")].ProgressSeconds != 30 {
		t.Fatalf("ghost progress = %d, want 30 with no penalty", e.users[GuestKey("ghost")].ProgressSeconds)
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

func TestSystemCrashAllowsOneFactionRespec(t *testing.T) {
	now := time.Unix(5250, 0)
	rules := testRules()
	rules.EncounterInterval = 72 * time.Hour
	rules.CityEventInterval = 72 * time.Hour
	rules.DistrictInterval = 72 * time.Hour
	rules.ContractDuration = 72 * time.Hour
	e, err := New(newMemoryRepo(), rules, rand.New(rand.NewSource(2)), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Join("", "runner", "acct", now); err != nil {
		t.Fatal(err)
	}
	if _, err = e.SetFaction(AccountKey("acct"), "runner", FactionGhostline, now); err != nil {
		t.Fatal(err)
	}
	e.world.NextFactionSwapAt = now
	messages, err := e.Tick(now)
	if err != nil || len(messages) == 0 || !strings.Contains(messages[0], "SYSTEM CRASH") {
		t.Fatalf("system crash start = %#v, %v", messages, err)
	}
	if _, err = e.SetFaction(AccountKey("acct"), "runner", FactionNomad, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err = e.SetFaction(AccountKey("acct"), "runner", FactionChrome, now.Add(2*time.Hour)); err == nil {
		t.Fatal("second respec succeeded during one crash")
	}
	p, err := e.Status(AccountKey("acct"), "runner", now.Add(2*time.Hour))
	if err != nil || p.Faction != FactionNomad {
		t.Fatalf("respec faction = %+v, %v", p, err)
	}
	messages, err = e.Tick(now.Add(25 * time.Hour))
	if err != nil || len(messages) == 0 || !strings.Contains(messages[0], "SYSTEM RESTORED") {
		t.Fatalf("system crash end = %#v, %v", messages, err)
	}
	if _, err = e.SetFaction(AccountKey("acct"), "runner", FactionChrome, now.Add(26*time.Hour)); err == nil {
		t.Fatal("respec succeeded after crash ended")
	}
}

func TestCityEventProgressChangesAreMeaningful(t *testing.T) {
	for _, event := range cityEvents {
		if event.progressChange == 0 {
			t.Fatalf("city event %q has no progress effect", event.kind)
		}
		if len(cityEventTexts[event.kind]) == 0 {
			t.Fatalf("city event %q has no announcement text", event.kind)
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
	if got := cityEventProgressChange(cityEvent{kind: cityEventCorporateSweep, progressChange: -90}, &Player{Scars: []string{ScarCorporateBackdoor}}); got != -120 {
		t.Fatalf("backdoor sweep change = %d, want -120", got)
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
		item, err = e.rareLootLocked(p, "")
	}
	if err != nil || item == nil {
		t.Fatalf("rare loot award = %+v, %v", item, err)
	}
	if !item.Unique || item.Rating <= gearTier(p.Level) || item.BreaksAt < p.Level+uniqueMinLife {
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

func TestContractCompletesForConnectedTeam(t *testing.T) {
	now := time.Unix(13000, 0)
	rules := testRules()
	rules.ContractDuration = time.Hour
	rules.EncounterInterval = 24 * time.Hour
	rules.CityEventInterval = 24 * time.Hour
	rules.DistrictInterval = 24 * time.Hour
	e, err := New(newMemoryRepo(), rules, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Join("", "alpha", "acct-alpha", now); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Join("", "beta", "acct-beta", now); err != nil {
		t.Fatal(err)
	}
	contractMessage, started, err := e.startContractLocked(now)
	if err != nil || !started || !strings.Contains(contractMessage, "CONTRACT:") {
		t.Fatalf("contract start = %q, %v, %v", contractMessage, started, err)
	}
	if len(e.world.Contract.Participants) != 2 {
		t.Fatalf("contract team = %#v", e.world.Contract.Participants)
	}
	message, err := e.resolveContractLocked(now.Add(time.Hour + time.Minute))
	if err != nil || !strings.Contains(message, "CONTRACT COMPLETE") || e.world.Contract != nil {
		t.Fatalf("contract completion = %q, %v, %#v", message, err, e.world.Contract)
	}
}

func TestContractFailsWhenRunnerDisconnects(t *testing.T) {
	now := time.Unix(14000, 0)
	rules := testRules()
	rules.ContractDuration = time.Hour
	e, err := New(newMemoryRepo(), rules, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Join("", "runner", "acct", now); err != nil {
		t.Fatal(err)
	}
	if _, started, err := e.startContractLocked(now); err != nil || !started {
		t.Fatalf("contract did not start: %v", err)
	}
	if _, err = e.Disconnect("runner", ActivityPart, now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if e.world.Contract == nil || !e.world.Contract.Failed {
		t.Fatalf("disconnect did not fail contract: %#v", e.world.Contract)
	}
	message, err := e.resolveContractLocked(now.Add(2 * time.Hour))
	if err != nil || !strings.Contains(message, "CONTRACT FAILED") || e.world.Contract != nil {
		t.Fatalf("contract failure = %q, %v, %#v", message, err, e.world.Contract)
	}
}

func TestConnectedRunnersCollideAndCooldown(t *testing.T) {
	now := time.Unix(15000, 0)
	rules := testRules()
	rules.EncounterInterval = 24 * time.Hour
	rules.CityEventInterval = 24 * time.Hour
	rules.DistrictInterval = 24 * time.Hour
	rules.ContractDuration = 24 * time.Hour
	repo := newMemoryRepo()
	e, err := New(repo, rules, rand.New(rand.NewSource(1)), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Join("", "alpha", "acct-alpha", now); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Join("", "beta", "acct-beta", now); err != nil {
		t.Fatal(err)
	}
	e.users[AccountKey("acct-alpha")].NextCollisionAt = now
	e.users[AccountKey("acct-beta")].NextCollisionAt = now

	messages, err := e.Tick(now)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, message := range messages {
		found = found || strings.Contains(message, "COLLISION:")
	}
	if !found {
		t.Fatalf("collision was not announced: %#v", messages)
	}
	for _, key := range []string{AccountKey("acct-alpha"), AccountKey("acct-beta")} {
		p := e.users[key]
		if !p.NextCollisionAt.After(now) || p.Heat == 0 {
			t.Fatalf("collision state for %s = %+v", key, p)
		}
	}

	messages, err = e.Tick(now)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range messages {
		if strings.Contains(message, "COLLISION:") {
			t.Fatalf("collision ignored cooldown: %#v", messages)
		}
	}
}

func TestCollisionPowerAndLossUseRunnerBuild(t *testing.T) {
	weak := &Player{Level: 1}
	strong := &Player{
		Level: 1, Heat: MaxHeat, Faction: FactionGhostline, District: DistrictCorporateArcology,
		Equipment: map[string]Item{SlotDeck: {Rating: 5}},
	}
	if collisionPower(strong) <= collisionPower(weak) {
		t.Fatalf("strong runner power = %d, weak runner power = %d", collisionPower(strong), collisionPower(weak))
	}
	if collisionPower(&Player{Level: 1, Scars: []string{ScarSyntheticAdrenalGland}}) <= collisionPower(weak) {
		t.Fatal("synthetic adrenal gland did not improve collision power")
	}
	if collisionPower(&Player{Level: 1, Scars: []string{ScarBurnedOptic}}) >= collisionPower(weak) {
		t.Fatal("burned optic did not reduce collision power")
	}
	if got := collisionLoss(&Player{Faction: FactionNomad}, 100); got >= 100 {
		t.Fatalf("nomad collision loss = %d, want mitigation", got)
	}
}

func TestScarsAndTitlesAreUniqueAndAccomplishmentBased(t *testing.T) {
	p := &Player{Level: 5}
	if !addScar(p, ScarGhostSignal) || addScar(p, ScarGhostSignal) || len(p.Scars) != 1 {
		t.Fatalf("scar uniqueness failed: %#v", p.Scars)
	}
	updateTitles(p)
	if p.CurrentTitle() != "ICEbreaker" {
		t.Fatalf("level title = %q, want ICEbreaker", p.CurrentTitle())
	}
	p.Heat = 75
	updateTitles(p)
	if p.CurrentTitle() != "Corporate Liability" {
		t.Fatalf("heat title = %q, want Corporate Liability", p.CurrentTitle())
	}
	updateTitles(p)
	if len(p.Titles) != 2 {
		t.Fatalf("duplicate titles were added: %#v", p.Titles)
	}
}

func TestExpectedGearMatchesStandardProgression(t *testing.T) {
	p := &Player{Level: 1, Connected: true, LastProgressAt: time.Unix(0, 0), LastHeatAt: time.Unix(0, 0), Equipment: map[string]Item{}}
	rules := testRules()
	rules.HeatDecayInterval = time.Hour
	now := time.Unix(0, 0)
	for level := 1; level <= 40; level++ {
		if got, want := p.EquipmentRating(), expectedGearRating(p.Level); got != want {
			t.Fatalf("level %d: standard gear rating %d, expected %d", p.Level, got, want)
		}
		now = now.Add(time.Duration(rules.LevelDuration(p.Level)) * time.Second)
		advancePlayer(p, now, rules)
	}
}

func TestIceOddsScaleWithLevel(t *testing.T) {
	onLevel := func(level int) *Player {
		p := &Player{Level: 1, Connected: true, LastProgressAt: time.Unix(0, 0), LastHeatAt: time.Unix(0, 0), Equipment: map[string]Item{}}
		rules := testRules()
		rules.HeatDecayInterval = time.Hour
		for p.Level < level {
			p.ProgressSeconds = rules.LevelDuration(p.Level)
			p.LastProgressAt = time.Unix(0, 0)
			advancePlayer(p, time.Unix(0, 0), rules)
		}
		return p
	}
	low, high := onLevel(3), onLevel(60)
	if iceWinChance(low, "") != iceBaseChance || iceWinChance(high, "") != iceBaseChance {
		t.Fatalf("on-level odds: Rep 3 = %d%%, Rep 60 = %d%%, want %d%% for both", iceWinChance(low, ""), iceWinChance(high, ""), iceBaseChance)
	}
	high.Equipment[SlotDeck] = Item{Name: "old", Rating: 1}
	if iceWinChance(high, "") >= iceBaseChance {
		t.Fatal("stale gear did not hurt ICE odds")
	}
	low.Faction = FactionGhostline
	if iceWinChance(low, "") <= iceBaseChance {
		t.Fatal("Ghostline did not improve ICE odds")
	}
	low.Faction, low.Heat = "", MaxHeat
	if iceWinChance(low, "") >= iceBaseChance {
		t.Fatal("Heat did not hurt ICE odds")
	}
	if got := iceWinChance(&Player{Level: 50, Equipment: map[string]Item{}}, ""); got != 10 {
		t.Fatalf("ungeared Rep 50 odds = %d%%, want floor of 10%%", got)
	}
}

func TestUniqueBurnsOutAndReturnsToPool(t *testing.T) {
	now := time.Unix(20000, 0)
	repo := newMemoryRepo()
	e, err := New(repo, testRules(), rand.New(rand.NewSource(1)), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Join("", "runner", "acct", now); err != nil {
		t.Fatal(err)
	}
	p := e.users[AccountKey("acct")]
	p.Level = 12
	repo.rares["Blackglass Deck"] = p.Identity
	p.Equipment[SlotDeck] = Item{Name: "Blackglass Deck", Rating: 9, Unique: true, BreaksAt: 13}
	if _, err = e.Tick(now); err != nil {
		t.Fatal(err)
	}
	if !p.Equipment[SlotDeck].Unique {
		t.Fatal("unique burned out before its burn-out level")
	}
	p.Level = 13
	messages, err := e.Tick(now)
	if err != nil {
		t.Fatal(err)
	}
	deck := p.Equipment[SlotDeck]
	if deck.Unique || deck.Rating != gearTier(13) || deck.Name != equipmentName(SlotDeck, gearTier(13)) {
		t.Fatalf("burned-out slot = %+v, want standard on-level gear", deck)
	}
	if _, claimed := repo.rares["Blackglass Deck"]; claimed {
		t.Fatal("burned-out artifact was not returned to the pool")
	}
	if len(messages) != 1 || !strings.Contains(messages[0], "Blackglass Deck burned out") {
		t.Fatalf("burn-out messages = %#v", messages)
	}
}

func TestBurnedOutUniqueCannotBeSalvagedBack(t *testing.T) {
	now := time.Unix(20100, 0)
	repo := newMemoryRepo()
	e, err := New(repo, testRules(), nil, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range rareItems {
		if item.name != "Blackglass Deck" {
			repo.rares[item.name] = "acct:someone"
		}
	}
	p := newPlayer(AccountKey("acct"), "runner", "acct", now)
	p.Heat = MaxHeat
	for seed := int64(0); seed < 1000; seed++ {
		e.rng = rand.New(rand.NewSource(seed))
		if item, err := e.rareLootLocked(p, "Blackglass Deck"); err != nil || item != nil {
			t.Fatalf("excluded artifact dropped: %+v, %v", item, err)
		}
	}
}

func TestLegacyUniquesGetABurnOutLevel(t *testing.T) {
	now := time.Unix(20200, 0)
	repo := newMemoryRepo()
	legacy := newPlayer(AccountKey("acct"), "runner", "acct", now)
	legacy.Level = 20
	legacy.Equipment[SlotDeck] = Item{Name: "Blackglass Deck", Rating: 8, Unique: true}
	repo.players[legacy.Identity] = legacy
	e, err := New(repo, testRules(), nil, now)
	if err != nil {
		t.Fatal(err)
	}
	breaksAt := e.users[legacy.Identity].Equipment[SlotDeck].BreaksAt
	if breaksAt < 20+uniqueMinLife || breaksAt >= 20+uniqueMinLife+uniqueLifeRange {
		t.Fatalf("legacy unique burns out at Rep %d", breaksAt)
	}
}

func TestShownTitleIsMostPrestigiousOrChosen(t *testing.T) {
	now := time.Unix(20300, 0)
	e, err := New(newMemoryRepo(), testRules(), nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Join("", "runner", "acct", now); err != nil {
		t.Fatal(err)
	}
	p := e.users[AccountKey("acct")]
	p.Level, p.District, p.Stats.IceWins = 12, DistrictFloodline, 25
	updateTitles(p)
	if got := p.CurrentTitle(); got != "ICE Surfer" {
		t.Fatalf("shown title = %q, want ICE Surfer (%v)", got, p.Titles)
	}
	if _, err = e.SetTitle(AccountKey("acct"), "runner", "ghost of floodline", now); err != nil {
		t.Fatal(err)
	}
	if got := p.CurrentTitle(); got != "Ghost of Floodline" {
		t.Fatalf("chosen title = %q", got)
	}
	if _, err = e.SetTitle(AccountKey("acct"), "runner", "Ghost in the Machine", now); err == nil {
		t.Fatal("unearned title was accepted")
	}
	if _, err = e.SetTitle(AccountKey("acct"), "runner", "auto", now); err != nil || p.CurrentTitle() != "ICE Surfer" {
		t.Fatalf("auto title = %q, %v", p.CurrentTitle(), err)
	}
}

func TestRejoinStaggersOverdueTimers(t *testing.T) {
	now := time.Unix(20400, 0)
	rules := testRules()
	rules.CityEventInterval = 72 * time.Hour
	e, err := New(newMemoryRepo(), rules, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, nick := range []string{"one", "two"} {
		if _, err = e.Join("", nick, nick, now); err != nil {
			t.Fatal(err)
		}
		if _, err = e.Disconnect(nick, ActivityQuit, now); err != nil {
			t.Fatal(err)
		}
	}
	back := now.Add(3 * time.Hour)
	for _, nick := range []string{"one", "two"} {
		if _, err = e.Join("", nick, nick, back); err != nil {
			t.Fatal(err)
		}
		p := e.users[AccountKey(nick)]
		for name, at := range map[string]time.Time{"encounter": p.NextEncounterAt, "district": p.NextDistrictAt, "collision": p.NextCollisionAt} {
			if at.Before(back.Add(15 * time.Minute)) {
				t.Fatalf("%s %s timer due %s after rejoin, want at least a quarter interval", nick, name, at.Sub(back))
			}
		}
	}
	e.pending = nil
	if messages, err := e.Tick(back); err != nil || len(messages) != 0 {
		t.Fatalf("rejoin tick fired events: %#v, %v", messages, err)
	}
}

func TestAbandonedUniquesReturnToPool(t *testing.T) {
	now := time.Unix(30000, 0)
	rules := testRules()
	rules.ArtifactOfflineRelease = 7 * 24 * time.Hour
	rules.CityEventInterval = 30 * 24 * time.Hour
	rules.GuestRetention = 30 * 24 * time.Hour
	repo := newMemoryRepo()
	e, err := New(repo, rules, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, nick := range []string{"gone", "brief"} {
		if _, err = e.Join("", nick, nick, now); err != nil {
			t.Fatal(err)
		}
	}
	gone, brief := e.users[AccountKey("gone")], e.users[AccountKey("brief")]
	gone.Level, brief.Level = 10, 10
	gone.Equipment[SlotDeck] = Item{Name: "Blackglass Deck", Rating: 7, Unique: true, BreaksAt: 14}
	brief.Equipment[SlotDrone] = Item{Name: "Whisperbyte Scout", Rating: 6, Unique: true, BreaksAt: 14}
	repo.rares["Blackglass Deck"], repo.rares["Whisperbyte Scout"] = gone.Identity, brief.Identity
	if _, err = e.Disconnect("gone", ActivityQuit, now); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Disconnect("brief", ActivityQuit, now.Add(2*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	later := now.Add(7*24*time.Hour + time.Minute)
	messages, err := e.Tick(later)
	if err != nil {
		t.Fatal(err)
	}
	if deck := gone.Equipment[SlotDeck]; deck.Unique || deck.Rating != gearTier(10) {
		t.Fatalf("abandoned slot = %+v, want standard on-level gear", deck)
	}
	if _, claimed := repo.rares["Blackglass Deck"]; claimed {
		t.Fatal("abandoned artifact was not returned to the pool")
	}
	if !brief.Equipment[SlotDrone].Unique || repo.rares["Whisperbyte Scout"] != brief.Identity {
		t.Fatal("artifact released before its owner passed the offline limit")
	}
	var released []string
	for _, message := range messages {
		if strings.Contains(message, "went dark") {
			released = append(released, message)
		}
	}
	if len(released) != 1 || !strings.Contains(released[0], "gone's UNIQUE Blackglass Deck went dark") {
		t.Fatalf("release messages = %#v", released)
	}
}

func TestConnectedRunnersStaySeenAcrossBotCrash(t *testing.T) {
	now := time.Unix(31000, 0)
	rules := testRules()
	rules.ArtifactOfflineRelease = 7 * 24 * time.Hour
	rules.EncounterInterval, rules.DistrictInterval, rules.CollisionInterval = 30*24*time.Hour, 30*24*time.Hour, 30*24*time.Hour
	rules.CityEventInterval = 30 * 24 * time.Hour
	repo := newMemoryRepo()
	e, err := New(repo, rules, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Join("", "idler", "idler", now); err != nil {
		t.Fatal(err)
	}
	e.users[AccountKey("idler")].Equipment[SlotDeck] = Item{Name: "Blackglass Deck", Rating: 7, Unique: true, BreaksAt: 1000}
	// Ten days of idling, then the bot crashes without a clean disconnect.
	crash := now.Add(10 * 24 * time.Hour)
	if _, err = e.Tick(crash); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(repo, rules, nil, crash.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = restarted.Tick(crash.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if !restarted.users[AccountKey("idler")].Equipment[SlotDeck].Unique {
		t.Fatal("a long-connected runner lost an artifact after a bot crash")
	}
}
