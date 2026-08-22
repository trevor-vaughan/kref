# Uninstall

How to remove kref from a repository and from your machine. There is no
`kref uninstall` command: most of the state is per-repo (git refs and git config
keys), but kref does keep a small amount under `$HOME`, so a complete removal
has two halves.

Part of the [kref documentation](usage.md).

______________________________________________________________________

## Per-repo state

Run these from inside the repository you want to clean.

**1. Delete kref's ref namespaces.** This is irreversible — the entries are the
refs. The objects are reclaimed by a later `git gc`.

```bash
git for-each-ref --format='%(refname)' 'refs/kref-*/*' \
  | xargs -r -n1 git update-ref -d
```

The `refs/kref-*/*` pattern is deliberate: it matches every tier namespace at
once — the built-in `kref-private`, `kref-personal`, and `kref-shared`, the
bookkeeping `kref-pushed`, **any custom tier** you declared with `kref tier add`,
and `kref-quarantine`, which holds writes parked by the secret scanner. Listing
namespaces by hand misses the last two, and a held secret is exactly what you do
not want left behind.

> **Rotate, don't just delete.** Deleting a ref removes kref's pointer to a
> secret; it does not un-leak it. Any secret that was ever pushed must be
> rotated. See [deleting things](usage.md#deleting-things).

**2. Drop kref's git config keys.** Discover them first, then unset each:

```bash
git config --get-regexp '^kref\.' | cut -d' ' -f1 | xargs -r -n1 git config --unset
```

These are the per-tier remotes (`kref.remote.<tier>`), custom tier declarations
(`kref.tier.<name>`), and warning-suppression flags.

**3. Deactivate the git hooks**, if you wired them up:

```bash
lefthook uninstall            # only if you ran `lefthook install`
```

Then delete the `kref-*` commands from `.lefthook.yml` (or remove the file
entirely, if kref put everything in it).

**4. Clean the working tree.** `kref init` adds a `.kref/` line to
`.git/info/exclude` — it is local-only and never committed. Remove that line,
and delete the `.kref/` directory if [tracking](usage.md#tracking-files) copied
any floater files into it.

**5. Optionally remove the identity refs.** kref stores author identities in
git-bug's namespace:

```bash
git for-each-ref --format='%(refname)' 'refs/identities/*' \
  | xargs -r -n1 git update-ref -d
```

Do this **only if the repo does not also use [git-bug](https://github.com/git-bug/git-bug)
itself** — the namespace is shared, so deleting it would take git-bug's
identities with it.

## Machine-wide state

kref writes a handful of files outside the repo. All are optional caches or
preferences, so removing them is safe:

| Path                                                                       | Written by                                   | Holds                                  |
|----------------------------------------------------------------------------|----------------------------------------------|----------------------------------------|
| `$XDG_CONFIG_HOME/kref/config.yaml` (usually `~/.config/kref/config.yaml`) | `kref config`, and the `,` view-options menu | user config and viewer preferences     |
| `$XDG_DATA_HOME/kref/<repo>/` (usually `~/.local/share/kref/`)             | `kref vault backup`                          | the local vault's private-tier bundles |
| `$XDG_STATE_HOME/kref/` (usually `~/.local/state/kref/`)                   | rejected MCP writes, reminder throttling     | recovery copies of refused writes      |
| `$XDG_CACHE_HOME/kref/tmp/` (usually `~/.cache/kref/tmp/`)                 | scratch files                                | nothing durable                        |

```bash
rm -rf ~/.config/kref ~/.local/share/kref ~/.local/state/kref ~/.cache/kref
```

> The vault under `$XDG_DATA_HOME` may be the **only** copy of a `private` tier
> you backed up — the private tier has no remote. Check
> [backup & recovery](backup-recovery.md) before deleting it.

If you installed shell completion with `kref completion <shell> --install`,
remove that file too:

```bash
rm -f ~/.local/share/bash-completion/completions/kref \
      ~/.local/share/zsh/site-functions/_kref \
      ~/.config/fish/completions/kref.fish
```

## The binaries

Finally delete the binaries (`./bin/kref`, `./bin/betterleaks`) and any copy or
symlink you placed on `PATH`.
