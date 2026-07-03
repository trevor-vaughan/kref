package store

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/git-bug/git-bug/entities/identity"
	"github.com/git-bug/git-bug/entity"
	"github.com/git-bug/git-bug/entity/dag"
	"github.com/git-bug/git-bug/repository"
	gogit "github.com/go-git/go-git/v5"

	"github.com/trevor-vaughan/kref/internal/config"
	"github.com/trevor-vaughan/kref/internal/entry"
)

const localStorageNamespace = "kref"

// Store is a kref knowledge store backed by a git repo's ref namespaces.
type Store struct {
	repo   repository.ClockedRepo
	author identity.Interface
	dir    string
	tiers  []entry.TierDef // resolved at Open/Init; display order

	cfg         *config.Config // effective (merged) config; populated at Open/Init
	cfgWarnings []string       // non-fatal warnings from the last config load
	// Per-layer favorites, retained from the last load so FavoriteOrigin can
	// report where a name came from. Either may be nil (layer absent).
	userFavs    map[string]string
	projectFavs map[string]string
}

// clockLoaders registers the lamport clocks for the built-in tiers at open
// time. Built-ins only: custom namespaces are witnessed post-open
// (witnessTierClocks).
func clockLoaders() []repository.ClockLoader {
	defs := make([]dag.Definition, 0, len(entry.AllTiers()))
	for _, t := range entry.AllTiers() {
		defs = append(defs, entry.Definition(t))
	}
	return []repository.ClockLoader{dag.ClockLoader(defs...)}
}

// Init bootstraps kref inside an EXISTING git repository at dir: it opens the
// repo, creates an author identity, and persists it as the repo user. The
// repository must already exist (run `git init` first) — kref stores its
// knowledge in the repo whose history you want it to travel with, so it never
// creates a throwaway repo of its own.
func Init(dir, name, email string) (*Store, error) {
	clean := filepath.Clean(dir)
	// Require an existing git repo. git-bug's OpenGoGitRepo searches upward for
	// .git but reports "no repo" with a non-sentinel error, so pre-check with
	// go-git's PlainOpenWithOptions (same upward search) for a reliable sentinel.
	if _, err := gogit.PlainOpenWithOptions(clean, &gogit.PlainOpenOptions{DetectDotGit: true}); err != nil {
		if errors.Is(err, gogit.ErrRepositoryNotExists) {
			return nil, fmt.Errorf("%s is not a git repository — run `git init` first, then `kref init`", clean)
		}
		return nil, err
	}
	repo, err := repository.OpenGoGitRepo(clean, localStorageNamespace, clockLoaders())
	if err != nil {
		return nil, err
	}
	author, err := newAuthor(repo, name, email)
	if err != nil {
		return nil, err
	}
	if err := author.Commit(repo); err != nil {
		return nil, err
	}
	// Persist this identity as the repo's user so Open() reloads the SAME
	// (committed, resolvable) identity rather than minting a fresh one.
	if err := identity.SetUserIdentity(repo, author); err != nil {
		return nil, err
	}
	st := &Store{repo: repo, author: author, dir: clean}
	if err := st.reloadTiers(); err != nil {
		return nil, err
	}
	if err := st.loadConfig(); err != nil {
		return nil, err
	}
	return st, nil
}

// newAuthor builds the author identity: explicit name/email when provided,
// otherwise the repository's configured git user.
func newAuthor(repo repository.ClockedRepo, name, email string) (*identity.Identity, error) {
	if name == "" && email == "" {
		return identity.NewFromGitUser(repo)
	}
	return identity.NewIdentity(repo, name, email)
}

// Author returns the display name and email of the store's author identity.
func (s *Store) Author() (string, string) {
	return s.author.Name(), s.author.Email()
}

// Initialized reports whether dir already has a primary author identity (the
// local pointer set by a prior Init), returning its name/email when so. It
// creates nothing and resolves no env/config override — it reports the stored
// primary only. A repo-open error yields ok=false so the caller can fall through
// to Init for the canonical "not a git repository" message.
func Initialized(dir string) (name, email string, ok bool, err error) {
	repo, err := repository.OpenGoGitRepo(filepath.Clean(dir), localStorageNamespace, clockLoaders())
	if err != nil {
		return "", "", false, nil
	}
	defer func() { _ = repo.Close() }()
	a, err := identity.GetUserIdentity(repo)
	if errors.Is(err, identity.ErrNoIdentitySet) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return a.Name(), a.Email(), true, nil
}

