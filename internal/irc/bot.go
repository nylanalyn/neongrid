package irc

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/lrstanley/girc"

	"neongrid/internal/config"
	"neongrid/internal/game"
)

type Bot struct {
	cfg    config.Config
	game   *game.Engine
	log    *log.Logger
	mu     sync.RWMutex
	client *girc.Client
}

func New(cfg config.Config, engine *game.Engine, logger *log.Logger) *Bot {
	if logger == nil {
		logger = log.Default()
	}
	return &Bot{cfg: cfg, game: engine, log: logger}
}

func (b *Bot) Run(ctx context.Context) error {
	legacyTLS := b.cfg.TLS12Only
	for {
		if ctx.Err() != nil {
			return nil
		}
		connected := make(chan struct{}, 1)
		client := b.newClient(legacyTLS, connected)
		b.mu.Lock()
		b.client = client
		b.mu.Unlock()

		done := make(chan error, 1)
		go func() { done <- client.Connect() }()
		established := false
		var err error
		waiting := true
		for waiting {
			select {
			case <-ctx.Done():
				client.Close()
				<-done
				b.clearClient(client)
				_ = b.game.DisconnectAll(time.Now())
				return nil
			case <-connected:
				established = true
			case err = <-done:
				select {
				case <-connected:
					established = true
				default:
				}
				waiting = false
			}
		}
		b.clearClient(client)
		if disconnectErr := b.game.DisconnectAll(time.Now()); disconnectErr != nil {
			return disconnectErr
		}
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			b.log.Printf("IRC disconnected: %v", err)
			if b.cfg.TLS && !established && !legacyTLS && legacyTLSFailure(err) {
				// ponytail: girc reports this endpoint's cipher failure as EOF; use a typed handshake signal if the library exposes one later.
				legacyTLS = true
				b.log.Printf("retrying with legacy TLS 1.2 RSA/CBC compatibility")
			}
		}
		if established && !b.cfg.TLS12Only {
			legacyTLS = false
		}

		timer := time.NewTimer(time.Duration(b.cfg.ReconnectSeconds) * time.Second)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil
		case <-timer.C:
		}
	}
}

func (b *Bot) Announce(message string) {
	b.mu.RLock()
	client := b.client
	b.mu.RUnlock()
	if client != nil {
		client.Cmd.Message(b.cfg.Channel, message)
	}
}

func (b *Bot) newClient(legacyTLS bool, connected chan<- struct{}) *girc.Client {
	ircConfig := girc.Config{
		Server: b.cfg.Server, Port: b.cfg.Port, SSL: b.cfg.TLS,
		Nick: b.cfg.Nick, User: b.cfg.User, Name: b.cfg.Name,
		RecoverFunc: girc.DefaultRecoverHandler,
		SupportedCaps: map[string][]string{
			"account-notify": nil, "account-tag": nil, "extended-join": nil,
			"message-tags": nil, "server-time": nil,
		},
	}
	if b.cfg.TLS {
		tlsConfig := &tls.Config{ServerName: b.cfg.Server, MinVersion: tls.VersionTLS12}
		if legacyTLS || b.cfg.TLS12Only {
			tlsConfig.MaxVersion = tls.VersionTLS12
			tlsConfig.CipherSuites = []uint16{
				tls.TLS_RSA_WITH_AES_256_CBC_SHA,
			}
		}
		ircConfig.TLSConfig = tlsConfig
	}
	client := girc.New(ircConfig)
	b.register(client, connected)
	return client
}

func legacyTLSFailure(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var alert tls.AlertError
	return errors.As(err, &alert)
}

func (b *Bot) register(client *girc.Client, connected chan<- struct{}) {
	client.Handlers.Add(girc.CONNECTED, func(_ *girc.Client, _ girc.Event) {
		if connected != nil {
			select {
			case connected <- struct{}{}:
			default:
			}
		}
	})
	client.Handlers.Add(girc.RPL_WELCOME, func(c *girc.Client, _ girc.Event) {
		if b.cfg.NickServ.Password != "" {
			command := strings.TrimSpace(b.cfg.NickServ.IdentifyCommand + " " + b.cfg.NickServ.Password)
			c.Cmd.Message(b.cfg.NickServ.Name, command)
		}
		c.Cmd.Join(b.cfg.Channel)
	})
	client.Handlers.Add(girc.JOIN, b.handleJoin)
	client.Handlers.Add(girc.RPL_NAMREPLY, b.handleNames)
	client.Handlers.Add("ACCOUNT", b.handleAccount)
	client.Handlers.Add(girc.RPL_WHOISACCOUNT, b.handleWhoisAccount)
	client.Handlers.Add(girc.PRIVMSG, b.handleMessage)
	client.Handlers.Add(girc.PART, b.handlePart)
	client.Handlers.Add(girc.QUIT, b.handleQuit)
	client.Handlers.Add(girc.KICK, b.handleKick)
	client.Handlers.Add(girc.NICK, b.handleNick)
	client.Handlers.Add(girc.DISCONNECTED, func(_ *girc.Client, _ girc.Event) {
		if err := b.game.DisconnectAll(time.Now()); err != nil {
			b.log.Printf("save disconnect state: %v", err)
		}
	})
}

