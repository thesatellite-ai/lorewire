# lorewire

**A message bus for AI coding agent sessions to talk to each other.**

<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-10B981" alt="License: Apache-2.0"></a>
  <img src="https://img.shields.io/badge/go-1.26+-10B981" alt="Go 1.26+">
  <img src="https://img.shields.io/badge/platform-macOS%20%7C%20Linux-10B981" alt="Platforms: macOS, Linux">
  <img src="https://img.shields.io/badge/100%25-local%20%C2%B7%20no%20server-10B981" alt="100% local, no server">
  <img src="https://img.shields.io/badge/deps-single%20binary-10B981" alt="Single binary, no runtime deps">
  <a href="https://github.com/thesatellite-ai/lorewire/stargazers"><img src="https://img.shields.io/github/stars/thesatellite-ai/lorewire?style=social" alt="GitHub stars"></a>
</p>

**lorewire** is a tiny, local-first, agent-agnostic **message bus** that lets multiple **AI coding agent sessions** — [Claude Code](https://claude.com/claude-code), other CLI agents, or plain shell scripts — send and receive messages across separate terminals. It's inter-process communication (IPC) for **multi-agent** workflows: register a name, then message peers **directly, by role, or by broadcast**, scope conversations into **rooms**, and hand secrets like **API keys** to whoever holds a role via a consume-once request/grant flow. One **Go** binary, pure-Go **SQLite** store, no server, no cloud, no API key.

Your **identity** (a stable `userId` + human `username`) and your default room/role live in a project-local `.lorewire.jsonc`, so opening a terminal in a project auto-configures it — no `export` every time. One identity owns many **sessions** (one per terminal). Delivery is pull-based (a session reads its inbox with `lorewire recv`), with an optional Claude Code hook that pushes incoming messages into a session automatically. `lorewire` is the comms sibling in the **lore** family — *lore agents talk over the lorewire.*

## Contents

- [Why lorewire?](#why-lorewire)
- [Why this design](#why-this-design)
- [Install](#install)
- [Quickstart — wire two sessions together](#quickstart--wire-two-sessions-together)
- [Identity & config](#identity--config)
- [Rooms, roles & secrets](#rooms-roles--secrets)
- [Commands](#commands)
- [Push layer — auto-deliver into a session](#push-layer--auto-deliver-into-a-session-optional)
- [Leaving & cleanup](#leaving--cleanup)
- [Walkthrough — two real Claude Code sessions talking](#walkthrough--two-real-claude-code-sessions-talking)
- [Storage](#storage)
- [Development](#development)
- [FAQ](#faq)
- [The lore family](#the-lore-family)

## Why lorewire?

**The wedge:** an AI coding agent can only act through its own tools, in its own process. Spin up two or three Claude Code sessions and they're blind to each other — no way to hand off work, ask "who's taking the frontend?", or share an API key without you copy-pasting between terminals. lorewire gives every session a shell command it already knows how to call, backed by one shared file, so the sessions coordinate *themselves*.

- **Agent-agnostic** — anything that can run a shell command joins the wire: Claude Code, other agents, scripts, or you.
- **Three addressing modes** — direct (`--to bob`), by role (`--to @cto`), or broadcast (`--to all`).
- **Rooms** — scope a conversation to a project; a session can be in many rooms at once.
- **Roles** — members declare who they are (ceo, cto, dev…), so an agent asks *a role*, not a name it may not know.
- **Consume-once secrets** — request an API key from whoever holds a role; it's delivered once and hard-deleted from history.
- **Auto-delivery** — an optional hook injects incoming messages into a Claude Code session on each turn; a matching hook auto-unregisters on exit.
- **Zero infrastructure** — a single Go binary and a local SQLite file. No server, no daemon, no cloud, no account.

| | **lorewire** | Shared files / ad-hoc | `tmux send-keys` | Custom HTTP broker |
|---|:---:|:---:|:---:|:---:|
| Cross-session messaging | ✅ | ⚠️ | ⚠️ | ✅ |
| Name / role / broadcast addressing | ✅ | ❌ | ❌ | ⚠️ |
| Rooms (multi-project scoping) | ✅ | ❌ | ❌ | ⚠️ |
| Consume-once secret delivery | ✅ | ❌ | ❌ | ⚠️ |
| Auto-deliver into the agent (hooks) | ✅ | ❌ | ⚠️ | ❌ |
| Concurrency-safe (atomic consume) | ✅ | ❌ | n/a | ✅ |
| No server / daemon to run | ✅ | ✅ | ✅ | ❌ |
| Single binary, zero runtime deps | ✅ | ✅ | ✅ | ❌ |

If you want a hosted, multi-tenant chat system, other tools exist. If you want dead-simple, local, zero-infra messaging between coding-agent sessions on your machine, that's lorewire.

## Why this design

Agent sessions (like Claude Code) are independent processes, and the model inside each one acts through tools — mostly the shell. So the mechanism has to be something it can invoke from a terminal: a small CLI (`lorewire`) plus a shared store (one SQLite file). No daemon, no network. Concurrent sessions coordinate through SQLite's own locking, hardened with a busy-timeout plus a retry-on-lock wrapper so parallel sends/reads never surface a lock error.

Nothing in the core is Claude-specific — it's a named message bus any process can call. Claude Code is simply the first supported integration (see the push hook below); other agents just need their own thin adapter.

## Install

```bash
task install          # go install . → puts `lorewire` on your $PATH (needs ~/go/bin on PATH)
# or just build locally:
task build            # produces ./lorewire in the repo
```

## Quickstart — wire two sessions together

Open two terminals. Give each its own identity via `LOREWIRE_NAME`, then launch Claude Code (or just use `lorewire` directly):

```bash
# terminal 1
export LOREWIRE_NAME=alice
lorewire register

# terminal 2
export LOREWIRE_NAME=bob
lorewire register
```

Now they can talk:

```bash
# alice sends a direct message to bob
lorewire send --to bob "can you take the frontend? I'll do the API"

# bob reads (and consumes) his inbox
lorewire recv
# [14:02:11] alice → bob: can you take the frontend? I'll do the API

# bob replies
lorewire send --to alice "on it"

# alice broadcasts to everyone registered
lorewire send --to all "build is green, pushing now"
```

`lorewire sessions` lists who's registered and when they were last active. That two-terminal flow is **quick mode**: just set `$LOREWIRE_NAME` and go — lorewire lazily creates the user for you. For anything ongoing, use a config file instead (next section).

## Identity & config

Retyping `export LOREWIRE_NAME=…` in every terminal gets old. Drop a `.lorewire.jsonc` in your project and lorewire self-configures from it — env vars and flags still override.

**Your identity is two things:**

- **username** — the human name you see: `bob`. Unique, and renameable.
- **userId** — a stable, immutable id (`usr_k3n9x2p`) that everything secretly references, so a rename never breaks anything.

Claim an identity once — it mints the userId and writes the config file for you:

```bash
cd ~/work/project-x
lorewire user create bob --room project-x --role cto
# → user "bob" → usr_k3n9x2p
# → wrote ./.lorewire.jsonc
```

```jsonc
// .lorewire.jsonc  (safe to commit — env vars override per person/terminal)
{
  "userId": "usr_k3n9x2p",
  "room": "project-x",
  "role": "cto"
}
```

Now any terminal opened in that folder is already bob, in project-x, as cto — no exports:

```bash
lorewire register           # this terminal joins the room with your role
lorewire whoami             # shows effective identity/room/role + where each came from
```

**One identity, many terminals.** Each terminal you open becomes its own **session** under your identity, auto-named `bob~a1f` (derived from the terminal). So `bob` with 3 terminals = 3 sessions, and:

- `lorewire send --to bob "…"` reaches **all** of bob's terminals.
- `lorewire send --to bob~a1f "…"` targets one specific terminal.

**Resolution order** for every value: command flag → `$LOREWIRE_*` env → `.lorewire.jsonc` (nearest, walking up from the current dir) → built-in default. So the config is your baseline; export to override one terminal, pass a flag to override one command.

**Reusing an identity.** In another project folder, point it at the same identity instead of making a new one:

```bash
lorewire init --username bob        # writes a .lorewire.jsonc here with bob's userId
```

On a fresh machine (the committed config has a userId your local DB doesn't know yet), re-establish it:

```bash
lorewire user create bob --id usr_k3n9x2p
```

## Rooms, roles & secrets

**Rooms are optional.** Everything above happens in a default room called `main` — if you never mention a room, that's where you are. Rooms just scope conversations for a project or team. A session can be in **many rooms at once**, and picks the room per command via `--room`, else `$LOREWIRE_ROOM`, else `main`.

```bash
# alice spins up a room for project-x and takes the ceo role
export LOREWIRE_NAME=alice
lorewire join --room project-x --role ceo        # first joiner owns the room

# others join with their roles
export LOREWIRE_NAME=bob && lorewire join --room project-x --role cto
export LOREWIRE_NAME=carol && lorewire join --room project-x --role dev

lorewire members --room project-x                # see who's here and their roles
```

**Address a role, not a name.** An agent that needs "a frontend dev" doesn't need to know names — it messages the role:

```bash
lorewire send --room project-x --to @dev "who can take the login page?"   # → every dev
lorewire send --room project-x --to all "standup in 5"                     # → all members
lorewire send --room project-x --to bob "PR is ready"                      # → just bob
```

Set `export LOREWIRE_ROOM=project-x` once and you can drop `--room` entirely.

**No role?** Joining without `--role` lands you as `guest` — you can read and send, but you're flagged in `members` as needing a role. Any member can promote you: `lorewire role set dave qa --room project-x`. (Trust is cooperative/local for now — roles are coordination labels, not enforced permissions. Hard enforcement comes with a networked/multi-user mode.)

**Asking a role-holder for a secret (e.g. an API key).** This is the request/grant flow — an agent needs a key that only the CTO/CEO holds:

```bash
# carol (a dev) needs a key; she asks whoever holds the cto role
lorewire request --room project-x --to @cto "OpenAI API key for project-x"
#   → bob (cto) sees it in his inbox tagged [request#12]

# bob fulfills it — the value is delivered consume-once
lorewire grant 12 --secret "sk-..."
#   → carol reads it exactly once via `lorewire recv`; the row is then hard-deleted

# or bob declines
lorewire deny 12 "use the shared vault instead"
```

Secret payloads are **hard-deleted after a single `recv`**, so keys don't linger in message history. A non-consuming `lorewire inbox` peek shows secrets **masked** — only `recv` reveals (and consumes) them.

## Commands

**Identity & config**

| Command | What it does |
|---|---|
| `lorewire user create NAME [--id usr_…] [--room R] [--role X]` | Claim a username (mint or `--id`-import a userId); writes `./.lorewire.jsonc`. |
| `lorewire user list [--json]` | List users and their session counts. |
| `lorewire user rename OLD NEW` | Rename a username (userId unchanged; cascades to live session handles). |
| `lorewire init --username NAME \| --user usr_…` | Point this dir's `.lorewire.jsonc` at an existing identity. |
| `lorewire whoami [--json]` | Show effective userId/username/session/room/role — and the source of each. |

**Presence & rooms**

| Command | What it does |
|---|---|
| `lorewire register [--new]` | Register this terminal's session and join the configured room with your role. `--new` forces a fresh session handle. |
| `lorewire join --room ROOM [--role ROLE]` | Join a room (creating it if new — first joiner becomes owner). No `--role` → `guest`. |
| `lorewire leave [--room ROOM] [--purge]` | Leave one room. `--purge` also deletes that room's inbox for you. |
| `lorewire leave --all [--purge]` | Unregister from **every** room and remove the session (what the `SessionEnd` hook runs). |
| `lorewire prune [--older-than 30m]` | Janitor: remove sessions idle past the cutoff (and their memberships). Messages left intact. |
| `lorewire rooms [--json]` | List rooms with member counts and owner. |
| `lorewire members [--room ROOM] [--json]` | List a room's members and their roles. |
| `lorewire role set NAME ROLE [--room ROOM]` | Change a member's role. |
| `lorewire sessions [--json]` | List all live sessions and last-seen times. |

**Messaging** (room resolves: `--room` flag → `$LOREWIRE_ROOM` → `main`)

| Command | What it does |
|---|---|
| `lorewire send [--room ROOM] --to NAME\|@ROLE\|all\|SESSION MSG` | Send. `NAME` fans out to all that user's sessions in the room; `@ROLE` to everyone with that role; `all` to all members; `SESSION` (`bob~a1f`) to one terminal. `MSG` positional or `--msg`. |
| `lorewire recv [--room ROOM] [--json]` | Read **and consume** unread messages. Without `--room`, drains **all** your rooms at once. |
| `lorewire inbox [--room ROOM] [--all] [--json]` | Show messages without consuming. Secret bodies are masked here. |
| `lorewire watch [--room ROOM] [--interval 2s] [--json]` | Block and stream new messages as they arrive. |

**Requesting secrets** (ask whoever holds a role for something they have)

| Command | What it does |
|---|---|
| `lorewire request [--room ROOM] --to @ROLE\|NAME MSG` | Ask for something. Recipients see it tagged `[request#ID]`. |
| `lorewire grant ID --secret VALUE` | Answer a request with a secret, delivered **consume-once** (hard-deleted after one `recv`). |
| `lorewire deny ID REASON` | Decline a request; the requester is notified. |

Identity resolves as: `--user`/`--name` flag → `$LOREWIRE_USER_ID`/`$LOREWIRE_NAME` env → `.lorewire.jsonc` userId. Room resolves: `--room` → `$LOREWIRE_ROOM` → `.lorewire.jsonc` room → `main`. Role likewise via `$LOREWIRE_ROLE` / config → `guest`. Put them in a `.lorewire.jsonc` (see [Identity & config](#identity--config)) and drop the flags entirely.

For every flag, environment variable, addressing form, schema table, exit code, and more examples, see the **[complete reference manual](docs/REFERENCE.md)**.

## Push layer — auto-deliver into a session (optional)

`lorewire recv` is pull-based: a session sees messages when it checks. To make incoming messages appear **automatically**, wire the included `UserPromptSubmit` hook. Every time that session submits a prompt, pending messages are pulled and injected into its context.

1. Make sure `lorewire` is on your `PATH` (`task install`) and `LOREWIRE_NAME` is exported in the terminal before launching `claude`.
2. Add the hook to that session's Claude Code settings (`.claude/settings.json` in the project, or `~/.claude/settings.json`). See `hooks/settings.example.json`:

```json
{
  "hooks": {
    "UserPromptSubmit": [
      { "hooks": [ { "type": "command", "command": "/absolute/path/to/hooks/lorewire-incoming.sh" } ] }
    ],
    "SessionEnd": [
      { "hooks": [ { "type": "command", "command": "/absolute/path/to/hooks/lorewire-leave.sh" } ] }
    ]
  }
}
```

The hook (`hooks/lorewire-incoming.sh`) is a no-op when `LOREWIRE_NAME` is unset, so it's safe to enable globally. It consumes the messages it delivers (they show up in context instead of via a manual `lorewire recv`).

This is "push while the session is active" — the hook fires on each turn. It cannot wake a fully idle session; for that, keep a `lorewire watch` terminal open alongside.

## Leaving & cleanup

A session should unregister when it's done so it stops showing as active and stops receiving `--to all` broadcasts:

```bash
lorewire leave                 # unregister $LOREWIRE_NAME, keep its inbox
lorewire leave --purge         # also delete messages addressed to it
```

Leaving keeps the inbox by default — re-registering the same name resumes unread history. Messages the leaver *sent* to others are never deleted (they live in the recipients' inboxes).

For sessions that crash or exit without leaving, run the janitor:

```bash
lorewire prune --older-than 30m   # drop sessions not seen in 30 minutes
```

`prune` relies on the heartbeat that `register`/`send`/`recv`/`watch` refresh, so use a generous window — a live-but-silent session that never calls those could otherwise be pruned. Explicit `leave` is the reliable signal; `prune` is only the safety net.

**Auto-leave for Claude Code sessions:** the `SessionEnd` hook (`hooks/lorewire-leave.sh`, included in `hooks/settings.example.json`) runs `lorewire leave` when a session closes, so agents unregister themselves. Like the push hook, it's a no-op without `LOREWIRE_NAME`.

## Walkthrough — two real Claude Code sessions talking

This is the end-to-end "go-to" flow: two Claude Code sessions that message each other, with auto-delivery in and auto-unregister on exit.

**One-time setup:** install the CLI and wire both hooks into `~/.claude/settings.json` (merge with any existing `hooks`):

```bash
task install   # puts `lorewire` on your PATH
```

```json
{
  "hooks": {
    "UserPromptSubmit": [
      { "hooks": [ { "type": "command", "command": "/Volumes/D/khanakia/Downloads/claude_session_hub/hooks/lorewire-incoming.sh" } ] }
    ],
    "SessionEnd": [
      { "hooks": [ { "type": "command", "command": "/Volumes/D/khanakia/Downloads/claude_session_hub/hooks/lorewire-leave.sh" } ] }
    ]
  }
}
```

**Launch two sessions**, each with its own identity exported *before* `claude` starts:

```bash
# terminal 1
export LOREWIRE_NAME=alice
claude

# terminal 2
export LOREWIRE_NAME=bob
claude
```

**Drive it in plain English.** In alice's session, type:

> Register on lorewire and send bob a message asking him to take the frontend.

Alice's Claude runs `lorewire register` then `lorewire send --to bob "..."`. The next time **bob's** session takes a turn (type anything), the `UserPromptSubmit` hook injects alice's message into bob's context automatically — bob's Claude sees it and can reply with `lorewire send --to alice "..."`.

**Confirm auto-leave.** Close alice's session (Ctrl-C / exit). The `SessionEnd` hook fires `lorewire leave --all`. Now in bob's terminal:

```bash
lorewire sessions      # alice is gone; only bob remains
```

**Tips**
- Want a live feed instead of turn-based delivery? Open a third terminal: `export LOREWIRE_NAME=alice && lorewire watch` streams alice's inbox in real time.
- Reset everything: `task reset` (wipes `~/.lorewire`).
- Debug a session's inbox without consuming: `lorewire inbox --all`.

## Storage

- Default database: `~/.lorewire/lorewire.db` (override with `$LOREWIRE_DB`).
- Tables: `users` (`user_id`,`username`), `sessions` (one per terminal, owned by a user, with `cwd`/`tty`/`pid`/`host`/`client` + a `meta` JSON blob), `rooms`, `members` (`room`,`session_id`,`owner_id`,`role`), `messages` (room-scoped, `from_id`/`to_id` are session ids, with `kind` and `read_at`).
- An incompatible pre-identity database is moved aside to `lorewire.db.bak-<timestamp>` (never silently wiped) and a fresh one is created.
- WAL journal mode so readers proceed during writes; `busy_timeout` + retry-on-lock for cross-process contention.
- A broadcast or `@role` send fans out at send time into one row per recipient, each with its own read watermark, so `recv` stays a simple "unread rows for me" query and consume-once holds even when two `recv` calls race.
- Secret messages are deleted (not just marked read) on `recv`, so a key survives exactly one read.

## Development

```bash
task build          # build ./lorewire
task test           # run all e2e scenarios
task test-e2e       # flat-mode scenario (direct, broadcast, concurrency)
task test-rooms     # rooms/roles/@role/request-grant scenario
task test-identity  # users, sessions, .lorewire.jsonc, precedence, rename
task demo           # register alice/bob/carol in the default DB and list sessions
task reset          # delete the default database
```

## FAQ

**Does lorewire send my data to the cloud?** No. Everything lives in a single local SQLite file (`~/.lorewire/lorewire.db`). There is no server, no network calls, and no telemetry — sessions coordinate entirely through that shared file on your machine.

**Does it need an API key or account?** No. lorewire is a plain CLI. Install it, run it, done. (It *helps agents pass* API keys around, but it doesn't require one itself.)

**Do I have to use rooms?** No. Rooms are optional. If you never mention a room, everything happens in a default room called `main`, and the tool behaves like a flat message bus. Rooms are additive scoping for when you want to separate projects or teams.

**Does it only work with Claude Code?** No — it's agent-agnostic. Any process that can run a shell command can join the wire: other CLI agents, shell scripts, cron jobs, or a human in a terminal. Claude Code is just the first integration, via an optional hook that auto-delivers messages into a session.

**How is this different from sharing a file or using `tmux send-keys`?** Ad-hoc files have no addressing, no atomic consume, and race under concurrency; `send-keys` blindly types into another pane with no inbox or delivery guarantees. lorewire gives you named/role/broadcast addressing, rooms, consume-once semantics, and concurrency-safe delivery, with none of the glue code.

**Is it safe to send API keys through it?** Secrets use a dedicated request/grant flow: the payload is delivered **consume-once** (hard-deleted after a single `recv`) and **masked** in non-consuming `inbox` peeks, so keys don't linger in message history. Note the store is a local plaintext SQLite file, so it's meant for cooperative sessions on your own machine, not as a hardened multi-user secrets vault.

**Will it bloat my agent's context?** No. Delivery is pull-based, and the optional push hook injects only *pending* messages on a turn, then consumes them — so nothing accumulates in context.

**What happens if a session crashes without unregistering?** Run `lorewire prune --older-than 30m` to remove stale sessions, or wire the `SessionEnd` hook so sessions auto-unregister with `lorewire leave --all` when they close.

**How many sessions can talk at once?** It's designed for 2–3 coordinating agent sessions, but there's no hard limit — concurrent sends and reads are serialized safely through SQLite.

## Documentation

- **[Complete reference manual](docs/REFERENCE.md)** — every command, flag, environment variable, config key, addressing form, hook, schema table, exit code, and recipe.
- [PLAN.md](PLAN.md) — design decisions and rationale for the identity/config layer.
- `hooks/settings.example.json` — ready-to-merge Claude Code hook config.

## The lore family

- **lore** — local-first memory / context for agents.
- **lorewire** — this: inter-agent messaging.

Sibling names (`lorenet`, `lorelink`, `lorebus`, …) are reserved-by-availability for future family tools.

<sub>lorewire — open-source, local-first message bus and IPC for AI coding agent sessions (Claude Code and other agents). Multi-agent communication with rooms, roles, broadcast, and consume-once secret delivery. Single Go binary, SQLite-backed, no server, no cloud, no API key.</sub>
