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
- Optional Claude Code hooks: `UserPromptSubmit` push delivery and `SessionEnd` auto-unregister.

### Changed
### Deprecated
### Removed
### Fixed
### Security

<!--
Release process:
  1. Move the relevant [Unreleased] entries under a new version heading below.
  2. Date it: ## [1.2.0] - YYYY-MM-DD
  3. Tag the release (e.g. v1.2.0) and update the link refs at the bottom.
-->

[Unreleased]: https://github.com/thesatellite-ai/lorewire/commits/main
