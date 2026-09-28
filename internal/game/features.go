package game

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	StanceHot  = "hot"
	StanceCold = "cold"

	// factionWeekLength is how long faction scores accumulate before a
	// champion is crowned; the champion gets championEdge percent on ICE.
	factionWeekLength = 7 * 24 * time.Hour
	championEdge      = 5

	bulletinInterval = 24 * time.Hour

	// streakDay is one Ghost Protocol payout period; a rejoin within
	// streakGrace of last being seen keeps the streak going.
	streakDay   = 24 * time.Hour
	streakGrace = time.Hour

	// rivalryBouts is how many collisions make two runners rivals.
	rivalryBouts = 5
	// theftChancePercent is the chance a collision winner steals one of the
	// loser's artifacts.
	theftChancePercent = 20

	raidWarning = 30 * time.Minute

	deadDropAlphabet = "ACDEFHJKMNPRTVWXY3479"
	deadDropNoise    = "#%&@*+=~"
	deadDropLength   = 5
)

var factionOrder = []string{FactionGhostline, FactionChrome, FactionNomad}

// FactionWeek is the running weekly faction competition.
type FactionWeek struct {
	Scores   map[string]int `json:"scores,omitempty"`
	EndsAt   time.Time      `json:"ends_at"`
	Champion string         `json:"champion,omitempty"`
}

// Raid is a pending Blackwall assault that tests every linked runner.
type Raid struct {
	ICE        string    `json:"ice"`
	District   string    `json:"district"`
	ResolvesAt time.Time `json:"resolves_at"`
}

// Bulletin collects the day's highlights for the daily summary.
type Bulletin struct {
	NextAt        time.Time      `json:"next_at"`
	Climbs        map[string]int `json:"climbs,omitempty"`
	CollisionWins map[string]int `json:"collision_wins,omitempty"`
}

// ---- Stance ----

