package todo

// DeltaResult is the item-level difference between a seen body and the head
// body, split by class. NewDone/ChangedItems are in head document order.
type DeltaResult struct {
	ToReview     int
	Changed      int
	NewDone      []string
	ChangedItems []string
}

// Delta compares seen against head at item level: NewDone are Done-item texts
// present in head but not in seen (newly completed); ChangedItems are non-done
// item texts present in head but not in seen (new or edited open work).
func Delta(seenBody, headBody string) DeltaResult {
	seenDone := itemTexts(Parse(seenBody), true)
	seenOther := itemTexts(Parse(seenBody), false)
	var res DeltaResult
	head := Parse(headBody)
	for _, s := range head.sections {
		for _, n := range s.nodes {
			if n.kind != nodeItem || len(n.lines) == 0 || len(n.lines[0]) < 6 {
				continue
			}
			text := n.lines[0][6:]
			if n.state == StateDone {
				if !seenDone[text] {
					res.NewDone = append(res.NewDone, text)
				}
			} else if !seenOther[text] {
				res.ChangedItems = append(res.ChangedItems, text)
			}
		}
	}
	res.ToReview = len(res.NewDone)
	res.Changed = len(res.ChangedItems)
	return res
}

// itemTexts returns the set of item texts (the part after the fixed 6-char
// "- [x] " marker) whose done-ness matches done.
func itemTexts(d *Document, done bool) map[string]bool {
	out := map[string]bool{}
	for _, s := range d.sections {
		for _, n := range s.nodes {
			if n.kind != nodeItem || len(n.lines) == 0 || len(n.lines[0]) < 6 {
				continue
			}
			if (n.state == StateDone) != done {
				continue
			}
			out[n.lines[0][6:]] = true
		}
	}
	return out
}
