package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"

	"neongrid/internal/game"
)

type Config struct {
	Server             string            `yaml:"server"`
	Port               int               `yaml:"port"`
	TLS                bool              `yaml:"tls"`
	TLS12Only          bool              `yaml:"tls12_only"`
	Nick               string            `yaml:"nick"`
	User               string            `yaml:"user"`
	Name               string            `yaml:"name"`
	Channel            string            `yaml:"channel"`
	Database           string            `yaml:"database"`
	WebListen          string            `yaml:"web_listen"`
	AdminAccounts      []string          `yaml:"admin_accounts"`
	NickServ           NickServConfig    `yaml:"nickserv"`
	Progression        ProgressionConfig `yaml:"progression"`
	Penalty            PenaltyConfig     `yaml:"penalty"`
	Events             EventConfig       `yaml:"events"`
	GuestRetentionDays int               `yaml:"guest_retention_days"`
	ReconnectSeconds   int               `yaml:"reconnect_seconds"`
}

type NickServConfig struct {
	Name            string `yaml:"name"`
	Password        string `yaml:"password"`
	IdentifyCommand string `yaml:"identify_command"`
}

type ProgressionConfig struct {
	BaseMinutes      int `yaml:"base_minutes"`
	LevelStepMinutes int `yaml:"level_step_minutes"`
}

type PenaltyConfig struct {
	SpeechBaseSeconds         int `yaml:"speech_base_seconds"`
	SpeechPerLevelSeconds     int `yaml:"speech_per_level_seconds"`
	SpeechPerCharacterSeconds int `yaml:"speech_per_character_seconds"`
	ActionBaseSeconds         int `yaml:"action_base_seconds"`
	ActionPerLevelSeconds     int `yaml:"action_per_level_seconds"`
	ActionPerCharacterSeconds int `yaml:"action_per_character_seconds"`
	NickSeconds               int `yaml:"nick_seconds"`
	PartSeconds               int `yaml:"part_seconds"`
	QuitSeconds               int `yaml:"quit_seconds"`
	KickSeconds               int `yaml:"kick_seconds"`
}

type EventConfig struct {
	TickSeconds          int `yaml:"tick_seconds"`
	EncounterMinutes     int `yaml:"encounter_minutes"`
	CityEventMinutes     int `yaml:"city_event_minutes"`
	PirateMinutes        int `yaml:"pirate_minutes"`
	DistrictHours        int `yaml:"district_hours"`
	HeatDecayMinutes     int `yaml:"heat_decay_minutes"`
	ContractHours        int `yaml:"contract_hours"`
	ContractParticipants int `yaml:"contract_participants"`
	CollisionMinutes     int `yaml:"collision_minutes"`
}

func Defaults() Config {
	return Config{
		Server: "irc.libera.chat", Port: 6697, TLS: true,
		Nick:    "neongrid",
		Channel: "#neongrid", Database: "neongrid.db",
		NickServ:    NickServConfig{Name: "NickServ", IdentifyCommand: "IDENTIFY"},
		Progression: ProgressionConfig{BaseMinutes: 30, LevelStepMinutes: 2},
		Penalty: PenaltyConfig{
			SpeechBaseSeconds: 30, SpeechPerLevelSeconds: 5, SpeechPerCharacterSeconds: 1,
			ActionBaseSeconds: 45, ActionPerLevelSeconds: 7, ActionPerCharacterSeconds: 1,
			NickSeconds: 90, PartSeconds: 180, QuitSeconds: 240, KickSeconds: 360,
		},
		Events:             EventConfig{TickSeconds: 30, EncounterMinutes: 60, CityEventMinutes: 120, PirateMinutes: 5, DistrictHours: 6, HeatDecayMinutes: 30, ContractHours: 8, ContractParticipants: 4, CollisionMinutes: 90},
		GuestRetentionDays: 14, ReconnectSeconds: 10,
	}
}

