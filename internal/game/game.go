package game

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"
)

var ErrRunnerNotFound = errors.New("runner not found")

const MaxHeat = 100

const (
	factionSwapDuration = 24 * time.Hour
	factionSwapWeek     = 7 * 24 * time.Hour
)

const (
	ActivityChat Activity = iota
	ActivityAction
	ActivityNick
	ActivityPart
	ActivityQuit
	ActivityKick
	// ActivityNetsplit is a server-side disconnect: no penalty, Heat, or contract failure.
	ActivityNetsplit
)

type Activity int

const (
	SlotWeaponRig     = "weapon_rig"
	SlotArmorPlating  = "armor_plating"
	SlotNeuralImplant = "neural_implant"
	SlotDeck          = "deck"
	SlotDrone         = "drone"
)

const (
	FactionGhostline = "ghostline"
	FactionChrome    = "chrome"
	FactionNomad     = "nomad"
)

const (
	ScarBurnedOptic           = "Burned Optic"
	ScarGhostSignal           = "Ghost Signal"
	ScarCorporateBackdoor     = "Corporate Backdoor"
	ScarSyntheticAdrenalGland = "Synthetic Adrenal Gland"
)

const (
	DistrictNeonMarket        = "Neon Market"
	DistrictFloodline         = "Floodline"
	DistrictCorporateArcology = "Corporate Arcology"
	DistrictOldTransit        = "Old Transit"
	DistrictGhostQuarter      = "Ghost Quarter"
)

var districts = []string{
	DistrictNeonMarket,
	DistrictFloodline,
	DistrictCorporateArcology,
	DistrictOldTransit,
	DistrictGhostQuarter,
}

var equipmentSlots = []string{
	SlotWeaponRig,
	SlotArmorPlating,
	SlotNeuralImplant,
	SlotDeck,
	SlotDrone,
}

type Item struct {
	Name   string `json:"name"`
	Rating int    `json:"rating"`
	Unique bool   `json:"unique,omitempty"`
	// BreaksAt is the Rep level at which a unique burns out and returns to
	// the drop pool.
	BreaksAt int `json:"breaks_at,omitempty"`
}

type Player struct {
	Identity          string
	Account           string
	Nick              string
	Alias             string
	Guest             bool
	Level             int
	ProgressSeconds   int64
	LastProgressAt    time.Time
	Connected         bool
	LastSeenAt        time.Time
	NextEncounterAt   time.Time
	NextCollisionAt   time.Time
	District          string
	NextDistrictAt    time.Time
	Heat              int
	LastHeatAt        time.Time
	Faction           string
	LastFactionSwapAt time.Time
	Equipment         map[string]Item
	Scars             []string
	Titles            []string
	// ChosenTitle is the earned title the runner picked with !title; empty
	// shows the most prestigious one.
	ChosenTitle string
	Stats       RunnerStats
	// Stance is "", StanceHot, or StanceCold; see SetStance.
	Stance string
	// Rivals counts collisions against each opponent identity.
	Rivals map[string]int
	// StreakSince is when the current unbroken link began; StreakDays is how
	// many full days of it have already paid out.
	StreakSince time.Time
	StreakDays  int
}

// RunnerStats counts milestones that titles are awarded for.
type RunnerStats struct {
	IceWins       int `json:"ice_wins,omitempty"`
	CollisionWins int `json:"collision_wins,omitempty"`
	Contracts     int `json:"contracts,omitempty"`
	Uniques       int `json:"uniques,omitempty"`
	Thefts        int `json:"thefts,omitempty"`
	DeadDrops     int `json:"dead_drops,omitempty"`
	RaidWins      int `json:"raid_wins,omitempty"`
	LongestStreak int `json:"longest_streak,omitempty"`
}

type WorldState struct {
	PirateUntil          time.Time
	NextCityEventAt      time.Time
	FactionSwapUntil     time.Time
	FactionSwapStartedAt time.Time
	NextFactionSwapAt    time.Time
	RecentEvents         []string
	Contract             *Contract
	FactionWeek          FactionWeek
	// DeadDrop is the clean code of the unclaimed dead drop hidden in the
	// current pirate frequency, if any.
	DeadDrop string
	Raid     *Raid
	Bulletin Bulletin
}

type Contract struct {
	Title        string
	District     string
	Participants []string
	EndsAt       time.Time
	Failed       bool
	// DroppedBy names the participant whose disconnect failed the contract.
	DroppedBy string
}

type cityEventKind string

const (
	cityEventBlackout       cityEventKind = "blackout"
	cityEventCorporateSweep cityEventKind = "corporate_sweep"
	cityEventDataLeak       cityEventKind = "data_leak"
	cityEventGangWar        cityEventKind = "gang_war"
	cityEventBounty         cityEventKind = "bounty"
	cityEventMegacorpRun    cityEventKind = "megacorp_run"
)

type cityEvent struct {
	kind           cityEventKind
	progressChange int64
}

// Announcement text for each kind lives in cityEventTexts (flavor.go).
var cityEvents = []cityEvent{
	{kind: cityEventBlackout, progressChange: -60},
	{kind: cityEventCorporateSweep, progressChange: -90},
	{kind: cityEventDataLeak, progressChange: 120},
	{kind: cityEventGangWar, progressChange: -120},
	{kind: cityEventBounty, progressChange: 90},
	{kind: cityEventMegacorpRun, progressChange: 180},
}

type Rules struct {
	GameChannel               string
	BaseLevelSeconds          int64
	LevelStepSeconds          int64
	SpeechBaseSeconds         int64
	SpeechPerLevelSeconds     int64
	SpeechPerCharacterSeconds int64
	ActionBaseSeconds         int64
	ActionPerLevelSeconds     int64
	ActionPerCharacterSeconds int64
	NickPenaltySeconds        int64
	PartPenaltySeconds        int64
	QuitPenaltySeconds        int64
	KickPenaltySeconds        int64
	EncounterInterval         time.Duration
	CityEventInterval         time.Duration
	DistrictInterval          time.Duration
	HeatDecayInterval         time.Duration
	ContractDuration          time.Duration
	ContractMaxParticipants   int
	CollisionInterval         time.Duration
	PirateDuration            time.Duration
	GuestRetention            time.Duration
	// ArtifactOfflineRelease is how long a runner can be offline before their
	// uniques return to the drop pool.
	ArtifactOfflineRelease time.Duration
}

type Repository interface {
	LoadAll() ([]*Player, error)
	Save(*Player) error
	Delete(string) error
	ClaimRareItem(name, owner string) (bool, error)
	ReleaseRareItem(name string) error
	TransferRareItem(name, owner string) error
	MigrateGuest(guestKey, accountKey, account, nick string) (*Player, error)
	Top(limit int) ([]*Player, error)
	LoadWorldState() (WorldState, error)
	SaveWorldState(WorldState) error
}

type Engine struct {
	mu    sync.Mutex
	repo  Repository
	rules Rules
	rng   *rand.Rand
	world WorldState
	users map[string]*Player
	// pending holds level-up and title announcements produced outside Tick
	// (commands, the observer, collisions) until the next Tick sends them.
	pending []string
	// missingSince tracks connected runners absent from the channel roster.
	missingSince map[string]time.Time
}

const maxPendingAnnouncements = 50

