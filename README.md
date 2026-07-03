# kref: a repo-resident knowledge base over git objects

[![test](https://github.com/trevor-vaughan/kref/actions/workflows/test.yml/badge.svg)](https://github.com/trevor-vaughan/kref/actions/workflows/test.yml)
[![lint](https://github.com/trevor-vaughan/kref/actions/workflows/lint.yml/badge.svg)](https://github.com/trevor-vaughan/kref/actions/workflows/lint.yml)
[![security](https://github.com/trevor-vaughan/kref/actions/workflows/security.yml/badge.svg)](https://github.com/trevor-vaughan/kref/actions/workflows/security.yml)
[![megalint](https://github.com/trevor-vaughan/kref/actions/workflows/megalint.yml/badge.svg)](https://github.com/trevor-vaughan/kref/actions/workflows/megalint.yml)
[![Go](https://img.shields.io/github/go-mod/go-version/trevor-vaughan/kref)](go.mod)
[![License: GPL-3.0](https://img.shields.io/badge/license-GPL--3.0-blue)](LICENSE)

`kref` stores specs, ADRs, plans, memories, and reference notes inside your git repository as git objects, under their own ref namespaces, not in your working tree and not on your `main` branch. The entry *bodies* travel with the repo (clone, push, pull) without cluttering your file tree, your `git log`, or your `git blame`. (`kref ingest` does write one small `<!-- kref-id: … -->` trailer back into each source file so re-ingest is idempotent; see [The ingest bridge](#the-ingest-bridge). Everything else lives in refs.)

It is built on [git-bug](https://github.com/git-bug/git-bug)'s `entity/dag` framework: every entry is a Lamport-ordered DAG of operations that merges conflict-free across machines and teammates.

> **Status:** pre-release (unversioned; until a release is tagged the binary reports a `git describe --tags --always` string: the nearest checkpoint tag, commits since, and a short SHA, or a bare short SHA when `HEAD` has no ancestral tag). Local-first CLI. See [Limitations](#limitations).

______________________________________________________________________

> 🤖 LLM WARNING 🤖
>
> This project was written with LLM (AI) assistance.
>
> 🤖 LLM WARNING 🤖

______________________________________________________________________

## Demo

**A quick tour**: initialize a store, capture a spec, an ADR, and a private memory across visibility tiers, then list and recall them.

![kref tour: init, capture typed entries across tiers, list, and recall one rendered](docs/demo/tour.gif)

**Secret-aware ingest**: point kref at markdown you already have. One file carries a leaked token; betterleaks catches it on the way in and quarantines that entry to the `private` tier, which has no remote and can never be pushed.

![kref secret-aware ingest: a leaked token is quarantined to the unpushable private tier](docs/demo/secrets.gif)

<sub>Both demos are rendered with [VHS](https://github.com/charmbracelet/vhs) from the tapes in [`.taskfiles/demo/`](.taskfiles/demo/); regenerate them with `task dev:demo` (needs `vhs`, `ttyd`, and `ffmpeg` on `PATH`).</sub>

______________________________________________________________________

## Why

I was tired of AI agents injecting tons of planning files into my repositories. I also wanted an easy way to keep a running log of issues that I wanted my agents to complete in a way that moved with my repo.

This is very much a work in progress and targeted towards my personal workflow. It is likely to change rapidly for a while.

- **Typed entries**: `spec`, `adr`, `plan`, `memory`, `reference`, `document` (free-form `kind`), each with status, links, and author attribution.
- **Three visibility tiers, plus your own**: `private` (never leaves your machine), `personal` (your devices), `shared` (your team), and any number of custom personal- or shared-typed tiers you declare with `kref tier add`, each with its own remote. Tiers are git ref namespaces; colors and glyphs follow the tier's type; the private tier is structurally unpushable.
- **Conflict-free sync**: push/pull each tier to a configured remote.
- **Secret-aware ingest**: markdown is scanned with [betterleaks](https://github.com/betterleaks/betterleaks) on the way in; anything that trips a rule is quarantined to the private tier.
- **Two-way file tracking**: `track` a markdown file and keep it synced with its entry. `reconcile` pulls file edits in, `reconcile --write` pushes entry edits back out (diff-and-refuse on conflict), without committing the file.
- **Git-native excision**: soft-delete (tombstone) or hard `purge` (`git gc` locally, `--push` to delete on the remote).

## Install

`kref` is a Go program. You need Go ≥ 1.26.4 to build it (or any Go with `GOTOOLCHAIN=auto`, which fetches the 1.26.4 toolchain over the network on the first build; `task build` sets this for you). It also needs the `betterleaks` binary at runtime (the Taskfile provisions a pinned copy; see below).

```bash
# from source
git clone <this-repo> && cd kref
task dev:tools # installs pinned tools (incl. betterleaks) into ./bin
task build     # builds ./bin/kref
export PATH="$PWD/bin:$PATH"   # put kref (and the pinned betterleaks) on PATH
kref --help
```

The `export PATH` line is what makes the examples below (which all call bare `kref`) runnable, and it puts the pinned `betterleaks` on `PATH` alongside it. Add it to your shell profile to persist it. Prefer a system path? `sudo install ./bin/kref /usr/local/bin/` works too, but then also put `betterleaks` on `PATH` (or set `KREF_BETTERLEAKS`), since kref only auto-discovers a `betterleaks` sitting next to its own binary.

Or, once the repository is public and a release is tagged, `go install` (then ensure `betterleaks` is on `PATH`, or set `KREF_BETTERLEAKS`):

```bash
go install github.com/trevor-vaughan/kref/cmd/kref@latest
```

Until then, use the source build above: `@latest` cannot resolve an unpublished/untagged module.

### Releases & supply chain

Tagged releases are built in CI by GoReleaser; nothing ships from a laptop. Each release carries cross-compiled archives (linux/darwin/windows on amd64/arm64) with a `checksums.txt`, an SPDX SBOM per archive (syft), and a Sigstore build-provenance attestation covering every checksummed artifact. Before running a downloaded binary you can prove it came from this repo's release workflow:

```bash
gh attestation verify kref_<version>_linux_amd64.tar.gz --repo trevor-vaughan/kref
```

The same posture applies to the code itself: every push runs the race-enabled test suite, golangci-lint, CodeQL, and govulncheck, plus a betterleaks scan of the repository's own history, all surfaced as SARIF in the Security tab. A shared [MegaLinter](https://megalinter.io/) profile ([`trevor-vaughan/megalint-config`](https://github.com/trevor-vaughan/megalint-config)) layers on YAML, shell, Markdown, and extra secret/IaC checks through the same SARIF pipeline: full-tree on `main` and weekly, diff-only on PRs.

## Quickstart

```bash
cd your-project           # any git repo

kref init                   # adopts your git user.name / user.email as the author

# The 90% path: point kref at markdown you already have. Each file becomes an
# entry (kept out of your working tree), with a kref-id trailer written back so
# re-ingesting is idempotent.
kref ingest docs/           # a whole tree (or one file: kref ingest docs/notes.md)
kref hooks install          # optional: re-ingest changed markdown on every commit
kref track docs/note.md     # keep one file two-way synced (reconcile pulls,
                            # reconcile --write pushes) — see "Tracking files"

# Compose an entry by hand when there is no file:
kref new --kind spec --body $'# Auth design\n\nprose...' --label area:auth  # title from H1
kref new --content-type application/json --title "Config schema" --body '{"k":"v"}'  # non-markdown
kref list                   # list entries across tiers (add --tier to filter)
kref search auth            # recall by a title/body substring, with match counts
kref list --label area:auth # recall by label (repeatable, AND)
kref show <id>              # view one — rendered and paged; --plain to get the stored body verbatim
kref show                   # ...or omit the id to see the most-recently-touched entry
kref edit <id>              # revise the body in your editor; kref update for non-interactive
kref status <id> accepted   # move it through open|active|accepted|superseded|obsolete
kref rm <id>                # soft-delete (tombstone; undo with kref restore)
```

`kref list` prints a header and a colour-coded visibility-tier column so you can see at a glance what is private vs shared:

```text
TIER        ID            KIND    STATUS  TITLE
● private   d22bdbc58f3f  memory  open    API key location
◐ personal  4179f614a5b3  adr     open    Use Postgres
○ shared    50ca0294f77e  spec    open    Auth flow spec

3 entries
```

Tiers are coloured (private = red `●`, personal = yellow `◐`, shared = green `○`); the glyph prints even with `NO_COLOR` or when piped, so the signal never depends on colour alone. Filter with `kref list --tier private`. Colour is auto-detected (on for an interactive terminal, off for pipes and under `NO_COLOR`); `KREF_COLOR=1` forces it on and `KREF_COLOR=0` forces it off, overriding both (handy for recording demos, or for colourising a pipe into a pager). `--json` output is never coloured.

`kref list` has three output modes: the default table, `--json` (full objects, the precise-timestamp path), and `--plain`, a tab-separated, header-less, colour-less mode for shells. `--plain` is a global flag with one meaning everywhere (chrome-free, line-oriented output for `grep`/`cut`/`xargs`), shared by `list`, `search` (TSV rows), and `show` (the verbatim stored body); on commands with nothing to strip it is a harmless no-op. On `list`, `--plain` honours every filter and lists each match uncollapsed, one per line, so it pipes cleanly into `grep`/`cut`/`xargs`. Choose columns with `--columns=a,b,c` (the `=` is required; any of `tier id fullid kind status title author email created updated edited labels tracked path source`, where `updated` is the last change of any kind, `edited` the last body change; `path` is the tracked-file path from `kref track`, `source` the provenance origin path an entry was ingested from), or use the `--wide` preset (`tier,id,kind,status,author,edited,title`). Run bare `kref list --columns` (no value) to print the full column list with descriptions. The common "give me the ids matching this filter" idiom is:

```bash
kref list --tier shared --plain --columns=id | xargs -n1 kref show
```

`--plain` also feeds bulk updates: `kref update` accepts multiple ids (or `--all`), so you can select with `list --plain` and apply one change to all of them. Only `--kind`/`--reset-author`/`--author` bulk-apply (per-entry content flags stay single-entry); `--all` confirms unless `-y`:

```bash
kref list --kind note --plain --columns=id | xargs kref update --kind reference
```

`--plain` and `--json` are mutually exclusive everywhere: they are the two machine contracts, and asking for both is a contradiction. `--columns` and `--wide` (`-w`) are list-local: not combinable with `--json` (already the full object) or `--new`, and mutually exclusive with each other; `--plain` is also not combinable with `--new`.

On an interactive terminal the table opens in a lean pager: the same scrolling and `/` search as `kref show`, but with no line-number gutter and no `<n>g` line jumps. Piped or redirected output prints straight through, `--plain` and `--json` never page, and `--no-pager` opts out on a terminal.

Sorting works the same across modes. `--sort <field>` reorders any of the three output modes (table, `--plain`, `--json`); fields are `tier id kind status title author created updated edited`. Bare keys sort ascending, except the date fields (`created`, `updated`, `edited`), which put the newest at the top; append `:asc`/`:desc` to override. The default is `--sort edited`: the entries whose body changed most recently first, in every output mode. `edited` tracks only body edits, so metadata churn (labels, links, status, retier) does not resurface an entry; use `--sort updated` for last-touched-by-anything order, or `--sort tier` to group by visibility. `kref search` takes the same flag plus `matches` (its default order is `matches:desc`).

`kref search <query>` finds entries whose title or body contains the query (case-insensitive) and shows how many times it occurs in each, most matches first:

```text
MATCHES  TIER        ID            KIND      TITLE
      3  ○ shared    50ca0294f77e  spec      Auth flow spec
      1  ◐ personal  4179f614a5b3  adr       Use Postgres

2 entries, 4 matches
```

It composes with the same `--kind`/`--status`/`--tier`/`--label` filters as `kref list`, pages on a terminal like `list` does, and under `--json` emits the full entry objects plus a `matches` field. `kref search <query> --plain` emits one tab-separated row per hit (matches, tier, id, kind, title) with no header or footer, for the same `grep`/`cut`/`xargs` pipelines as `list --plain`.

`kref show` renders entries before printing: the metadata is laid out as an aligned key/value table, markdown bodies are rendered with a rich terminal renderer and wrapped to your terminal width, recognized code and structured text (JSON, YAML, Go, Python, shell, etc.) is syntax-highlighted, and anything else prints verbatim. Rendered markdown also reflows: soft-wrapped source lines join back into full-width paragraphs, list items, and blockquotes (LLM-authored entries typically arrive hard-wrapped at ~78 columns); hard line breaks, code blocks, and tables are left untouched, and `--plain` returns the exact stored bytes. On an interactive terminal a full-screen pager opens automatically with a line-number gutter. Keys: `j`/`k` or arrow keys scroll; `ctrl+d`/`ctrl+u` page; `gg`/`G` jump to the top or bottom; `<n>g` jumps to a line; `/` searches (with `n`/`N` for next/previous match); `r` re-reads the entry from the store and re-renders in place (handy when an agent or a sync is updating the entry you are reading); `?` toggles the key-binding help; `q` quits. When output is piped or redirected (not a terminal), paging is skipped. Three flags control the output:

| Flag               | Effect                                                                                                         |
|--------------------|----------------------------------------------------------------------------------------------------------------|
| `--plain` (global) | emit the stored body verbatim, no header (the redirect/byte-fidelity form): `kref show --plain <id> > note.md` |
| `--no-header`      | omit the metadata block                                                                                        |
| `--no-pager`       | never page, even on an interactive terminal                                                                    |

Entries carry a MIME content type (default `text/markdown`). Set it at creation time with `kref new --content-type <type>` or change it later with `kref update --content-type <type>`. `kref ingest` detects the type from the file extension automatically: markdown files behave as before (a `kref-id` trailer is written back and the file is tracked); a non-markdown text file (`.json`, `.yaml`, `.go`, `.py`, `.sh`, etc.), when named explicitly, is stored content-only: no trailer is written and the file is not tracked (a directory argument is still walked for `*.md` only). Binary files are rejected. Supported types include `text/markdown`, `text/plain`, `application/json`, `application/yaml`, `application/toml`, `text/x-go`, `text/x-python`, `text/x-shellscript`, `text/javascript`, and `text/x-typescript`. The `content_type` field appears in `--json` output from `show` and `list`.

Labels are a free-form, multi-valued organization axis, orthogonal to the visibility tier. Attach them at `new` time (`--label`, repeatable), or with `kref label add|rm <id> <label>...`, and filter with `kref list --label` (repeatable, AND). Convention: `prefix:value` (e.g. `area:auth`, `project:kref`). They merge conflict-free across machines and show in `kref list` (`[…]`) and `kref show`.

Entries default to the `personal` tier (syncs only to *your* remote). Pass `--tier shared` to put an entry on the team remote, or `--tier private` to keep it on this machine only.

By default kref works on the repo you are in: like git itself, it walks up from the current directory to the enclosing repository, so `kref list` works from any subdirectory. Outside any repo it errors cleanly (`.git not found`). Every command also accepts `--dir <path>` to target a different repository explicitly. `--dir` only selects which repository's ref store is used; file *path arguments* (e.g. `ingest ./notes.md`) are still resolved against your current working directory, not against `--dir`. The quickstart flow `cd your-project` first keeps the two in agreement; if you drive a repo elsewhere via `--dir`, give path arguments as absolute paths (or `cd` into that repo) so a relative path is not read (or, for `ingest`, written) under the wrong tree. Output is human-readable by default; pass the global `--json` flag on any command for machine-readable output (stable, script-friendly shapes).

## Concepts

### Entries and tiers

An entry is a typed record (`--kind`, default `document`) with a title, an optional markdown body, a status, typed links to other entries, and author attribution. Each entry lives in one **tier**, selected with `--tier`:

| Tier       | Ref namespace          | Leaves the machine?                     |
|------------|------------------------|-----------------------------------------|
| `private`  | `refs/kref-private/*`  | **Never** (no remote can be configured) |
| `personal` | `refs/kref-personal/*` | Only to *your* configured remote        |
| `shared`   | `refs/kref-shared/*`   | To the team's configured remote         |

```bash
kref new --tier personal --kind memory --title "rotation cadence" --body "..."
```

The three built-ins are not the whole story: declare your own tier with `kref tier add <name> --type personal|shared [--remote <name> [--url <url>]]` and it behaves exactly like a built-in of that type: same ref namespace scheme (`refs/kref-<name>/*`), same sync, same secret gates; the glyph and color follow the type, the word is your tier name. Definitions live in machine-local git config (`kref.tier.<name>`), so each clone declares its own set. Reads *discover* undeclared namespaces from refs (a teammate's custom tier renders shared-typed instead of vanishing), but writes into a namespace are refused until you declare it. `kref tier rm` undeclares (refusing while the tier still holds entries unless `--force`); the refs are never deleted, the namespace just becomes undeclared and read-only again.

Retiering keeps an entry's identity. `kref retier <id> <tier>` moves an entry between any declared tiers without changing its id; links, labels, and provenance ride along, and a `retier` provenance event is recorded. Moving to a shared-typed tier rescans for secrets (fail-closed), confirms (`--yes` to skip), and warns about links to entries that stay below shared. Demoting an already-pushed entry warns honestly: it only stops future local sync and cannot retract what already left.

### Attribution

`kref init` adopts your git identity (`user.name` / `user.email`); override with `--name`/`--email`. The creating author is recorded on every entry (`CreatedBy`) and shown by `kref show`. Note that operations are attributed but not cryptographically signed; see [Limitations](#limitations).

You can override the author per shell or per repo without re-running `init`. The kref author is the *logical* author stamped on entry history; it is independent of who authors the underlying git objects (those always stay your default git identity). This matters when kref runs in a container or CI whose git identity isn't yours but you still want your name on the knowledge. Set it by precedence (highest first):

1. `KREF_AUTHOR_NAME` + `KREF_AUTHOR_EMAIL` (environment), per shell/container.
1. `kref.author.name` + `kref.author.email` (git config, read merged from global + local); set it once in your global `~/.gitconfig` and it follows you into every repo and mounted container:
   ```bash
   git config --global kref.author.name  "Your Name"
   git config --global kref.author.email "you@example.com"
   ```
1. The identity baked at `kref init` (the fallback).

Each source must supply both name and email or kref errors; it never mixes a name from one layer with an email from another. An override is resolved to a real, sync-resolvable identity (reused if it already exists), so attribution still propagates on push.

The "who am I" pointer is local and does not travel. The identity baked at `init` is stored in the repo's *local* git config, so it never rides along with the repo. Re-running `kref init` does not re-initialize or change it; it simply prints the current identity (use the overrides above to attribute differently). When you clone or download a repo from elsewhere, you inherit none of the origin's "user identity"; you `kref init` as yourself. Pulled entries keep their *original* authors (authorship travels with the entry), while any new entries you create are attributed to you.

The displayed author can be corrected after the fact (e.g. an entry ingested under the wrong identity) with `kref update`: `--reset-author` reattributes it to your current kref identity, and `--author "Name <email>"` to an explicit author. The two are mutually exclusive and need no other change, so `kref update <id> --reset-author` is valid on its own. Reattribution is an append-only operation authored by *you*, so it shows in `kref log` (`reattribute`) without rewriting the original `Create`: honest history, consistent with the unsigned model above.

### Provenance

Every `new`/`ingest` appends an append-only origin event (`{actor, actor_kind (human|agent), source_path, trigger, time}`) surfaced by `kref show` (`Origin: ingest by claude (agent) from docs/note.md`). Set `--actor`/`KREF_ACTOR` to mark an agent; otherwise the git identity is recorded as a human (self-asserted, like the git author: a context signal, not authz). Source paths are stored relative to the repository root (basename if a file is ingested from outside the repo), so an absolute local path never reaches the syncable log. Because the kref-id trailer ties a file to its entry, you can address an entry by the file it came from: `kref show ./docs/note.md`, `kref restore ./docs/note.md`.

### History & divergence

Edits never overwrite irrecoverably: every body edit is retained in the operation DAG. `kref log <id>` shows the full timeline (who changed what, when); each body edit is numbered (`v1`, `v2`, …) and carries a compact change summary (`v2  +318/-42 chars, +7/-2 lines`). `kref diff <id>` renders what changed between versions as an inline diff (additions green, removals red, unchanged lines as context): bare, it walks the whole chain (`v1`, `v1 → v2`, …); `kref diff <id> 3` shows just what v3 changed; `kref diff <id> 1 4` spans a range. Pass `--full` for the old whole-body view, the recovery path for a body a later edit superseded (`kref diff <id> --full`, copy the version out). When the same entry is edited on two machines and synced, kref forms a merge commit and flags the entry `◆ merged` in `kref show`/`kref list`. Review the divergent bodies with `kref diff`, settle on a final body with `kref edit`, then `kref resolve <id>` to acknowledge the merge and clear the flag. The flag means "an *unacknowledged* concurrent merge exists": a later divergence re-flags it. (Acknowledgement is itself synced; if two machines resolve concurrently the resulting merge re-flags once, so resolve again to settle.) Nothing is lost; nothing is silent.

### Hygiene & consolidation

`kref` is built to be written to freely and gardened periodically. The default `kref list` stays legible on its own: it hides `superseded` entries and collapses entries that share a normalized title (lowercased, whitespace-folded) into one row tagged `(×N)`. `kref list --all` shows everything, uncollapsed. The clean view is a presentation layer only: `kref list --json` always returns the full, uncollapsed set, so scripts and agents are unaffected.

Archiving retires an entry without deleting it: `kref archive <id>` hides it from the normal list (its status is preserved, so an `obsolete` entry stays `obsolete`), `kref list --archived` shows only the archived ones (tagged `(archived)`), and `kref unarchive <id>` brings it back. `kref archive --obsolete` archives every obsolete entry in one go, after a confirmation (`-y`/`--yes` skips it). Unlike `rm`/tombstone, archiving is a pure visibility flag, not a deletion.

`kref tidy` is a read-only review surface that clusters the likely-redundant: duplicate-title groups, `◆ merged` (diverged) entries, and superseded chains. Act on a cluster with `kref supersede <old> <new>`, which links the new entry to the old (`supersedes`) and marks the old one superseded so it drops from the default list. Relationships are inspected with `kref links <id>` (incoming and outgoing typed edges) and `kref tree <id>` (the parent-child hierarchy). For arbitrary relationships beyond supersede, `kref link add <id> <target> --type depends-on` creates a generic typed link (free-form `--type`, default `relates`) and `kref link rm` removes it; links are one-directional (the `kref links` viewer resolves incoming edges by scanning). Linking a more-public entry to a more-private one warns that the private entry's id rides along on push but proceeds, the same warn-not-block stance `kref retier` takes on cross-tier links when moving an entry toward a shared-typed tier. Duplicate detection is exact normalized-title matching; fuzzy and semantic similarity are deferred to a future search-index tier.

### The ingest bridge

```mermaid
%%{init: {'theme': 'base', 'themeVariables': {
  'primaryColor': '#2f6dab',
  'primaryTextColor': '#1e1e1e',
  'primaryBorderColor': '#7c8ba1',
  'lineColor': '#7c8ba1',
  'edgeLabelBackground': '#eef2f8',
  'tertiaryColor': 'transparent',
  'tertiaryTextColor': '#7c8ba1',
  'tertiaryBorderColor': '#7c8ba1',
  'clusterBkg': 'transparent',
  'clusterBorder': '#7c8ba1',
  'titleColor': '#7c8ba1',
  'noteBkgColor': '#eef2f8',
  'noteTextColor': '#1e1e1e',
  'fontFamily': 'system-ui, sans-serif'
}, 'themeCSS': '.node .nodeLabel{color:#ffffff!important;fill:#ffffff!important;}'}}%%
flowchart TD
  ingest["kref ingest"]
  scan["betterleaks scan"]
  ingest --> scan
  scan --> secret{"secret detected?"}
  secret -->|no| store["store / update entry in its tier"]
  store --> done["done"]
  secret -->|yes| marked{"file already kref-id mapped?"}
  marked -->|"no (unmarked)"| quarantine["quarantine new entry to private"]
  quarantine --> done
  marked -->|yes| tier{"mapped entry's tier?"}
  tier -->|private| safe["re-ingest stays private (safe no-op or update)"]
  safe --> done
  tier -->|"personal / shared"| failclosed["fail closed: ingest aborts, secret never reaches remote"]
  classDef sysA fill:#2f6dab,color:#ffffff,stroke:#7c8ba1
  classDef sysB fill:#1d7848,color:#ffffff,stroke:#7c8ba1
  classDef sysC fill:#7457b8,color:#ffffff,stroke:#7c8ba1
  class store,safe sysA
  class quarantine sysB
  class failclosed sysC
```

`kref ingest <path>...` reads file(s), runs a betterleaks scan, and stores each as an entry. A directory argument is walked recursively for `*.md` only. To ingest a non-markdown file, name it explicitly (e.g. `kref ingest config.json`). How a named file is handled depends on its type:

- **Markdown** (`.md`): on the first ingest kref stamps a `<!-- kref-id: … -->` trailer into the file; a later ingest of that file updates the same entry instead of creating a duplicate (an unchanged file is a no-op). This is the only type a directory walk picks up.
- **Other text** (`.json`, `.yaml`, `.go`, `.py`, `.sh`, `.toml`, `.ts`, etc., named explicitly): stored *content-only* with a content type detected from the extension: no trailer is written and the file is not tracked. Re-ingest creates a new entry each time.
- **Binary**: rejected with an error.

If a secret is detected in an *unmarked* file the entry is quarantined to `private`; in an already-mapped file that lives in a syncable tier (`personal`/`shared`) ingest fails closed, so the secret never reaches that tier's remote; rotate it and `kref purge <id> --gc`, then re-ingest. A file already quarantined into `private` is the exception: because `private` structurally cannot push, re-ingesting it stays safe and re-runnable (unchanged → no-op; edited → updates the still-private entry), so re-running `kref ingest <dir>` over a tree that quarantined a file does not error.

If betterleaks flags prose that is not a real secret (e.g. design notes that quote a token format), supply a custom config or allowlist via the `BETTERLEAKS_CONFIG` environment variable (its gitleaks-compatible `GITLEAKS_CONFIG` is also honored); kref passes the environment through to betterleaks, so an allowlisted pattern is no longer quarantined. An entry that was already quarantined into `private` and that you have confirmed is a false positive can be moved out directly with `kref retier <id> shared`; the ingest summary prints this hint whenever it quarantines a file. `--kind <kind>` sets the kind on new entries (default `document`); a re-ingest with `--kind` re-kinds the entry, while a kind-less ingest leaves an existing entry's kind untouched, so the kind-less post-commit hook never reverts a kind set via `kref update --kind` or an earlier `kref ingest --kind`. The same betterleaks scan guards `kref update --file` (file-sourced bodies); typed `--body`/stdin content is not scanned. `--skip-missing` skips paths that do not exist. Entries live in git refs (`refs/kref-{private,personal,shared}/*`), not on disk. To keep a file and its entry in sync *after* the first import (rather than re-running `ingest` by hand), track it (next).

### Tracking files

`ingest` is one-shot: it imports a file's content, but the file and the entry then drift independently. Tracking keeps a chosen file and its entry in sync over time, in either direction.

```bash
kref track docs/note.md      # ingest it, then mark the entry tracked
kref reconcile docs/note.md  # pull: re-read the file into its entry
kref reconcile               # ...or sweep every tracked file (asks to confirm)
```

`kref track <path>` ingests the file and records the link. A file inside the repo is tracked in place; a *floater* (a path outside the repo) is copied under `.kref/<name>` and tracked there; the original is never moved. `.kref/` is ignored locally through `.git/info/exclude` (set up by `kref init`), so it stays out of the tracked tree without a committed `.gitignore`. `kref untrack <id|path>` stops syncing and leaves the file on disk.

`kref reconcile` pulls (file → entry): it re-reads each tracked file and updates its entry when the file changed (idempotent; a moved file self-heals via its trailer, a deleted file is skipped, a secret fails closed unless `--force`). By default it never writes files. Addressing a tracked file by path (`kref reconcile docs/note.md`) resolves it through its `kref-id` trailer, but falls back to the stored tracked-path mapping if that trailer is gone (e.g. a markdown formatter stripped the HTML comment), so the path form works wherever the sweep form does. `kref reconcile --write` pushes the other way (entry → file): when the file is a safe fast-forward (its content is a past version of the entry), the entry's body is written back out. If the file has diverged (holds edits the entry never saw), reconcile prints a unified entry-vs-file diff and refuses, so you can pull (`reconcile`) or overwrite with `--write --force`. Writing files is the only destructive direction, so it is opt-in and, in a sweep, gated behind a confirmation.

Drift is visible without syncing: `kref show` shows a `Tracked  <path> [in-sync|drifted|missing]` row in the metadata header, `kref list --check` flags drifted tracked entries, and `kref reconcile --dry-run` reports what would change (with diffs under `--write`) without touching anything.

### Sync

Tiers map to git remotes via local git config (`kref.remote.<tier>`). The private tier can never be given a remote.

```bash
kref remote set shared origin git@github.com:you/team-kref.git
kref remote                    # list every tier's remote (alias: kref remote list)
kref remote get shared         # print one tier's remote; errors when unconfigured
kref sync push                 # push all syncable tiers (private is skipped)
kref sync pull --tier shared   # or one tier
```

Custom tiers sync exactly like the built-ins: wire the remote with `kref remote set <tier> ...` after the fact, or in one step at declaration time with `kref tier add <name> --type shared --remote <name> --url <url>`. `kref sync push`/`pull` iterate every declared tier that has a remote, custom or not.

Sync moves the author identity alongside the entries, so teammates can resolve who wrote what. Merges are conflict-free (Lamport-ordered operation DAGs).

You can choose where each tier syncs. A tier's remote is an ordinary git remote, so the layouts below are all plain git plumbing; pick per tier, mix freely:

- **`shared` → the project repo itself** (`kref remote set shared origin`). Easiest: whoever can pull the code can pull the knowledge; nothing new to provision. The flip side: anyone with read access to the repo can read the shared tier: right for team-internal projects, wrong if the repo is public or widely mirrored.
- **`shared` → a separate, restricted repo** (`kref remote set shared team-kb git@github.com:org/project-kb.git`). The knowledge base gets its own (tighter) access control: contractors can clone the code without seeing design history; a public project keeps its spec discussions private. Costs one extra repo to create and grant.
- **`personal` → your own mirror** (`kref remote set personal me git@github.com:you/project-notes.git`). Your memories and drafts follow you across machines (laptop ⇄ desktop ⇄ devcontainer) without touching the team's remotes. A private fork or any bare repo you own works.
- **`personal`/`shared` → a bare repo on a filesystem you already trust** (`kref remote set personal vault /mnt/nas/kref/project.git`, after `git init --bare` there). No forge account involved: right for air-gapped setups or a NAS-backed home lab; your backup story is whatever already backs that filesystem.
- **`private` → nowhere, ever.** Not a layout choice: the private tier refuses a remote by construction. Off-machine safety for it is `kref bundle export` / `kref vault backup` (see [Backing up private knowledge](#backing-up--recovering-private-knowledge)).

Whatever the layout, `kref remote` shows the current map at a glance, and anything already pushed must be treated as disclosed to whoever can read that remote; moving an entry to a tighter tier later stops *future* syncs, it cannot retract copies.

`kref sync push` is a secret boundary. Before any content leaves the machine, push scans the delta about to leave (every body version of each new or changed entry) with betterleaks, and fails closed on a hit (the push is aborted before the remote is contacted; the offending entry id and rule are reported, never the secret value). Because the DAG retains full history, a secret added and later edited away is still caught; the fix is to rotate it, `kref purge <id> --gc`, recreate the entry clean, and re-push. A successful push records per-entry pushed-state (local `refs/kref-pushed/*` bookkeeping that never leaves), so later pushes re-scan only the new delta.

After syncing, `kref list --new` shows two groups: *incoming* (entries your last `sync pull` brought from teammates) and *unpushed* (entries you changed since your last push). `kref log <id> --since-pull` shows just the ops you added to an entry after the last pull.

### Backing up & recovering private knowledge

The `private` tier never has a remote, so it lives only in this repo and would be lost if the repo/disk dies. Two local-only recovery paths fill that gap (neither ever touches a network remote):

```bash
# Portable bundle — your cross-machine / re-clone path. Keep the file wherever.
kref bundle export --tier private private.bundle
kref bundle import --tier private private.bundle   # into a fresh clone (authors preserved)

# Local vault — same-machine convenience under $XDG_DATA_HOME (not cache).
kref vault backup     # mirror private to ~/.local/share/kref/<repo>/private.bundle
kref vault restore    # bring it back after an rm -rf or a bad purge
```

`bundle export`/`import` take any tier(s) via repeatable `--tier` (default: all), and read/write `-` for stdin/stdout, so an imported entry keeps its original author, and you can encrypt a backup by composing with an external tool:

```bash
kref bundle export --tier private - | age -r AGE_RECIPIENT > private.age
age -d private.age | kref bundle import -
```

Bundles and the vault are unencrypted (the live `.git` refs are too). Native encryption at rest is a deferred decision, captured as the *Encryption at rest for the private tier* ADR in kref's own store (`kref list --kind adr`); candidates are [SOPS](https://github.com/getsops/sops) and [age](https://github.com/FiloSottile/age).

### Hooks

Couple kref to git's lifecycle with [lefthook](https://lefthook.dev). lefthook is not bundled, so install it first (`go install github.com/evilmartians/lefthook@latest`, or your package manager).

```bash
kref hooks install     # writes/merges .lefthook.yml: post-merge/checkout -> sync pull,
                     # pre-push -> sync push, post-commit -> ingest changed markdown
                     # under docs/superpowers/plans, specs, .specify, openspec.
                     # Hooks call kref by ABSOLUTE PATH; --force MERGES into an
                     # existing .lefthook.yml (your other hooks are preserved).
lefthook install     # REQUIRED: register the hooks into .git/hooks
kref hooks print       # print the config instead of writing it
```

`kref hooks install` only writes `.lefthook.yml` (its output reports `"status": "written"`); the hooks stay dormant until `lefthook install` registers them into `.git/hooks`. Run `lefthook install` again after any edit to `.lefthook.yml`.

The generated hooks invoke kref by absolute path (so the hook finds the same `betterleaks`-sibling binary regardless of the committer's `PATH`). The trade-off: if you move or reinstall kref to a new path, the registered hooks point at the old location until you re-run `kref hooks install` (and `lefthook install`).

Override the watched directories with repeatable `--ingest-path` flags (default: `docs/superpowers/plans specs .specify openspec`):

```bash
kref hooks install --ingest-path docs/plans --ingest-path adr
```

### Configuration & favorites

kref reads two config layers, user over project. The user file at `$XDG_CONFIG_HOME/kref/config.yaml` (usually `~/.config/kref/config.yaml`) is a sparse override that lives only on your machine. It layers over a shared project entry, a `kind:config` entry with the reserved name `kref.conf`, found by kind and synced with whatever tier it lives in. The user file wins key by key; anything it omits falls through to the project entry, then to built-in defaults.

```bash
kref config              # the effective (merged) config; add --json for a machine-readable object
kref config init         # write the user file template (--force overwrites, backing up to .bck)
kref config init --shared  # instead create the shared kref.conf project entry (--tier picks its tier)
kref config check        # validate the effective config; report schema version and betterleaks status
kref config edit         # edit the user file in $EDITOR, validating before save (visudo-style)
kref config migrate      # migrate the shared kref.conf entry to the current schema
```

The user file auto-migrates on load: an out-of-date file is upgraded in place, with the original saved to `.bck`. The shared entry never auto-migrates (it is team-visible state); bring it forward deliberately with `kref config migrate`.

The trust model is deliberate. `trusted_keys` is honored only from the user file and gates which keys a shared `kref.conf` may set on you, so a teammate cannot push config you did not opt into. It defaults to `[favorites, warn_unscanned]`; `scanners` is deliberately not trusted by default, so a shared entry can never silently reconfigure secret scanning on your machine.

`warn_unscanned: false` silences the "stored UNSCANNED" advisory that appears when betterleaks is absent. It only quiets the advisory; it never relaxes the `kref sync push` secret boundary, which always scans and fails closed regardless of config.

Favorites give an entry a memorable name usable anywhere an id is accepted (`kref show <name>`, `kref diff <name>`, …). A name must contain a non-hex character so it can never shadow an id.

```bash
kref fav add a1b2c3d4 todo     # name an entry — id first, so `add <TAB>` completes the entry (alias: kref alt)
kref fav ls                    # list favorites from both layers, tagged (user)/(shared) — also the bare `kref fav`
kref show todo                 # resolve the favorite anywhere an id goes
kref fav rm todo               # remove it (`rm <TAB>` completes existing favorite names)
kref fav add a1b2c3d4 release --shared   # write to the shared kref.conf entry (which must already exist)
```

Favorites default to your user file; `--shared` (on `add`/`rm`) reads and writes the shared `kref.conf` project entry instead, so the whole team resolves the same name; create it first with `kref config init --shared`.

### MCP server

`kref mcp` runs a [Model Context Protocol](https://modelcontextprotocol.io) server over stdio, exposing a curated set of agent tools over the same store the CLI uses: `kref_remember`, `kref_recall`, `kref_get`, `kref_update`, `kref_patch`, `kref_lifecycle`, `kref_supersede`. `kref_lifecycle` covers the reversible document lifecycle (set_status, delete/restore via tombstones, archive/unarchive); `purge` (irreversible) and `retier` (a disclosure-sensitive move) are deliberately not exposed to agents. `kref_patch` is the agent editor, and it is deliberately MCP-only (no CLI equivalent; a human edits with `kref edit`): it applies a standard unified diff to the entry body, the format LLMs emit natively. The applier is lenient where models are sloppy and strict where safety demands it: hunk line numbers are hints only (each hunk is located by its context lines, matched exactly or up to trailing whitespace, and hunks apply in document order), while a hunk whose context is missing (stale diff) or ambiguous (identical sections, no usable line hint) fails loudly, all-or-nothing, so a patch never half-applies or silently lands in the wrong place. Each successful patch is one new body version. Point an agent host at it per repo:

```json
{ "mcpServers": { "kref": { "command": "kref", "args": ["--dir", "/path/to/repo", "mcp"] } } }
```

Shell-capable agents mostly don't need it (they already have `--json` on every command), but `kref_patch` is the exception worth wiring in: fine-grained edits exist only on the MCP surface. MCP writes are recorded as agent provenance.

### Agent instructions

`kref agents_md` prints a canonical policy block for your global `AGENTS.md`/`CLAUDE.md`, the instruction layer that outranks skills, so it can override other skills' file-writing defaults (plans, specs, and handoffs become kref entries instead of worktree files). `kref agents_md --skill` emits a complete `SKILL.md` driving manual for skill-loading agent hosts. The text ships in the binary, so it always matches the installed version's commands; regenerate after upgrades:

```bash
kref agents_md >> ~/.claude/CLAUDE.md   # or your global AGENTS.md
kref agents_md --skill > ~/.claude/skills/kref/SKILL.md
```

### Deleting things

| Command                       | Effect                                                         | Safe for secrets? |
|-------------------------------|----------------------------------------------------------------|-------------------|
| `kref rm <id>`                | Soft tombstone; undo with `kref restore <id>`; op-DAG retained | **No**            |
| `kref restore <id>`           | Un-tombstone a soft-deleted entry                              | n/a               |
| `kref purge <id>`             | Remove the ref (local, irreversible)                           | Locally yes       |
| `kref purge <id> --gc`        | …and run repo-wide `git gc --prune=now` to excise objects now  | Locally yes       |
| `kref purge <id> --gc --push` | …and delete the ref on the tier's remote                       | Best effort\*     |

`purge` prompts with the full entry and caveats by default; `--force` skips the prompt. **`--gc` runs `git gc --prune=now` across your whole repository:** it prunes every unreachable object, including unrelated dangling commits or dropped stashes, so use it deliberately (it is the right choice when excising a secret). \*Anything already pushed must be assumed compromised, so **rotate the secret**.

## Command reference

### Help depth

`kref help` and `kref --help` adapt to where output goes. On an interactive terminal you get the concise, grouped command list. When stdout is a pipe or redirect (which is what an automated agent sees), `kref` prints the full recursive tree instead: every command and subcommand, their flags and examples, plus a preamble covering the global flags (`--json`, `--plain`, `--dir`, `--actor`) and the JSON output / exit-code contract.

Force either depth explicitly on the `help` command:

```
kref help --long       # full tree, regardless of terminal (alias: -l)
kref help --short      # concise list, regardless of pipe (alias: -s)
kref help sync --long  # full help scoped to one command's subtree
```

`--long` and `--short` cannot be combined.

```
kref init [--name --email]                    initialize a store + author identity (re-run prints the existing identity; never re-inits)
kref new [--title --kind --body --tier --label --content-type]  create a new entry (title derived from the body if omitted; --label repeatable; --content-type sets MIME, default text/markdown)
kref ingest <path>... [--kind --tier --skip-missing]  ingest files/dir(s) — markdown: trailer written, entry tracked; non-markdown text: stored with auto-detected type, no trailer; binary: rejected
kref track <path>                             ingest a file and keep its entry synced with it
kref untrack <id|path>                        stop syncing an entry with its file (file left on disk)
kref reconcile [<id|path>] [--write --force --dry-run -y]  pull file→entry (default), or push entry→file (--write)
kref update <id|path>... | --all [--body --file --title --kind --content-type --reset-author --author] [-y]  update body/title/kind/author/content-type (set is an alias). Multiple ids or --all bulk-apply --kind/--reset-author/--author (content flags are single-entry only); --all confirms unless -y
kref edit <id>                                edit the body in your editor (KREF_EDITOR>VISUAL>EDITOR>vi; title re-derives from the H1)
kref status <id> <status>                     set status: open|active|accepted|superseded|obsolete
kref supersede <old> <new>                    mark <old> superseded by <new>
kref link add|rm <id|path> <target> [--type]  create or remove a generic typed link (default relates)
kref retier <id|path> <tier> [--yes]          move to any declared tier (mv is an alias)
kref tier add <name> --type personal|shared   declare a custom tier (--remote/--url to wire sync)
kref tier rm <name> [--force]                 undeclare a custom tier (refs stay)
kref tier list                                all tiers: type, remote, declared state
kref label add|rm <id> <label>...             attach or remove labels
kref list [--kind --status --tier --label --include-deleted --archived --all --new] [--plain --columns=<a,b,c> --wide/-w --sort <field>[:asc|:desc] --no-pager]  list entries, paged on a terminal (--archived: only archived; --new: changes since last sync; --plain/--columns/--wide: shell-friendly columns, never paged; bare --columns lists available columns; --sort: order by tier|id|kind|status|title|author|created|updated|edited — dates newest-first, default: edited (last body change; updated = last change of any kind); --no-pager: print straight through)
kref search <query> [--kind --status --tier --label --plain --sort <field>[:asc|:desc] --no-pager]  case-insensitive title/body substring search; shows how many matches each entry contains, most first (--plain: TSV matches/tier/id/kind/title, no chrome; --sort adds `matches` to the list fields)
kref show [<id|path>] [--plain --no-header --no-pager]  show an entry rendered and paged; --plain emits the stored body verbatim with no header (useful for redirect: kref show --plain <id> > note.md); --no-header omits the metadata block; --no-pager disables the pager even on a terminal
kref log <id|path> [--since-pull]                     show the operation history with numbered body versions and +/- change stats (alias: audit; --since-pull: your post-pull edits)
kref diff <id|path> [[<from>] <to>] [--full --no-pager]  inline colored diff between body versions (bare: the whole chain; one number: what vN changed; two: a range); --full prints whole bodies (recover a superseded one); paged on a terminal
kref resolve <id|path>                               acknowledge a concurrent merge (clears ◆ merged)
kref links <id|path>                                 show incoming and outgoing links
kref tree <id|path>                                  show the parent-child tree
kref tidy                                            review duplicates, diverged, and superseded entries
kref rm <id|path>                                    soft-delete (tombstone)
kref restore <id|path>                               restore a tombstoned entry
kref archive <id|path>                               hide an entry from the normal list (keeps its status)
kref archive --obsolete [-y]                          archive every obsolete entry (confirms unless -y/--yes)
kref unarchive <id|path>                             return an archived entry to the normal list
kref purge <id> [--force --gc --push]         hard-delete
kref remote [list]                            show every tier's remote config (bare `kref remote` = list)
kref remote get <tier>                        print one tier's remote (errors if unconfigured or private)
kref remote set <tier> <name> [url]           configure a tier's remote
kref sync push|pull [--tier] [--force]        sync tiers with their remotes (push --force: proceed UNSCANNED when the scanner is missing; found secrets still block)
kref bundle export|import [--tier] [<file>]   portable git bundle of entries (- = std stream; default all tiers)
kref vault backup|restore                     mirror the private tier to/from a local, machine-only vault
kref hooks install|print [--force]            lefthook wiring (--force merges)
kref mcp                                       run an MCP server (stdio) exposing kref tools
kref agents_md [--skill]                       print agent guidance: an AGENTS.md policy block, or a full SKILL.md with --skill
kref version                                  print the version
kref completion <shell> [--install --dir]     print a shell completion script (or write it with --install)
```

`--json` is a global flag accepted by every command; the entry, lifecycle, and sync commands are human-readable by default and switch to JSON under `--json`. `kref version` follows the same rule: it prints a plain `kref <version>` line (identical to `kref --version`) by default, and `{"version": "…"}` under `--json`. (`kref hooks print` emits its lefthook config directly regardless.) Under `--json`, a command failure is also machine-readable: the error is written to stderr as a single-line `{"error": "..."}` envelope (plain `error: <msg>` without `--json`), so a script never has to parse two formats. `--plain` is the other global machine mode: chrome-free, line-oriented text (TSV for `list`/`search`, the verbatim stored body for `show`), never colored, never paged; `--plain` and `--json` are mutually exclusive. `--dir <path>` and `--actor <name>` are likewise global; `--actor` (or `KREF_ACTOR`) attributes actions to an agent in the provenance log; absent, the git identity is recorded as a human.

`kref edit` opens the body in an external editor resolved in order from `KREF_EDITOR`, then `VISUAL`, then `EDITOR`, falling back to `vi`. (The value is split on spaces, so `KREF_EDITOR="code --wait"` works.)

**Aliases** (syntactic sugar; the canonical name above is what the docs use): `new` = `create`, `ingest` = `import`/`add`, `show` = `cat`/`view`/`get`, `list` = `ls`, `rm` = `remove`/`delete`/`del`, `purge` = `destroy`, `remote` = `remotes`, `version` = `ver`.

## Shell completion

Print the completion script for your shell, or write it straight to the shell's standard completion directory with `--install`:

```bash
kref completion bash --install   # ~/.local/share/bash-completion/completions/kref
kref completion zsh  --install   # ~/.local/share/zsh/site-functions/_kref  (must be on fpath)
kref completion fish --install   # ~/.config/fish/completions/kref.fish
```

Without `--install` the script goes to stdout. `--install` honors `$XDG_DATA_HOME`/`$XDG_CONFIG_HOME`; pass `--dir <path>` to write somewhere else (handy for a zsh `fpath` that omits the default). For zsh, `--install` also prints the `fpath` line to add when the directory is not already on it. kref never edits your shell rc files. PowerShell is print-only; add the output to your `$PROFILE`.

Once installed, completion knows what each command takes, so a `<TAB>` offers the right thing instead of a stray directory listing:

- **Entry ids** on every command that takes one (`show`, `rm`, `edit`, `update`, `log`, `diff`, `status`, `retier`, `supersede`, `link`, …), listing each id beside its title so you can pick from the list. `restore` offers only soft-deleted entries and `unarchive` only archived ones, the entries those commands actually accept. Typing a `/` or `.md` prefix completes file paths instead, matching the commands' ability to address an entry by its file.
- **Fixed vocabularies** where they apply: `kref status <id> <TAB>` → `open active accepted superseded obsolete`; `kref retier <id> <TAB>` and `--tier` → `private personal shared`.
- **Your own values** for `--kind` and `--label` (`kref list --kind <TAB>`), drawn from the entries in your store, so completion answers "what do I have?". `kref ingest --kind <TAB>` and `kref track --kind <TAB>` do the same, falling back to `document` (the flag's default) in a fresh store that has no kinds yet.
- **Command aliases** as first-word completions: `kref imp<TAB>` offers `import` (an alias of `ingest`), and a bare `kref <TAB>` lists aliases like `ls`, `cat`, and `new` beside the canonical names.
- **Column names** for `kref list --columns=<TAB>`, comma-aware: after `--columns=id,<TAB>` it offers the remaining columns (each with its description) and keeps the cursor in place for chaining. Use the `=` form: a bare `--columns` means "list the available columns", so it never consumes the next word.

Commands that take no argument (`list`, `new`, `tidy`, `sync push`, …) complete their flags rather than falling back to a file listing.

## betterleaks (runtime dependency)

kref uses [betterleaks](https://github.com/betterleaks/betterleaks) as its secret scanner: a drop-in successor to gitleaks by gitleaks' original author, sharing its v8 report schema and CLI flags. The `ingest` and `sync push` paths shell out to `betterleaks` (on the way in, and on the delta about to leave). `task test` provisions a pinned betterleaks into `./bin` via its `dev:tools` dependency and points kref at it through `KREF_BETTERLEAKS` (run `task dev:tools` directly to provision it for a plain `task build`). At runtime, kref resolves betterleaks in order: `KREF_BETTERLEAKS` (if set), then a `betterleaks` next to the kref binary (so the source build's `./bin/kref` + `./bin/betterleaks` layout works with no extra setup, as do the lefthook hooks, which call kref by absolute path), then `betterleaks` on `PATH`. Pin version: `BETTERLEAKS_VERSION` in `Taskfile.yml`.

kref forwards its environment to betterleaks, so a custom config `.toml` (which can carry an `[allowlist]`) set via `BETTERLEAKS_CONFIG` (or its gitleaks-compatible `GITLEAKS_CONFIG`) is honored for both the `ingest` and `sync push` scans: the escape hatch for prose that trips a rule without being a real secret.

When betterleaks cannot be found, kref degrades by surface rather than failing everything: `ingest`, `track`, `reconcile`, and `update --file` proceed with a loud warning (the content is stored/pulled unscanned, flagged `unscanned` in `--json` output, and nothing can be quarantined), while `kref sync push` stays fail-closed: content that was never scanned is not allowed to leave the machine, so the push errors with an install hint instead. `kref sync push --force` overrides that one refusal: the delta leaves UNSCANNED with a loud warning, and it is not re-scanned later (it already left). `--force` never overrides a *positive* finding: with a working scanner, a detected secret still blocks the push. Install betterleaks with `go install github.com/betterleaks/betterleaks@latest` (or point `KREF_BETTERLEAKS` at a binary). Note that a plain `go install .../kref@latest` cannot provision betterleaks for you (`go install` builds exactly one binary and has no post-install hooks), hence the two-step install above.

## Uninstall

`kref` keeps all of its state inside the repository (git refs and a few git config keys) with no `$HOME` footprint, so removing it is per-repo and there is no `kref uninstall` command. To excise it from a repo:

```bash
# 1. Delete kref's ref namespaces (entries — irreversible; the objects are
#    reclaimed by a later `git gc`).
git for-each-ref --format='%(refname)' \
  'refs/kref-private/*' 'refs/kref-personal/*' 'refs/kref-shared/*' 'refs/kref-pushed/*' \
  | xargs -r -n1 git update-ref -d

# 2. Drop kref's git config keys (discover them first, then unset each).
git config --get-regexp '^kref\.' | cut -d' ' -f1 | xargs -r -n1 git config --unset

# 3. If you wired hooks, deactivate them and drop kref's entries from .lefthook.yml.
lefthook uninstall            # if you ran `lefthook install`
#   then delete the `kref-*` commands from .lefthook.yml (or remove the file).

# 4. Drop the `.kref/` line `kref init` added to .git/info/exclude (it is
#    local-only and never committed), and delete the `.kref/` directory if
#    tracking copied any floater files into it.
```

Finally delete the binaries (`./bin/kref`, `./bin/betterleaks`) and any copy or symlink you placed on `PATH`.

## Limitations

This is a first release; some things are deliberately deferred (see `docs/dev/` and the design spec):

- **No cryptographic signing.** Operations are attributed by git identity but unsigned: git-bug v0.10.1 exposes no API to equip an identity with a signing key, and cannot use your system GPG/gpg-agent. Attribution is therefore forgeable.
- **No encryption at rest.** The `private` tier stays local but is not encrypted on disk.
- **No semantic search.** A derived vector index is planned, not built.

## Development

See [`docs/dev/`](docs/dev/) for architecture, the design spec, and implementation plans. Quick loop:

Common tasks are aliased at the root (`task --list` shows everything); the `dev:` and `deps:` namespaces hold the development loop and Go-module management respectively.

```bash
task dev:tools     # pinned betterleaks, ginkgoleaf, and golangci-lint into ./bin
task test          # full Ginkgo suite (task test MODE=llm for errors-only)
task lint          # go vet + gofmt check + golangci-lint (same pin as CI)
task build         # ./bin/kref with embedded version
task dev:test:e2e  # unit + end-to-end suites (slower)
task check         # fmt + lint + e2e
task dev:demo      # re-render the README demo GIFs into docs/demo (needs vhs, ttyd, ffmpeg)
task clean         # remove ./bin, the built binary, and .test-output
task deps:upgrade  # bump module deps to latest minor/patch, then tidy + verify
```

## License

[GPL-3.0](LICENSE), inherited from git-bug, which `kref` links against.
