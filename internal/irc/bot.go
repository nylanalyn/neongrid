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
	limits *limiter
	mu     sync.RWMutex
	client *girc.Client
	// lastJoinAttempt throttles channel rejoins after a kick, part, or failed join.
	lastJoinAttempt time.Time
}

const (
	rejoinInterval = time.Minute
	// rosterGrace is how long a connected runner may be missing from the
	// channel roster before Maintain drops it.
	rosterGrace = time.Minute
)

func New(cfg config.Config, engine *game.Engine, logger *log.Logger) *Bot {
	if logger == nil {
		logger = log.Default()
	}
	return &Bot{cfg: cfg, game: engine, log: logger, limits: newLimiter()}
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
			if b.cfg.TLS && b.cfg.TLSLegacyFallback && !established && !legacyTLS && legacyTLSFailure(err) {
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

func (b *Bot) Ready() bool {
	b.mu.RLock()
	client := b.client
	b.mu.RUnlock()
	return client != nil && client.IsConnected() && client.IsInChannel(b.cfg.Channel)
}

// Maintain runs once per scheduler tick. It rejoins the game channel after a
// kick, part, or failed join, and drops runners that are no longer in the
// channel roster. It reports whether the bot is in the channel.
func (b *Bot) Maintain(now time.Time) bool {
	b.mu.Lock()
	client := b.client
	if client == nil || !client.IsConnected() {
		b.mu.Unlock()
		return false
	}
	if !client.IsInChannel(b.cfg.Channel) {
		if now.Sub(b.lastJoinAttempt) >= rejoinInterval {
			b.lastJoinAttempt = now
			b.log.Printf("not in %s; attempting to rejoin", b.cfg.Channel)
			client.Cmd.Join(b.cfg.Channel)
		}
		b.mu.Unlock()
		return false
	}
	b.mu.Unlock()
	channel := client.LookupChannel(b.cfg.Channel)
	if channel == nil {
		return false
	}
	roster := make(map[string]bool, len(channel.UserList))
	for _, nick := range channel.UserList {
		roster[girc.ToRFC1459(nick)] = true
	}
	present := func(nick string) bool { return roster[girc.ToRFC1459(nick)] }
	if err := b.game.Reconcile(present, rosterGrace, now); err != nil {
		b.log.Printf("reconcile roster: %v", err)
	}
	return true
}

func (b *Bot) newClient(legacyTLS bool, connected chan<- struct{}) *girc.Client {
	ircConfig := girc.Config{
		Server: b.cfg.Server, Port: b.cfg.Port, SSL: b.cfg.TLS,
		Nick: b.cfg.Nick, User: b.cfg.User, Name: b.cfg.Name,
		RecoverFunc: girc.DefaultRecoverHandler,
		SASL:        b.saslMech(),
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

// saslMech authenticates with SASL PLAIN when a password is configured. girc
// only attempts it when the server advertises sasl, and closes the connection
// if authentication fails.
func (b *Bot) saslMech() girc.SASLMech {
	if b.cfg.NickServ.Password == "" || !b.cfg.NickServ.SASL {
		return nil
	}
	account := b.cfg.NickServ.Account
	if account == "" {
		account = b.cfg.Nick
	}
	return &girc.SASLPlain{User: account, Pass: b.cfg.NickServ.Password}
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
		if b.cfg.NickServ.Password != "" && !(b.cfg.NickServ.SASL && c.HasCapability("sasl")) {
			// SASL already authenticated before registration when the server
			// offered it; otherwise fall back to a NickServ message.
			if b.cfg.NickServ.SASL {
				b.log.Printf("server did not offer SASL; identifying through %s", b.cfg.NickServ.Name)
			}
			command := strings.TrimSpace(b.cfg.NickServ.IdentifyCommand + " " + b.cfg.NickServ.Password)
			c.Cmd.Message(b.cfg.NickServ.Name, command)
		}
		b.mu.Lock()
		b.lastJoinAttempt = time.Now()
		b.mu.Unlock()
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
	if kind == game.ActivityChat {
		if claim, err := b.game.ClaimDeadDrop(identity, e.Source.Name, e.Params[0], message, eventTime(e)); err != nil {
			b.log.Printf("dead drop %s: %v", e.Source.Name, err)
		} else if claim != "" {
			client.Cmd.Message(e.Params[0], claim)
		}
	}
	if !freeCommand(message, kind) {
		penalty, _, err := b.game.Activity(identity, e.Source.Name, e.Params[0], kind, utf8.RuneCountInString(message), eventTime(e))
		if err == nil && penalty > 0 && b.limits.allow("penalty:"+sourceKey(e.Source), penaltyNoticeCooldown, time.Now()) {
			name := e.Source.Name
			if p, statusErr := b.game.Status(identity, e.Source.Name, eventTime(e)); statusErr == nil {
				name = p.DisplayName()
			}
			client.Cmd.Message(e.Params[0], fmt.Sprintf("[GRID] %s broadcast into the Grid. +%s to next Rep.", name, formatPenalty(penalty)))
		}
	}
	b.handleCommand(client, &e, identity, account)
}

func (b *Bot) handlePart(client *girc.Client, e girc.Event) {
	if e.Source == nil || len(e.Params) == 0 || !strings.EqualFold(e.Params[0], b.cfg.Channel) {
		return
	}
	if e.Source.ID() == client.GetID() {
		b.leftChannel("parted")
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
	kind := game.ActivityQuit
	if len(e.Params) > 0 && isNetsplit(e.Last()) {
		kind = game.ActivityNetsplit
	}
	if _, err := b.game.Disconnect(e.Source.Name, kind, eventTime(e)); err != nil && err != game.ErrRunnerNotFound {
		b.log.Printf("quit %s: %v", e.Source.Name, err)
	}
}

func (b *Bot) handleKick(client *girc.Client, e girc.Event) {
	if len(e.Params) < 2 || !strings.EqualFold(e.Params[0], b.cfg.Channel) {
		return
	}
	if strings.EqualFold(e.Params[1], client.GetNick()) {
		b.leftChannel("kicked")
		return
	}
	if _, err := b.game.Disconnect(e.Params[1], game.ActivityKick, eventTime(e)); err != nil && err != game.ErrRunnerNotFound {
		b.log.Printf("kick %s: %v", e.Params[1], err)
	}
}

// leftChannel stops the game when the bot is no longer watching the channel.
// Maintain rejoins on the next scheduler tick.
func (b *Bot) leftChannel(reason string) {
	b.log.Printf("%s from %s; pausing runners until rejoin", reason, b.cfg.Channel)
	b.mu.Lock()
	b.lastJoinAttempt = time.Time{}
	b.mu.Unlock()
	if err := b.game.DisconnectAll(time.Now()); err != nil {
		b.log.Printf("save disconnect state: %v", err)
	}
}

// isNetsplit recognises the server-generated "server.one server.two" quit
// reason. Most networks prefix user-chosen quit reasons (for example
// "Quit: ..."), so a runner cannot normally fake one.
func isNetsplit(reason string) bool {
	servers := strings.Fields(reason)
	if len(servers) != 2 || servers[0] == servers[1] {
		return false
	}
	for _, server := range servers {
		if !strings.Contains(server, ".") || strings.HasPrefix(server, ".") || strings.HasSuffix(server, ".") {
			return false
		}
		for _, r := range server {
			if !(r == '.' || r == '-' || r == '*' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
				return false
			}
		}
	}
	return true
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
		name := e.Source.Name
		if p, statusErr := b.game.Status("", e.Params[0], eventTime(e)); statusErr == nil {
			name = p.DisplayName()
		}
		client.Cmd.Message(b.cfg.Channel, fmt.Sprintf("[GRID] %s altered their network signature. +%s to next Rep.", name, formatPenalty(penalty)))
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
	case "status", "runner", "top", "gear", "world", "events", "help", "alias", "title", "stance":
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
	lines, known := commandLines[command]
	if !known || !b.allowCommand(e, command, lines) {
		return
	}
	reply := func(message string) { client.Cmd.Reply(*e, message) }
	now := eventTime(*e)
	switch command {
	case "status", "runner":
		p, err := b.game.Status(identity, e.Source.Name, now)
		if err != nil {
			reply("[GRID] no runner profile found yet.")
			return
		}
		reply(statusLine(p, b.game.Rules()))
	case "top":
		players, err := b.game.Top(5, now)
		if err != nil {
			reply("[GRID] leaderboard offline.")
			return
		}
		for i, p := range players {
			title := p.CurrentTitle()
			if title == "" {
				title = "unranked"
			}
			reply(fmt.Sprintf("[GRID] #%d %s — Rep %d | %s", i+1, p.DisplayName(), p.Level, title))
		}
	case "gear":
		p, err := b.game.Status(identity, e.Source.Name, now)
		if err != nil {
			reply("[GRID] no runner loadout found yet.")
			return
		}
		reply(gearLine(p))
	case "world":
		reply(worldLine(b.game.World(), now))
	case "events":
		events := b.game.RecentEvents(5)
		if len(events) == 0 {
			reply("[GRID] no recent events logged.")
			return
		}
		for _, message := range events {
			reply(message)
		}
	case "alias":
		if len(fields) == 1 {
			p, err := b.game.Status(identity, e.Source.Name, now)
			if err != nil {
				reply("[GRID] no runner profile found yet.")
				return
			}
			alias := p.Alias
			if alias == "" {
				alias = "(using current nick)"
			}
			reply("[GRID] netrunner name: " + alias + " | set with !alias <name> or clear with !alias clear.")
			return
		}
		if len(fields) != 2 {
			reply("[GRID] usage: !alias <name> or !alias clear.")
			return
		}
		alias := fields[1]
		if strings.EqualFold(alias, "clear") {
			alias = ""
		}
		p, err := b.game.SetAlias(identity, e.Source.Name, alias, now)
		if err != nil {
			reply("[GRID] alias unavailable: " + err.Error())
			return
		}
		reply("[GRID] netrunner name set: " + p.DisplayName())
	case "title":
		if len(fields) == 1 {
			p, err := b.game.Status(identity, e.Source.Name, now)
			if err != nil {
				reply("[GRID] no runner profile found yet.")
				return
			}
			reply(titleLine(p))
			return
		}
		p, err := b.game.SetTitle(identity, e.Source.Name, strings.Join(fields[1:], " "), now)
		if err != nil {
			reply("[GRID] title unavailable: " + err.Error())
			return
		}
		reply("[GRID] now showing: " + titleOrUnranked(p))
	case "stance":
		if len(fields) == 1 {
			p, err := b.game.Status(identity, e.Source.Name, now)
			if err != nil {
				reply("[GRID] no runner profile found yet.")
				return
			}
			reply("[GRID] stance: " + game.StanceLabel(p.Stance) + " | hot = bigger payouts and loot, worse ICE odds, more Heat; cold = the reverse. !stance hot|cold|normal")
			return
		}
		p, err := b.game.SetStance(identity, e.Source.Name, fields[1], now)
		if err != nil {
			reply("[GRID] stance unavailable: " + err.Error())
			return
		}
		reply("[GRID] stance set: " + game.StanceLabel(p.Stance))
	case "faction":
		if len(fields) < 2 {
			reply("[GRID] choose once: ghostline (safer ICE), chrome (bigger shards), or nomad (smaller ICE losses).")
			return
		}
		p, err := b.game.SetFaction(identity, e.Source.Name, fields[1], now)
		if err != nil {
			reply("[GRID] faction unavailable: " + err.Error())
			return
		}
		reply(fmt.Sprintf("[GRID] faction set: %s", p.Faction))
	case "help":
		reply(fmt.Sprintf("[GRID] NeonGrid is an idle-RPG: stay linked to gain Rep. In %s, speech, /me, nick changes, PART, QUIT, and KICK add delay to your next Rep. Other channels are clean.", b.cfg.Channel))
		reply("[GRID] Zero-penalty commands: !help !status/!runner !top !gear !world !events !alias !title !stance. Pirate frequency makes chatter safe, and hides a dead drop: first to type the clean code claims it.")
		reply("[GRID] !faction ghostline|chrome|nomad: better ICE odds, bigger shards, or softer losses. Faction wins score toward a weekly champion. A rare 24h system crash permits one respec.")
		reply("[GRID] !stance hot|cold|normal sets your risk. !alias <name> sets a public name. !title <name> picks which earned title you show. A full day linked without leaving pays a Ghost Protocol bonus.")
		reply("[GRID] Megacorp Runs recruit linked runners, and Blackwall raids test everyone linked; !world shows what's active.")
	case "pirate":
		if !b.isAdmin(account) {
			reply("[GRID] admin clearance required.")
			return
		}
		message, err := b.game.ForcePirate(now)
		if err == nil {
			client.Cmd.Message(b.cfg.Channel, message)
		}
	}
}

// commandLines is how many lines each command can reply with; multi-line
// commands get a longer cooldown so they cannot be used to flood the channel.
var commandLines = map[string]int{
	"status": 1, "runner": 1, "gear": 1, "world": 1, "alias": 1, "title": 1, "stance": 1, "faction": 1, "pirate": 1,
	"top": 5, "events": 5, "help": 5,
}

func (b *Bot) allowCommand(e *girc.Event, command string, lines int) bool {
	source := sourceKey(e.Source)
	keys := map[string]time.Duration{"cmd:" + source: commandCooldown}
	if lines > 1 {
		keys["multi:"+source] = multiLineCooldown
		// A private reply only reaches the requester, so only public replies
		// share a per-channel cooldown.
		if girc.IsValidChannel(e.Params[0]) {
			keys["target:"+strings.ToLower(e.Params[0])+":"+command] = targetCooldown
		}
	}
	return b.limits.allowAll(time.Now(), keys)
}

// sourceKey identifies a user for rate limiting by ident@host, so changing
// nick does not reset cooldowns.
func sourceKey(source *girc.Source) string {
	if source.Host == "" {
		return strings.ToLower(source.Name)
	}
	return strings.ToLower(source.Ident + "@" + source.Host)
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
	extras := ""
	if p.Stance != "" {
		extras += " | stance " + p.Stance
	}
	if p.StreakDays > 0 {
		extras += fmt.Sprintf(" | streak %dd", p.StreakDays)
	}
	return fmt.Sprintf("[GRID] %s | Rep %d | district %s | heat %d/%d | next %s | rating %d | faction %s%s | title %s | scars %s | id %s", p.DisplayName(), p.Level, p.District, p.Heat, game.MaxHeat, formatPenalty(int64(p.NextLevelIn(rules)/time.Second)), p.EquipmentRating(), faction, extras, title, scars, identity)
}

func titleOrUnranked(p *game.Player) string {
	if title := p.CurrentTitle(); title != "" {
		return title
	}
	return "unranked"
}

// titleLine lists earned titles on one line, the shown one first.
func titleLine(p *game.Player) string {
	if len(p.Titles) == 0 {
		return "[GRID] no titles earned yet. Rep, ICE wins, collisions, contracts, and artifacts all unlock them."
	}
	shown := p.CurrentTitle()
	others := make([]string, 0, len(p.Titles))
	for _, title := range p.Titles {
		if title != shown {
			others = append(others, title)
		}
	}
	line := fmt.Sprintf("[GRID] showing %s (%d earned)", shown, len(p.Titles))
	if len(others) > 0 {
		line += " | also: " + strings.Join(others, ", ")
	}
	return line + " | !title <name> to switch, !title auto for your best."
}

func gearLine(p *game.Player) string {
	slots := []string{game.SlotWeaponRig, game.SlotArmorPlating, game.SlotNeuralImplant, game.SlotDeck, game.SlotDrone}
	parts := make([]string, 0, len(slots))
	for _, slot := range slots {
		if item, ok := p.Equipment[slot]; ok {
			name := item.Name
			if item.Unique {
				if item.BreaksAt > 0 {
					name += fmt.Sprintf(" [UNIQUE, burns out at Rep %d]", item.BreaksAt)
				} else {
					name += " [UNIQUE]"
				}
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
	line := baseWorldLine(world, now)
	for _, extra := range worldExtras(world, now) {
		line += " | " + extra
	}
	return line
}

// worldExtras summarizes raids, dead drops, and the faction week.
func worldExtras(world game.WorldState, now time.Time) []string {
	var extras []string
	if world.Raid != nil {
		remaining := max(0, int64(world.Raid.ResolvesAt.Sub(now)/time.Second))
		extras = append(extras, fmt.Sprintf("BLACKWALL: %s hits %s in %s", world.Raid.ICE, world.Raid.District, formatPenalty(remaining)))
	}
	if world.DeadDrop != "" && world.PirateUntil.After(now) {
		extras = append(extras, "dead drop unclaimed")
	}
	week := world.FactionWeek
	if len(week.Scores) > 0 || week.Champion != "" {
		line := "faction week: " + game.FactionStandings(week.Scores)
		if !week.EndsAt.IsZero() {
			line += ", ends in " + formatPenalty(max(0, int64(week.EndsAt.Sub(now)/time.Second)))
		}
		if week.Champion != "" {
			line += "; reigning " + game.FactionLabel(week.Champion)
		}
		extras = append(extras, line)
	}
	return extras
}

func baseWorldLine(world game.WorldState, now time.Time) string {
	crash := factionSwapLine(world, now)
	if world.Contract != nil {
		remaining := world.Contract.EndsAt.Sub(now)
		if remaining < 0 {
			remaining = 0
		}
		line := fmt.Sprintf("[GRID] contract active: %s in %s; %s remaining.", world.Contract.Title, world.Contract.District, formatPenalty(int64(remaining/time.Second)))
		if world.PirateUntil.After(now) {
			line += " Pirate frequency active; transmissions safe."
		}
		if crash != "" {
			line += " " + crash
		}
		return line
	}
	if world.PirateUntil.After(now) {
		line := "[GRID] pirate frequency active for " + formatPenalty(int64(world.PirateUntil.Sub(now)/time.Second)) + "; transmissions safe."
		if crash != "" {
			line += " " + crash
		}
		return line
	}
	remaining := world.NextCityEventAt.Sub(now)
	if remaining < 0 {
		remaining = 0
	}
	line := "[GRID] pirate frequency dormant | next city event in " + formatPenalty(int64(remaining/time.Second))
	if crash != "" {
		line += " | " + crash
	}
	return line
}

func factionSwapLine(world game.WorldState, now time.Time) string {
	if world.FactionSwapUntil.After(now) {
		return "system crash active for " + formatPenalty(int64(world.FactionSwapUntil.Sub(now)/time.Second)) + "; one faction respec available."
	}
	if !world.NextFactionSwapAt.IsZero() {
		remaining := world.NextFactionSwapAt.Sub(now)
		if remaining < 0 {
			remaining = 0
		}
		return "next system crash in " + formatPenalty(int64(remaining/time.Second))
	}
	return ""
}

func formatPenalty(seconds int64) string {
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	return (time.Duration(seconds) * time.Second).Round(time.Second).String()
}
