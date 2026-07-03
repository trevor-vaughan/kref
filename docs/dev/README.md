# kref — developer / maintainer documentation

This directory is the maintainer's map of `kref`. User-facing docs live in the
top-level [`README.md`](../../README.md); this explains **how it works** and
**why it's shaped this way**.

The design specs and implementation plans no longer live as files in this tree
— they live in `kref`'s own knowledge base, because that is exactly what `kref`
is for: planning material belongs in git refs, not the working tree. Browse them
with the tool itself:

```
kref list --kind spec        # the architecture design and the per-feature designs
kref list --kind plan        # the task-by-task implementation plans
kref show <id>               # read one (use the id from the list)
```

- **Architecture design:** the `spec` titled *Git-Native Knowledge Base —
  Architecture Design* holds the architecture, the key decisions (D1–D6), and
  the deferred list. The other `spec` entries capture one feature's design each
  (idempotent marker-based ingest, the unified entry model, the UX slices).
- **Implementation plans:** the `plan` entries are the task-by-task plans the
  code was built against, from Phase 1 (core store) and Phase 2 (sync) through
  the later feature slices.

> Four phase plans embed example AWS tokens as illustrations of the
> secret-scanning they describe. Ingest quarantined those to the unpushable
> `private` tier when they were added; the rest are `shared`. List them with
> `kref list --tier private`. (Note: the current scanner, betterleaks, filters
> synthetic AWS keys, so a re-ingest would not re-quarantine them — they remain
> private because tier assignment is sticky.)

## Substrate

`kref` is a thin domain layer over git-bug's `entity/dag` framework
(`github.com/git-bug/git-bug`, GPL-3.0, pinned at `v0.10.1`). git-bug gives us,
for free:

- entities stored as a Lamport-ordered DAG of operations under
  `refs/<namespace>/<id>`;
- conflict-free merge across clones, with identity/attribution;
- push/pull/fetch over git remotes.

We do **not** fork git-bug — we depend on its public `entity/dag`, `identity`,
and `repository` packages. The single source of truth is the git-object op-DAG;
any query surface (today an in-memory index; possibly SQLite later) is a
derived, rebuildable projection. See spec §4.1.

## Layout

```
cmd/kref/            cobra CLI (main.go wires commands; commands.go implements them)
internal/entry/    the dag entity: Snapshot, operations, Definition, Compile, Read
internal/store/    Store over the repo: init/open, identity, add/get/list/tombstone,
                   purge (store.go) and per-tier remotes + sync (sync.go)
internal/render/   pure human-readable presentation of entries (no TTY/--json logic)
internal/scan/     betterleaks shell-out (Scan([]byte) -> []Finding)
internal/bridge/   ingest (scan -> quarantine -> store) + .gitignore guard
internal/hooks/    lefthook config renderer
internal/mcpserver/ thin MCP adapter over the store (curated agent tools)
```

### How an entry works

Each **kind** (`spec|adr|plan|memory|reference|document`) is the *same* dag
entity in a different tier namespace. An entry is a sequence of **operations**
(`Create`, `SetStatus`, `SetBody`, `SetTitle`, `SetKind`, `AddLabel`,
`RemoveLabel`, `AddLink`, `RemoveLink`, `Track`, `Untrack`, `RecordOrigin`,
`Reattribute`, `AckMerge`, `Archive`, `Unarchive`, `Tombstone`, `Restore`), each
embedding `dag.OpBase` and implementing `Apply(*Snapshot)`. `Entry.Compile()`
folds the ops into a `Snapshot`. Tiers are `entry.Tier` values whose
`Namespace()` is `kref-<tier>`; `entry.AllTiers()` drives every store operation,
so adding a tier or kind is local.

### Adding a new operation

1. Add an `OperationType` const and a struct embedding `dag.OpBase` in
   `internal/entry/operations.go`, with `Id()`, `Validate()`, `Apply(*Snapshot)`.
2. Register it in `operationUnmarshaler`.
3. Add a constructor `New…(author, …)` and a `Store`/CLI entry point if needed.

## Sync model

Tiers map to remotes via local git config `kref.remote.<tier>`. `private` is
refused at every layer (`SetRemote`/`Push`/`Pull`/`SyncableTiers`) — it is
structurally unpushable, which is the core security property. `Push` sends the
author **identity** (`identity.Push`) before the entries (`dag.Push`) so authors
resolve remotely; `Pull` does `identity.Pull` then `dag.Pull` (merge). This
identity-before-entries ordering is why hub (shared-origin) sync works.

## Sensitive data

Defense in depth: ingest scanning (betterleaks) quarantines secrets to `private`;
`rm` is a reversible tombstone (not safe for secrets); `purge` excises locally
(`dag.Remove` + `git gc --prune=now`) and, with `--push`, deletes the ref on the
remote (`git push <remote> --delete`). Purge is irreversible and assume-breach:
a leaked secret that was ever pushed must be rotated.

## Testing

- **Ginkgo v2 + Gomega only.** Each package has one `*_suite_test.go` bootstrap.
- **The `entry` package's specs are black-box `package entry_test`** — Ginkgo
  dot-imports an `Entry` identifier that collides with our `entry.Entry` type.
  All other packages are white-box.
- Sync is tested with **real multi-repo round-trips** (peer-to-peer and a bare
  origin proving identity propagation), not stubs.
- Run via `task test` (it provisions a pinned betterleaks and wires `KREF_BETTERLEAKS`).

## Known limitations / deferred

- **No cryptographic signing.** git-bug v0.10.1 exposes no public API to equip
  an identity with a signing key and cannot use system GPG/gpg-agent. Spec §10,
  §11. Attribution is git-identity-based and unsigned.
- **No encryption at rest** for the private/personal tiers (spec §11).
- **No vector index / semantic search** yet (spec §11).