func New(repo Repository, rules Rules, rng *rand.Rand, now time.Time) (*Engine, error) {
	if repo == nil {
		return nil, errors.New("game repository is required")
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(now.UnixNano()))
	}
	if rules.BaseLevelSeconds < 1 {
		rules.BaseLevelSeconds = 1800
	}
	if rules.EncounterInterval < time.Second {
		rules.EncounterInterval = time.Hour
	}
	if rules.CityEventInterval < time.Second {
		rules.CityEventInterval = 2 * time.Hour
	}
	if rules.DistrictInterval < time.Second {
		rules.DistrictInterval = 6 * time.Hour
	}
	if rules.HeatDecayInterval < time.Second {
		rules.HeatDecayInterval = 30 * time.Minute
	}
	if rules.ContractDuration < time.Minute {
		rules.ContractDuration = 8 * time.Hour
	}
	if rules.ContractMaxParticipants < 1 {
		rules.ContractMaxParticipants = 4
	}
	if rules.CollisionInterval < time.Minute {
		rules.CollisionInterval = 90 * time.Minute
	}
	if rules.PirateDuration < time.Second {
		rules.PirateDuration = 5 * time.Minute
	}
	if rules.GuestRetention < time.Hour {
		rules.GuestRetention = 14 * 24 * time.Hour
	}
	if rules.ArtifactOfflineRelease < time.Hour {
		rules.ArtifactOfflineRelease = 7 * 24 * time.Hour
	}

	users := make(map[string]*Player)
	players, err := repo.LoadAll()
	if err != nil {
		return nil, err
	}
	for _, p := range players {
		if p.Level < 1 {
			p.Level = 1
		}
		p.Heat = clampHeat(p.Heat)
		p.Connected = false
		p.LastProgressAt = now
		p.LastHeatAt = now
		ensureEquipment(p)
		for slot, item := range p.Equipment {
			// Uniques from before burn-out existed get a normal remaining lifespan.
			if item.Unique && item.BreaksAt == 0 {
				item.BreaksAt = p.Level + uniqueLifeLevels(rng)
				p.Equipment[slot] = item
			}
		}
		if !validDistrict(p.District) {
			p.District = DistrictNeonMarket
		}
		if p.NextDistrictAt.IsZero() {
			p.NextDistrictAt = now.Add(rules.DistrictInterval)
		}
		if p.NextEncounterAt.IsZero() {
			p.NextEncounterAt = now.Add(rules.EncounterInterval)
		}
		if p.NextCollisionAt.IsZero() {
			p.NextCollisionAt = now.Add(rules.CollisionInterval)
		}
		users[p.Identity] = p
		if err := repo.Save(p); err != nil {
			return nil, err
		}
	}
	world, err := repo.LoadWorldState()
	if err != nil {
		return nil, err
	}
	worldChanged := false
	if world.NextCityEventAt.IsZero() {
		world.NextCityEventAt = now.Add(rules.CityEventInterval)
		worldChanged = true
	}
	if world.NextFactionSwapAt.IsZero() {
		world.NextFactionSwapAt = nextFactionSwapAt(now, rng)
		worldChanged = true
	}
	if world.FactionWeek.EndsAt.IsZero() {
		world.FactionWeek.EndsAt = now.Add(factionWeekLength)
		worldChanged = true
	}
	if world.Bulletin.NextAt.IsZero() {
		world.Bulletin.NextAt = now.Add(bulletinInterval)
		worldChanged = true
	}
	if worldChanged {
		if err := repo.SaveWorldState(world); err != nil {
			return nil, err
		}
	}
	return &Engine{repo: repo, rules: rules, rng: rng, world: world, users: users, missingSince: make(map[string]time.Time)}, nil
}

func AccountKey(account string) string { return "acct:" + strings.ToLower(strings.TrimSpace(account)) }

func GuestKey(nick string) string { return "guest:" + strings.ToLower(strings.TrimSpace(nick)) }

func (e *Engine) Join(identity, nick, account string, now time.Time) (*Player, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.joinLocked(identity, nick, account, now)
}

func (e *Engine) joinLocked(identity, nick, account string, now time.Time) (*Player, error) {
	guest := account == ""
	if guest {
		identity = GuestKey(nick)
	} else {
		identity = AccountKey(account)
	}
	p := e.users[identity]
	contractChanged := false
	if !guest {
		if guestPlayer := e.migratableGuestLocked(nick, account); guestPlayer != nil {
			guestKey := guestPlayer.Identity
			e.advanceLocked(guestPlayer, now)
			if err := e.repo.Save(guestPlayer); err != nil {
				return nil, err
			}
			migrated, err := e.repo.MigrateGuest(guestPlayer.Identity, identity, account, nick)
			if err != nil {
				return nil, err
			}
			delete(e.users, guestPlayer.Identity)
			p = migrated
			e.users[identity] = p
			contractChanged = e.rekeyContractParticipantLocked(guestKey, identity)
		}
	}
	if p == nil {
		p = newPlayer(identity, nick, account, now)
		p.NextEncounterAt = now.Add(e.rules.EncounterInterval)
		p.NextDistrictAt = now.Add(e.rules.DistrictInterval)
		p.NextCollisionAt = now.Add(e.rules.CollisionInterval)
		e.users[identity] = p
	} else if p.Connected {
		e.advanceLocked(p, now)
	} else {
		e.staggerTimersLocked(p, now)
		// A rejoin soon after a netsplit or bot outage keeps the streak alive.
		if p.StreakSince.IsZero() || now.Sub(p.LastSeenAt) > streakGrace {
			p.StreakSince, p.StreakDays = now, 0
		}
	}
	p.Nick = nick
	p.Account = account
	p.Guest = guest
	p.Connected = true
	p.LastSeenAt = now
	p.LastProgressAt = now
	p.LastHeatAt = now
	ensureEquipment(p)
	if err := e.repo.Save(p); err != nil {
		return nil, err
	}
	if contractChanged {
		if err := e.repo.SaveWorldState(e.world); err != nil {
			return nil, err
		}
	}
	return clonePlayer(p), nil
}

// staggerTimersLocked pushes timers that came due while a runner was offline
// to a random point later in their interval, so a mass rejoin after a
// netsplit or restart does not fire everyone's encounter, drift, and
// collision in the same tick.
func (e *Engine) staggerTimersLocked(p *Player, now time.Time) {
	stagger := func(at *time.Time, interval time.Duration) {
		if at.IsZero() || at.After(now) {
			return
		}
		*at = now.Add(interval/4 + time.Duration(e.rng.Int63n(int64(interval*3/4)+1)))
	}
	stagger(&p.NextEncounterAt, e.rules.EncounterInterval)
	stagger(&p.NextDistrictAt, e.rules.DistrictInterval)
	stagger(&p.NextCollisionAt, e.rules.CollisionInterval)
}