// SetStance sets how hard a runner pushes against ICE: hot means better
// payouts and loot but worse odds and more Heat; cold is the reverse.
func (e *Engine) SetStance(identity, nick, stance string, now time.Time) (*Player, error) {
	stance = strings.ToLower(strings.TrimSpace(stance))
	if stance == "normal" {
		stance = ""
	}
	if stance != "" && stance != StanceHot && stance != StanceCold {
		return nil, errors.New("stance must be hot, cold, or normal")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	p := e.findLocked(identity, nick)
	if p == nil {
		return nil, ErrRunnerNotFound
	}
	e.advanceLocked(p, now)
	p.Stance = stance
	p.LastSeenAt = now
	if err := e.repo.Save(p); err != nil {
		return nil, err
	}
	return clonePlayer(p), nil
}

// StanceLabel is the display name for a runner's stance.
func StanceLabel(stance string) string {
	if stance == "" {
		return "normal"
	}
	return stance
}

func stanceOdds(p *Player) int {
	switch p.Stance {
	case StanceHot:
		return -10
	case StanceCold:
		return 10
	}
	return 0
}

func stanceReward(p *Player, gain int64) int64 {
	switch p.Stance {
	case StanceHot:
		return gain * 3 / 2
	case StanceCold:
		return gain * 3 / 5
	}
	return gain
}

func stanceHeat(p *Player, heat int) int {
	switch p.Stance {
	case StanceHot:
		return heat * 3 / 2
	case StanceCold:
		return heat / 2
	}
	return heat
}

// ---- Faction week ----

func (e *Engine) scoreFactionLocked(p *Player, points int) {
	if p.Faction == "" {
		return
	}
	if e.world.FactionWeek.Scores == nil {
		e.world.FactionWeek.Scores = make(map[string]int)
	}
	e.world.FactionWeek.Scores[p.Faction] += points
}

func (e *Engine) factionWeekTickLocked(now time.Time) string {
	week := &e.world.FactionWeek
	if week.EndsAt.IsZero() {
		week.EndsAt = now.Add(factionWeekLength)
		return ""
	}
	if now.Before(week.EndsAt) {
		return ""
	}
	winner, best, tied := "", 0, false
	for _, faction := range factionOrder {
		switch score := week.Scores[faction]; {
		case score > best:
			winner, best, tied = faction, score, false
		case score == best && score > 0:
			tied = true
		}
	}
	standings := FactionStandings(week.Scores)
	week.Scores = nil
	week.EndsAt = now.Add(factionWeekLength)
	week.Champion = ""
	switch {
	case best == 0:
		return ""
	case tied:
		return fmt.Sprintf("[GRID] FACTION WEEK ends in a dead heat (%s). Nobody gets the edge this week.", standings)
	}
	week.Champion = winner
	return fmt.Sprintf("[GRID] FACTION WEEK: %s takes it (%s). %s runners get an edge on ICE until next week.", FactionLabel(winner), standings, FactionLabel(winner))
}

// FactionStandings lists faction scores, highest first, e.g. "Chrome 42 · Nomad 12".
func FactionStandings(scores map[string]int) string {
	factions := append([]string(nil), factionOrder...)
	sort.SliceStable(factions, func(i, j int) bool { return scores[factions[i]] > scores[factions[j]] })
	parts := make([]string, 0, len(factions))
	for _, faction := range factions {
		parts = append(parts, fmt.Sprintf("%s %d", FactionLabel(faction), scores[faction]))
	}
	return strings.Join(parts, " · ")
}

func FactionLabel(faction string) string {
	if faction == "" {
		return "unaffiliated"
	}
	return strings.ToUpper(faction[:1]) + faction[1:]
}

// ---- Pirate-frequency dead drops ----

// openPirateFrequencyLocked starts a safe-chat window with a dead drop hidden
// in it: a short code interleaved with noise that the first runner to type
// it cleanly can claim.
func (e *Engine) openPirateFrequencyLocked(now time.Time) string {
	e.world.PirateUntil = now.Add(e.rules.PirateDuration)
	var code, garbled strings.Builder
	for i := 0; i < deadDropLength; i++ {
		c := deadDropAlphabet[e.rng.Intn(len(deadDropAlphabet))]
		code.WriteByte(c)
		garbled.WriteByte(c)
		if i < deadDropLength-1 {
			garbled.WriteByte(deadDropNoise[e.rng.Intn(len(deadDropNoise))])
		}
	}
	e.world.DeadDrop = code.String()
	return fmt.Sprintf("[GRID] PIRATE FREQUENCY: channel transmissions are safe for %s. A DEAD DROP is buried in the static: %s. Strip the noise and transmit the clean code to claim it.",
		formatDuration(e.rules.PirateDuration), garbled.String())
}

// ClaimDeadDrop checks a game-channel message against the open dead drop and
// returns an announcement when it claims it.
func (e *Engine) ClaimDeadDrop(identity, nick, channel, text string, now time.Time) (string, error) {
	if !strings.EqualFold(channel, e.rules.GameChannel) {
		return "", nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.world.DeadDrop == "" || !now.Before(e.world.PirateUntil) || !strings.EqualFold(strings.TrimSpace(text), e.world.DeadDrop) {
		return "", nil
	}
	p := e.findLocked(identity, nick)
	if p == nil || !p.Connected {
		return "", nil
	}
	e.advanceLocked(p, now)
	gain := max64(60, e.rules.LevelDuration(p.Level)/10)
	p.ProgressSeconds += gain
	p.Stats.DeadDrops++
	e.scoreFactionLocked(p, 1)
	e.updateTitlesLocked(p)
	e.world.DeadDrop = ""
	message := fmt.Sprintf("[GRID] DEAD DROP claimed: %s decoded the static and pocketed %s.", p.DisplayName(), formatDuration(time.Duration(gain)*time.Second))
	if err := e.repo.Save(p); err != nil {
		return "", err
	}
	return message, e.rememberLocked(message)
}

func (e *Engine) deadDropExpiryLocked(now time.Time) string {
	if e.world.DeadDrop == "" || now.Before(e.world.PirateUntil) {
		return ""
	}
	e.world.DeadDrop = ""
	return "[GRID] The pirate frequency closed. The dead drop decayed back into static, unclaimed."
}

// ---- Blackwall raids ----

func (e *Engine) startRaidLocked(now time.Time) (string, bool) {
	if e.world.Raid != nil {
		return "", false
	}
	linked := 0
	for _, p := range e.users {
		if p.Connected {
			linked++
		}
	}
	if linked < 2 {
		return "", false
	}
	raid := &Raid{ICE: e.pick(iceNames), District: e.pick(districts), ResolvesAt: now.Add(raidWarning)}
	e.world.Raid = raid
	return fmt.Sprintf("[GRID] BLACKWALL ALERT: %s is surfacing in %s. Every linked runner gets tested against it in %s. Stay on the Grid.",
		raid.ICE, raid.District, formatDuration(raidWarning)), true
}

// resolveRaidLocked pits the combined gear and Rep of everyone linked against
// a wall built for their levels. Extra runners make the team a little more
// than the sum of its parts.
func (e *Engine) resolveRaidLocked(now time.Time) (string, error) {
	raid := e.world.Raid
	if raid == nil || now.Before(raid.ResolvesAt) {
		return "", nil
	}
	e.world.Raid = nil
	var team []*Player
	power, wall := 0, 0
	for _, p := range e.users {
		if !p.Connected {
			continue
		}
		e.advanceLocked(p, now)
		team = append(team, p)
		power += p.Level + p.EquipmentRating()
		if p.Faction == FactionGhostline {
			power += 2
		}
		wall += p.Level + expectedGearRating(p.Level)
	}
	if len(team) == 0 {
		return fmt.Sprintf("[GRID] %s surfaced in %s and found nobody home. It sank back into the deep Grid.", raid.ICE, raid.District), nil
	}
	teamwork := max(75, 100-5*(len(team)-1))
	roll := 55 + e.rng.Intn(76)
	success := power*100*100 >= wall*roll*teamwork
	for _, p := range team {
		if success {
			p.ProgressSeconds += max64(60, e.rules.LevelDuration(p.Level)/8)
			p.Heat = addHeat(p.Heat, 3)
			p.Stats.RaidWins++
			e.scoreFactionLocked(p, 1)
			e.updateTitlesLocked(p)
		} else {
			p.ProgressSeconds -= max64(60, e.rules.LevelDuration(p.Level)/12)
			p.Heat = addHeat(p.Heat, 6)
		}
		if err := e.repo.Save(p); err != nil {
			return "", err
		}
	}
	runners := "runner"
	if len(team) != 1 {
		runners = "runners"
	}
	if success {
		return fmt.Sprintf("[GRID] BLACKWALL DOWN: %d %s tore %s apart in %s. Everyone linked shares the payout.", len(team), runners, raid.ICE, raid.District), nil
	}
	return fmt.Sprintf("[GRID] BLACKWALL HOLDS: %s shredded a %d-%s assault in %s. Everyone linked takes the hit.", raid.ICE, len(team), strings.TrimSuffix(runners, "s"), raid.District), nil
}

// ---- Ghost Protocol streaks ----

func (e *Engine) streakTickLocked(p *Player, now time.Time) string {
	if p.StreakSince.IsZero() {
		p.StreakSince = now
		return ""
	}
	days := int(now.Sub(p.StreakSince) / streakDay)
	if days <= p.StreakDays {
		return ""
	}
	p.StreakDays = days
	if days > p.Stats.LongestStreak {
		p.Stats.LongestStreak = days
	}
	bonus := max64(120, e.rules.LevelDuration(p.Level)/4)
	p.ProgressSeconds += bonus
	e.updateTitlesLocked(p)
	unit := "days"
	if days == 1 {
		unit = "day"
	}
	return fmt.Sprintf("[GRID] GHOST PROTOCOL: %s has held the link for %d %s straight. +%s.", p.DisplayName(), days, unit, formatDuration(time.Duration(bonus)*time.Second))
}

// ---- Daily bulletin ----

func (e *Engine) noteBulletinLocked(counts *map[string]int, identity string, amount int) {
	if *counts == nil {
		*counts = make(map[string]int)
	}
	(*counts)[identity] += amount
}

func (e *Engine) bulletinTickLocked(now time.Time) string {
	bulletin := &e.world.Bulletin
	if bulletin.NextAt.IsZero() {
		bulletin.NextAt = now.Add(bulletinInterval)
		return ""
	}
	if now.Before(bulletin.NextAt) {
		return ""
	}
	var parts []string
	if p, n := e.leaderLocked(bulletin.Climbs); p != nil {
		parts = append(parts, fmt.Sprintf("top climber %s (+%d Rep)", p.DisplayName(), n))
	}
	heat := make(map[string]int)
	for identity, p := range e.users {
		heat[identity] = p.Heat
	}
	if p, n := e.leaderLocked(heat); p != nil {
		parts = append(parts, fmt.Sprintf("most wanted %s (Heat %d)", p.DisplayName(), n))
	}
	if p, n := e.leaderLocked(bulletin.CollisionWins); p != nil {
		plural := "s"
		if n == 1 {
			plural = ""
		}
		parts = append(parts, fmt.Sprintf("street champ %s (%d collision win%s)", p.DisplayName(), n, plural))
	}
	if len(e.world.FactionWeek.Scores) > 0 {
		parts = append(parts, "faction week: "+FactionStandings(e.world.FactionWeek.Scores))
	}
	bulletin.Climbs, bulletin.CollisionWins = nil, nil
	bulletin.NextAt = now.Add(bulletinInterval)
	if len(parts) == 0 {
		return ""
	}
	return "[GRID] DAILY BULLETIN: " + strings.Join(parts, " · ")
}

// leaderLocked returns the runner with the highest positive count; ties go
// to the lowest identity so the bulletin is deterministic.
func (e *Engine) leaderLocked(counts map[string]int) (*Player, int) {
	identities := make([]string, 0, len(counts))
	for identity := range counts {
		identities = append(identities, identity)
	}
	sort.Strings(identities)
	var leader *Player
	best := 0
	for _, identity := range identities {
		if p := e.users[identity]; p != nil && counts[identity] > best {
			leader, best = p, counts[identity]
		}
	}
	return leader, best
}

// ---- Collisions: locality, rivals, theft ----

// pickOpponentLocked prefers runners in the initiator's district, and within
// that pool gives their rival an even chance of being the one they meet.
func (e *Engine) pickOpponentLocked(initiator *Player) *Player {
	var local, anywhere []*Player
	for _, p := range e.users {
		if !p.Connected || p == initiator {
			continue
		}
		anywhere = append(anywhere, p)
		if p.District == initiator.District {
			local = append(local, p)
		}
	}
	pool := local
	if len(pool) == 0 {
		pool = anywhere
	}
	if len(pool) == 0 {
		return nil
	}
	// Sort for determinism under a seeded RNG; map iteration order is random.
	sort.Slice(pool, func(i, j int) bool { return pool[i].Identity < pool[j].Identity })
	if rival := topRival(initiator); rival != "" && e.rng.Intn(2) == 0 {
		for _, p := range pool {
			if p.Identity == rival {
				return p
			}
		}
	}
	return pool[e.rng.Intn(len(pool))]
}

func recordBout(a, b *Player) int {
	if a.Rivals == nil {
		a.Rivals = make(map[string]int)
	}
	if b.Rivals == nil {
		b.Rivals = make(map[string]int)
	}
	a.Rivals[b.Identity]++
	b.Rivals[a.Identity]++
	return a.Rivals[b.Identity]
}

// topRival is the identity a runner has collided with most, if at least
// rivalryBouts times.
func topRival(p *Player) string {
	best, rival := rivalryBouts-1, ""
	for identity, bouts := range p.Rivals {
		if bouts > best || (bouts == best && rival != "" && identity < rival) {
			best, rival = bouts, identity
		}
	}
	return rival
}

// stealUniqueLocked sometimes moves one of the loser's artifacts to the
// winner, keeping its remaining lifespan. It returns the stolen item's name.
func (e *Engine) stealUniqueLocked(winner, loser *Player) (string, error) {
	var slots []string
	for _, slot := range equipmentSlots {
		if loser.Equipment[slot].Unique {
			slots = append(slots, slot)
		}
	}
	if len(slots) == 0 || e.rng.Intn(100) >= theftChancePercent {
		return "", nil
	}
	slot := slots[e.rng.Intn(len(slots))]
	item := loser.Equipment[slot]
	if err := e.repo.TransferRareItem(item.Name, winner.Identity); err != nil {
		return "", err
	}
	remaining := max(1, item.BreaksAt-loser.Level)
	item.BreaksAt = winner.Level + remaining
	ensureEquipment(winner)
	winner.Equipment[slot] = item
	loser.Equipment[slot] = Item{Name: equipmentName(slot, gearTier(loser.Level)), Rating: gearTier(loser.Level)}
	winner.Stats.Thefts++
	winner.Stats.Uniques++
	e.updateTitlesLocked(winner)
	return item.Name, nil
}

func cloneCounts(counts map[string]int) map[string]int {
	if counts == nil {
		return nil
	}
	out := make(map[string]int, len(counts))
	for k, v := range counts {
		out[k] = v
	}
	return out
}
