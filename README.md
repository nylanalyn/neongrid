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

## Commands

- `!status` / `!runner` — show Rep, next level time, gear rating, and identity
- `!top` — show the leaderboard
- `!faction ghostline|chrome|nomad` — permanently choose a lightweight specialization
- `!help` — show the compact command list
- `!pirate` — admin-only manual pirate-frequency window

Factions are permanent: Ghostline improves ICE odds, Chrome increases successful shard gains, and Nomad reduces failed-encounter losses.

Normal chat in the game channel is still a transmission and receives the normal penalty. `!status`, `!runner`, and `!top` are safe read-only commands; use another channel or a private message for other administration.

## Configuration

See [config.example.yaml](config.example.yaml). SQLite state is stored in the configured database path. Progress is timestamp-based and persisted; only time while a runner is connected counts. A restart clears stale online presence, then users resume when they rejoin.

## Tests

```sh
go test ./...
```