func (e *Engine) Bind(nick, account string, now time.Time) (*Player, error) {
	if strings.TrimSpace(account) == "" {
		return nil, errors.New("account is required")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	identity := AccountKey(account)
	contractChanged := false
	if p := e.findConnectedLocked(nick); p != nil && !p.Guest && p.Identity != identity {
		// The user switched NickServ accounts. The previous account keeps its
		// runner; it just goes offline instead of being re-keyed to the new one.
		e.advanceLocked(p, now)
		p.Connected = false
		p.LastSeenAt = now
		contractChanged = e.markContractFailedLocked(p.Identity)
		if err := e.repo.Save(p); err != nil {
			return nil, err
		}
	}
	p, err := e.joinLocked(identity, nick, account, now)
	if err != nil {
		return nil, err
	}
	if contractChanged {
		if err := e.repo.SaveWorldState(e.world); err != nil {
			return nil, err
		}
	}
	return p, nil
}

// migratableGuestLocked returns the guest runner that provably belongs to the
// user now identified as account: the guest currently connected under nick,
// or the offline guest record for a nick that matches the account name.
func (e *Engine) migratableGuestLocked(nick, account string) *Player {
	if p := e.findConnectedLocked(nick); p != nil {
		if p.Guest {
			return p
		}
		return nil
	}
	if p := e.users[GuestKey(nick)]; p != nil && p.Guest && strings.EqualFold(strings.TrimSpace(nick), strings.TrimSpace(account)) {
		return p
	}
	return nil
}

func (e *Engine) SetAlias(identity, nick, alias string, now time.Time) (*Player, error) {
	alias = strings.TrimSpace(alias)
	if alias != "" && !validAlias(alias) {
		return nil, errors.New("alias must be 1-24 printable ASCII characters with no spaces")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	p := e.findLocked(identity, nick)
	if p == nil {
		return nil, ErrRunnerNotFound
	}
	if alias != "" {
		for _, other := range e.users {
			if other != p && (strings.EqualFold(other.Alias, alias) || strings.EqualFold(other.Nick, alias)) {
				return nil, errors.New("that name is already in use by another runner")
			}
		}
	}
	e.advanceLocked(p, now)
	p.Alias = alias
	p.LastSeenAt = now
	if err := e.repo.Save(p); err != nil {
		return nil, err
	}
	return clonePlayer(p), nil
}

// SetTitle picks which earned title a runner shows; "" or "auto" returns to
// showing the most prestigious one.
func (e *Engine) SetTitle(identity, nick, title string, now time.Time) (*Player, error) {
	title = strings.TrimSpace(title)
	e.mu.Lock()
	defer e.mu.Unlock()
	p := e.findLocked(identity, nick)
	if p == nil {
		return nil, ErrRunnerNotFound
	}
	chosen := ""
	if title != "" && !strings.EqualFold(title, "auto") {
		for _, earned := range p.Titles {
			if strings.EqualFold(earned, title) {
				chosen = earned
			}
		}
		if chosen == "" {
			return nil, errors.New("you have not earned that title")
		}
	}
	e.advanceLocked(p, now)
	p.ChosenTitle = chosen
	p.LastSeenAt = now
	if err := e.repo.Save(p); err != nil {
		return nil, err
	}
	return clonePlayer(p), nil
}

func (e *Engine) SetFaction(identity, nick, faction string, now time.Time) (*Player, error) {
	faction = strings.ToLower(strings.TrimSpace(faction))
	if !validFaction(faction) {
		return nil, errors.New("unknown faction")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	p := e.findLocked(identity, nick)
	if p == nil {
		return nil, ErrRunnerNotFound
	}
	if p.Faction == faction {
		return nil, errors.New("runner already uses that faction")
	}
	if p.Faction != "" {
		if !e.factionSwapOpenLocked(now) {
			return nil, errors.New("faction is already locked")
		}
		if !p.LastFactionSwapAt.Before(e.world.FactionSwapStartedAt) {
			return nil, errors.New("faction crash respec already used")
		}
	}
	e.advanceLocked(p, now)
	p.Faction = faction
	if e.factionSwapOpenLocked(now) {
		p.LastFactionSwapAt = now
	}
	p.LastSeenAt = now
	if err := e.repo.Save(p); err != nil {
		return nil, err
	}
	return clonePlayer(p), nil
}

func (e *Engine) Activity(identity, nick, channel string, kind Activity, length int, now time.Time) (int64, bool, error) {
	if !strings.EqualFold(channel, e.rules.GameChannel) {
		return 0, false, nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	p := e.findLocked(identity, nick)
	if p == nil {
		return 0, false, ErrRunnerNotFound
	}
	e.advanceLocked(p, now)
	p.LastSeenAt = now
	if (kind == ActivityChat || kind == ActivityAction) && now.Before(e.world.PirateUntil) {
		if err := e.repo.Save(p); err != nil {
			return 0, true, err
		}
		return 0, true, nil
	}
	penalty := PenaltySeconds(p.Level, kind, length, e.rules)
	p.ProgressSeconds -= penalty
	if err := e.repo.Save(p); err != nil {
		return 0, false, err
	}
	return penalty, false, nil
}

func (e *Engine) Disconnect(nick string, kind Activity, now time.Time) (int64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	p := e.findLocked("", nick)
	if p == nil || !p.Connected {
		return 0, ErrRunnerNotFound
	}
	e.advanceLocked(p, now)
	penalty := PenaltySeconds(p.Level, kind, 0, e.rules)
	p.ProgressSeconds -= penalty
	p.Heat = addHeat(p.Heat, disconnectHeat(kind))
	p.Connected = false
	p.LastSeenAt = now
	p.LastProgressAt = now
	p.LastHeatAt = now
	contractChanged := false
	if kind != ActivityNetsplit {
		contractChanged = e.markContractFailedLocked(p.Identity)
		p.StreakSince, p.StreakDays = time.Time{}, 0
	}
	if err := e.repo.Save(p); err != nil {
		return 0, err
	}
	if contractChanged {
		if err := e.repo.SaveWorldState(e.world); err != nil {
			return 0, err
		}
	}
	return penalty, nil
}

// DisconnectAll marks every runner offline when the bot itself loses the
// channel. It is not the runners' fault, so it does not fail contracts; a
// participant who is still missing when the contract resolves fails it then.
func (e *Engine) DisconnectAll(now time.Time) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, p := range e.users {
		if !p.Connected {
			continue
		}
		e.advanceLocked(p, now)
		p.Connected = false
		p.LastProgressAt = now
		p.LastHeatAt = now
		if err := e.repo.Save(p); err != nil {
			return err
		}
	}
	clear(e.missingSince)
	return nil
}

// Reconcile compares connected runners with the channel roster. A runner
// missing for longer than grace (a missed QUIT, PART, or nick change) is
// dropped without penalty so it cannot keep accruing progress while gone.
func (e *Engine) Reconcile(present func(nick string) bool, grace time.Duration, now time.Time) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	contractChanged := false
	for key, p := range e.users {
		if !p.Connected || present(p.Nick) {
			delete(e.missingSince, key)
			continue
		}
		since, ok := e.missingSince[key]
		if !ok {
			e.missingSince[key] = now
			continue
		}
		if now.Sub(since) < grace {
			continue
		}
		delete(e.missingSince, key)
		e.advanceLocked(p, now)
		p.Connected = false
		p.LastSeenAt = now
		p.StreakSince, p.StreakDays = time.Time{}, 0
		contractChanged = e.markContractFailedLocked(p.Identity) || contractChanged
		if err := e.repo.Save(p); err != nil {
			return err
		}
	}
	for key := range e.missingSince {
		if p := e.users[key]; p == nil || !p.Connected {
			delete(e.missingSince, key)
		}
	}
	if contractChanged {
		return e.repo.SaveWorldState(e.world)
	}
	return nil
}

func (e *Engine) Rename(oldNick, newNick string, now time.Time) (int64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	p := e.findLocked("", oldNick)
	if p == nil || !p.Connected {
		return 0, ErrRunnerNotFound
	}
	oldKey := p.Identity
	newKey := GuestKey(newNick)
	rekey := p.Guest && newKey != oldKey
	if existing := e.users[newKey]; rekey && existing != nil && existing != p {
		// Another guest record owns the new nick's key. Keep this runner under
		// its current key but follow the new nick, so later QUIT/PART events
		// still find it and the other guest's progress is untouched.
		rekey = false
	}
	e.advanceLocked(p, now)
	penalty := PenaltySeconds(p.Level, ActivityNick, 0, e.rules)
	p.ProgressSeconds -= penalty
	p.Heat = addHeat(p.Heat, 4)
	p.Nick = newNick
	contractChanged := false
	if rekey {
		p.Identity = newKey
		delete(e.users, oldKey)
		contractChanged = e.rekeyContractParticipantLocked(oldKey, newKey)
		if err := e.repo.Delete(oldKey); err != nil {
			return 0, err
		}
	}
	e.users[p.Identity] = p
	if err := e.repo.Save(p); err != nil {
		return 0, err
	}
	if contractChanged {
		if err := e.repo.SaveWorldState(e.world); err != nil {
			return 0, err
		}
	}
	return penalty, nil
}

func (e *Engine) Status(identity, nick string, now time.Time) (*Player, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	p := e.findLocked(identity, nick)
	if p == nil {
		return nil, ErrRunnerNotFound
	}
	e.advanceLocked(p, now)
	if err := e.repo.Save(p); err != nil {
		return nil, err
	}
	return clonePlayer(p), nil
}

func (e *Engine) Top(limit int, now time.Time) ([]*Player, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if limit < 1 {
		limit = 10
	}
	for _, p := range e.users {
		if p.Connected {
			e.advanceLocked(p, now)
			if err := e.repo.Save(p); err != nil {
				return nil, err
			}
		}
	}
	return e.repo.Top(limit)
}

func (e *Engine) Tick(now time.Time) ([]string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	// Announcements queued since the last tick follow this tick's world events.
	carried := e.takePendingLocked()
	var messages []string
	if message, err := e.factionSwapTickLocked(now); err != nil {
		return nil, err
	} else if message != "" {
		messages = append(messages, message)
	}
	if message, err := e.resolveContractLocked(now); err != nil {
		return nil, err
	} else if message != "" {
		messages = append(messages, message)
	}
	if message, err := e.resolveRaidLocked(now); err != nil {
		return nil, err
	} else if message != "" {
		messages = append(messages, message)
	}
	for _, message := range []string{e.deadDropExpiryLocked(now), e.factionWeekTickLocked(now), e.bulletinTickLocked(now)} {
		if message != "" {
			messages = append(messages, message)
		}
	}
	if !e.world.NextCityEventAt.IsZero() && !now.Before(e.world.NextCityEventAt) {
		message, err := e.cityEventLocked(now)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
		e.world.NextCityEventAt = now.Add(e.rules.CityEventInterval)
		if err := e.repo.SaveWorldState(e.world); err != nil {
			return nil, err
		}
	}
	messages = append(messages, carried...)
	for key, p := range e.users {
		if p.Guest && !p.Connected && !p.LastSeenAt.IsZero() && now.Sub(p.LastSeenAt) > e.rules.GuestRetention {
			if err := e.repo.Delete(key); err != nil {
				return nil, err
			}
			delete(e.users, key)
			continue
		}
		if !p.Connected {
			if !p.LastSeenAt.IsZero() && now.Sub(p.LastSeenAt) > e.rules.ArtifactOfflineRelease {
				released, err := e.releaseAbandonedUniquesLocked(p)
				if err != nil {
					return nil, err
				}
				messages = append(messages, released...)
			}
			continue
		}
		e.advanceLocked(p, now)
		if message := e.streakTickLocked(p, now); message != "" {
			messages = append(messages, message)
		}
		if !p.NextDistrictAt.IsZero() && !now.Before(p.NextDistrictAt) {
			oldDistrict := p.District
			p.District = e.randomDistrictLocked(oldDistrict)
			p.NextDistrictAt = now.Add(e.rules.DistrictInterval)
			messages = append(messages, "[GRID] "+render(e.pick(driftTemplates), "runner", p.DisplayName(), "from", oldDistrict, "to", p.District))
		}
		if !p.NextEncounterAt.IsZero() && !now.Before(p.NextEncounterAt) {
			message, err := e.encounterLocked(p)
			if err != nil {
				return nil, err
			}
			messages = append(messages, message)
			p.NextEncounterAt = now.Add(e.rules.EncounterInterval)
			e.advanceLocked(p, now)
		}
		messages = append(messages, e.takePendingLocked()...)
		burnouts, err := e.burnOutUniquesLocked(p)
		if err != nil {
			return nil, err
		}
		messages = append(messages, burnouts...)
		if err := e.repo.Save(p); err != nil {
			return nil, err
		}
	}
	if message, err := e.collisionLocked(now); err != nil {
		return nil, err
	} else if message != "" {
		messages = append(messages, message)
	}
	messages = append(messages, e.takePendingLocked()...)
	if err := e.rememberLocked(messages...); err != nil {
		return nil, err
	}
	return messages, nil
}

func (e *Engine) factionSwapTickLocked(now time.Time) (string, error) {
	if !e.world.FactionSwapUntil.IsZero() && !now.Before(e.world.FactionSwapUntil) {
		e.world.FactionSwapUntil = time.Time{}
		if err := e.repo.SaveWorldState(e.world); err != nil {
			return "", err
		}
		return "[GRID] SYSTEM RESTORED: faction locks are back online.", nil
	}
	if e.world.FactionSwapUntil.IsZero() && !e.world.NextFactionSwapAt.IsZero() && !now.Before(e.world.NextFactionSwapAt) {
		e.world.FactionSwapStartedAt = now
		e.world.FactionSwapUntil = now.Add(factionSwapDuration)
		e.world.NextFactionSwapAt = nextFactionSwapAt(now, e.rng)
		if err := e.repo.SaveWorldState(e.world); err != nil {
			return "", err
		}
		return "[GRID] SYSTEM CRASH: faction locks are unstable for 24 hours. Use !faction <name> to respec once, or stay the course.", nil
	}
	return "", nil
}

func (e *Engine) factionSwapOpenLocked(now time.Time) bool {
	return !e.world.FactionSwapStartedAt.IsZero() && !e.world.FactionSwapUntil.IsZero() && now.Before(e.world.FactionSwapUntil)
}

func nextFactionSwapAt(now time.Time, rng *rand.Rand) time.Time {
	return now.Add(factionSwapWeek + time.Duration(rng.Intn(7))*24*time.Hour)
}

func (e *Engine) ForcePirate(now time.Time) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	message := e.openPirateFrequencyLocked(now)
	if err := e.rememberLocked(message); err != nil {
		return "", err
	}
	return message, nil
}

func (e *Engine) Rules() Rules { return e.rules }

func (e *Engine) World() WorldState {
	e.mu.Lock()
	defer e.mu.Unlock()
	return cloneWorld(e.world)
}

func (e *Engine) Snapshot(now time.Time) ([]*Player, WorldState, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	// Advance copies only: page views must not write to the database or
	// consume level-up announcements that belong to the next Tick.
	players := make([]*Player, 0, len(e.users))
	for _, p := range e.users {
		view := clonePlayer(p)
		advancePlayer(view, now, e.rules)
		players = append(players, view)
	}
	return players, cloneWorld(e.world), nil
}

func Districts() []string { return append([]string(nil), districts...) }

func (e *Engine) RecentEvents(limit int) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if limit < 1 {
		limit = 10
	}
	if limit > len(e.world.RecentEvents) {
		limit = len(e.world.RecentEvents)
	}
	events := make([]string, limit)
	for i := range events {
		events[i] = e.world.RecentEvents[len(e.world.RecentEvents)-1-i]
	}
	return events
}

