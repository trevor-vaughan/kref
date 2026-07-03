package store

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/git-bug/git-bug/entities/identity"
	"github.com/git-bug/git-bug/entity"
	"github.com/git-bug/git-bug/entity/dag"
	"github.com/git-bug/git-bug/repository"

	"github.com/riotbox/kref/internal/entry"
	"github.com/riotbox/kref/internal/scan"
)

const remoteConfigPrefix = "kref.remote." // kref.remote.<tier> = <remote name>

// remoteAdder is satisfied by *repository.GoGitRepo (and the test mock).
// It is narrower than repository.TestedRepo so we do not pull in test-only methods.
type remoteAdder interface {
	AddRemote(name, url string) error
}

// SetRemote configures (and, if url != "", creates) the git remote for a tier.
// The private tier may never have a remote.
func (s *Store) SetRemote(t entry.Tier, name, url string) error {
	d, err := s.DeclaredTier(string(t))
	if err != nil {
		return err
	}
	if d.Type == entry.TierPrivate {
		return fmt.Errorf("the private tier cannot have a remote")
	}
	if url != "" {
		remotes, err := s.repo.GetRemotes()
		if err != nil {
			return err
		}
		if _, exists := remotes[name]; !exists {
			adder, ok := s.repo.(remoteAdder)
			if !ok {
				return fmt.Errorf("repository does not support AddRemote")
			}
			if err := adder.AddRemote(name, url); err != nil {
				return err
			}
		}
	}
	return s.repo.LocalConfig().StoreString(remoteConfigPrefix+string(t), name)
}

// RemoteFor returns the configured remote name for a tier, or "" if none/private.
func (s *Store) RemoteFor(t entry.Tier) (string, error) {
	if t == entry.TierPrivate {
		return "", nil
	}
	name, err := s.repo.LocalConfig().ReadString(remoteConfigPrefix + string(t))
	if errors.Is(err, repository.ErrNoConfigEntry) {
		return "", nil // tier simply has no remote configured
	}
	if err != nil {
		return "", err // a real config-read failure must not look like "unset"
	}
	return name, nil
}

// Push sends a tier's entries to its configured remote. Private is always refused.
// It scans the outbound delta for secrets BEFORE any network contact (fail-closed),
// then records pushed-state so subsequent calls only re-scan changed entries.
//
// The scan→push→record sequence assumes the single-process invocation model (one
// Store per CLI run, no concurrency). A future long-lived Store shared across
// concurrent requests (e.g. an MCP server) must serialize Push or re-establish
// the scan/record invariant, or a concurrent write between scan and record could
// advance the pushed marker past unscanned content.
func (s *Store) Push(t entry.Tier) error {
	_, err := s.push(t, false)
	return err
}

// PushForce is Push with the missing-scanner refusal overridden: when
// betterleaks is unavailable the delta leaves the machine UNSCANNED, reported
// via unscanned=true so the caller can warn loudly. A positive finding from a
// working scanner still blocks — force never overrides a detected secret.
func (s *Store) PushForce(t entry.Tier) (unscanned bool, err error) {
	return s.push(t, true)
}

func (s *Store) push(t entry.Tier, allowUnscanned bool) (bool, error) {
	d, err := s.DeclaredTier(string(t))
	if err != nil {
		return false, err
	}
	if d.Type == entry.TierPrivate {
		return false, fmt.Errorf("refusing to push the private tier")
	}
	remote, err := s.RemoteFor(t)
	if err != nil {
		return false, err
	}
	if remote == "" {
		return false, fmt.Errorf("no remote configured for tier %s (use `kref remote set %s <name> <url>`)", t, t)
	}
	// Scan the delta about to leave; fail closed BEFORE any network contact.
	delta, err := s.pushDelta(t)
	if err != nil {
		return false, err
	}
	unscanned := false
	offenders, err := s.scanForPush(delta)
	switch {
	case err != nil && allowUnscanned && errors.Is(err, scan.ErrMissing):
		unscanned = true // explicit override: push what the scanner never saw
	case err != nil && errors.Is(err, scan.ErrMissing):
		return false, fmt.Errorf("%w\n(or re-run with --force to push UNSCANNED)", err)
	case err != nil:
		return false, err
	}
	if len(offenders) > 0 {
		return false, &PushBlockedError{Tier: t, Remote: remote, Offenders: offenders}
	}
	if _, err := identity.Push(s.repo, remote); err != nil {
		return unscanned, fmt.Errorf("push identity: %w", err)
	}
	if _, err = dag.Push(entry.Definition(t), s.repo, remote); err != nil {
		return unscanned, err
	}
	// Record what left so the next push scans only the new delta. Forced
	// (unscanned) entries are recorded too: they have already left, so a later
	// scan could not unpublish them.
	return unscanned, s.recordPushed(t, delta)
}

