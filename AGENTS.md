# AGENTS.md

Guidance for AI agents and contributors working on `kref`. For build/test
mechanics and the full convention list, read [`CONTRIBUTING.md`](CONTRIBUTING.md)
first — this file records only the conventions that are easy to violate by
omission.

## CLI command aliases (syntactic sugar)

Every user-facing command exposes short, conventional verb aliases via cobra's
`Aliases` field. The **canonical name is the one in the docs**; aliases are
sugar that resolve to it natively.

Current map (keep this and `README.md`'s "Aliases" note in sync):

| Canonical | Aliases |
|---|---|
| `new` | `create` |
| `ingest` | `import`, `add` |
| `show` | `cat`, `view`, `get` |
| `list` | `ls` |
| `log` | `audit` |
| `rm` | `remove`, `delete`, `del` |
| `purge` | `destroy` |
| `remote` | `remotes` |
| `version` | `ver` |
| `retier` | `mv` |

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
- **Document every alias.** An alias visible in `--help` but absent from
  `README.md` is documentation drift; add it to the README "Aliases" note and
  the CHANGELOG.

## JSON output convention

All commands emit **snake_case** JSON keys (`id`, `created_at`,
`created_by_email`). `list`/`show` get theirs from `json` tags on
`entry.Snapshot`; the other commands build lowercase maps. New commands and new
`Snapshot` fields must keep snake_case — an agent chaining `kref new` → `kref show`
relies on `id` meaning `id` everywhere.

## Help grouping

New user-facing commands must be added to a `--help` group in `newRootCmd`
(`core`/`lifecycle`/`sync`/`setup`/`additional`) with an explicit `GroupID`, and
in the intended top-to-bottom order (`cobra.EnableCommandSorting` is off). A
command with no group lands under "Additional Commands:".

<!-- kref agent policy (regenerate after upgrades: kref agents_md) -->
## kref — repo-resident knowledge base (agent policy)

This user keeps knowledge OUT of git worktrees using kref: entries live as
git refs, never as files. In any repo where `kref list` succeeds, these
rules OVERRIDE other skills' file-writing defaults:

- Specs, plans, brainstorms, design notes, evaluations, session state:
  create kref ENTRIES (`kref new --kind spec --label area:design`), never
  files under docs/ or .claude/. If a skill wants to write a plan or spec
  file, write a kref entry instead.
- Recall before asking or re-deriving: `kref search <term>` (per-entry
  match counts, most relevant first) or `kref list --kind <kind>`; read one
  entry with `kref show --plain <id>`.
- Parsing output? ALWAYS pass `--json`. Human output is not a stable API.
- Full-body updates are last-write-wins. Before `kref update <id>` with a
  rewritten body: re-read the entry AND check `kref log <id>` for versions
  you did not write; if the tip moved since your read, re-fetch and re-apply.
  Nothing is ever lost (`kref diff <id> --full` recovers any version), but
  recovery is not a merge strategy.
- Prefer the MCP `kref_patch` tool (unified diff; stale or ambiguous hunks
  fail loudly) over full-body replacement for small edits.
- Secrets: NEVER write them into a tier that syncs (anything but private,
  custom tiers included). kref scans and the push boundary fail-closes, but
  treat that as a backstop — secrets go to the private tier or nowhere.
  Never use `sync push --force`.
- Attribution: pass `--actor <agent-name>` (or set KREF_ACTOR) on writes so
  provenance records an agent, not the human.
- Questions for the human go in a "## Questions" section inside the relevant
  entry; answers come back inline — re-read before every update.
- Link related entries (`kref link add <id> <target>`) instead of repeating
  content; label design material `--label area:design`.
