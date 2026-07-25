# AGENTS.md

Guidance for AI agents and contributors working on `kref`. For build/test
mechanics and the full convention list, read [`CONTRIBUTING.md`](CONTRIBUTING.md)
first — this file records only the conventions that are easy to violate by
omission.

## CLI command aliases (syntactic sugar)

Every user-facing command exposes short, conventional verb aliases via cobra's
`Aliases` field. The **canonical name is the one in the docs**; aliases are
sugar that resolve to it natively.

Current map (keep it in sync with the `command aliases` spec in
`cmd/kref/cli_test.go`, which asserts the same table):

| Canonical | Aliases |
|---|---|
| `new` | `create` |
| `update` | `set` |
| `ingest` | `import`, `add` |
| `show` | `cat`, `view`, `get` |
| `list` | `ls` |
| `log` | `audit` |
| `rm` | `remove`, `delete`, `del` |
| `purge` | `destroy` |
| `remote` | `remotes` |
| `version` | `ver` |
| `retier` | `mv` |
| `fav` | `alt` |
| `agents_md` | `agents-md` |

Rules:

- **New user-facing commands ship their aliases in the same change** — wire
  them on the `cobra.Command` (`Aliases: []string{…}`), not as a follow-up.
- **Aliases are additive and stable.** Adding one is cheap; removing one breaks
  muscle memory and scripts, so treat removals as a breaking change.
- **Canonical names stay authoritative.** Docs, help text, and JSON output keys
  reference the canonical name; aliases never appear as the documented form.
- **No collisions.** An alias must not equal any canonical name or another
  command's alias. The `command aliases` spec in `cmd/kref/cli_test.go` asserts
  the full map — update it when you change the map, and it will fail loudly on a
  duplicate.
- **The binary is the published list.** `docs/usage.md`'s "Aliases" section
  deliberately does not enumerate them — it points readers at `kref help`, which
  prints each command's aliases beside its canonical name and therefore cannot
  go stale. Don't add a second hand-maintained list to the user docs; a new
  alias needs the `cobra.Command`, the table above, the spec, and the CHANGELOG.

## JSON output convention

All commands emit **snake_case** JSON keys (`id`, `created_at`,
`created_by_email`). `list`/`show` get theirs from `json` tags on
`entry.Snapshot`; the other commands build lowercase maps. New commands and new
`Snapshot` fields must keep snake_case — an agent chaining `kref new` → `kref show`
relies on `id` meaning `id` everywhere.

**An array-valued key is always present and always an array.** `null` never
stands for an empty collection, and an absent key never means an empty one, so a
consumer can iterate any of them unconditionally — `kref list --json` on a repo
with no matches is `[]`, and `links`/`labels`/`provenance`/`comments` are `[]` on
an entry that has none. Three rules follow:

- **Never `omitempty` on a slice or map field** of a type that reaches `--json`.
  Saving a few bytes costs every consumer a key-presence check.
- **Return empty, not nil, from anything whose result gets serialized.** A nil
  slice is invisible in Go (`len`, `range`, and `append` all behave) and becomes
  `null` only at `json.Marshal`, which is why this drifts silently. `entry.Compile`
  and `store.List` establish it for the entry surface; a new producer must do the
  same.
- The `JSON collection shape` spec in `cmd/kref/cli_test.go` asserts this across
  commands. A new `--json` command belongs in its `listStyle` table.

## Confirmation flags

Skipping an interactive confirmation is **always `-y`/`--yes`**, with the
shorthand. `--force` is reserved for **overriding a safety check** — the secret
scan on `new`/`update`/`comment`, the `reconcile` active guard, `tier rm`'s
orphan check. The two are different verbs and one flag must not carry both: a
script author who has learned `--force = "I accept the scanner's false
positive"` should never find it silently meaning "yes, hard-delete this."

The `confirmation flag convention` spec in `cmd/kref/cli_test.go` walks the whole
command tree and fails on either half of that (a `--force` whose help mentions a
confirmation prompt, or a `--yes` missing its `-y`).

## Help grouping

New user-facing commands must be added to a `--help` group in `newRootCmd`
(`core`/`lifecycle`/`sync`/`setup`/`additional`) with an explicit `GroupID`, and
in the intended top-to-bottom order (`cobra.EnableCommandSorting` is off). A
command with no group lands under "Additional Commands:".

## Driving the TUIs (agents: this is how you actually run kref)

Several commands take over the terminal on a TTY. An agent's plain `bash` tool
is not a TTY, so a bare `kref show <id>` there silently takes the *static* path
— you have proved the command parses, not that the viewer works. Wrap it in
`tmux` to launch, send keys, and read the screen.

### The gate

`usePager` (`cmd/kref/output.go:59`) is the switch: stdout is a real terminal,
`--json` not set, `--plain` not set. Each command additionally honors its own
`--no-pager`. So the same binary is both a scriptable CLI and a TUI, and you
choose which by how you invoke it — `--plain`/`--json`/a pipe for assertions,
tmux for the interactive surface.

