package main

import (
	"bytes"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/spf13/cobra"

	"github.com/trevor-vaughan/kref/internal/bridge"
	"github.com/trevor-vaughan/kref/internal/config"
)

var _ = Describe("useColor KREF_COLOR override", func() {
	// A command carrying the persistent --json flag, like the real root.
	newCmd := func(json bool) *cobra.Command {
		cmd := &cobra.Command{}
		cmd.Flags().Bool("json", json, "")
		return cmd
	}

	// Under `go test` stdout is never a terminal, so auto-detection yields
	// false — which makes a forced-on result unambiguously the override's doing.

	It("forces color on with KREF_COLOR=1 even under NO_COLOR and a non-tty", func() {
		GinkgoT().Setenv("NO_COLOR", "1")
		GinkgoT().Setenv("KREF_COLOR", "1")
		Expect(useColor(newCmd(false))).To(BeTrue())
	})

	It("forces color off with KREF_COLOR=0", func() {
		GinkgoT().Setenv("NO_COLOR", "")
		GinkgoT().Setenv("KREF_COLOR", "0")
		Expect(useColor(newCmd(false))).To(BeFalse())
	})

	It("never colors JSON output, even with KREF_COLOR=1", func() {
		GinkgoT().Setenv("KREF_COLOR", "1")
		Expect(useColor(newCmd(true))).To(BeFalse())
	})

	It("falls back to auto-detection when KREF_COLOR has an unrecognized value", func() {
		GinkgoT().Setenv("NO_COLOR", "")
		GinkgoT().Setenv("KREF_COLOR", "yes")
		// auto path: not a terminal under go test → false
		Expect(useColor(newCmd(false))).To(BeFalse())
	})

	It("still honors NO_COLOR when KREF_COLOR is unset", func() {
		GinkgoT().Setenv("NO_COLOR", "1")
		GinkgoT().Setenv("KREF_COLOR", "")
		Expect(useColor(newCmd(false))).To(BeFalse())
	})
})

var _ = Describe("resolveColor", func() {
	newCmd := func() *cobra.Command {
		cmd := &cobra.Command{}
		cmd.Flags().Bool("json", false, "")
		return cmd
	}
	pref := func(on bool) *config.Config { return &config.Config{Color: &on} }

	It("honors the saved preference when no env signal forces a choice", func() {
		// Under `go test` stdout is not a terminal, so auto-detection would say
		// false: a true here can only be the saved preference.
		GinkgoT().Setenv("NO_COLOR", "")
		GinkgoT().Setenv("KREF_COLOR", "")
		Expect(resolveColor(newCmd(), pref(true))).To(BeTrue())
		Expect(resolveColor(newCmd(), pref(false))).To(BeFalse())
	})

	It("lets the env force win over the saved preference", func() {
		// KREF_COLOR is per-invocation intent; the saved pref is a default.
		GinkgoT().Setenv("KREF_COLOR", "0")
		Expect(resolveColor(newCmd(), pref(true))).To(BeFalse())
		GinkgoT().Setenv("KREF_COLOR", "1")
		Expect(resolveColor(newCmd(), pref(false))).To(BeTrue())
	})

	It("keeps NO_COLOR ahead of the saved preference", func() {
		GinkgoT().Setenv("KREF_COLOR", "")
		GinkgoT().Setenv("NO_COLOR", "1")
		Expect(resolveColor(newCmd(), pref(true))).To(BeFalse())
	})

	It("falls through to auto-detection when nothing is saved", func() {
		GinkgoT().Setenv("NO_COLOR", "")
		GinkgoT().Setenv("KREF_COLOR", "")
		Expect(resolveColor(newCmd(), &config.Config{})).To(BeFalse()) // not a tty under go test
	})
})

var _ = Describe("ingestSummary quarantine guidance", func() {
	render := func(results []bridge.IngestResult) string {
		var b bytes.Buffer
		ingestSummary(&b, results, false, true)
		return b.String()
	}

	It("gates the UNSCANNED warning on warn_unscanned", func() {
		results := []bridge.IngestResult{{Path: "n.md", ID: "abc123", Tier: "shared", Action: "created", Unscanned: true}}
		var on, off bytes.Buffer
		ingestSummary(&on, results, false, true)
		ingestSummary(&off, results, false, false)
		Expect(on.String()).To(ContainSubstring("stored UNSCANNED"))
		Expect(off.String()).NotTo(ContainSubstring("UNSCANNED"))
	})

	It("explains the cause and the way out when a file is quarantined", func() {
		out := render([]bridge.IngestResult{
			{Path: "notes.md", ID: "abc123", Tier: "shared", Action: "created"},
			{Path: "leak.md", ID: "def456", Tier: "private", Action: "quarantined", Quarantined: true},
		})

		Expect(out).To(ContainSubstring("quarantined to the private tier"))
		Expect(out).To(ContainSubstring("false positive"))
		Expect(out).To(ContainSubstring("kref retier"))
		Expect(out).To(ContainSubstring("kref purge"))
	})

	It("stays silent about quarantine recovery when nothing was quarantined", func() {
		out := render([]bridge.IngestResult{
			{Path: "notes.md", ID: "abc123", Tier: "shared", Action: "created"},
		})

		Expect(out).NotTo(ContainSubstring("quarantined to the private tier"))
		Expect(out).NotTo(ContainSubstring("kref retier"))
	})
})
