package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newAgentsMDCmd prints canonical agent guidance: a compact policy block
// for the user's global AGENTS.md/CLAUDE.md (the instruction layer that
// outranks skills, so it can override file-writing defaults), or a complete
// SKILL.md driving manual with --skill. The text ships in the binary so it
// always matches the installed version's command surface — the
// `kref completion <shell>` pattern applied to agents.
func newAgentsMDCmd() *cobra.Command {
	var skill bool
	c := &cobra.Command{
		Use:               "agents_md",
		Aliases:           []string{"agents-md"},
		Short:             "Print agent guidance: an AGENTS.md policy block (or a full skill with --skill)",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		Example: exampleBlock([]string{
			"kref agents_md >> ~/.claude/CLAUDE.md      # append the policy block",
			"kref agents_md --skill > SKILL.md          # emit the full driving manual",
		}),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if skill {
				fmt.Fprint(cmd.OutOrStdout(), skillManual)
				return nil
			}
			fmt.Fprint(cmd.OutOrStdout(), agentsPolicyBlock)
			return nil
		},
	}
	c.Flags().BoolVar(&skill, "skill", false, "emit a complete SKILL.md driving manual instead of the AGENTS.md block")
	return c
}

// agentsPolicyBlock is the always-in-context policy layer. It states the
// overrides and disciplines, not the full command surface — that is the
// skill's job. Mention only shipped features; regenerate after upgrades.
const agentsPolicyBlock = `<!-- kref agent policy (regenerate after upgrades: kref agents_md) -->
## kref — repo-resident knowledge base (agent policy)

This user keeps knowledge OUT of git worktrees using kref: entries live as
git refs, never as files. In any repo where ` + "`kref list`" + ` succeeds, these
rules OVERRIDE other skills' file-writing defaults:

- Specs, plans, brainstorms, design notes, evaluations, session state:
  create kref ENTRIES (` + "`kref new --kind spec --label area:design`" + `), never
  files under docs/ or .claude/. If a skill wants to write a plan or spec
  file, write a kref entry instead.
- Recall before asking or re-deriving: ` + "`kref search <term>`" + ` (per-entry
  match counts, most relevant first) or ` + "`kref list --kind <kind>`" + `; read one
  entry with ` + "`kref show --plain <id>`" + `.
- Parsing output? ALWAYS pass ` + "`--json`" + `. Human output is not a stable API.
- Full-body updates are last-write-wins, EXCEPT ` + "`kind:todo`" + ` entries, which
  enforce an optimistic version check: read the version (the ` + "`vN`" + ` in
  ` + "`kref log`" + `, echoed by ` + "`kref_get`" + `/` + "`kref_recall`" + ` and the
  ` + "`kref todo`" + ` header) and declare it — ` + "`kref update --if-version N`" + `,
  MCP ` + "`kref_update`" + ` REQUIRES ` + "`if_version`" + `, and ` + "`kref edit`" + `
  checks implicitly; a stale write is refused (body kept under
  ` + "`$XDG_STATE_HOME/kref/rejected/`" + `), not clobbered. For other kinds: before
  a ` + "`kref update <id>`" + ` rewrite, re-read the entry AND check ` + "`kref log <id>`" + `
  for versions you did not write; if the tip moved, re-fetch and re-apply.
  Nothing is ever lost (` + "`kref diff <id> --full`" + ` recovers any version), but
  recovery is not a merge strategy.
- Prefer the MCP ` + "`kref_patch`" + ` tool (unified diff; stale or ambiguous hunks
  fail loudly) over full-body replacement for small edits.
- Secrets: NEVER write them into a tier that syncs (anything but private,
  custom tiers included). kref scans and the push boundary fail-closes, but
  treat that as a backstop — secrets go to the private tier or nowhere.
  Never use ` + "`sync push --force`" + `.
- Attribution: pass ` + "`--actor <agent-name>`" + ` (or set KREF_ACTOR) on writes so
  provenance records an agent, not the human.
- Questions for the human go in a "## Questions" section inside the relevant
  entry; answers come back inline — re-read before every update.
- Link related entries as work connects them (` + "`kref link add <id> <target>`" + `)
  — a plan to its spec, a spec to the brainstorm behind it — instead of
  repeating content; label design material ` + "`--label area:design`" + `.
- Favorites: name an entry with ` + "`kref fav add <id> <name>`" + ` (names need a
  non-hex char); then ` + "`kref show <name>`" + ` resolves anywhere an id does.
  ` + "`kref config`" + ` shows effective config; keys live in
  ` + "`~/.config/kref/config.yaml`" + ` (user) over the shared ` + "`kref.conf`" + ` entry.
- Keep the lifecycle current: set status as an entry moves
  (` + "`kref status <id> open|active|accepted|superseded|obsolete`" + `), and
  ` + "`kref supersede <old> <new>`" + ` when one entry replaces another rather than
  editing the old one into obsolescence.
`

