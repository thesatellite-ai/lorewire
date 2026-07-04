# Architecture

How lorewire is built and *why* — the design decisions, the data model, the concurrency story, and the trade-offs. If you're maintaining lorewire (human or agent), read this first so no context is lost.

## Contents

- [Shape of the thing](#shape-of-the-thing)
- [Why these choices](#why-these-choices)
- [Source layout](#source-layout)
- [Data model (schema)](#data-model-schema)
- [How a message flows](#how-a-message-flows)
- [Identity & sessions](#identity--sessions)
- [Concurrency](#concurrency)
- [Rooms, roles & membership](#rooms-roles--membership)
- [Secrets](#secrets)
- [Config resolution](#config-resolution)
- [Migrations & the legacy backup](#migrations--the-legacy-backup)
- [Design principles we follow](#design-principles-we-follow)
- [Deliberate non-goals](#deliberate-non-goals)

## Shape of the thing

lorewire is a **single Go binary** and a **single local SQLite file**. There is no server, no daemon, no network. Separate processes — terminals, AI agents, scripts — coordinate by each running the `lorewire` CLI, which reads and writes the shared database at `~/.lorewire/lorewire.db`.

```
 alice's terminal            bob's Claude agent           a script
      │  lorewire send            │  lorewire recv             │  lorewire send
      ▼                           ▼                            ▼
 ┌──────────────────────── ~/.lorewire/lorewire.db (SQLite, WAL) ───────────────────────┐
 │  users · rooms · sessions · members · messages                                        │
 └───────────────────────────────────────────────────────────────────────────────────── ┘
```

The reason this works: an AI agent can only act through tools — mostly a shell. So the mechanism has to be a plain command it can invoke (`lorewire …`) plus a shared store. A CLI + one SQLite file is the smallest thing that satisfies that.

## Why these choices

| Choice | Why |
|---|---|
| **CLI + shared SQLite, no server** | An agent invokes shell commands; a command + file needs no lifecycle, ports, or auth to run. Nothing to start or supervise. |
| **Pure-Go SQLite (`modernc.org/sqlite`)** | No CGO → a single static binary, trivial to `go install` and distribute. |
| **SQLite (not files/JSON)** | Atomic writes and real queries. A directory of message files races and can't answer "unread rows for me" cleanly. |
| **WAL + busy_timeout + retry + `synchronous=NORMAL`** | Many short-lived processes write concurrently; these make that safe and fast without a lock server. |
| **Pull-based delivery** | The model reads its inbox when it acts. Simple, no push infrastructure. A hook layers "push into context" on top. |
| **Local, cooperative trust** | All sessions are one user's machine/account; roles are coordination labels, not enforced ACLs. Keeps it a weekend-simple tool. |

## Source layout

Small, flat, one responsibility per file (package `main`).

| File | Owns |
|---|---|
| `main.go` | CLI dispatch, every `cmd*` handler, identity/room/role resolution (`resolveIdentity`, `ctx`), and terminal-context capture (`captureContext`). |
| `store.go` | The `Store` type: schema, migrations, and every DB operation (users, sessions, rooms, members, messages). |
| `config.go` | Environment/system probes: nanoid, the JSONC parser, `.lorewire.jsonc` discovery, the session-token derivation (`terminalToken`/`terminalTokenSourced`), git/os/tty probes. |
| `constants.go` | Every closed-set value (delimiters, id prefix, env-var names, addressing tokens, source labels, client kinds, agent session-id vars). |
| `config_test.go` | Unit tests for the pure functions (JSONC stripper, id/session shape). |
| `scripts/e2e*.sh` | End-to-end scenarios (flat, rooms, identity). |
| `hooks/` | Claude Code integration (register / incoming / leave). |

## Data model (schema)

One SQLite file. WAL journal. All validation lives in the app layer (the DB is dumb storage).

```sql
users(
  user_id    TEXT PRIMARY KEY,   -- usr_<7-char nanoid>, stable & immutable
  username   TEXT NOT NULL UNIQUE,
  created_at TEXT NOT NULL
)

rooms(
  name       TEXT PRIMARY KEY,   -- "main" is the default, system-owned
  owner_id   TEXT NOT NULL,      -- user_id of the creator ("" for system)
  created_at TEXT NOT NULL
)

sessions(                        -- one row per terminal/agent
  session_id TEXT PRIMARY KEY,   -- username~<8-hex hash of the terminal token>
  owner_id   TEXT NOT NULL,      -- -> users.user_id
  -- terminal/agent context (captured at register, refreshed on use):
  cwd, tty, host, client, os_user, os, arch, shell, term_program TEXT,
  pid INTEGER, ssh INTEGER, tmux INTEGER,
  git_branch, git_repo, version TEXT,
  id_source  TEXT,               -- where the session id was derived from
  meta       TEXT,               -- reserved JSON blob for future fields
  created_at TEXT NOT NULL,
  last_seen  TEXT NOT NULL
)

members(                         -- per-session room membership
  room       TEXT NOT NULL,
  session_id TEXT NOT NULL,      -- -> sessions.session_id
  owner_id   TEXT NOT NULL,      -- -> users.user_id (denormalized; owner never changes)
  role       TEXT NOT NULL,      -- ceo/cto/dev/guest/…
  joined_at  TEXT NOT NULL,
  PRIMARY KEY (room, session_id)
)

messages(
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  room       TEXT NOT NULL DEFAULT 'main',
  from_id    TEXT NOT NULL,      -- sender session_id
  to_id      TEXT NOT NULL,      -- recipient session_id (fan-out resolved at send time)
  kind       TEXT NOT NULL DEFAULT 'msg',  -- msg | request | secret | deny
  body       TEXT NOT NULL,
  ref_id     INTEGER,            -- for grant/deny: the request message id
  created_at TEXT NOT NULL,
  read_at    TEXT                -- NULL = unread
)
```

Key modeling decisions:

- **user vs session.** A user (`userId` + `username`) is a stable identity; a session is one terminal/agent it owns. This split makes rename cheap and lets one identity be present in many places at once. See [Identity & sessions](#identity--sessions).
- **`members` is per-session.** When bob has three terminals in a room, that's three membership rows. Delivery is per-terminal, so this lines up naturally, and grouping by `owner_id` gives the per-user view for free.
- **`members.owner_id` is denormalized** (also derivable via `sessions`). Safe, because a session's owner never changes, and it lets us group/scope by user without a join.
- **Fan-out at send time.** A broadcast / `@role` / username send expands into **one `messages` row per recipient session**, each with its own `read_at`. So `recv` is a plain "unread rows where `to_id` = me" query, and consume-once holds even under racing readers.
- **`meta`** is an intentionally-reserved JSON column for future freeform fields, so adding one never needs a migration.

## How a message flows

`alice: lorewire send --to bob "hi"` in room `demo`:

1. `cmdSend` resolves alice's identity + session (`ctx`), ensuring her session row exists and she's a member of `demo`.
2. `Store.Send` calls `resolveRecipients("demo", alice_session, "bob")` → looks up **bob's session ids that are members of `demo`** (fan-out for a username).
3. It inserts one `messages` row per recipient (`to_id` = each bob session), `read_at` NULL.
4. `bob: lorewire recv` → `Store.Recv(bob_session, "")` selects unread rows where `to_id` = bob's session across all rooms, returns them, and in the **same transaction** marks them read (or hard-deletes secrets). Consume-once.

The recipient must be a **member** of the room to receive — which `recv`/`watch`/`register`/`send` all ensure automatically (see [membership](#rooms-roles--membership)).

## Identity & sessions

Full plain-language version: [SESSIONS.md](SESSIONS.md). In brief:

- **userId** — `usr_` + a 7-char nanoid (`crypto/rand`, alphabet `a-z0-9`). Immutable. The anchor everything references.
- **username** — unique, human, renameable. A rename updates `users` and **cascades** the `username~` prefix across that user's `sessions`/`members`/`messages` rows in one transaction (so live terminals keep their session; the userId — what configs point at — is untouched).
- **session id** — `username` + `~` + first 8 hex of `sha256(terminalToken)`. The token is resolved tool-agnostically (agent session-id env var → tty → pid; overridable). 8 hex = 32 bits keeps per-user collisions negligible. Each session records `id_source` for debugging.

The critical insight for agents: inside an agent, stdin is piped and `/dev/tty` is often unavailable, so the token comes from the **agent's own session-id env var** (e.g. `CLAUDE_CODE_SESSION_ID`), which every tool-call and hook of that agent inherits → one stable session.

## Concurrency

Every `lorewire` invocation is a separate process, so coordination is entirely at the SQLite layer:

- **WAL journal mode** — readers proceed during a writer.
- **`busy_timeout(10000)`** — wait up to 10s for the write lock instead of failing.
- **`withRetry`** — a Go-side retry loop on `SQLITE_BUSY`/locked (belt-and-suspenders over busy_timeout), so a transient lock never surfaces as a user error.
- **`synchronous=NORMAL`** — safe under WAL (no corruption on crash; at most the last committed transaction lost on power failure), markedly faster for many small writes.
- **Consume-once under races** — `Recv` selects and marks-read/deletes in one transaction, so two concurrent `recv` calls can't both claim the same message.

The e2e suite includes a 20-parallel-senders + racing-recv test that asserts exactly-once delivery.

## Rooms, roles & membership

- **Rooms are optional.** Everything happens in `main` unless a room is specified. Resolution: `--room` > `$LOREWIRE_ROOM` > `.lorewire.jsonc` > `main`.
- **Delivery is room-scoped.** `--to <user>` / `@role` / `all` only reach sessions that are **members of that room**. So a receiver must be a member — which is why `recv`/`watch`/`inbox`/`send`/`request` all ensure membership.
- **`EnsureMember` vs `Join` — an important distinction.** `Join` (used by `register`/`join`) **upserts the role** because those are deliberate "set my role here" actions. Incidental commands (`send`/`recv`/…) use **`EnsureMember`** (insert-or-ignore) so merely messaging or listening **never overwrites** a role set by an explicit `join --role`/`role set`. This was a real bug once; the split is the fix.
- **Addressing** (`resolveRecipients`): `@role` → members with that role; `all`/`*` → all members; a value containing `~` → a literal session id; anything else → a username (fan-out to that user's member sessions). Sender always excluded.

## Secrets

A request/grant/deny flow lets a session ask a role-holder for something (e.g. an API key):

- `request --to @cto "…"` → a `messages` row with `kind=request`; recipients see `[request#ID]`.
- `grant ID --secret "…"` → a `kind=secret` reply to the original requester, linked via `ref_id`.
- **Consume-once:** on `recv`, secret rows are **hard-deleted** (not just marked read), so a key survives exactly one read. In `inbox` (a non-consuming peek) secret bodies are **masked**, so a peek can't leak them.

Scope note: the store is a local plaintext SQLite file — this is for cooperative sessions on one machine, not a hardened multi-user vault.

## Config resolution

`.lorewire.jsonc` (JSONC — comments stripped by a small string-aware parser, no dependency) supplies defaults; env and flags override. Found by walking up from the current directory (like `.git`). Keys: `userId`, `username` (portability hint for `import`), `room`, `role`.

Precedence (first non-empty wins), resolved as a whole so a flag/env of either kind beats a lower layer:

- **Identity:** `--user`/`--name` → `$LOREWIRE_USER_ID`/`$LOREWIRE_NAME` → config `userId`.
- **Room:** `--room` → `$LOREWIRE_ROOM` → config `room` → `main`.
- **Role:** `--role` → `$LOREWIRE_ROLE` → config `role` → `guest`.
- **Session token:** `$LOREWIRE_SESSION` (full id) → `$LOREWIRE_SESSION_TOKEN` → `$LOREWIRE_SESSION_ENV`-named var → built-in agent vars → tty → pid.

`lorewire import` re-creates the config's identity in an empty DB (fresh machine), reading the `userId` + `username` from the file.

## Migrations & the legacy backup

- **Additive migrations.** New columns are added to existing tables via `ALTER TABLE ADD COLUMN`, guarded by a `columnExists` check, so upgrading keeps data.
- **Incompatible-schema backup.** A pre-identity database (has `messages` but no `users` table) is not forward-compatible; rather than wipe it, lorewire renames it aside to `lorewire.db.bak-<timestamp>` and starts fresh, printing a notice. The old file is never silently destroyed.

## Design principles we follow

These come from the project's engineering standards and are enforced in review:

- **No bare strings for closed sets.** Every delimiter, id prefix, env-var name, addressing token, source label, and client kind is a named constant in `constants.go`.
- **DB as dumb storage.** No triggers, no CHECK constraints beyond structural ones; all validation is app-layer. (Keeps future sharding/portability simple.)
- **Context-dense comments.** Exported symbols and any non-obvious/"looks wrong but intentional" block explain the *why* and the invariant, so a fresh session loses no context.
- **DRY at the data layer.** Shared `sessionSelectCols` + `scanSession` so the SELECT and scanner can't drift.
- **The build is the test.** `gofmt` + `go vet` + `go test -race` + the e2e suites gate every change (`task check` / `task test`).

## Deliberate non-goals

- **No networking / multi-machine.** One machine, one DB. Cross-host would need a synced store + real auth (a different product).
- **No enforced permissions.** Roles are cooperative labels; a session can claim any role. Fine for a single user's own agents.
- **Not a secrets vault.** Consume-once + masking reduce lingering, but the DB is plaintext and local.
- **No message TTL/GC (yet).** Read messages stay until `reset`/`prune`. Easy to add later.

See also: [SESSIONS.md](SESSIONS.md) (identity resolution), [INTEGRATIONS.md](INTEGRATIONS.md) (wiring agents), [REFERENCE.md](REFERENCE.md) (commands), [GLOSSARY.md](GLOSSARY.md) (terms).
