package main

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("viewer action table", func() {
	It("binds every key exactly once", func() {
		seen := map[string]string{}
		for _, a := range viewerActions {
			for _, k := range a.Keys {
				Expect(seen).NotTo(HaveKey(k), "key %q is bound by both %q and %q", k, seen[k], a.Label)
				seen[k] = a.Label
			}
		}
	})

	It("gives every action at least one key and a label", func() {
		for _, a := range viewerActions {
			Expect(a.Keys).NotTo(BeEmpty(), "an action has no keys: %q", a.Label)
			Expect(a.Label).NotTo(BeEmpty(), "action %v has no label", a.Keys)
		}
	})

	It("indexes every key to its action", func() {
		for _, a := range viewerActions {
			for _, k := range a.Keys {
				got, ok := actionForKey(k)
				Expect(ok).To(BeTrue(), "key %q is not indexed", k)
				Expect(got.Label).To(Equal(a.Label))
			}
		}
	})

	It("marks handler-less actions as viewport passthroughs", func() {
		// A row with no Do documents a key the viewport handles. It must not be
		// dispatched, or the viewer would swallow a scroll key and do nothing.
		for _, a := range viewerActions {
			if a.Do == nil {
				Expect(a.Passthrough).To(BeTrue(), "%q has no handler but is not a passthrough", a.Label)
			}
		}
	})
})

var _ = Describe("viewer help rows", func() {
	It("reproduces the curated help layout", func() {
		Expect(helpRows()).To(Equal([]string{
			"j/k ↑/↓       scroll a line",
			"pgup/pgdn     scroll a page",
			"ctrl+d/u      scroll a half page",
			"tab/S-tab     next/prev item",
			"→/← l/h       into a reply / out to parent",
			"g/G           top / bottom",
			"<n>g          goto body line n",
			"space         fold the current section",
			"^space        fold / unfold everything",
			"/ n/N         search / next / prev",
			"r/e/d/x       reply / edit / delete / resolve↔reopen",
			"ctrl+r        refresh",
			"t             toggle colour",
			"? q esc       help / quit",
		}))
	})

	It("advertises every non-hidden action", func() {
		joined := strings.Join(helpRows(), "\n")
		for _, a := range viewerActions {
			if a.Hidden {
				continue
			}
			Expect(joined).To(ContainSubstring(a.display()),
				"action %q is bound but never advertised", a.Label)
		}
	})

	It("never advertises a hidden action", func() {
		joined := strings.Join(helpRows(), "\n")
		Expect(joined).NotTo(ContainSubstring("retired"))
	})
})
