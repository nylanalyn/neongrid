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
}

type Player struct {
	Identity        string
	Account         string
	Nick            string
	Guest           bool
	Level           int
	ProgressSeconds int64
	LastProgressAt  time.Time
	Connected       bool
	LastSeenAt      time.Time
	NextEncounterAt time.Time
	Faction         string
	Equipment       map[string]Item
}

type WorldState struct {
	PirateUntil     time.Time
	NextCityEventAt time.Time
	RecentEvents    []string
}

type cityEvent struct {
	text           string
	progressChange int64
}

var cityEvents = []cityEvent{
	{text: "BLACKOUT rolls across the lower stacks.", progressChange: -60},
	{text: "CORPORATE SWEEP detected. Keep your signatures cold.", progressChange: -90},
	{text: "DATA LEAK: fresh intel is spilling onto the Grid.", progressChange: 120},
	{text: "GANG WAR erupts beneath the maglev lines.", progressChange: -120},
	{text: "BOUNTY contract posted; every faction is watching.", progressChange: 90},
	{text: "MEGACORP RUN authorized. The payout is probably a trap.", progressChange: 180},
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
	PirateDuration            time.Duration
	GuestRetention            time.Duration
}

type Repository interface {
	LoadAll() ([]*Player, error)
	Save(*Player) error
	Delete(string) error
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
		p.Connected = false
		p.LastProgressAt = now
		ensureEquipment(p)
		if p.NextEncounterAt.IsZero() {
			p.NextEncounterAt = now.Add(rules.EncounterInterval)
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
	if world.NextCityEventAt.IsZero() {
		world.NextCityEventAt = now.Add(rules.CityEventInterval)
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
	if !guest {
		if guestPlayer := e.users[GuestKey(nick)]; guestPlayer != nil {
			e.advanceLocked(guestPlayer, now)
			migrated, err := e.repo.MigrateGuest(guestPlayer.Identity, identity, account, nick)
			if err != nil {
				return nil, err
			}
			delete(e.users, guestPlayer.Identity)
			p = migrated
			e.users[identity] = p
		}
	}
	if p == nil {
		p = newPlayer(identity, nick, account, now)
		p.NextEncounterAt = now.Add(e.rules.EncounterInterval)
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
	ensureEquipment(p)
	if err := e.repo.Save(p); err != nil {
		return nil, err
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
	if guest != nil && guest.Guest {
		e.advanceLocked(guest, now)
		migrated, err := e.repo.MigrateGuest(guest.Identity, identity, account, nick)
		if err != nil {
			return nil, err
		}
		delete(e.users, guest.Identity)
		e.users[identity] = migrated
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
	e.users[identity] = guest
	if err := e.repo.Save(guest); err != nil {
		return nil, err
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
	if p.Faction != "" {
		return nil, errors.New("faction is already locked")
	}
	e.advanceLocked(p, now)
	p.Faction = faction
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
	p.Connected = false
	p.LastSeenAt = now
	p.LastProgressAt = now
	if err := e.repo.Save(p); err != nil {
		return 0, err
	}
	return penalty, nil
}

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
		if err := e.repo.Save(p); err != nil {
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
	p.Nick = newNick
	if p.Guest {
		p.Identity = newKey
		delete(e.users, oldKey)
		if err := e.repo.Delete(oldKey); err != nil {
			return 0, err
		}
	}
	e.users[p.Identity] = p
	if err := e.repo.Save(p); err != nil {
		return 0, err
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
		if !p.NextEncounterAt.IsZero() && !now.Before(p.NextEncounterAt) {
			messages = append(messages, e.encounterLocked(p))
			p.NextEncounterAt = now.Add(e.rules.EncounterInterval)
			e.advanceLocked(p, now)
		}
		for level := oldLevel + 1; level <= p.Level; level++ {
			messages = append(messages, fmt.Sprintf("[GRID] %s reached Rep %d. %s upgraded.", p.Nick, level, equipmentSlots[(level-2)%len(equipmentSlots)]))
		}
		if err := e.repo.Save(p); err != nil {
			return nil, err
		}
	}
	if err := e.rememberLocked(messages...); err != nil {
		return nil, err
	}
	return messages, nil
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
	return e.world
}

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
		LastProgressAt: now, LastSeenAt: now, NextEncounterAt: now,
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
		return
	}
	if p.LastProgressAt.IsZero() {
		p.LastProgressAt = now
	}
	elapsed := int64(now.Sub(p.LastProgressAt) / time.Second)
	if elapsed > 0 {
		p.ProgressSeconds += elapsed
		p.LastProgressAt = p.LastProgressAt.Add(time.Duration(elapsed) * time.Second)
	}
	for p.ProgressSeconds >= e.rules.LevelDuration(p.Level) {
		p.ProgressSeconds -= e.rules.LevelDuration(p.Level)
		p.Level++
		tier := 1 + (p.Level-1)/3
		slot := equipmentSlots[(p.Level-2)%len(equipmentSlots)]
		p.Equipment[slot] = Item{Name: equipmentName(slot, tier), Rating: tier}
	}
}

func (e *Engine) encounterLocked(p *Player) string {
	rating := p.Level + p.EquipmentRating()
	if p.Faction == FactionGhostline {
		rating += 2
	}
	threat := 1 + e.rng.Intn(max(2, rating+5))
	if rating >= threat {
		gain := max64(10, e.rules.LevelDuration(p.Level)/20)
		if p.Faction == FactionChrome {
			gain = gain * 3 / 2
		}
		p.ProgressSeconds += gain
		return fmt.Sprintf("[GRID] %s survived an ICE breach and secured a data shard.", p.Nick)
	}
	loss := max64(5, e.rules.LevelDuration(p.Level)/30)
	if p.Faction == FactionNomad {
		loss = max64(3, loss/2)
	}
	p.ProgressSeconds -= loss
	return fmt.Sprintf("[GRID] %s hit hostile ICE and lost time escaping the trace.", p.Nick)
}

func validFaction(faction string) bool {
	switch faction {
	case FactionGhostline, FactionChrome, FactionNomad:
		return true
	default:
		return false
	}
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
	for _, p := range e.users {
		if !p.Connected {
			continue
		}
		p.ProgressSeconds += event.progressChange
		if err := e.repo.Save(p); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("[GRID] CITY EVENT: %s Active runners %s.", event.text, formatProgressChange(event.progressChange)), nil
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
	return &copy
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

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
