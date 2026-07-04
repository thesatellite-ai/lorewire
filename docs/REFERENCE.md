# lorewire — complete reference

Exhaustive documentation for every command, flag, environment variable, config key, addressing form, hook, and table. For the overview and pitch see the [README](../README.md); this document is the full manual.

## Contents

- [Concepts](#concepts)
- [Installation](#installation)
- [Configuration](#configuration)
- [Environment variables](#environment-variables)
- [Resolution precedence](#resolution-precedence)
- [Addressing (`--to`)](#addressing---to)
- [Command reference](#command-reference)
  - [Identity & config](#identity--config-commands)
  - [Presence & rooms](#presence--rooms-commands)
  - [Messaging](#messaging-commands)
  - [Secrets (request / grant / deny)](#secret-commands)
- [Hooks](#hooks)
- [Database & schema](#database--schema)
- [Message kinds](#message-kinds)
- [Exit codes & errors](#exit-codes--errors)
- [Recipes](#recipes)

## Concepts

| Term | Meaning |
|---|---|
| **user** | A stable identity: an immutable `userId` (`usr_` + 7-char nanoid, e.g. `usr_k3n9x2p`) plus a unique, renameable `username` (`bob`). Stored once in the `users` table. |
| **session** | One open terminal, owned by a user. Auto-named `username~<hash>` (e.g. `bob~a1f`) from the terminal it runs in. One user owns many sessions. |
| **room** | A named channel that scopes messages. Default room is `main`, so rooms are optional. A session can be in many rooms at once. |
| **role** | A label a session holds in a room (`ceo`, `cto`, `dev`, …). Used for `@role` addressing and as coordination metadata. No-role joins default to `guest`. Trust is cooperative/local — roles are labels, not enforced ACLs. |
| **message** | A row delivered to one recipient session. Fan-out (broadcast, `@role`, username) resolves to one row per recipient at send time. |
| **secret** | A message delivered consume-once: hard-deleted after a single `recv`, and masked in non-consuming `inbox` peeks. |

## Installation

```bash
# from source (Go 1.26+):
git clone https://github.com/thesatellite-ai/lorewire.git
cd lorewire
task install          # go install . → ~/go/bin/lorewire (ensure ~/go/bin is on PATH)

# or build a local binary:
task build            # ./lorewire

# or directly:
go install github.com/thesatellite-ai/lorewire@latest
```

Verify:

```bash
lorewire --help
```

## Configuration

A project-local `.lorewire.jsonc` supplies your default identity, room, and role so terminals self-configure. It is JSONC — `//` and `/* */` comments are allowed.

```jsonc
// .lorewire.jsonc — commit this; env vars and flags override it per terminal/command
{
  "userId": "usr_k3n9x2p",   // your stable identity (claim with `lorewire user create`)
  "room": "project-x",        // default room for commands run here
  "role": "cto"               // role you assume when you join
}
```

**Keys:**

| Key | Type | Meaning |
|---|---|---|
| `userId` | string | The identity this directory belongs to. Must exist in the local database (create/import it with `lorewire user create`). |
| `room` | string | Default room. Falls back to `main`. |
| `role` | string | Role assumed on `join`/`register`. Falls back to `guest`. |

**Discovery:** lorewire walks up from the current directory to the nearest `.lorewire.jsonc` (like `.git`/`.editorconfig`), so it works from subdirectories.

**Writing it:** `lorewire user create` and `lorewire init` write this file for you (see those commands).

## Environment variables

| Variable | Used by | Meaning |
|---|---|---|
| `LOREWIRE_DB` | all | Path to the SQLite database. Default `~/.lorewire/lorewire.db`. |
| `LOREWIRE_USER_ID` | all identity-aware commands | Overrides the config `userId`. Must exist in the DB. |
| `LOREWIRE_NAME` | all identity-aware commands | Username (quick mode). If no userId is resolved anywhere, the user is auto-created for this name. |
| `LOREWIRE_ROOM` | messaging/presence | Overrides the default room. |
| `LOREWIRE_ROLE` | register/join | Overrides the default role. |
| `LOREWIRE_SESSION` | all | Overrides the computed session id for this terminal entirely. |
| `LOREWIRE_SESSION_TOKEN` | all | Overrides the terminal token used to derive the session id (advanced; use to force distinct sessions). |
| `LOREWIRE_CLIENT` | register | Label stored on the session (`claude-code`, `shell`, …). Auto-detected if unset. |
| `LOREWIRE_BIN` | hooks | Path to the `lorewire` binary for the hook scripts (defaults to `lorewire` on `PATH`). |

## Resolution precedence

For every value, the first non-empty source wins:

**Identity** (userId + username, resolved together, first match):

1. `--user usr_…` flag
2. `--name NAME` / `--from NAME` flag
3. `$LOREWIRE_USER_ID`
4. `$LOREWIRE_NAME`
5. `.lorewire.jsonc` `userId`
6. otherwise → error (`no identity: …`)

An explicit `$LOREWIRE_NAME` therefore overrides a committed config `userId` — handy when a shared repo's config names someone else.

**Room:** `--room` → `$LOREWIRE_ROOM` → config `room` → `main`.

**Role:** `--role` → `$LOREWIRE_ROLE` → config `role` → `guest`.

**Session id:** `$LOREWIRE_SESSION` → `username~<hash(terminal token)>`. The terminal token is `$LOREWIRE_SESSION_TOKEN` → the tty device behind stdin → the parent shell pid.

## Addressing (`--to`)

`send` and `request` accept these `--to` forms, resolved within the target room:

| Form | Delivers to |
|---|---|
| `bob` (a username) | **all** of that user's sessions in the room (fan-out) |
| `bob~a1f` (a session id, contains `~`) | that one terminal |
| `@cto` (an `@role`) | every session in the room holding that role |
| `all` or `*` | every session in the room |

The sender's own session is always excluded from fan-out. A `--to` that resolves to zero recipients prints a `WARN` (it never looks like success).

## Command reference

Every command reads `$LOREWIRE_DB` (or the default DB) and exits non-zero with `error: …` on failure. Flags shown with their defaults.

### Identity & config commands

#### `lorewire user create`

Claim a username and mint (or import) its userId, then write `./.lorewire.jsonc`.

```
lorewire user create NAME [--id usr_…] [--room ROOM] [--role ROLE] [--no-write]
```

| Flag | Default | Meaning |
|---|---|---|
| `--id` | (mint new) | Reuse an existing userId instead of minting one (re-import on a new machine). |
| `--room` | `main` | Room to seed into the written config. |
| `--role` | `guest` | Role to seed into the written config. |
| `--no-write` | `false` | Don't write `.lorewire.jsonc`. |

`NAME` is positional and must come first. Fails if the username is already taken, or if a supplied `--id` already belongs to a different username.

```bash
lorewire user create bob --room project-x --role cto
#   user "bob" → usr_k3n9x2p
#   wrote /abs/path/.lorewire.jsonc

# re-import an identity on a new machine (config already has the userId):
lorewire user create bob --id usr_k3n9x2p

# mint without touching the filesystem:
lorewire user create ci-bot --no-write
```

#### `lorewire user list`

```
lorewire user list [--json]
```

Lists every user with its userId and session count.

```bash
lorewire user list
#   bob        usr_k3n9x2p  2 session(s)
#   johnw      usr_9q4bznt  1 session(s)
lorewire user list --json
```

#### `lorewire user rename`

```
lorewire user rename OLD NEW
```

Renames a username. The userId is unchanged (so committed configs keep working), and the rename **cascades** to that user's live session ids (`bob~a1f` → `bobby~a1f`) across sessions, memberships, and messages. `NEW` may not contain `~` or spaces.

```bash
lorewire user rename bob bobby
```

#### `lorewire init`

```
lorewire init --username NAME | --user usr_… [--room ROOM] [--role ROLE]
```

Points the current directory's `.lorewire.jsonc` at an **existing** identity (does not create one). Use to reuse one identity across multiple project folders.

| Flag | Meaning |
|---|---|
| `--username` | Existing username to resolve to a userId. |
| `--user` | Existing userId directly. |
| `--room` | Seed room in the written config. |
| `--role` | Seed role in the written config. |

```bash
cd ~/work/other-project
lorewire init --username bob --room other --role dev
```

#### `lorewire import`

```
lorewire import [NAME]
```

Re-creates the identity described by the current directory's `.lorewire.jsonc` in this machine's database — the fresh-machine / post-wipe path. Reads the `userId` (and `username` hint) from the config and registers it if the DB doesn't already have it. Idempotent: a no-op if the identity already exists. `NAME` is optional and only needed if the config predates the `username` field.

```bash
# on a new machine after cloning a repo whose .lorewire.jsonc has a userId:
lorewire import
#   imported "bob" (usr_k3n9x2p) from .../.lorewire.jsonc — ready to use here
lorewire import            # again → "already imported: usr_k3n9x2p is "bob""
```

#### `lorewire whoami`

```
lorewire whoami [--json]
```

Prints the effective identity/session/room/role for the current directory and environment, the **source** of each value (flag / env / config / default), and — once this terminal has registered — the current session's full stored detail (cwd, tty, pid, host, client, timestamps) and the rooms it belongs to. The go-to command for "who am I, and what is this terminal's session?".

```bash
lorewire whoami
#   username : bob (config)
#   userId   : usr_k3n9x2p
#   session  : bob~a1f
#   room     : project-x (config)
#   role     : cto (config)
#   config   : /abs/path/.lorewire.jsonc
#
#   session (this terminal):
#     cwd      : /Users/bob/project-x
#     tty      : /dev/ttys004
#     pid      : 90382
#     host     : mbp.local
#     client   : claude-code
#     started  : 14:02:11
#     last seen: just now
#     member of: project-x (cto), ops (lead)
```

`--json` returns everything in one object — resolved identity, `sources`, `sessionDetail` (the full session row, `null` until registered), `memberships`, and `config`:

```bash
lorewire whoami --json
# { "userId": "...", "username": "...", "session": "bob~a1f", "registered": true,
#   "room": "...", "role": "...", "config": "...",
#   "sources": { "identity": "config", "room": "config", "role": "config" },
#   "sessionDetail": { "ID": "bob~a1f", "CWD": "...", "TTY": "...", "PID": 90382,
#                      "Host": "...", "Client": "claude-code", "CreatedAt": "...", "LastSeen": "..." },
#   "memberships": [ { "Room": "project-x", "Role": "cto", ... } ] }
```

Every listing/read command also supports `--json` for scripting: `whoami`, `sessions`, `rooms`, `members`, `user list`, `recv`, `inbox`, `watch`.

### Presence & rooms commands

#### `lorewire register`

```
lorewire register [--new] [--user usr_…] [--name NAME] [--room ROOM] [--role ROLE]
```

Registers this terminal's session (capturing cwd/tty/pid/host/client) and joins the resolved room with the resolved role. Idempotent — safe to run repeatedly (it heartbeats).

| Flag | Default | Meaning |
|---|---|---|
| `--new` | `false` | Force a fresh session handle for this terminal (rotate the token). |
| `--user` | (resolved) | userId override. |
| `--name` | (resolved) | username override (quick mode). |
| `--room` | (resolved) | room override. |
| `--role` | (resolved) | role override. |

```bash
lorewire register                       # uses .lorewire.jsonc / env
LOREWIRE_NAME=alice lorewire register   # quick mode, no config
lorewire register --new                 # distinct session for a second pane sharing a tty
```

#### `lorewire join`

```
lorewire join --room ROOM [--role ROLE] [--user usr_…] [--name NAME]
```

Joins (creating if new — first joiner owns it) a room with a role. Rejoining updates your role.

```bash
lorewire join --room project-x --role cto
```

#### `lorewire leave`

```
lorewire leave [--room ROOM] [--purge]        # leave one room
lorewire leave --all [--purge]                # remove this terminal's session everywhere
```

| Flag | Default | Meaning |
|---|---|---|
| `--room` | (resolved) | Room to leave. |
| `--all` | `false` | Remove this session from every room and delete the session row. |
| `--purge` | `false` | Also delete this session's inbox (messages addressed to it). |

`leave --all` affects only **this** terminal's session, not the whole user. This is what the `SessionEnd` hook runs.

```bash
lorewire leave --room project-x
lorewire leave --all --purge
```

#### `lorewire prune`

```
lorewire prune [--older-than 30m]
```

Removes sessions whose last activity predates the cutoff (a janitor for crashed terminals) along with their memberships. Messages are left intact.

```bash
lorewire prune --older-than 1h
```

#### `lorewire reset`

```
lorewire reset sessions [--user NAME | --me] [--yes]
lorewire reset messages [--yes]
lorewire reset all [--yes]
```

Deletes data, with a **preview-then-confirm** gate: without `--yes` it only prints what *would* be deleted and the exact confirming command; with `--yes` it deletes and reports counts.

| Scope | Deletes | Keeps |
|---|---|---|
| `sessions` | all sessions + memberships | users, rooms, messages |
| `sessions --user NAME` | just that user's sessions | everyone/everything else |
| `sessions --me` | just your sessions (current folder's identity) | everyone/everything else |
| `messages` | all messages | users, rooms, sessions |
| `all` | everything (users, rooms, sessions, messages) | — (re-seeds the default room) |

```bash
lorewire reset sessions --user bob        # preview: "would delete: 2 session(s) belonging to bob…"
lorewire reset sessions --user bob --yes  # "deleted 2 session(s) of bob"
lorewire reset all --yes                  # full wipe (then `lorewire import` to restore identities)
```

#### `lorewire rooms`

```
lorewire rooms [--me] [--json]
```

Lists rooms with member counts and owner. `--me` limits to rooms your current identity is a member of (member counts still reflect the whole room).

```bash
lorewire rooms
#   main        1 member(s)  owner (system)
#   project-x   4 member(s)  owner bob
lorewire rooms --me        # only rooms you're in
```

#### `lorewire members`

```
lorewire members [--room ROOM] [--json]
```

Lists a room's member sessions with their roles and owning username. Room resolves from `--room` → `$LOREWIRE_ROOM` → config → `main`.

```bash
lorewire members --room project-x
#   room "project-x":
#     bob~a1f            cto       (bob)
#     carol~9c2          dev       (carol)
#     dave~7f1           guest     (dave)   ← needs a role
```

#### `lorewire role set`

```
lorewire role set NAME|SESSION ROLE [--room ROOM]
```

Changes a member's role. A **username** target updates all that user's sessions in the room; a **session id** (`bob~a1f`) updates just that one.

```bash
lorewire role set dave qa --room project-x
lorewire role set carol~9c2 lead --room project-x
```

#### `lorewire sessions`

```
lorewire sessions [--me] [--json]
```

Lists live sessions grouped by user, with cwd (and git branch inline), client, and last-seen. `--me` limits to your own sessions. `--json` includes the full per-session detail: `TTY`, `PID`, `Host`, `OSUser`, `OS`/`Arch`/`Shell`, `TermProgram`, `SSH`/`Tmux`, `GitBranch`/`GitRepo`, `Version`, and `IDSource` (where the session id was derived — `agent:VAR` / `tty` / `ppid` / `env:VAR`).

```bash
lorewire sessions
#   bob (usr_k3n9x2p)
#     bob~a1f   /Users/bob/project-x   ttys004   claude-code   seen just now
#     bob~b2c   /Users/bob/project-x   ttys006   shell         seen 2m ago
```

### Messaging commands

#### `lorewire send`

```
lorewire send [--room ROOM] --to TARGET [--from NAME] [--user usr_…] [--msg BODY] [BODY…]
```

Sends a message. See [Addressing](#addressing---to) for `--to` forms. The body is `--msg` or the trailing positional words. Ensures the sender's session exists first.

| Flag | Meaning |
|---|---|
| `--to` | Recipient: `NAME`, `@ROLE`, `all`, or a session id. **Required.** |
| `--room` | Room (resolved otherwise). |
| `--from` | Sender username override. |
| `--user` | Sender userId override. |
| `--msg` | Body (alternative to positional). |

```bash
lorewire send --to bob "PR is ready"
lorewire send --room project-x --to @dev "who takes the login page?"
lorewire send --to all "build is green"
lorewire send --to bob~a1f "just your left terminal"
```

#### `lorewire recv`

```
lorewire recv [--room ROOM] [--name NAME] [--user usr_…] [--json]
```

Reads **and consumes** unread messages for your session. Without `--room`, drains all your rooms at once. Secrets are revealed here and then hard-deleted (consume-once).

```bash
lorewire recv
lorewire recv --room project-x --json
```

#### `lorewire inbox`

```
lorewire inbox [--room ROOM] [--all] [--name NAME] [--user usr_…] [--json]
```

Shows messages **without** consuming them. `--all` includes already-read history. Secret bodies are masked here (only `recv` reveals them).

```bash
lorewire inbox --all
lorewire inbox --room project-x
```

#### `lorewire watch`

```
lorewire watch [--room ROOM] [--interval 2s] [--name NAME] [--user usr_…] [--json]
```

Blocks and streams new messages as they arrive (consuming them), polling every `--interval`. Ctrl-C to stop. A dedicated inbox terminal.

```bash
lorewire watch
lorewire watch --room project-x --interval 1s
```

### Secret commands

#### `lorewire request`

```
lorewire request [--room ROOM] --to @ROLE|NAME [--from NAME] [--user usr_…] [--msg BODY] [BODY…]
```

Asks a role-holder (or a specific user) for something. Recipients see it tagged `[request#ID]` in their inbox.

```bash
lorewire request --room project-x --to @cto "OpenAI API key for project-x"
```

#### `lorewire grant`

```
lorewire grant ID --secret VALUE [--from NAME] [--user usr_…]
lorewire grant ID [--from NAME] VALUE…
```

Answers request `ID` with a secret, delivered **consume-once** to the requester (hard-deleted after one `recv`). `ID` is the leading positional; the secret is `--secret` or trailing positional words.

```bash
lorewire grant 12 --secret "sk-abc123"
```

#### `lorewire deny`

```
lorewire deny ID [--from NAME] [--user usr_…] REASON…
```

Declines request `ID`; the requester is notified with the reason.

```bash
lorewire deny 12 "use the shared vault instead"
```

## Hooks

Two optional Claude Code hooks turn pull-based delivery into auto-delivery. Both are no-ops unless an identity is available (either `$LOREWIRE_NAME`/`$LOREWIRE_USER_ID` is set, or a `.lorewire.jsonc` exists in the tree), so they're safe to enable globally.

**`hooks/lorewire-incoming.sh`** — a `UserPromptSubmit` hook that runs `lorewire recv` and injects any pending messages into the session's context on each turn (consuming them).

**`hooks/lorewire-leave.sh`** — a `SessionEnd` hook that runs `lorewire leave --all` so a session unregisters itself when it closes.

Wire both in `~/.claude/settings.json` (or a project `.claude/settings.json`), merging with any existing `hooks` (see `hooks/settings.example.json`):

```json
{
  "hooks": {
    "UserPromptSubmit": [
      { "hooks": [ { "type": "command", "command": "/abs/path/hooks/lorewire-incoming.sh" } ] }
    ],
    "SessionEnd": [
      { "hooks": [ { "type": "command", "command": "/abs/path/hooks/lorewire-leave.sh" } ] }
    ]
  }
}
```

The hooks honor `$LOREWIRE_BIN` (path to the binary) and `$LOREWIRE_CLIENT` (session label).

## Database & schema

Single SQLite file at `$LOREWIRE_DB` (default `~/.lorewire/lorewire.db`), WAL journal mode, `busy_timeout` + retry-on-lock for cross-process concurrency.

```sql
users(user_id PK, username UNIQUE, created_at)
rooms(name PK, owner_id, created_at)
sessions(session_id PK, owner_id, cwd, tty, pid, host, client,
         os_user, os, arch, shell, term_program, ssh, tmux, git_branch, git_repo, version, id_source,
         meta, created_at, last_seen)
members(room, session_id, owner_id, role, joined_at, PRIMARY KEY(room, session_id))
messages(id PK, room, from_id, to_id, kind, body, ref_id, created_at, read_at)
```

- `sessions.meta` is a JSON blob for extension fields (no migration needed to add one).
- `members.owner_id` is denormalized (safe — a session's owner never changes) so users can be grouped without a join.
- A broadcast / `@role` / username send fans out into one `messages` row per recipient, each with its own `read_at`, so `recv` is a simple "unread rows for my session" query and consume-once holds under racing reads.
- Secret rows are **deleted** (not just marked read) on `recv`.
- **Migration:** an incompatible pre-identity database (has `messages` but no `users`) is moved to `lorewire.db.bak-<timestamp>` and a fresh one is created — the old file is never wiped silently.

## Message kinds

The `kind` column drives display and delivery semantics:

| Kind | Meaning | Display tag |
|---|---|---|
| `msg` | Normal chatter | (none) |
| `request` | A request for something | `[request#ID]` |
| `secret` | A consume-once secret reply | `[secret]` (masked in `inbox`) |
| `deny` | A declined request | `[denied]` |

## Exit codes & errors

- Exit `0` — success.
- Exit `1` — runtime error (printed as `error: <message>` to stderr).
- Exit `2` — usage error (unknown command / missing required argument); usage is printed.

Common errors:

- `no identity: run lorewire user create <name> …` — no userId or name resolved anywhere.
- `userId "usr_…" is not in this database — run lorewire user create <name> --id usr_… to import it` — a config/env userId that this DB doesn't know (e.g. new machine).
- `username "…" is already taken by usr_…` — the username exists under a different userId.
- `WARN: no recipients for "…" in room "…"` — a send/request fan-out matched nobody (delivered to no one).

## Recipes

**Two sessions, zero setup (quick mode):**

```bash
# terminal 1
export LOREWIRE_NAME=alice; lorewire register
# terminal 2
export LOREWIRE_NAME=bob;   lorewire register
lorewire send --to alice "hey"      # from bob
lorewire recv                        # alice reads it
```

**A project team via config:**

```bash
cd ~/work/project-x
lorewire user create bob --room project-x --role cto   # writes .lorewire.jsonc
lorewire register                                       # any terminal here is bob@project-x
lorewire send --to @dev "standup in 5"
```

**Reach a teammate on all their terminals:**

```bash
lorewire send --room project-x --to carol "can you rebase?"   # → every carol~ session
```

**Hand off an API key safely:**

```bash
# requester
lorewire request --room project-x --to @cto "staging API key"
# cto (sees [request#7])
lorewire grant 7 --secret "sk-…"        # requester reads once via recv, then it's gone
```

**Multiple rooms from one identity:**

```bash
export LOREWIRE_ROOM=project-x; lorewire send --to @dev "x update"
lorewire send --room ops --to @sre "deploy at 3pm"   # override room per command
```

**Clean up crashed sessions:**

```bash
lorewire prune --older-than 30m
```
