package store

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"

	"github.com/git-bug/git-bug/entity/dag"
	"github.com/git-bug/git-bug/repository"

	"github.com/trevor-vaughan/kref/internal/entry"
)

// tierConfigPrefix declares custom tiers in machine-local git config, next to
// their remotes: kref.tier.<name> = personal|shared. A tier definition without
// its remote is inert, so the two belong in the same (per-machine) store.
const tierConfigPrefix = "kref.tier."

// resolveTiers builds the store's tier set: the built-ins, plus customs
// declared in git config, plus namespaces discovered from existing refs/kref-*
// (a teammate's tiers arriving via fetch or bundle render instead of being
// invisible). Discovered-only tiers are typed shared for display and marked
// undeclared; writes to them are refused by DeclaredTier.
func resolveTiers(repo repository.ClockedRepo) ([]entry.TierDef, error) {
	defs := entry.BuiltinTierDefs()
	seen := map[entry.Tier]bool{}
	for _, d := range defs {
		seen[d.Name] = true
	}
	declared, err := repo.LocalConfig().ReadAll(tierConfigPrefix)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(declared))
	for k := range declared {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic error selection on multiple bad keys
	for _, k := range keys {
		name := strings.TrimPrefix(k, tierConfigPrefix)
		typ := entry.Tier(declared[k])
		if err := entry.ValidateTierName(name); err != nil {
			return nil, fmt.Errorf("config %s: %w", k, err)
		}
		if !entry.ValidTierType(typ) {
			return nil, fmt.Errorf("config %s: invalid tier type %q (want personal|shared)", k, typ)
		}
		defs = append(defs, entry.TierDef{Name: entry.Tier(name), Type: typ, Declared: true})
		seen[entry.Tier(name)] = true
	}
	refs, err := repo.ListRefs("refs/kref-")
	if err != nil {
		return nil, err
	}
	for _, r := range refs {
		rest := strings.TrimPrefix(r, "refs/kref-")
		name, _, ok := strings.Cut(rest, "/")
		if !ok || seen[entry.Tier(name)] {
			continue
		}
		// Reserved names (pushed) and malformed namespaces are bookkeeping or
		// junk, not tiers; skipping is the correct read-side behavior.
		if entry.ValidateTierName(name) != nil {
			continue
		}
		defs = append(defs, entry.TierDef{Name: entry.Tier(name), Type: entry.TierShared, Declared: false})
		seen[entry.Tier(name)] = true
	}
	entry.SortTierDefs(defs)
	return defs, nil
}

// witnessTierClocks mirrors OpenGoGitRepo's clock-loader pass for tiers that
// were unknowable at open time (custom tiers come from git config, which needs
// the repo open first). A namespace with missing clocks gets every entity
// witnessed, so the next write cannot mint a lamport time that precedes
// existing history. Clock names follow git-bug's dag convention
// ("<namespace>-create"/"<namespace>-edit", entity/dag/entity.go).
func witnessTierClocks(repo repository.ClockedRepo, defs []entry.TierDef) error {
	clocks, err := repo.AllClocks()
	if err != nil {
		return err
	}
	var missing []dag.Definition
	for _, d := range defs {
		if d.Builtin() || !d.Declared {
			continue
		}
		ns := d.Name.Namespace()
		_, createOK := clocks[ns+"-create"]
		_, editOK := clocks[ns+"-edit"]
		if createOK && editOK {
			continue
		}
		missing = append(missing, entry.Definition(d.Name))
	}
	if len(missing) == 0 {
		return nil
	}
	return dag.ClockLoader(missing...).Witnesser(repo)
}

// reloadTiers re-resolves the tier set after a declaration change.
func (s *Store) reloadTiers() error {
	tiers, err := resolveTiers(s.repo)
	if err != nil {
		return err
	}
	if err := witnessTierClocks(s.repo, tiers); err != nil {
		return err
	}
	s.tiers = tiers
	return nil
}

// Tiers returns the resolved tier set in display order.
func (s *Store) Tiers() []entry.TierDef {
	out := make([]entry.TierDef, len(s.tiers))
	copy(out, s.tiers)
	return out
}

// TierNames returns every resolved tier name in display order — the iteration
// set for read paths (list, get, resolve, hygiene).
func (s *Store) TierNames() []entry.Tier {
	out := make([]entry.Tier, len(s.tiers))
	for i, d := range s.tiers {
		out[i] = d.Name
	}
	return out
}

