package game

import (
	"sort"
	"testing"
	"time"
)

type memoryRepo struct {
	players map[string]*Player
	world   WorldState
}

func newMemoryRepo() *memoryRepo { return &memoryRepo{players: map[string]*Player{}} }

func (r *memoryRepo) LoadAll() ([]*Player, error) {
	var out []*Player
	for _, p := range r.players {
		out = append(out, clonePlayer(p))
	}
	return out, nil
}
func (r *memoryRepo) Save(p *Player) error    { r.players[p.Identity] = clonePlayer(p); return nil }
func (r *memoryRepo) Delete(key string) error { delete(r.players, key); return nil }
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
		EncounterInterval: time.Hour, CityEventInterval: time.Hour, PirateDuration: time.Minute,
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
