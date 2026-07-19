package config

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// reservedFavoriteNames may not be user-assigned. kref.conf is the project
// config entry's own alias.
var reservedFavoriteNames = map[string]bool{"kref.conf": true}

// hexOnly matches a string made entirely of lowercase hex digits — exactly the
// shape of an id prefix. A favorite name must NOT match this, so favorite and
// id namespaces are disjoint and resolution needs no precedence rule.
var hexOnly = regexp.MustCompile(`^[0-9a-f]+$`)

// ValidFavoriteName enforces the disjoint-namespace + hygiene rules.
func ValidFavoriteName(name string) error {
	switch {
	case name == "":
		return errors.New("favorite name is empty")
	case reservedFavoriteNames[name]:
		return fmt.Errorf("favorite name %q is reserved", name)
	case strings.HasPrefix(name, "-"):
		return fmt.Errorf("favorite name %q may not start with '-'", name)
	case strings.ContainsAny(name, " \t\n\r"):
		return fmt.Errorf("favorite name %q may not contain whitespace", name)
	case hexOnly.MatchString(name):
		return fmt.Errorf("favorite name %q is a valid hex id-prefix and would shadow an id; use a name containing a non-hex character", name)
	}
	return nil
}

// Validate is the linter: schema + semantic checks shared by every write path
// and exposed read-only by `kref config check`.
func Validate(c *Config) error {
	// Version is not required here: a sparse layer (e.g. a hand-written user
	// file) may omit it, and migration owns version currency. Reject only a
	// nonsensical negative value.
	if c.Version < 0 {
		return fmt.Errorf("config: invalid version %d", c.Version)
	}
	for name := range c.Favorites {
		if err := ValidFavoriteName(name); err != nil {
			return fmt.Errorf("config: favorites: %w", err)
		}
	}
	known := map[string]bool{"favorites": true, "warn_unscanned": true}
	for _, k := range c.TrustedKeys {
		if !known[k] {
			return fmt.Errorf("config: trusted_keys names unknown key %q", k)
		}
	}
	return nil
}
