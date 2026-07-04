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

[ -z "${LOREWIRE_NAME:-}" ] && exit 0

# Prefer an installed `lorewire`; fall back to a repo-local build if
# LOREWIRE_BIN points at one.
LW="${LOREWIRE_BIN:-lorewire}"
command -v "$LW" >/dev/null 2>&1 || exit 0

out="$("$LW" recv 2>/dev/null || true)"
[ -z "$out" ] && exit 0
[ "$out" = "(no new messages)" ] && exit 0

echo "📨 Messages from other sessions (delivered by lorewire, addressed to '$LOREWIRE_NAME'):"
echo "$out"
echo "(Reply with: lorewire send --to <name> \"...\")"