// skillManual is the full driving manual, formatted as a SKILL.md so it can
// be dropped into any skill-loading agent host verbatim.
const skillManual = `---
name: kref
description: Drive kref, the repo-resident knowledge base over git refs. Use when capturing or recalling project knowledge (specs, plans, decisions, notes), updating entries, or when any task would otherwise write knowledge files (plans, specs, handoffs) into a git worktree that has kref enabled.
---

# Driving kref

kref stores knowledge as versioned entries in git refs — nothing in the
worktree. Detection: ` + "`kref list`" + ` exits 0 in an enabled repo (kref discovers
the enclosing repo from any subdirectory, git-style).

## Daily loop

1. RECALL — ` + "`kref search <term>`" + ` (match counts, most relevant first);
   filter with --kind/--label/--tier; ` + "`kref list`" + ` defaults to
   newest-first recency (` + "`--sort <field>`" + ` to override).
2. READ — ` + "`kref show --plain <id>`" + ` for exact stored content
   (` + "`--plain`" + ` skips rendering, headers, and paging; --json for metadata;
   ` + "`--header`" + ` for the metadata block alone, no body — a cheap peek).
   Entries are addressed by unique id prefix or by a tracked file path.
3. ACT — create, update, link, label (below).
4. CAPTURE — decisions and designs become entries, not files. Kinds in
   common use: document (default), spec, note, todo, memory. Label design
   material ` + "`area:design`" + `. Set ` + "`--actor <you>`" + ` so provenance says agent.

## Creating and updating

- New entry: ` + "`kref new --kind spec --title \"...\" --label area:design`" + ` with
  the body piped on stdin (or --body). Title derives from the H1 if omitted.
- Small edits: use the MCP ` + "`kref_patch`" + ` tool — a standard unified diff;
  hunk line numbers are hints, context must match, stale/ambiguous hunks
  FAIL LOUDLY instead of clobbering. This is the safe concurrent editor.
- Full-body rewrite (` + "`kref update <id>`" + ` with stdin/--body/--file) is
  last-write-wins for most kinds — re-fetch, check ` + "`kref log <id>`" + ` for
  versions you did not write, and only then update; if the tip moved, re-fetch
  and re-apply. For ` + "`kind:todo`" + ` this is ENFORCED via optimistic concurrency:
  read the version (` + "`vN`" + ` in ` + "`kref log`" + `, echoed by ` + "`kref_get`" + `/` + "`kref_recall`" + `)
  and declare it — MCP ` + "`kref_update`" + ` REQUIRES ` + "`if_version`" + `,
  ` + "`kref update --if-version N`" + ` guards on the CLI, and a stale write is
  refused (body kept under ` + "`$XDG_STATE_HOME/kref/rejected/`" + `), not clobbered.
- Recovery: every body version is retained — ` + "`kref log <id>`" + ` lists them,
  ` + "`kref diff <id> [m] [n]`" + ` shows changes, ` + "`kref diff <id> --full`" + ` dumps
  whole versions for copy-out. ` + "`kref rm`" + ` is a reversible tombstone
  (` + "`kref restore`" + ` undoes); ` + "`kref purge`" + ` is destructive — do not use it.

## Organizing

- Link related entries: ` + "`kref link add <id> <target>`" + ` (typed, default
  "relates"; open ` + "`kref show <id>`" + ` and press ` + "`e`" + ` to see both directions with titles).
- Labels: ` + "`kref label add <id> <label>...`" + `; filter listings with --label.
- Status lifecycle: ` + "`kref status <id> open|active|accepted|superseded|obsolete`" + `;
  ` + "`kref supersede <old> <new>`" + ` retires an entry in favor of another.
- Archive (hide from listings, keep status): ` + "`kref archive <id>`" + ` /
  ` + "`kref unarchive <id>`" + `; see them via ` + "`kref list --archived`" + `.

## Tiers (visibility)

private (this machine only, can never sync) · personal (default) · shared
(team remote) · plus custom personal- or shared-typed tiers (` + "`kref tier list`" + `
shows them all). Secrets go in private or nowhere — ingest scans, quarantines
to private, and push refuses detected secrets; never override with
` + "`sync push --force`" + `. Moving tiers (` + "`kref retier`" + `) is a HUMAN decision —
do not retier, especially toward a shared-typed tier.

## Machine output

Pass ` + "`--json`" + ` on any command for stable machine-readable output; errors
come as {"error": "..."} on stderr with exit 1. Never scrape human output —
it is colored, aligned, and subject to change.

## MCP server

Hosts without a shell use ` + "`kref mcp`" + ` (stdio). Tools: kref_remember,
kref_recall, kref_get, kref_update, kref_patch, kref_lifecycle
(set_status/delete/restore/archive/unarchive), kref_supersede. Deliberately
absent: purge, retier, sync — disclosure and destruction stay human. The
same discipline applies: kref_patch over kref_update for edits.

Comments (` + "`kref comment`" + `, incl. resolving question threads) are a CLI
feature for now — there is no MCP comment tool yet. Comments are their own
append-only operations, so they never touch an entry's body version and need no
if_version token.
`
