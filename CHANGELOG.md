# Changelog

All notable changes to `kref` are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

`kref` is pre-release; nothing has shipped yet. This section summarizes what the
first release will contain. Granular, release-to-release entries start once
there is a tagged release to diff against.

### Added

- **`edited` timestamp** — a distinct "last body change" time, separate from
  `updated` (last change of *any* kind). It is derived from the entry's
  `SetBody` operations, so it needs no migration and reads correctly for
  existing entries. `edited` is a sortable `--sort` key and a `--columns=edited`
  column, and `kref list` now **defaults to `--sort edited`** so metadata churn
  (labels, links, status, retier) no longer resurfaces entries whose prose is
  unchanged; `--wide` shows `edited` in place of `updated`. `--json` gains an
  `edited_at` field. Use `--sort updated` for the previous last-touched order.
  Sort direction stays the `:asc`/`:desc` suffix (no `--asc`/`--desc` flags).
- Rendered markdown now **reflows** soft-wrapped source to the display width:
  paragraphs, list items, and blockquotes hard-wrapped in the stored body
  (LLM-authored entries typically arrive at ~78 columns) join back into
  full-width lines in `kref show`, the pager, and pipes. Hard line breaks
  (trailing two spaces or a backslash), code blocks, and tables are left
  untouched; `--plain` and `--json` output is unaffected. Rendering itself
  moved to glamour v2 (`charm.land/glamour/v2`) for clean blockquote wrapping.
- `--plain` is now a **global** flag with one meaning everywhere — chrome-free,
  line-oriented output for `grep`/`cut`/`xargs`: TSV for `kref list`
  (behavior unchanged) and `kref search` (new: matches/tier/id/kind/title, no
  header or footer), the verbatim stored body with no header for `kref show`.
  It implies no color and no pager, and is mutually exclusive with `--json`.
  **BREAKING:** `kref show --raw` is removed — `kref show --plain` is the
  byte-fidelity form (`--no-header` still gives a rendered, headerless view).
- **Custom tiers**: declare any number of personal- or shared-typed tiers
  (`kref tier add <name> --type … [--remote …]`), each with its own remote;
  reads discover undeclared namespaces from refs so teammates' tiers render
  instead of vanishing. Colors/glyphs follow the tier's TYPE. **BREAKING:**
  `kref promote` and `kref private` are removed — `kref retier <id> <tier>`
  is the single movement verb (same fail-closed secret gate on any
  shared-typed destination).
- `kref remote list` shows every tier's configured remote (name, URL, and
  syncability — the private tier is listed as never-syncing); bare
  `kref remote` runs it. `kref remote get <tier>` prints one tier's remote and
  errors when the tier is unconfigured (or private), so scripts can probe
  configuration. Both honor `--json`.
- kref now nudges about un-synced work: `kref init` notes that no sync remote
  is configured yet, and after an op-creating command (new/ingest/update/…) a
  warning fires — at most once per day, tracked in local git config — when
  syncable entries exist but no tier has a remote. Read-only commands and
  `--json` runs stay quiet.
- A missing betterleaks binary no longer hard-fails every content path:
  `ingest`/`track`/`reconcile`/`update --file` proceed with a loud UNSCANNED
  warning (and an `unscanned` flag in `--json`), while `sync push` stays
  fail-closed — unscanned content never leaves the machine unless you say so:
  `sync push --force` overrides the missing-scanner refusal (loud UNSCANNED
  warning; a secret detected by a working scanner still blocks). Errors and
  warnings carry the install hint
  (`go install github.com/betterleaks/betterleaks@latest`).
- `kref agents_md` prints a canonical agent-policy block for a global
  AGENTS.md/CLAUDE.md (the instruction layer that outranks skills — it
  overrides other skills' file-writing defaults so knowledge becomes kref
  entries, not worktree files); `--skill` emits a complete SKILL.md driving
  manual. The text ships in the binary so it tracks the installed version,
  the `kref completion` pattern applied to agents.
- The MCP server covers the full reversible document lifecycle: the new
  `kref_lifecycle` tool handles set_status (validated against the canonical
  vocabulary, now exported as `entry.Statuses`), delete/restore (tombstones),
  and archive/unarchive. `purge` and `retier` are deliberately not exposed —
  destruction and disclosure-sensitive moves stay human.
