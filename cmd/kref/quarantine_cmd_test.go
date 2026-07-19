package main

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/git-bug/git-bug/entity"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/trevor-vaughan/kref/internal/store"
)

var _ = Describe("quarantineLine age + stale marker", func() {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	It("shows relative age and no marker for a fresh item", func() {
		it := store.QuarantineItem{ID: entity.Id("abc123"), HeldOp: false, Kind: "doc", DestTier: "shared", Title: "T", CreatedAt: now.Add(-2 * time.Hour)}
		line := quarantineLine(it, now)
		Expect(line).To(ContainSubstring("2h ago"))
		Expect(line).NotTo(ContainSubstring("STALE"))
	})
	It("flags a stale item", func() {
		it := store.QuarantineItem{ID: entity.Id("def456"), HeldOp: false, Kind: "doc", DestTier: "shared", Title: "T", CreatedAt: now.Add(-9 * 24 * time.Hour)}
		line := quarantineLine(it, now)
		Expect(line).To(ContainSubstring("STALE"))
	})
})

// qSecret is a full-length GitHub PAT fixture betterleaks flags (short synthetic
// forms are filtered); used to trip the entry-body scanner so a write parks.
const qSecret = "ghp_012345678901234567890123456789abcdef"

// parseQID extracts the quarantine item id from a "quarantined as <id> ..." line.
func parseQID(out string) string {
	GinkgoHelper()
	const marker = "quarantined as "
	i := strings.Index(out, marker)
	Expect(i).To(BeNumerically(">=", 0), "expected a 'quarantined as' line, got: "+out)
	rest := out[i+len(marker):]
	j := strings.IndexAny(rest, " \n")
	Expect(j).To(BeNumerically(">", 0))
	return rest[:j]
}

// parseCommentID extracts the comment id from a "commented <id>" line.
func parseCommentID(out string) string {
	GinkgoHelper()
	const marker = "commented "
	i := strings.Index(out, marker)
	Expect(i).To(BeNumerically(">=", 0), "expected a 'commented' line, got: "+out)
	return strings.TrimSpace(strings.SplitN(out[i+len(marker):], "\n", 2)[0])
}

var _ = Describe("kref quarantine approve|reject", func() {
	// setup inits a repo, creates a personal entry, parks a flagged update, and
	// returns the repo dir, the entry id, and the quarantine item id.
	setup := func() (dir, id, qid string) {
		GinkgoT().Setenv("XDG_STATE_HOME", GinkgoT().TempDir())
		dir = gitRepo()
		run("--dir", dir, "init", "--name", "Tester", "--email", "t@e.com")
		out := run("--dir", dir, "new", "--tier", "personal", "--title", "T", "--body", "orig", "--json")
		var added struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &added)).To(Succeed())
		out = run("--dir", dir, "update", added.ID, "--body", qSecret)
		Expect(out).To(ContainSubstring("quarantined as"))
		return dir, added.ID, parseQID(out)
	}

	It("approves a parked update and applies the body to the live entry", func() {
		dir, id, qid := setup()
		out := run("--dir", dir, "quarantine", "approve", qid, "-m", "fixture ok")
		Expect(out).To(ContainSubstring("approved"))
		Expect(run("--dir", dir, "show", id, "--plain")).To(ContainSubstring(qSecret))
	})

	It("rejects a parked update, preserves content, and leaves the entry untouched", func() {
		dir, id, qid := setup()
		out := run("--dir", dir, "quarantine", "reject", qid, "-m", "real secret")
		Expect(out).To(ContainSubstring("rejected"))
		Expect(out).To(ContainSubstring("rejected/")) // recovery path
		Expect(run("--dir", dir, "show", id, "--plain")).To(ContainSubstring("orig"))
	})

	It("new --force promotes a flagged draft to its tier in one step", func() {
		GinkgoT().Setenv("XDG_STATE_HOME", GinkgoT().TempDir())
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "Tester", "--email", "t@e.com")
		out := run("--dir", dir, "new", "--tier", "personal", "--title", "Fx", "--body", qSecret, "--force")
		Expect(out).To(ContainSubstring("force-approved"))
		Expect(run("--dir", dir, "list", "--tier", "personal")).To(ContainSubstring("Fx"))
	})

	It("update --force replays a flagged body onto the live entry in one step", func() {
		GinkgoT().Setenv("XDG_STATE_HOME", GinkgoT().TempDir())
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "Tester", "--email", "t@e.com")
		out := run("--dir", dir, "new", "--tier", "personal", "--title", "T", "--body", "orig", "--json")
		var added struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &added)).To(Succeed())
		out = run("--dir", dir, "update", added.ID, "--body", qSecret, "--force")
		Expect(out).To(ContainSubstring("force-approved"))
		Expect(run("--dir", dir, "show", added.ID, "--plain")).To(ContainSubstring(qSecret))
	})
})

