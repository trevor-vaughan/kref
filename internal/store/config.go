package store

import (
	"os"
	"sort"
	"strconv"

	"github.com/git-bug/git-bug/entity"

	"github.com/trevor-vaughan/kref/internal/config"
)

// loadConfig builds the effective config: the user file (trusted, local)
// layered over the single kind:config entry (trust-filtered by the user's
// trusted_keys). An invalid USER FILE is a hard error (a broken personal file
// must be fixed). An invalid/ambiguous PROJECT ENTRY degrades to defaults with
// a recorded warning — a broken shared entry must never brick every command.
func (s *Store) loadConfig() error {
	s.cfgWarnings = nil
	user, err := s.loadUserConfig()
	if err != nil {
		return err
	}
	// When no user file exists (user == nil) the root of trust falls back to the
	// compiled-in defaults (favorites + warn_unscanned), but the merge's user
	// layer stays nil: a synthesized default user would clobber the project's
	// own values (e.g. warn_unscanned: false) with default true. Merge documents
	// that a nil layer contributes nothing.
	trustedKeys := config.Default().TrustedKeys
	if user != nil {
		trustedKeys = user.TrustedKeys
	}
	project := s.loadProjectConfig(trustedKeys)
	s.cfg = config.Merge(project, user)
	s.userFavs, s.projectFavs = nil, nil
	if user != nil {
		s.userFavs = user.Favorites
	}
	if project != nil {
		s.projectFavs = project.Favorites
	}
	return nil
}

// FavoriteOrigin reports the layer a favorite came from: "user", "shared"
// (project entry), or "" if unknown. User shadows project on a name clash.
func (s *Store) FavoriteOrigin(name string) string {
	if _, ok := s.userFavs[name]; ok {
		return "user"
	}
	if _, ok := s.projectFavs[name]; ok {
		return "shared"
	}
	return ""
}

// loadUserConfig reads, migrates, parses, and validates the user file. Absent
// file => nil (no user layer; the caller falls back to default trust). A file
// newer than this binary or present-but-invalid => hard error. When migration
// changes the bytes the file is rewritten in place (regenerated comments; the
// original is backed up to .bck).
func (s *Store) loadUserConfig() (*config.Config, error) {
	path, err := config.UserPath(os.Getenv)
	if err != nil {
		return nil, nil //nolint:nilerr // no HOME/XDG => treat as no user config
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	migrated, changed, err := config.Migrate(b)
	if err != nil {
		return nil, err // newer-than-binary etc. — a broken/too-new user file is a hard stop
	}
	c, err := config.Parse(migrated)
	if err != nil {
		return nil, err
	}
	if err := config.Validate(c); err != nil {
		return nil, err
	}
	if changed {
		// Auto-rewrite the user file (local, safe): regenerates comments from the
		// template and backs the original up to .bck. The project ENTRY never
		// auto-writes (that is `kref config migrate`, Task C).
		if err := config.WriteFile(path, c); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// loadProjectConfig finds the one kind:config entry, parses its body, and drops
// any key not in trustedKeys (trust follows the medium: entries are untrusted;
// trusted_keys from an entry is always ignored). Any trouble => nil + warning.
func (s *Store) loadProjectConfig(trustedKeys []string) *config.Config {
	body, _, ok := s.findConfigEntry()
	if !ok {
		return nil
	}
	parsed, err := config.Parse([]byte(body))
	if err != nil {
		s.warnConfig("project config entry is invalid; ignoring: " + err.Error())
		return nil
	}
	return config.Filter(parsed, trustedKeys)
}

// findConfigEntry returns the body and id of the single kind:config entry. When
// more than one exists (e.g. one per tier) it records an ambiguity warning and
// uses the lexically-smallest id (deterministic). Returns ok=false when none.
func (s *Store) findConfigEntry() (body string, id string, ok bool) {
	snaps, err := s.List(ListFilter{Kind: "config"})
	if err != nil {
		s.warnConfig("could not scan for config entry: " + err.Error())
		return "", "", false
	}
	if len(snaps) == 0 {
		return "", "", false
	}
	sort.Slice(snaps, func(i, j int) bool { return snaps[i].ID.String() < snaps[j].ID.String() })
	if len(snaps) > 1 {
		s.warnConfig("multiple kind:config entries found; using " + snaps[0].ID.String())
	}
	return snaps[0].Body, snaps[0].ID.String(), true
}

// ProjectConfigEntry returns the single kind:config entry's body and id (ok
// false when none exists). Exported for the `fav --shared` write path.
func (s *Store) ProjectConfigEntry() (string, entity.Id, bool) {
	body, idStr, ok := s.findConfigEntry()
	if !ok {
		return "", "", false
	}
	return body, entity.Id(idStr), true
}

// MigrateConfig migrates the project config ENTRY to the current schema and,
// when it changed, writes the new body back (minting one entry version). Unlike
// the user file, the shared entry never auto-migrates. Returns a human summary.
func (s *Store) MigrateConfig() (string, error) {
	body, idStr, ok := s.findConfigEntry()
	if !ok {
		return "no project config entry (create one with `kref config init --shared`)", nil
	}
	migrated, changed, err := config.Migrate([]byte(body))
	if err != nil {
		return "", err
	}
	if !changed {
		return "project config entry already at the current schema", nil
	}
	parsed, err := config.Parse(migrated)
	if err != nil {
		return "", err
	}
	if err := config.Validate(parsed); err != nil {
		return "", err
	}
	if err := s.Update(entity.Id(idStr), string(migrated), ""); err != nil {
		return "", err
	}
	return "migrated project config entry " + idStr + " to schema v" + strconv.Itoa(config.CurrentVersion), nil
}

func (s *Store) warnConfig(msg string) { s.cfgWarnings = append(s.cfgWarnings, msg) }

// Favorites returns the effective favorites map (never nil after a load).
func (s *Store) Favorites() map[string]string { return s.EffectiveConfig().Favorites }

// EffectiveConfig returns the merged config, falling back to defaults if a load
// has not run (defensive; every constructor calls loadConfig).
func (s *Store) EffectiveConfig() *config.Config {
	if s.cfg == nil {
		return config.Default()
	}
	return s.cfg
}

// ConfigWarnings returns non-fatal warnings recorded during the last load
// (surfaced by `kref config check`).
func (s *Store) ConfigWarnings() []string { return s.cfgWarnings }
