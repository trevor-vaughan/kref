package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/git-bug/git-bug/entity/dag"
	"github.com/git-bug/git-bug/repository"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/trevor-vaughan/kref/internal/entry"
)

// pgpArmorPrefix opens an OpenPGP armored signature block. It is the
// discriminator between signatures git-bug can dearmor and every other format
// git can produce (ssh, x509) — see signingRepo.ReadCommit.
const pgpArmorPrefix = "-----BEGIN PGP SIGNATURE-----"

// signingRepo teaches kref's repo handle about git-native commit signatures,
// which git-bug cannot produce or even read.
//
// On WRITE it shadows StoreCommit so every operation-pack commit is created by
// `git commit-tree -S`, handing signing to git itself: gpg.format
// (openpgp/ssh/x509), user.signingkey, gpg.program and gpg.ssh.program all just
// work. git-bug's own path cannot, because it takes an *openpgp.Entity drawn
// from a git-bug-managed PGP keyring that kref never populates
// (entity/dag/operation_pack.go, repository/gogit.go).
//
// On READ it shadows ReadCommit and ListCommits, because git-bug's ReadCommit
// unconditionally OpenPGP-dearmors whatever signature it finds and fails the
// entire read when that does not parse — so an ssh-signed entry is unreadable
// without this. Reads are intercepted ALWAYS, not only when this process is
// writing signatures: a signed commit may have been made by an earlier run with
// signing on, or pulled from a collaborator, and it has to stay readable either
// way.
//
// The interception works because git-bug reaches the repo through the
// repository.RepoData interface and kref holds its repo as a
// repository.ClockedRepo interface value: embedding the interface promotes its
// methods, and declaring one here shadows the promoted one for every caller
// inside git-bug.
//
// StoreSignedCommit is deliberately NOT shadowed. It is reachable only when the
// author identity carries a registered PGP key, which kref never creates; if one
// ever exists, an explicit request for PGP-entity signing should get PGP signing
// rather than be silently rerouted.
type signingRepo struct {
	repository.ClockedRepo

	dir   string   // repo path, for `git -C`
	name  string   // committer name: the resolved kref author
	email string   // committer email; SSH allowed-signers matches on this
	sign  bool     // whether writes are signed; reads are handled regardless
	env   []string // extra environment for git subprocesses (identity profile)

	// Own go-git handle: ReadCommit cannot delegate, and git-bug guards its
	// handle with a private mutex, so this one needs its own — the store
	// refreshes excerpts on a background goroutine.
	ggMu sync.Mutex
	gg   *gogit.Repository
}

// newSigningRepo wraps repo so kref can read and write git-native signatures.
// Callers should wrap IMMEDIATELY after opening: the author identity is itself
// stored in signed commits, so loading it already needs the read overrides —
// which is why the commit identity is supplied later, via setSigningAuthor.
//
// A failure to open the second go-git handle is not fatal: it only costs
// signature support, so the caller keeps a working store rather than none. It
// returns a warning rather than nothing, because the degraded state is
// indistinguishable from a store that was never set up to sign — every entry
// reads as `unsigned` and `kref resign` refuses with "not configured to sign",
// which sends the reader off configuring a key that is already configured.
//
// git-bug's own OpenGoGitRepo does the same open immediately before this, so in
// practice reaching the warning takes a race with something removing .git.
func newSigningRepo(repo repository.ClockedRepo, dir string, cfg *gitConfigSnapshot) (repository.ClockedRepo, string) {
	gg, err := gogit.PlainOpenWithOptions(dir, &gogit.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return repo, fmt.Sprintf(
			"signing and signature verification are unavailable: %s could not be reopened: %v", dir, err)
	}
	// An active identity profile is exported as git's global config layer, so
	// `git commit-tree -S` picks up that profile's user.signingkey and
	// gpg.format. Repo-local config still applies on top, which is what keeps
	// commit.gpgsign working. A profile error is left for resolveAuthor to
	// report, where it can say which profile and why.
	var env []string
	signCfg := cfg
	if _, path, perr := activeIdentityProfile(cfg); perr == nil && path != "" {
		env = []string{"GIT_CONFIG_GLOBAL=" + path}
		// The profile REPLACES git's global layer for every subprocess kref runs,
		// including the `git commit-tree -S` below. Re-resolving under it is what
		// keeps the layer that supplies the key and the layer that decides to use
		// it the same layer.
		signCfg, err = loadGitConfigSnapshot(dir, env)
	}
	var sign bool
	if err == nil {
		sign, err = signingEnabled(signCfg)
	}
	var warn string
	if err != nil {
		// Signing is off, and loudly. The alternative — deciding "no" in silence
		// because git could not answer — is indistinguishable from a store that was
		// never set up to sign, and sends the reader off configuring a key that is
		// already configured.
		warn = fmt.Sprintf("kref cannot sign here: %v", err)
	}
	return &signingRepo{
		ClockedRepo: repo,
		dir:         dir,
		sign:        sign,
		env:         env,
		gg:          gg,
	}, warn
}