func PenaltySeconds(level int, kind Activity, length int, rules Rules) int64 {
	if level < 1 {
		level = 1
	}
	if length < 0 {
		length = 0
	}
	levelScale := int64(level - 1)
	switch kind {
	case ActivityChat:
		return rules.SpeechBaseSeconds + levelScale*rules.SpeechPerLevelSeconds + int64(length)*rules.SpeechPerCharacterSeconds
	case ActivityAction:
		return rules.ActionBaseSeconds + levelScale*rules.ActionPerLevelSeconds + int64(length)*rules.ActionPerCharacterSeconds
	case ActivityNick:
		return rules.NickPenaltySeconds + levelScale*rules.SpeechPerLevelSeconds
	case ActivityPart:
		return rules.PartPenaltySeconds + levelScale*rules.SpeechPerLevelSeconds
	case ActivityQuit:
		return rules.QuitPenaltySeconds + levelScale*rules.SpeechPerLevelSeconds
	case ActivityKick:
		return rules.KickPenaltySeconds + levelScale*rules.SpeechPerLevelSeconds
	default:
		return 0
	}
}

func (r Rules) LevelDuration(level int) int64 {
	if level < 1 {
		level = 1
	}
	duration := r.BaseLevelSeconds + int64(level-1)*r.LevelStepSeconds
	if duration < 1 {
		return 1
	}
	return duration
}

func newPlayer(identity, nick, account string, now time.Time) *Player {
	return &Player{
		Identity: identity, Account: account, Nick: nick, Guest: account == "", Level: 1,
		LastProgressAt: now, LastSeenAt: now, NextEncounterAt: now, LastHeatAt: now, District: DistrictNeonMarket, StreakSince: now,
		Equipment: make(map[string]Item),
	}
}