// Open opens an existing store and loads the default author identity.
func Open(dir string) (*Store, error) {
	repo, err := repository.OpenGoGitRepo(filepath.Clean(dir), localStorageNamespace, clockLoaders())
	if err != nil {
		return nil, err
	}
	author, err := resolveAuthor(repo)
	if err != nil {
		return nil, err
	}
	st := &Store{repo: repo, author: author, dir: filepath.Clean(dir)}
	if err := st.reloadTiers(); err != nil {
		return nil, err
	}
	if err := st.loadConfig(); err != nil {
		return nil, err
	}
	return st, nil
}

// resolveAuthor picks the identity that authors operations, by precedence:
// env (KREF_AUTHOR_NAME + KREF_AUTHOR_EMAIL) > git config (kref.author.name +
// kref.author.email, merged global+local) > the init-time stored repo identity.
func resolveAuthor(repo repository.ClockedRepo) (identity.Interface, error) {
	name, email, source, err := authorOverride(repo)
	if err != nil {
		return nil, err
	}
	if source != "" {
		return findOrCreateIdentity(repo, name, email)
	}
	author, err := identity.GetUserIdentity(repo)
	if err != nil {
		return nil, fmt.Errorf("no author identity (run `kref init`): %w", err)
	}
	return author, nil
}

// authorOverride returns an explicit (name, email) author and its source, or an
// empty source when none is configured. A source supplying exactly one of
// name/email is an error, so a mixed (e.g. human-name + container-email)
// identity is never silently assembled.
func authorOverride(repo repository.ClockedRepo) (name, email, source string, err error) {
	en := strings.TrimSpace(os.Getenv("KREF_AUTHOR_NAME"))
	ee := strings.TrimSpace(os.Getenv("KREF_AUTHOR_EMAIL"))
	if en != "" || ee != "" {
		if en == "" || ee == "" {
			return "", "", "", fmt.Errorf("set both KREF_AUTHOR_NAME and KREF_AUTHOR_EMAIL, or neither")
		}
		return en, ee, "env", nil
	}
	cn := configString(repo, "kref.author.name")
	ce := configString(repo, "kref.author.email")
	if cn != "" || ce != "" {
		if cn == "" || ce == "" {
			return "", "", "", fmt.Errorf("set both kref.author.name and kref.author.email in git config, or neither")
		}
		return cn, ce, "gitconfig", nil
	}
	return "", "", "", nil
}

// configString reads one merged (global+local) git config value, treating a
// missing key as empty.
func configString(repo repository.ClockedRepo, key string) string {
	v, err := repo.AnyConfig().ReadString(key)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(v)
}

// findOrCreateIdentity returns the existing local identity matching name+email,
// else creates and commits a new one — keeping an overridden author resolvable
// for sync without minting a duplicate identity on every run.
func findOrCreateIdentity(repo repository.ClockedRepo, name, email string) (identity.Interface, error) {
	for streamed := range identity.ReadAllLocal(repo) {
		if streamed.Err != nil {
			return nil, streamed.Err
		}
		if i := streamed.Entity; i.Name() == name && i.Email() == email {
			return i, nil
		}
	}
	i, err := identity.NewIdentity(repo, name, email)
	if err != nil {
		return nil, err
	}
	if err := i.Commit(repo); err != nil {
		return nil, err
	}
	return i, nil
}

func (s *Store) Close() error { return s.repo.Close() }

// Add creates a new entry in a tier with the default content type.
func (s *Store) Add(t entry.Tier, kind, title, body string) (entity.Id, error) {
	return s.AddWithContentType(t, kind, title, body, "")
}

// AddWithContentType creates a new entry, recording its content type (empty
// means the entry-layer default, text/markdown).
func (s *Store) AddWithContentType(t entry.Tier, kind, title, body, contentType string) (entity.Id, error) {
	e := entry.New(t)
	c := entry.NewCreate(s.author, kind, title)
	c.ContentType = contentType
	e.Append(c)
	if body != "" {
		e.Append(entry.NewSetBody(s.author, body))
	}
	if err := e.Commit(s.repo); err != nil {
		return "", err
	}
	return e.Id(), nil
}

