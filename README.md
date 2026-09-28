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

The example also enables a local read-only observer at [http://127.0.0.1:8080](http://127.0.0.1:8080). Set `web_listen: ''` (or `NEONGRID_WEB_LISTEN=''`) to disable it, or bind it to another address only when the page should be reachable beyond the local machine. It exposes no account controls; IRC remains the game.

For Uptime Kuma, monitor `GET http://127.0.0.1:8080/healthz` from Vesper (adjust the port to match `web_listen`). It returns HTTP 200 after the bot joins its IRC channel and HTTP 503 while disconnected or unable to join. If Kuma runs in a container, it needs access to the host's loopback address.

The bot requests IRCv3 account identity when available (`account-tag`, `extended-join`, and `account-notify`). It falls back to `WHOIS` account responses and uses temporary `guest:<nick>` identities until a NickServ account is observed. Guest runners are retained for the configured number of days and migrate to the account key without losing progress.

When `nickserv.password` is set, the bot authenticates with SASL PLAIN during connection registration, so it is identified before it joins the channel. `nickserv.account` sets the account name (default: the nick). If the server does not offer SASL, the bot falls back to messaging `IDENTIFY` to NickServ; set `nickserv.sasl: false` to always use that. A rejected SASL login closes the connection, and the bot keeps retrying on the reconnect interval, so check the logs if it never joins.

Some older IRC endpoints only speak the legacy TLS 1.2 RSA/CBC suite. If yours is one of them, set `tls12_only: true` (or `NEONGRID_TLS12_ONLY=true`). `tls_legacy_fallback: true` instead retries with that suite after a failed modern handshake. It is off by default because an attacker on the network path can force the fallback, which gives up forward secrecy. Upgrading the server’s TLS configuration is preferable.

## Commands

- `!status` / `!runner` — show Rep, district, Heat, next level time, gear rating, and identity
- `!top` — show the leaderboard
- `!gear` — show the current equipment loadout
- `!world` — show pirate-frequency and city-event timing
- `!events` — show the latest persisted passive event announcements
- `!alias <name>` / `!alias clear` — set or clear the stable public netrunner name
- `!title` / `!title <name>` / `!title auto` — list earned titles, pick the one you show, or go back to your most prestigious
- `!stance` / `!stance hot|cold|normal` — show or set how hard you push against ICE
- `!faction ghostline|chrome|nomad` — choose a lightweight specialization; rare system crashes allow one respec
- `!help` — show the compact command list
- `!pirate` — admin-only manual pirate-frequency window

Commands work in the game channel or by private message; replies go back where the command was sent. Each user can run one command every few seconds. `!help`, `!top`, and `!events` reply with several lines, so each user can run them once a minute, and the same one is answered in the channel at most every 30 seconds. Extra requests are ignored silently. Chat in the game channel is always penalized, but the bot reports it at most once every 20 seconds per user.

Factions are normally permanent: Ghostline improves ICE odds and mitigates corporate sweeps, Chrome increases shard gains and bounty/run payouts, and Nomad reduces failed-encounter losses while softening gang wars. Roughly once a week, a persisted 24-hour `SYSTEM CRASH` opens one faction respec per runner; the replacement faction remains permanent when the window closes. City events are typed effects: they can vary by district or faction, modify active runners and gear, and pull encounters forward. Pirate-frequency events remain safe-chat windows.

ICE is built for each runner's level. The odds depend on gear compared with a standard loadout at that level: on-level gear wins about 65% of the time. Ghostline, the Corporate Arcology and Old Transit districts, and Ghost Signal improve the odds; the Ghost Quarter, Burned Optic, and Heat lower them. Odds stay between 10% and 95%.

Successful passive ICE encounters have a small chance to recover a named artifact such as `Blackglass Deck` or `Prototype Mantis Rig`. Only one of each artifact exists on the Grid at a time. An artifact rates 2–4 tiers above standard gear at the level it drops and is protected from level-up replacement. It burns out 4–8 levels later: it returns to the drop pool, the slot gets standard gear for the runner's current level, and the runner rolls the drop table again, which can turn up a different artifact. `!gear` shows when each artifact burns out. If a runner stays offline for longer than `events.artifact_offline_days` (default 7), their artifacts return to the drop pool and those slots get standard gear for their level, so the few artifacts keep circulating.

Heat rises when runners broadcast identity changes, get disconnected, lose ICE, or attract corporate attention; it decays while connected according to `events.heat_decay_minutes`.

Megacorp Runs can recruit up to `events.contract_participants` currently connected runners for `events.contract_hours`. Every recruited runner must remain linked until the deadline; a disconnect fails the whole contract and the team takes a setback. Netsplits and the bot's own outages do not fail a contract, but any participant still missing at the deadline does. Check `!world` for the active contract.

Connected runners may also collide automatically in passive deck hacks, dead-drop races, hunts, and drone incidents. Gear rating, faction, district, and Heat shape the outcome; both runners receive a cooldown so the channel does not become a combat log.

Rare incidents can leave persistent cyberware scars such as `Ghost Signal`, `Burned Optic`, or `Synthetic Adrenal Gland`; some help and some hurt. Titles are awarded for milestones in Rep, Heat, district, ICE wins, collision wins, completed contracts, artifacts found or stolen, dead drops, Blackwall raids, rivalries, and link streaks, and each new title is announced. A runner collects every title they earn but shows only one in `!status`, `!top`, and the observer: their most prestigious, or the one they pick with `!title <name>`.

Normal chat in the game channel is still a transmission and receives the normal penalty. `!help`, `!status`, `!runner`, `!top`, `!gear`, `!world`, and `!events` are safe read-only commands; `!alias` and `!title` are zero-penalty profile settings. Use another channel or a private message for other administration. Runners drift districts automatically; `events.district_hours` controls the interval, while `events.collision_minutes` controls the minimum time between passive runner collisions.

Runners can set a stable public netrunner name with `!alias chicken-licker`. Announcements, leaderboards, status, contracts, and the web observer use the alias while the underlying IRC account identity remains unchanged. Aliases are limited to 24 printable ASCII characters without spaces, and cannot match another runner's alias or nick (case-insensitive); `!alias clear` returns to the current nick. A guest's alias carries over when the guest links an account.

## Living on the Grid

- **Stance.** `!stance hot` makes ICE payouts 1.5x larger, doubles rare-loot odds, lowers ICE odds by 10%, and makes ICE Heat build 1.5x as fast. `!stance cold` does the reverse: 0.6x payouts, half the loot odds, +10% odds, half the Heat.
- **Collisions** pick an opponent in the same district when there is one. Five or more bouts against the same runner, when each is the other's most frequent opponent, turn the collision into a numbered **rivalry** round. A collision winner has a 20% chance to steal one of the loser's artifacts, along with the levels it has left before burning out.
- **Faction week.** ICE wins (1), collision wins (2), completed contracts (3 each), dead drops (1), and Blackwall wins (1) score for your faction. After seven days the top faction is crowned and gets +5% ICE odds for the next week. A tie crowns nobody. `!world` shows the standings.
- **Dead drops.** Each pirate frequency hides a five-character code in the static, for example `K#7%Q&X@M`. The first runner to strip the noise and type the clean code (`K7QXM`) in the channel claims a data shard. Chat is safe during the window, so guessing costs nothing.
- **Blackwall raids.** Occasionally a named ICE surfaces with 30 minutes' warning, if at least two runners are linked. Everyone linked when it resolves is tested together: combined Rep and gear against a wall built for their levels. The more runners linked, the better the team's odds. Everyone shares the reward or the setback.
- **Ghost Protocol.** Each full day linked without quitting pays a bonus. A netsplit or bot outage doesn't break the streak if the runner is back within an hour. `!status` shows the current streak.
- **Daily bulletin.** Once a day the bot posts one line with the top climber, the runner with the most Heat, the most collision wins, and the faction standings.
- **Contracts** that lose a runner fail on the next tick and name who dropped, instead of waiting for the deadline.

When a runner rejoins, any encounter, district drift, or collision that came due while they were offline is rescheduled to a random point later in its interval, so a mass rejoin doesn't fire every runner's events at once.

QUITs caused by netsplits (the server-generated `server.one server.two` reason) carry no penalty or Heat. If the bot is kicked or leaves the channel, every runner is paused and the bot tries to rejoin once a minute. The world clock (events, encounters, contracts) only advances while the bot is in the channel. Each tick the bot also checks the channel's user list. A runner missing from it for more than a minute (for example, a missed QUIT) is disconnected without a penalty.

## Configuration

See [config.example.yaml](config.example.yaml). SQLite state is stored in the configured database path using WAL mode, so a `-wal` and `-shm` file sit next to it; back up all three together, or use `sqlite3 neongrid.db .backup`. Progress is timestamp-based and persisted; only time while a runner is connected counts. A restart clears stale online presence, then users resume when they rejoin. `NEONGRID_ADMIN_ACCOUNTS` accepts a comma-separated account list. Negative penalty and event values are rejected at startup.

## Tests

```sh
go test ./...
```
