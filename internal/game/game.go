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
}

type Player struct {
	Identity          string
	Account           string
	Nick              string
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
}

type WorldState struct {
	PirateUntil          time.Time
	NextCityEventAt      time.Time
	FactionSwapUntil     time.Time
	FactionSwapStartedAt time.Time
	NextFactionSwapAt    time.Time
	RecentEvents         []string
	Contract             *Contract
}

type Contract struct {
	Title        string
	District     string
	Participants []string
	EndsAt       time.Time
	Failed       bool
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
	text           string
	progressChange int64
}

var cityEvents = []cityEvent{
	{kind: cityEventBlackout, text: "BLACKOUT rolls across the lower stacks.", progressChange: -60},
	{kind: cityEventCorporateSweep, text: "CORPORATE SWEEP detected. Keep your signatures cold.", progressChange: -90},
	{kind: cityEventDataLeak, text: "DATA LEAK: fresh intel is spilling onto the Grid.", progressChange: 120},
	{kind: cityEventGangWar, text: "GANG WAR erupts beneath the maglev lines.", progressChange: -120},
	{kind: cityEventBounty, text: "BOUNTY contract posted; every faction is watching.", progressChange: 90},
	{kind: cityEventMegacorpRun, text: "MEGACORP RUN authorized. The payout is probably a trap.", progressChange: 180},
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
}