// setSigningAuthor records the identity that signed commits are attributed to.
// It is a no-op on an unwrapped repo, which is the case when signature support
// is unavailable.
func setSigningAuthor(repo repository.ClockedRepo, name, email string) {
	if sr, ok := repo.(*signingRepo); ok {
		sr.name, sr.email = name, email
	}
}

// signingEnabled reports whether kref should sign its commits. An explicit
// kref.sign wins; otherwise git's own commit.gpgsign decides, so a user who
// already signs their code signs their kref entries with no extra setup.
//
// "Explicit" means present, not true: kref.sign=false has to beat a
// commit.gpgsign the user set for their code, which is the whole point of
// having a kref-specific key.
func signingEnabled(cfg *gitConfigSnapshot) (bool, error) {
	for _, key := range []string{"kref.sign", "commit.gpgsign"} {
		v, set, err := cfg.getBool(key)
		if err != nil {
			return false, err
		}
		if set {
			return v, nil
		}
	}
	return false, nil
}

// signing reports whether writes through this repo are signed.
func (r *signingRepo) signing() bool { return r.sign }

// signingEnv exposes the extra environment kref's git subprocesses must run
// under: the active identity profile, exported as git's global config layer.
//
// It is on the interface because signing is not confined to this file. `kref
// resign` establishes a second `git commit-tree -S` path of its own, and a
// subprocess that misses this env resolves user.signingkey and gpg.format from
// the user's real global config instead of the profile — signing as one identity
// while kref writes as another, which is exactly the split identity profiles
// exist to prevent.
func (r *signingRepo) signingEnv() []string { return r.env }

// StoreCommit shadows the embedded ClockedRepo's method. git-bug calls it for
// every entry write and for the merge commits MergeAll creates when two sides
// edited concurrently, so all of them are signed.
func (r *signingRepo) StoreCommit(tree repository.Hash, parents ...repository.Hash) (repository.Hash, error) {
	if !r.sign {
		// Delegate so the unsigned write path stays byte-identical to
		// git-bug's, not merely equivalent.
		return r.ClockedRepo.StoreCommit(tree, parents...)
	}
	args := []string{"-C", r.dir, "commit-tree", "-S", tree.String()}
	for _, p := range parents {
		args = append(args, "-p", p.String())
	}
	cmd := exec.Command("git", args...)
	// Empty message, matching what git-bug writes: nothing reads it, because
	// the operation payload lives in the tree.
	cmd.Stdin = strings.NewReader("")
	// The commit identity is the resolved kref author rather than git's
	// configured user. The two normally coincide (the author is derived from git
	// config); when a KREF_AUTHOR_* override makes them differ, matching the
	// operation's own author is both more accurate and what SSH allowed-signers
	// verification checks against.
	//
	// Before setSigningAuthor runs there is no author to attribute to, so git
	// falls back to its own config — the same source git-bug would have used.
	cmd.Env = append(os.Environ(), r.env...)
	if r.name != "" && r.email != "" {
		cmd.Env = append(cmd.Env,
			"GIT_AUTHOR_NAME="+r.name, "GIT_AUTHOR_EMAIL="+r.email,
			"GIT_COMMITTER_NAME="+r.name, "GIT_COMMITTER_EMAIL="+r.email,
		)
	}
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("sign commit for tree %s: %w: %s",
			tree, err, strings.TrimSpace(stderr.String()))
	}
	hash := repository.Hash(strings.TrimSpace(out.String()))
	if !hash.IsValid() {
		return "", fmt.Errorf("sign commit for tree %s: git returned %q, not a hash", tree, hash)
	}
	return hash, nil
}