// findLocked resolves the runner behind an IRC event. Only one user can hold
// a nick at a time, so nick matches consider connected runners only; an
// offline runner is reachable by its identity key, or by its guest key when
// the caller has no account.
func (e *Engine) findLocked(identity, nick string) *Player {
	if identity != "" {
		if p := e.users[identity]; p != nil {
			return p
		}
		if strings.HasPrefix(identity, "acct:") {
			if p := e.users[AccountKey(strings.TrimPrefix(identity, "acct:"))]; p != nil {
				return p
			}
		}
	}
	if p := e.findConnectedLocked(nick); p != nil {
		return p
	}
	if identity == "" {
		if p := e.users[GuestKey(nick)]; p != nil && p.Guest {
			return p
		}
	}
	return nil
}

func (e *Engine) findConnectedLocked(nick string) *Player {
	for _, p := range e.users {
		if p.Connected && strings.EqualFold(p.Nick, nick) {
			return p
		}
	}
	return nil
}

func (e *Engine) advanceLocked(p *Player, now time.Time) {
	before := p.Level
	e.queueLocked(advancePlayer(p, now, e.rules)...)
	if gained := p.Level - before; gained > 0 {
		e.noteBulletinLocked(&e.world.Bulletin.Climbs, p.Identity, gained)
	}
}

func (e *Engine) updateTitlesLocked(p *Player) {
	e.queueLocked(updateTitles(p)...)
}

func (e *Engine) queueLocked(messages ...string) {
	e.pending = append(e.pending, messages...)
	if len(e.pending) > maxPendingAnnouncements {
		e.pending = e.pending[len(e.pending)-maxPendingAnnouncements:]
	}
}

func (e *Engine) takePendingLocked() []string {
	messages := e.pending
	e.pending = nil
	return messages
}

// advancePlayer accrues connected time, Heat decay, level-ups, and titles, and
// returns the announcements those changes deserve.
func advancePlayer(p *Player, now time.Time, rules Rules) []string {
	if !p.Connected {
		p.LastProgressAt = now
		p.LastHeatAt = now
		return nil
	}
	if p.LastProgressAt.IsZero() {
		p.LastProgressAt = now
	}
	if p.LastHeatAt.IsZero() {
		p.LastHeatAt = now
	}
	// A connected runner is being seen, so offline timers (guest pruning,
	// artifact release) start from the last tick even after a bot crash.
	if now.After(p.LastSeenAt) {
		p.LastSeenAt = now
	}
	elapsed := int64(now.Sub(p.LastProgressAt) / time.Second)
	if elapsed > 0 {
		p.ProgressSeconds += elapsed
		p.LastProgressAt = p.LastProgressAt.Add(time.Duration(elapsed) * time.Second)
	}
	if elapsedHeat := int64(now.Sub(p.LastHeatAt) / rules.HeatDecayInterval); elapsedHeat > 0 {
		p.Heat = clampHeat(p.Heat - int(elapsedHeat))
		p.LastHeatAt = p.LastHeatAt.Add(time.Duration(elapsedHeat) * rules.HeatDecayInterval)
	}
	var messages []string
	startLevel, lastGear := p.Level, ""
	for p.ProgressSeconds >= rules.LevelDuration(p.Level) {
		p.ProgressSeconds -= rules.LevelDuration(p.Level)
		p.Level++
		ensureEquipment(p)
		tier := gearTier(p.Level)
		slot := equipmentSlots[(p.Level-2)%len(equipmentSlots)]
		if item, ok := p.Equipment[slot]; ok && item.Unique {
			lastGear = fmt.Sprintf("UNIQUE %s retained.", item.Name)
			continue
		}
		item := Item{Name: equipmentName(slot, tier), Rating: tier}
		p.Equipment[slot] = item
		lastGear = fmt.Sprintf("Installed %s.", item.Name)
	}
	switch gained := p.Level - startLevel; {
	case gained == 1:
		messages = append(messages, fmt.Sprintf("[GRID] %s %s Rep %d. %s", p.DisplayName(), levelUpVerbs[p.Level%len(levelUpVerbs)], p.Level, lastGear))
	case gained > 1:
		messages = append(messages, fmt.Sprintf("[GRID] %s surged to Rep %d (+%d). Latest: %s", p.DisplayName(), p.Level, gained, lastGear))
	}
	return append(messages, updateTitles(p)...)
}

type rareItem struct {
	name string
	slot string
	// bonus is how many tiers above on-level gear the artifact rates.
	bonus int
}

var rareItems = []rareItem{
	{name: "Blackglass Deck", slot: SlotDeck, bonus: 3},
	{name: "Saint-9 Reflex Coil", slot: SlotNeuralImplant, bonus: 2},
	{name: "Prototype Mantis Rig", slot: SlotWeaponRig, bonus: 4},
	{name: "Aegis Nullplate", slot: SlotArmorPlating, bonus: 3},
	{name: "Whisperbyte Scout", slot: SlotDrone, bonus: 2},
}

const (
	rareLootChancePercent = 5
	// Uniques last uniqueMinLife to uniqueMinLife+uniqueLifeRange-1 levels.
	uniqueMinLife   = 4
	uniqueLifeRange = 5
	// iceBaseChance is the percent chance to beat ICE with on-level gear.
	iceBaseChance = 65
)

func uniqueLifeLevels(rng *rand.Rand) int { return uniqueMinLife + rng.Intn(uniqueLifeRange) }

// gearTier is the Mk rating of standard gear installed at level.
func gearTier(level int) int { return 1 + (level-1)/3 }

// expectedGearRating is the total rating of a standard loadout at level: each
// slot holds the gear from the most recent level-up that upgraded it.
func expectedGearRating(level int) int {
	total := 0
	for l := level; l >= 2 && l > level-len(equipmentSlots); l-- {
		total += gearTier(l)
	}
	return total
}

// iceWinChance is the percent chance to beat ICE. ICE is built for the
// runner's level, so what matters is gear against an on-level loadout, plus
// build, location, and Heat.
func iceWinChance(p *Player, champion string) int {
	chance := iceBaseChance + 3*(p.EquipmentRating()-expectedGearRating(p.Level)) + stanceOdds(p)
	if champion != "" && p.Faction == champion {
		chance += championEdge
	}
	if hasScar(p, ScarGhostSignal) {
		chance += 5
	}
	if hasScar(p, ScarBurnedOptic) {
		chance -= 5
	}
	if p.Faction == FactionGhostline {
		chance += 10
	}
	chance += 5 * districtEncounterBonus(p.District)
	chance -= p.Heat / 5
	return max(10, min(95, chance))
}

func (e *Engine) encounterLocked(p *Player) (string, error) {
	ice, place := e.pick(iceNames), e.placeIn(p.District)
	if e.rng.Intn(100) < iceWinChance(p, e.world.FactionWeek.Champion) {
		p.Stats.IceWins++
		e.scoreFactionLocked(p, 1)
		gain := max64(10, e.rules.LevelDuration(p.Level)/20)
		if p.Faction == FactionChrome {
			gain = gain * 3 / 2
		}
		p.ProgressSeconds += stanceReward(p, gain)
		p.Heat = addHeat(p.Heat, stanceHeat(p, 2))
		scar := ""
		if e.rng.Intn(25) == 0 && addScar(p, ScarGhostSignal) {
			scar = " Ghost Signal acquired."
		}
		item, err := e.rareLootLocked(p, "")
		if err != nil {
			return "", err
		}
		if item != nil {
			return "[GRID] " + render(e.pick(iceUniqueTemplates), "runner", p.DisplayName(), "ice", ice, "place", place, "item", item.Name) + scar, nil
		}
		return "[GRID] " + render(e.pick(iceWinTemplates), "runner", p.DisplayName(), "ice", ice, "place", place) + scar, nil
	}
	loss := max64(5, e.rules.LevelDuration(p.Level)/30)
	if p.Faction == FactionNomad {
		loss = max64(3, loss/2)
	}
	if p.District == DistrictGhostQuarter {
		loss = loss * 3 / 2
	}
	p.ProgressSeconds -= loss
	p.Heat = addHeat(p.Heat, stanceHeat(p, 8))
	scar := ""
	if e.rng.Intn(12) == 0 && addScar(p, ScarBurnedOptic) {
		scar = " Burned Optic acquired."
	}
	return "[GRID] " + render(e.pick(iceLossTemplates), "runner", p.DisplayName(), "ice", ice, "place", place) + scar, nil
}

