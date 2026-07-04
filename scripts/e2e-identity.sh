#!/bin/bash
# End-to-end scenario for the identity/config layer: quick mode, .lorewire.jsonc
# via `user create`, config resolution + precedence, multi-session per user,
# --to <username> fan-out, secret request/grant, rename cascade, and leave.
# Runs against a throwaway database (never the real ~/.lorewire).
set -euo pipefail
REPO="$(cd "$(dirname "$0")/.." && pwd)"
go build -o "$REPO/lorewire" "$REPO"
LW="$REPO/lorewire"
export LOREWIRE_DB="$(mktemp -d)/lw.db"
PROJ="$(mktemp -d)/project-x"; mkdir -p "$PROJ"

pass() { echo "  ok: $1"; }
fail() { echo "  FAIL: $1"; exit 1; }

echo "== quick mode (no config, just \$LOREWIRE_NAME) =="
( export LOREWIRE_NAME=alice LOREWIRE_SESSION_TOKEN=a1; "$LW" register >/dev/null )
( export LOREWIRE_NAME=bob   LOREWIRE_SESSION_TOKEN=b1; "$LW" register >/dev/null )
( export LOREWIRE_NAME=alice LOREWIRE_SESSION_TOKEN=a1; "$LW" send --to bob "hi" >/dev/null )
got=$( export LOREWIRE_NAME=bob LOREWIRE_SESSION_TOKEN=b1; "$LW" recv | grep -c 'hi' )
[ "$got" = "1" ] && pass "quick-mode delivery" || fail "quick-mode delivery"

echo "== config via user create (writes .lorewire.jsonc) =="
cd "$PROJ"
"$LW" user create carol --room project-x --role dev >/dev/null
grep -q '"room": "project-x"' "$PROJ/.lorewire.jsonc" && pass "config written with room" || fail "config room"
grep -q '"role": "dev"' "$PROJ/.lorewire.jsonc" && pass "config written with role" || fail "config role"
room=$( export LOREWIRE_SESSION_TOKEN=c1; "$LW" whoami | grep '^room' )
echo "$room" | grep -q project-x && pass "whoami reads config room" || fail "whoami room=$room"

echo "== precedence: env overrides config =="
r=$( export LOREWIRE_SESSION_TOKEN=c1 LOREWIRE_ROOM=ops; "$LW" whoami | grep '^room' )
echo "$r" | grep -q 'ops (env)' && pass "env overrides config" || fail "precedence: $r"

echo "== multi-session for one user + --to username fan-out =="
( export LOREWIRE_SESSION_TOKEN=c1; "$LW" register >/dev/null )
( export LOREWIRE_SESSION_TOKEN=c2; "$LW" register >/dev/null )
( export LOREWIRE_NAME=dave LOREWIRE_SESSION_TOKEN=d1; "$LW" join --room project-x --role qa >/dev/null )
( export LOREWIRE_NAME=dave LOREWIRE_SESSION_TOKEN=d1; "$LW" send --room project-x --to carol "both terminals" >/dev/null )
g1=$( export LOREWIRE_SESSION_TOKEN=c1; "$LW" recv --room project-x | grep -c 'both terminals' )
g2=$( export LOREWIRE_SESSION_TOKEN=c2; "$LW" recv --room project-x | grep -c 'both terminals' )
[ "$g1" = "1" ] && [ "$g2" = "1" ] && pass "--to username fanned out to both sessions" || fail "fan-out g1=$g1 g2=$g2"

echo "== secret request/grant consume-once =="
( export LOREWIRE_SESSION_TOKEN=c1; "$LW" request --room project-x --to @qa "staging key" >/dev/null )
REQ=$( export LOREWIRE_NAME=dave LOREWIRE_SESSION_TOKEN=d1; "$LW" inbox --room project-x | grep -Eo 'request#[0-9]+' | grep -Eo '[0-9]+' | head -1 )
( export LOREWIRE_NAME=dave LOREWIRE_SESSION_TOKEN=d1; "$LW" grant "$REQ" --secret "sk-XYZ" >/dev/null )
sec=$( export LOREWIRE_SESSION_TOKEN=c1; "$LW" recv --room project-x | grep -Eo 'sk-XYZ' | head -1 )
[ "$sec" = "sk-XYZ" ] && pass "secret delivered" || fail "secret"
again=$( export LOREWIRE_SESSION_TOKEN=c1; "$LW" recv --room project-x )
[ "$again" = "(no new messages)" ] && pass "consume-once (secret gone)" || fail "consume-once"

echo "== a recv-only session auto-joins and receives (regression) =="
# A terminal that only ever runs recv must still become a room member and get
# --to <user> messages — the "nothing works when I watch" bug.
( export LOREWIRE_SESSION_TOKEN=cwatch; "$LW" recv --room project-x >/dev/null )
inroom=$( "$LW" members --room project-x | grep -c 'caroline\|carol' )
( export LOREWIRE_NAME=dave LOREWIRE_SESSION_TOKEN=d1; "$LW" send --room project-x --to carol "to the watcher" >/dev/null )
wgot=$( export LOREWIRE_SESSION_TOKEN=cwatch; "$LW" recv --room project-x | grep -c 'to the watcher' )
[ "$wgot" = "1" ] && pass "recv-only session joined and received" || fail "recv-only receive ($wgot)"

echo "== incidental send/recv does NOT downgrade an explicit role (regression) =="
drole=$( "$LW" members --room project-x | awk '/dave~/{print $2; exit}' )
[ "$drole" = "qa" ] && pass "dave still qa after his own sends" || fail "role clobbered: dave=$drole"

echo "== rename cascades to sessions =="
"$LW" user rename carol caroline >/dev/null
mem=$( "$LW" members --room project-x | grep -c 'caroline~' )
[ "$mem" -ge 1 ] && pass "rename cascaded to session ids" || fail "rename cascade ($mem)"
# the same terminal (token c1) now resolves to caroline~<same hash> and can leave
( export LOREWIRE_NAME=caroline LOREWIRE_SESSION_TOKEN=c1; "$LW" leave --all >/dev/null )
left=$( "$LW" members --room project-x | grep -c 'caroline~' )
[ "$mem" -gt "$left" ] && pass "leave --all removed the renamed session" || fail "leave after rename"

echo "E2E-IDENTITY PASS"