var _ = Describe("kref quarantine show", func() {
	It("renders a held update's review (findings + proposed diff) statically", func() {
		GinkgoT().Setenv("XDG_STATE_HOME", GinkgoT().TempDir())
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "Tester", "--email", "t@e.com")
		out := run("--dir", dir, "new", "--tier", "personal", "--title", "Doc", "--body", "orig", "--json")
		var added struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &added)).To(Succeed())
		qid := parseQID(run("--dir", dir, "update", added.ID, "--body", qSecret))

		show := run("--dir", dir, "quarantine", "show", qid)
		Expect(show).To(ContainSubstring("held set-body"))
		Expect(show).To(ContainSubstring("github-pat"))
		Expect(show).To(ContainSubstring("proposed change"))
		Expect(show).To(ContainSubstring("- orig")) // the current body, diffed out
	})
})

var _ = Describe("kref quarantine list --rejected + recover", func() {
	rejectOne := func(dir string) (id, qid string) {
		run("--dir", dir, "init", "--name", "Tester", "--email", "t@e.com")
		out := run("--dir", dir, "new", "--tier", "personal", "--title", "Doc", "--body", "orig", "--json")
		var added struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &added)).To(Succeed())
		qid = parseQID(run("--dir", dir, "update", added.ID, "--body", qSecret))
		run("--dir", dir, "quarantine", "reject", qid, "-m", "real secret")
		return added.ID, qid
	}

	It("lists a rejected item under --rejected (not the pending queue) and recovers it", func() {
		GinkgoT().Setenv("XDG_STATE_HOME", GinkgoT().TempDir())
		dir := gitRepo()
		_, qid := rejectOne(dir)

		Expect(run("--dir", dir, "quarantine", "list")).To(ContainSubstring("no writes awaiting review"))
		rl := run("--dir", dir, "quarantine", "list", "--rejected")
		Expect(rl).To(ContainSubstring("rejected write"))
		Expect(rl).To(ContainSubstring(qid[:8]))

		out := run("--dir", dir, "quarantine", "recover", qid)
		Expect(out).To(ContainSubstring("recovered"))
		// back in the pending queue, gone from rejected
		Expect(run("--dir", dir, "quarantine", "list")).To(ContainSubstring(qid[:8]))
		Expect(run("--dir", dir, "quarantine", "list", "--rejected")).To(ContainSubstring("no rejected writes"))
	})
})

var _ = Describe("kref quarantine purge", func() {
	rejectOne := func(dir string) string { // returns qid
		run("--dir", dir, "init", "--name", "Tester", "--email", "t@e.com")
		out := run("--dir", dir, "new", "--tier", "personal", "--title", "Doc", "--body", "orig", "--json")
		var added struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &added)).To(Succeed())
		qid := parseQID(run("--dir", dir, "update", added.ID, "--body", qSecret))
		run("--dir", dir, "quarantine", "reject", qid, "-m", "real secret")
		return qid
	}

	It("purges one rejected item with -y", func() {
		GinkgoT().Setenv("XDG_STATE_HOME", GinkgoT().TempDir())
		dir := gitRepo()
		qid := rejectOne(dir)
		out := run("--dir", dir, "quarantine", "purge", qid, "-y")
		Expect(out).To(ContainSubstring("purged"))
		Expect(run("--dir", dir, "quarantine", "list", "--rejected")).To(ContainSubstring("no rejected writes"))
	})

	It("bulk-purges all rejected items with -y", func() {
		GinkgoT().Setenv("XDG_STATE_HOME", GinkgoT().TempDir())
		dir := gitRepo()
		rejectOne(dir)
		out := run("--dir", dir, "quarantine", "purge", "-y")
		Expect(out).To(ContainSubstring("purged"))
		Expect(run("--dir", dir, "quarantine", "list", "--rejected")).To(ContainSubstring("no rejected writes"))
	})

	It("aborts without 'yes' and keeps the item", func() {
		GinkgoT().Setenv("XDG_STATE_HOME", GinkgoT().TempDir())
		dir := gitRepo()
		qid := rejectOne(dir)
		out := runIn("no\n", "--dir", dir, "quarantine", "purge", qid)
		Expect(out).To(ContainSubstring("aborted"))
		Expect(run("--dir", dir, "quarantine", "list", "--rejected")).To(ContainSubstring(qid[:8]))
	})

	It("refuses to purge a pending item", func() {
		GinkgoT().Setenv("XDG_STATE_HOME", GinkgoT().TempDir())
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "Tester", "--email", "t@e.com")
		out := run("--dir", dir, "new", "--tier", "personal", "--title", "P", "--body", "orig", "--json")
		var added struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &added)).To(Succeed())
		qid := parseQID(run("--dir", dir, "update", added.ID, "--body", qSecret))
		_, err := runErr("--dir", dir, "quarantine", "purge", qid, "-y")
		Expect(err).To(HaveOccurred()) // pending, not rejected
	})
})