func (e *Engine) collisionLocked(now time.Time) (string, error) {
	var due []*Player
	for _, p := range e.users {
		if p.Connected && !p.NextCollisionAt.IsZero() && !now.Before(p.NextCollisionAt) {
			due = append(due, p)
		}
	}
	if len(due) == 0 {
		return "", nil
	}
	initiator := due[e.rng.Intn(len(due))]
	opponent := e.pickOpponentLocked(initiator)
	if opponent == nil {
		return "", nil
	}
	bouts := recordBout(initiator, opponent)
	rivalry := bouts >= rivalryBouts && topRival(initiator) == opponent.Identity && topRival(opponent) == initiator.Identity
	winner, loser := initiator, opponent
	if collisionPower(opponent)+e.rng.Intn(5) > collisionPower(initiator)+e.rng.Intn(5) {
		winner, loser = opponent, initiator
	}

	gain := max64(30, e.rules.LevelDuration(winner.Level)/20)
	loss := collisionLoss(loser, max64(20, e.rules.LevelDuration(loser.Level)/24))
	winner.Heat = addHeat(winner.Heat, 3)
	loser.Heat = addHeat(loser.Heat, 5)
	winner.Stats.CollisionWins++
	e.scoreFactionLocked(winner, 2)
	e.noteBulletinLocked(&e.world.Bulletin.CollisionWins, winner.Identity, 1)
	scar := ""
	if e.rng.Intn(20) == 0 && addScar(winner, ScarSyntheticAdrenalGland) {
		scar = " Synthetic Adrenal Gland acquired."
	}
	kind := e.rng.Intn(4)
	switch kind {
	case 0:
		winner.ProgressSeconds += gain
		loser.ProgressSeconds -= loss
	case 1:
		winner.ProgressSeconds += gain * 2
		loser.ProgressSeconds -= max64(15, loss/2)
	case 2:
		winner.ProgressSeconds += max64(15, gain/2)
		loser.ProgressSeconds -= loss * 2
	case 3:
		winner.ProgressSeconds += gain
		loser.ProgressSeconds -= loss
		if item, ok := loser.Equipment[SlotDrone]; ok && !item.Unique && item.Rating > 0 {
			item.Rating--
			if !strings.Contains(item.Name, "damaged") {
				item.Name += " [damaged]"
			}
			loser.Equipment[SlotDrone] = item
		}
	}
	prefix := "[GRID] COLLISION: "
	if rivalry {
		prefix = fmt.Sprintf("[GRID] RIVALRY, round %d: ", bouts)
	}
	message := prefix + render(e.pick(collisionTemplates[kind]),
		"winner", winner.DisplayName(), "loser", loser.DisplayName(), "gain", formatDuration(time.Duration(gain)*time.Second),
		"district", loser.District, "place", e.placeIn(loser.District)) + scar
	if stolen, err := e.stealUniqueLocked(winner, loser); err != nil {
		return "", err
	} else if stolen != "" {
		message += fmt.Sprintf(" %s walked off with %s's UNIQUE %s!", winner.DisplayName(), loser.DisplayName(), stolen)
	}
	e.updateTitlesLocked(winner)
	e.updateTitlesLocked(loser)
	winner.NextCollisionAt = now.Add(e.rules.CollisionInterval)
	loser.NextCollisionAt = now.Add(e.rules.CollisionInterval)
	if err := e.repo.Save(winner); err != nil {
		return "", err
	}
	if err := e.repo.Save(loser); err != nil {
		return "", err
	}
	return message, nil
}

func collisionPower(p *Player) int {
	power := p.Level + p.EquipmentRating() + p.Heat/25
	if hasScar(p, ScarSyntheticAdrenalGland) {
		power += 2
	}
	if hasScar(p, ScarBurnedOptic) {
		power--
	}
	switch p.Faction {
	case FactionGhostline:
		power += 2
	case FactionChrome, FactionNomad:
		power++
	}
	switch p.District {
	case DistrictFloodline, DistrictCorporateArcology:
		power++
	case DistrictGhostQuarter:
		power--
	}
	return power
}

func collisionLoss(p *Player, loss int64) int64 {
	if p.Faction == FactionNomad {
		return max64(3, loss/2)
	}
	return loss
}

// rareLootLocked rolls the unique drop table. A drop rates above on-level
// gear and burns out a few levels later. exclude names an artifact that may
// not drop, such as one that just burned out.
func (e *Engine) rareLootLocked(p *Player, exclude string) (*Item, error) {
	if e.rng.Intn(100) >= rareLootChance(p) {
		return nil, nil
	}
	start := e.rng.Intn(len(rareItems))
	for i := range rareItems {
		candidate := rareItems[(start+i)%len(rareItems)]
		if candidate.name == exclude {
			continue
		}
		claimed, err := e.repo.ClaimRareItem(candidate.name, p.Identity)
		if err != nil {
			return nil, err
		}
		if !claimed {
			continue
		}
		item := &Item{Name: candidate.name, Rating: gearTier(p.Level) + candidate.bonus, Unique: true, BreaksAt: p.Level + uniqueLifeLevels(e.rng)}
		ensureEquipment(p)
		p.Equipment[candidate.slot] = *item
		p.Stats.Uniques++
		e.updateTitlesLocked(p)
		return item, nil
	}
	return nil, nil
}

// retireUniqueLocked returns the unique in slot to the drop pool and fits
// standard gear for the runner's current level in its place.
func (e *Engine) retireUniqueLocked(p *Player, slot string) (Item, error) {
	if err := e.repo.ReleaseRareItem(p.Equipment[slot].Name); err != nil {
		return Item{}, err
	}
	replacement := Item{Name: equipmentName(slot, gearTier(p.Level)), Rating: gearTier(p.Level)}
	p.Equipment[slot] = replacement
	return replacement, nil
}

// releaseAbandonedUniquesLocked frees artifacts held by a runner who has been
// offline too long, so the few that exist keep circulating.
func (e *Engine) releaseAbandonedUniquesLocked(p *Player) ([]string, error) {
	var messages []string
	for _, slot := range equipmentSlots {
		item := p.Equipment[slot]
		if !item.Unique {
			continue
		}
		if _, err := e.retireUniqueLocked(p, slot); err != nil {
			return nil, err
		}
		messages = append(messages, fmt.Sprintf("[GRID] %s's UNIQUE %s went dark while its owner was off the Grid. It's back on the black market.", p.DisplayName(), item.Name))
	}
	if len(messages) > 0 {
		if err := e.repo.Save(p); err != nil {
			return nil, err
		}
	}
	return messages, nil
}

// burnOutUniquesLocked retires uniques that reached their burn-out level:
// the artifact returns to the drop pool, the slot gets standard on-level
// gear, and the runner rolls the drop table again.
func (e *Engine) burnOutUniquesLocked(p *Player) ([]string, error) {
	var messages []string
	for _, slot := range equipmentSlots {
		item := p.Equipment[slot]
		if !item.Unique || item.BreaksAt == 0 || p.Level < item.BreaksAt {
			continue
		}
		replacement, err := e.retireUniqueLocked(p, slot)
		if err != nil {
			return nil, err
		}
		message := fmt.Sprintf("[GRID] %s's UNIQUE %s burned out and slipped back onto the black market. Fitted %s.", p.DisplayName(), item.Name, replacement.Name)
		salvage, err := e.rareLootLocked(p, item.Name)
		if err != nil {
			return nil, err
		}
		if salvage != nil {
			message += fmt.Sprintf(" The salvage run turned up UNIQUE %s!", salvage.Name)
		}
		messages = append(messages, message)
	}
	if len(messages) > 0 {
		if err := e.repo.Save(p); err != nil {
			return nil, err
		}
	}
	return append(messages, e.takePendingLocked()...), nil
}

