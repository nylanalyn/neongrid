# NeonGrid

NeonGrid is a cyberpunk IRC idle-RPG for a dedicated, mostly silent channel. Players are runners connected to the Grid: they gain Rep while connected, receive automatic gear upgrades, survive passive ICE encounters, and suffer level-scaled time penalties for activity in the game channel. Activity in other channels is ignored.

## Requirements

- Go 1.22+
- An IRC server with a channel the bot can join
- SQLite (bundled through the Go driver)

## Build and run

```sh
go build ./cmd/neongrid
./neongrid -config config.yaml
```

The config file is optional. Without one, defaults target Libera.Chat over TLS and `#neongrid`. Every setting can also be overridden with a `NEONGRID_...` environment variable; secrets should be supplied that way.

Copy the example and set at least a unique nick and channel:

```sh
cp config.example.yaml config.yaml
NEONGRID_NICKSERV_PASSWORD='secret' ./neongrid -config config.yaml
```

The bot requests IRCv3 account identity when available (`account-tag`, `extended-join`, and `account-notify`). It falls back to `WHOIS` account responses and uses temporary `guest:<nick>` identities until a NickServ account is observed. Guest runners are retained for the configured number of days and migrate to the account key without losing progress.

For older IRC endpoints that abort modern TLS negotiation, the bot retries once with the legacy TLS 1.2 RSA/CBC suite required by those servers. Upgrading the server’s TLS configuration is preferable.

If the endpoint is known to require that legacy mode, set `tls12_only: true` (or `NEONGRID_TLS12_ONLY=true`) to use it directly and avoid the modern-TLS attempt.

## Commands

- `!status` / `!runner` — show Rep, district, Heat, next level time, gear rating, and identity
- `!top` — show the leaderboard
- `!gear` — show the current equipment loadout
- `!world` — show pirate-frequency and city-event timing
- `!events` — show the latest persisted passive event announcements
- `!faction ghostline|chrome|nomad` — permanently choose a lightweight specialization
- `!help` — show the compact command list
- `!pirate` — admin-only manual pirate-frequency window

Factions are permanent: Ghostline improves ICE odds and mitigates corporate sweeps, Chrome increases shard gains and bounty/run payouts, and Nomad reduces failed-encounter losses while softening gang wars. City events are typed effects: they can vary by district or faction, modify active runners and gear, and pull encounters forward. Pirate-frequency events remain safe-chat windows.

Successful passive ICE encounters have a small chance to recover a named artifact such as `Blackglass Deck` or `Prototype Mantis Rig`. Each artifact is unique across the Grid and is protected from ordinary level-up gear replacement.

Heat rises when runners broadcast identity changes, get disconnected, lose ICE, or attract corporate attention; it decays while connected according to `events.heat_decay_minutes`.

Megacorp Runs can recruit up to `events.contract_participants` currently connected runners for `events.contract_hours`. Every recruited runner must remain linked until the deadline; a disconnect fails the whole contract and the team takes a setback. Check `!world` for the active contract.

Connected runners may also collide automatically in passive deck hacks, dead-drop races, hunts, and drone incidents. Gear rating, faction, district, and Heat shape the outcome; both runners receive a cooldown so the channel does not become a combat log.

Normal chat in the game channel is still a transmission and receives the normal penalty. `!help`, `!status`, `!runner`, `!top`, `!gear`, `!world`, and `!events` are safe read-only commands; use another channel or a private message for other administration. Runners drift districts automatically; `events.district_hours` controls the interval, while `events.collision_minutes` controls the minimum time between passive runner collisions.

## Configuration

See [config.example.yaml](config.example.yaml). SQLite state is stored in the configured database path. Progress is timestamp-based and persisted; only time while a runner is connected counts. A restart clears stale online presence, then users resume when they rejoin.

## Tests

```sh
go test ./...
```
