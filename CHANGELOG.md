# Changelog

All notable changes to `kref` are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

`kref` is pre-release; nothing has shipped yet. This section summarizes what the
first release will contain. Granular, release-to-release entries start once
there is a tagged release to diff against.

### Added

- **`KREF_DIR` environment variable** — the repository directory now resolves in
  the order `--dir` flag > `KREF_DIR` > the current directory, so a host that sets
  a per-project environment variable (an MCP host config, for instance) can run
  `kref` with no `--dir` argument and no shell wrapper.
- **MCP global mode** — `kref mcp --allow <root>` (repeatable) lets one server
  serve several repositories: each tool call passes an absolute `dir` that must
  resolve inside an allowed root (canonicalized and segment-checked, so `/x/a`
  never authorizes `/x/ab`); with exactly one root, `dir` may be omitted. Without
  `--allow` the server stays locked to its `--dir`/`KREF_DIR` repo and a per-call
  `dir` naming any other repository is refused — the boundary that keeps an
  agent from reaching an unrelated repo's private tier.
- **MCP client-roots mode** — `kref mcp --client-roots` confines each tool call
  to the directories the client advertises via the MCP `roots` capability
  (fetched per call), as an alternative to `--allow`. The two are mutually
  exclusive; if the client advertises no usable `file://` roots, every call is
  refused (fail closed).
- **MCP tier scoping in multi-repo modes** — a `--allow` or `--client-roots`
  server serves only syncable (non-private-typed) tiers of an addressed repo,
  except a client's own sole advertised root. Cross-repo `private`/`agent` tiers
  and the quarantine review queue are never served — the exfiltration boundary
  called for by the global-server safety design.
- **`kref_update` labels and links** — the MCP `kref_update` tool now takes
  optional `add_labels`/`remove_labels` and `add_links` (`[{to, type?}]`, type
  defaulting to `relates`)/`remove_links` arrays, so an agent manages metadata
  without extra tools; `body` is optional for a metadata-only update. A
  secret-bearing label value on a syncable tier is refused.
- **Concurrent-write safety** — every entity write now takes a per-repo advisory
  lock (an `flock` on `.git/kref/write.lock`) for the whole read-modify-write, so
  two kref processes on one checkout can no longer lose an operation on the same
  git ref. On contention a write prints a brief notice and retries a few times
  before erroring; reads are never blocked.
- **Interactive todo cockpit** — on a terminal, `kref todo` (and `kref todo show`)
  now opens an interactive view instead of printing and exiting. A sticky two-line
  header stays put while you scroll: the global signal (awaiting-you count,
  open/done, version) on top, and the local context (what the cursor is on) below.
  The entry's comment threads render as a discussion zone above the body — open
  questions with `◉`, plain comments with `·`; a resolved question starts folded to
  its head, body, and a `▸ N replies` hint. Navigation reads like `kref show`:
  `↑`/`↓` or `j`/`k` scroll one line, `PageUp`/`PageDown` (and `ctrl+d`/`ctrl+u`)
  scroll a page/half while a single always-on action-cursor stays put. The cursor
  moves with `Tab`/`Shift-Tab` (next/previous comment or heading) and `←`/`→` or
  `h`/`l` (walk the thread tree — out to the parent, into the first reply);
  `gg`/`G` jump it to the first/last item; it follows back into view when you act.
  Headings fold at every level (`#` through `######`, so `###` subsections fold
  too): `Space` folds/unfolds whatever the cursor is on — a comment folds just its
  own replies (at any depth), a heading folds its section to a `▸ N lines` hint;
  `o`/`c` open/close the section under the cursor and `O`/`C` open/close every
  section (Done starts collapsed; `--full` starts it expanded).
  A footer marker (`top`/`NN%`/`bot`/`all`, plus `↓N` lines below the cursor)
  shows where you are. `t` toggles colour — styled markdown versus the raw source (as
  `show --plain`), and also drops the chrome styling (title-bar reverse, faint
  footer) so the whole view goes plain, not just the content. `?` shows a key
  popup; `q` or `ctrl+c` quits, leaving the last
  view in the scrollback (`esc` is reserved for dismissing dialogs, not quitting).
  Writes happen in a centred modal: `r` replies under the cursor, `e` edits
  (pre-filled), `x` resolves the thread's open question (optional closing note),
  each with `ctrl+s` to submit, `ctrl+o` to compose in `$EDITOR`, and `esc` to
  cancel; `d` deletes with a `y/n` confirm. Long comment bodies wrap to the width.
  `ctrl+r` refreshes and the awaiting-you count updates after each write. Piping,
  `--plain`, or the `--no-pager` flag keep the previous static render for scripts and
  demos. The cockpit and `kref show` share one `internal/tui.ScrollView` component.
- **Quarantine recover / purge** — a rejection is reversible until purged:
  `kref quarantine list --rejected` browses tombstoned rejections,
  `kref quarantine recover <id>` returns one to the pending queue, and
  `kref quarantine purge [<id>]` hard-deletes rejected items — pruning the history
  so a held secret is excised (not just hidden) and removing the recovery files
  (one item, or all rejected with `-y`). A read-only `kref_quarantine` MCP tool
  (list/show) lets agents see the queue without the secret; approve/reject/purge
  stay human/CLI actions.