func rareLootChance(p *Player) int {
	chance := rareLootChancePercent + p.Heat/10
	switch p.Stance {
	case StanceHot:
		chance *= 2
	case StanceCold:
		chance /= 2
	}
	return min(100, chance)
}

func addScar(p *Player, scar string) bool {
	if hasScar(p, scar) {
		return false
	}
	p.Scars = append(p.Scars, scar)
	return true
}

func hasScar(p *Player, scar string) bool {
	for _, existing := range p.Scars {
		if existing == scar {
			return true
		}
	}
	return false
}

func addTitle(p *Player, title string) bool {
	for _, existing := range p.Titles {
		if existing == title {
			return false
		}
	}
	p.Titles = append(p.Titles, title)
	return true
}

type titleRule struct {
	name   string
	earned func(*Player) bool
}

// titleRules run from least to most prestigious. Runners collect every title
// they earn but show only one: the most prestigious, or the one they chose.
var titleRules = []titleRule{
	{"Ghost of Floodline", func(p *Player) bool { return p.District == DistrictFloodline && p.Level >= 3 }},
	{"ICEbreaker", func(p *Player) bool { return p.Level >= 5 }},
	{"Corporate Liability", func(p *Player) bool { return p.Heat >= 75 }},
	{"Relic Hunter", func(p *Player) bool { return p.Stats.Uniques >= 1 }},
	{"Cat Burglar", func(p *Player) bool { return p.Stats.Thefts >= 1 }},
	{"Static Whisperer", func(p *Player) bool { return p.Stats.DeadDrops >= 3 }},
	{"Street Samurai", func(p *Player) bool { return p.Stats.CollisionWins >= 10 }},
	{"Deniable Asset", func(p *Player) bool { return p.Stats.Contracts >= 3 }},
	{"Wallbreaker", func(p *Player) bool { return p.Stats.RaidWins >= 3 }},
	{"ICE Surfer", func(p *Player) bool { return p.Stats.IceWins >= 25 }},
	{"Chrome Saint", func(p *Player) bool { return p.Level >= 50 }},
	{"Blood Feud", func(p *Player) bool { return p.Rivals[topRival(p)] >= 10 }},
	{"Ghost Protocol", func(p *Player) bool { return p.Stats.LongestStreak >= 7 }},
	{"Black Market Royalty", func(p *Player) bool { return p.Stats.Uniques >= 3 }},
	{"Nine-Day Signal", func(p *Player) bool { return p.Level >= 100 }},
	{"Blackwall Veteran", func(p *Player) bool { return p.Stats.IceWins >= 100 }},
	{"Neon Ghost", func(p *Player) bool { return p.Level >= 150 }},
	{"Ghost in the Machine", func(p *Player) bool { return p.Level >= 250 }},
}

// updateTitles awards milestone titles and returns an announcement for each new one.
func updateTitles(p *Player) []string {
	var messages []string
	for _, rule := range titleRules {
		if rule.earned(p) && addTitle(p, rule.name) {
			messages = append(messages, fmt.Sprintf("[GRID] %s earned the title %s.", p.DisplayName(), rule.name))
		}
	}
	return messages
}

func hasTitle(p *Player, title string) bool {
	for _, existing := range p.Titles {
		if existing == title {
			return true
		}
	}
	return false
}

// CurrentTitle is the one title shown for a runner.
func (p *Player) CurrentTitle() string {
	if p.ChosenTitle != "" && hasTitle(p, p.ChosenTitle) {
		return p.ChosenTitle
	}
	for i := len(titleRules) - 1; i >= 0; i-- {
		if hasTitle(p, titleRules[i].name) {
			return titleRules[i].name
		}
	}
	if len(p.Titles) == 0 {
		return ""
	}
	return p.Titles[len(p.Titles)-1]
}

func (e *Engine) randomDistrictLocked(current string) string {
	options := make([]string, 0, len(districts)-1)
	for _, district := range districts {
		if district != current {
			options = append(options, district)
		}
	}
	return options[e.rng.Intn(len(options))]
}

func districtEncounterBonus(district string) int {
	switch district {
	case DistrictCorporateArcology:
		return 2
	case DistrictOldTransit:
		return 1
	case DistrictGhostQuarter:
		return -2
	default:
		return 0
	}
}

func validDistrict(district string) bool {
	for _, known := range districts {
		if district == known {
			return true
		}
	}
	return false
}

func validFaction(faction string) bool {
	switch faction {
	case FactionGhostline, FactionChrome, FactionNomad:
		return true
	default:
		return false
	}
}

func validAlias(alias string) bool {
	if alias == "" || len(alias) > 24 {
		return false
	}
	for _, r := range alias {
		if r < '!' || r > '~' {
			return false
		}
	}
	return true
}

func disconnectHeat(kind Activity) int {
	switch kind {
	case ActivityKick:
		return 15
	case ActivityPart, ActivityQuit:
		return 5
	default:
		return 0
	}
}

func addHeat(heat, amount int) int {
	return clampHeat(heat + amount)
}

func clampHeat(heat int) int {
	if heat < 0 {
		return 0
	}
	if heat > MaxHeat {
		return MaxHeat
	}
	return heat
}

func (e *Engine) rememberLocked(messages ...string) error {
	if len(messages) == 0 {
		return nil
	}
	e.world.RecentEvents = append(e.world.RecentEvents, messages...)
	if len(e.world.RecentEvents) > 20 {
		e.world.RecentEvents = e.world.RecentEvents[len(e.world.RecentEvents)-20:]
	}
	return e.repo.SaveWorldState(e.world)
}

func (e *Engine) cityEventLocked(now time.Time) (string, error) {
	if e.rng.Intn(10) == 0 {
		return e.openPirateFrequencyLocked(now), nil
	}
	if e.rng.Intn(8) == 0 {
		if message, started := e.startRaidLocked(now); started {
			return message, nil
		}
	}
	event := cityEvents[e.rng.Intn(len(cityEvents))]
	if event.kind == cityEventMegacorpRun {
		if message, started, err := e.startContractLocked(now); err != nil {
			return "", err
		} else if started {
			return message, nil
		}
	}
	var scarMessages []string
	for _, p := range e.users {
		if !p.Connected {
			continue
		}
		e.advanceLocked(p, now)
		p.ProgressSeconds += cityEventProgressChange(event, p)
		if event.kind == cityEventCorporateSweep {
			p.Heat = addHeat(p.Heat, 10)
			if e.rng.Intn(20) == 0 && addScar(p, ScarCorporateBackdoor) {
				scarMessages = append(scarMessages, fmt.Sprintf("%s acquired %s", p.DisplayName(), ScarCorporateBackdoor))
			}
		}
		applyCityEventGear(event, p)
		if event.kind == cityEventDataLeak {
			// Fresh intel draws an ICE trace forward so the next passive encounter arrives sooner.
			nextEncounter := now.Add(15 * time.Minute)
			if p.NextEncounterAt.IsZero() || p.NextEncounterAt.After(nextEncounter) {
				p.NextEncounterAt = nextEncounter
			}
		}
		e.updateTitlesLocked(p)
		if err := e.repo.Save(p); err != nil {
			return "", err
		}
	}
	message := fmt.Sprintf("[GRID] CITY EVENT: %s Active runners %s.", e.pick(cityEventTexts[event.kind]), formatProgressChange(event.progressChange))
	if len(scarMessages) > 0 {
		message += " " + strings.Join(scarMessages, "; ") + "."
	}
	return message, nil
}

