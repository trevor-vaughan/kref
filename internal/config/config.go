// Package config owns kref's user + project configuration: schema, defaults,
// layered merge, validation, and versioned migration. It operates on YAML
// bytes and plain structs only (no git-bug imports); the store supplies the
// project-entry bytes and applies the trust filter.
package config

// CurrentVersion is the config schema version this binary writes. Bump it in
// migrate.go alongside a new migration step.
const CurrentVersion = 1

// Config is a configuration layer OR the merged effective config. Layers are
// sparse: a field left unset by a layer's source bytes is the zero value (nil
// for the optional scalar/collection fields), so Merge can apply true per-key
// overriding — a layer only contributes the keys it actually set. Default()
// and Merge() produce fully-resolved values.
type Config struct {
	Version int `yaml:"version" json:"version"`
	// WarnUnscanned is a pointer so a layer that omits it (nil) does not clobber
	// a lower layer's value during Merge. Resolve it with WarnUnscannedOn.
	WarnUnscanned *bool             `yaml:"warn_unscanned,omitempty" json:"warn_unscanned,omitempty"`
	Favorites     map[string]string `yaml:"favorites,omitempty" json:"favorites,omitempty"`
	// TrustedKeys is honored ONLY from the user file. It is the root of trust:
	// it gates which keys a config entry may contribute. Never read it from an
	// entry (see internal/store/config.go).
	TrustedKeys []string `yaml:"trusted_keys,omitempty" json:"trusted_keys,omitempty"`
}

func boolPtr(b bool) *bool { return &b }

// WarnUnscannedOn resolves the optional flag: an unset (nil) value takes the
// default, which is to warn (true).
func (c *Config) WarnUnscannedOn() bool { return c.WarnUnscanned == nil || *c.WarnUnscanned }

// Default returns the compiled-in defaults — the base every Merge starts from,
// so an absent/sparse file never breaks a load.
func Default() *Config {
	return &Config{
		Version:       CurrentVersion,
		WarnUnscanned: boolPtr(true),
		Favorites:     map[string]string{},
		TrustedKeys:   []string{"favorites", "warn_unscanned"},
	}
}

// Merge layers sparse configs over the compiled-in defaults: project first,
// then user (user wins). Each layer contributes ONLY the keys it set — a nil
// scalar or absent favorite does not override a lower layer. Favorites union by
// name with the later (user) layer winning. TrustedKeys is taken from the user
// layer only (root of trust). project or user may be nil.
func Merge(project, user *Config) *Config {
	out := Default()
	apply := func(c *Config, trusted bool) {
		if c == nil {
			return
		}
		if c.WarnUnscanned != nil {
			out.WarnUnscanned = boolPtr(*c.WarnUnscanned)
		}
		for k, v := range c.Favorites {
			out.Favorites[k] = v
		}
		if trusted && len(c.TrustedKeys) > 0 {
			out.TrustedKeys = append([]string(nil), c.TrustedKeys...)
		}
	}
	apply(project, false)
	apply(user, true)
	out.Version = CurrentVersion
	return out
}

// Filter keeps only the keys named in trustedKeys from an ENTRY-sourced
// (untrusted-medium) config, returning a SPARSE layer suitable for Merge: an
// untrusted key is left unset (nil) so it never overrides the user/default
// value. trusted_keys is ALWAYS dropped — an entry may never set the root of
// trust.
func Filter(c *Config, trustedKeys []string) *Config {
	trusted := map[string]bool{}
	for _, k := range trustedKeys {
		trusted[k] = true
	}
	out := &Config{Version: c.Version}
	if trusted["warn_unscanned"] && c.WarnUnscanned != nil {
		out.WarnUnscanned = boolPtr(*c.WarnUnscanned)
	}
	if trusted["favorites"] && len(c.Favorites) > 0 {
		out.Favorites = map[string]string{}
		for k, v := range c.Favorites {
			out.Favorites[k] = v
		}
	}
	return out
}