- **Quarantine review in place** — `kref quarantine show <id>` opens a review
  view for a held write: its findings and the proposed change (a current→proposed
  diff for a body write), with `a`/`r` to approve/reject and `o` to open the target
  entry — no leaving to type another command. Pressing Enter on a review row in the
  `kref list` cockpit opens the same view. The viewer is shared between both entry
  points (built on `internal/tui.ScrollView`).
- **Quarantine review polish** — held writes now show their age and a STALE
  marker past 7 days (in `kref list`, `kref quarantine list`/`show`, and the todo
  cockpit badge); a throttled post-command reminder fires when stale writes await
  review; and bare `kref quarantine` on a terminal opens the interactive review
  queue (`n`/`p` between held writes, `a`/`r` to decide, decide-and-advance).
- **Interactive `kref list` cockpit** — on a terminal, `kref list` is now
  navigable: arrow through the entries (the quarantine review queue grouped on
  top) and act on the selected row without retyping ids. `Enter` opens it (the
  `kref todo` cockpit for a todo, the `kref show` pager otherwise) and returns to
  the saved cursor; `e` edits in `$EDITOR`; `a`/`r` approve/reject a quarantine
  row; `x`/`u` archive/restore; `s` sets status; `f` sets/clears an alias; `/`
  `n` `N` search; `?` keys; `q` quits. Rows render through the same formatter as
  the static table (extracted `render.ListLines`); opening reuses the existing
  viewers. `--plain`, `--json`, and `--no-pager` keep the static output.
