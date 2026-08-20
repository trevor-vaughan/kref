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