// ReadCommit shadows the embedded method because git-bug's implementation
// (repository/gogit.go) unconditionally OpenPGP-dearmors any signature it finds
// and fails the whole read when that does not parse. An SSH or x509 signature is
// not PGP armor, so without this override every signed entry becomes unreadable
// with "EOF".
//
// kref verifies signatures through git (see signature.go), never through
// git-bug, so leaving SignedData/Signature unset costs nothing. A genuinely
// PGP-armored signature still delegates, both to keep that path byte-identical
// and because a pulled collaborator identity may carry registered PGP keys —
// which would make git-bug's verifier read those fields and trip over nil.
func (r *signingRepo) ReadCommit(hash repository.Hash) (repository.Commit, error) {
	out, sig, err := r.readCommitOnce(hash)
	if errors.Is(err, plumbing.ErrObjectNotFound) {
		// A go-git handle opened before a fetch cannot see the packfile that
		// fetch just wrote, so refresh it once before believing the object is
		// really absent. Only a genuine miss pays for this, and a genuine miss
		// is fatal to the caller anyway.
		if reopened := r.reopen(); reopened {
			out, sig, err = r.readCommitOnce(hash)
		}
	}
	switch {
	case errors.Is(err, plumbing.ErrObjectNotFound):
		return repository.Commit{}, repository.ErrNotFound
	case err != nil:
		return repository.Commit{}, err
	}
	if strings.HasPrefix(sig, pgpArmorPrefix) {
		return r.ClockedRepo.ReadCommit(hash)
	}
	return out, nil
}

// readCommitOnce reads a commit's structure through the private go-git handle,
// returning the signature separately so the caller can decide whether to
// delegate. It never touches the signature itself — that is the whole point.
func (r *signingRepo) readCommitOnce(hash repository.Hash) (repository.Commit, string, error) {
	r.ggMu.Lock()
	defer r.ggMu.Unlock()

	commit, err := r.gg.CommitObject(plumbing.NewHash(hash.String()))
	if err != nil {
		return repository.Commit{}, "", err
	}
	parents := make([]repository.Hash, len(commit.ParentHashes))
	for i, p := range commit.ParentHashes {
		parents[i] = repository.Hash(p.String())
	}
	return repository.Commit{
		Hash:     hash,
		Parents:  parents,
		TreeHash: repository.Hash(commit.TreeHash.String()),
	}, commit.PGPSignature, nil
}

// reopen refreshes the go-git handle so it picks up objects written since it was
// opened. It reports whether a fresh handle was installed.
func (r *signingRepo) reopen() bool {
	gg, err := gogit.PlainOpenWithOptions(r.dir, &gogit.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return false
	}
	r.ggMu.Lock()
	defer r.ggMu.Unlock()
	r.gg = gg
	return true
}

