package entry

import (
	"errors"
	"fmt"
	"strings"
)

// ResolveCommentID expands a hex prefix to the full id of exactly one of the
// snapshot's comments. It exists because the comment ops (EditComment,
// DeleteComment, ResolveComment, UnresolveComment) match their target by exact
// id and are deliberate no-ops otherwise — a merge property that costs them the
// ability to report a bad target. Every write boundary that accepts a
// user-supplied target MUST resolve it here first, so an unknown or ambiguous
// target fails loudly instead of being appended as an op that silently does
// nothing.
func (s *Snapshot) ResolveCommentID(prefix string) (string, error) {
	if prefix == "" {
		return "", errors.New("empty comment id")
	}
	var matches []string
	for _, c := range s.Comments {
		if strings.HasPrefix(c.ID, prefix) {
			matches = append(matches, c.ID)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no comment matches %q", prefix)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("comment prefix %q is ambiguous (%d matches)", prefix, len(matches))
	}
}

// FindComment returns a pointer to the comment with the given full id, or nil
// when it is absent. It does not match prefixes — resolve those first with
// ResolveCommentID.
func (s *Snapshot) FindComment(id string) *Comment {
	for i := range s.Comments {
		if s.Comments[i].ID == id {
			return &s.Comments[i]
		}
	}
	return nil
}

// ActorVia joins the two halves of a composed agent actor: the model an agent
// declared for itself, and the client that carried the write
// ("claude-opus-5 via claude-code/2.1.4"). The halves differ in how much they
// can be trusted — the model is self-reported, the client comes from the MCP
// handshake — so they are stored joined rather than merged, and this package
// owns the format because it owns the field.
const ActorVia = " via "

// ActorModel returns just the model half of a composed actor, for surfaces
// where the client half is repetitive noise (a comment header repeats it on
// every reply). The full string stays in provenance and JSON. An actor that was
// never composed is returned unchanged.
func ActorModel(actor string) string {
	model, _, found := strings.Cut(actor, ActorVia)
	if !found {
		return actor
	}
	return model
}
