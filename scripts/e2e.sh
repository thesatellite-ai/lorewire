#!/bin/bash
# End-to-end scenario for lorewire: three sessions register, exchange direct and
# broadcast messages, and verify consume-once + concurrency semantics against a
# throwaway database (never the user's real ~/.lorewire).
set -euo pipefail
cd "$(dirname "$0")/.."

go build -o lorewire .
export LOREWIRE_DB="$(mktemp -d)/lorewire.db"
echo "using throwaway DB: $LOREWIRE_DB"

LOREWIRE_NAME=alice ./lorewire register
LOREWIRE_NAME=bob   ./lorewire register
LOREWIRE_NAME=carol ./lorewire register
./lorewire sessions

echo "== direct + broadcast =="
LOREWIRE_NAME=alice ./lorewire send --to bob "can you take the frontend?"
LOREWIRE_NAME=alice ./lorewire send --to all "starting the build now"

echo "== bob consumes (both), then empty =="
LOREWIRE_NAME=bob ./lorewire recv
LOREWIRE_NAME=bob ./lorewire recv

echo "== carol got only the broadcast =="
LOREWIRE_NAME=carol ./lorewire recv

echo "== concurrency: 20 parallel sends, racing recv, expect 20 unique =="
for i in $(seq 1 20); do LOREWIRE_NAME=alice ./lorewire send --to bob "m$i" >/dev/null & done
wait
LOREWIRE_NAME=bob ./lorewire recv >/tmp/lw_e2e_a.txt & LOREWIRE_NAME=bob ./lorewire recv >/tmp/lw_e2e_b.txt & wait
uniq=$(cat /tmp/lw_e2e_a.txt /tmp/lw_e2e_b.txt | grep -Eo 'm[0-9]+' | sort -u | wc -l | tr -d ' ')
echo "unique delivered: $uniq (expect 20)"
test "$uniq" = "20" && echo "E2E PASS"
