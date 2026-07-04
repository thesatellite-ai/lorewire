# Plan — config file + identity/session model

Working doc for the "stop exporting env vars every terminal" feature. Captures every decision we agreed on, the schema, the commands, and the build phases.

## Goal

A project-local config file supplies name/room/role so a terminal is auto-configured; env vars and flags still override. Identity is split into a stable, unique **user** (userId + username) that can own **many sessions** (one per terminal).

## Decisions (locked)

| # | Decision | Choice |
|---|---|---|
| Format | Config file format | **JSONC** (`.lorewire.jsonc`), comments allowed; parsed by stripping `//` and `/* */` then std `encoding/json`. No new dependency. |
| File | Committed or gitignored | **Committed**, userId included. Config is the *default*; env overrides per-person. (Multi-human-same-DB is the only break, and it's not a real use case.) |
| Local/global split | Two files? | **Dropped.** Single `.lorewire.jsonc`. |
| Identity | Model | **user** = `userId` (stable) + `username` (human), both UNIQUE, one row in `users`. A user owns many **sessions**. |
| userId | Format | `usr_` prefix + **7-char nanoid**, body alphabet lowercase `a–z` + `0–9` (underscore only as the `usr_` separator) → e.g. `usr_k3n9x2p`. |
| Re-import | Portability | `lorewire user create <name> --id <existing>` re-establishes a userId in a fresh DB (new machine). |
| Session | Handle | Auto per terminal: `username` + `~` + short hash of the **tty** → `bob~a1f`. Override via `$LOREWIRE_SESSION`; `register --new` force-rotates. |
| Membership | Per-session vs per-user | **Per-session** — one `members` row per terminal (smallest change; delivery is per-terminal). |
| members.owner_id | Store userId in members? | **Yes** — safe redundancy (owner never changes), lets us group by user without a join. |
| Session context | Extra fields | Store `cwd`, `tty`, `pid`, `host`, `client`, `created_at`, `last_seen`, plus a `meta` JSON blob for future fields (no migration to add one). |
| username storage | Denormalize name into sessions/members? | **No** — keep it only in `users` (single source of truth), resolve at read time so a rename touches one row. |
| Addressing | `--to <username>` | Fans out to **all** that user's sessions in the room. `--to bob~a1f` targets one. `@role` / `all` unchanged. |
| Quick mode | No config + `$LOREWIRE_NAME` | Lazily ensure a user for that name (auto-create if free), so the 2-terminal demo needs zero setup. |
| Resolution | Precedence | `--flag` > `$LOREWIRE_*` env > `.lorewire.jsonc` (nearest, walking up from CWD) > built-in default. |

## Schema (target)

```sql
CREATE TABLE users (
  user_id    TEXT PRIMARY KEY,      -- usr_<nanoid7>
  username   TEXT NOT NULL UNIQUE,
  created_at TEXT NOT NULL
);

CREATE TABLE rooms (
  name       TEXT PRIMARY KEY,
  owner_id   TEXT NOT NULL,         -- user_id, '' for system default room
  created_at TEXT NOT NULL
);

CREATE TABLE sessions (
  session_id TEXT PRIMARY KEY,      -- username~<ttyhash>
  owner_id   TEXT NOT NULL,         -- -> users.user_id
  cwd        TEXT, tty TEXT, pid INTEGER, host TEXT, client TEXT,
  meta       TEXT,                  -- JSON blob, extensible
  created_at TEXT NOT NULL,
  last_seen  TEXT NOT NULL
);

CREATE TABLE members (
  room       TEXT NOT NULL,
  session_id TEXT NOT NULL,         -- -> sessions.session_id
  owner_id   TEXT NOT NULL,         -- -> users.user_id (denormalized, safe)
  role       TEXT NOT NULL,
  joined_at  TEXT NOT NULL,
  PRIMARY KEY (room, session_id)
);

CREATE TABLE messages (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  room       TEXT NOT NULL DEFAULT 'main',
  from_id    TEXT NOT NULL,         -- session_id of sender
  to_id      TEXT NOT NULL,         -- session_id of recipient (fan-out already resolved)
  kind       TEXT NOT NULL DEFAULT 'msg',
  body       TEXT NOT NULL,
  ref_id     INTEGER,
  created_at TEXT NOT NULL,
  read_at    TEXT
);
```

Migration: existing DBs use the old flat schema (`sessions.name`, `members.name`, `messages.from_name/to_name`, no `users`). A one-time migration creates `users` (one per distinct old name, minting a userId), rewrites sessions/members/messages to the session-id shape (old bare `name` becomes both the username's user and a `name`-keyed session), and preserves messages. Guarded by a schema check so it runs once.

## Config resolution

`.lorewire.jsonc` (nearest, walking up from CWD):
```jsonc
{
  "userId": "usr_k3n9x2p",  // claimed via `lorewire user create`
  "room": "project-x",
  "role": "cto"
}
```
Effective value per key = first hit of: flag, then `LOREWIRE_USER_ID`/`LOREWIRE_NAME`/`LOREWIRE_ROOM`/`LOREWIRE_ROLE`, then config, then default (`room=main`, `role=guest`).

## Commands

| Command | Behavior |
|---|---|
| `lorewire user create NAME [--id usr_…]` | Claim a username (fail if taken), mint/import userId, auto-write `./.lorewire.jsonc`. |
| `lorewire init --username NAME` / `--user usr_…` | Point this dir's jsonc at an existing identity. |
| `lorewire user list` | List users + session counts. |
| `lorewire user rename OLD NEW` | Rename a username (userId unchanged). |
| `lorewire whoami` | Effective userId, username, session handle, room, role — and the source of each. |
| `lorewire register [--new]` | Ensure the resolved user exists, create/refresh this terminal's session, auto-join the configured room with the configured role. `--new` rotates the session handle. |
| `lorewire sessions` | Group by owner (`bob (2 sessions)` …) with cwd/tty/client/last-seen. |
| existing: `join`/`leave`/`send`/`recv`/`inbox`/`watch`/`members`/`rooms`/`role`/`prune`/`request`/`grant`/`deny` | Updated to the session-id model; `--to` accepts username (fan-out) or session id. |

## Build phases — STATUS

- [x] 1. `users` table + nanoid userId + backup-on-incompatible migration (`store.go`).
- [x] 2. Session-id derivation (tty via stdin Rdev) + rich session columns + `meta` (`config.go`, `store.go`).
- [x] 3. JSONC parser + walk-up config loader + resolver precedence (`config.go`, `main.go`).
- [x] 4. `user create/list/rename` (+ `--id` import) + `init` + `whoami` (`main.go`).
- [x] 5. Rewired `register` (identity + session + auto-join) and quick mode.
- [x] 6. Rewired messaging to session ids + `--to username` fan-out; rename cascades to session ids.
- [x] 7. Session-aware hooks, README, and `scripts/e2e-identity.sh` (10 assertions) + Taskfile `test-identity`.

All three e2e suites pass (`task test`): flat, rooms, identity. `go vet` clean. Not committed (awaiting review).

## Notable fixes found during build

- **Positional-before-flags:** `user create NAME --room …` — Go's `flag.Parse` stops at the leading positional, so the name is taken off before parsing flags (same pattern as `grant`/`deny`).
- **Identity precedence:** a config `userId` must NOT beat an explicit `$LOREWIRE_NAME`; resolution is ordered flag→env→config across *both* userId and username.
- **Rename cascade:** `session_id` embeds the username for readability, so `user rename` rewrites the id prefix across sessions/members/messages (userId — what configs reference — is untouched, so committed `.lorewire.jsonc` files never break).