func (b *Bot) handleJoin(client *girc.Client, e girc.Event) {
	if e.Source == nil || len(e.Params) == 0 || !strings.EqualFold(e.Params[0], b.cfg.Channel) || e.Source.ID() == client.GetID() {
		return
	}
	nick, at := e.Source.Name, eventTime(e)
	account := b.account(client, &e)
	if _, err := b.game.Join("", nick, account, at); err != nil {
		b.log.Printf("join %s: %v", nick, err)
	}
	if account == "" {
		client.Cmd.Whois(nick)
	}
}

func (b *Bot) handleNames(client *girc.Client, e girc.Event) {
	if len(e.Params) < 4 || !strings.EqualFold(e.Params[2], b.cfg.Channel) {
		return
	}
	for _, name := range strings.Fields(e.Params[3]) {
		nick := namesNick(name)
		if nick == "" || strings.EqualFold(nick, client.GetNick()) {
			continue
		}
		account := ""
		if user := client.LookupUser(nick); user != nil {
			account = user.Extras.Account
		}
		if _, err := b.game.Join("", nick, account, eventTime(e)); err != nil {
			b.log.Printf("names %s: %v", nick, err)
		}
		if account == "" {
			client.Cmd.Whois(nick)
		}
	}
}

func namesNick(value string) string {
	value = strings.TrimLeft(value, "~&@%+")
	if separator := strings.IndexByte(value, '!'); separator >= 0 {
		value = value[:separator]
	}
	return value
}

func (b *Bot) handleAccount(client *girc.Client, e girc.Event) {
	if e.Source == nil || e.Source.ID() == client.GetID() || len(e.Params) == 0 || e.Params[0] == "*" {
		return
	}
	if _, err := b.game.Bind(e.Source.Name, e.Params[0], eventTime(e)); err != nil {
		b.log.Printf("account bind %s: %v", e.Source.Name, err)
	}
}

func (b *Bot) handleWhoisAccount(client *girc.Client, e girc.Event) {
	if len(e.Params) < 3 || strings.EqualFold(e.Params[1], client.GetNick()) {
		return
	}
	if _, err := b.game.Bind(e.Params[1], e.Params[2], eventTime(e)); err != nil {
		b.log.Printf("WHOIS bind %s: %v", e.Params[1], err)
	}
}

func (b *Bot) handleMessage(client *girc.Client, e girc.Event) {
	if e.Source == nil || len(e.Params) < 2 {
		return
	}
	message := e.Last()
	kind := game.ActivityChat
	if e.IsAction() {
		kind = game.ActivityAction
		message = e.StripAction()
	}
	account := b.account(client, &e)
	identity := ""
	if account != "" {
		identity = game.AccountKey(account)
	}
	if !freeCommand(message, kind) {
		penalty, _, err := b.game.Activity(identity, e.Source.Name, e.Params[0], kind, utf8.RuneCountInString(message), eventTime(e))
		if err == nil && penalty > 0 {
			client.Cmd.Message(e.Params[0], fmt.Sprintf("[GRID] %s broadcast into the Grid. +%s to next Rep.", e.Source.Name, formatPenalty(penalty)))
		}
	}
	b.handleCommand(client, &e, identity, account)
}

func (b *Bot) handlePart(_ *girc.Client, e girc.Event) {
	if e.Source == nil || len(e.Params) == 0 || !strings.EqualFold(e.Params[0], b.cfg.Channel) {
		return
	}
	if _, err := b.game.Disconnect(e.Source.Name, game.ActivityPart, eventTime(e)); err != nil && err != game.ErrRunnerNotFound {
		b.log.Printf("part %s: %v", e.Source.Name, err)
	}
}

