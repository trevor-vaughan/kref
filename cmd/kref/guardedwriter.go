package main

import (
	"errors"
	"strings"

	"github.com/git-bug/git-bug/entity"

	"github.com/trevor-vaughan/kref/internal/commentguard"
	"github.com/trevor-vaughan/kref/internal/scan"
	"github.com/trevor-vaughan/kref/internal/store"
)

// writeResult reports what the comment guard did with a body-bearing write.
// A parked write was NOT applied: it sits in the quarantine review queue until a
// human approves it. Unscanned means betterleaks was missing, so the body was
// stored without a scan (warn-not-fail, matching the CLI).
type writeResult struct {
	CommentID string
	Parked    *store.Parked
	Unscanned bool
}

// guardedWriter applies the comment secret policy to every body-bearing write
// from the interactive viewer, the way `kref comment` and the MCP tool already
// do. The viewer holds this behind the commentWriter interface, so the model
// itself stays free of scan/quarantine knowledge.
//
// actor/actorKind are held because the quarantine API needs them and the
// interface's edit/resolve methods carry no actor arguments; AddComment uses the
// actor it is passed, which comes from the same resolveActor call.
type guardedWriter struct {
	s         *store.Store
	actor     string
	actorKind string
}

func newGuardedWriter(s *store.Store, actor, actorKind string) *guardedWriter {
	return &guardedWriter{s: s, actor: actor, actorKind: actorKind}
}

// check runs the comment guard for a body destined for entry id. Non-empty
// findings mean the write must be parked rather than applied; unscanned means it
// may be applied but betterleaks never saw it.
func (g *guardedWriter) check(id entity.Id, body string) (unscanned bool, findings []scan.Finding, err error) {
	snap, err := g.s.Get(id)
	if err != nil {
		return false, nil, err
	}
	unscanned, err = commentguard.Check(snap, body, false)
	var refused *commentguard.RefusedError
	if errors.As(err, &refused) {
		return false, refused.Findings, nil
	}
	if err != nil {
		return false, nil, err
	}
	return unscanned, nil, nil
}

func (g *guardedWriter) AddComment(id entity.Id, actor, actorKind, body string, question bool, replyTo string) (writeResult, error) {
	unscanned, findings, err := g.check(id, body)
	if err != nil {
		return writeResult{}, err
	}
	if len(findings) > 0 {
		p, perr := g.s.QuarantineComment(id, body, question, replyTo, findings, actor, actorKind)
		if perr != nil {
			return writeResult{}, perr
		}
		return writeResult{Parked: &p}, nil
	}
	cid, err := g.s.AddComment(id, actor, actorKind, body, question, replyTo)
	if err != nil {
		return writeResult{}, err
	}
	return writeResult{CommentID: cid, Unscanned: unscanned}, nil
}

func (g *guardedWriter) EditComment(id entity.Id, target, body string) (writeResult, error) {
	unscanned, findings, err := g.check(id, body)
	if err != nil {
		return writeResult{}, err
	}
	if len(findings) > 0 {
		p, perr := g.s.QuarantineEditComment(id, target, body, findings, g.actor, g.actorKind)
		if perr != nil {
			return writeResult{}, perr
		}
		return writeResult{Parked: &p}, nil
	}
	if err := g.s.EditComment(id, target, body); err != nil {
		return writeResult{}, err
	}
	return writeResult{CommentID: target, Unscanned: unscanned}, nil
}

// ResolveWithNote resolves target, first posting note as a closing comment when
// one was written. A flagged note parks the whole gesture as a SINGLE resolve
// intent — matching `kref comment --resolve`. The viewer previously did this as
// AddComment followed by ResolveComment, which would have parked the note and
// resolved the question anyway.
func (g *guardedWriter) ResolveWithNote(id entity.Id, target, note string) (writeResult, error) {
	if strings.TrimSpace(note) == "" {
		// No body means nothing to scan: a bare state transition.
		return writeResult{CommentID: target}, g.s.ResolveComment(id, target)
	}
	unscanned, findings, err := g.check(id, note)
	if err != nil {
		return writeResult{}, err
	}
	if len(findings) > 0 {
		p, perr := g.s.QuarantineResolveNote(id, target, note, findings, g.actor, g.actorKind)
		if perr != nil {
			return writeResult{}, perr
		}
		return writeResult{Parked: &p}, nil
	}
	if _, err := g.s.AddComment(id, g.actor, g.actorKind, note, false, target); err != nil {
		return writeResult{}, err
	}
	if err := g.s.ResolveComment(id, target); err != nil {
		return writeResult{}, err
	}
	return writeResult{CommentID: target, Unscanned: unscanned}, nil
}

// UnresolveComment and DeleteComment carry no body, so there is nothing to scan
// and nothing to park — they pass straight through to the store.
func (g *guardedWriter) UnresolveComment(id entity.Id, target string) error {
	return g.s.UnresolveComment(id, target)
}

func (g *guardedWriter) DeleteComment(id entity.Id, target string) error {
	return g.s.DeleteComment(id, target)
}
