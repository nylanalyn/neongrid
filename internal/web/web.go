package web

import (
	"context"
	"errors"
	"html/template"
	"net/http"
	"sort"
	"strings"
	"time"

	"neongrid/internal/game"
)

type Server struct {
	engine  *game.Engine
	channel string
	tmpl    *template.Template
}

func New(engine *game.Engine, channel string) *Server {
	return &Server{
		engine:  engine,
		channel: channel,
		tmpl:    template.Must(template.New("index").Parse(indexTemplate)),
	}
}

func (s *Server) Run(ctx context.Context, address string) error {
	server := &http.Server{
		Addr:              address,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	stopped := make(chan struct{})
	defer close(stopped)
	go func() {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = server.Shutdown(shutdownCtx)
		case <-stopped:
		}
	}()
	err := server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.index)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	return mux
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	now := time.Now()
	players, world, err := s.engine.Snapshot(now)
	if err != nil {
		http.Error(w, "observer snapshot unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.Execute(w, buildPage(players, world, s.channel, s.engine.Rules(), now)); err != nil {
		return
	}
}

type pageData struct {
	Channel         string
	GeneratedAt     string
	World           string
	Active          int
	Known           int
	ActiveRunners   []runnerData
	Leaderboard     []runnerData
	Districts       []districtData
	RecentIncidents []string
}

type runnerData struct {
	Rank      int
	Nick      string
	Rep       int
	District  string
	Heat      int
	Next      string
	Rating    int
	Title     string
	Scars     string
	Artifacts string
	Online    bool
}

type districtData struct {
	Name    string
	Active  int
	Percent int
}

func buildPage(players []*game.Player, world game.WorldState, channel string, rules game.Rules, now time.Time) pageData {
	sort.Slice(players, func(i, j int) bool {
		if players[i].Level != players[j].Level {
			return players[i].Level > players[j].Level
		}
		if players[i].ProgressSeconds != players[j].ProgressSeconds {
			return players[i].ProgressSeconds > players[j].ProgressSeconds
		}
		return strings.ToLower(players[i].Nick) < strings.ToLower(players[j].Nick)
	})

	data := pageData{
		Channel:     channel,
		GeneratedAt: now.UTC().Format(time.RFC3339),
		World:       worldSummary(world, now),
		Known:       len(players),
	}
	for rank, p := range players {
		view := runnerDataFor(p, rules)
		view.Rank = rank + 1
		data.Leaderboard = append(data.Leaderboard, view)
		if p.Connected {
			data.Active++
			data.ActiveRunners = append(data.ActiveRunners, view)
		}
	}
	if len(data.Leaderboard) > 20 {
		data.Leaderboard = data.Leaderboard[:20]
	}
	sort.Slice(data.ActiveRunners, func(i, j int) bool {
		return strings.ToLower(data.ActiveRunners[i].Nick) < strings.ToLower(data.ActiveRunners[j].Nick)
	})

	counts := make(map[string]int)
	for _, p := range players {
		if p.Connected {
			counts[p.District]++
		}
	}
	maxActive := 1
	for _, count := range counts {
		if count > maxActive {
			maxActive = count
		}
	}
	for _, district := range game.Districts() {
		count := counts[district]
		data.Districts = append(data.Districts, districtData{Name: district, Active: count, Percent: count * 100 / maxActive})
	}
	for i := len(world.RecentEvents) - 1; i >= 0 && len(data.RecentIncidents) < 8; i-- {
		data.RecentIncidents = append(data.RecentIncidents, world.RecentEvents[i])
	}
	return data
}

func runnerDataFor(p *game.Player, rules game.Rules) runnerData {
	title := p.CurrentTitle()
	if title == "" {
		title = "unranked"
	}
	scars := strings.Join(p.Scars, ", ")
	if scars == "" {
		scars = "none"
	}
	var artifacts []string
	for _, item := range p.Equipment {
		if item.Unique {
			artifacts = append(artifacts, item.Name)
		}
	}
	sort.Strings(artifacts)
	if len(artifacts) == 0 {
		artifacts = []string{"none"}
	}
	return runnerData{
		Nick:      p.Nick,
		Rep:       p.Level,
		District:  p.District,
		Heat:      p.Heat,
		Next:      durationText(p.NextLevelIn(rules)),
		Rating:    p.EquipmentRating(),
		Title:     title,
		Scars:     scars,
		Artifacts: strings.Join(artifacts, ", "),
		Online:    p.Connected,
	}
}

func worldSummary(world game.WorldState, now time.Time) string {
	parts := make([]string, 0, 2)
	if world.PirateUntil.After(now) {
		parts = append(parts, "PIRATE FREQUENCY active · chat safe for "+durationText(world.PirateUntil.Sub(now)))
	}
	if world.Contract != nil {
		status := "CONTRACT active · " + world.Contract.Title + " in " + world.Contract.District
		if world.Contract.Failed {
			status = "CONTRACT compromised · " + world.Contract.Title
		}
		if world.Contract.EndsAt.After(now) {
			status += " · " + durationText(world.Contract.EndsAt.Sub(now)) + " remaining"
		}
		parts = append(parts, status)
	}
	if world.FactionSwapUntil.After(now) {
		parts = append(parts, "SYSTEM CRASH active · one faction respec available for "+durationText(world.FactionSwapUntil.Sub(now)))
	}
	if len(parts) > 0 {
		return strings.Join(parts, " | ")
	}
	if world.NextCityEventAt.IsZero() {
		return "Grid quiet · waiting for the next city event"
	}
	status := "City event window in " + durationText(world.NextCityEventAt.Sub(now))
	if !world.NextFactionSwapAt.IsZero() {
		status += " · next system crash in " + durationText(world.NextFactionSwapAt.Sub(now))
	}
	return status
}

