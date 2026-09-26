package store

import (
	"fmt"
	"strings"
)

// gitConfigSnapshot is git's fully-resolved configuration for one repository,
// read once and answered from memory.
//
// kref used to read config through git-bug's AnyConfig, which resolves the
// global layer with go-git: $XDG_CONFIG_HOME/git/config and $HOME/.gitconfig,
// and nothing else. No include, no includeIf, no system config, no
// GIT_CONFIG_GLOBAL. Every decision kref made from config — whether to sign,
// which identity profile is active, who to attribute work to — was therefore
// blind to layers git itself reads, and silently so.
//
// Asking git per key fixes that and costs a process each time. `git config
// --list` resolves every layer in ONE call for the price of a single --get, and
// store.Open sits on the shell-completion path, so kref reads the lot once and
// hands out lookups.
//
// The snapshot is taken when the store opens and is not refreshed by reads. A
// write that changes what it holds must replace it — see Store.UseIdentity.
type gitConfigSnapshot struct {
	// Kept so getBool can defer an exotic value back to git under the same
	// scope this snapshot was taken in.
	dir  string
	env  []string
	vals map[string]gitConfigEntry
}

// gitConfigEntry is one resolved value.
//
// valueless records `[commit] gpgsign` written with no `=`, which git reads as
// true. go-git reported it as the empty string, indistinguishable from
// `gpgsign =`, which is false — so kref read "sign everything" as "sign
// nothing".
type gitConfigEntry struct {
	text      string
	valueless bool
}

// loadGitConfigSnapshot reads dir's merged git configuration.
//
// --null is what makes the output parseable: records are separated by NUL and
// the key ends at the FIRST newline, so a value containing a newline survives
// and a key with no value is distinguishable from one set to "".
//
// An unreadable config is an error rather than an empty snapshot. Treating it
// as empty would report a user who configured an author and a signing key as
// having configured neither, and attribute their work to the wrong person.
func loadGitConfigSnapshot(dir string, env []string) (*gitConfigSnapshot, error) {
	out, err := runGit(env, "", "-C", dir, "config", "--list", "--null")
	if err != nil {
		return nil, fmt.Errorf("read git config: %w", err)
	}
	c := &gitConfigSnapshot{dir: dir, env: env, vals: make(map[string]gitConfigEntry)}
	for record := range strings.SplitSeq(out, "\x00") {
		if record == "" {
			// The final NUL terminates rather than separates, so the split leaves
			// an empty tail.
			continue
		}
		key, value, hasValue := strings.Cut(record, "\n")
		// Last one wins, which is what `git config --get` returns for a key set
		// more than once.
		c.vals[key] = gitConfigEntry{text: value, valueless: !hasValue}
	}
	return c, nil
}

// get returns the value of key, or "" when it is unset. A key present with no
// value reads as "", matching what `git config --get` prints for one.
func (c *gitConfigSnapshot) get(key string) string {
	return c.vals[key].text
}

// getBool resolves key as git would, reporting separately whether it was set at
// all. The distinction is load-bearing: kref.sign falls through to
// commit.gpgsign when unset and overrides it when set to false.
//
// The word forms, the empty string and a valueless key are resolved here.
// Everything else is an integer to git — in decimal, hex or octal, with an
// optional unit suffix, and with rules that are NOT Go's: git rejects the
// underscores and the 0o prefix that strconv.ParseInt accepts, and accepts a
// leading space that it rejects. Mirroring that parser here would be a second
// implementation of it and a second chance to disagree, so the rare value that
// needs it is put back to git. Real configurations never reach that line.
func (c *gitConfigSnapshot) getBool(key string) (value, set bool, err error) {
	e, ok := c.vals[key]
	if !ok {
		return false, false, nil
	}
	if e.valueless {
		return true, true, nil
	}
	switch strings.ToLower(e.text) {
	case "true", "yes", "on":
		return true, true, nil
	case "false", "no", "off", "":
		return false, true, nil
	}
	out, err := runGit(c.env, "", "-C", c.dir, "config", "--get", "--type=bool", key)
	if err != nil {
		return false, true, err
	}
	return strings.TrimSpace(out) == "true", true, nil
}