| Surface | Command | Implementation |
|---|---|---|
| Entry viewer (read + comment write) | `kref show <id>` | `RunViewer`, `cmd/kref/viewer.go:1223` |
| Todo cockpit (viewer + todo header) | `kref todo <id>` | `runTodoCockpit`, `cmd/kref/todo.go:77` |
| List cockpit (row nav + actions) | `kref list` | `runListCockpit`, `cmd/kref/listcockpit.go:626` |
| Quarantine review queue | `kref quarantine` | `runReviewModel`, `cmd/kref/quarantine_review_view.go:277` |
| Static pager (no model) | `kref search`, `kref diff` | `Page`, `cmd/kref/pager.go:263` |

### Recipe

```bash
sudo dnf install -y tmux                       # not in the dev container by default
go build -o /workspace/kref ./cmd/kref         # see "stale ./bin/kref" below

tmux new-session -d -s holder -x 80 -y 10 'sleep 900'   # keeps the server alive
tmux new-session -d -s kref -x 120 -y 40 -c /workspace './kref show <id>'

# Poll for readiness instead of sleeping: every footer carries "? keys" and a
# quit hint, and both stay visible down to 80 columns (the quit key differs —
# "q quit" or "q back" — the help marker does not).
timeout 15 bash -c 'until tmux capture-pane -t kref -p | grep -q "? keys"; do sleep 0.2; done'
tmux capture-pane -t kref -p

tmux send-keys -t kref '?'          # keys popup — the authoritative binding list
tmux send-keys -t kref Escape
tmux send-keys -t kref '/'; tmux send-keys -t kref 'needle'; tmux send-keys -t kref Enter
tmux capture-pane -t kref -p | tail -3          # status line reports "match 1/N"

tmux send-keys -t kref 'q'
tmux kill-session -t kref; tmux kill-session -t holder
```

Press `?` in any of the four TUIs rather than trusting a key table in a doc —
the popup is generated from the model's own bindings, so it cannot drift.

### Cross-surface conventions

Each is enforced by a spec, so a new surface that breaks one fails a test rather
than drifting quietly. Keep this list and the specs in sync.

- **`? keys` and a quit hint are visible at every width**, and the footer spends
  whatever width is available. Hosts pass variants from richest to tersest to
  `ScrollView.Fit`, which picks the widest that fits; the last one drops the
  position prefix rather than the two hints. Asserted on the *rendered* last
  line, not the composed string — the list footer once needed 111 columns and
  passed a substring check while showing neither hint at 80.
  (`TUI footer convention` and `TUI footer width use`, `cmd/kref/cli_test.go`)
- **Chrome never wraps.** `reserved()` is a fixed 2-3 rows and the viewport
  height derives from it, so a second footer line would have to change that
  reservation — and would change it again whenever the search indicator appears,
  resizing the viewport under the reader mid-scroll. Pick a shorter variant
  instead. (`stays a single row at every width`)
- **`g` goes to the top and `G` to the bottom** in all four surfaces.
  (`TUI navigation convention`)
- **`n`/`N` cycle search matches** in all four. The quarantine queue steps with
  `]`/`[`. (`review viewer navigation`, `cmd/kref/quarantine_review_view_test.go`)
- **`esc` is a layered dismiss**: modal → help popup → committed search → quit.
- **`ctrl+c` always quits**, from any layer. A modal holding a draft spills it to
  `$XDG_STATE_HOME/kref/rejected/` first and prints the path — never a silent
  loss. (`viewerModel ctrl+c escape hatch`, `cmd/kref/viewer_test.go`)
- **Mouse events reach the viewport** in every surface that enables capture.
  (`TUI mouse convention`)
- **Every interactive write records its actor.** A `viewerInput` that omits
  `actor` writes an unattributed comment, which then renders as the repo's git
  identity — an agent's reply shown as the human's. (`viewer attribution
  convention`, an AST walk over every construction site)
- **Chrome is measured in display columns, never bytes.** Titles carry ANSI from
  the pre-rendered header and multi-byte glyphs throughout, so byte slicing both
  under-fills the bar and cuts through runes and escape sequences. Use
  `ansi.Truncate`/`ansi.StringWidth`. (`ScrollView chrome truncation`,
  `internal/tui/scrollview_test.go`)
- **Wherever ↑/↓ move a selection, k/j move it too.** The bubbles viewport binds
  both pairs together (`up`/`k`, `down`/`j`, `left`/`h`, `right`/`l`), so any
  surface scrolling through `PassKey` gets this free; the gap is a host that
  routes arrows itself. Switch on `KeyMsg.String()`, not `KeyMsg.Type`, so the
  letters sit beside the arrows. Footers advertise the arrows only — both forms
  belong in the `?` popup. (`TUI vim-key convention`)
- **A key the interface advertises accounts for itself.** If it cannot act on the
  current selection it says why, rather than doing nothing — `noComment` in the
  viewer, `entryRow` in the list cockpit.

### Gotchas that cost time

