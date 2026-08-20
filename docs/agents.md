# kref for agents: MCP server & instructions

How to give an AI agent access to a kref store: the MCP server, its tool
surface and repo-scoping modes, and the policy block that teaches an agent to
route plans and specs into kref instead of writing files into your tree.

For the human-facing command reference see [usage](usage.md); for the
human-only approve/reject workflow that governs what agents write, see
[the quarantine review queue](usage.md#quarantine-review-queue).

______________________________________________________________________

## MCP server

`kref mcp` runs a [Model Context Protocol](https://modelcontextprotocol.io)
server over stdio, exposing a curated set of agent tools over the same store the
CLI uses: `kref_remember`, `kref_recall`, `kref_get`, `kref_update`,
`kref_patch`, `kref_lifecycle`, `kref_comment`, `kref_supersede`, and
`kref_quarantine`. `kref_lifecycle` covers the reversible document lifecycle
(set_status, delete/restore via tombstones, archive/unarchive); `kref_comment`
threads comments and questions; `kref_quarantine` is read-only over the
[review queue](usage.md#quarantine-review-queue) (an agent can see what it parked, but
only a human approves). `purge` (irreversible) and `retier` (a
disclosure-sensitive move) are deliberately not exposed to agents.

The read tools return enough to triage without a second call. `kref_recall`
reports each hit's kind, version, updated date, and labels, plus — when a
`search` term is given — how many times it matched (results are relevance-
ordered); pass `limit` to cap the hit count and it tells you how many were held
back. `kref_get` returns the entry's kind, content-type, updated date, labels,
and links alongside the body.

`kref_patch` is the agent editor, and it is deliberately MCP-only (no CLI
equivalent; a human edits with `kref edit`): it applies a standard unified diff
to the entry body, the format LLMs emit natively. The applier is lenient where
models are sloppy and strict where safety demands it: hunk line numbers are
hints only (each hunk is located by its context lines, matched exactly or up to
trailing whitespace, and hunks apply in document order), while a hunk whose
context is missing (stale diff) or ambiguous (identical sections, no usable line
hint) fails loudly, all-or-nothing, so a patch never half-applies or silently
lands in the wrong place. Each successful patch is one new body version.

Point an agent host at it per repo:

```json
{ "mcpServers": { "kref": { "command": "kref", "args": ["--dir", "/path/to/repo", "mcp"] } } }
```

A host that sets a per-project environment variable can instead point kref via
`KREF_DIR` (repo precedence: `--dir` > `KREF_DIR` > the current directory), with
no `--dir` argument:

```json
{ "mcpServers": { "kref": { "command": "kref", "args": ["mcp"], "env": { "KREF_DIR": "/path/to/repo" } } } }
```

A single server can serve several repositories: `kref mcp --allow <root>`
(repeatable) enables **global mode**, where each tool call passes an absolute
`dir` that must resolve inside an allowed root (canonicalized, so `/x/a` never
authorizes `/x/ab`). With exactly one allowed root, `dir` may be omitted. Without
`--allow` the server stays **locked** to its `--dir`/`KREF_DIR` repo, and a
per-call `dir` naming any other repository is refused — an unbounded per-call
`dir` would let a prompt-injected agent reach the private tier of any repo the
user owns, so cross-repo access requires this explicit boundary.

In global mode the server serves only **syncable** (non-private-typed) tiers of
an addressed repo, even when `--allow` names exactly one root. So a multi-repo
server never exposes another repo's private-typed tiers or its quarantine
review queue, which is the cross-repo exfiltration boundary. Locked mode (a
pinned `--dir`) serves all tiers of its one repo, so that is the mode to use when
an agent needs private-tier access to a particular repository.

Full-body writes to a `kind:todo` entry are guarded against the lost-update
problem with an optimistic version check (compare-and-swap). Every read surfaces
the entry's current body version — the `vN` that `kref log` numbers, returned by
`kref_get` (`version: N`) and `kref_recall` (`vN` per line).

`kref_update` **requires** `if_version` for a todo: pass the version you read,
and the write is refused as stale if the entry has since moved on, so a
concurrent edit is never silently clobbered. A refused write loses nothing — the
rejected body is written to `$XDG_STATE_HOME/kref/rejected/` and named in the
error.

`kref_patch` needs no version token (its hunks already fail loudly on stale
context), which is the other reason to prefer it for small edits. See
[todos](usage.md#todos) for the CLI side of the same guard.

Shell-capable agents mostly don't need it (they already have `--json` on every
command), but `kref_patch` is the exception worth wiring in: fine-grained edits
exist only on the MCP surface. MCP writes are recorded as agent provenance.

______________________________________________________________________

## Agent instructions

`kref agents_md` prints a canonical policy block for your global
`AGENTS.md`/`CLAUDE.md`, the instruction layer that outranks skills, so it can
override other skills' file-writing defaults (plans, specs, and handoffs become
kref entries instead of worktree files). `kref agents_md --skill` emits a
complete `SKILL.md` driving manual for skill-loading agent hosts. The text ships
in the binary, so it always matches the installed version's commands; regenerate
after upgrades:

```bash
kref agents_md >> ~/.claude/CLAUDE.md   # or your global AGENTS.md
kref agents_md --skill > ~/.claude/skills/kref/SKILL.md
```