// Get loads and compiles an entry, searching all tiers.
func (s *Store) Get(id entity.Id) (*entry.Snapshot, error) {
	for _, t := range s.TierNames() {
		e, err := entry.Read(s.repo, t, id)
		if err != nil {
			if entity.IsErrNotFound(err) {
				continue
			}
			return nil, fmt.Errorf("read %s in tier %s: %w", id, t, err)
		}
		snap := e.Compile()
		snap.Tier = string(t)
		snap.TierType = string(s.TierType(t))
		return snap, nil
	}
	return nil, fmt.Errorf("entry %s not found", id)
}

// ListFilter narrows a List query.
type ListFilter struct {
	Kind            string
	Status          string
	Tier            entry.Tier
	Search          string
	Labels          []string
	IncludeDelete   bool
	IncludeArchived bool // include archived entries alongside the rest
	ArchivedOnly    bool // restrict to archived entries (a dedicated archive view)
}

// List returns compiled snapshots across tiers, applying the filter.
func (s *Store) List(f ListFilter) ([]*entry.Snapshot, error) {
	var out []*entry.Snapshot
	needle := strings.ToLower(f.Search)
	for _, t := range s.TierNames() {
		if f.Tier != "" && f.Tier != t {
			continue
		}
		for streamed := range dag.ReadAll(entry.Definition(t), entry.WrapForRead(), s.repo, resolvers(s.repo)) {
			if streamed.Err != nil {
				return nil, streamed.Err
			}
			snap := streamed.Entity.Compile()
			snap.Tier = string(t)
			snap.TierType = string(s.TierType(t))
			if snap.Deleted && !f.IncludeDelete {
				continue
			}
			if f.ArchivedOnly && !snap.Archived {
				continue
			}
			if snap.Archived && !f.ArchivedOnly && !f.IncludeArchived {
				continue
			}
			if f.Kind != "" && snap.Kind != f.Kind {
				continue
			}
			if f.Status != "" && snap.Status != f.Status {
				continue
			}
			if needle != "" &&
				!strings.Contains(strings.ToLower(snap.Title), needle) &&
				!strings.Contains(strings.ToLower(snap.Body), needle) {
				continue
			}
			if len(f.Labels) > 0 {
				have := make(map[string]bool, len(snap.Labels))
				for _, l := range snap.Labels {
					have[l] = true
				}
				missing := false
				for _, want := range f.Labels {
					if !have[want] {
						missing = true
						break
					}
				}
				if missing {
					continue
				}
			}
			out = append(out, snap)
		}
	}
	return out, nil
}

// SearchResult pairs a snapshot with how many times the query occurs in its
// title and body (case-insensitive). The embedded snapshot keeps the JSON
// shape a superset of List's, plus a "matches" field.
type SearchResult struct {
	*entry.Snapshot
	Matches int `json:"matches"`
}

// Search lists entries matching f (f.Search is the query) and counts the
// query's occurrences across each entry's title and body, most matches first
// (stable order within equal counts).
func (s *Store) Search(f ListFilter) ([]SearchResult, error) {
	if strings.TrimSpace(f.Search) == "" {
		return nil, fmt.Errorf("search query is empty")
	}
	items, err := s.List(f)
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(f.Search)
	out := make([]SearchResult, 0, len(items))
	for _, it := range items {
		n := strings.Count(strings.ToLower(it.Title), q) + strings.Count(strings.ToLower(it.Body), q)
		out = append(out, SearchResult{Snapshot: it, Matches: n})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Matches > out[j].Matches })
	return out, nil
}

// ErrNoEntries indicates the store holds no non-deleted entries.
var ErrNoEntries = errors.New("no entries yet")