- Fine-grained edits for agents: the `kref_patch` MCP tool applies a unified
  diff to an entry's body with a lenient, LLM-tolerant applier — hunk line
  numbers are hints only; each hunk is located by its context lines (exact or
  trailing-whitespace-insensitive), hunks apply in document order, and git
  preamble/file headers/no-newline markers are tolerated. Safety stays strict:
  missing context (a stale diff) or ambiguous context (identical sections with
  no usable line hint) fails loudly and application is all-or-nothing, so a
  patch never half-applies or silently lands in the wrong section. Each
  successful patch is one new body version. Deliberately MCP-only — a human
  edits with `kref edit`, and full-body replacement (`kref update
  --body/--file`) remains the CLI path.
- `--sort <field>[:asc|:desc]` on `kref list` and `kref search` orders the
  table, `--plain`, and `--json` output by `tier|id|kind|status|title|author|
  created|updated` (search adds `matches`). Bare keys sort ascending, except
  the date fields (`created`, `updated`), which put the newest at the top;
  tab completion offers every key and its non-default direction form.
  `kref list`'s default order is `updated` (most recently touched first, in
  every output mode); `--sort tier` restores visibility grouping.
- Tab completion for `kref list --columns=<TAB>` offers the column vocabulary
  with descriptions, comma-aware: already-chosen columns drop out and the
  completion continues after each comma.
- `kref search <query>` replaces `kref list --search`: a dedicated command that
  shows how many times the query occurs in each matching entry (title + body,
  case-insensitive), most matches first, with a `N entries, M matches` footer.
  It keeps the `--kind`/`--status`/`--tier`/`--label` filters, pages on a
  terminal, and adds a `matches` field to each `--json` object.
- `kref list` pages its table on an interactive terminal, using a lean variant
  of the pager (no line-number gutter, no `<n>g` line jumps — scrolling and `/`
  search only). Pipes, `--plain`, and `--json` bypass it; `--no-pager` opts out.
- The `kref show` pager gained an `r` hotkey: re-read the entry through a fresh
  store handle and re-render in place (scroll position preserved) — for
  watching an entry another process (an agent, a sync) is updating.
- `kref log` numbers body versions (`v1`, `v2`, …) and shows a compact change
  summary per edit (`+318/-42 chars, +7/-2 lines`) instead of just the new
  total. `kref diff` now renders inline colored diffs by default (additions
  green, removals red, unchanged context between): bare walks every version
  pair, `kref diff <id> <n>` shows what vN changed, `kref diff <id> <m> <n>`
  spans a range, and `--full` keeps the whole-bodies recovery view. `--json`
  still emits the full version set.
- `KREF_COLOR` environment variable: `KREF_COLOR=1` forces ANSI color on and
  `KREF_COLOR=0` forces it off, overriding both `NO_COLOR` and terminal
  detection (recording environments like VHS set `NO_COLOR` in their session;
  this is the escape hatch). `--json` output is never colored. Any other value
  falls back to auto-detection.
- `kref show` now renders entries before printing: markdown via a rich terminal
  renderer, recognized code and structured text (JSON, YAML, Go, Python, shell,
  TypeScript, etc.) with syntax highlighting, and everything else verbatim. On an
  interactive terminal output is paged in a full-screen viewer (scroll with
  `j`/`k`, page with `ctrl+d`/`ctrl+u`, search with `/`, quit with `q`); piped
  output skips the pager. Flags: the global `--plain` (emit the stored body
  verbatim, no header), `--no-header` (omit the metadata block), `--no-pager`
  (never page).
  The same pager backs `kref diff` (with a line-number gutter so `<n>g` jumps to
  a version line); `kref diff --no-pager` opts out.
- Entries carry a MIME content type (default `text/markdown`). `kref new` and
  `kref update` accept `--content-type <type>` to set it. Supported types:
  `text/markdown`, `text/plain`, `application/json`, `application/yaml`,
  `application/toml`, `text/x-go`, `text/x-python`, `text/x-shellscript`,
  `text/javascript`, `text/x-typescript`. Binary content is rejected.
- `kref ingest` detects the content type from the file extension. Markdown files
  behave as before (trailer written, file tracked). Non-markdown text files are
  stored content-only: the detected type is recorded, no trailer is written, and
  the file is not tracked. Binary files are rejected.
