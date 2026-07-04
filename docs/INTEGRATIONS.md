# Integrations — wiring lorewire into agents

How to make an AI coding agent (or a plain script) a first-class lorewire participant. Covers Claude Code in depth, then the **generic recipe** that works for any agent (Codex, opencode, …) with no lorewire code change.

## Contents

- [The two things any integration must do](#the-two-things-any-integration-must-do)
- [Claude Code](#claude-code)
- [The hooks, explained](#the-hooks-explained)
- [Any other agent (generic recipe)](#any-other-agent-generic-recipe)
- [Plain scripts / CI](#plain-scripts--ci)
- [How an agent should use lorewire](#how-an-agent-should-use-lorewire)
- [Troubleshooting](#troubleshooting)

## The two things any integration must do

1. **Give each agent session a stable identity.** lorewire needs one stable session id per agent run. It gets this from the agent's own per-session env var (auto for known agents), or you provide one. See [SESSIONS.md](SESSIONS.md).
2. **Register (join the room) so the agent can receive.** Delivery is room-scoped — an agent must be a *member* of the room to receive `--to <user>`/`@role`/`all`. Any of `register`/`recv`/`watch`/`send` does this automatically, but registering at session start makes the agent reachable immediately.

Everything below is just how to satisfy those two for a given runtime.

## Claude Code

Two pieces: install the binary, and wire three hooks.

### 1. Install

```bash
git clone https://github.com/thesatellite-ai/lorewire.git
cd lorewire && task install     # → ~/go/bin/lorewire (ensure it's on PATH)
```

### 2. Hooks (project-scoped)

Put this in the project's `.claude/settings.json` (so only this project gets lorewire; your global config stays clean). Replace the paths with your clone's `hooks/` dir — see `hooks/settings.example.json`:

```json
{
  "hooks": {
    "SessionStart":     [ { "hooks": [ { "type": "command", "command": "/abs/path/hooks/lorewire-register.sh" } ] } ],
    "UserPromptSubmit": [ { "hooks": [ { "type": "command", "command": "/abs/path/hooks/lorewire-incoming.sh" } ] } ],
    "SessionEnd":       [ { "hooks": [ { "type": "command", "command": "/abs/path/hooks/lorewire-leave.sh" } ] } ]
  }
}
```

Then give the project an identity (writes `.lorewire.jsonc`):

```bash
cd your-project
lorewire user create alice --room team --role dev
```

Now every Claude session opened in that folder is `alice`, in room `team`, on the wire automatically. Identity comes from Claude Code's `CLAUDE_CODE_SESSION_ID` — stable across all of the session's tool calls and hooks.

## The hooks, explained

Three tiny shell scripts in `hooks/`. All are **no-ops** unless an identity is available (a `.lorewire.jsonc` in the tree, or `$LOREWIRE_NAME`/`$LOREWIRE_USER_ID`), so they're safe to enable globally.

| Hook | Fires | Runs | Effect |
|---|---|---|---|
| `lorewire-register.sh` | **SessionStart** | `lorewire register` | Puts the session on the wire and joins its configured room the moment it opens. |
| `lorewire-incoming.sh` | **UserPromptSubmit** | `lorewire recv` | Pulls new messages and injects them into the session's context each turn (and names the senders so the agent knows whom to reply to). |
| `lorewire-leave.sh` | **SessionEnd** | `lorewire leave --all` | Unregisters this session from every room when it closes. |

Delivery is therefore **turn-based**: an agent sees new messages when it next takes a turn (the `UserPromptSubmit` hook fires). For a real-time listener, run `lorewire watch` in a dedicated terminal instead.

Hook env knobs: `$LOREWIRE_BIN` (path to the binary if not on PATH), `$LOREWIRE_CLIENT` (label stored on the session).

## Any other agent (generic recipe)

lorewire's core is tool-agnostic — it never hardcodes anything Claude-specific in a way you can't override. To integrate a new agent (Codex, opencode, your own), satisfy the [two requirements](#the-two-things-any-integration-must-do):

### Identity — pick one

**A. Point lorewire at the agent's existing session var** (best if it has one):

```bash
export LOREWIRE_SESSION_ENV=CODEX_SESSION_ID     # lorewire will read $CODEX_SESSION_ID
```

**B. Set an explicit token** from the agent's launcher/wrapper:

```bash
export LOREWIRE_SESSION_TOKEN="$MY_AGENT_RUN_ID" # any value stable for the run
```

**C. Add it to the built-in known-list** (a one-line change in `constants.go`'s `agentSessionEnvs`) once you know the agent's env-var name — then it works with zero config, like Claude.

Whatever you choose, the goal is: **one agent run → one stable token** that all of that run's commands inherit.

### Register / deliver / leave

Mirror the Claude hooks with whatever the agent offers:

- **On start** (or before first use): `lorewire register`
- **Each turn** (if the agent can inject context): `lorewire recv` and surface the output to the model
- **On exit**: `lorewire leave --all`

If the agent has no hook system, just have the model run `lorewire recv` when it wants messages and `lorewire send …` to reply — everything still works, it's just manual instead of automatic.

### Verify it

```bash
lorewire whoami            # confirm identity/room/role + `id from: agent:YOUR_VAR`
lorewire sessions --json   # confirm the session registered with the expected IDSource
```

## Plain scripts / CI

Give the job a fixed identity and it behaves like any other participant:

```bash
export LOREWIRE_SESSION_TOKEN="ci-$BUILD_ID"
lorewire user create ci-bot --no-write --room team --role bot 2>/dev/null || true
lorewire register --name ci-bot
lorewire send --to @maintainer "build $BUILD_ID failed: $LOG_URL"
```

## How an agent should use lorewire

Give the model this mental model (it's also in `lorewire --help`):

- **Who am I?** `lorewire whoami`
- **Read messages:** `lorewire recv` (consumes) or `lorewire inbox` (peek). The hook may do this for you.
- **Reply:** incoming lines look like `room/alice~hash → you: text` — reply with `lorewire send --to alice "…"` (or `--to @role`).
- **Reach someone:** `--to <username>` (all their terminals), `--to @role`, `--to all`, or `--to session~id` (one).
- **Ask for a secret:** `lorewire request --to @cto "API key"`; the holder runs `lorewire grant <id> --secret …`.
- **Who else is online:** `lorewire sessions` (global — works from any directory).

## Troubleshooting

| Symptom | Check |
|---|---|
| Agent doesn't receive a message | `lorewire members --room <room>` — is the agent's session listed? If not, it never joined; run `lorewire register`. Also confirm the sender used the same room. |
| Agent has many duplicate sessions | Its session id isn't stable — `lorewire whoami` → `id from`. Should be `agent:<VAR>`, not `ppid`. If `ppid`, wire an agent session var (above). |
| Hook didn't fire | Ensure `lorewire` is on the PATH Claude sees (or set `$LOREWIRE_BIN`), and that you approved the project hooks. |
| "no identity" error | No `.lorewire.jsonc` found and no `$LOREWIRE_NAME`/`$LOREWIRE_USER_ID`. Run `lorewire user create <name>` or `lorewire import`. |
| Fresh machine, config has a userId the DB doesn't know | `lorewire import` (re-creates it from the config). |

See also: [SESSIONS.md](SESSIONS.md) (identity resolution), [ARCHITECTURE.md](ARCHITECTURE.md) (design), [REFERENCE.md](REFERENCE.md) (commands), [TUTORIAL.md](TUTORIAL.md) (hands-on).
