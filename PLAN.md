# NeonGrid MVP Plan

## Outcome

Build a small Go IRC idle-RPG bot for `#neongrid`: runners progress while connected, gain equipment and passive encounters, and are punished for activity in the game channel only.

## Shape

- `cmd/neongrid`: load config, open SQLite, start the IRC bot and scheduler.
- `internal/config`: YAML config with environment-variable overrides and safe defaults.
- `internal/game`: identity, progression, penalties, equipment, encounters, city events, pirate-frequency safe windows, and commands. No IRC or SQLite knowledge.
- `internal/storage`: SQLite schema and persistence for runners plus world state.
- `internal/irc`: IRC connection, account identity discovery, guest binding, channel-only event handling, commands, reconnect loop.

## MVP rules

- Authenticated runners use the IRC account name as their stable key.
- Unauthenticated users get a temporary `guest:<nick>` runner immediately; account authentication migrates that runner without losing state.
- Progress is accumulated from timestamps only while the runner is marked connected. Startup clears stale online presence, so a restart never grants offline progress.
- Speaking, `/me`, nick changes, PART, QUIT, and KICK in the configured game channel add level-scaled delay. Other channels are ignored.
- Level-ups advance one equipment slot, and passive ticks can emit encounters and city events.
- A pirate-frequency window makes game-channel chat safe until its deadline.

## Delivery order

1. Write this plan and repository README/config example.
2. Implement game rules with focused tests for progression, penalties, migration, and pirate safety.
3. Implement SQLite persistence and a migration-safe schema.
4. Wire IRCv3 account tags/extended JOIN/ACCOUNT events, NickServ fallback, commands, and reconnects.
5. Run formatting, unit tests, and a build; fix only issues found.

## Deliberate MVP limits

- No game password, active combat commands, web UI, or external item/event data files.
- Events are lightweight announcements/effects; richer world simulation can follow real usage.

## Future problems

- Add an explicit faction respec/reset path, likely admin-controlled or tied to a future season. Faction choice remains permanent for now.