var _ = Describe("kref quarantine list", func() {
	It("lists pending items and empties as they are decided", func() {
		GinkgoT().Setenv("XDG_STATE_HOME", GinkgoT().TempDir())
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "Tester", "--email", "t@e.com")
		out := run("--dir", dir, "new", "--tier", "personal", "--title", "Doc", "--body", "orig", "--json")
		var added struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &added)).To(Succeed())
		qid := parseQID(run("--dir", dir, "update", added.ID, "--body", qSecret))

		list := run("--dir", dir, "quarantine", "list")
		Expect(list).To(ContainSubstring("awaiting review"))
		Expect(list).To(ContainSubstring("set-body"))
		Expect(list).To(ContainSubstring(qid[:8]))

		Expect(run("--dir", dir, "quarantine", "list", "--json")).To(ContainSubstring(`"op_kind": "set-body"`))

		run("--dir", dir, "quarantine", "approve", qid, "-m", "ok")
		Expect(run("--dir", dir, "quarantine", "list")).To(ContainSubstring("no writes awaiting review"))
	})
})

var _ = Describe("bare kref quarantine", func() {
	// seedQuarantine inits a repo, parks a flagged update, and returns the repo
	// dir plus the quarantine item id.
	seedQuarantine := func() (dir, qid string) {
		GinkgoT().Setenv("XDG_STATE_HOME", GinkgoT().TempDir())
		dir = gitRepo()
		run("--dir", dir, "init", "--name", "Tester", "--email", "t@e.com")
		out := run("--dir", dir, "new", "--tier", "personal", "--title", "T", "--body", "orig", "--json")
		var added struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &added)).To(Succeed())
		out = run("--dir", dir, "update", added.ID, "--body", qSecret)
		Expect(out).To(ContainSubstring("quarantined as"))
		return dir, parseQID(out)
	}

	It("bare quarantine with --json emits the static queue", func() {
		dir, _ := seedQuarantine()
		out := run("--dir", dir, "--json", "quarantine")
		Expect(out).To(ContainSubstring("\"id\""))
	})

	It("bare quarantine on an empty queue with --plain prints the clear line", func() {
		GinkgoT().Setenv("XDG_STATE_HOME", GinkgoT().TempDir())
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "Tester", "--email", "t@e.com")
		out := run("--dir", dir, "--plain", "quarantine")
		Expect(out).To(ContainSubstring("review queue is clear"))
	})
})

var _ = Describe("kref list quarantine banner", func() {
	It("prepends a review-queue banner while writes await review, and drops it once decided", func() {
		GinkgoT().Setenv("XDG_STATE_HOME", GinkgoT().TempDir())
		dir := gitRepo()
		run("--dir", dir, "init", "--name", "Tester", "--email", "t@e.com")
		out := run("--dir", dir, "new", "--tier", "personal", "--title", "Doc", "--body", "orig", "--json")
		var added struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &added)).To(Succeed())
		qid := parseQID(run("--dir", dir, "update", added.ID, "--body", qSecret))

		banner := run("--dir", dir, "list", "--no-pager")
		Expect(banner).To(ContainSubstring("awaiting secret review"))
		Expect(banner).To(ContainSubstring("set-body"))
		Expect(banner).To(ContainSubstring("Doc")) // the normal list still renders below

		// --json and --plain stay clean (machine formats)
		Expect(run("--dir", dir, "list", "--json")).NotTo(ContainSubstring("awaiting secret review"))
		Expect(run("--dir", dir, "list", "--plain")).NotTo(ContainSubstring("awaiting secret review"))

		run("--dir", dir, "quarantine", "approve", qid, "-m", "ok")
		Expect(run("--dir", dir, "list", "--no-pager")).NotTo(ContainSubstring("awaiting secret review"))
	})
})

var _ = Describe("kref comment quarantine paths", func() {
	newEntry := func() (dir, id string) {
		GinkgoT().Setenv("XDG_STATE_HOME", GinkgoT().TempDir())
		dir = gitRepo()
		run("--dir", dir, "init", "--name", "Tester", "--email", "t@e.com")
		out := run("--dir", dir, "new", "--tier", "personal", "--title", "T", "--body", "body", "--json")
		var added struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(out), &added)).To(Succeed())
		return dir, added.ID
	}

	It("parks a flagged comment edit instead of refusing", func() {
		dir, id := newEntry()
		cid := parseCommentID(run("--dir", dir, "comment", id, "-m", "original"))
		out := run("--dir", dir, "comment", id, "--edit", cid, "-m", qSecret)
		Expect(out).To(ContainSubstring("quarantined as"))
	})

	It("parks a flagged resolve note instead of refusing", func() {
		dir, id := newEntry()
		qcid := parseCommentID(run("--dir", dir, "comment", id, "-m", "is this ok?", "-q"))
		out := run("--dir", dir, "comment", id, "--resolve", qcid, "-m", qSecret)
		Expect(out).To(ContainSubstring("quarantined as"))
	})

	It("comment --force applies a flagged comment in one step", func() {
		dir, id := newEntry()
		out := run("--dir", dir, "comment", id, "-m", qSecret, "--force")
		Expect(out).To(ContainSubstring("force-approved"))
	})
})
