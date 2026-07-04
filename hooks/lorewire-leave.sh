#!/bin/bash
# SessionEnd hook: auto-unregister this session from lorewire when it closes.
#
# Pairs with hooks/lorewire-incoming.sh (the UserPromptSubmit push hook). When
# the Claude Code session ends, this removes it from EVERY room and the sessions
# table (leave --all) so it stops appearing in `lorewire sessions`/`members` and
# stops receiving broadcasts — no need to remember `lorewire leave` by hand.
#
# Identity comes from $LOREWIRE_NAME. No-op if unset, so it's safe to enable
# globally. It does NOT --purge, so any unread inbox is kept: relaunching the
# same $LOREWIRE_NAME re-registers and resumes history.
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

"$LW" leave --all >/dev/null 2>&1 || true