- **A dying TUI kills the tmux server** when it is the last session, and every
  later `capture-pane` fails with `no server running` — which reads like a tmux
  problem but is really the app exiting. Keep a `sleep` holder session, or wrap
  the launch as `bash -c './kref …; echo EXIT=$?; sleep 60'` so the exit code
  and any error stay on screen. A bare `kref todo` with two `kind:todo` entries
  exits 1 (`pass an id`); `kref quarantine` on an empty queue prints
  `review queue is clear` and exits 0. Both look identical to a crash otherwise.
- **`capture-pane -p` strips styling.** Use `-e` to keep escape sequences when
  the question is about colour, `-J` to join wrapped lines.
- **Size matters.** Pass `-x 120 -y 40`; narrow panes reflow and hide content,
  and the viewer wraps to the reported width.
- **Stale `./bin/kref`.** `task test`/`check` build to a temp path, so `./bin/kref`
  only refreshes on `task build` — and it can end up unremovable under SELinux.
  Build to `/workspace/kref` for TUI runs; keep `task test` for the suites.
- **Do not skip the static path.** Assertions belong in tests and in
  `--plain`/`--json` runs; tmux is for confirming the interactive surface renders
  and responds, which no unit test covers.

<!-- kref agent policy (regenerate after upgrades: kref agents_md) -->
## kref — repo-resident knowledge base (agent policy)

This user keeps knowledge OUT of git worktrees using kref: entries live as
git refs, never as files. In any repo where `kref list` succeeds, these
rules OVERRIDE other skills' file-writing defaults:

- **NEVER lose the user's work.** This is non-negotiable. Any write that can be
  refused — a stale-write CAS, a todo lint reject, a secret block — MUST preserve
  the exact text it rejected (to `$XDG_STATE_HOME/kref/rejected/`, or a kept
  editor/draft buffer) and tell the user where it went. Silently dropping a long
  comment or edit on a rejection, forcing the author to retype it from scratch, is
  the worst possible outcome. When a check is a false positive, offer an explicit
  override (`--force` on the CLI, `force:true` on the MCP tool) — never silent
  data loss, and never a rejection the user can't recover from or override.
- Specs, plans, brainstorms, design notes, evaluations, session state:
  create kref ENTRIES (`kref new --kind spec --label area:design`), never
  files under docs/ or .claude/. If a skill wants to write a plan or spec
  file, write a kref entry instead.
- Recall before asking or re-deriving: `kref search <term>` (per-entry
  match counts, most relevant first) or `kref list --kind <kind>`; read one
  entry with `kref show --plain <id>`.
- Parsing output? ALWAYS pass `--json`. Human output is not a stable API.
- Full-body updates are last-write-wins, EXCEPT `kind:todo` entries, which
  enforce an optimistic version check: read the version (the `vN` in
  `kref log`, echoed by `kref_get`/`kref_recall` and the
  `kref todo` header) and declare it — `kref update --if-version N`,
  MCP `kref_update` REQUIRES `if_version`, and `kref edit`
  checks implicitly; a stale write is refused (body kept under
  `$XDG_STATE_HOME/kref/rejected/`), not clobbered. For other kinds: before
  a `kref update <id>` rewrite, re-read the entry AND check `kref log <id>`
  for versions you did not write; if the tip moved, re-fetch and re-apply.
  Nothing is ever lost (`kref diff <id> --full` recovers any version), but
  recovery is not a merge strategy.
- Prefer the MCP `kref_patch` tool (unified diff; stale or ambiguous hunks
  fail loudly) over full-body replacement for small edits.
- Secrets: NEVER write them into a tier that syncs (anything but private,
  custom tiers included). kref scans and the push boundary fail-closes, but
  treat that as a backstop — secrets go to the private tier or nowhere.
  Never use `sync push --force`. Comment bodies are scanned at write time
  (`kref comment`, MCP `kref_comment`): a secret on a syncable entry is refused
  and the text preserved to the recovery dir — rotate the secret and retry, or
  pass `--force`/`force:true` for a genuine false positive. The push boundary
  also scans comment op-history as a backstop, but treat the write-time gate as
  the one to respect.
- Attribution: pass `--actor <agent-name>` (or set KREF_ACTOR) on writes so
  provenance records an agent, not the human.
- Questions for the human go in a "## Questions" section inside the relevant
  entry; answers come back inline — re-read before every update.
- Link related entries as work connects them (`kref link add <id> <target>`)
  — a plan to its spec, a spec to the brainstorm behind it — instead of
  repeating content; label design material `--label area:design`.
- Favorites: name an entry with `kref fav add <id> <name>` (names need a
  non-hex char); then `kref show <name>` resolves anywhere an id does.
  `kref config` shows effective config; keys live in
  `~/.config/kref/config.yaml` (user) over the shared `kref.conf` entry.
- Keep the lifecycle current: set status as an entry moves
  (`kref status <id> open|active|accepted|superseded|obsolete`), and
  `kref supersede <old> <new>` when one entry replaces another rather than
  editing the old one into obsolescence.
