# Changelog

All notable changes to `kref` are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-08-19

First release. Everything below is new, so this entry describes what `kref`
does rather than what changed — release-to-release `Changed`/`Fixed`/`Removed`
entries begin with 0.2.0, once there is a published version to diff against.

`kref` stores specs, ADRs, plans, memories, and reference notes inside a git
repository as git objects under their own ref namespaces — not in the working
tree and not on `main`. Entry bodies travel with the repo through clone, push,
and pull without touching your file tree, `git log`, or `git blame`.

### Added

#### Storage model

- Repo-resident knowledge base stored as git objects under `refs/kref-<tier>/*`,
  built on [git-bug](https://github.com/git-bug/git-bug)'s `entity/dag`. Every
  entry is a Lamport-ordered DAG of operations that merges conflict-free across
  machines and teammates.
- **Three visibility tiers** — `private` (structurally unpushable; refused at
  every layer, so it never leaves the machine), `personal`, and `shared` — as
  separate ref namespaces, each with its own remote.
- **Custom tiers**: declare any number of personal- or shared-typed tiers with
  `kref tier add <name> --type … [--remote …]`. Reads discover undeclared
  namespaces from refs, so a teammate's tier renders rather than vanishing.
  Colors and glyphs follow the tier's type.
- `kref retier <id> <tier>` (alias `mv`) is the single movement verb between
  tiers, with a fail-closed secret gate on any shared-typed destination. Entry
  ids are stable across a move.

#### Entries

- **Typed entries** — `spec`, `adr`, `plan`, `memory`, `reference`, `document`
  (plus any free-form `--kind`) — each with a status
  (`open`/`active`/`accepted`/`superseded`/`obsolete`), labels, typed links,
  supersede/merge tracking, and append-only provenance.
- Lifecycle verbs: `new`, `update`/`edit`, `status`, `rm`/`restore` (reversible
  tombstones), `archive`/`unarchive` (hide without deleting;
  `archive --obsolete` and `archive --accepted` bulk-archive a status), and
  `purge` (irreversible; `--gc` to excise objects now, `--push` to delete the
  ref on the remote).
- `kref new` takes its body from `--body`, else `--file <path>`, else piped
  stdin. A `kref-id` trailer in a `--file` body is stripped, so re-creating from
  an exported body does not bake the marker in.
- **Entries carry a MIME content type** (default `text/markdown`), settable with
  `--content-type`: `text/plain`, `application/json`, `application/yaml`,
  `application/toml`, `text/x-go`, `text/x-python`, `text/x-shellscript`,
  `text/javascript`, `text/x-typescript`. Binary content is rejected.
- **Attribution and provenance** — git identity propagates alongside entries and
  can be corrected with `update --reset-author`/`--author`. Every `new`/`ingest`
  appends an origin event (actor, human-vs-agent, source path).
- **History** — `kref log` numbers body versions (`v1`, `v2`, …) with a per-edit
  change summary (`+318/-42 chars, +7/-2 lines`). `kref diff` renders inline
  colored diffs; `kref diff <id> <n>` shows one version's change, a range spans
  versions, and `--full` gives the whole-bodies recovery view.
- **Divergence is surfaced, never silent** — an entry edited on two machines
  merges conflict-free and is flagged `◆ merged` until `kref resolve` acks it.
- **Hygiene** — `kref tidy` clusters likely-redundant entries (duplicate titles,
  merged entries, superseded chains); `kref supersede <old> <new>` links and
  retires; `kref link add/rm` creates free-form typed edges; `kref tree` shows
  the parent-child hierarchy.
- **Concurrent-write safety** — every entity write takes a per-repo advisory
  lock (`flock` on `.git/kref/write.lock`) across the whole read-modify-write,
  so two kref processes on one checkout cannot lose an operation. Contention
  retries briefly before erroring; reads are never blocked.

#### Finding and reading

- `kref list` prints a static table with a color-coded tier column and a glyph
  that survives `NO_COLOR` and pipes, filtered by `--tier`, `--kind`,
  `--status`, `--label` (repeatable, AND), `--open-questions`, `--archived`,
  `--all`, `--include-deleted`, and `--new` (what arrived or went unpushed since
  your last sync). `--columns=…`/`--wide` select columns and `--check` flags
  drifted tracked files.
- `kref search <query>` counts occurrences per entry (title + body,
  case-insensitive), most matches first, with a `N entries, M matches` footer.
- **Sorting** — `--sort <field>[:asc|:desc]` over
  `tier|id|kind|status|title|author|created|updated|edited` (`search` adds
  `matches`). Date fields put newest first by default. `kref list` defaults to
  `--sort edited`, so metadata churn (labels, links, status, retier) does not
  resurface an entry whose prose is unchanged; `--sort updated` gives
  last-touched-by-anything order.
- **An `edited` timestamp** distinct from `updated`, derived from the entry's
  `SetBody` operations, so it needs no migration and reads correctly for
  existing entries.
- **Favorites pin to the top** — entries with a favorite name float above the
  rest in every output mode, with the active sort applied within the favorite
  and non-favorite groups. `kref fav add <id> <name>`, `kref fav rm`, and a bare
  `kref fav` listing.
- `kref show` renders before printing: aligned metadata, rich markdown, syntax
  highlighting for recognized code and structured text, and everything else
  verbatim. Rendered markdown **reflows** soft-wrapped source to the display
  width — hard breaks, code blocks, and tables are left untouched. Outgoing and
  incoming typed links appear in the metadata header, capped at ten so a hub
  entry cannot push its own body off screen. Omit the id for the
  most-recently-touched entry, or address one by the file it came from.

#### Interactive surfaces

On a terminal, bare `kref` opens a cockpit over your entries, `kref show` and
`kref todo` open a full-screen viewer, and `kref search`/`kref diff` page. All
four are built on one shared component and share a key vocabulary.

- **Navigation** — `j`/`k` and arrows scroll a line, `ctrl+d`/`ctrl+u` a half
  page, `g`/`G` and `home`/`end` jump to top and bottom, `<n>g` jumps to a body
  line, and `←`/`→` (`h`/`l`) pan wide lines or walk a comment thread.
- **Folding** — `space` folds the section under the cursor, `^space` folds
  everything or unfolds it when anything is folded. Markdown folds at every
  heading level; a folded section collapses to a `▸ N lines` hint.
- **Search** — `/` with `n`/`N` to cycle matches, on every surface. Committing a
  search opens any folds so a hit is never hidden; dismissing it with `esc`
  restores the folds you had.
- **Acting in place** — the cockpit opens an entry with `Enter` and returns you
  to your cursor; `e` edits, `x`/`u` archive/restore, `s` sets status, `f`
  sets or clears a favorite name, and `a`/`r` approve/reject a quarantine row.
- **Menus** — `?` shows the keys, `:` lists commands that have no key (including
  the expanded header: the entry's op-log, editors, recent versions, and the
  complete link list), `c` opens the comment menu, and `,` opens view options.
  Actions that cannot run right now stay visible with the reason attached rather
  than appearing to do nothing.
- **View options persist** — the line-number gutter, colour, and the `kref todo`
  glyph theme save to `~/.config/kref/config.yaml` and survive the session.
  Colour resolves as `KREF_COLOR`/`NO_COLOR` → saved preference → terminal
  detection; static and piped output ignores the file, so a redirect's bytes
  never depend on a preference.
- **Chrome adapts to the terminal** — the sticky header carries purpose-built
  fields (tier/status, version, link count, open questions), dropping the
  rightmost as the window narrows and shortening the title before dropping any
  of them. Footers offer progressively terser variants and show the widest that
  fits. Truncation is by display width, so an em-dash or ANSI never produces a
  partial escape.
- `ctrl+r` re-reads the entry from the store and re-renders in place — for
  watching an entry an agent or a sync is updating. `esc` is a layered dismiss
  (modal, then popup, then a committed search, then quit); quitting leaves the
  last view in the scrollback rather than clearing the screen.

#### Comments and questions

- `kref comment <id>` threads append-only comments: `-m` for the body (or piped
  stdin), `-q` to mark it a question, `--reply-to <cid>` to reply,
  `--resolve <cid>`/`--unresolve <cid>` to close and reopen, `--edit <cid>` and
  `--delete <cid>` to revise or redact. Comment ids are addressed by prefix.
- Comments are their own DAG operations, so they merge cleanly and never touch
  the body version — a comment on a `kind:todo` entry cannot lose a stale-write
  race. Edit and delete are themselves append-only, so a delete redacts the
  working view, not the pushed history.
- `kref show` renders a threaded discussion — open questions `◉`, resolved `✓`,
  indented replies — on every path (styled, `--plain`, and the viewer).
  `kref list --open-questions` filters to entries with an unanswered question.
- In the viewer, `a` posts a comment and `A` raises a question on the entry
  itself, while `r`/`e`/`d`/`x` reply, edit, delete, and resolve↔reopen the
  comment under the cursor. Drafts survive `ctrl+c`: a non-empty draft is
  preserved to the recovery tree and its path reported.

#### Todos

- A `kind:todo` entry follows a fixed grammar — one H1 and the sections
  `## Open`, `## Future / low priority`, and `## Done (compact)` — which buys a
  navigable cockpit and a formatter that keeps the document tidy on every write.
- `kref todo` opens the cockpit (`kref todo show [id]` for a specific one), with
  an awaiting-you count, per-section open-item counts, numbered open questions,
  and edited-staleness in the header. `--full` expands the Done section;
  `--no-pager` prints the static view.
- `kref todo fmt` moves done items into `## Done (compact)` and normalizes
  spacing — it runs automatically on every todo write, and `update --no-fmt`
  skips it. `kref todo lint` reports what the formatter cannot safely fix
  (`h1`, `unknown-heading`, `missing-section`, `checkbox-state`).
- **Stale-write guard (compare-and-swap)** — `kref update --if-version N`
  refuses a todo write when the entry has moved past `N`; omitting it warns
  loudly. `kref edit` checks implicitly. A refused write never loses content:
  the body is preserved to `$XDG_STATE_HOME/kref/rejected/` and named in the
  error.

#### Files: ingest and tracking

- `kref ingest <path>…` walks a tree or takes single files, writing a `kref-id`
  trailer back into markdown so re-ingesting is idempotent. The content type is
  detected from the extension; non-markdown text is stored content-only (typed,
  no trailer, not tracked) and binary files are rejected.
- `kref track <file>` keeps a file and its entry in sync. `kref reconcile` pulls
  file edits into the entry and `kref reconcile --write` pushes entry edits back
  out — without ever committing the file.

#### Sync and backup

- Per-tier `kref sync push`/`pull` to configured remotes. Push sends author
  identities before entries so attribution resolves remotely, which is what
  makes hub (shared-origin) sync work.
- `kref init` adopts your git identity and binds the `shared` tier to `origin`
  when one exists, so the common case needs no follow-up `kref remote set`.
  With no remote it says so, and a throttled warning (at most once a day) fires
  after op-creating commands while syncable entries have nowhere to go.
- `kref remote list`/`get`/`set` show and configure each tier's remote,
  including the private tier's permanent never-syncing status.
- **Backup for the unpushable tier** — `kref bundle export`/`import` produce a
  portable bundle for any tier (repeatable `--tier`, `-` for stdin/stdout, so
  you can pipe through `age` or `sops`), and `kref vault backup`/`restore`
  mirror the private tier under `$XDG_DATA_HOME`.

#### Secret handling and quarantine review

- [betterleaks](https://github.com/betterleaks/betterleaks) scans on the way in
  (`ingest`, `track`, `reconcile`, `update --file`) and on the way out
  (`sync push` scans the delta about to leave and fails closed before the remote
  is contacted).
- **Flagged writes are held for human review, not refused.** A body or comment
  that trips the scanner on a syncable tier is diverted into a reserved,
  private-typed `quarantine` namespace — a new entry becomes a draft, an update
  or comment is parked as an intent-item recording the intended write and its
  base version — and a review question-comment naming the finding (rule and
  line, never the value) opens on the entry. The live target is untouched, and
  the namespace is non-syncable, so a held secret cannot be pushed.
- `kref quarantine approve <id>` applies the held write **through the normal
  write path**, inheriting the write-lock, todo compare-and-swap, and DAG merge.
  A write whose target moved since parking is refused as a stale re-review
  conflict. `kref quarantine reject <id>` discards it, preserves the text to the
  recovery dir, and tombstones the item for audit.
- A rejection is reversible until purged: `kref quarantine list --rejected`
  browses tombstoned rejections, `recover` returns one to the queue, and
  `purge` hard-deletes rejected items — pruning history so a held secret is
  excised, not just hidden. Only rejected items are purgeable.
- Held writes report their age and are marked **STALE** past 7 days, in
  `kref list`'s banner, `kref quarantine list`/`show`, and the todo cockpit
  badge. A throttled reminder fires after a mutating command while stale writes
  await review.
- Bare `kref quarantine` on a terminal opens the review queue: `]`/`[` between
  held writes, `a`/`r` to decide with an optional note, `o` to open the target,
  and decide-and-advance. `kref quarantine show <id>` opens the same review for
  one item, as does `Enter` on a review row in the cockpit.
- Approving a false positive is a **human** decision — the MCP tools have no
  `force`. At the CLI, `--force` on `new`/`update`/`comment` parks the write and
  approves it in one step rather than skipping the scan, so the audit trail
  survives.
- A missing betterleaks binary degrades by surface instead of failing
  everything: content paths proceed with a loud UNSCANNED warning (and an
  `unscanned` flag under `--json`), while `sync push` stays fail-closed —
  `sync push --force` overrides the missing-scanner refusal, but a secret found
  by a working scanner still blocks.

#### Agents

- `kref mcp` runs a [Model Context Protocol](https://modelcontextprotocol.io)
  server over stdio exposing `kref_remember`, `kref_recall`, `kref_get`,
  `kref_update`, `kref_patch`, `kref_lifecycle`, `kref_comment`,
  `kref_supersede`, and a read-only `kref_quarantine`. `purge` and `retier` are
  deliberately not exposed — destruction and disclosure-sensitive moves stay
  human.
- **`kref_patch` is the agent editor**, MCP-only by design (a human edits with
  `kref edit`). It applies a unified diff with an LLM-tolerant applier — hunk
  line numbers are hints, each hunk is located by its context — while staying
  strict on safety: missing or ambiguous context fails loudly and application is
  all-or-nothing, so a patch never half-applies or lands in the wrong section.
- **Read tools return enough to triage without a second call** — `kref_recall`
  reports kind, version, updated date, labels, and per-entry match counts, with
  `limit` capping results and reporting how many were held back; `kref_get`
  returns kind, content-type, updated date, labels, and links with the body.
- **Repo scoping is explicit.** Without `--allow`, the server is locked to its
  `--dir`/`KREF_DIR` repo and a per-call `dir` naming another repository is
  refused. `kref mcp --allow <root>` (repeatable) enables global mode, where
  each call passes an absolute `dir` inside an allowed root (canonicalized and
  segment-checked, so `/x/a` never authorizes `/x/ab`). Global mode serves only
  syncable tiers, so a multi-repo server never exposes another repo's
  private-typed tiers or its review queue.
- `kref_update` also takes `add_labels`/`remove_labels` and
  `add_links`/`remove_links`, so an agent manages metadata without extra tools;
  `body` is optional for a metadata-only update.
- `kref agents_md` prints a canonical agent-policy block for a global
  `AGENTS.md`/`CLAUDE.md`, and `--skill` emits a complete `SKILL.md` driving
  manual. The text ships in the binary, so it tracks the installed version.

#### Output contracts and shell integration

- Human-readable output by default. **`--json`** gives stable, script-friendly
  objects; an array-valued key is always present and always an array, so a
  consumer can iterate unconditionally. Errors are a single-line
  `{"error": "…"}` on stderr, and exit status is `0` or `1`.
- **`--plain`** is chrome-free and line-oriented everywhere — TSV for `list` and
  `search`, the stored body (followed by any comments) for `show`. It implies no
  colour and no pager, and is mutually exclusive with `--json`.
- **`--dir`** selects the repo; path arguments still resolve against the current
  directory. The repo resolves as `--dir` > `KREF_DIR` > the current directory,
  so an MCP host that sets a per-project variable needs no shell wrapper. Like
  git, kref walks up to the enclosing repository from any subdirectory.
- **`kref help` adapts to context** — a concise grouped list on a terminal, the
  full recursive tree when piped (what an agent sees), with `--long`/`--short`
  to force either.
- **Shell completion** for bash, zsh, and fish, with `--install` to write it to
  the shell's standard directory. Completion is store-backed: entry ids beside
  their titles (`restore` offers only tombstoned entries, `unarchive` only
  archived), declared tier names including custom ones, your own `--kind` and
  `--label` values, and comma-aware `--columns=`.
- `kref version` reports `kref <version> (commit <UTC RFC3339>)`, with
  `commit_date` under `--json`. Builds without an injected version fall back to
  the VCS information the Go toolchain embeds. Release archives stamp the commit
  date rather than build time and pin file mtimes to it, making them
  byte-for-byte reproducible and verifiable against the published SBOM and
  provenance attestations.
- `KREF_COLOR=1`/`=0` forces colour on or off, overriding `NO_COLOR` and
  terminal detection. `--json` output is never coloured.

#### Configuration and hooks

- Two config layers — a machine-local user file at
  `$XDG_CONFIG_HOME/kref/config.yaml` over a shared project entry — with a
  deliberate local-then-project trust model. All settable keys round-trip, so
  rewriting the file from a viewer preference never drops a setting it does not
  know about.
- `kref hooks install` writes or merges `.lefthook.yml` wiring pull on merge,
  scan-and-push on push, and ingest-changed-markdown on commit, with repeatable
  `--ingest-path` to choose the watched directories. Hooks call kref by absolute
  path so they find the same `betterleaks` sibling regardless of the committer's
  `PATH`. [lefthook](https://lefthook.dev) is not bundled, and `lefthook install`
  is required to register the hooks into `.git/hooks`.

### Security

- betterleaks scans every ingest and every push, and every comment-writing
  surface — including replies, edits, and resolve notes typed in the viewer — so
  no write path bypasses the policy.
- The `private` tier is structurally unpushable, refused at `SetRemote`, `Push`,
  `Pull`, and `SyncableTiers`. The `quarantine` namespace holding flagged writes
  is likewise non-syncable.
- Scratch files (editor buffers, bundle staging, scanner reports) are created
  under `$XDG_CACHE_HOME/kref/tmp` (mode 0700) rather than the shared system
  temp dir, since they can carry private-tier bodies. HOME-less environments
  fall back to the system temp dir with files at 0600.
- Dependencies and the Go toolchain are pinned to versions with no known
  vulnerabilities. CI runs `govulncheck`, CodeQL, golangci-lint, and a
  betterleaks scan of the repository's own history on every change, surfacing
  findings as SARIF in the Security tab.
- Releases are built by GoReleaser in CI, from a tree that has passed the same
  quality gate a pull request must pass. Each ships an SPDX SBOM per archive, a
  keyless cosign signature over `checksums.txt`, and a Sigstore
  build-provenance attestation covering every published file — verifiable with
  `gh attestation verify … --repo trevor-vaughan/kref` and
  `cosign verify-blob --bundle checksums.txt.sigstore.json …`.
- `SECURITY.md` documents the trust model and a private vulnerability reporting
  channel.

### Known limitations

- **Operations are attributed but not cryptographically signed.** git-bug
  v0.10.1 exposes no API to equip an identity with a signing key, so attribution
  is forgeable.
- **No encryption at rest.** The `private` tier stays local but is not encrypted
  on disk, and neither are bundles or the vault.
- **No semantic search.** Matching is substring and exact normalized-title; a
  derived vector index is planned, not built.

[0.1.0]: https://github.com/trevor-vaughan/kref/releases/tag/v0.1.0