func Load(path string) (Config, error) {
	cfg := Defaults()
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return Config{}, err
		}
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return Config{}, fmt.Errorf("read config: %w", err)
		}
	}
	if err := applyEnv(&cfg); err != nil {
		return Config{}, err
	}
	if cfg.User == "" {
		cfg.User = cfg.Nick
	}
	if cfg.Name == "" {
		cfg.Name = cfg.Nick
	}
	if cfg.NickServ.Name == "" {
		cfg.NickServ.Name = "NickServ"
	}
	if cfg.NickServ.IdentifyCommand == "" {
		cfg.NickServ.IdentifyCommand = "IDENTIFY"
	}
	if cfg.GuestRetentionDays < 1 {
		cfg.GuestRetentionDays = 14
	}
	if cfg.ReconnectSeconds < 1 {
		cfg.ReconnectSeconds = 10
	}
	if cfg.Events.TickSeconds < 1 {
		cfg.Events.TickSeconds = 30
	}
	return cfg, validate(cfg)
}

func (c Config) Rules() game.Rules {
	return game.Rules{
		GameChannel:               c.Channel,
		BaseLevelSeconds:          int64(c.Progression.BaseMinutes) * 60,
		LevelStepSeconds:          int64(c.Progression.LevelStepMinutes) * 60,
		SpeechBaseSeconds:         int64(c.Penalty.SpeechBaseSeconds),
		SpeechPerLevelSeconds:     int64(c.Penalty.SpeechPerLevelSeconds),
		SpeechPerCharacterSeconds: int64(c.Penalty.SpeechPerCharacterSeconds),
		ActionBaseSeconds:         int64(c.Penalty.ActionBaseSeconds),
		ActionPerLevelSeconds:     int64(c.Penalty.ActionPerLevelSeconds),
		ActionPerCharacterSeconds: int64(c.Penalty.ActionPerCharacterSeconds),
		NickPenaltySeconds:        int64(c.Penalty.NickSeconds),
		PartPenaltySeconds:        int64(c.Penalty.PartSeconds),
		QuitPenaltySeconds:        int64(c.Penalty.QuitSeconds),
		KickPenaltySeconds:        int64(c.Penalty.KickSeconds),
		EncounterInterval:         durationMinutes(c.Events.EncounterMinutes, 60),
		CityEventInterval:         durationMinutes(c.Events.CityEventMinutes, 120),
		DistrictInterval:          time.Duration(maxInt(c.Events.DistrictHours, 6)) * time.Hour,
		HeatDecayInterval:         durationMinutes(c.Events.HeatDecayMinutes, 30),
		ContractDuration:          time.Duration(maxInt(c.Events.ContractHours, 8)) * time.Hour,
		ContractMaxParticipants:   maxInt(c.Events.ContractParticipants, 4),
		CollisionInterval:         durationMinutes(c.Events.CollisionMinutes, 90),
		PirateDuration:            durationMinutes(c.Events.PirateMinutes, 5),
		GuestRetention:            durationDays(c.GuestRetentionDays, 14),
	}
}

func durationMinutes(value, fallback int) time.Duration {
	if value < 1 {
		value = fallback
	}
	return time.Duration(value) * time.Minute
}

func durationDays(value, fallback int) time.Duration {
	if value < 1 {
		value = fallback
	}
	return time.Duration(value) * 24 * time.Hour
}

func validate(c Config) error {
	if c.Server == "" {
		return errors.New("server is required")
	}
	if c.Port < 1 || c.Port > 65535 {
		return errors.New("port must be 1..65535")
	}
	if c.Nick == "" {
		return errors.New("nick is required")
	}
	if c.Channel == "" || c.Channel[0] != '#' {
		return errors.New("channel must start with #")
	}
	if c.Database == "" {
		return errors.New("database is required")
	}
	return nil
}