// signerName returns the identity git reports for the key that signed a commit
// (%GS): the ssh principal from allowed_signers, or the openpgp uid.
//
// The attester is taken from here rather than from the operation's author field
// on purpose. An attestation's entire value is that a KEY vouched for the
// history; reporting a self-asserted name beside a verified verdict would let
// the payload claim an identity the signature does not support.
func (r *signingRepo) signerName(hash repository.Hash) (string, error) {
	cmd := exec.Command("git", "-C", r.dir, "show", "-s", "--format=%GS", hash.String())
	cmd.Env = append(os.Environ(), r.env...)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("signer of %s: %w: %s",
			hash, err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// AddRemote forwards to the wrapped repo. kref type-asserts its repo handle to
// remoteAdder (sync.go), and embedding an INTERFACE hides every method the
// dynamic type has beyond that interface — so the assertion fails on the wrapper
// unless the wrapper carries the method itself.
func (r *signingRepo) AddRemote(name, url string) error {
	adder, ok := r.ClockedRepo.(remoteAdder)
	if !ok {
		return errors.New("repository does not support AddRemote")
	}
	return adder.AddRemote(name, url)
}

// ListCommits must be shadowed as well, even though the override below is a
// faithful copy of git-bug's own walk (repository/common.go
// nonNativeListCommits). git-bug's ListCommits hands *GoGitRepo — not the
// interface value kref holds — to that helper, so the helper's internal
// ReadCommit calls dispatch straight back to the dearmoring implementation and
// bypass the override above. Identity reads (entities/identity/identity.go)
// reach commits only through here, so without this they fail on any
// non-PGP-signed commit.
func (r *signingRepo) ListCommits(ref string) ([]repository.Hash, error) {
	hash, err := r.ResolveRef(ref)
	if err != nil {
		return nil, err
	}

	var result []repository.Hash
	stack := []repository.Hash{hash}
	visited := make(map[repository.Hash]struct{})

	for len(stack) > 0 {
		h := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if _, ok := visited[h]; ok {
			continue
		}
		visited[h] = struct{}{}
		result = append(result, h)

		commit, err := r.ReadCommit(h)
		if err != nil {
			return nil, err
		}
		stack = append(stack, commit.Parents...)
	}

	// Reverse the child-first discovery order, as git-bug's own copy of this walk
	// does. The result is roughly oldest-first, but it is a reversed DFS
	// pre-order, NOT a topological sort: across a merge a commit can appear
	// before its own parent. Callers that need real ancestry must compute it
	// (see ancestry in signature.go), not infer it from this order.
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result, nil
}

// opsTreeEntryName is git-bug's name for the blob holding a commit's serialized
// operation pack (entity/dag/operation_pack.go, `opsEntryName`). It is
// unexported there, so it is restated here; a canary spec pins the value.
const opsTreeEntryName = "ops"

// commitAttests returns the attestation this commit's operation pack carries,
// or nil if it carries none.
//
// It decodes only the type tag, claim and timestamp — enough to answer the
// question without resolving the author identity, which would need the entity
// resolvers. The attester's NAME is deliberately not taken from here: it comes
// from the signature (`%GS`) in a later task, because who signed the commit is
// a stronger fact than who the payload says signed it.
//
// This is the only way to answer the question. dag.Entity keeps its operations
// in one flat slice with a single lastCommit hash, so a compiled entry cannot
// say which commit an operation arrived in.
func (r *signingRepo) commitAttests(hash repository.Hash) (*entry.Attestation, error) {
	r.ggMu.Lock()
	defer r.ggMu.Unlock()

	commit, err := r.gg.CommitObject(plumbing.NewHash(hash.String()))
	if err != nil {
		return nil, fmt.Errorf("read commit %s: %w", hash, err)
	}
	tree, err := commit.Tree()
	if err != nil {
		return nil, fmt.Errorf("read tree of %s: %w", hash, err)
	}
	file, err := tree.File(opsTreeEntryName)
	switch {
	case errors.Is(err, object.ErrFileNotFound), errors.Is(err, object.ErrDirectoryNotFound):
		// A commit with no ops blob is not a kref operation pack. That is not an
		// error here: the walk covers whatever is reachable, and "carries no
		// attestation" is the honest answer.
		return nil, nil
	case err != nil:
		// Anything else is a storage failure. Tree.File surfaces those too, and
		// reporting one as "no attestation" would silently downgrade a verdict
		// on a corrupt object store — every other read on this path says so out
		// loud.
		return nil, fmt.Errorf("read ops entry of %s: %w", hash, err)
	}
	blob, err := file.Contents()
	if err != nil {
		return nil, fmt.Errorf("read ops of %s: %w", hash, err)
	}
	var pack struct {
		Ops []struct {
			Type      dag.OperationType `json:"type"`
			Claim     entry.Claim       `json:"claim"`
			Timestamp int64             `json:"timestamp"`
		} `json:"ops"`
	}
	if err := json.Unmarshal([]byte(blob), &pack); err != nil {
		return nil, fmt.Errorf("decode ops of %s: %w", hash, err)
	}
	for _, op := range pack.Ops {
		if op.Type != entry.AttestOp {
			continue
		}
		// Re-check the vocabulary on READ. Attest.Validate enforces it on write,
		// but git-bug only runs operation validation from Commit — read calls
		// operationPack.Validate, which checks the pack's author and not its
		// ops. So on a chain fetched from a peer, Claim is whatever bytes are in
		// the blob, and it flows verbatim into the rendered signature header.
		//
		// An unrecognised claim does not vouch. Failing closed here rather than
		// erroring keeps `kref show` readable: an entry carrying an
		// unintelligible attestation reports the verdict it had without one,
		// which is the honest answer.
		if op.Claim != entry.ClaimAuthored && op.Claim != entry.ClaimReceived {
			continue
		}
		return &entry.Attestation{
			At:    time.Unix(op.Timestamp, 0),
			Claim: op.Claim,
		}, nil
	}
	return nil, nil
}