// MostRecent returns the entry with the latest UpdatedAt across all tiers,
// excluding deleted entries. Ties break by id (ascending) so repeated runs are
// stable. It returns ErrNoEntries when the store is empty.
func (s *Store) MostRecent() (*entry.Snapshot, error) {
	items, err := s.List(ListFilter{})
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, ErrNoEntries
	}
	best := items[0]
	for _, it := range items[1:] {
		switch {
		case it.UpdatedAt.After(best.UpdatedAt):
			best = it
		case it.UpdatedAt.Equal(best.UpdatedAt) && it.ID.String() < best.ID.String():
			best = it
		}
	}
	return best, nil
}

// Tombstone soft-deletes an entry by appending a tombstone op in its tier.
func (s *Store) Tombstone(id entity.Id) error {
	for _, t := range s.TierNames() {
		e, err := entry.Read(s.repo, t, id)
		if err != nil {
			if entity.IsErrNotFound(err) {
				continue
			}
			return fmt.Errorf("read %s in tier %s: %w", id, t, err)
		}
		e.Append(entry.NewTombstone(s.author))
		return e.Commit(s.repo)
	}
	return fmt.Errorf("entry %s not found", id)
}

// SetStatus appends a status change to an entry, searching all tiers.
func (s *Store) SetStatus(id entity.Id, status string) error {
	for _, t := range s.TierNames() {
		e, err := entry.Read(s.repo, t, id)
		if err != nil {
			if entity.IsErrNotFound(err) {
				continue
			}
			return fmt.Errorf("read %s in tier %s: %w", id, t, err)
		}
		e.Append(entry.NewSetStatus(s.author, status))
		return e.Commit(s.repo)
	}
	return fmt.Errorf("entry %s not found", id)
}

// SetKind appends a kind change to an entry, searching all tiers.
func (s *Store) SetKind(id entity.Id, kind string) error {
	for _, t := range s.TierNames() {
		e, err := entry.Read(s.repo, t, id)
		if err != nil {
			if entity.IsErrNotFound(err) {
				continue
			}
			return fmt.Errorf("read %s in tier %s: %w", id, t, err)
		}
		e.Append(entry.NewSetKind(s.author, kind))
		return e.Commit(s.repo)
	}
	return fmt.Errorf("entry %s not found", id)
}

// SetContentType replaces an entry's content type.
func (s *Store) SetContentType(id entity.Id, contentType string) error {
	for _, t := range s.TierNames() {
		e, err := entry.Read(s.repo, t, id)
		if err != nil {
			if entity.IsErrNotFound(err) {
				continue
			}
			return fmt.Errorf("read %s in tier %s: %w", id, t, err)
		}
		e.Append(entry.NewSetContentType(s.author, contentType))
		return e.Commit(s.repo)
	}
	return fmt.Errorf("entry %s not found", id)
}

// Reattribute appends a displayed-author change to an entry, searching all tiers.
func (s *Store) Reattribute(id entity.Id, name, email string) error {
	for _, t := range s.TierNames() {
		e, err := entry.Read(s.repo, t, id)
		if err != nil {
			if entity.IsErrNotFound(err) {
				continue
			}
			return fmt.Errorf("read %s in tier %s: %w", id, t, err)
		}
		e.Append(entry.NewReattribute(s.author, name, email))
		return e.Commit(s.repo)
	}
	return fmt.Errorf("entry %s not found", id)
}

// Archive hides an entry from the normal list, searching all tiers. Status is
// preserved; reverse with Unarchive.
func (s *Store) Archive(id entity.Id) error {
	for _, t := range s.TierNames() {
		e, err := entry.Read(s.repo, t, id)
		if err != nil {
			if entity.IsErrNotFound(err) {
				continue
			}
			return fmt.Errorf("read %s in tier %s: %w", id, t, err)
		}
		e.Append(entry.NewArchive(s.author))
		return e.Commit(s.repo)
	}
	return fmt.Errorf("entry %s not found", id)
}

// Unarchive reverses Archive, returning an entry to the normal list.
func (s *Store) Unarchive(id entity.Id) error {
	for _, t := range s.TierNames() {
		e, err := entry.Read(s.repo, t, id)
		if err != nil {
			if entity.IsErrNotFound(err) {
				continue
			}
			return fmt.Errorf("read %s in tier %s: %w", id, t, err)
		}
		e.Append(entry.NewUnarchive(s.author))
		return e.Commit(s.repo)
	}
	return fmt.Errorf("entry %s not found", id)
}

