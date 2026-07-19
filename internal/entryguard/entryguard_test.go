package entryguard_test

import (
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/entryguard"
)

// secretBody carries a ghp_ GitHub-PAT-shaped fixture; betterleaks' filter drops
// fabricated AWS keys, so this is the reliable trip-wire the suite uses.
const secretBody = "note: awsToken := \"ghp_012345678901234567890123456789abcdef\"\n"

func syncable() *entry.Snapshot {
	return &entry.Snapshot{ID: "deadbeef", Tier: "personal", TierType: string(entry.TierPersonal)}
}
func private() *entry.Snapshot {
	return &entry.Snapshot{ID: "deadbeef", Tier: "private", TierType: string(entry.TierPrivate)}
}

var _ = Describe("entryguard.Check", func() {
	BeforeEach(func() { GinkgoT().Setenv("XDG_STATE_HOME", GinkgoT().TempDir()) })

	It("allows a clean body on a syncable entry", func() {
		unscanned, err := entryguard.Check(syncable(), "just a plain note", false)
		Expect(err).NotTo(HaveOccurred())
		Expect(unscanned).To(BeFalse())
	})

	It("refuses a secret on a syncable entry, writes NO recovery file, and names the finding (not the secret value)", func() {
		_, err := entryguard.Check(syncable(), secretBody, false)
		var refused *entryguard.RefusedError
		Expect(errors.As(err, &refused)).To(BeTrue())
		// The caller diverts the flagged write into quarantine; the guard must
		// NOT leave its own copy of the secret on disk (a purge could never
		// reach a target-keyed recovery file, so it would outlive the purge).
		matches, gerr := filepath.Glob(filepath.Join(os.Getenv("XDG_STATE_HOME"), "kref", "rejected", "*"))
		Expect(gerr).NotTo(HaveOccurred())
		Expect(matches).To(BeEmpty())
		Expect(err.Error()).To(ContainSubstring("secret"))
		Expect(err.Error()).To(ContainSubstring("entry body"))
		Expect(err.Error()).NotTo(ContainSubstring("saved to"))
		Expect(err.Error()).NotTo(ContainSubstring("ghp_012345678901234567890123456789abcdef"))
	})

	It("allows a secret on a private entry (private cannot push)", func() {
		unscanned, err := entryguard.Check(private(), secretBody, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(unscanned).To(BeFalse())
	})

	It("force overrides the refusal on a syncable entry (false-positive escape hatch)", func() {
		unscanned, err := entryguard.Check(syncable(), secretBody, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(unscanned).To(BeFalse())
	})
})
