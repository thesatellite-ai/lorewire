# Tutorial — two Claude Code sessions talking

A hands-on walkthrough: set up two project folders, each a lorewire identity, then have two Claude Code sessions message each other with zero manual `export`s. By the end you'll have tested direct messages, role addressing, broadcasts, consume-once secrets, and auto-register/auto-leave hooks.

Everything here also works with plain terminals (no Claude) — see [Manual test](#manual-test-no-claude-needed).

## Prerequisites

- lorewire installed and on your `PATH`:

```bash
git clone https://github.com/thesatellite-ai/lorewire.git
cd lorewire
task install        # or: go install github.com/thesatellite-ai/lorewire@latest
lorewire --help     # confirm it runs
```

- Note the absolute path to the repo's `hooks/` directory — you'll reference it in the Claude settings below. From the clone above it's `$(pwd)/hooks`. This tutorial calls it `LOREWIRE_HOOKS`.

```bash
export LOREWIRE_HOOKS="$(pwd)/hooks"   # run this from the cloned repo root
```

## Step 1 — create two identity folders

Each folder gets its own identity via a `.lorewire.jsonc`. `lorewire user create` claims the username, mints the userId, and writes the config for you.

```bash
mkdir -p ~/lw-demo/alice ~/lw-demo/bob

( cd ~/lw-demo/alice && lorewire user create alice --room demo --role cto )
( cd ~/lw-demo/bob   && lorewire user create bob   --room demo --role dev )
```

Each folder now has a `.lorewire.jsonc` like:

```jsonc
{
  "userId": "usr_xxxxxxx",
  "room": "demo",
  "role": "cto"
}
```

Both users share the room `demo`; alice is `cto`, bob is `dev`.

## Step 2 — wire the hooks (project-scoped)

Three hooks make a Claude session self-manage on the wire:

- **SessionStart** → `lorewire register` (join the room automatically)
- **UserPromptSubmit** → `lorewire recv` (inject incoming messages into context each turn)
- **SessionEnd** → `lorewire leave --all` (unregister on exit)

Put them in a **project-scoped** `.claude/settings.json` in each folder, so your global Claude config is untouched. This snippet writes both files with the correct absolute hook paths filled in (run it with `LOREWIRE_HOOKS` set from the Prerequisites):

```bash
for d in ~/lw-demo/alice ~/lw-demo/bob; do
  mkdir -p "$d/.claude"
  cat > "$d/.claude/settings.json" <<JSON
{
  "hooks": {
    "SessionStart":     [ { "hooks": [ { "type": "command", "command": "$LOREWIRE_HOOKS/lorewire-register.sh" } ] } ],
    "UserPromptSubmit": [ { "hooks": [ { "type": "command", "command": "$LOREWIRE_HOOKS/lorewire-incoming.sh" } ] } ],
    "SessionEnd":       [ { "hooks": [ { "type": "command", "command": "$LOREWIRE_HOOKS/lorewire-leave.sh" } ] } ]
  }
}
JSON
done
```

All three hook scripts are no-ops unless an identity is available (a `.lorewire.jsonc` in the tree, or `$LOREWIRE_NAME`/`$LOREWIRE_USER_ID`), so they're safe.

## Step 3 — open two Claude Code sessions

```bash
# terminal 1
cd ~/lw-demo/alice && claude

# terminal 2
cd ~/lw-demo/bob && claude
```

Claude may ask you to **trust the project hooks** the first time — approve it. On start, each session auto-registers and joins the `demo` room.

Now drive it in plain English:

1. In **alice's** session, type: *"send bob a lorewire message asking him to take the login page."* Alice's Claude runs `lorewire send --to bob "..."`.
2. In **bob's** session, type anything (e.g. *"any lorewire messages?"*). The UserPromptSubmit hook injects alice's message into bob's context. Bob's Claude can reply: `lorewire send --to @cto "on it"`.
3. Back in **alice's** session, type anything → her hook delivers bob's reply.
4. Close one session (Ctrl-D / `exit`). Its SessionEnd hook runs `lorewire leave --all` automatically.

## Verify from a third terminal

```bash
lorewire user list             # alice + bob
lorewire sessions              # live terminals, grouped by user (cwd, tty, client, last-seen)
lorewire members --room demo   # who's in the room and their roles
lorewire rooms                 # rooms and member counts
```

Inside a demo folder, `lorewire whoami` shows the effective identity/room/role and the source of each value.

## Manual test (no Claude needed)

The fastest way to see it work — two plain terminals:

```bash
# terminal 1
cd ~/lw-demo/alice && lorewire register && lorewire watch    # live inbox stream

# terminal 2
cd ~/lw-demo/bob && lorewire register
lorewire send --to alice "hi from bob"     # appears in alice's watch within ~2s
lorewire send --to @cto "role message"     # also reaches alice (she's cto)
```

## Exercise every feature

Run these from inside either folder (identity/room come from the config):

```bash
# addressing
lorewire send --to bob "direct — all of bob's terminals"
lorewire send --to @dev "everyone with the dev role"
lorewire send --to all  "everyone in the room"

# reading
lorewire recv                 # read + consume unread
lorewire inbox --all          # peek without consuming

# consume-once secret handoff
lorewire request --to @dev "staging API key"   # alice asks whoever is dev
lorewire grant 1 --secret "sk-demo-123"         # bob fulfills (use the #N shown in his inbox)
lorewire recv                                    # alice reveals it once — then it's gone

# identity
lorewire whoami               # effective values + where each came from
```

**Multi-session:** open a second terminal in the **same** folder — you become a second session of that user. `lorewire send --to alice` then reaches **all** of alice's terminals; `lorewire send --to alice~<hash>` targets one.

## Reset / clean up

```bash
lorewire leave --all              # unregister the current terminal
lorewire prune --older-than 0s    # remove all sessions (keeps users)
lorewire user list                # confirm what remains
```

To wipe everything (users, rooms, messages), delete the database:

```bash
rm -rf ~/.lorewire                # or: task reset (from the repo)
```

## How it works / troubleshooting

- **No exports needed** because each folder's `.lorewire.jsonc` supplies identity/room/role; lorewire walks up from the current directory to find it. Env vars and flags still override (see the [reference](REFERENCE.md#resolution-precedence)).
- **Nothing delivered?** Confirm both sessions joined the room: `lorewire members --room demo`. A session must be a room member to receive `--to @role` / `--to all` (the SessionStart hook / `lorewire register` handles this).
- **Hook didn't fire?** Ensure `lorewire` is on the `PATH` seen by Claude, and that you approved the project hooks. The hooks honor `$LOREWIRE_BIN` if the binary isn't on `PATH`.
- **Two people, one machine:** each OS user has their own `~/.lorewire` database, so they don't collide. To act as a different identity in a shared folder, override with `export LOREWIRE_USER_ID=…` (or `LOREWIRE_NAME=…`).

See the [complete reference](REFERENCE.md) for every command and flag.
