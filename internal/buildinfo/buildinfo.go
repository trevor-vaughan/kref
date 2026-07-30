// Package buildinfo resolves the build identity kref reports from `kref version`,
// `kref --version`, and the MCP handshake. It answers two questions — which
// version is this, and what commit is it built from — using whichever of three
// sources is available: ldflags injected by goreleaser or `task build`, the VCS
// stamps the Go toolchain embeds automatically in any build from a checkout, or
// the module version recorded by `go install pkg@tag`.
//
// Resolve takes *debug.BuildInfo as a parameter rather than calling
// debug.ReadBuildInfo itself: a `go test` binary carries no vcs.* settings, so
// every VCS branch here would otherwise be unreachable from a test.
package buildinfo

import (
	"runtime/debug"
	"strings"
	"time"
)

// devVersion is the compiled-in default for the -X main.Version ldflag, and the
// last-resort answer. On input it means "nothing was injected".
const devVersion = "dev"

// develVersion is the placeholder the toolchain records as Main.Version for a
// binary built outside a module release — notably every `go test` binary.
const develVersion = "(devel)"

// shortRevLen matches git's conventional short hash (git log --oneline,
// git describe --always), so a reported revision pastes straight into `git show`.
const shortRevLen = 7

// Info is the resolved build identity.
type Info struct {
	// Version is a release tag ("v0.1.0"), a short commit ("bf490bd", with a
	// "-dirty" suffix when built from a modified tree), or "dev".
	Version string
	// CommitDate is the UTC RFC3339 date of the commit the binary was built
	// from, or "" when no source could supply one.
	CommitDate string
}

// Resolve picks the best available version and commit date. version and date are
// the ldflag-injected values (-X main.Version, -X main.Date); bi and ok are the
// two results of debug.ReadBuildInfo.
//
// Version precedence: the injected tag, then the embedded VCS revision, then the
// module version from `go install pkg@tag`, then "dev".
//
// Date precedence: the injected date, then the embedded VCS commit time. Either
// is discarded unless it parses as RFC3339, so the --json contract is guaranteed
// well-formed rather than echoing whatever a build passed in.
func Resolve(version, date string, bi *debug.BuildInfo, ok bool) Info {
	out := Info{Version: strings.TrimSpace(version), CommitDate: utcRFC3339(date)}
	if out.Version == devVersion {
		out.Version = "" // the compiled-in default means "nothing was injected"
	}
	// ok is false when the binary has no embedded build table, in which case bi
	// is nil and neither embedded source exists.
	if ok && bi != nil {
		if out.Version == "" {
			out.Version = shortRev(setting(bi, "vcs.revision"), setting(bi, "vcs.modified"))
		}
		if out.Version == "" && bi.Main.Version != develVersion {
			out.Version = bi.Main.Version
		}
		if out.CommitDate == "" {
			out.CommitDate = utcRFC3339(setting(bi, "vcs.time"))
		}
	}
	if out.Version == "" {
		out.Version = devVersion
	}
	return out
}

// String renders the human one-liner minus the "kref " prefix, which cobra's
// version template supplies. The commit parenthetical appears only when a date
// is known, so a test binary prints exactly "dev".
func (i Info) String() string {
	if i.CommitDate == "" {
		return i.Version
	}
	return i.Version + " (commit " + i.CommitDate + ")"
}

// shortRev renders a VCS revision as a short hash, suffixed "-dirty" when the
// working tree was modified. It returns "" when the revision is missing or
// shorter than a short hash, both so callers fall through to the next source and
// so the truncation below can never slice out of range.
func shortRev(rev, modified string) string {
	if len(rev) < shortRevLen {
		return ""
	}
	short := rev[:shortRevLen]
	if modified == "true" {
		short += "-dirty"
	}
	return short
}

// setting returns the value of a named debug.BuildSetting, or "".
func setting(bi *debug.BuildInfo, key string) string {
	if bi == nil {
		return ""
	}
	for _, s := range bi.Settings {
		if s.Key == key {
			return s.Value
		}
	}
	return ""
}

// utcRFC3339 normalizes a timestamp to UTC RFC3339, returning "" when it does
// not parse. Both sources (goreleaser's .CommitDate and the toolchain's
// vcs.time) document RFC3339, so a parse failure means some other build passed
// something else — and a date we cannot read is not trustworthy enough to print.
func utcRFC3339(s string) string {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(s))
	if err != nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
