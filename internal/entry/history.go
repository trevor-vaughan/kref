package entry

import (
	"fmt"
	"time"

	"github.com/trevor-vaughan/kref/internal/textdiff"
)

// LogEntry is a render-friendly view of one operation in an entry's history.
type LogEntry struct {
	Op      string    `json:"op"` // create | set-body | set-title | set-kind | set-content-type | set-status | add-label | remove-label | add-link | remove-link | tombstone | restore | origin | ack-merge
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
		default:
			le.Op = "op"
		}
		out = append(out, le)
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
