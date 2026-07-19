package render

import (
	"fmt"
	"io"

	"github.com/trevor-vaughan/kref/internal/todo"
)

// TodoLint writes the lint result. A clean result prints a single "ok" line;
// each violation prints its location and rule. color is accepted for
// signature-consistency with the other render helpers; the output is plain.
func TodoLint(w io.Writer, vs []todo.Violation, color bool) {
	_ = color
	if len(vs) == 0 {
		fmt.Fprintln(w, "todo: ok")
		return
	}
	for _, v := range vs {
		if v.Line > 0 {
			fmt.Fprintf(w, "todo: line %d: %s [%s]\n", v.Line, v.Msg, v.Rule)
			continue
		}
		fmt.Fprintf(w, "todo: %s [%s]\n", v.Msg, v.Rule)
	}
}
