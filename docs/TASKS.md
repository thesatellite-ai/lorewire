# lorewire — tasks

Open work, with enough detail under each item to pick it up cold next session. Checkboxes: `[ ]` todo, `[>]` in progress, `[x]` done.

## Validation & integrations

- [ ] **Validate the real two-agent flow (Claude ↔ Claude)**
  - Restart both demo Claude sessions (`~/Downloads/lorewire-demo-alice`, `-bob`) so `SessionStart` re-registers under the new agent-session-id logic.
  - In a 3rd terminal: `lorewire members --room demo` should list `alice~…` and `bob~…`, each `id from: agent:CLAUDE_CODE_SESSION_ID` (check `lorewire sessions --json`).
  - alice sends → bob sees it on his next turn. Confirm no duplicate/zombie bob sessions.
  - This is the end-to-end proof the `/dev/tty` → `CLAUDE_CODE_SESSION_ID` fix actually works in a live agent.

- [ ] **Codex integration + validate**
  - Find Codex's real per-session env var name (I guessed `CODEX_SESSION_ID` in `constants.go` `agentSessionEnvs` — verify/correct it). Run `env | grep -i codex` inside a Codex session.
  - Either add the confirmed name to `agentSessionEnvs`, or document `LOREWIRE_SESSION_ENV=<var>` / `LOREWIRE_SESSION_TOKEN`.
  - Add register/recv/leave adapters equivalent to the Claude hooks (Codex's hook/lifecycle mechanism).
  - Validate: two Codex sessions message each other; `whoami` shows `id from: agent:<var>`.

- [ ] **opencode integration + validate**
  - Same as Codex: confirm opencode's session env var (guessed `OPENCODE_SESSION_ID`), wire lifecycle hooks, validate two opencode sessions talk.
  - Bonus: cross-tool test — a Claude session and an opencode session in the same room messaging each other.

## Housekeeping (small, do soon)

- [x] **Update `CHANGELOG.md`** — done; all features are under `[Unreleased]`. Remaining: move them under a dated version heading when tagging `v0.1.0`.

- [ ] **Tag `v0.1.0`**
  - After CHANGELOG. `git tag v0.1.0 && git push --tags`.
  - Makes the built `version` show a real value instead of `+dirty`. Coordinate with GoReleaser task below.

- [ ] **CI/CD (GitHub Actions)**
  - Add `.github/workflows/ci.yml` running the gate on push/PR: `gofmt -l`, `go vet ./...`, `go test -race ./...`, `go build`. (The e2e shell scripts can run too, or a subset.)
  - Reference the `lore` repo's `.github/workflows/ci.yml` for the workspace pattern if/when we adopt go.work.

- [ ] **GoReleaser auto-publish**
  - Add `.goreleaser.yml` + a `release.yml` workflow that builds cross-platform binaries and publishes a GitHub Release on tag push.
  - Include `install.sh` / `install.ps1` (mirror lore's). Optionally a Homebrew tap formula.
  - Keep CGO off (pure-Go modernc SQLite) so cross-compile stays trivial.

## Testing

- [ ] **Full-coverage unit tests**
  - Currently only `config_test.go` (JSONC stripper, id shape) + shell e2e. Add Go unit tests for the `Store` layer: users (create/rename cascade/import), sessions (register/context/id_source), rooms/members (Join vs EnsureMember role-preservation), messaging (fan-out, @role, consume-once secret, recv-marks-read under concurrency), reset/prune.
  - Use an in-memory / temp-file SQLite DB per test (`LOREWIRE_DB` or a `newTestStore(t)` helper). Table-driven; `t.Parallel()` where safe; `go test -race`.
  - Wire coverage into `task check` / CI; aim for meaningful coverage of `store.go` + resolution logic.

## Big migration (v2 direction — matches the `lore` repo / ubgo stack)

- [ ] **Restructure to the `lore` repo layout**
  - Target: a `go.work` workspace like `lore` (separate modules for the data layer, shared libs, and the app/CLI) instead of the current single flat `package main`.
  - First step when we start: study `thesatellite-ai/lore` structure in detail (dbent / lace / saas modules, cmd/pkg/schema/gen layout) and write a mapping plan. (Do NOT map it now — this is a placeholder.)
  - Preserve behavior + the current CLI surface; migrate incrementally with the e2e suite as the safety net.

- [ ] **Migrate to typesafe ent (entgo) ORM**
  - Replace hand-written `database/sql` + string SQL in `store.go` with ent schemas + generated typesafe client (like `lore/dbent`): schema/ + generated gen/.
  - Model users, rooms, sessions, members, messages as ent schemas; use generated field constants (no string field names); reference tables over `field.Enum` where taxonomy is user-facing (per ubgo-golang-v2 rules).
  - Keep CGO-free: ent runs on the pure-Go `modernc.org/sqlite` driver via `database/sql`.
  - This is large — do it alongside the restructure, behind the same e2e safety net. Follow the `ubgo-golang-v2` skill's rules (3 commandments, entpoly, DB-as-dumb-storage, naming).

## Deferred features (discussed, chose not to build yet)

- [ ] **`--strict` rooms / pending-lobby for no-role joiners**
  - Today a no-role join → `guest`. Add an opt-in `--strict` room where a joiner with no role lands in a `pending` state and can't send until an owner assigns a role. Needs a member state column + a send gate.

- [ ] **Message TTL / auto-cleanup**
  - Read messages currently linger until `reset`/`prune`. Add optional expiry (e.g. delete read messages older than N, or a `messages` GC on open). Decide policy (per-room? global? config knob).

- [ ] **Multi-user / networked trust model (big; only if going beyond one machine)**
  - Current model is local + cooperative (roles are labels, self-declared, one machine's DB). For real multi-user: room join-secrets/tokens, owner-assigned roles (no self-declaring privileged roles), impersonation prevention, and a shared/synced store or a small server. Substantial; separate product tier.

## Polish (nice-to-have)

- [ ] **Social-preview / OG image for the GitHub repo**
  - Repo link currently shares without a card. Generate a 1280×640 image (brand-kit was skipped) and set it via repo settings / `gh`.

- [ ] **Generalize `clientKind` detection**
  - `config.go` `clientKind()` still detects Claude via `CLAUDECODE`. Generalize (e.g. read `AI_AGENT`, or a small known-map) so `sessions.client` is accurate for Codex/opencode too. Cosmetic.
