package entry

import (
	"fmt"
	"time"

	"github.com/trevor-vaughan/kref/internal/textdiff"
)

// LogEntry is a render-friendly view of one operation in an entry's history.
type LogEntry struct {
	Op      string    `json:"op"` // create | set-body | set-title | set-kind | set-content-type | set-status | add-label | remove-label | add-link | remove-link | tombstone | restore | origin | ack-merge | attest
	Author  string    `json:"author"`
	Time    time.Time `json:"time"`
	Detail  string    `json:"detail"`            // op-specific one-line summary
	Version int       `json:"version,omitempty"` // 1-based body version for set-body ops; 0 otherwise
}

// Log maps the entry's operations (in Lamport order) to typed log entries.
// After a sync-merge this includes every branch's operations, so a
// concurrently-edited body is visible rather than silently shadowed.
func (e *Entry) Log() []LogEntry {
	ops := e.Operations()
	out := make([]LogEntry, 0, len(ops))
	prevBody := "" // the body before each set-body, for per-version change stats
	version := 0
	for _, op := range ops {
		le := LogEntry{Author: op.Author().Name(), Time: op.Time()}
		switch o := op.(type) {
		case *Create:
			le.Op, le.Detail = "create", fmt.Sprintf("%s %q", o.Kind, o.Title)
		case *SetBody:
			version++
			st := textdiff.Stats(prevBody, o.Body)
			le.Op = "set-body"
			le.Version = version
			le.Detail = fmt.Sprintf("v%d  +%d/-%d chars, +%d/-%d lines",
				version, st.CharsAdded, st.CharsRemoved, st.LinesAdded, st.LinesRemoved)
			prevBody = o.Body
		case *SetTitle:
			le.Op, le.Detail = "set-title", o.Title
		case *SetKind:
			le.Op, le.Detail = "set-kind", o.Kind
		case *SetContentType:
			le.Op, le.Detail = "set-content-type", o.ContentType
		case *SetStatus:
			le.Op, le.Detail = "set-status", o.Status
		case *AddLabel:
			le.Op, le.Detail = "add-label", o.Label
		case *RemoveLabel:
			le.Op, le.Detail = "remove-label", o.Label
		case *AddLink:
			le.Op, le.Detail = "add-link", o.LinkType+" "+o.To
		case *RemoveLink:
			le.Op, le.Detail = "remove-link", o.To
		case *Tombstone:
			le.Op = "tombstone"
		case *Restore:
			le.Op = "restore"
		case *RecordOrigin:
			le.Op, le.Detail = "origin", o.Trigger+" by "+o.Actor
		case *AckMerge:
			le.Op, le.Detail = "ack-merge", fmt.Sprintf("%d commit(s)", len(o.Acked))
		case *Reattribute:
			le.Op, le.Detail = "reattribute", o.Name+" <"+o.Email+">"
		case *Archive:
			le.Op = "archive"
		case *Unarchive:
			le.Op = "unarchive"
		case *Attest:
			le.Op, le.Detail = "attest", string(o.Claim)
		default:
			le.Op = "op"
		}
		out = append(out, le)
	}
	return out
}

// Author identifies whoever authored an operation.
type Author struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// Authors returns the distinct authors of the entry's operations, in first-seen
// order.
//
// This is the best attribution kref has: the operation pack carries the author
// identity, whereas the enclosing git commit does not — git-bug builds commits
// from git's `author.*` config, which is normally unset, so kref's commits
// typically have an EMPTY commit ident. Anything deciding "who wrote this?" must
// ask here, not ask git.
//
// Attest operations are excluded: attesting is not authoring, and under
// ClaimReceived it is explicitly a claim about someone ELSE's work. Counting an
// attester here would feed back into the claim itself — `kref attest` derives
// ClaimReceived from whether this list holds anyone but you, so a peer's bare
// attestation would make your own later re-attestation of your own entry report
// "received". Re-attestation is the prescribed repair after a key expires, so
// that is a normal path, not a corner.
//
// It is not, however, PROOF. The author on an operation is asserted by whoever
// wrote it; a signature attests to the key that made the commit, not to these
// fields, and nothing cross-checks the two. Two callers read this: the
// foreign-author guard in `kref resign`, a courtesy check against signing
// someone else's work by accident rather than a control against someone who
// means to write as you, and the claim derivation in `kref attest`, which turns
// it into a user-visible trust label. Deriving attribution from the signature
// instead is a deferred design change, not a local fix here.
func (e *Entry) Authors() []Author {
	out := make([]Author, 0)
	seen := map[Author]bool{}
	for _, op := range e.Operations() {
		if _, ok := op.(*Attest); ok {
			continue
		}
		a := Author{Name: op.Author().Name(), Email: op.Author().Email()}
		if !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	return out
}

// BodyVersion is one historical body, captured from a SetBody operation.
type BodyVersion struct {
	Author string    `json:"author"`
	Time   time.Time `json:"time"`
	Body   string    `json:"body"`
}

// BodyVersions returns each SetBody body in order — the material for `kref diff`
// and for recovering a body that a later edit superseded.
func (e *Entry) BodyVersions() []BodyVersion {
	out := make([]BodyVersion, 0)
	for _, op := range e.Operations() {
		if sb, ok := op.(*SetBody); ok {
			out = append(out, BodyVersion{Author: op.Author().Name(), Time: op.Time(), Body: sb.Body})
		}
	}
	return out
}

// CommentBodies returns the body of every AddComment and EditComment operation
// in the entry's history, in operation order. Because the DAG ships full
// history, a comment body still pushes after the comment is deleted or its text
// edited away — so the push-time secret scan must examine all of them, not just
// the bodies visible in the live snapshot.
func (e *Entry) CommentBodies() []string {
	out := make([]string, 0)
	for _, op := range e.Operations() {
		switch o := op.(type) {
		case *AddComment:
			out = append(out, o.Body)
		case *EditComment:
			out = append(out, o.Body)
		}
	}
	return out
}
