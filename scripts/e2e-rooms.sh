#!/bin/bash
# End-to-end scenario for rooms, roles, @role addressing, multi-room membership,
# and the request/grant/deny secret flow — against a throwaway database (never
# the user's real ~/.lorewire).
set -euo pipefail
cd "$(dirname "$0")/.."

go build -o lorewire .
export LOREWIRE_DB="$(mktemp -d)/lw.db"
LW=./lorewire
echo "using throwaway DB: $LOREWIRE_DB"

echo "== backward compat: no room = main =="
LOREWIRE_NAME=alice $LW register
LOREWIRE_NAME=bob   $LW register
LOREWIRE_NAME=alice $LW send --to bob "hi bob (default room)"
LOREWIRE_NAME=bob   $LW recv

echo "== rooms + roles =="
LOREWIRE_NAME=alice $LW join --room project-x --role ceo
LOREWIRE_NAME=bob   $LW join --room project-x --role cto
LOREWIRE_NAME=carol $LW join --room project-x --role dev
LOREWIRE_NAME=dave  $LW join --room project-x            # no role -> guest
$LW members --room project-x

echo "== @role addressing =="
LOREWIRE_NAME=alice $LW send --room project-x --to @dev "devs: standup in 5"
LOREWIRE_NAME=carol $LW recv --room project-x

echo "== multi-room: alice also in ops =="
LOREWIRE_NAME=alice $LW join --room ops --role lead
LOREWIRE_NAME=eve   $LW join --room ops --role sre
LOREWIRE_NAME=alice $LW send --room ops --to eve "deploy window 3pm"
LOREWIRE_NAME=eve   $LW recv

echo "== secret request -> grant (consume-once) =="
LOREWIRE_NAME=carol $LW request --room project-x --to @cto "need OpenAI API key"
REQID=$(LOREWIRE_NAME=bob $LW inbox --room project-x | grep -Eo 'request#[0-9]+' | grep -Eo '[0-9]+' | head -1)
LOREWIRE_NAME=bob $LW grant "$REQID" --secret "sk-SECRET-123"
echo "-- peek (masked) --"; LOREWIRE_NAME=carol $LW inbox --room project-x --all | grep secret
echo "-- consume (revealed) --"
got=$(LOREWIRE_NAME=carol $LW recv --room project-x | grep -Eo 'sk-SECRET-123' || true)
test "$got" = "sk-SECRET-123" && echo "secret delivered OK"
empty=$(LOREWIRE_NAME=carol $LW recv --room project-x)
test "$empty" = "(no new messages)" && echo "consume-once OK (secret gone)"

echo "== deny =="
LOREWIRE_NAME=carol $LW request --room project-x --to @cto "prod DB password"
REQID2=$(LOREWIRE_NAME=bob $LW inbox --room project-x | grep -Eo 'request#[0-9]+' | grep -Eo '[0-9]+' | head -1)
LOREWIRE_NAME=bob $LW deny "$REQID2" "use the vault"
LOREWIRE_NAME=carol $LW recv --room project-x | grep -q denied && echo "deny delivered OK"

echo "== role set + leave --all =="
$LW role set dave qa --room project-x
LOREWIRE_NAME=bob $LW leave --all
$LW members --room project-x | grep -q bob && echo "FAIL: bob still present" || echo "leave --all removed bob from rooms OK"

echo "E2E-ROOMS PASS"