- **Unified reader — `kref show` gains fold + shared search** — the `show` pager
  and the todo cockpit now share one reading model (new `internal/outline`).
  Markdown bodies in `kref show` fold by heading at **every** level (`#`…`######`):
  `space` folds the section at the viewport top, `o`/`c` open/close it, `O`/`C`
  every section; a folded section collapses to a `▸ N lines` hint and headings gain
  `▾`/`▸` markers (so `show`'s rendered markdown now carries fold affordances). The
  trailing **Comments** block folds as one section. Both the pager and the cockpit
  gain incremental search — `/` then a query, `n`/`N` to cycle matches — and
  committing a search opens any folds so a hit is never hidden. `kref show` also
  gains `Tab`/`Shift-Tab` to jump to the next/previous heading (parity with the
  cockpit; `j`/`k` still scroll line by line). Both views now show the same
  `top`/`NN%`/`bot`/`all` scroll marker — with a `↓N` count of the lines below the
  cursor (cockpit) or the visible window (pager) — and reload on `ctrl+r` (the
  pager keeps `r` as an alias). Non-markdown entries (code, JSON) don't fold —
  `space` pages as before. `--plain` and piped output are unchanged.
- **Show & pager UX** — `kref show` no longer clears the screen on quit: the last
  view you were reading stays in the terminal scrollback (`less -X` behaviour).
  Press `e` to expand the header in place with the entry's history — created,
  edited (relative + version), the editors and their edit counts, the last ten
  body versions, and its links (both directions, with titles). Help is now a
  centred popup on `?` instead of a footer swap. The standalone `kref links`
  command is removed — links live in the expanded header, and `show --json` still
  carries outgoing links. Incoming-link lookups are now served from the excerpt
  cache (no full-history scan).
- **Comments & questions** — `kref comment <id>` threads append-only comments on
  an entry: `-m` for the body (or piped stdin), `-q` to mark it a question,
  `--reply-to <cid>` to reply, and `--resolve <cid>` to close a question (with an
  optional closing note via `-m`). Comment ids are addressed by prefix within the
  entry. `kref show` renders a threaded **Comments** section after the body —
  open questions (`◉`), resolved ones (`✓`), and indented replies — on every view
  path (styled, `--plain`, and the pager). `kref list --open-questions` filters
  to entries that still have an unanswered question. Comments are their own DAG
  operations, so they merge cleanly and never touch the body version — a comment
  on a `kind:todo` entry can't lose a stale-write race. `--edit <cid>` revises a
  comment body (shown with `· edited`) and `--delete <cid>` soft-deletes one
  (rendered `[deleted]`, with its thread replies kept intact; `-y` skips the
  confirmation prompt). Edit and delete are themselves append-only ops, so a
  delete redacts only the working view, not the pushed history. Comment bodies
  are secret-scanned at write time — a secret on a syncable entry is refused, the
  text preserved to the recovery dir and the finding named, so nothing is lost;
  `--force` overrides a betterleaks false positive — and the push boundary now
  also scans comment op-history (including deleted and edited-away comments) as a
  backstop. Open questions now live only as `-q` comments — the old
  `- [?]` todo body marker is retired (it lints as an invalid checkbox state), and
  `kref todo`'s "awaiting you" signal counts unresolved question-comments instead
  of body markers.
- **Todo cockpit render polish** — `kref todo` now annotates each `##`/`###`
  section heading with its open-item count (e.g. `### Priority (next up) (3)`),
  numbers the awaiting-you questions, and shows an edited-staleness field in the
  header (`edited 2h ago (2026-07-08)`). A new `--full` flag expands the Done
  section instead of collapsing it. All four are display-only — the stored body,
  the formatter/linter, and the seen-body watermark are untouched.
- **`kref todo show [id]`** — an explicit subcommand for the todo cockpit,
  alongside the bare `kref todo` shortcut. It gives `kref todo show <TAB>` a
  clean list of todo ids to pick from, while `kref todo <TAB>` now lists the
  `show`/`fmt`/`lint` subcommands instead of mixing verbs and ids in one grid.
  `kref todo` and `kref todo <id>` are unchanged.
- **Todo stale-write guard (compare-and-swap)** — writes to a `kind:todo` entry
  now carry an optimistic version check so a concurrent edit is never silently
  clobbered. The token is the body version `kref log` numbers as `vN`
  (`len(BodyVersions)`). `kref update --if-version N` refuses the write when the
  entry has moved past `N`; omitting it on a todo still writes but prints a loud
  "unguarded todo write" warning. `kref edit` checks implicitly (the version it
  opened versus head at save). The MCP `kref_update` tool **requires**
  `if_version` for a todo and refuses a stale one (`kref_patch` needs no token —
  its hunks already fail on stale context). A refused write never loses content:
  the body is preserved to `$XDG_STATE_HOME/kref/rejected/` and named in the
  error. The current version is surfaced so callers can supply it — the
  `kref todo` header (`· vN`), `kref_get` (`version: N`), and `kref_recall`
  (`vN` per line) — and `kref show --json`/`kref list --json` gain a `version`
  field.
- **`kref show --header`** — print only the metadata block (no body, no pager):
  a cheap, token-light metadata peek, symmetric with `--no-header` (the two are
  mutually exclusive).
- **`kref init` adopts `origin`** — when the repository already has an `origin`
  git remote, `init` binds the `shared` tier to it automatically (and reports
  it), so the common case needs no follow-up `kref remote set`. When no remote
  exists, `init` warns that sync is impossible until one is configured. `--json`
  gains a `shared_remote` field.
- **`kref archive --accepted`** — bulk-archive every `accepted` entry in one go,
  mirroring `--obsolete` (same confirmation prompt and `-y`/`--yes` bypass). The
  two status flags are mutually exclusive.
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
- Threaded discussion for agents: the `kref_comment` MCP tool mirrors
  `kref_lifecycle` (one tool, `action` enum) with `add` (`question`/`reply_to`
  for open questions and threading), `resolve` (with an optional closing note),
  `edit`, and `delete`. Comment bodies are betterleaks-scanned (shared
  `internal/commentguard` policy): a flagged `add`, `edit`, or resolve-note on a
  syncable entry is held for human review (see the quarantine-review entry below).
- Quarantine review for flagged writes: a body or comment that trips betterleaks
  on a syncable tier is now **held for human review** instead of refused, across
  every write surface — CLI `new`/`update`/`comment` and MCP `kref_remember`/
  `kref_update`/`kref_patch`/`kref_comment`. The content is diverted into a new
  reserved, private-typed `quarantine` namespace (a new entry becomes a draft; an
  update or comment is parked as a `kind:quarantine` intent-item recording the
  intended write and the base version), a review question-comment naming the
  finding (rule and line, never the value) opens on the entry, and the live
  target is left untouched. `kref quarantine approve <id>` then applies the held
  write **through the normal write path** (inheriting the write-lock, todo CAS,
  and DAG merge): a draft is promoted to its tier (the shared-promotion secret
  gate still applies unless the finding is allowlisted), an update/comment is
  replayed onto the live entry and the item archived `q-status:approved`; a write
  whose target moved since parking (a todo whose version advanced) is refused as a
  stale re-review conflict. `kref quarantine reject <id>` discards the write,
  preserves the text to the recovery dir, and tombstones the item
  `q-status:rejected`. The `quarantine` namespace is non-syncable, so a held
  secret never leaves the machine; the push scan remains the backstop.
  `internal/entryguard` and `internal/commentguard` are the shared scan policy;
  scanning now covers every CLI body source (not just `--file`). Approving a
  false positive is a human decision: the MCP tools have no `force`; at the CLI,
  `--force` on `new`/`update`/`comment` parks the write and approves it in one
  step (no longer a scan-skip), keeping the audit trail.
- Richer MCP read tools so an agent can triage without a follow-up call:
  `kref_recall` now reports each hit's kind, version, updated date, and labels,
  and — when a `search` term is given — the per-entry match count (results
  relevance-ordered); a new `limit` caps the hit count to the most relevant and
  reports how many were held back. `kref_get` now returns kind, content-type,
  updated date, and links alongside the version, labels, and body it already
  gave.
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

- **`kref new` now reads a body from piped/redirected stdin** when `--body` is
  omitted, matching `kref update` and the documented agent guidance to pipe a
  body on stdin. Previously `kref new … < file` silently created an entry with
  an empty body. An interactive terminal is still never consumed (it would block
  on an EOF that never comes), and an explicit `--body` still wins.
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
