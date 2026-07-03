//go:build e2e

package e2e_test

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("config + favorites end-to-end", func() {
	It("round-trips a personal favorite: add, list, resolve, remove", func() {
		e := newKrefEnv("Fav User", "fav@example.com")
		e.mustRun("init", "--json")
		id := idOf(e.mustRun("new", "--title", "Demo entry", "--json"))

		e.mustRun("fav", "add", id, "demo")

		ls := e.mustRun("fav", "ls")
		Expect(ls).To(ContainSubstring("demo"))
		Expect(ls).To(ContainSubstring("(user)"))

		// The favorite resolves anywhere an id is accepted.
		show := e.mustRun("show", "demo", "--json")
		var s struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(show), &s)).To(Succeed())
		Expect(s.ID).To(Equal(id))

		e.mustRun("fav", "rm", "demo")
		Expect(e.mustRun("fav", "ls")).NotTo(ContainSubstring("demo"))
	})

	It("rejects a favorite name that is a valid hex id-prefix", func() {
		e := newKrefEnv("Fav User", "fav@example.com")
		e.mustRun("init", "--json")
		id := idOf(e.mustRun("new", "--title", "Hexy", "--json"))

		_, stderr, err := e.run("", "fav", "add", id, "beef")
		Expect(err).To(HaveOccurred())
		Expect(stderr).To(ContainSubstring("hex"))
	})

	It("writes and validates the user config file", func() {
		e := newKrefEnv("Cfg User", "cfg@example.com")
		e.mustRun("init", "--json")

		Expect(e.mustRun("config", "init")).To(ContainSubstring("wrote"))

		check := e.mustRun("config", "check")
		Expect(check).To(ContainSubstring("config valid"))
		Expect(check).To(ContainSubstring("schema version: 1"))
	})

	It("creates the shared kref.conf entry and resolves a shared favorite", func() {
		e := newKrefEnv("Shared User", "shared@example.com")
		e.mustRun("init", "--json")
		id := idOf(e.mustRun("new", "--tier", "shared", "--title", "Road map", "--json"))

		Expect(e.mustRun("config", "init", "--shared")).To(ContainSubstring("created project config entry"))

		e.mustRun("fav", "add", id, "road", "--shared")

		ls := e.mustRun("fav", "ls")
		Expect(ls).To(ContainSubstring("road"))
		Expect(ls).To(ContainSubstring("(shared)"))

		// The shared favorite resolves like an id.
		Expect(e.mustRun("show", "road", "--json")).To(ContainSubstring(id))

		// The reserved kref.conf name resolves to a config-kind entry.
		conf := e.mustRun("show", "kref.conf", "--json")
		var c struct {
			Kind string `json:"kind"`
		}
		Expect(json.Unmarshal([]byte(conf), &c)).To(Succeed())
		Expect(c.Kind).To(Equal("config"))
	})

	It("documents the visudo-style editor in `config edit --help`", func() {
		// Driving the interactive editor needs $EDITOR, which the suite's
		// isolated env does not inject; asserting the help text keeps this
		// spec honest without faking an editor.
		e := newKrefEnv("Edit User", "edit@example.com")
		e.mustRun("init", "--json")

		help := e.mustRun("config", "edit", "--help")
		Expect(help).To(SatisfyAny(
			ContainSubstring("visudo"),
			ContainSubstring("EDITOR"),
		))
	})
})
