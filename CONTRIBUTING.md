# Contributing to lorewire

Thanks for your interest in improving **lorewire**. This guide covers how to get set up, the conventions we follow, and what a good pull request looks like.

By participating, you agree to abide by our [Code of Conduct](CODE_OF_CONDUCT.md).

## Getting started

```sh
git clone https://github.com/thesatellite-ai/lorewire.git
cd lorewire
task build      # builds ./lorewire (needs Go 1.26+ and go-task)
task test       # run the end-to-end scenarios
```

If you don't use [go-task](https://taskfile.dev), the equivalents are `go build -o lorewire .` and `./scripts/e2e.sh` / `./scripts/e2e-rooms.sh`.

## Ways to contribute

- **Report a bug** — open an issue using the bug template; include steps to reproduce, your OS/version, and what you expected.
- **Request a feature** — open an issue using the feature template; describe the problem first, then your proposed solution.
- **Send a pull request** — for anything non-trivial, open an issue first so we can agree on the approach before you write code.

## Branches & commits

- Branch off `main`. Use a short descriptive branch name (`fix/...`, `feat/...`, `docs/...`).
- Write commits in **Conventional Commit** style: `type(scope): description` (`feat`, `fix`, `docs`, `refactor`, `test`, `chore`).
- Keep commits focused; one logical change per commit where practical.

## Pull request checklist

- [ ] The change is scoped and described (link the issue it closes).
- [ ] `go vet ./...` is clean and `task test` passes.
- [ ] New behavior has tests where it makes sense (extend `scripts/e2e*.sh`).
- [ ] Docs/README/CHANGELOG updated if the change is user-facing.
- [ ] No unrelated files or formatting churn.

## Changelog

User-facing changes go under `[Unreleased]` in [CHANGELOG.md](CHANGELOG.md), following [Keep a Changelog](https://keepachangelog.com/).

## Questions

Open a [discussion or issue](https://github.com/thesatellite-ai/lorewire/issues). We're happy to help you land your first contribution.
