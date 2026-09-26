# Security Policy

## Supported versions

Only the latest tagged release and the current default branch are supported.
Fixes land on the default branch and ship in the next tag; there is no
back-porting to earlier tags.

## Verifying a release

Release artifacts are signed and attested. Before trusting a download:

```bash
# 1. provenance: the file was built by this repo's release workflow
gh attestation verify kref_<version>_linux_amd64.tar.gz --repo trevor-vaughan/kref

# 2. signature: checksums.txt is signed keylessly by that same workflow
cosign verify-blob checksums.txt \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp '^https://github\.com/trevor-vaughan/kref/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com

# 3. integrity: your download matches the now-trusted checksum list
sha256sum --ignore-missing -c checksums.txt
```

Builds are reproducible — modtimes and the embedded date come from the commit,
not the build clock — so a rebuild of a tag can be compared byte-for-byte
against the published archive.

## Reporting a vulnerability

Please report suspected vulnerabilities privately through GitHub's [private
vulnerability reporting](https://github.com/trevor-vaughan/kref/security/advisories/new)
rather than opening a public issue. This keeps the report confidential until a
fix is ready and gives us a tracked channel for coordinated disclosure.

## Trust model

`kref` stores knowledge as git objects under per-tier ref namespaces. The trust
boundaries are:

- **Tiers.** `private` (`refs/kref-private/*`) is structurally unpushable — no
  remote can be configured for it, so it never leaves the machine.
  `personal`/`shared` reach only the git remote you configure per tier.
- **Ingest.** `kref ingest` scans markdown with betterleaks before storing. A secret
  in an unmarked file quarantines the entry to `private`; a secret in a file
  already mapped to a syncable tier fails closed (the entry is not updated).

### Known limitations (attacker → goal → mitigation)

- **Forgeable attribution while signing is off.** Operations record a git
  identity, which is a claim rather than a proof: an attacker with write access
  to a shared remote can author entries as someone else. *Mitigated by turning
  signing on — set `commit.gpgsign` (or `kref.sign`) and kref signs every
  operation it writes, using git's own signing configuration. See
  [signing](docs/usage.md#signing).*
- **No signing policy.** kref reports a bad or unverifiable signature; it never
  enforces one. Unsigned, `bad` and `untrusted` material is still read, listed
  and pulled — a signature verdict is information for the human, not a gate.
  *Check `sig_state` (on `show`/`list --json`) or the ⚠ marker before trusting
  an entry's stated author; `kref list --unsigned` finds the unsigned ones.*
- **Attribution is self-asserted, even when signed.** A signature proves which
  key wrote a commit; the author name and email are separate, unverified fields
  that the signer chooses. kref's `Entry.Authors()` — and the foreign-author
  guard that makes `kref resign` refuse someone else's entry — read those
  fields, not the signature. *The guard is a courtesy check against accidents,
  not a security control. Signature-derived attribution is a deferred design
  project.*
- **No encryption at rest.** The `private` tier stays local but is plaintext in
  `.git`. An attacker with filesystem read access can read it. *Use full-disk
  encryption; do not store secrets you would not put in `.git`.*
- **Purge is not un-leak.** `kref purge --gc --push` deletes the entry's refs
  locally (including kref's own `kref-pushed`/`kref-resign-backup` mirrors, which
  would otherwise keep the body reachable) and on the remote — but anything
  already fetched by a peer persists, and so does anything in a bundle you
  exported. *Rotate the secret; treat any pushed secret as compromised.*
