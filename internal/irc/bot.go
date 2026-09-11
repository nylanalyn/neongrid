package irc

import (
	"context"
	"crypto/tls"
	"fmt"
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
	legacyTLS := false
	for {
		if ctx.Err() != nil {
			return nil
		}
		client := b.newClient(legacyTLS)
		b.mu.Lock()
		b.client = client
		b.mu.Unlock()

		done := make(chan error, 1)
		go func() { done <- client.Connect() }()
		select {
		case <-ctx.Done():
			client.Close()
			<-done
			b.clearClient(client)
			_ = b.game.DisconnectAll(time.Now())
			return nil
		case err := <-done:
			b.clearClient(client)
			if disconnectErr := b.game.DisconnectAll(time.Now()); disconnectErr != nil {
				return disconnectErr
			}
			if ctx.Err() != nil {
				return nil
			}
			if err != nil {
				b.log.Printf("IRC disconnected: %v", err)
				if b.cfg.TLS && !legacyTLS {
					// ponytail: one compatibility retry for old TLS endpoints; make the policy configurable if more legacy networks appear.
					legacyTLS = true
					b.log.Printf("retrying with legacy TLS 1.2 RSA/CBC compatibility")
				}
			}
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

func (b *Bot) newClient(legacyTLS bool) *girc.Client {
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
		ircConfig.TLSConfig = &tls.Config{ServerName: b.cfg.Server, MinVersion: tls.VersionTLS12, MaxVersion: tls.VersionTLS12}
		if legacyTLS {
			ircConfig.TLSConfig.CipherSuites = []uint16{
				tls.TLS_RSA_WITH_AES_256_CBC_SHA,
			}
		}
	}
	client := girc.New(ircConfig)
	b.register(client)
	return client
}

func (b *Bot) register(client *girc.Client) {
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

func (b *Bot) handleAccount(_ *girc.Client, e girc.Event) {
	if e.Source == nil || len(e.Params) == 0 || e.Params[0] == "*" {
		return
	}
	if _, err := b.game.Bind(e.Source.Name, e.Params[0], eventTime(e)); err != nil {
		b.log.Printf("account bind %s: %v", e.Source.Name, err)
	}
}

func (b *Bot) handleWhoisAccount(_ *girc.Client, e girc.Event) {
	if len(e.Params) < 3 {
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
	case "status", "runner", "top":
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
			client.Cmd.Message(target, fmt.Sprintf("[GRID] #%d %s — Rep %d", i+1, p.Nick, p.Level))
		}
	case "help":
		client.Cmd.Message(target, "[GRID] !status/!runner !top !help — passive progression; keep chatter out of the game channel.")
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
	for _, admin := range b.cfg.AdminAccounts {
		if strings.EqualFold(strings.TrimSpace(admin), account) {
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
	return fmt.Sprintf("[GRID] %s | Rep %d | next %s | rating %d | id %s", p.Nick, p.Level, formatPenalty(int64(p.NextLevelIn(rules)/time.Second)), p.EquipmentRating(), identity)
}

func formatPenalty(seconds int64) string {
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	return (time.Duration(seconds) * time.Second).Round(time.Second).String()
}
