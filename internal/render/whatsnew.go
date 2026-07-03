package render

import (
	"fmt"
	"io"

	"github.com/trevor-vaughan/kref/internal/entry"
)

// WhatsNew renders the incoming (from last pull) and unpushed (local) sections.
func WhatsNew(w io.Writer, incoming, unpushed []*entry.Snapshot, color bool) {
	if len(incoming) == 0 && len(unpushed) == 0 {
		fmt.Fprintln(w, "nothing new since your last sync")
		return
	}
	if len(incoming) > 0 {
		fmt.Fprintln(w, "Incoming (last pull):")
		List(w, incoming, color, true)
		fmt.Fprintln(w)
	}
	if len(unpushed) > 0 {
		fmt.Fprintln(w, "Unpushed (local):")
		List(w, unpushed, color, true)
	}
}