func (b *Bot) handleQuit(_ *girc.Client, e girc.Event) {
	if e.Source == nil {
		return
	}
	if _, err := b.game.Disconnect(e.Source.Name, game.ActivityQuit, eventTime(e)); err != nil && err != game.ErrRunnerNotFound {
		b.log.Printf("quit %s: %v", e.Source.Name, err)
	}
}

func (b *Bot) handleKick(_ *girc.Client, e girc.Event) {
	if len(e.Params) < 2 || !strings.EqualFold(e.Params[0], b.cfg.Channel) {
		return
	}
	if _, err := b.game.Disconnect(e.Params[1], game.ActivityKick, eventTime(e)); err != nil && err != game.ErrRunnerNotFound {
		b.log.Printf("kick %s: %v", e.Params[1], err)
	}
}

func (b *Bot) handleNick(client *girc.Client, e girc.Event) {
	if e.Source == nil || len(e.Params) == 0 {
		return
	}
	penalty, err := b.game.Rename(e.Source.Name, e.Params[0], eventTime(e))
	if err != nil && err != game.ErrRunnerNotFound {
		b.log.Printf("nick %s: %v", e.Source.Name, err)
	}
	if err == nil && penalty > 0 {
		client.Cmd.Message(b.cfg.Channel, fmt.Sprintf("[GRID] %s altered their network signature. +%s to next Rep.", e.Source.Name, formatPenalty(penalty)))
	}
}

func freeCommand(message string, kind game.Activity) bool {
	if kind != game.ActivityChat {
		return false
	}
	fields := strings.Fields(message)
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "!") {
		return false
	}
	switch strings.ToLower(strings.TrimPrefix(fields[0], "!")) {
	case "status", "runner", "top", "gear", "world", "events", "help":
		return true
	default:
		return false
	}
}

func (b *Bot) handleCommand(client *girc.Client, e *girc.Event, identity, account string) {
	fields := strings.Fields(e.Last())
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "!") {
		return
	}
	command := strings.ToLower(strings.TrimPrefix(fields[0], "!"))
	target := e.Params[0]
	now := eventTime(*e)
	switch command {
	case "status", "runner":
		p, err := b.game.Status(identity, e.Source.Name, now)
		if err != nil {
			client.Cmd.Message(target, "[GRID] no runner profile found yet.")
			return
		}
		client.Cmd.Message(target, statusLine(p, b.game.Rules()))
	case "top":
		players, err := b.game.Top(5, now)
		if err != nil {
			client.Cmd.Message(target, "[GRID] leaderboard offline.")
			return
		}
		for i, p := range players {
			title := p.CurrentTitle()
			if title == "" {
				title = "unranked"
			}
			client.Cmd.Message(target, fmt.Sprintf("[GRID] #%d %s — Rep %d | %s", i+1, p.Nick, p.Level, title))
		}
	case "gear":
		p, err := b.game.Status(identity, e.Source.Name, now)
		if err != nil {
			client.Cmd.Message(target, "[GRID] no runner loadout found yet.")
			return
		}
		client.Cmd.Message(target, gearLine(p))
	case "world":
		client.Cmd.Message(target, worldLine(b.game.World(), now))
	case "events":
		events := b.game.RecentEvents(5)
		if len(events) == 0 {
			client.Cmd.Message(target, "[GRID] no recent events logged.")
			return
		}
		for _, message := range events {
			client.Cmd.Message(target, message)
		}
	case "faction":
		if len(fields) < 2 {
			client.Cmd.Message(target, "[GRID] choose once: ghostline (safer ICE), chrome (bigger shards), or nomad (smaller ICE losses).")
			return
		}
		p, err := b.game.SetFaction(identity, e.Source.Name, fields[1], now)
		if err != nil {
			client.Cmd.Message(target, "[GRID] faction unavailable: "+err.Error())
			return
		}
		client.Cmd.Message(target, fmt.Sprintf("[GRID] faction locked: %s", p.Faction))
	case "help":
		client.Cmd.Message(target, fmt.Sprintf("[GRID] NeonGrid is an idle-RPG: stay linked to gain Rep. In %s, speech, /me, nick changes, PART, QUIT, and KICK add delay to your next Rep. Other channels are clean.", b.cfg.Channel))
		client.Cmd.Message(target, "[GRID] Zero-penalty commands: !help !status/!runner !top !gear !world !events. Pirate frequency can temporarily make game-channel chatter safe.")
		client.Cmd.Message(target, "[GRID] Lock in one faction with !faction <name>: ghostline = better ICE odds; chrome = bigger shard gains; nomad = softer ICE losses.")
		client.Cmd.Message(target, "[GRID] Megacorp Runs may recruit linked runners; !world shows the active contract and deadline.")
	case "pirate":
		if !b.isAdmin(account) {
			client.Cmd.Message(target, "[GRID] admin clearance required.")
			return
		}
		message, err := b.game.ForcePirate(now)
		if err == nil {
			client.Cmd.Message(b.cfg.Channel, message)
		}
	}
}

