package todo

import (
	"fmt"
	"regexp"
	"strings"
)

// Violation is one lint finding. Line is 1-based; 0 means document-level.
type Violation struct {
	Line int    `json:"line"`
	Rule string `json:"rule"`
	Msg  string `json:"message"`
}

// knownHeadings is the closed set of level-2 sections the todo grammar allows.
// ### subsections are free-named and not checked. Questions live as
// comment-threads now (the [?] marker is retired), so there is no questions
// section.
var knownHeadings = map[string]bool{
	"## Open":                  true,
	"## Future / low priority": true,
	"## Done (compact)":        true,
}

var checkboxRe = regexp.MustCompile(`^- \[(.)\] `)

// Lint checks the fixed grammar and returns the violations the formatter cannot
// safely auto-fix (placement issues are auto-fixed by Format, not linted). An
// empty result means the document is well-formed.
func Lint(body string) []Violation {
	var vs []Violation
	lines := strings.Split(body, "\n")
	h1, haveOpen, haveDone := 0, false, false
	for i, ln := range lines {
		switch {
		case strings.HasPrefix(ln, "# ") && !strings.HasPrefix(ln, "## "):
			h1++
		case strings.HasPrefix(ln, "## "):
			switch ln {
			case "## Open":
				haveOpen = true
			case "## Done (compact)":
				haveDone = true
			}
			if !knownHeadings[ln] {
				vs = append(vs, Violation{Line: i + 1, Rule: "unknown-heading",
					Msg: fmt.Sprintf("unknown section heading %q", ln)})
			}
		}
		if m := checkboxRe.FindStringSubmatch(ln); m != nil {
			state := m[1]
			switch state {
			case " ", "x":
			default:
				vs = append(vs, Violation{Line: i + 1, Rule: "checkbox-state",
					Msg: fmt.Sprintf("invalid checkbox state %q (use [ ] or [x])", state)})
			}
		}
	}
	if h1 != 1 {
		vs = append(vs, Violation{Line: 0, Rule: "h1",
			Msg: fmt.Sprintf("expected exactly one H1 title, found %d", h1)})
	}
	if !haveOpen {
		vs = append(vs, Violation{Line: 0, Rule: "missing-section",
			Msg: `missing required section "## Open"`})
	}
	if !haveDone {
		vs = append(vs, Violation{Line: 0, Rule: "missing-section",
			Msg: `missing required section "## Done (compact)"`})
	}
	return vs
}
