# Glossary

Quick definitions of every lorewire term. For the full picture see [ARCHITECTURE.md](ARCHITECTURE.md) and [SESSIONS.md](SESSIONS.md).

**user** — A stable identity: a `userId` plus a `username`. One user owns many sessions. Stored in the `users` table.

**userId** — The immutable, unique id for a user: `usr_` + a 7-character nanoid (e.g. `usr_k3n9x2p`). The anchor everything references; survives a username rename. Generated with `crypto/rand`.

**username** — The human-readable, unique name for a user (`alice`, `bob`). Renameable via `lorewire user rename`; the rename cascades into that user's session ids.

**session** — One open terminal or agent run, owned by a user. A user's three terminals are three sessions. Stored in the `sessions` table, keyed by session id.

**session id** — A session's handle: `username` + `~` + an 8-hex fingerprint of the terminal token, e.g. `bob~50fe752f`. Human-readable prefix, stable per terminal/agent.

**terminal token** — The raw value hashed into a session id. Resolved tool-agnostically: explicit override → agent session-id env var → controlling tty → parent pid. See [SESSIONS.md](SESSIONS.md).

**id_source** — A label recorded on each session saying where its id was derived from: `agent:CLAUDE_CODE_SESSION_ID`, `env:LOREWIRE_SESSION_TOKEN`, `tty:/dev/tty`, `ppid`, … Shown by `whoami` ("id from") and `sessions --json`. For debugging.

**room** — A named channel that scopes messages. Default is `main`, so rooms are optional. A session can be in many rooms. Stored in `rooms`.

**member / membership** — The record that a session belongs to a room, with a role there. One row per session per room, in `members`. You must be a member of a room to receive its messages.

**role** — A label a session holds in a room (`ceo`, `cto`, `dev`, `guest`, …). Used for `@role` addressing and coordination. No-role joins default to `guest`. Roles are cooperative labels, not enforced permissions.

**owner (of a room)** — The user that created the room (first joiner). `main` is system-owned.

**message** — One delivered row addressed to a single recipient session. Broadcasts/`@role`/username sends fan out into one message per recipient at send time. Stored in `messages`.

**kind** — A message's type: `msg` (normal), `request`, `secret`, `deny`. Drives display and delivery semantics.

**broadcast** — `--to all` (or `*`): deliver to every member of the room.

**@role addressing** — `--to @dev`: deliver to every member of the room holding that role.

**fan-out** — Expanding one logical send (a username, a role, `all`) into one stored message per concrete recipient session, at send time.

**consume-once** — Secret delivery semantics: a `secret` message is **hard-deleted** after a single `recv`, so a key survives exactly one read. Masked in `inbox` peeks.

**request / grant / deny** — The secret flow. `request --to @role` asks; the holder runs `grant ID --secret …` (consume-once) or `deny ID reason`.

**quick mode** — Using lorewire with no config file: set `$LOREWIRE_NAME=alice` and go; the user is auto-created. For quick tests.

**.lorewire.jsonc** — The project-local config (JSONC, comments allowed) holding `userId`, `username`, `room`, `role`. Found by walking up from the current directory. Committed; env/flags override.

**import** — `lorewire import`: re-create the identity described by the local `.lorewire.jsonc` in this machine's database (fresh-machine / post-wipe path). Idempotent.

**prune** — `lorewire prune`: janitor that removes sessions idle past a cutoff (for crashed terminals).

**reset** — `lorewire reset sessions|messages|all`: delete sessions / messages / everything, with a `--yes` confirmation gate.

**global vs session commands** — Global commands (`sessions`, `rooms`, `members`, `user list`, `prune`, `reset`) read the shared DB and work from any directory. Session commands (`send`, `recv`, `whoami`, `register`, …) act as *you*, using the current folder's identity.

**hook** — A small script wired into an agent's lifecycle. lorewire ships three for Claude Code: register (SessionStart), incoming/recv (UserPromptSubmit), leave (SessionEnd). See [INTEGRATIONS.md](INTEGRATIONS.md).
