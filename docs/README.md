# lorewire documentation

The full knowledge base for lorewire — a message bus for AI coding agent sessions to talk to each other. Start with the [project README](../README.md) for the overview; the docs below go deep.

## Start here

- **[Tutorial](TUTORIAL.md)** — hands-on, step by step: get two Claude Code sessions talking (and a no-agent path for a 30-second sanity check). Read this first if you just want to *use* lorewire.
- **[Use cases cookbook](USE-CASES.md)** — 11 real scenarios worked start-to-finish with exact commands and expected output: direct message, team + roles, task hand-off, secret request, broadcast, orchestrator↔workers, multi-terminal fan-out, one identity across projects, CI bot, fresh-machine import, and cleanup.

## Understand it

- **[How sessions work](SESSIONS.md)** — plain-language explainer of how lorewire decides "which session am I?" (agents vs terminals), the `username~hash` id, and debugging with `id_source`. Read this if session ids ever confuse you.
- **[Architecture](ARCHITECTURE.md)** — the internal design and every decision *and its why*: the CLI-plus-SQLite shape, the full schema, message flow, concurrency, rooms/roles/membership, secrets, migrations, principles, and non-goals. Read this if you're maintaining or extending lorewire.
- **[Glossary](GLOSSARY.md)** — one-line definitions of every term (user, session, room, role, member, message, kind, fan-out, consume-once, …).

## Do specific things

- **[Integrations](INTEGRATIONS.md)** — wire lorewire into an agent: Claude Code hooks in depth, plus the generic recipe for any agent (Codex, opencode, …) and plain scripts/CI. Includes a troubleshooting table.
- **[Reference](REFERENCE.md)** — the complete manual: every command, flag, environment variable, config key, addressing form, hook, schema table, exit code, and recipe.

## Roadmap

- **[Tasks / roadmap](TASKS.md)** — open work: Codex & opencode integration, CI/CD, GoReleaser, full unit-test coverage, the ent/typesafe migration, and deferred features — each with detail to pick up cold.

## Also in the repo

- [`../PLAN.md`](../PLAN.md) — the design/decision log for the identity + config layer.
- [`../hooks/`](../hooks/) — the Claude Code hook scripts + `settings.example.json`.
- [`../CHANGELOG.md`](../CHANGELOG.md) — release history.
- [`../CONTRIBUTING.md`](../CONTRIBUTING.md) — dev setup and conventions.

## Reading paths by goal

| I want to… | Read |
|---|---|
| Try it in 5 minutes | [Tutorial](TUTORIAL.md) |
| See a specific scenario worked end-to-end | [Use cases](USE-CASES.md) |
| Understand why my session id looks like that | [SESSIONS.md](SESSIONS.md) |
| Look up a command or flag | [Reference](REFERENCE.md) |
| Connect a non-Claude agent | [Integrations](INTEGRATIONS.md) |
| Maintain / extend the code | [Architecture](ARCHITECTURE.md) + [Glossary](GLOSSARY.md) |
| Debug delivery / identity | [SESSIONS.md](SESSIONS.md) + [Integrations](INTEGRATIONS.md#troubleshooting) |
