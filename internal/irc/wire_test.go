package irc

import (
	"bufio"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lrstanley/girc"

	"neongrid/internal/config"
	"neongrid/internal/game"
	"neongrid/internal/storage"
)

// fakeServer is a minimal IRC server on one end of a net.Pipe. It welcomes
// the bot, answers its JOIN with a roster, and records every line it receives.
type fakeServer struct {
	t      *testing.T
	conn   net.Conn
	mu     sync.Mutex
	writer *bufio.Writer
	lines  chan string
	roster string
}

func (s *fakeServer) send(lines ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, line := range lines {
		_, _ = s.writer.WriteString(line + "\r\n")
	}
	_ = s.writer.Flush()
}

func (s *fakeServer) serve() {
	reader := bufio.NewReader(s.conn)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			close(s.lines)
			return
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "USER "):
			s.send(":srv 001 neongrid :welcome")
		case line == "JOIN #neongrid":
			s.send(":neongrid!bot@bot.host JOIN #neongrid",
				":srv 353 neongrid = #neongrid :neongrid "+s.roster,
				":srv 366 neongrid #neongrid :End of /NAMES list.")
		}
		s.lines <- line
	}
}

// expect waits for a line the bot sent that satisfies match.
func (s *fakeServer) expect(match func(string) bool) string {
	s.t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case line, ok := <-s.lines:
			if !ok {
				s.t.Fatal("connection closed while waiting for bot output")
			}
			if match(line) {
				return line
			}
		case <-timeout:
			s.t.Fatal("timed out waiting for bot output")
		}
	}
}

// drain collects everything the bot sends during the given quiet period.
func (s *fakeServer) drain(quiet time.Duration) []string {
	var out []string
	for {
		select {
		case line := <-s.lines:
			out = append(out, line)
		case <-time.After(quiet):
			return out
		}
	}
}