type Repository interface {
	LoadAll() ([]*Player, error)
	Save(*Player) error
	Delete(string) error
	ClaimRareItem(name, owner string) (bool, error)
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
}

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
	if worldChanged {
		if err := repo.SaveWorldState(world); err != nil {
			return nil, err
		}
	}
	return &Engine{repo: repo, rules: rules, rng: rng, world: world, users: users}, nil
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
		if guestPlayer := e.users[GuestKey(nick)]; guestPlayer != nil {
			guestKey := guestPlayer.Identity
			e.advanceLocked(guestPlayer, now)
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

func (e *Engine) Bind(nick, account string, now time.Time) (*Player, error) {
	if strings.TrimSpace(account) == "" {
		return nil, errors.New("account is required")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	guest := e.users[GuestKey(nick)]
	if guest == nil {
		guest = e.findLocked("", nick)
	}
	identity := AccountKey(account)
	contractChanged := false
	if guest != nil && guest.Guest {
		guestKey := guest.Identity
		e.advanceLocked(guest, now)
		migrated, err := e.repo.MigrateGuest(guestKey, identity, account, nick)
		if err != nil {
			return nil, err
		}
		delete(e.users, guestKey)
		e.users[identity] = migrated
		contractChanged = e.rekeyContractParticipantLocked(guestKey, identity)
		guest = migrated
	}
	if guest == nil {
		return e.joinLocked(identity, nick, account, now)
	}
	e.advanceLocked(guest, now)
	guest.Identity = identity
	guest.Account = account
	guest.Guest = false
	guest.Nick = nick
	guest.Connected = true
	guest.LastSeenAt = now
	guest.LastProgressAt = now
	guest.LastHeatAt = now
	e.users[identity] = guest
	if err := e.repo.Save(guest); err != nil {
		return nil, err
	}
	if contractChanged {
		if err := e.repo.SaveWorldState(e.world); err != nil {
			return nil, err
		}
	}
	return clonePlayer(guest), nil
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
	contractChanged := e.markContractFailedLocked(p.Identity)
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

func (e *Engine) DisconnectAll(now time.Time) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	contractChanged := false
	for _, p := range e.users {
		if !p.Connected {
			continue
		}
		e.advanceLocked(p, now)
		p.Connected = false
		p.LastProgressAt = now
		p.LastHeatAt = now
		contractChanged = e.markContractFailedLocked(p.Identity) || contractChanged
		if err := e.repo.Save(p); err != nil {
			return err
		}
	}
	if contractChanged {
		if err := e.repo.SaveWorldState(e.world); err != nil {
			return err
		}
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
	if p.Guest && newKey != oldKey {
		if existing := e.users[newKey]; existing != nil && existing != p {
			return 0, errors.New("nickname is already claimed by another guest runner")
		}
	}
	e.advanceLocked(p, now)
	penalty := PenaltySeconds(p.Level, ActivityNick, 0, e.rules)
	p.ProgressSeconds -= penalty
	p.Heat = addHeat(p.Heat, 4)
	p.Nick = newNick
	contractChanged := false
	if p.Guest {
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
	for key, p := range e.users {
		if p.Guest && !p.Connected && !p.LastSeenAt.IsZero() && now.Sub(p.LastSeenAt) > e.rules.GuestRetention {
			if err := e.repo.Delete(key); err != nil {
				return nil, err
			}
			delete(e.users, key)
			continue
		}
		if !p.Connected {
			continue
		}
		oldLevel := p.Level
		e.advanceLocked(p, now)
		if !p.NextDistrictAt.IsZero() && !now.Before(p.NextDistrictAt) {
			oldDistrict := p.District
			p.District = e.randomDistrictLocked(oldDistrict)
			p.NextDistrictAt = now.Add(e.rules.DistrictInterval)
			messages = append(messages, fmt.Sprintf("[GRID] %s drifted from %s to %s.", p.Nick, oldDistrict, p.District))
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
		for level := oldLevel + 1; level <= p.Level; level++ {
			slot := equipmentSlots[(level-2)%len(equipmentSlots)]
			if item := p.Equipment[slot]; item.Unique {
				messages = append(messages, fmt.Sprintf("[GRID] %s reached Rep %d. UNIQUE %s retained.", p.Nick, level, item.Name))
				continue
			}
			messages = append(messages, fmt.Sprintf("[GRID] %s reached Rep %d. %s upgraded.", p.Nick, level, slot))
		}
		if err := e.repo.Save(p); err != nil {
			return nil, err
		}
	}
	if message, err := e.collisionLocked(now); err != nil {
		return nil, err
	} else if message != "" {
		messages = append(messages, message)
	}
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
	e.world.PirateUntil = now.Add(e.rules.PirateDuration)
	message := fmt.Sprintf("[GRID] PIRATE FREQUENCY: transmissions are safe for %s.", formatDuration(e.rules.PirateDuration))
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
	// ponytail: snapshot uses the engine's existing global lock; add a read model if public traffic needs higher throughput.
	players := make([]*Player, 0, len(e.users))
	for _, p := range e.users {
		if p.Connected {
			e.advanceLocked(p, now)
			if err := e.repo.Save(p); err != nil {
				return nil, WorldState{}, err
			}
		}
		players = append(players, clonePlayer(p))
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
		LastProgressAt: now, LastSeenAt: now, NextEncounterAt: now, LastHeatAt: now, District: DistrictNeonMarket,
		Equipment: make(map[string]Item),
	}
}

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
	for _, p := range e.users {
		if strings.EqualFold(p.Nick, nick) {
			return p
		}
	}
	return nil
}

func (e *Engine) advanceLocked(p *Player, now time.Time) {
	if !p.Connected {
		p.LastProgressAt = now
		p.LastHeatAt = now
		return
	}
	if p.LastProgressAt.IsZero() {
		p.LastProgressAt = now
	}
	if p.LastHeatAt.IsZero() {
		p.LastHeatAt = now
	}
	elapsed := int64(now.Sub(p.LastProgressAt) / time.Second)
	if elapsed > 0 {
		p.ProgressSeconds += elapsed
		p.LastProgressAt = p.LastProgressAt.Add(time.Duration(elapsed) * time.Second)
	}
	if elapsedHeat := int64(now.Sub(p.LastHeatAt) / e.rules.HeatDecayInterval); elapsedHeat > 0 {
		p.Heat = clampHeat(p.Heat - int(elapsedHeat))
		p.LastHeatAt = p.LastHeatAt.Add(time.Duration(elapsedHeat) * e.rules.HeatDecayInterval)
	}
	for p.ProgressSeconds >= e.rules.LevelDuration(p.Level) {
		p.ProgressSeconds -= e.rules.LevelDuration(p.Level)
		p.Level++
		tier := 1 + (p.Level-1)/3
		slot := equipmentSlots[(p.Level-2)%len(equipmentSlots)]
		if item, ok := p.Equipment[slot]; !ok || !item.Unique {
			p.Equipment[slot] = Item{Name: equipmentName(slot, tier), Rating: tier}
		}
	}
	updateTitles(p)
}

type rareItem struct {
	name   string
	slot   string
	rating int
}

var rareItems = []rareItem{
	{name: "Blackglass Deck", slot: SlotDeck, rating: 8},
	{name: "Saint-9 Reflex Coil", slot: SlotNeuralImplant, rating: 7},
	{name: "Prototype Mantis Rig", slot: SlotWeaponRig, rating: 9},
	{name: "Aegis Nullplate", slot: SlotArmorPlating, rating: 8},
	{name: "Whisperbyte Scout", slot: SlotDrone, rating: 7},
}

const rareLootChancePercent = 5

func (e *Engine) encounterLocked(p *Player) (string, error) {
	rating := p.Level + p.EquipmentRating()
	if hasScar(p, ScarGhostSignal) {
		rating++
	}
	if hasScar(p, ScarBurnedOptic) {
		rating--
	}
	if p.Faction == FactionGhostline {
		rating += 2
	}
	rating += districtEncounterBonus(p.District)
	rating += p.Heat / 20
	threat := 1 + e.rng.Intn(max(2, rating+5+p.Heat/10))
	if rating >= threat {
		gain := max64(10, e.rules.LevelDuration(p.Level)/20)
		if p.Faction == FactionChrome {
			gain = gain * 3 / 2
		}
		p.ProgressSeconds += gain
		p.Heat = addHeat(p.Heat, 2)
		scar := ""
		if e.rng.Intn(25) == 0 && addScar(p, ScarGhostSignal) {
			scar = " Ghost Signal acquired."
		}
		item, err := e.rareLootLocked(p)
		if err != nil {
			return "", err
		}
		if item != nil {
			return fmt.Sprintf("[GRID] %s survived an ICE breach and recovered UNIQUE %s.%s", p.Nick, item.Name, scar), nil
		}
		return fmt.Sprintf("[GRID] %s survived an ICE breach and secured a data shard.%s", p.Nick, scar), nil
	}
	loss := max64(5, e.rules.LevelDuration(p.Level)/30)
	if p.Faction == FactionNomad {
		loss = max64(3, loss/2)
	}
	if p.District == DistrictGhostQuarter {
		loss = loss * 3 / 2
	}
	p.ProgressSeconds -= loss
	p.Heat = addHeat(p.Heat, 8)
	scar := ""
	if e.rng.Intn(12) == 0 && addScar(p, ScarBurnedOptic) {
		scar = " Burned Optic acquired."
	}
	return fmt.Sprintf("[GRID] %s hit hostile ICE and lost time escaping the trace.%s", p.Nick, scar), nil
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
	var opponents []*Player
	for _, p := range e.users {
		if p.Connected && p != initiator {
			opponents = append(opponents, p)
		}
	}
	if len(opponents) == 0 {
		return "", nil
	}
	opponent := opponents[e.rng.Intn(len(opponents))]
	winner, loser := initiator, opponent
	if collisionPower(opponent)+e.rng.Intn(5) > collisionPower(initiator)+e.rng.Intn(5) {
		winner, loser = opponent, initiator
	}

	gain := max64(30, e.rules.LevelDuration(winner.Level)/20)
	loss := collisionLoss(loser, max64(20, e.rules.LevelDuration(loser.Level)/24))
	winner.Heat = addHeat(winner.Heat, 3)
	loser.Heat = addHeat(loser.Heat, 5)
	scar := ""
	if e.rng.Intn(20) == 0 && addScar(winner, ScarSyntheticAdrenalGland) {
		scar = " Synthetic Adrenal Gland acquired."
	}
	message := ""
	switch e.rng.Intn(4) {
	case 0:
		winner.ProgressSeconds += gain
		loser.ProgressSeconds -= loss
		message = fmt.Sprintf("[GRID] COLLISION: %s cracked %s's deck and siphoned %s.%s", winner.Nick, loser.Nick, formatDuration(time.Duration(gain)*time.Second), scar)
	case 1:
		winner.ProgressSeconds += gain * 2
		loser.ProgressSeconds -= max64(15, loss/2)
		message = fmt.Sprintf("[GRID] COLLISION: %s won a dead-drop race against %s.%s", winner.Nick, loser.Nick, scar)
	case 2:
		winner.ProgressSeconds += max64(15, gain/2)
		loser.ProgressSeconds -= loss * 2
		message = fmt.Sprintf("[GRID] COLLISION: %s hunted %s through the %s.%s", winner.Nick, loser.Nick, loser.District, scar)
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
		message = fmt.Sprintf("[GRID] COLLISION: %s jammed %s's drone feed and took the shard.%s", winner.Nick, loser.Nick, scar)
	}
	updateTitles(winner)
	updateTitles(loser)
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

func (e *Engine) rareLootLocked(p *Player) (*Item, error) {
	if e.rng.Intn(100) >= rareLootChance(p) {
		return nil, nil
	}
	start := e.rng.Intn(len(rareItems))
	for i := range rareItems {
		candidate := rareItems[(start+i)%len(rareItems)]
		claimed, err := e.repo.ClaimRareItem(candidate.name, p.Identity)
		if err != nil {
			return nil, err
		}
		if !claimed {
			continue
		}
		item := &Item{Name: candidate.name, Rating: candidate.rating, Unique: true}
		ensureEquipment(p)
		p.Equipment[candidate.slot] = *item
		return item, nil
	}
	return nil, nil
}

func rareLootChance(p *Player) int {
	return min(100, rareLootChancePercent+p.Heat/10)
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

func updateTitles(p *Player) {
	if p.Level >= 5 {
		addTitle(p, "ICEbreaker")
	}
	if p.Level >= 10 {
		addTitle(p, "Nine-Day Signal")
	}
	if p.Heat >= 75 {
		addTitle(p, "Corporate Liability")
	}
	if p.District == DistrictFloodline && p.Level >= 3 {
		addTitle(p, "Ghost of Floodline")
	}
}

func (p *Player) CurrentTitle() string {
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
		e.world.PirateUntil = now.Add(e.rules.PirateDuration)
		return fmt.Sprintf("[GRID] PIRATE FREQUENCY: for %s, channel transmissions are safe.", formatDuration(e.rules.PirateDuration)), nil
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
				scarMessages = append(scarMessages, fmt.Sprintf("%s acquired %s", p.Nick, ScarCorporateBackdoor))
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
		updateTitles(p)
		if err := e.repo.Save(p); err != nil {
			return "", err
		}
	}
	message := fmt.Sprintf("[GRID] CITY EVENT: %s Active runners %s.", event.text, formatProgressChange(event.progressChange))
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
		Title:    "Helix Dynamics breach",
		District: districts[e.rng.Intn(len(districts))],
		EndsAt:   now.Add(e.rules.ContractDuration),
	}
	nicks := make([]string, 0, count)
	for _, index := range order[:count] {
		p := candidates[index]
		contract.Participants = append(contract.Participants, p.Identity)
		nicks = append(nicks, p.Nick)
	}
	e.world.Contract = contract
	return fmt.Sprintf("[GRID] CONTRACT: %s in %s. Stay linked for %s. Team: %s.", contract.Title, contract.District, formatDuration(e.rules.ContractDuration), strings.Join(nicks, ", ")), true, nil
}

func (e *Engine) resolveContractLocked(now time.Time) (string, error) {
	contract := e.world.Contract
	if contract == nil || now.Before(contract.EndsAt) {
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
		return fmt.Sprintf("[GRID] CONTRACT COMPLETE: %s cleared. The team secured its payout.", contract.Title), nil
	}
	return fmt.Sprintf("[GRID] CONTRACT FAILED: %s collapsed after a team signal dropped. The breach cost the team time.", contract.Title), nil
}

func (e *Engine) markContractFailedLocked(identity string) bool {
	if e.world.Contract == nil || e.world.Contract.Failed {
		return false
	}
	for _, participant := range e.world.Contract.Participants {
		if participant == identity {
			e.world.Contract.Failed = true
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
