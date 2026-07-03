package render_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/riotbox/kref/internal/render"
)

// Every entry was verified against a goldmark+glamour scratch harness on
// 2026-07-02; these are behavioral goldens, not aspirations.
var _ = DescribeTable("UnwrapMarkdown",
	func(in, want string) { Expect(render.UnwrapMarkdown(in)).To(Equal(want)) },

	Entry("empty input passes through", "", ""),
	Entry("single-line paragraph passes through", "hello\n", "hello\n"),
	Entry("joins a wrapped paragraph",
		"A paragraph that was wrapped\nacross three lines by an LLM\nshould join into one.\n",
		"A paragraph that was wrapped across three lines by an LLM should join into one.\n"),
	Entry("keeps a trailing-two-space hard break, joins the rest",
		"A hard break line ends here  \nso this line stays separate\nbut joins with this one.\n",
		"A hard break line ends here  \nso this line stays separate but joins with this one.\n"),
	Entry("keeps a backslash hard break",
		"Backslash hard break\\\nkeeps its newline.\n",
		"Backslash hard break\\\nkeeps its newline.\n"),
	Entry("joins bullets, nested bullets, and task items, keeping markers",
		"- a bullet wrapped across\n  two lines joins\n  - nested bullet also\n    wrapped joins\n- [ ] task item wrapped\n  across lines joins too\n",
		"- a bullet wrapped across two lines joins\n  - nested bullet also wrapped joins\n- [ ] task item wrapped across lines joins too\n"),
	Entry("joins a blockquote paragraph onto one marker line",
		"> a blockquote paragraph\n> wrapped across lines\n> joins into one quote line\n",
		"> a blockquote paragraph wrapped across lines joins into one quote line\n"),
	Entry("joins a lazy blockquote continuation (no marker on line 2)",
		"> lazy continuation quote\nwithout the marker joins\n",
		"> lazy continuation quote without the marker joins\n"),
	Entry("leaves a fenced code block untouched",
		"```text\ncode fence line one\n  code fence line two stays\n```\n",
		"```text\ncode fence line one\n  code fence line two stays\n```\n"),
	Entry("leaves an indented code block untouched",
		"    indented code block\n    stays exactly as is\n",
		"    indented code block\n    stays exactly as is\n"),
	Entry("leaves a GFM table untouched",
		"| col a | col b |\n|-------|-------|\n| cell  | cell  |\n",
		"| col a | col b |\n|-------|-------|\n| cell  | cell  |\n"),
	Entry("leaves an ATX heading line untouched",
		"# Heading stays\n\npara joins\nhere\n",
		"# Heading stays\n\npara joins here\n"),
	Entry("leaves a setext heading untouched",
		"setext heading\nwrapped second line\n===\n",
		"setext heading\nwrapped second line\n===\n"),
	Entry("joins CRLF input and normalizes line endings to LF",
		"para one wrapped\r\nacross crlf lines\r\n",
		"para one wrapped across crlf lines\n"),
	Entry("joins loose list item paragraphs",
		"- loose item para\n  wrapped lines\n\n- second loose item\n",
		"- loose item para wrapped lines\n\n- second loose item\n"),
	Entry("preserves a missing trailing newline",
		"no trailing newline para\nwrapped",
		"no trailing newline para wrapped"),
)