func (e *Engine) startContractLocked(now time.Time) (string, bool, error) {
	if e.world.Contract != nil {
		return "", false, nil
	}
	var candidates []*Player
	for _, p := range e.users {
		if p.Connected {
			candidates = append(candidates, p)
		}
	}
	if len(candidates) == 0 {
		return "", false, nil
	}
	order := e.rng.Perm(len(candidates))
	count := min(len(candidates), e.rules.ContractMaxParticipants)
	contract := &Contract{
		Title:    e.pick(contractCorps) + " " + e.pick(contractJobs),
		District: districts[e.rng.Intn(len(districts))],
		EndsAt:   now.Add(e.rules.ContractDuration),
	}
	nicks := make([]string, 0, count)
	for _, index := range order[:count] {
		p := candidates[index]
		contract.Participants = append(contract.Participants, p.Identity)
		nicks = append(nicks, p.DisplayName())
	}
	e.world.Contract = contract
	return fmt.Sprintf("[GRID] CONTRACT: %s in %s. Stay linked for %s. Team: %s.", contract.Title, contract.District, formatDuration(e.rules.ContractDuration), strings.Join(nicks, ", ")), true, nil
}

func (e *Engine) resolveContractLocked(now time.Time) (string, error) {
	contract := e.world.Contract
	// A dropped signal ends the contract on the next tick instead of at the deadline.
	if contract == nil || (now.Before(contract.EndsAt) && !contract.Failed) {
		return "", nil
	}
	success := !contract.Failed
	for _, identity := range contract.Participants {
		p := e.users[identity]
		if p == nil || !p.Connected {
			success = false
			break
		}
	}
	for _, identity := range contract.Participants {
		p := e.users[identity]
		if p == nil {
			continue
		}
		e.advanceLocked(p, now)
		if success {
			p.ProgressSeconds += max64(60, e.rules.LevelDuration(p.Level)/6)
			p.Heat = addHeat(p.Heat, 3)
			p.Stats.Contracts++
			e.scoreFactionLocked(p, 3)
			e.updateTitlesLocked(p)
		} else {
			p.ProgressSeconds -= max64(120, e.rules.LevelDuration(p.Level)/4)
			p.Heat = addHeat(p.Heat, 10)
		}
		if err := e.repo.Save(p); err != nil {
			return "", err
		}
	}
	e.world.Contract = nil
	if success {
		return fmt.Sprintf("[GRID] CONTRACT COMPLETE: the %s is done and nobody blinked. The team splits the payout.", contract.Title), nil
	}
	if contract.DroppedBy != "" {
		return fmt.Sprintf("[GRID] CONTRACT FAILED: the %s collapsed when %s's signal dropped mid-run. The whole team eats the setback.", contract.Title, contract.DroppedBy), nil
	}
	return fmt.Sprintf("[GRID] CONTRACT FAILED: the %s collapsed; not everyone was linked at the deadline. The whole team eats the setback.", contract.Title), nil
}

func (e *Engine) markContractFailedLocked(identity string) bool {
	if e.world.Contract == nil || e.world.Contract.Failed {
		return false
	}
	for _, participant := range e.world.Contract.Participants {
		if participant == identity {
			e.world.Contract.Failed = true
			if p := e.users[identity]; p != nil {
				e.world.Contract.DroppedBy = p.DisplayName()
			}
			return true
		}
	}
	return false
}

func (e *Engine) rekeyContractParticipantLocked(oldIdentity, newIdentity string) bool {
	if e.world.Contract == nil || oldIdentity == newIdentity {
		return false
	}
	changed := false
	for index, participant := range e.world.Contract.Participants {
		if participant == oldIdentity {
			e.world.Contract.Participants[index] = newIdentity
			changed = true
		}
	}
	return changed
}

func cityEventProgressChange(event cityEvent, p *Player) int64 {
	change := event.progressChange
	switch event.kind {
	case cityEventBlackout:
		if p.District == DistrictOldTransit {
			change -= 60
		}
	case cityEventCorporateSweep:
		if p.Faction == FactionGhostline {
			change /= 2
		}
		if p.District == DistrictCorporateArcology {
			change -= 30
		}
		if hasScar(p, ScarCorporateBackdoor) {
			change -= 30
		}
	case cityEventDataLeak:
		if p.District == DistrictFloodline {
			change += 60
		}
	case cityEventGangWar:
		if p.District == DistrictGhostQuarter {
			change -= 90
		}
		if p.Faction == FactionNomad {
			change += 30
		}
	case cityEventBounty:
		if p.Faction == FactionChrome {
			change += 60
		}
	case cityEventMegacorpRun:
		if p.District == DistrictCorporateArcology || p.Faction == FactionChrome {
			change += 60
		}
	}
	return change
}

func applyCityEventGear(event cityEvent, p *Player) {
	if event.kind != cityEventDataLeak {
		return
	}
	ensureEquipment(p)
	item := p.Equipment[SlotDeck]
	if item.Name == "" {
		item.Name = "Ghostline deck"
	}
	item.Rating++
	if !strings.Contains(item.Name, "leak-overclocked") {
		item.Name += " [leak-overclocked]"
	}
	p.Equipment[SlotDeck] = item
}

func formatProgressChange(seconds int64) string {
	sign := "+"
	if seconds < 0 {
		sign = "-"
		seconds = -seconds
	}
	return sign + formatDuration(time.Duration(seconds)*time.Second) + " progress"
}

func (p *Player) EquipmentRating() int {
	total := 0
	for _, item := range p.Equipment {
		total += item.Rating
	}
	return total
}

func (p *Player) DisplayName() string {
	if p.Alias != "" {
		return p.Alias
	}
	return p.Nick
}

func (p *Player) NextLevelIn(rules Rules) time.Duration {
	remaining := rules.LevelDuration(p.Level) - p.ProgressSeconds
	if remaining < 0 {
		remaining = 0
	}
	return time.Duration(remaining) * time.Second
}

func equipmentName(slot string, tier int) string {
	names := map[string]string{
		SlotWeaponRig:     "Mono-edge weapon rig",
		SlotArmorPlating:  "Reactive armor plating",
		SlotNeuralImplant: "Neural reflex implant",
		SlotDeck:          "Ghostline deck",
		SlotDrone:         "Scout drone",
	}
	return fmt.Sprintf("%s Mk %d", names[slot], tier)
}

func ensureEquipment(p *Player) {
	if p.Equipment == nil {
		p.Equipment = make(map[string]Item)
	}
}

func clonePlayer(p *Player) *Player {
	copy := *p
	copy.Equipment = make(map[string]Item, len(p.Equipment))
	for slot, item := range p.Equipment {
		copy.Equipment[slot] = item
	}
	copy.Scars = append([]string(nil), p.Scars...)
	copy.Titles = append([]string(nil), p.Titles...)
	copy.Rivals = cloneCounts(p.Rivals)
	return &copy
}

func cloneWorld(world WorldState) WorldState {
	copy := world
	copy.RecentEvents = append([]string(nil), world.RecentEvents...)
	if world.Contract != nil {
		contract := *world.Contract
		contract.Participants = append([]string(nil), world.Contract.Participants...)
		copy.Contract = &contract
	}
	if world.Raid != nil {
		raid := *world.Raid
		copy.Raid = &raid
	}
	copy.FactionWeek.Scores = cloneCounts(world.FactionWeek.Scores)
	copy.Bulletin.Climbs = cloneCounts(world.Bulletin.Climbs)
	copy.Bulletin.CollisionWins = cloneCounts(world.Bulletin.CollisionWins)
	return copy
}

func formatDuration(d time.Duration) string {
	if d%time.Hour == 0 {
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	if d%time.Minute == 0 {
		return fmt.Sprintf("%dm", int(d/time.Minute))
	}
	return d.Round(time.Second).String()
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