- `--json` output from `kref show` and `kref list` includes a `content_type`
  field on every entry.
- Repo-resident knowledge base stored as git objects under `refs/kref-<tier>/*`
  (built on git-bug's `entity/dag`), so knowledge travels with the repo without
  touching the working tree or `main`'s history.
- Three visibility tiers — `private` (structurally unpushable, never leaves the
  machine), `personal`, and `shared` — as separate ref namespaces, with per-tier
  `sync push`/`pull` to configured remotes.
- Typed entries (`spec`, `adr`, `plan`, `memory`, `reference`, `document`) with
  status (`open`/`active`/`accepted`/`superseded`/`obsolete`), labels, typed
  links, supersede/merge tracking, and append-only provenance; git-identity
  attribution propagates alongside entries and can be corrected with
  `update --reset-author`/`--author`.
- Entry lifecycle and discovery: `new`, `ingest` (idempotent, multi-path,
  `--kind`), `update`/`edit`, `status`, `retier` (alias `mv`), `rm`/`restore`,
  `archive`/`unarchive` (hide entries without deleting; `archive --obsolete`
  bulk-archives), `purge`, plus history and divergence views (`log` (alias
  `audit`), `diff`, `list --new`, `tidy`, `tree`).
- Secret-scanning boundary: betterleaks runs on `ingest` and on `sync push`,
  failing closed and quarantining detected secrets to the private tier.
- Human-readable output by default with a global `--json` flag, plus a
  shell-friendly `list --plain` (TSV) with selectable `--columns=…` / `--wide`
  and an `--archived` view; an MCP stdio server (`kref mcp`) exposing curated
  tools; lefthook wiring via `kref hooks install`; build-time version embedding
  (`kref version`).
- `SECURITY.md` with the trust model and a private vulnerability reporting
  channel (GitHub security advisories, with maintainer email as a fallback).
- **Favorites pin to the top of `kref list`.** Entries with a favorite name
  (user or shared layer) float above the rest in every view — table, `--plain`,
  and `--json` — with the active `--sort` (or the default order) applied *within*
  the favorite and non-favorite groups. `kref fav add` now takes its arguments
  **id first** (`kref fav add <id> <name>`), so `kref fav add <TAB>` completes the
  entry you are naming and the following word is the free-form name. `kref fav rm
  <TAB>` completes the favorite names in the layer it will act on (or an
  ActiveHelp hint when there are none), and a bare `kref fav` now defaults to
  `kref fav ls`.

### Fixed

- `kref bundle export <file>` reported success even when the final flush to
  disk failed (e.g. disk full), which could leave a truncated backup bundle.
  The file close is now checked and a failure is returned as an error.
- Failing to append the `.kref/` line to `.git/info/exclude` during tracking
  setup was silently ignored; it now surfaces as an error.
- `kref update <id> --kind <kind>` (or `--title`) on an interactive terminal
  hung forever waiting on stdin. Stdin is now read as the body source only when
  it is actually piped or redirected; a bare interactive `kref update <id>`
  errors with guidance instead of hanging.

### Security

- betterleaks scans every ingest and push to keep secrets out of syncable tiers.
- Scratch files (editor buffers for `kref edit`, bundle export/import staging,
  betterleaks reports) are created under `$XDG_CACHE_HOME/kref/tmp`
  (`~/.cache/kref/tmp`, mode 0700) instead of the shared system temp dir —
  they can carry entry bodies, including private-tier content. HOME-less
  environments fall back to the system temp dir (files remain 0600).
- Dependencies and the Go toolchain are pinned to versions with no known
  vulnerabilities; CI runs `govulncheck`, CodeQL, golangci-lint, and a
  betterleaks scan of the repository's own history on every change, with
  findings surfaced as SARIF in the Security tab.
- Tagged releases are built by GoReleaser in CI and ship with an SPDX SBOM
  per archive and a Sigstore build-provenance attestation
  (`gh attestation verify` against the release workflow).

### Known limitations

- Operations are attributed but **not cryptographically signed** — git-bug
  v0.10.1 exposes no API to equip an identity with a signing key.
- No encryption at rest; no semantic/vector search yet.