// TierDef resolves a tier name (declared or discovered), erroring with the
// known set when absent.
func (s *Store) TierDef(name string) (entry.TierDef, error) {
	for _, d := range s.tiers {
		if string(d.Name) == name {
			return d, nil
		}
	}
	known := make([]string, len(s.tiers))
	for i, d := range s.tiers {
		known[i] = string(d.Name)
	}
	return entry.TierDef{}, fmt.Errorf("unknown tier %q (known: %s)", name, strings.Join(known, ", "))
}

// DeclaredTier resolves a tier name for a write target: the tier must be
// declared (built-in or kref.tier.<name> config), not merely discovered from
// refs someone else pushed.
func (s *Store) DeclaredTier(name string) (entry.TierDef, error) {
	d, err := s.TierDef(name)
	if err != nil {
		return entry.TierDef{}, err
	}
	if !d.Declared {
		return entry.TierDef{}, fmt.Errorf("tier %q exists in refs but is not declared on this machine — declare it first: kref tier add %s --type personal|shared", name, name)
	}
	return d, nil
}

// TierType returns the resolved type of a tier name, defaulting to shared for
// unknown names (the safe display default for foreign namespaces).
func (s *Store) TierType(t entry.Tier) entry.Tier {
	for _, d := range s.tiers {
		if d.Name == t {
			return d.Type
		}
	}
	return entry.TierShared
}

// TierAdd declares a custom tier and optionally wires its remote in one step.
// remoteName may be "" (declare only); remoteURL requires remoteName and
// creates the git remote when it does not already exist (same semantics as
// SetRemote).
func (s *Store) TierAdd(name string, typ entry.Tier, remoteName, remoteURL string) error {
	if err := entry.ValidateTierName(name); err != nil {
		return err
	}
	if !entry.ValidTierType(typ) {
		return fmt.Errorf("invalid tier type %q (want personal|shared)", typ)
	}
	if d, err := s.TierDef(name); err == nil && d.Declared && !d.Builtin() {
		return fmt.Errorf("tier %q already declared (type %s)", name, d.Type)
	}
	if err := s.repo.LocalConfig().StoreString(tierConfigPrefix+name, string(typ)); err != nil {
		return err
	}
	// reloadTiers also witnesses the namespace's clocks, which matters when the
	// declaration adopts refs that arrived earlier (a previously-discovered tier).
	if err := s.reloadTiers(); err != nil {
		return err
	}
	if remoteName != "" {
		return s.SetRemote(entry.Tier(name), remoteName, remoteURL)
	}
	return nil
}

// TierRemove undeclares a custom tier. It refuses while refs/kref-<name>/*
// still hold entries unless force, which orphans the namespace: the refs stay
// (the tier becomes discovered/undeclared, read-only) — nothing is deleted.
func (s *Store) TierRemove(name string, force bool) error {
	d, err := s.TierDef(name)
	if err != nil {
		return err
	}
	if d.Builtin() {
		return fmt.Errorf("cannot remove built-in tier %q", name)
	}
	if !d.Declared {
		return fmt.Errorf("tier %q is not declared on this machine", name)
	}
	refs, err := s.repo.ListRefs("refs/" + entry.Tier(name).Namespace() + "/")
	if err != nil {
		return err
	}
	if len(refs) > 0 && !force {
		return fmt.Errorf("tier %q still holds %d entry(ies) — `kref retier` them first, or --force to orphan the namespace (refs stay, tier becomes undeclared)", name, len(refs))
	}
	// git-bug's RemoveAll cannot remove a single option inside a subsection
	// ([kref "tier"] foo) — it only matches whole subsections. Shell out, as
	// Purge already does for repo surgery.
	if err := gitConfigUnset(s.dir, tierConfigPrefix+name); err != nil {
		return err
	}
	if err := gitConfigUnset(s.dir, remoteConfigPrefix+name); err != nil {
		return err
	}
	return s.reloadTiers()
}

// gitConfigUnset removes one local git-config key; a missing key is not an
// error (git exits 5 for unset-on-absent).
func gitConfigUnset(dir, key string) error {
	cmd := exec.Command("git", "-C", dir, "config", "--unset", key)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	err := cmd.Run()
	var xerr *exec.ExitError
	if errors.As(err, &xerr) && xerr.ExitCode() == 5 {
		return nil
	}
	return err
}