// Pull fetches and merges a tier's entries from its configured remote.
// It syncs the remote's identity refs first so that operation authors can be resolved.
func (s *Store) Pull(t entry.Tier) error {
	d, err := s.DeclaredTier(string(t))
	if err != nil {
		return err
	}
	if d.Type == entry.TierPrivate {
		return fmt.Errorf("the private tier has no remote")
	}
	remote, err := s.RemoteFor(t)
	if err != nil {
		return err
	}
	if remote == "" {
		return fmt.Errorf("no remote configured for tier %s", t)
	}
	// Identity refs must arrive before entry refs so resolvers can find authors.
	if err := identity.Pull(s.repo, remote); err != nil {
		return fmt.Errorf("pulling identities from %s: %w", remote, err)
	}
	if _, err := dag.Fetch(entry.Definition(t), s.repo, remote); err != nil {
		return err
	}
	var incoming []entity.Id
	for m := range dag.MergeAll(entry.Definition(t), entry.WrapForRead(), s.repo, resolvers(s.repo), remote, s.author) {
		if m.Err != nil {
			return m.Err
		}
		if m.Status == entity.MergeStatusInvalid {
			return fmt.Errorf("merge failure: %s", m.Reason)
		}
		if m.Status == entity.MergeStatusNew || m.Status == entity.MergeStatusUpdated {
			incoming = append(incoming, m.Id)
		}
	}
	return s.recordIncoming(t, incoming)
}

func incomingKey(t entry.Tier) string { return "kref.incoming." + string(t) }

// recordIncoming stores this pull's New/Updated entries with their post-pull
// op-count as "kref.incoming.<tier> = hex:count hex:count ...", rewritten each pull.
func (s *Store) recordIncoming(t entry.Tier, ids []entity.Id) error {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		e, err := entry.Read(s.repo, t, id)
		if err != nil {
			return fmt.Errorf("read incoming %s: %w", id, err)
		}
		parts = append(parts, id.String()+":"+strconv.Itoa(len(e.Operations())))
	}
	return s.repo.LocalConfig().StoreString(incomingKey(t), strings.Join(parts, " "))
}

// incomingEntries parses the recorded incoming set for a tier into id→op-count.
func (s *Store) incomingEntries(t entry.Tier) map[string]int {
	raw, err := s.repo.LocalConfig().ReadString(incomingKey(t))
	if err != nil || strings.TrimSpace(raw) == "" {
		return map[string]int{}
	}
	out := map[string]int{}
	for _, p := range strings.Fields(raw) {
		hex, cnt, ok := strings.Cut(p, ":")
		if !ok {
			continue
		}
		n, err := strconv.Atoi(cnt)
		if err != nil {
			continue
		}
		out[hex] = n
	}
	return out
}

// TierRemote describes one tier's sync-remote configuration for display.
type TierRemote struct {
	Tier     entry.Tier
	Type     entry.Tier // drives the badge glyph/color
	Remote   string     // configured git remote name; "" when none
	URL      string     // the remote's fetch URL; "" when the remote is gone or unset
	Syncable bool       // false only for the private tier, which can never sync
}

// Remotes reports every DECLARED tier's remote configuration in tier order
// (remotes are a declared-tier feature; discovered namespaces cannot take
// one). Private-typed tiers are included (Syncable=false) so callers can
// present the full model.
func (s *Store) Remotes() ([]TierRemote, error) {
	urls, err := s.repo.GetRemotes()
	if err != nil {
		return nil, err
	}
	out := make([]TierRemote, 0, len(s.tiers))
	for _, d := range s.tiers {
		if !d.Declared {
			continue
		}
		if d.Type == entry.TierPrivate {
			out = append(out, TierRemote{Tier: d.Name, Type: d.Type})
			continue
		}
		name, err := s.RemoteFor(d.Name)
		if err != nil {
			return nil, err
		}
		out = append(out, TierRemote{Tier: d.Name, Type: d.Type, Remote: name, URL: urls[name], Syncable: true})
	}
	return out, nil
}

const noRemoteWarnKey = "kref.warn.noremote" // unix seconds of the last no-remote warning

// WarnNoRemoteDue reports whether the periodic "no remote configured — your
// work has no off-machine copy" warning should fire: at least one entry lives
// in a syncable (non-private) tier, no syncable tier has a remote, and the
// last warning is at least interval old. Private-only stores stay quiet —
// private structurally cannot sync, so there is nothing to configure.
func (s *Store) WarnNoRemoteDue(now time.Time, interval time.Duration) (bool, error) {
	configured, err := s.SyncableTiers()
	if err != nil {
		return false, err
	}
	if len(configured) > 0 {
		return false, nil
	}
	snaps, err := s.List(ListFilter{})
	if err != nil {
		return false, err
	}
	syncable := false
	for _, sn := range snaps {
		if sn.Tier != string(entry.TierPrivate) {
			syncable = true
			break
		}
	}
	if !syncable {
		return false, nil
	}
	raw, err := s.repo.LocalConfig().ReadString(noRemoteWarnKey)
	if errors.Is(err, repository.ErrNoConfigEntry) {
		return true, nil // never warned
	}
	if err != nil {
		return false, err
	}
	last, convErr := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if convErr != nil {
		return true, nil // unreadable marker — warn and let Mark rewrite it
	}
	return now.Sub(time.Unix(last, 0)) >= interval, nil
}

// MarkNoRemoteWarned records now as the last time the no-remote warning fired.
func (s *Store) MarkNoRemoteWarned(now time.Time) error {
	return s.repo.LocalConfig().StoreString(noRemoteWarnKey, strconv.FormatInt(now.Unix(), 10))
}

// SyncableTiers returns declared, non-private-typed tiers that have a
// configured remote.
func (s *Store) SyncableTiers() ([]entry.Tier, error) {
	var out []entry.Tier
	for _, d := range s.tiers {
		if !d.Declared || d.Type == entry.TierPrivate {
			continue
		}
		r, err := s.RemoteFor(d.Name)
		if err != nil {
			return nil, err
		}
		if r != "" {
			out = append(out, d.Name)
		}
	}
	return out, nil
}