// Track marks an entry as synced with a local file at a repo-relative path,
// searching all tiers. Re-tracking an already-tracked entry re-points the path.
func (s *Store) Track(id entity.Id, path string) error {
	for _, t := range s.TierNames() {
		e, err := entry.Read(s.repo, t, id)
		if err != nil {
			if entity.IsErrNotFound(err) {
				continue
			}
			return fmt.Errorf("read %s in tier %s: %w", id, t, err)
		}
		e.Append(entry.NewTrack(s.author, path))
		return e.Commit(s.repo)
	}
	return fmt.Errorf("entry %s not found", id)
}

// Untrack clears an entry's local-file tracking, searching all tiers. The file
// on disk is left untouched.
func (s *Store) Untrack(id entity.Id) error {
	for _, t := range s.TierNames() {
		e, err := entry.Read(s.repo, t, id)
		if err != nil {
			if entity.IsErrNotFound(err) {
				continue
			}
			return fmt.Errorf("read %s in tier %s: %w", id, t, err)
		}
		e.Append(entry.NewUntrack(s.author))
		return e.Commit(s.repo)
	}
	return fmt.Errorf("entry %s not found", id)
}

// AddLabel adds a label to an entry, searching all tiers.
func (s *Store) AddLabel(id entity.Id, label string) error {
	for _, t := range s.TierNames() {
		e, err := entry.Read(s.repo, t, id)
		if err != nil {
			if entity.IsErrNotFound(err) {
				continue
			}
			return fmt.Errorf("read %s in tier %s: %w", id, t, err)
		}
		e.Append(entry.NewAddLabel(s.author, label))
		return e.Commit(s.repo)
	}
	return fmt.Errorf("entry %s not found", id)
}

// RemoveLabel removes a label from an entry, searching all tiers.
func (s *Store) RemoveLabel(id entity.Id, label string) error {
	for _, t := range s.TierNames() {
		e, err := entry.Read(s.repo, t, id)
		if err != nil {
			if entity.IsErrNotFound(err) {
				continue
			}
			return fmt.Errorf("read %s in tier %s: %w", id, t, err)
		}
		e.Append(entry.NewRemoveLabel(s.author, label))
		return e.Commit(s.repo)
	}
	return fmt.Errorf("entry %s not found", id)
}

// Restore reverses a Tombstone, making the entry live again.
func (s *Store) Restore(id entity.Id) error {
	for _, t := range s.TierNames() {
		e, err := entry.Read(s.repo, t, id)
		if err != nil {
			if entity.IsErrNotFound(err) {
				continue
			}
			return fmt.Errorf("read %s in tier %s: %w", id, t, err)
		}
		e.Append(entry.NewRestore(s.author))
		return e.Commit(s.repo)
	}
	return fmt.Errorf("entry %s not found", id)
}

// Update appends a body (and, if changed, title) op to an existing entry,
// searching every tier. It is the mutating half of marker-based re-ingest.
func (s *Store) Update(id entity.Id, body, title string) error {
	for _, t := range s.TierNames() {
		e, err := entry.Read(s.repo, t, id)
		if err != nil {
			if entity.IsErrNotFound(err) {
				continue
			}
			return fmt.Errorf("read %s: %w", id, err)
		}
		snap := e.Compile()
		changed := false
		if body != snap.Body {
			e.Append(entry.NewSetBody(s.author, body))
			changed = true
		}
		if title != "" && title != snap.Title {
			e.Append(entry.NewSetTitle(s.author, title))
			changed = true
		}
		if !changed {
			return nil
		}
		return e.Commit(s.repo)
	}
	return fmt.Errorf("entry %s not found", id)
}

