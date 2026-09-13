package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	_ "modernc.org/sqlite"

	"neongrid/internal/game"
)

type Store struct{ db *sql.DB }

const currentSchemaVersion = 6

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.init(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) init() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`
CREATE TABLE IF NOT EXISTS players (
  identity TEXT PRIMARY KEY,
  account TEXT NOT NULL DEFAULT '',
  nick TEXT NOT NULL,
  guest INTEGER NOT NULL DEFAULT 1,
  level INTEGER NOT NULL DEFAULT 1,
  progress_seconds INTEGER NOT NULL DEFAULT 0,
  last_progress_at INTEGER NOT NULL DEFAULT 0,
  connected INTEGER NOT NULL DEFAULT 0,
  last_seen_at INTEGER NOT NULL DEFAULT 0,
  next_encounter_at INTEGER NOT NULL DEFAULT 0,
  faction TEXT NOT NULL DEFAULT '',
  equipment_json TEXT NOT NULL DEFAULT '{}'
);
CREATE TABLE IF NOT EXISTS world_state (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS schema_version (
  version INTEGER NOT NULL
);`); err != nil {
		return err
	}

	version := 0
	err = tx.QueryRow("SELECT version FROM schema_version LIMIT 1").Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		version = 1
		if _, err := tx.Exec("INSERT INTO schema_version(version) VALUES(?)", version); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	for version < currentSchemaVersion {
		switch version {
		case 1:
			if _, err := tx.Exec("ALTER TABLE players ADD COLUMN district TEXT NOT NULL DEFAULT 'Neon Market'"); err != nil {
				return err
			}
			if _, err := tx.Exec("ALTER TABLE players ADD COLUMN next_district_at INTEGER NOT NULL DEFAULT 0"); err != nil {
				return err
			}
		case 2:
			if _, err := tx.Exec(`CREATE TABLE rare_items (
  name TEXT PRIMARY KEY,
  owner_identity TEXT NOT NULL
)`); err != nil {
				return err
			}
		case 3:
			if _, err := tx.Exec("ALTER TABLE players ADD COLUMN heat INTEGER NOT NULL DEFAULT 0"); err != nil {
				return err
			}
			if _, err := tx.Exec("ALTER TABLE players ADD COLUMN last_heat_at INTEGER NOT NULL DEFAULT 0"); err != nil {
				return err
			}
		case 4:
			if _, err := tx.Exec("ALTER TABLE players ADD COLUMN next_collision_at INTEGER NOT NULL DEFAULT 0"); err != nil {
				return err
			}
		case 5:
			if _, err := tx.Exec("ALTER TABLE players ADD COLUMN history_json TEXT NOT NULL DEFAULT '{}'"); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported schema version %d", version)
		}
		version++
		if _, err := tx.Exec("UPDATE schema_version SET version = ?", version); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) LoadAll() ([]*game.Player, error) {
	rows, err := s.db.Query(`SELECT identity, account, nick, guest, level, progress_seconds, last_progress_at, connected, last_seen_at, next_encounter_at, next_collision_at, district, next_district_at, heat, last_heat_at, faction, equipment_json, history_json FROM players`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var players []*game.Player
	for rows.Next() {
		p, err := scanPlayer(rows)
		if err != nil {
			return nil, err
		}
		players = append(players, p)
	}
	return players, rows.Err()
}

func (s *Store) Save(p *game.Player) error {
	return s.save(s.db, p)
}

func (s *Store) save(exec interface {
	Exec(string, ...any) (sql.Result, error)
}, p *game.Player) error {
	equipment, err := json.Marshal(p.Equipment)
	if err != nil {
		return err
	}
	history, err := json.Marshal(playerHistory{Scars: p.Scars, Titles: p.Titles, LastFactionSwapAt: p.LastFactionSwapAt, Alias: p.Alias})
	if err != nil {
		return err
	}
	_, err = exec.Exec(`
INSERT INTO players(identity, account, nick, guest, level, progress_seconds, last_progress_at, connected, last_seen_at, next_encounter_at, next_collision_at, district, next_district_at, heat, last_heat_at, faction, equipment_json, history_json)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(identity) DO UPDATE SET
 account=excluded.account, nick=excluded.nick, guest=excluded.guest, level=excluded.level,
 progress_seconds=excluded.progress_seconds, last_progress_at=excluded.last_progress_at,
 connected=excluded.connected, last_seen_at=excluded.last_seen_at, next_encounter_at=excluded.next_encounter_at, next_collision_at=excluded.next_collision_at,
 district=excluded.district, next_district_at=excluded.next_district_at,
 heat=excluded.heat, last_heat_at=excluded.last_heat_at,
 faction=excluded.faction, equipment_json=excluded.equipment_json, history_json=excluded.history_json`,
		p.Identity, p.Account, p.Nick, boolInt(p.Guest), p.Level, p.ProgressSeconds,
		unix(p.LastProgressAt), boolInt(p.Connected), unix(p.LastSeenAt), unix(p.NextEncounterAt), unix(p.NextCollisionAt), p.District, unix(p.NextDistrictAt), p.Heat, unix(p.LastHeatAt), p.Faction, string(equipment), string(history))
	return err
}

func (s *Store) Delete(identity string) error {
	_, err := s.db.Exec("DELETE FROM players WHERE identity = ?", identity)
	return err
}

func (s *Store) ClaimRareItem(name, owner string) (bool, error) {
	result, err := s.db.Exec(`INSERT INTO rare_items(name, owner_identity) VALUES(?, ?) ON CONFLICT(name) DO NOTHING`, name, owner)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func (s *Store) MigrateGuest(guestKey, accountKey, account, nick string) (*game.Player, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	guest, err := loadByKey(tx, guestKey)
	if err != nil {
		return nil, err
	}
	accountPlayer, err := loadByKey(tx, accountKey)
	if err != nil {
		return nil, err
	}
	if guest == nil && accountPlayer == nil {
		return nil, errors.New("guest and account runner not found")
	}
	merged := accountPlayer
	if merged == nil {
		merged = guest
	} else if guest != nil {
		merged = merge(guest, merged)
	}
	merged.Identity, merged.Account, merged.Nick, merged.Guest = accountKey, account, nick, false
	merged.Connected = true
	if err := s.save(tx, merged); err != nil {
		return nil, err
	}
	if guest != nil && guestKey != accountKey {
		if _, err := tx.Exec("DELETE FROM players WHERE identity = ?", guestKey); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return merged, nil
}

func (s *Store) Top(limit int) ([]*game.Player, error) {
	rows, err := s.db.Query(`SELECT identity, account, nick, guest, level, progress_seconds, last_progress_at, connected, last_seen_at, next_encounter_at, next_collision_at, district, next_district_at, heat, last_heat_at, faction, equipment_json, history_json FROM players ORDER BY level DESC, progress_seconds DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var players []*game.Player
	for rows.Next() {
		p, err := scanPlayer(rows)
		if err != nil {
			return nil, err
		}
		players = append(players, p)
	}
	return players, rows.Err()
}

func (s *Store) LoadWorldState() (game.WorldState, error) {
	rows, err := s.db.Query("SELECT key, value FROM world_state")
	if err != nil {
		return game.WorldState{}, err
	}
	defer rows.Close()
	var state game.WorldState
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return state, err
		}
		if key == "recent_events" {
			if err := json.Unmarshal([]byte(value), &state.RecentEvents); err != nil {
				return state, fmt.Errorf("world state %s: %w", key, err)
			}
			continue
		}
		if key == "active_contract" {
			if err := json.Unmarshal([]byte(value), &state.Contract); err != nil {
				return state, fmt.Errorf("world state %s: %w", key, err)
			}
			continue
		}
		seconds, err := parseUnix(value)
		if err != nil {
			return state, fmt.Errorf("world state %s: %w", key, err)
		}
		switch key {
		case "pirate_until":
			state.PirateUntil = fromUnix(seconds)
		case "next_city_event_at":
			state.NextCityEventAt = fromUnix(seconds)
		case "faction_swap_until":
			state.FactionSwapUntil = fromUnix(seconds)
		case "faction_swap_started_at":
			state.FactionSwapStartedAt = fromUnix(seconds)
		case "next_faction_swap_at":
			state.NextFactionSwapAt = fromUnix(seconds)
		}
	}
	return state, rows.Err()
}

func (s *Store) SaveWorldState(state game.WorldState) error {
	recentEvents, err := json.Marshal(state.RecentEvents)
	if err != nil {
		return err
	}
	contract, err := json.Marshal(state.Contract)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for key, value := range map[string]string{
		"pirate_until":            fmt.Sprint(unix(state.PirateUntil)),
		"next_city_event_at":      fmt.Sprint(unix(state.NextCityEventAt)),
		"faction_swap_until":      fmt.Sprint(unix(state.FactionSwapUntil)),
		"faction_swap_started_at": fmt.Sprint(unix(state.FactionSwapStartedAt)),
		"next_faction_swap_at":    fmt.Sprint(unix(state.NextFactionSwapAt)),
		"recent_events":           string(recentEvents),
		"active_contract":         string(contract),
	} {
		if _, err := tx.Exec(`INSERT INTO world_state(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type scanner interface{ Scan(...any) error }

type playerHistory struct {
	Scars             []string  `json:"scars"`
	Titles            []string  `json:"titles"`
	LastFactionSwapAt time.Time `json:"last_faction_swap_at,omitempty"`
	Alias             string    `json:"alias,omitempty"`
}

func scanPlayer(row scanner) (*game.Player, error) {
	var p game.Player
	var guest, connected int
	var lastProgress, lastSeen, nextEncounter, nextCollision, nextDistrict, lastHeat int64
	var heat int
	var equipment, history string
	if err := row.Scan(&p.Identity, &p.Account, &p.Nick, &guest, &p.Level, &p.ProgressSeconds, &lastProgress, &connected, &lastSeen, &nextEncounter, &nextCollision, &p.District, &nextDistrict, &heat, &lastHeat, &p.Faction, &equipment, &history); err != nil {
		return nil, err
	}
	p.Guest, p.Connected = guest != 0, connected != 0
	p.LastProgressAt, p.LastSeenAt, p.NextEncounterAt, p.NextCollisionAt, p.NextDistrictAt = fromUnix(lastProgress), fromUnix(lastSeen), fromUnix(nextEncounter), fromUnix(nextCollision), fromUnix(nextDistrict)
	p.Heat, p.LastHeatAt = heat, fromUnix(lastHeat)
	if equipment == "" {
		equipment = "{}"
	}
	if err := json.Unmarshal([]byte(equipment), &p.Equipment); err != nil {
		return nil, err
	}
	if p.Equipment == nil {
		p.Equipment = map[string]game.Item{}
	}
	if history == "" {
		history = "{}"
	}
	var savedHistory playerHistory
	if err := json.Unmarshal([]byte(history), &savedHistory); err != nil {
		return nil, err
	}
	p.Scars, p.Titles, p.Alias = savedHistory.Scars, savedHistory.Titles, savedHistory.Alias
	p.LastFactionSwapAt = savedHistory.LastFactionSwapAt
	return &p, nil
}

func loadByKey(tx *sql.Tx, key string) (*game.Player, error) {
	row := tx.QueryRow(`SELECT identity, account, nick, guest, level, progress_seconds, last_progress_at, connected, last_seen_at, next_encounter_at, next_collision_at, district, next_district_at, heat, last_heat_at, faction, equipment_json, history_json FROM players WHERE identity = ?`, key)
	p, err := scanPlayer(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return p, err
}

func merge(guest, account *game.Player) *game.Player {
	result := clone(account)
	if guest.Level > result.Level || (guest.Level == result.Level && guest.ProgressSeconds > result.ProgressSeconds) {
		result.Level, result.ProgressSeconds = guest.Level, guest.ProgressSeconds
	}
	if guest.LastSeenAt.After(result.LastSeenAt) {
		result.LastSeenAt = guest.LastSeenAt
	}
	if guest.LastProgressAt.After(result.LastProgressAt) {
		result.LastProgressAt = guest.LastProgressAt
	}
	if guest.LastSeenAt.After(account.LastSeenAt) {
		result.District, result.NextDistrictAt, result.NextCollisionAt = guest.District, guest.NextDistrictAt, guest.NextCollisionAt
	}
	if guest.Heat > result.Heat {
		result.Heat = guest.Heat
	}
	if guest.LastHeatAt.After(result.LastHeatAt) {
		result.LastHeatAt = guest.LastHeatAt
	}
	if guest.LastFactionSwapAt.After(result.LastFactionSwapAt) {
		result.LastFactionSwapAt = guest.LastFactionSwapAt
	}
	if result.Alias == "" {
		result.Alias = guest.Alias
	}
	for slot, item := range guest.Equipment {
		if item.Rating > result.Equipment[slot].Rating {
			result.Equipment[slot] = item
		}
	}
	result.Scars = appendUnique(result.Scars, guest.Scars...)
	result.Titles = appendUnique(result.Titles, guest.Titles...)
	return result
}

func appendUnique(values []string, additions ...string) []string {
	for _, addition := range additions {
		found := false
		for _, value := range values {
			if value == addition {
				found = true
				break
			}
		}
		if !found && addition != "" {
			values = append(values, addition)
		}
	}
	return values
}

func clone(p *game.Player) *game.Player {
	result := *p
	result.Equipment = map[string]game.Item{}
	for slot, item := range p.Equipment {
		result.Equipment[slot] = item
	}
	return &result
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
func unix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}
func fromUnix(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.Unix(value, 0)
}
func parseUnix(value string) (int64, error) {
	return strconv.ParseInt(value, 10, 64)
}
