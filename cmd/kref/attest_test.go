package main

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("attest command", func() {
	// bornSigned turns signing on BEFORE the entry exists, so every commit in its
	// history is already signed and there is nothing for an attestation to add.
	bornSigned := func(dir string) string {
		GinkgoHelper()
		Expect(run("--dir", dir, "init", "--name", "T", "--email", "t@x")).To(ContainSubstring("initialized"))
		enableTestSigning(dir)
		out := run("--dir", dir, "new", "--title", "Signed", "--body", "b", "--json")
		var a struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &a)).To(Succeed())
		return a.ID
	}

	// Published history is exactly what attest exists for: resign cannot rewrite
	// it, so the two commands have to disagree about the same entry — one refusing
	// and pointing here, this one fast-forwarding an attestation onto it.
	It("attests published history that resign refuses to rewrite, and reports the claim", func() {
		dir := gitRepo()
		id := unsignedThenSigning(dir)
		markPushed(dir, id)

		_, err := runErr("--dir", dir, "resign", id)
		Expect(err).To(MatchError(ContainSubstring("kref attest")))

		// The WHOLE tally line, not just the word: "authored" appears in
		// "(0 authored, 1 received)" too, so a substring match passes with the
		// counters inverted and pins nothing about the claim it names.
		out := run("--dir", dir, "attest", id)
		Expect(out).To(ContainSubstring("attested 1 entry (1 authored, 0 received)"))
		Expect(run("--dir", dir, "show", id, "--json")).To(ContainSubstring(`"sig_state": "good"`))
	})

	// You named one entry and it was not vouched for. Reporting that only in the
	// output leaves `kref attest <id> && ...` running on, and an agent or hook
	// reading the status believes the history is now vouched for when it is not.
	It("exits non-zero when the entry it was given is refused", func() {
		dir := gitRepo()
		id := bornSigned(dir)

		out, err := runErr("--dir", dir, "attest", id)
		Expect(err).To(HaveOccurred())
		Expect(out + err.Error()).To(ContainSubstring("nothing to attest"))
	})

	// A sweep is the opposite case: skipping entries that need nothing is what
	// --all is FOR. Failing it would make `attest --all` non-zero in every repo
	// whose history is already signed and train people to ignore the status.
	It("exits zero for a sweep that skips entries", func() {
		dir := gitRepo()
		bornSigned(dir)

		out, err := runErr("--dir", dir, "attest", "--all")
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("skipped"))
		Expect(out).To(ContainSubstring("nothing to attest"))
	})

	It("requires an id or --all", func() {
		dir := gitRepo()
		bornSigned(dir)

		_, err := runErr("--dir", dir, "attest")
		Expect(err).To(MatchError(ContainSubstring("--all")))
	})

	It("rejects an id together with --all", func() {
		dir := gitRepo()
		id := bornSigned(dir)

		_, err := runErr("--dir", dir, "attest", id, "--all")
		Expect(err).To(MatchError(ContainSubstring("not both")))
	})

	// The JSON is a contract: a caller reads the claim and the new tip to know
	// what was vouched for and where the entry now points.
	It("reports every result field in --json", func() {
		dir := gitRepo()
		id := unsignedThenSigning(dir)

		var res struct {
			Results []struct {
				ID       string `json:"id"`
				Tier     string `json:"tier"`
				Attested bool   `json:"attested"`
				Claim    string `json:"claim"`
				Reason   string `json:"reason"`
				NewTip   string `json:"new_tip"`
			} `json:"results"`
		}
		Expect(json.Unmarshal([]byte(run("--dir", dir, "attest", id, "--json")), &res)).To(Succeed())
		Expect(res.Results).To(HaveLen(1))
		r := res.Results[0]
		Expect(r.ID).To(Equal(id))
		Expect(r.Tier).To(Equal("personal"))
		Expect(r.Attested).To(BeTrue())
		Expect(r.Claim).To(Equal("authored"))
		Expect(r.Reason).To(BeEmpty())
		Expect(r.NewTip).To(MatchRegexp(`^[0-9a-f]{40}$`))
	})
})
