#!/bin/bash
# SessionStart hook: register this terminal's session and join the configured
# room, so a Claude Code session is on the wire the moment it opens — no manual
# `lorewire register`. Pairs with lorewire-incoming.sh (delivery) and
# lorewire-leave.sh (auto-unregister on exit).
#
# Identity/room/role come from the project's .lorewire.jsonc (or $LOREWIRE_*).
# No-op when no identity is available, so it's safe to enable globally.
set -euo pipefail

have_config() {
	local d; d="$(pwd)"
	while [ "$d" != "/" ]; do
		[ -f "$d/.lorewire.jsonc" ] && return 0
		d="$(dirname "$d")"
	done
	return 1
}
[ -n "${LOREWIRE_NAME:-}" ] || [ -n "${LOREWIRE_USER_ID:-}" ] || have_config || exit 0

LW="${LOREWIRE_BIN:-lorewire}"
command -v "$LW" >/dev/null 2>&1 || exit 0

"$LW" register >/dev/null 2>&1 || true
