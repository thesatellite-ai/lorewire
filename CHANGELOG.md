# Changelog

All notable changes to **lorewire** are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Core message bus: `register`, `send`, `recv`, `inbox`, `watch`, `sessions` over a shared SQLite store.
- Direct, broadcast (`--to all`), and role (`--to @role`) message addressing.
- Rooms with a default `main` room (rooms are optional); a session can belong to many rooms at once.
- Roles per room membership, `@role` addressing, and `role set`; no-role joins default to `guest`.
- Session lifecycle: `join`, `leave` (per room), `leave --all`, and a `prune` janitor for stale sessions.
- Request/grant/deny flow for secrets, with consume-once delivery (secrets hard-deleted after one `recv`, masked in `inbox` peeks).
- Identity model: users (`userId` + `username`) that own many sessions (one per terminal/agent); `.lorewire.jsonc` project config (JSONC) supplying identity/room/role, with walk-up discovery and env/flag overrides.
- Agent-agnostic, stable session ids: derived from an agent's per-session env var (`LOREWIRE_SESSION_TOKEN` / `LOREWIRE_SESSION_ENV` / a built-in known-list) with tty/pid fallbacks; `id_source` records the layer used.
- Rich per-session context columns (cwd, tty, pid, host, client, os_user, os, arch, shell, term_program, ssh, tmux, git branch/repo, version).
- Commands: `whoami` (full session detail + JSON), `import` (re-create a config's identity on a fresh machine), `reset` (sessions/messages/all, with `--user`/`--me` and a confirm gate), `--me` filters on `sessions`/`rooms`, `user rename` (cascades to session ids), `user sessions` (live + historical), and `log` (message-history transcript).
- Identity-level message history: messages record `from_owner`/`to_owner` (userIds); `inbox` is user-scoped (with `--session`), `log` keys on userId so history spans a user's past sessions.
- Optional Claude Code hooks: `SessionStart` register, `UserPromptSubmit` push delivery, `SessionEnd` auto-unregister.

### Changed
- `inbox` is now user-scoped (all your sessions) instead of single-session; `recv` remains the session-scoped consuming read.

### Deprecated
### Removed
### Fixed
- Migration ordering: owner-column indexes are created after the columns are added, so upgrading an existing database no longer fails.

### Security
- SQLite `synchronous=NORMAL` under WAL; consume-once secrets; masked secret bodies in non-consuming peeks.

<!--
Release process:
  1. Move the relevant [Unreleased] entries under a new version heading below.
  2. Date it: ## [1.2.0] - YYYY-MM-DD
  3. Tag the release (e.g. v1.2.0) and update the link refs at the bottom.
-->

[Unreleased]: https://github.com/thesatellite-ai/lorewire/commits/main