func durationText(duration time.Duration) string {
	if duration < 0 {
		duration = 0
	}
	return duration.Round(time.Second).String()
}

const indexTemplate = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="refresh" content="30">
<title>NeonGrid // Observer</title>
<style>
:root { color-scheme: dark; --bg: #080b12; --panel: #101722; --line: #273447; --ink: #e8f1ff; --muted: #8291a8; --cyan: #62e9ff; --pink: #ff6fb5; }
* { box-sizing: border-box; }
body { margin: 0; background: radial-gradient(circle at top right, #172b3f, var(--bg) 45%); color: var(--ink); font: 15px/1.5 system-ui, sans-serif; }
main, header { max-width: 1180px; margin: auto; padding: 28px 20px; }
header { padding-bottom: 8px; }
.eyebrow, .label { color: var(--cyan); letter-spacing: .14em; font-size: 12px; text-transform: uppercase; }
h1 { margin: 4px 0; font-size: clamp(32px, 6vw, 64px); line-height: 1; }
h2 { margin: 0 0 14px; font-size: 20px; }
p { color: var(--muted); }
.hero, .panel { background: color-mix(in srgb, var(--panel) 92%, transparent); border: 1px solid var(--line); border-radius: 14px; }
.hero { display: flex; gap: 28px; align-items: center; justify-content: space-between; padding: 22px; margin: 14px 0 20px; }
.hero h2 { margin: 4px 0 0; color: var(--pink); }
.stat { min-width: 100px; text-align: right; }
.stat strong { display: block; color: var(--cyan); font-size: 30px; line-height: 1; }
.stat span, small, .muted { color: var(--muted); }
.layout { display: grid; grid-template-columns: 1.4fr 1fr; gap: 20px; }
.panel { padding: 20px; }
.wide { grid-column: 1 / -1; }
table { width: 100%; border-collapse: collapse; }
th { color: var(--muted); font-size: 12px; font-weight: 500; text-align: left; text-transform: uppercase; }
th, td { padding: 10px 8px; border-bottom: 1px solid var(--line); vertical-align: top; }
td strong, td small { display: block; }
td strong { color: var(--ink); }
.heat { color: var(--pink); }
.zones { display: grid; gap: 12px; list-style: none; padding: 0; margin: 0; }
.zones li { display: grid; grid-template-columns: 1fr 42px; gap: 10px; align-items: center; }
.bar { display: block; height: 5px; grid-column: 1 / -1; background: #1c2735; border-radius: 9px; overflow: hidden; }
.bar i { display: block; height: 100%; background: linear-gradient(90deg, var(--cyan), var(--pink)); }
.incident { margin: 0; padding: 9px 0; border-bottom: 1px solid var(--line); color: var(--muted); }
footer { max-width: 1180px; margin: auto; padding: 0 20px 35px; color: var(--muted); font-size: 12px; }
@media (max-width: 760px) { .hero, .layout { display: block; } .stat { display: inline-block; margin: 18px 22px 0 0; text-align: left; } .panel { margin-bottom: 20px; overflow-x: auto; } table { min-width: 600px; } }
</style>
</head>
<body>
<header>
<div class="eyebrow">NeonGrid // public observer</div>
<h1>The Grid is listening.</h1>
<p>{{.Channel}} · snapshot {{.GeneratedAt}} · refreshes every 30 seconds</p>
</header>
<main>
<section class="hero">
<div><div class="label">World state</div><h2>{{.World}}</h2></div>
<div class="stat"><strong>{{.Active}}</strong><span>active runners</span></div>
<div class="stat"><strong>{{.Known}}</strong><span>known runners</span></div>
</section>
<div class="layout">
<section class="panel">
<h2>Live runners</h2>
{{if .ActiveRunners}}
<table><thead><tr><th>Runner</th><th>District</th><th>Rep</th><th>Heat</th></tr></thead><tbody>
{{range .ActiveRunners}}<tr><td><strong>{{.Nick}}</strong><small>{{.Title}} · scars: {{.Scars}}</small>{{if ne .Artifacts "none"}}<small>artifact: {{.Artifacts}}</small>{{end}}</td><td>{{.District}}</td><td>{{.Rep}} <small>next {{.Next}}</small></td><td class="heat">{{.Heat}}/100</td></tr>{{end}}
</tbody></table>
{{else}}<p>No runners are currently linked to the Grid.</p>{{end}}
</section>
<section class="panel">
<h2>District map</h2>
<ul class="zones">{{range .Districts}}<li><span>{{.Name}}</span><b>{{.Active}}</b><span class="bar"><i style="width: {{.Percent}}%;"></i></span></li>{{end}}</ul>
</section>
<section class="panel wide">
<h2>Rep rankings</h2>
{{if .Leaderboard}}<table><thead><tr><th>#</th><th>Runner</th><th>Rep</th><th>District</th><th>Rating</th></tr></thead><tbody>
{{range .Leaderboard}}<tr><td>{{.Rank}}</td><td><strong>{{.Nick}}</strong><small>{{.Title}}{{if not .Online}} · offline{{end}}</small></td><td>{{.Rep}}</td><td>{{.District}}</td><td>{{.Rating}}</td></tr>{{end}}
</tbody></table>{{else}}<p>No runner records yet.</p>{{end}}
</section>
<section class="panel wide">
<h2>Recent incidents</h2>
{{if .RecentIncidents}}{{range .RecentIncidents}}<p class="incident">{{.}}</p>{{end}}{{else}}<p>No incidents logged yet.</p>{{end}}
</section>
</div>
</main>
<footer>Read-only observer. The game remains in IRC.</footer>
</body>
</html>`
