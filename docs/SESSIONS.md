# How lorewire figures out "which session am I?"

This doc explains, in plain language, how lorewire decides your **session id** — the thing that makes messages land in the right terminal. If `bob~50fe752f` ever made you go "wait, where did *that* come from?", this is for you.

## The 30-second version

Every time you run a `lorewire` command it is a brand-new, short-lived program — it starts, does one thing, and exits. There is no long-running "session" process holding your identity. So on every run, lorewire has to re-answer one question:

> **"Which terminal or agent am I part of right now?"**

To answer it, lorewire looks for a **fingerprint** — a value that is the *same* every time within one terminal/agent, but *different* across terminals/agents. It turns that fingerprint into your session id. That's the whole idea.

Two everyday cases cover almost everything:

- **A normal terminal** → the fingerprint is the terminal window itself (its tty device).
- **An AI agent (Claude Code, Codex, …)** → the fingerprint is the agent's own session id, which it puts in an environment variable.

You configure nothing. lorewire auto-detects.

## What a session id looks like

```
bob~50fe752f
└─┬─┘ └───┬──┘
 user    an 8-char fingerprint of this terminal/agent
```

- The part **before** `~` is your **username** — human-readable, so you can tell whose session it is at a glance.
- The part **after** `~` is a short **hash** of the fingerprint (see [The hash](#the-hash-why-its-hashed) below).

One person (**user**) can have many **sessions** — one per terminal/agent. `bob~50fe752f` and `bob~1a2b3c4d` are the same person on two different terminals.

## The fingerprint: where lorewire looks (the ladder)

lorewire tries these sources **top to bottom and stops at the first one it finds**. You almost never touch the top or bottom — the middle two do the work.

| # | Source | When it's used |
|---|---|---|
| 1 | `$LOREWIRE_SESSION` | You set the *entire* session id yourself. Rare — full manual control. |
| 2 | `$LOREWIRE_SESSION_TOKEN` | You give an explicit token to fingerprint. Handy in scripts/tests. |
| 3 | `$LOREWIRE_SESSION_ENV` | You name *another* env var that holds a session id (for a new agent). |
| 4 | **built-in agent list** | An agent's own session var (`CLAUDE_CODE_SESSION_ID`, …). **← agents land here** |
| 5 | **controlling tty** | A real terminal's device. **← plain terminals land here** |
| 6 | parent pid | Last resort if nothing above exists. |

The two that matter in daily use are **#4 (agents)** and **#5 (terminals)**. The rest are overrides.

### Why agents can't just use the terminal (#5)

Inside an agent, when it runs `lorewire`, the command's input is piped and there is often **no real terminal attached** (`/dev/tty` returns "device not configured"). So the tty trick is unreliable there. But every serious agent gives its subprocesses a **stable per-session environment variable** that all of them inherit — the agent's own session id. Claude Code sets `CLAUDE_CODE_SESSION_ID`. lorewire reads that (source #4), so **every command and every hook in one agent session maps to one lorewire session.** That is what makes agent-to-agent messaging reliable.

## The hash: why it's hashed

lorewire doesn't put the raw fingerprint into the id — it hashes it and keeps 8 hex characters. Two reasons:

1. **Readability.** A Claude session id is a long UUID like `aa23d6b6-4d47-482b-9414-c7cf3403fa33`. `bob~aa23d6b6-4d47-482b-9414-c7cf3403fa33` would be unwieldy. `bob~50fe752f` is tidy.
2. **Stability.** The hash is deterministic — the same fingerprint always produces the same 8 characters, so all of that session's commands agree.

Here's the exact math (you can run it yourself):

```bash
SID="aa23d6b6-4d47-482b-9414-c7cf3403fa33"          # Claude's session id
printf '%s' "agent-$SID" | shasum -a 256 | cut -c1-8   # → 50fe752f
# session id = bob~50fe752f
```

**Is 8 characters enough?** Yes. That's 32 bits = ~4.3 billion values, and collisions only matter *within one user's* sessions (different usernames can never collide — the username is the prefix). Even 1,000 simultaneous sessions of a single user gives roughly a 0.01% collision chance. In practice it's effectively zero.

## Seeing where your id came from (debugging)

Every session records its source in an `id_source` field. Two ways to read it:

```bash
lorewire whoami
# ...
# session  : bob~50fe752f
#   id from  : agent:CLAUDE_CODE_SESSION_ID     ← this line
# ...

lorewire sessions --json
# [ { "ID": "bob~50fe752f", "IDSource": "agent:CLAUDE_CODE_SESSION_ID", ... } ]
```

Typical `id_source` values:

| Value | Meaning |
|---|---|
| `agent:CLAUDE_CODE_SESSION_ID` | keyed off Claude Code's session id |
| `agent:CODEX_SID` | keyed off a var you named via `LOREWIRE_SESSION_ENV` |
| `env:LOREWIRE_SESSION_TOKEN` | you supplied an explicit token |
| `tty:/dev/tty` | a real terminal's device |
| `ppid` | last-resort fallback |

If two things that should be one session show up as two, `id_source` tells you why in seconds.

## Use cases & examples

### 1. A person in a normal terminal

Nothing to configure. Open a terminal, and every command there is one session (keyed off the tty).

```bash
cd ~/work/project-x
lorewire register          # session e.g. bob~9c2f1a7e   (id from: tty:/dev/tty)
lorewire send --to alice "hi"
lorewire whoami            # confirms the same session id every time
```

Open a **second** terminal window → different tty → a **second** session (`bob~<other>`). Both belong to bob; `--to bob` reaches both.

### 2. A Claude Code agent (zero config)

The `.claude/settings.json` hooks (or the agent itself) run lorewire, and it keys off `CLAUDE_CODE_SESSION_ID` automatically.

```bash
# inside the agent, all of these resolve to ONE stable session:
lorewire register
lorewire recv
lorewire send --to alice "on it"
# id from: agent:CLAUDE_CODE_SESSION_ID
```

Because the id is stable, the agent stays registered and reachable — `--to bob` from alice finds it.

### 3. A different agent (Codex, opencode, anything)

Two ways, no lorewire code change:

**A — point lorewire at the agent's own session var:**

```bash
export LOREWIRE_SESSION_ENV=CODEX_SESSION_ID     # the agent already sets CODEX_SESSION_ID
lorewire register                                 # id from: agent:CODEX_SESSION_ID
```

**B — set the token explicitly** (e.g. from the agent's launcher/wrapper):

```bash
export LOREWIRE_SESSION_TOKEN="$MY_AGENT_RUN_ID"
lorewire register                                 # id from: env:LOREWIRE_SESSION_TOKEN
```

Either way, one agent run = one lorewire session.

### 4. One user, many terminals (fan-out)

```bash
# terminal 1:  bob~aaaa1111
# terminal 2:  bob~bbbb2222
lorewire sessions --me      # shows both of bob's sessions
# alice runs:
lorewire send --to bob "ping"   # delivered to BOTH bob~aaaa1111 and bob~bbbb2222
```

Send to one specific terminal with the full session id: `--to bob~aaaa1111`.

### 5. A script / CI job

Give it a fixed, explicit identity so it's predictable:

```bash
export LOREWIRE_SESSION_TOKEN="ci-build-$BUILD_ID"
lorewire register --name ci-bot
lorewire send --to @maintainer "build $BUILD_ID failed"
```

### 6. Force a fresh session in the same place

Same terminal, but you want a distinct session (e.g. testing two peers side by side):

```bash
lorewire register --new     # appends a random suffix so this terminal gets a new id
```

## Frequently confusing bits

**"Who set `CLAUDE_CODE_SESSION_ID`?"** Claude Code did — automatically, for its own session. lorewire only *reads* it; it never sets it.

**"Why did the UUID `aa23d6b6-…` become `50fe752f`?"** It's the first 8 hex of `sha256("agent-aa23d6b6-…")`. Same input → same output, every time. See [the math](#the-hash-why-its-hashed).

**"My agent and my normal terminal show different bob sessions — bug?"** No. They're genuinely different places (an agent vs a terminal window), so they're different sessions of the same user bob. `--to bob` reaches both (once both are registered).

**"Two of my sessions have the same id!"** Extremely unlikely (8-hex, per-user). If it ever happens, use `lorewire register --new` in one of them to re-roll.

**"How do I make lorewire work with a brand-new agent?"** Set `LOREWIRE_SESSION_ENV=<its session var>` or `LOREWIRE_SESSION_TOKEN=<a stable per-run value>` in that agent's environment. Nothing else.

## One-line mental model

> Each `lorewire` command asks "who am I?" and answers with a fingerprint — an **agent's session id** (for agents) or the **terminal device** (for humans) — hashed into `username~xxxxxxxx`, so every command from the same place shares one session.

See also: the [complete reference](REFERENCE.md) and the [tutorial](TUTORIAL.md).