func applyEnv(c *Config) error {
	str("NEONGRID_SERVER", &c.Server)
	str("NEONGRID_NICK", &c.Nick)
	str("NEONGRID_USER", &c.User)
	str("NEONGRID_NAME", &c.Name)
	str("NEONGRID_CHANNEL", &c.Channel)
	str("NEONGRID_DATABASE", &c.Database)
	str("NEONGRID_WEB_LISTEN", &c.WebListen)
	str("NEONGRID_NICKSERV_NAME", &c.NickServ.Name)
	str("NEONGRID_NICKSERV_PASSWORD", &c.NickServ.Password)
	str("NEONGRID_NICKSERV_IDENTIFY_COMMAND", &c.NickServ.IdentifyCommand)
	if err := intEnv("NEONGRID_PORT", &c.Port); err != nil {
		return err
	}
	if err := boolEnv("NEONGRID_TLS", &c.TLS); err != nil {
		return err
	}
	if err := boolEnv("NEONGRID_TLS12_ONLY", &c.TLS12Only); err != nil {
		return err
	}
	if err := intEnv("NEONGRID_GUEST_RETENTION_DAYS", &c.GuestRetentionDays); err != nil {
		return err
	}
	if err := intEnv("NEONGRID_RECONNECT_SECONDS", &c.ReconnectSeconds); err != nil {
		return err
	}
	if err := intEnv("NEONGRID_PROGRESSION_BASE_MINUTES", &c.Progression.BaseMinutes); err != nil {
		return err
	}
	if err := intEnv("NEONGRID_PROGRESSION_LEVEL_STEP_MINUTES", &c.Progression.LevelStepMinutes); err != nil {
		return err
	}
	if err := intEnv("NEONGRID_PENALTY_SPEECH_BASE_SECONDS", &c.Penalty.SpeechBaseSeconds); err != nil {
		return err
	}
	if err := intEnv("NEONGRID_PENALTY_SPEECH_PER_LEVEL_SECONDS", &c.Penalty.SpeechPerLevelSeconds); err != nil {
		return err
	}
	if err := intEnv("NEONGRID_PENALTY_SPEECH_PER_CHARACTER_SECONDS", &c.Penalty.SpeechPerCharacterSeconds); err != nil {
		return err
	}
	if err := intEnv("NEONGRID_PENALTY_ACTION_BASE_SECONDS", &c.Penalty.ActionBaseSeconds); err != nil {
		return err
	}
	if err := intEnv("NEONGRID_PENALTY_ACTION_PER_LEVEL_SECONDS", &c.Penalty.ActionPerLevelSeconds); err != nil {
		return err
	}
	if err := intEnv("NEONGRID_PENALTY_ACTION_PER_CHARACTER_SECONDS", &c.Penalty.ActionPerCharacterSeconds); err != nil {
		return err
	}
	if err := intEnv("NEONGRID_PENALTY_NICK_SECONDS", &c.Penalty.NickSeconds); err != nil {
		return err
	}
	if err := intEnv("NEONGRID_PENALTY_PART_SECONDS", &c.Penalty.PartSeconds); err != nil {
		return err
	}
	if err := intEnv("NEONGRID_PENALTY_QUIT_SECONDS", &c.Penalty.QuitSeconds); err != nil {
		return err
	}
	if err := intEnv("NEONGRID_PENALTY_KICK_SECONDS", &c.Penalty.KickSeconds); err != nil {
		return err
	}
	if err := intEnv("NEONGRID_EVENTS_TICK_SECONDS", &c.Events.TickSeconds); err != nil {
		return err
	}
	if err := intEnv("NEONGRID_EVENTS_ENCOUNTER_MINUTES", &c.Events.EncounterMinutes); err != nil {
		return err
	}
	if err := intEnv("NEONGRID_EVENTS_CITY_EVENT_MINUTES", &c.Events.CityEventMinutes); err != nil {
		return err
	}
	if err := intEnv("NEONGRID_EVENTS_PIRATE_MINUTES", &c.Events.PirateMinutes); err != nil {
		return err
	}
	if err := intEnv("NEONGRID_EVENTS_DISTRICT_HOURS", &c.Events.DistrictHours); err != nil {
		return err
	}
	if err := intEnv("NEONGRID_EVENTS_HEAT_DECAY_MINUTES", &c.Events.HeatDecayMinutes); err != nil {
		return err
	}
	if err := intEnv("NEONGRID_EVENTS_CONTRACT_HOURS", &c.Events.ContractHours); err != nil {
		return err
	}
	if err := intEnv("NEONGRID_EVENTS_CONTRACT_PARTICIPANTS", &c.Events.ContractParticipants); err != nil {
		return err
	}
	return intEnv("NEONGRID_EVENTS_COLLISION_MINUTES", &c.Events.CollisionMinutes)
}

func str(name string, target *string) {
	if value, ok := os.LookupEnv(name); ok {
		*target = value
	}
}

func intEnv(name string, target *int) error {
	value, ok := os.LookupEnv(name)
	if !ok {
		return nil
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	*target = n
	return nil
}

func boolEnv(name string, target *bool) error {
	value, ok := os.LookupEnv(name)
	if !ok {
		return nil
	}
	b, err := strconv.ParseBool(value)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	*target = b
	return nil
}

func maxInt(value, fallback int) int {
	if value < 1 {
		return fallback
	}
	return value
}
