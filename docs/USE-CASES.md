# Use cases — a step-by-step cookbook

Real scenarios, each worked start-to-finish with the exact commands and the output you should see. These use **plain terminals** so you can run and verify them yourself; each recipe ends with a note on how it maps to the **AI-agent** flow (where the hooks do these steps for you).

Session-id suffixes (the `~a1b2` part) vary per terminal/agent — yours will differ; that's expected.

## Contents

- [Setup (once)](#setup-once)
- [1. Two peers, one direct message](#1-two-peers-one-direct-message)
- [2. A project team with roles](#2-a-project-team-with-roles)
- [3. Hand off a task by role](#3-hand-off-a-task-by-role)
- [4. Ask a role-holder for a secret (API key)](#4-ask-a-role-holder-for-a-secret-api-key)
- [5. Broadcast a standup to everyone](#5-broadcast-a-standup-to-everyone)
- [6. One orchestrator, many workers](#6-one-orchestrator-many-workers)
- [7. One person, many terminals (fan-out)](#7-one-person-many-terminals-fan-out)
- [8. One identity across two projects](#8-one-identity-across-two-projects)
- [9. A CI bot that pings a maintainer](#9-a-ci-bot-that-pings-a-maintainer)
- [10. Move to a new machine (import)](#10-move-to-a-new-machine-import)
- [11. Inspect & clean up](#11-inspect--clean-up)
- [Mapping to AI agents](#mapping-to-ai-agents)

## Setup (once)

Install the CLI:

```bash
git clone https://github.com/thesatellite-ai/lorewire.git
cd lorewire && task install     # → ~/go/bin/lorewire (ensure it's on PATH)
lorewire --help                 # sanity check
```

Every recipe below uses **two or three terminals**. Where a recipe needs an identity, we use one of two styles:

- **Quick mode** — `export LOREWIRE_NAME=alice` (lorewire auto-creates the user). Fastest for trying things.
- **Config mode** — a `.lorewire.jsonc` in a folder (via `lorewire user create`). Best for real, ongoing use.

## 1. Two peers, one direct message

**Goal:** alice sends bob a message; bob reads it and replies.

**Terminal 1 (alice):**

```bash
export LOREWIRE_NAME=alice
lorewire register
#   registered session alice~a1b2 (user alice) — created + joined room "main" as guest
```

**Terminal 2 (bob):**

```bash
export LOREWIRE_NAME=bob
lorewire register
#   registered session bob~c3d4 (user bob) — joined room "main" as guest
```

**Terminal 1 (alice) — send:**

```bash
lorewire send --to bob "can you review PR #42?"
#   sent to bob~c3d4 in room "main"
```

**Terminal 2 (bob) — read + reply:**

```bash
lorewire recv
#   [14:02:11] main/alice~a1b2 → bob~c3d4: can you review PR #42?
lorewire send --to alice "on it — looking now"
#   sent to alice~a1b2 in room "main"
```

**What happened:** both joined the default room `main` on `register`. `--to bob` found bob's session in `main` and delivered one message. `recv` read and consumed it (running `recv` again shows `(no new messages)`).

## 2. A project team with roles

**Goal:** set up a `project-x` room where people have roles you can address.

Use config folders so identities persist. In three folders (one per person):

```bash
mkdir -p ~/team/alice ~/team/bob ~/team/carol

( cd ~/team/alice && lorewire user create alice --room project-x --role cto )
( cd ~/team/bob   && lorewire user create bob   --room project-x --role dev )
( cd ~/team/carol && lorewire user create carol --room project-x --role dev )
```

Each `user create` writes a `.lorewire.jsonc` and prints e.g. `user "alice" → usr_…  /  wrote …/.lorewire.jsonc`.

Now register each (from its folder — no env vars needed):

```bash
( cd ~/team/alice && lorewire register )   # created + joined room "project-x" as cto
( cd ~/team/bob   && lorewire register )   # joined room "project-x" as dev (owner alice)
( cd ~/team/carol && lorewire register )
```

See the room:

```bash
lorewire members --room project-x
#   room "project-x":
#     alice~a1b2          cto       (alice)
#     bob~c3d4            dev       (bob)
#     carol~e5f6          dev       (carol)
```

**What happened:** `project-x` was created by its first joiner (alice, who owns it). Roles are set from each folder's config.

## 3. Hand off a task by role

**Goal:** alice (cto) assigns work to whoever is a `dev`, without naming them.

**From alice's folder:**

```bash
cd ~/team/alice
lorewire send --room project-x --to @dev "please take the login page"
#   sent to bob~c3d4, carol~e5f6 in room "project-x"
```

**Any dev reads it:**

```bash
cd ~/team/bob && lorewire recv
#   [14:10:03] project-x/alice~a1b2 → bob~c3d4: please take the login page
lorewire send --room project-x --to alice "I'll take it — bob"
```

**What happened:** `@dev` fanned out to *both* devs (bob and carol). Either can claim it and reply. (If you set `export LOREWIRE_ROOM=project-x` per terminal, you can drop `--room`.)

## 4. Ask a role-holder for a secret (API key)

**Goal:** a dev needs the staging API key; the cto grants it, delivered consume-once.

**From carol (dev):**

```bash
cd ~/team/carol
lorewire request --room project-x --to @cto "need the staging API key"
#   requested from alice~a1b2 in room "project-x" — they answer with `lorewire grant ID --secret ...`
```

**alice (cto) sees the request:**

```bash
cd ~/team/alice && lorewire recv
#   [14:15:20] project-x/carol~e5f6 → alice~a1b2 [request#7]: need the staging API key
lorewire grant 7 --secret "sk-staging-abc123"
#   granted request #7 — secret delivered to carol~e5f6 in room "project-x" (consume-once)
```

**carol reads it — once:**

```bash
cd ~/team/carol
lorewire inbox --all        # peek: the secret body is MASKED
#   [14:15:22] project-x/alice~a1b2 → carol~e5f6 [secret]: <secret — use `lorewire recv` to consume>
lorewire recv               # reveal — and it's gone after this
#   [14:15:22] project-x/alice~a1b2 → carol~e5f6 [secret]: sk-staging-abc123
lorewire recv               # again → (no new messages)
```

**What happened:** the secret was a consume-once message — masked in `inbox`, revealed exactly once by `recv`, then hard-deleted. To decline instead, alice would run `lorewire deny 7 "use the vault"`.

## 5. Broadcast a standup to everyone

**Goal:** message every member of the room at once.

```bash
cd ~/team/alice
lorewire send --room project-x --to all "standup in 5 — drop your status"
#   sent to bob~c3d4, carol~e5f6 in room "project-x"
```

Each member reads with `lorewire recv` and replies `--to alice` (or `--to @cto`).

**What happened:** `--to all` delivered one copy to every *other* member (the sender is excluded).

## 6. One orchestrator, many workers

**Goal:** a lead assigns tasks to a pool of workers and collects results — the multi-agent pattern.

Set up a room `build` with a lead and two workers (quick mode this time):

```bash
# Terminal 1 (lead)
export LOREWIRE_NAME=lead LOREWIRE_ROOM=build
lorewire join --room build --role lead

# Terminal 2 (worker A)
export LOREWIRE_NAME=worker-a LOREWIRE_ROOM=build
lorewire join --room build --role worker

# Terminal 3 (worker B)
export LOREWIRE_NAME=worker-b LOREWIRE_ROOM=build
lorewire join --room build --role worker
```

**Lead dispatches to the worker pool:**

```bash
# Terminal 1
lorewire send --to @worker "task: run the integration tests and report pass/fail"
#   sent to worker-a~…, worker-b~… in room "build"
```

**Each worker does the job and reports back to the lead:**

```bash
# Terminal 2
lorewire recv
#   […] build/lead~… → worker-a~…: task: run the integration tests and report pass/fail
lorewire send --to lead "worker-a: 142 passed, 0 failed"
```

**Lead collects results:**

```bash
# Terminal 1
lorewire recv
#   […] build/worker-a~… → lead~…: worker-a: 142 passed, 0 failed
#   […] build/worker-b~… → lead~…: worker-b: done, 1 flaky test retried
```

**What happened:** `@worker` addressed the whole pool by role; workers replied directly to `lead`. This is exactly how you'd coordinate several agent sessions — one orchestrator, N workers.

## 7. One person, many terminals (fan-out)

**Goal:** understand how a message reaches *all* of a user's terminals, and how to target just one.

Open **two** terminals as the same user, in the same room. To simulate two distinct sessions from a script you can set `LOREWIRE_SESSION_TOKEN`; in real life just open two terminal windows.

```bash
# Terminal 1
export LOREWIRE_NAME=dan LOREWIRE_ROOM=build
lorewire register        # session dan~1111

# Terminal 2
export LOREWIRE_NAME=dan LOREWIRE_ROOM=build
lorewire register        # session dan~2222

lorewire sessions --me
#   dan (usr_…)
#     dan~1111   …   seen just now
#     dan~2222   …   seen just now
```

**Someone sends to the user:**

```bash
lorewire send --to dan "ping"      # from another session
#   sent to dan~1111, dan~2222 in room "build"   ← both terminals
```

**To reach exactly one terminal, use its full session id:**

```bash
lorewire send --to dan~1111 "just terminal 1"
#   sent to dan~1111 in room "build"
```

**What happened:** `--to dan` (a username) fans out to *all* of dan's sessions in the room; `--to dan~1111` (a session id, note the `~`) targets one.

## 8. One identity across two projects

**Goal:** be the same person (`bob`) in two different project folders/rooms.

You already have bob in `~/team/bob` (room `project-x`). Point a second folder at the *same* identity:

```bash
mkdir -p ~/other-project
cd ~/other-project
lorewire init --username bob --room ops --role sre
#   wrote …/other-project/.lorewire.jsonc (userId usr_…, username bob)
```

Now bob is `dev` in `project-x` **and** `sre` in `ops` — the same underlying identity in two rooms:

```bash
cd ~/other-project && lorewire register    # joins room "ops" as sre
lorewire rooms --me
#   ops         …   (you're a member)
#   project-x   …   (you're a member)
```

Send into whichever room you're standing in, or override per command:

```bash
lorewire send --to @lead "ops question"                 # uses this folder's room (ops)
lorewire send --room project-x --to alice "PR is up"    # override → project-x
```

**What happened:** `init` reused bob's existing `userId` in a new folder with a different room/role. One identity, many rooms.

## 9. A CI bot that pings a maintainer

**Goal:** a non-interactive job posts a message.

```bash
export LOREWIRE_SESSION_TOKEN="ci-$BUILD_ID"     # stable id for this run
export LOREWIRE_NAME=ci-bot LOREWIRE_ROOM=team
lorewire send --to @maintainer "build $BUILD_ID FAILED — $LOG_URL"
```

**What happened:** the bot got a fixed identity/session and addressed maintainers by role. A maintainer's `recv` (or their agent's hook) surfaces it.

## 10. Move to a new machine (import)

**Goal:** you cloned a repo whose committed `.lorewire.jsonc` has a `userId` this machine's DB doesn't know yet.

```bash
cd ~/team/alice     # freshly cloned; DB here has no "alice" yet
lorewire whoami
#   error: userId "usr_…" is not in this database — run `lorewire user create <name> --id usr_…` to import it…
lorewire import
#   imported "alice" (usr_…) from …/.lorewire.jsonc — ready to use here
lorewire register   # now works
```

**What happened:** `import` re-created alice's identity from the config's `userId` + `username`. Idempotent — running it again says "already imported".

## 11. Inspect & clean up

**See everything (from any directory — these are global):**

```bash
lorewire sessions          # all live sessions, grouped by user, with cwd/branch
lorewire user list         # all users + session counts
lorewire rooms             # all rooms + member counts
lorewire members --room project-x
lorewire whoami            # who am I here + this terminal's full session detail + `id from`
```

**Clean up:**

```bash
lorewire leave --all               # unregister the current terminal (its SessionEnd hook does this for agents)
lorewire prune --older-than 30m    # remove sessions idle > 30m (crashed terminals)
lorewire reset sessions --user bob # preview removing just bob's sessions
lorewire reset sessions --user bob --yes
lorewire reset all --yes           # wipe everything (then re-`import` identities)
```

## Mapping to AI agents

Every recipe above is a plain-terminal version of what agents do automatically. With the Claude Code hooks wired ([INTEGRATIONS.md](INTEGRATIONS.md)):

- **`register`** happens on `SessionStart` — the agent is on the wire the moment it opens.
- **`recv`** happens on every turn (`UserPromptSubmit`) — incoming messages are injected into the agent's context, with the sender named.
- **`leave --all`** happens on `SessionEnd`.
- **`send` / `request` / `grant`** — the agent runs these itself when you (or another agent) ask it to, e.g. *"send bob a message asking him to take the login page"* → `lorewire send --to bob "…"`.

So the two-agent version of recipe 1 is: open Claude in alice's folder and bob's folder, tell alice *"message bob to review PR #42"*, and bob sees it on his next turn. Same commands — just driven by the model instead of typed by you.

See also: [TUTORIAL.md](TUTORIAL.md) (the guided first run), [REFERENCE.md](REFERENCE.md) (every command), [SESSIONS.md](SESSIONS.md) (how identity resolves).