// Purge hard-deletes an entry: optionally deletes its ref on the tier's remote
// (push), removes the local ref, and (gc) excises the now-unreferenced objects.
// Irreversible.
func (s *Store) Purge(id entity.Id, gc, push bool) error {
	var found entry.Tier
	ok := false
	for _, t := range s.TierNames() {
		if _, err := entry.Read(s.repo, t, id); err != nil {
			if entity.IsErrNotFound(err) {
				continue
			}
			return fmt.Errorf("read %s in tier %s: %w", id, t, err)
		}
		found, ok = t, true
		break
	}
	if !ok {
		return fmt.Errorf("entry %s not found", id)
	}

	if push {
		if found == entry.TierPrivate {
			return fmt.Errorf("the private tier has no remote to purge from")
		}
		remote, err := s.RemoteFor(found)
		if err != nil {
			return err
		}
		if remote == "" {
			return fmt.Errorf("no remote configured for tier %s", found)
		}
		ref := fmt.Sprintf("refs/%s/%s", found.Namespace(), id)
		cmd := exec.Command("git", "-C", s.dir, "push", remote, "--delete", ref)
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("delete %s on remote %s: %w", ref, remote, err)
		}
	}

	if err := dag.Remove(entry.Definition(found), s.repo, id); err != nil {
		return fmt.Errorf("remove %s in tier %s: %w", id, found, err)
	}
	if gc {
		cmd := exec.Command("git", "-C", s.dir, "gc", "--prune=now", "--quiet")
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("git gc: %w", err)
		}
	}
	return nil
}

// Resolve expands a full id or an unambiguous hex prefix to a stored entry id,
// searching every tier. The not-found and ambiguous errors never name a tier —
// callers see one entry space, not kref's internal per-tier loop.
func (s *Store) Resolve(prefix string) (entity.Id, error) {
	if prefix == "" {
		return "", fmt.Errorf("empty id")
	}
	// A favorite name is disjoint from any hex id-prefix (config.ValidFavoriteName
	// forbids pure-hex names), so this never shadows an id: only non-hex tokens
	// can match a favorite key.
	if id, ok := s.EffectiveConfig().Favorites[prefix]; ok {
		return entity.Id(id), nil
	}
	// kref.conf is the reserved name for the project config entry, discovered by
	// KIND (not via the favorites map) so it works before any favorite exists.
	if prefix == "kref.conf" {
		if _, id, ok := s.findConfigEntry(); ok {
			return entity.Id(id), nil
		}
	}
	var matches []entity.Id
	seen := map[entity.Id]bool{}
	for _, t := range s.TierNames() {
		ids, err := dag.ListLocalIds(entry.Definition(t), s.repo)
		if err != nil {
			return "", err
		}
		for _, id := range ids {
			if id.HasPrefix(prefix) && !seen[id] {
				seen[id] = true
				matches = append(matches, id)
			}
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("entry %s not found", prefix)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("id prefix %s is ambiguous (%d matches)", prefix, len(matches))
	}
}

// RecordOrigin appends a provenance event to an entry, searching all tiers.
func (s *Store) RecordOrigin(id entity.Id, actor, actorKind, sourcePath, trigger string) error {
	for _, t := range s.TierNames() {
		e, err := entry.Read(s.repo, t, id)
		if err != nil {
			if entity.IsErrNotFound(err) {
				continue
			}
			return fmt.Errorf("read %s in tier %s: %w", id, t, err)
		}
		e.Append(entry.NewRecordOrigin(s.author, actor, actorKind, sourcePath, trigger))
		return e.Commit(s.repo)
	}
	return fmt.Errorf("entry %s not found", id)
}

// Log returns the operation history of an entry, searching all tiers.
func (s *Store) Log(id entity.Id) ([]entry.LogEntry, error) {
	for _, t := range s.TierNames() {
		e, err := entry.Read(s.repo, t, id)
		if err != nil {
			if entity.IsErrNotFound(err) {
				continue
			}
			return nil, fmt.Errorf("read %s in tier %s: %w", id, t, err)
		}
		return e.Log(), nil
	}
	return nil, fmt.Errorf("entry %s not found", id)
}

// BodyVersions returns each historical body of an entry, searching all tiers.
func (s *Store) BodyVersions(id entity.Id) ([]entry.BodyVersion, error) {
	for _, t := range s.TierNames() {
		e, err := entry.Read(s.repo, t, id)
		if err != nil {
			if entity.IsErrNotFound(err) {
				continue
			}
			return nil, fmt.Errorf("read %s in tier %s: %w", id, t, err)
		}
		return e.BodyVersions(), nil
	}
	return nil, fmt.Errorf("entry %s not found", id)
}

// mergeCommits returns the hashes of merge commits (>1 parent) in the entry's
// ref history across whichever tier holds it.
func (s *Store) mergeCommits(id entity.Id) ([]string, error) {
	for _, t := range s.TierNames() {
		ref := "refs/" + t.Namespace() + "/" + id.String()
		hashes, err := s.repo.ListCommits(ref)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				continue // not in this tier
			}
			return nil, fmt.Errorf("list commits for %s/%s: %w", t, id, err)
		}
		var merges []string
		for _, h := range hashes {
			c, err := s.repo.ReadCommit(h)
			if err != nil {
				return nil, fmt.Errorf("read commit %s: %w", h, err)
			}
			if len(c.Parents) > 1 {
				merges = append(merges, string(h))
			}
		}
		return merges, nil
	}
	return nil, fmt.Errorf("entry %s not found", id)
}