func (b *Bot) account(client *girc.Client, e *girc.Event) string {
	if account, ok := e.Tags.Get("account"); ok && account != "" && account != "*" {
		return account
	}
	if e.Command == girc.JOIN && len(e.Params) > 1 && e.Params[1] != "*" {
		return e.Params[1]
	}
	if e.Source != nil {
		if user := client.LookupUser(e.Source.Name); user != nil {
			return user.Extras.Account
		}
	}
	return ""
}

func (b *Bot) isAdmin(account string) bool {
	account = strings.TrimSpace(account)
	if account == "" {
		return false
	}
	for _, admin := range b.cfg.AdminAccounts {
		admin = strings.TrimSpace(admin)
		if admin != "" && strings.EqualFold(admin, account) {
			return true
		}
	}
	return false
}

func (b *Bot) clearClient(client *girc.Client) {
	b.mu.Lock()
	if b.client == client {
		b.client = nil
	}
	b.mu.Unlock()
}

func eventTime(e girc.Event) time.Time {
	if e.Timestamp.IsZero() {
		return time.Now()
	}
	return e.Timestamp
}

func statusLine(p *game.Player, rules game.Rules) string {
	identity := "guest"
	if !p.Guest {
		identity = p.Account
	}
	faction := p.Faction
	if faction == "" {
		faction = "unaffiliated"
	}
	title := p.CurrentTitle()
	if title == "" {
		title = "unranked"
	}
	scars := strings.Join(p.Scars, ", ")
	if scars == "" {
		scars = "none"
	}
	return fmt.Sprintf("[GRID] %s | Rep %d | district %s | heat %d/%d | next %s | rating %d | faction %s | title %s | scars %s | id %s", p.Nick, p.Level, p.District, p.Heat, game.MaxHeat, formatPenalty(int64(p.NextLevelIn(rules)/time.Second)), p.EquipmentRating(), faction, title, scars, identity)
}

func gearLine(p *game.Player) string {
	slots := []string{game.SlotWeaponRig, game.SlotArmorPlating, game.SlotNeuralImplant, game.SlotDeck, game.SlotDrone}
	parts := make([]string, 0, len(slots))
	for _, slot := range slots {
		if item, ok := p.Equipment[slot]; ok {
			name := item.Name
			if item.Unique {
				name += " [UNIQUE]"
			}
			parts = append(parts, fmt.Sprintf("%s: %s", strings.ReplaceAll(slot, "_", " "), name))
		}
	}
	if len(parts) == 0 {
		return "[GRID] loadout: unconfigured"
	}
	return "[GRID] loadout | " + strings.Join(parts, " | ")
}

func worldLine(world game.WorldState, now time.Time) string {
	if world.Contract != nil {
		remaining := world.Contract.EndsAt.Sub(now)
		if remaining < 0 {
			remaining = 0
		}
		line := fmt.Sprintf("[GRID] contract active: %s in %s; %s remaining.", world.Contract.Title, world.Contract.District, formatPenalty(int64(remaining/time.Second)))
		if world.PirateUntil.After(now) {
			line += " Pirate frequency active; transmissions safe."
		}
		return line
	}
	if world.PirateUntil.After(now) {
		return "[GRID] pirate frequency active for " + formatPenalty(int64(world.PirateUntil.Sub(now)/time.Second)) + "; transmissions safe."
	}
	remaining := world.NextCityEventAt.Sub(now)
	if remaining < 0 {
		remaining = 0
	}
	return "[GRID] pirate frequency dormant | next city event in " + formatPenalty(int64(remaining/time.Second))
}

func formatPenalty(seconds int64) string {
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	return (time.Duration(seconds) * time.Second).Round(time.Second).String()
}
