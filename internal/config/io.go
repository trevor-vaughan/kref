package config

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// UserPath returns $XDG_CONFIG_HOME/kref/config.yaml (or $HOME/.config/...).
func UserPath(getenv func(string) string) (string, error) {
	base := getenv("XDG_CONFIG_HOME")
	if base == "" {
		home := getenv("HOME")
		if home == "" {
			return "", fmt.Errorf("cannot resolve config path: neither XDG_CONFIG_HOME nor HOME is set")
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "kref", "config.yaml"), nil
}

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

// Parse decodes config bytes into a SPARSE layer with unknown keys rejected:
// only keys actually present in the bytes are set (absent optional fields stay
// nil / zero), so the result is a proper Merge layer. Defaults are filled by
// Merge, not here — this is what lets a present-but-silent file avoid clobbering
// a lower layer's value (e.g. a user file that omits warn_unscanned).
func Parse(b []byte) (*Config, error) {
	c := &Config{}
	if len(bytes.TrimSpace(b)) == 0 {
		return c, nil
	}
	d := yaml.NewDecoder(bytesReader(b))
	d.KnownFields(true)
	if err := d.Decode(c); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return c, nil
}

// MarshalEntry serializes a config for storage in a kref config ENTRY body:
// version + warn_unscanned (only if set) + favorites. trusted_keys is NEVER
// written to an entry — the root of trust is user-file-only and is ignored when
// an entry is loaded (see internal/store/config.go).
func MarshalEntry(c *Config) ([]byte, error) {
	out := &Config{Version: CurrentVersion, WarnUnscanned: c.WarnUnscanned, Favorites: c.Favorites}
	return yaml.Marshal(out)
}

// WriteFile writes c to path atomically, backing up any existing file to
// path+".bck" first. The output is the commented template with c's values.
func WriteFile(path string, c *Config) error {
	out, err := Template(c)
	if err != nil {
		return err
	}
	return WriteBytes(path, out)
}

// WriteBytes writes b to path atomically (temp file + rename), backing up any
// existing file to path+".bck" first. Unlike WriteFile it writes the bytes
// VERBATIM, preserving the caller's formatting and comments — used by
// `kref config edit` to save exactly what the user typed.
func WriteBytes(path string, b []byte) error {
	if existing, err := os.ReadFile(path); err == nil {
		if err := os.WriteFile(path+".bck", existing, 0o600); err != nil {
			return fmt.Errorf("config: backup: %w", err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// InitialUserConfig is the sparse config `kref config init` writes: the safe
// default trusted_keys, but warn_unscanned deliberately UNSET so the fresh file
// does not override a project entry's value (sparse-override; see Template).
func InitialUserConfig() *Config {
	return &Config{Version: CurrentVersion, TrustedKeys: Default().TrustedKeys}
}

// Template renders the canonical commented config with c's values. It is the
// single source of truth for the file's shape and documentation (shipped in
// the binary). Migration regenerates comments from here rather than preserving
// user comments (the .bck retains the original).
//
// warn_unscanned and favorites are rendered as ACTIVE keys only when the layer
// actually set them; otherwise they appear as commented documentation. This
// keeps the user file a true sparse-override file: an unset key falls through
// to the project entry / built-in default rather than clobbering it.
func Template(c *Config) ([]byte, error) {
	var b []byte
	p := func(s string) { b = append(b, s...) }
	p(fmt.Sprintf("# kref configuration. Managed by `kref config`.\nversion: %d\n\n", CurrentVersion))

	p("# warn_unscanned silences the \"stored UNSCANNED\" warning when betterleaks\n")
	p("# is absent. Left unset, the project config (if any) or the default (warn)\n")
	p("# applies; uncomment to override.\n")
	if c.WarnUnscanned != nil {
		p(fmt.Sprintf("warn_unscanned: %t\n\n", *c.WarnUnscanned))
	} else {
		p("# warn_unscanned: true\n\n")
	}

	p("# trusted_keys gates which keys a kref config ENTRY (project config,\n")
	p("# teammate-editable) may set. Entries are NOT fully trusted, and this key is\n")
	p("# honored ONLY from this file. 'scanners' is deliberately omitted: it names\n")
	p("# executables that run on ingest — a synced entry must not choose what runs\n")
	p("# on your machine.\n")
	tkeys := c.TrustedKeys
	if tkeys == nil {
		tkeys = Default().TrustedKeys
	}
	tk, err := yaml.Marshal(map[string][]string{"trusted_keys": tkeys})
	if err != nil {
		return nil, err
	}
	b = append(b, tk...)

	if len(c.Favorites) > 0 {
		p("\n# favorites map a name to an entry id; manage with `kref fav add|rm`.\n")
		fb, err := yaml.Marshal(map[string]map[string]string{"favorites": c.Favorites})
		if err != nil {
			return nil, err
		}
		b = append(b, fb...)
	} else {
		p("\n# favorites map a name to an entry id; manage with `kref fav add|rm`.\n")
		p("# favorites:\n#   todo: <entry-id>\n")
	}
	return b, nil
}