// UnacknowledgedMerge reports whether the entry has a merge commit not yet
// acknowledged via kref resolve. It reads the already-compiled snapshot's
// AckedMerges, so callers that hold a snapshot pay only for the commit walk.
func (s *Store) UnacknowledgedMerge(snap *entry.Snapshot) (bool, error) {
	hashes, err := s.mergeCommits(snap.ID)
	if err != nil {
		return false, err
	}
	acked := make(map[string]bool, len(snap.AckedMerges))
	for _, h := range snap.AckedMerges {
		acked[h] = true
	}
	for _, h := range hashes {
		if !acked[h] {
			return true, nil
		}
	}
	return false, nil
}

// Merged reports whether an entry has an unacknowledged concurrent-merge — a
// coarse but honest signal that the entry was edited concurrently and synced,
// and not yet cleared with kref resolve. Convenience wrapper that compiles the
// snapshot first.
func (s *Store) Merged(id entity.Id) (bool, error) {
	snap, err := s.Get(id)
	if err != nil {
		return false, err
	}
	return s.UnacknowledgedMerge(snap)
}

// AcknowledgeMerge records the entry's currently-unacknowledged merge-commit
// hashes so the ◆ merged flag clears. Returns the count newly acknowledged (0
// when there was nothing to resolve).
func (s *Store) AcknowledgeMerge(id entity.Id) (int, error) {
	snap, err := s.Get(id)
	if err != nil {
		return 0, err
	}
	hashes, err := s.mergeCommits(id)
	if err != nil {
		return 0, err
	}
	acked := make(map[string]bool, len(snap.AckedMerges))
	for _, h := range snap.AckedMerges {
		acked[h] = true
	}
	var fresh []string
	for _, h := range hashes {
		if !acked[h] {
			fresh = append(fresh, h)
		}
	}
	if len(fresh) == 0 {
		return 0, nil
	}
	for _, t := range s.TierNames() {
		e, err := entry.Read(s.repo, t, id)
		if err != nil {
			if entity.IsErrNotFound(err) {
				continue
			}
			return 0, fmt.Errorf("read %s in tier %s: %w", id, t, err)
		}
		e.Append(entry.NewAckMerge(s.author, fresh))
		if err := e.Commit(s.repo); err != nil {
			return 0, err
		}
		return len(fresh), nil
	}
	return 0, fmt.Errorf("entry %s not found", id)
}

// RepoRelative renders path relative to the repository root. A path outside the
// repo collapses to its basename so an absolute local path never leaks into the
// (syncable) provenance log.
func (s *Store) RepoRelative(path string) string {
	root := repoRoot(s.dir)
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Base(path)
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.Base(path)
	}
	return filepath.ToSlash(rel)
}

// Root returns the absolute path to the repository root (the directory holding
// .git) that backs this store.
func (s *Store) Root() string { return repoRoot(s.dir) }

// repoRoot walks up from start to the directory containing .git; falls back to
// the absolute form of start if none is found.
func repoRoot(start string) string {
	dir, err := filepath.Abs(start)
	if err != nil {
		return start
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return dir
		}
		dir = parent
	}
}

func resolvers(repo repository.ClockedRepo) entity.Resolvers {
	return entity.Resolvers{&identity.Identity{}: identity.NewSimpleResolver(repo)}
}
