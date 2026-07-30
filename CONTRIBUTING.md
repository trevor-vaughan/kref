# Contributing to kref

Thanks for helping out. This is a small, test-driven Go project.

## Setup

- **Go ≥ 1.26.4** (pinned in `go.mod`; or any Go with `GOTOOLCHAIN=auto`, which `task` sets for you).
- [`task`](https://taskfile.dev) for all build/test/lint.

Common tasks are aliased at the root; the `dev:` namespace holds the build/test
loop and `deps:` holds Go-module management (`task --list` shows everything).

```bash
task dev:tools     # installs pinned betterleaks + ginkgoleaf into ./bin (required by ingest/tests)
task test          # full Ginkgo suite, rendered by ginkgoleaf (task test MODE=llm for errors-only)
task lint          # go vet + gofmt check
task build         # ./bin/kref with an embedded version
task dev:test:e2e  # unit + end-to-end suites (slower)
task check         # fmt + lint + vuln + e2e under -race -shuffle (run before pushing)
task clean         # remove ./bin, the built binary, and .test-output
task deps:upgrade  # bump module deps to latest minor/patch, then tidy + verify
```

`task test` provisions betterleaks and points kref at it via `KREF_BETTERLEAKS`, so prefer
it over a bare `go test ./...` (which would need betterleaks on `PATH`).

Suite results are rendered through [ginkgoleaf](https://github.com/trevor-vaughan/ginkgoleaf):
each package's Ginkgo JSON report is captured, rendered, and archived under
`.test-output/reports/`. `FORMAT` selects the renderer — `tree` (default, for humans) or
`github` for GitHub Actions annotations (the `test` workflow sets `FORMAT=github`). The
suite fails the task if any spec fails or any package fails to build.

### Dogfooding kref from your agent host

This repo ships a project-scoped [`.mcp.json`](.mcp.json), so an agent host that
reads it (Claude Code, for one) offers kref's own MCP tools against this repo's
knowledge base — the specs, plans, and TODO lists that drive the project live as
kref entries, not as files in the tree.

```json
{ "mcpServers": { "kref": { "command": "scripts/mcp-server.sh" } } }
```

The launcher prefers `./bin/kref` — the `task build` output, so the server
exposes the tree you are editing — and falls back to a `kref` installed on your
`PATH`. With neither, it exits with a message naming both places it looked and
telling you to run `task build` (or `go install ./cmd/kref`), instead of failing
with a bare `ENOENT` from your host. It also anchors its working directory to the
repo root, so the server serves this checkout however the host launched it, and
the committed config stays free of anyone's absolute paths.

**`./bin/kref` is only refreshed by `task build`** — `task test`/`check`/`e2e`
build to a temp path — so after changing anything under `cmd/` or `internal/`,
run `task build` or the MCP surface stays stale.

Your host will ask you to approve the server the first time; a project-scoped
MCP server is untrusted until you say so.

To point an agent at several repositories instead, run the server yourself with
`kref mcp --allow <root>` or `--client-roots`; see
[MCP server](docs/usage.md#mcp-server) for the boundary rules.

## Conventions

- **Tests: Ginkgo v2 + Gomega only.** No testify, no bare stdlib asserts. One
  `*_suite_test.go` bootstrap per package. The `entry` package's specs are
  **black-box `package entry_test`** (Ginkgo's `Entry` symbol collides with the
  `entry.Entry` type); every other package is white-box.
- **TDD:** write the failing spec, watch it fail, implement, watch it pass.
  Sync/distributed features must be tested with real multi-repo round-trips, not
  stubs.
- **Don't fork git-bug.** Depend on its public packages; pin the version in
  `go.mod`. If you need a capability it doesn't expose, prefer upstreaming.
- **Match existing style.** Read the file before editing. Keep the
  `entry.AllTiers()`-driven generality (no code that assumes a fixed set of
  tiers or kinds).
- **Don't commit build artifacts.** `./bin/` and `.kref/` are gitignored.

## Commits & PRs

- Imperative subject ≤ 72 chars; body explains *why*.
- Stage files by name (no `git add -A`); review `git diff --staged`.
- Run `task check` green before opening a PR.
- CI also runs a shared [MegaLinter](https://github.com/trevor-vaughan/megalint-config)
  profile (YAML, shell, Markdown, secret/IaC linters). To preview locally, clone that
  repo and run `task megalint:run TARGET=$PWD` (needs Podman or Docker).

## Where things live

See [`docs/dev/README.md`](docs/dev/README.md) for architecture. The design
specs and implementation plans live in `kref`'s own knowledge base, not the git
tree — browse them with `kref list --kind spec` / `kref list --kind plan`.
