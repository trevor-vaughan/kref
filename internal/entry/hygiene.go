package entry

import "github.com/git-bug/git-bug/entity"

// LinkRef is one end of a relationship, resolved for display.
type LinkRef struct {
	ID    entity.Id `json:"id"`
	Type  string    `json:"type"`
	Title string    `json:"title"`
}

// LinkView holds an entry's outgoing and incoming typed edges.
type LinkView struct {
	Outgoing []LinkRef `json:"outgoing"`
	Incoming []LinkRef `json:"incoming"`
}

// TreeNode is a node in a parent-child relationship tree.
type TreeNode struct {
	ID       entity.Id   `json:"id"`
	Title    string      `json:"title"`
	Children []*TreeNode `json:"children"`
}

// TidyEntry is a compact reference to an entry in a tidy report.
type TidyEntry struct {
	ID     entity.Id `json:"id"`
	Title  string    `json:"title"`
	Tier   string    `json:"tier"`
	Status string    `json:"status"`
}

// DuplicateGroup is a set of live entries sharing a normalized title.
type DuplicateGroup struct {
	NormalizedTitle string      `json:"normalized_title"`
	Entries         []TidyEntry `json:"entries"`
}

// TidyReport is the read-only consolidation review surface.
type TidyReport struct {
	Duplicates []DuplicateGroup `json:"duplicates"`
	Diverged   []TidyEntry      `json:"diverged"`
	Superseded []TidyEntry      `json:"superseded"`
}
