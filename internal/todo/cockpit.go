package todo

import (
	"fmt"
	"strings"
	"time"

	"github.com/trevor-vaughan/kref/internal/entry"
)

// Cockpit is the read-model behind `kref todo`'s header. ToReview and Changed
// are -1 until the seen-watermark exists (Plan 2b); the renderer suppresses a
// -1 signal.
type Cockpit struct {
	Open      int
	Done      int
	Awaiting  int
	Questions []string
	ToReview  int
	Changed   int
	Version   int       // head body-version (the CAS token); 0 suppresses the header token
	Edited    time.Time // last body-edit time; zero suppresses the edited field
	// QuarantinePending is the repo-wide count of writes held for secret review,
	// surfaced as a passive awareness badge; 0 suppresses it. It is a global
	// signal (not derived from this todo's body), set by the caller.
	QuarantinePending int
	// QuarantineStale is how many of QuarantinePending have aged past the stale
	// threshold (a subset; 0 <= QuarantineStale <= QuarantinePending).
	QuarantineStale int
}

// Summarize computes the cockpit counts from a todo body and the entry's
// comments. Open/Done come from the body; the awaiting-you signal (count and
// question list) comes from open question-comments — the [?] body marker is
// retired.
func Summarize(body string, comments []entry.Comment) Cockpit {
	d := Parse(body)
	qs := Questions(comments)
	return Cockpit{
		Open:      CountItems(d, StateOpen),
		Done:      CountItems(d, StateDone),
		Awaiting:  len(qs),
		Questions: qs,
		ToReview:  -1,
		Changed:   -1,
	}
}

// Questions returns the first line of every open question-comment (Question and
// not Resolved and not Deleted), in comment order — the text shown in the
// cockpit's numbered awaiting-you list.
func Questions(comments []entry.Comment) []string {
	var out []string
	for _, c := range comments {
		if !c.Question || c.Resolved || c.Deleted {
			continue
		}
		out = append(out, strings.SplitN(c.Body, "\n", 2)[0])
	}
	return out
}

// CollapseSections folds each section whose heading is in collapsed, replacing
// its body with the same one-line summary CollapseDone uses. Unknown headings
// are ignored. Pure and idempotent.
func CollapseSections(body string, collapsed map[string]bool) string {
	if len(collapsed) == 0 {
		return body
	}
	d := Parse(body)
	for heading := range collapsed {
		idx := d.sectionIndex(heading)
		if idx < 0 {
			continue
		}
		n := CountItems(d, StateDone)
		summary := fmt.Sprintf("_%d done — `kref show` for detail_", n)
		d.sections[idx].nodes = []node{{kind: nodeRaw, lines: []string{"", summary, ""}}}
	}
	return d.String()
}

// CollapseDone returns the body with the "## Done (compact)" section's content
// replaced by a single "N done — `kref show` for detail" summary line (the
// heading is kept). A body without that section is returned unchanged.
func CollapseDone(body string) (string, error) {
	return CollapseSections(body, map[string]bool{"## Done (compact)": true}), nil
}
