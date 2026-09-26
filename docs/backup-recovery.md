# Backing up & recovering private knowledge

The `private` tier deliberately has no remote, so nothing backs it up for you.
This page covers the two local-only recovery paths — a portable bundle and the
local vault — and how to restore from either.

Part of the [kref documentation](usage.md).

______________________________________________________________________

The `private` tier never has a remote, so it lives only in this repo and would
be lost if the repo/disk dies. Two local-only recovery paths fill that gap
(neither ever touches a network remote):

```bash
# Portable bundle — your cross-machine / re-clone path. Keep the file wherever.
kref bundle export --tier private private.bundle
kref bundle import --tier private private.bundle   # into a fresh clone (authors preserved)

# Local vault — same-machine convenience under $XDG_DATA_HOME (not cache).
kref vault backup     # mirror private to ~/.local/share/kref/<repo>/private.bundle
kref vault restore    # bring it back after an rm -rf or a bad purge
```

`bundle export`/`import` take any tier(s) via repeatable `--tier` (default:
all), and read/write `-` for stdin/stdout, so an imported entry keeps its
original author, and you can encrypt a backup by composing with an external
tool:

```bash
kref bundle export --tier private - | age -r AGE_RECIPIENT > private.age
age -d private.age | kref bundle import -
```

Bundles and the vault are unencrypted (the live `.git` refs are too). Native
encryption at rest is a deferred decision; candidates are
[SOPS](https://github.com/getsops/sops) and
[age](https://github.com/FiloSottile/age). The reasoning is recorded as an
*Encryption at rest for the private tier* ADR inside kref's **own** knowledge
base — if you have cloned the kref source repo, read it there with `kref list
--kind adr`; it is not present in your project's store.

______________________________________________________________________

## Undoing a `kref resign`

`kref resign` rewrites commits to add signatures. Before moving an entry's ref
it saves the previous tip under `refs/kref-resign-backup/<namespace>/<id>`, so
the rewrite is reversible without a bundle:

```bash
# what a resigned entry pointed at before
git rev-parse refs/kref-resign-backup/kref-shared/<id>

# put it back
git update-ref refs/kref-shared/<id> \
  "$(git rev-parse refs/kref-resign-backup/kref-shared/<id>)"
```

Entry ids are unaffected either way: they come from the operation payload, not
from the commit, so a resign and its undo both leave ids, links and favorites
untouched.

These refs are local bookkeeping and never leave your machine — `kref sync push`
transfers only a tier's own namespace.

They do hold the entry's pre-rewrite content, though, which is why `kref purge`
removes an entry's backup ref (and its `refs/kref-pushed/*` mirror) along with
the entry itself. Purging is deliberately not undoable: leaving either mirror
behind would keep a purged body readable with `git cat-file` no matter how hard
you garbage-collect. If you want a resign to be reversible, do not purge the
entry in between.

Once you are satisfied with a resign, the backups are safe to delete:

```bash
git for-each-ref --format='%(refname)' refs/kref-resign-backup/ |
  xargs -r -n1 git update-ref -d
```
