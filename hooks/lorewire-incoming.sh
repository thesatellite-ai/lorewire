#!/bin/bash
# UserPromptSubmit hook: auto-deliver messages from other agent sessions.
#
# Wire this into a session's settings so that every time you submit a prompt,
# any pending lorewire messages are pulled and injected into the session
# context — this is the "push" layer on top of the pull-based `lorewire recv`.
#
# The session's identity comes from $LOREWIRE_NAME (export it before launching
# `claude` in that terminal). If unset, the hook is a no-op so it's safe to
# enable globally.
#
# NOTE: this consumes messages (marks them read), so they appear in the session
# context instead of a manual `lorewire recv`. That's the intended behavior.
set -euo pipefail

# Only act where lorewire is intended: either an identity is exported, or a
# .lorewire.jsonc exists in this dir or an ancestor. Otherwise no-op so we don't
# create stray sessions in unrelated projects.
have_config() {
	local d; d="$(pwd)"
	while [ "$d" != "/" ]; do
		[ -f "$d/.lorewire.jsonc" ] && return 0
		d="$(dirname "$d")"
	done
	return 1
}
[ -n "${LOREWIRE_NAME:-}" ] || [ -n "${LOREWIRE_USER_ID:-}" ] || have_config || exit 0

# Prefer an installed `lorewire`; fall back to a repo-local build if
# LOREWIRE_BIN points at one.
LW="${LOREWIRE_BIN:-lorewire}"
command -v "$LW" >/dev/null 2>&1 || exit 0

out="$("$LW" recv 2>/dev/null || true)"
[ -z "$out" ] && exit 0
[ "$out" = "(no new messages)" ] && exit 0

# Identity may come from .lorewire.jsonc rather than $LOREWIRE_NAME, so derive
# the display name from whoami instead of assuming the env var is set.
who="$("$LW" whoami 2>/dev/null | awk '/^username/{print $3; exit}')"
echo "📨 Messages for ${who:-this session} from other lorewire sessions:"
echo "$out"
echo "(Reply with: lorewire send --to <name|@role> \"...\")"
