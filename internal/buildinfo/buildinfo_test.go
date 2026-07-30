package buildinfo_test

import (
	"runtime/debug"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/buildinfo"
)

const (
	rev  = "bf490bd1234567890abcdef1234567890abcdef12" // DevSkim: ignore DS173237
	when = "2026-07-29T23:30:56Z"
)

// stamped builds the *debug.BuildInfo the Go toolchain produces for a build from
// a git checkout. A `go test` binary carries NO vcs.* settings and reports
// Main.Version as "(devel)", which is exactly why Resolve takes the build info
// as a parameter: without that seam every branch below is unreachable from a
// test. Main.Version here is the pseudo-version a plain `go build` synthesizes,
// so these specs also pin that vcs.revision wins over it.
func stamped(rev, when, modified string) *debug.BuildInfo {
	return &debug.BuildInfo{
		Main: debug.Module{Version: "v0.0.0-20260729233552-e6d370f3c86b"},
		Settings: []debug.BuildSetting{
			{Key: "vcs", Value: "git"},
			{Key: "vcs.revision", Value: rev},
			{Key: "vcs.time", Value: when},
			{Key: "vcs.modified", Value: modified},
		},
	}
}

var _ = Describe("buildinfo.Resolve", func() {
	Describe("version precedence", func() {
		It("prefers an injected ldflag tag over every embedded stamp", func() {
			got := buildinfo.Resolve("v0.1.0", "", stamped(rev, when, "false"), true)
			Expect(got.Version).To(Equal("v0.1.0"))
		})

		It("treats the default \"dev\" ldflag as absent and falls through", func() {
			got := buildinfo.Resolve("dev", "", stamped(rev, when, "false"), true)
			Expect(got.Version).To(Equal("bf490bd"))
		})

		It("shortens vcs.revision to 7 chars, matching git log --oneline", func() {
			got := buildinfo.Resolve("", "", stamped(rev, when, "false"), true)
			Expect(got.Version).To(Equal("bf490bd"))
		})

		It("marks a modified working tree dirty", func() {
			got := buildinfo.Resolve("", "", stamped(rev, when, "true"), true)
			Expect(got.Version).To(Equal("bf490bd-dirty"))
		})

		It("never appends -dirty to an injected tag", func() {
			got := buildinfo.Resolve("v0.1.0", "", stamped(rev, when, "true"), true)
			Expect(got.Version).To(Equal("v0.1.0"))
		})

		It("uses Main.Version when there are no VCS stamps (the go install pkg@tag case)", func() {
			bi := &debug.BuildInfo{Main: debug.Module{Version: "v0.1.0"}}
			got := buildinfo.Resolve("", "", bi, true)
			Expect(got.Version).To(Equal("v0.1.0"))
			Expect(got.CommitDate).To(BeEmpty())
		})

		It("rejects the (devel) placeholder Main.Version a test binary reports", func() {
			bi := &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}
			got := buildinfo.Resolve("", "", bi, true)
			Expect(got.Version).To(Equal("dev"))
		})

		It("degrades to dev when no build info is available at all", func() {
			got := buildinfo.Resolve("", "", nil, false)
			Expect(got.Version).To(Equal("dev"))
			Expect(got.CommitDate).To(BeEmpty())
		})

		It("ignores a short or empty vcs.revision rather than reporting a truncated hash", func() {
			got := buildinfo.Resolve("", "", stamped("", when, "false"), true)
			Expect(got.Version).To(Equal("v0.0.0-20260729233552-e6d370f3c86b"))
		})
	})

	Describe("commit date precedence", func() {
		It("prefers an injected date over vcs.time", func() {
			got := buildinfo.Resolve("v0.1.0", "2026-07-28T09:14:03Z", stamped(rev, when, "false"), true)
			Expect(got.CommitDate).To(Equal("2026-07-28T09:14:03Z"))
		})

		It("falls back to vcs.time", func() {
			got := buildinfo.Resolve("v0.1.0", "", stamped(rev, when, "false"), true)
			Expect(got.CommitDate).To(Equal(when))
		})

		It("normalizes a valid non-UTC date to UTC", func() {
			got := buildinfo.Resolve("v0.1.0", "2026-07-29T19:30:56-04:00", nil, false)
			Expect(got.CommitDate).To(Equal("2026-07-29T23:30:56Z"))
		})

		It("discards an unparseable injected date rather than leaking it into --json", func() {
			got := buildinfo.Resolve("v0.1.0", "last tuesday", nil, false)
			Expect(got.CommitDate).To(BeEmpty())
		})

		It("discards an unparseable vcs.time but keeps the revision", func() {
			got := buildinfo.Resolve("", "", stamped(rev, "not-a-time", "false"), true)
			Expect(got.Version).To(Equal("bf490bd"))
			Expect(got.CommitDate).To(BeEmpty())
		})
	})

	Describe("String", func() {
		It("appends the commit parenthetical when a date is known", func() {
			got := buildinfo.Resolve("v0.1.0", when, nil, false)
			Expect(got.String()).To(Equal("v0.1.0 (commit 2026-07-29T23:30:56Z)"))
		})

		It("renders a bare version when no date is known", func() {
			got := buildinfo.Resolve("v0.1.0", "", nil, false)
			Expect(got.String()).To(Equal("v0.1.0"))
		})

		It("renders exactly dev for a test binary, so `kref version` stays `kref dev`", func() {
			bi := &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}
			Expect(buildinfo.Resolve("dev", "", bi, true).String()).To(Equal("dev"))
		})
	})
})