func startWireBot(t *testing.T, cfg config.Config, roster string) (*Bot, *girc.Client, *fakeServer, *game.Engine) {
	t.Helper()
	store, err := storage.Open(filepath.Join(t.TempDir(), "neongrid.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	engine, err := game.New(store, cfg.Rules(), nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	b := New(cfg, engine, nil)
	client := b.newClient(false, make(chan struct{}, 1))
	client.Config.AllowFlood = true
	b.mu.Lock()
	b.client = client
	b.mu.Unlock()

	serverConn, clientConn := net.Pipe()
	server := &fakeServer{t: t, conn: serverConn, writer: bufio.NewWriter(serverConn), lines: make(chan string, 256), roster: roster}
	go server.serve()
	go func() { _ = client.MockConnect(clientConn) }()
	t.Cleanup(func() { client.Close(); serverConn.Close() })

	server.expect(func(line string) bool { return line == "JOIN #neongrid" })
	waitFor(t, func() bool { return b.Ready() })
	return b, client, server, engine
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func wireConfig() config.Config {
	cfg := config.Defaults()
	cfg.Server = "irc.example.test"
	cfg.TLS = false
	return cfg
}

func connected(engine *game.Engine, nick string) bool {
	players, _, err := engine.Snapshot(time.Now())
	if err != nil {
		return false
	}
	for _, p := range players {
		if p.Nick == nick {
			return p.Connected
		}
	}
	return false
}

func TestPrivateCommandsReplyToSender(t *testing.T) {
	_, _, server, engine := startWireBot(t, wireConfig(), "alice")
	waitFor(t, func() bool { return connected(engine, "alice") })
	server.send(":alice!a@alice.host PRIVMSG neongrid :!status")
	line := server.expect(func(line string) bool { return strings.HasPrefix(line, "PRIVMSG ") })
	if !strings.HasPrefix(line, "PRIVMSG alice :[GRID] alice | Rep 1") {
		t.Fatalf("private reply = %q, want it addressed to alice", line)
	}
}

func TestMultiLineCommandsAreRateLimited(t *testing.T) {
	_, _, server, engine := startWireBot(t, wireConfig(), "alice bob")
	waitFor(t, func() bool { return connected(engine, "alice") && connected(engine, "bob") })
	for i := 0; i < 3; i++ {
		server.send(":alice!a@alice.host PRIVMSG #neongrid :!help")
	}
	// Another user asking for the same multi-line reply in the same channel
	// is covered by the per-channel cooldown.
	server.send(":bob!b@bob.host PRIVMSG #neongrid :!help")
	// A nick change must not reset alice's cooldown.
	server.send(":alice!a@alice.host NICK alice2", ":alice2!a@alice.host PRIVMSG #neongrid :!help")
	var help int
	for _, line := range server.drain(200 * time.Millisecond) {
		if strings.HasPrefix(line, "PRIVMSG #neongrid :[GRID] NeonGrid is an idle-RPG") {
			help++
		}
	}
	if help != 1 {
		t.Fatalf("!help was answered %d times, want 1", help)
	}
}

func TestPenaltyNoticesAreRateLimitedButPenaltiesApply(t *testing.T) {
	_, _, server, engine := startWireBot(t, wireConfig(), "alice")
	waitFor(t, func() bool { return connected(engine, "alice") })
	for i := 0; i < 4; i++ {
		server.send(":alice!a@alice.host PRIVMSG #neongrid :hello")
	}
	var notices int
	for _, line := range server.drain(200 * time.Millisecond) {
		if strings.Contains(line, "broadcast into the Grid") {
			notices++
		}
	}
	p, err := engine.Status("", "alice", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if notices != 1 || p.ProgressSeconds > -4*30 {
		t.Fatalf("notices=%d progress=%d; want 1 notice and all four penalties", notices, p.ProgressSeconds)
	}
}

func TestBotRejoinsAfterKick(t *testing.T) {
	b, _, server, engine := startWireBot(t, wireConfig(), "alice")
	waitFor(t, func() bool { return connected(engine, "alice") })
	server.send(":op!o@op.host KICK #neongrid neongrid :out")
	waitFor(t, func() bool { return !b.Ready() })
	if connected(engine, "alice") {
		t.Fatal("runners kept accruing after the bot was kicked")
	}
	if b.Maintain(time.Now()) {
		t.Fatal("Maintain reported ready while outside the channel")
	}
	server.expect(func(line string) bool { return line == "JOIN #neongrid" })
	waitFor(t, func() bool { return b.Ready() && connected(engine, "alice") })
}

func TestNetsplitQuitIsNotPenalized(t *testing.T) {
	_, _, server, engine := startWireBot(t, wireConfig(), "alice bob")
	waitFor(t, func() bool { return connected(engine, "alice") && connected(engine, "bob") })
	server.send(":alice!a@alice.host QUIT :*.net *.split", ":bob!b@bob.host QUIT :Quit: bye")
	waitFor(t, func() bool { return !connected(engine, "alice") && !connected(engine, "bob") })
	alice, _ := engine.Status("", "alice", time.Now())
	bob, _ := engine.Status("", "bob", time.Now())
	if alice.ProgressSeconds < 0 || alice.Heat != 0 {
		t.Fatalf("netsplit was penalized: %+v", alice)
	}
	if bob.ProgressSeconds >= 0 || bob.Heat == 0 {
		t.Fatalf("ordinary quit was not penalized: %+v", bob)
	}
}

func TestIdentifyFallbackWhenServerLacksSASL(t *testing.T) {
	cfg := wireConfig()
	cfg.NickServ.Password = "hunter2"
	// startWireBot waits for JOIN, which the bot sends right after identifying.
	store, err := storage.Open(filepath.Join(t.TempDir(), "neongrid.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	engine, err := game.New(store, cfg.Rules(), nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	b := New(cfg, engine, nil)
	client := b.newClient(false, make(chan struct{}, 1))
	serverConn, clientConn := net.Pipe()
	server := &fakeServer{t: t, conn: serverConn, writer: bufio.NewWriter(serverConn), lines: make(chan string, 256)}
	go server.serve()
	go func() { _ = client.MockConnect(clientConn) }()
	defer func() { client.Close(); serverConn.Close() }()
	server.expect(func(line string) bool { return line == "PRIVMSG NickServ :IDENTIFY hunter2" })
}

func TestDeadDropClaimOverIRC(t *testing.T) {
	_, _, server, engine := startWireBot(t, wireConfig(), "alice")
	waitFor(t, func() bool { return connected(engine, "alice") })
	if _, err := engine.ForcePirate(time.Now()); err != nil {
		t.Fatal(err)
	}
	server.send(":alice!a@alice.host PRIVMSG #neongrid :" + engine.World().DeadDrop)
	line := server.expect(func(line string) bool { return strings.Contains(line, "DEAD DROP claimed") })
	if !strings.HasPrefix(line, "PRIVMSG #neongrid :[GRID] DEAD DROP claimed: alice") {
		t.Fatalf("claim announcement = %q", line)
	}
	for _, line := range server.drain(100 * time.Millisecond) {
		if strings.Contains(line, "broadcast into the Grid") {
			t.Fatalf("claiming during the pirate frequency was penalized: %q", line)
		}
	}
}
