package entry

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/git-bug/git-bug/entities/identity"
	"github.com/git-bug/git-bug/entity"
	"github.com/git-bug/git-bug/entity/dag"

	"github.com/trevor-vaughan/kref/internal/content"
)

// Operation is a kref operation: a dag.Operation that also folds into a Snapshot.
type Operation interface {
	dag.Operation
	Apply(*Snapshot)
}

const (
	_ dag.OperationType = iota
	CreateOp
	SetStatusOp
	SetBodyOp
	SetTitleOp
	AddLinkOp
	RemoveLinkOp
	TombstoneOp
	RestoreOp
	AddLabelOp
	RemoveLabelOp
	RecordOriginOp
	SetKindOp
	AckMergeOp
	TrackOp
	UntrackOp
	ReattributeOp
	ArchiveOp
	UnarchiveOp
	SetContentTypeOp
)

// Create initializes an entry with a kind and title.
type Create struct {
	dag.OpBase
	Kind        string `json:"kind"`
	Title       string `json:"title"`
	ContentType string `json:"content_type,omitempty"`
}

func NewCreate(author identity.Interface, kind, title string) *Create {
	return &Create{
		OpBase: dag.NewOpBase(CreateOp, author, time.Now().Unix()),
		Kind:   kind,
		Title:  title,
	}
}

func (op *Create) Id() entity.Id { return dag.IdOperation(op, &op.OpBase) }

func (op *Create) Validate() error {
	if op.Kind == "" {
		return fmt.Errorf("kind required")
	}
	if op.Title == "" {
		return fmt.Errorf("title required")
	}
	return op.OpBase.Validate(op, CreateOp)
}

func (op *Create) Apply(s *Snapshot) {
	s.Kind = op.Kind
	s.Title = op.Title
	s.Status = "open"
	s.ContentType = op.ContentType
	if s.ContentType == "" {
		s.ContentType = content.Default
	}
	s.CreatedAt = op.Time()
	s.UpdatedAt = op.Time()
	if a := op.Author(); a != nil {
		s.CreatedBy = a.Name()
		s.CreatedByEmail = a.Email()
	}
}

// SetStatus changes the entry status.
type SetStatus struct {
	dag.OpBase
	Status string `json:"status"`
}

func NewSetStatus(author identity.Interface, status string) *SetStatus {
	return &SetStatus{OpBase: dag.NewOpBase(SetStatusOp, author, time.Now().Unix()), Status: status}
}
func (op *SetStatus) Id() entity.Id { return dag.IdOperation(op, &op.OpBase) }
func (op *SetStatus) Validate() error {
	if op.Status == "" {
		return fmt.Errorf("status required")
	}
	return op.OpBase.Validate(op, SetStatusOp)
}
func (op *SetStatus) Apply(s *Snapshot) { s.Status = op.Status; s.UpdatedAt = op.Time() }

// SetBody replaces the markdown body.
type SetBody struct {
	dag.OpBase
	Body string `json:"body"`
}

func NewSetBody(author identity.Interface, body string) *SetBody {
	return &SetBody{OpBase: dag.NewOpBase(SetBodyOp, author, time.Now().Unix()), Body: body}
}
func (op *SetBody) Id() entity.Id   { return dag.IdOperation(op, &op.OpBase) }
func (op *SetBody) Validate() error { return op.OpBase.Validate(op, SetBodyOp) }
func (op *SetBody) Apply(s *Snapshot) {
	s.Body = op.Body
	s.EditedAt = op.Time()
	s.UpdatedAt = op.Time()
}

// SetTitle replaces the entry title.
type SetTitle struct {
	dag.OpBase
	Title string `json:"title"`
}

func NewSetTitle(author identity.Interface, title string) *SetTitle {
	return &SetTitle{OpBase: dag.NewOpBase(SetTitleOp, author, time.Now().Unix()), Title: title}
}
func (op *SetTitle) Id() entity.Id { return dag.IdOperation(op, &op.OpBase) }
func (op *SetTitle) Validate() error {
	if op.Title == "" {
		return fmt.Errorf("title required")
	}
	return op.OpBase.Validate(op, SetTitleOp)
}
func (op *SetTitle) Apply(s *Snapshot) { s.Title = op.Title; s.UpdatedAt = op.Time() }

// SetKind replaces the entry kind.
type SetKind struct {
	dag.OpBase
	Kind string `json:"kind"`
}

func NewSetKind(author identity.Interface, kind string) *SetKind {
	return &SetKind{OpBase: dag.NewOpBase(SetKindOp, author, time.Now().Unix()), Kind: kind}
}
func (op *SetKind) Id() entity.Id { return dag.IdOperation(op, &op.OpBase) }
func (op *SetKind) Validate() error {
	if op.Kind == "" {
		return fmt.Errorf("kind required")
	}
	return op.OpBase.Validate(op, SetKindOp)
}
func (op *SetKind) Apply(s *Snapshot) { s.Kind = op.Kind; s.UpdatedAt = op.Time() }

// SetContentType replaces the entry's content type (selects the show renderer).
type SetContentType struct {
	dag.OpBase
	ContentType string `json:"content_type"`
}

func NewSetContentType(author identity.Interface, ct string) *SetContentType {
	return &SetContentType{OpBase: dag.NewOpBase(SetContentTypeOp, author, time.Now().Unix()), ContentType: ct}
}
func (op *SetContentType) Id() entity.Id { return dag.IdOperation(op, &op.OpBase) }
func (op *SetContentType) Validate() error {
	if op.ContentType == "" {
		return fmt.Errorf("content type required")
	}
	if _, err := content.Canonical(op.ContentType); err != nil {
		return err
	}
	return op.OpBase.Validate(op, SetContentTypeOp)
}
func (op *SetContentType) Apply(s *Snapshot) { s.ContentType = op.ContentType; s.UpdatedAt = op.Time() }

// Track marks the entry as kept in sync with a local file at a repo-relative path.
type Track struct {
	dag.OpBase
	Path string `json:"path"`
}

func NewTrack(author identity.Interface, path string) *Track {
	return &Track{OpBase: dag.NewOpBase(TrackOp, author, time.Now().Unix()), Path: path}
}
func (op *Track) Id() entity.Id { return dag.IdOperation(op, &op.OpBase) }
func (op *Track) Validate() error {
	if op.Path == "" {
		return fmt.Errorf("tracked path required")
	}
	return op.OpBase.Validate(op, TrackOp)
}
func (op *Track) Apply(s *Snapshot) {
	s.Tracked = true
	s.TrackedPath = op.Path
	s.UpdatedAt = op.Time()
}

// Untrack clears the entry's local-file tracking. The file on disk is untouched.
type Untrack struct {
	dag.OpBase
}

func NewUntrack(author identity.Interface) *Untrack {
	return &Untrack{OpBase: dag.NewOpBase(UntrackOp, author, time.Now().Unix())}
}
func (op *Untrack) Id() entity.Id   { return dag.IdOperation(op, &op.OpBase) }
func (op *Untrack) Validate() error { return op.OpBase.Validate(op, UntrackOp) }
func (op *Untrack) Apply(s *Snapshot) {
	s.Tracked = false
	s.TrackedPath = ""
	s.UpdatedAt = op.Time()
}

// Reattribute overwrites the displayed author of an entry. The op is authored
// by whoever runs it (its OpBase author), while its payload carries the new
// display author — so history records who reattributed without rewriting the
// immutable Create op.
type Reattribute struct {
	dag.OpBase
	Name  string `json:"name"`
	Email string `json:"email"`
}

func NewReattribute(author identity.Interface, name, email string) *Reattribute {
	return &Reattribute{
		OpBase: dag.NewOpBase(ReattributeOp, author, time.Now().Unix()),
		Name:   name,
		Email:  email,
	}
}

func (op *Reattribute) Id() entity.Id { return dag.IdOperation(op, &op.OpBase) }

func (op *Reattribute) Validate() error {
	if op.Name == "" {
		return fmt.Errorf("author name required")
	}
	if op.Email == "" {
		return fmt.Errorf("author email required")
	}
	return op.OpBase.Validate(op, ReattributeOp)
}

func (op *Reattribute) Apply(s *Snapshot) {
	s.CreatedBy = op.Name
	s.CreatedByEmail = op.Email
	s.UpdatedAt = op.Time()
}

// AckMerge records merge-commit hashes acknowledged at resolve time, so the
// ◆ merged flag clears until a new (unacknowledged) merge appears.
type AckMerge struct {
	dag.OpBase
	Acked []string `json:"acked"`
}

func NewAckMerge(author identity.Interface, acked []string) *AckMerge {
	return &AckMerge{OpBase: dag.NewOpBase(AckMergeOp, author, time.Now().Unix()), Acked: acked}
}
func (op *AckMerge) Id() entity.Id { return dag.IdOperation(op, &op.OpBase) }
func (op *AckMerge) Validate() error {
	if len(op.Acked) == 0 {
		return fmt.Errorf("acked merge set required")
	}
	return op.OpBase.Validate(op, AckMergeOp)
}
func (op *AckMerge) Apply(s *Snapshot) {
	for _, h := range op.Acked {
		seen := false
		for _, e := range s.AckedMerges {
			if e == h {
				seen = true
				break
			}
		}
		if !seen {
			s.AckedMerges = append(s.AckedMerges, h)
		}
	}
	s.UpdatedAt = op.Time()
}

// AddLink adds a typed edge.
type AddLink struct {
	dag.OpBase
	To       string `json:"to"`
	LinkType string `json:"link_type"`
}

func NewAddLink(author identity.Interface, to, linkType string) *AddLink {
	return &AddLink{OpBase: dag.NewOpBase(AddLinkOp, author, time.Now().Unix()), To: to, LinkType: linkType}
}
func (op *AddLink) Id() entity.Id { return dag.IdOperation(op, &op.OpBase) }
func (op *AddLink) Validate() error {
	if op.To == "" {
		return fmt.Errorf("link target required")
	}
	return op.OpBase.Validate(op, AddLinkOp)
}
func (op *AddLink) Apply(s *Snapshot) {
	for _, l := range s.Links {
		if l.To == op.To && l.Type == op.LinkType {
			return
		}
	}
	s.Links = append(s.Links, Link{To: op.To, Type: op.LinkType})
	s.UpdatedAt = op.Time()
}

// RemoveLink removes all edges to a target.
type RemoveLink struct {
	dag.OpBase
	To string `json:"to"`
}

func NewRemoveLink(author identity.Interface, to string) *RemoveLink {
	return &RemoveLink{OpBase: dag.NewOpBase(RemoveLinkOp, author, time.Now().Unix()), To: to}
}
func (op *RemoveLink) Id() entity.Id   { return dag.IdOperation(op, &op.OpBase) }
func (op *RemoveLink) Validate() error { return op.OpBase.Validate(op, RemoveLinkOp) }
func (op *RemoveLink) Apply(s *Snapshot) {
	kept := s.Links[:0]
	for _, l := range s.Links {
		if l.To != op.To {
			kept = append(kept, l)
		}
	}
	s.Links = kept
	s.UpdatedAt = op.Time()
}

// Tombstone soft-deletes the entry (reversible; op-DAG retained).
type Tombstone struct {
	dag.OpBase
}

func NewTombstone(author identity.Interface) *Tombstone {
	return &Tombstone{OpBase: dag.NewOpBase(TombstoneOp, author, time.Now().Unix())}
}
func (op *Tombstone) Id() entity.Id   { return dag.IdOperation(op, &op.OpBase) }
func (op *Tombstone) Validate() error { return op.OpBase.Validate(op, TombstoneOp) }
func (op *Tombstone) Apply(s *Snapshot) {
	s.Deleted = true
	s.Status = "deleted"
	s.UpdatedAt = op.Time()
}

// Restore reverses a Tombstone: it un-deletes the entry. Status returns to
// "open" (kref has no CLI to set non-open status, so this is the only prior
// state to restore to).
type Restore struct {
	dag.OpBase
}

func NewRestore(author identity.Interface) *Restore {
	return &Restore{OpBase: dag.NewOpBase(RestoreOp, author, time.Now().Unix())}
}
func (op *Restore) Id() entity.Id   { return dag.IdOperation(op, &op.OpBase) }
func (op *Restore) Validate() error { return op.OpBase.Validate(op, RestoreOp) }
func (op *Restore) Apply(s *Snapshot) {
	if !s.Deleted {
		return // nothing to restore; don't disturb a live entry's status
	}
	s.Deleted = false
	s.Status = "open"
	s.UpdatedAt = op.Time()
}

// Archive hides an entry from the normal list. Unlike Tombstone it does NOT
// change Status, so an obsolete (or any) entry keeps its status while hidden.
type Archive struct {
	dag.OpBase
}

func NewArchive(author identity.Interface) *Archive {
	return &Archive{OpBase: dag.NewOpBase(ArchiveOp, author, time.Now().Unix())}
}
func (op *Archive) Id() entity.Id   { return dag.IdOperation(op, &op.OpBase) }
func (op *Archive) Validate() error { return op.OpBase.Validate(op, ArchiveOp) }
func (op *Archive) Apply(s *Snapshot) {
	s.Archived = true
	s.UpdatedAt = op.Time()
}

// Unarchive reverses an Archive, clearing the hidden flag. Status is untouched.
type Unarchive struct {
	dag.OpBase
}

func NewUnarchive(author identity.Interface) *Unarchive {
	return &Unarchive{OpBase: dag.NewOpBase(UnarchiveOp, author, time.Now().Unix())}
}
func (op *Unarchive) Id() entity.Id   { return dag.IdOperation(op, &op.OpBase) }
func (op *Unarchive) Validate() error { return op.OpBase.Validate(op, UnarchiveOp) }
func (op *Unarchive) Apply(s *Snapshot) {
	s.Archived = false
	s.UpdatedAt = op.Time()
}

// AddLabel adds a label to the entry's label set.
type AddLabel struct {
	dag.OpBase
	Label string `json:"label"`
}

func NewAddLabel(author identity.Interface, label string) *AddLabel {
	return &AddLabel{OpBase: dag.NewOpBase(AddLabelOp, author, time.Now().Unix()), Label: label}
}
func (op *AddLabel) Id() entity.Id { return dag.IdOperation(op, &op.OpBase) }
func (op *AddLabel) Validate() error {
	if op.Label == "" {
		return fmt.Errorf("label required")
	}
	return op.OpBase.Validate(op, AddLabelOp)
}
func (op *AddLabel) Apply(s *Snapshot) {
	for _, l := range s.Labels {
		if l == op.Label {
			return
		}
	}
	s.Labels = append(s.Labels, op.Label)
	sort.Strings(s.Labels)
	s.UpdatedAt = op.Time()
}

// RemoveLabel removes a label from the entry's label set.
type RemoveLabel struct {
	dag.OpBase
	Label string `json:"label"`
}

func NewRemoveLabel(author identity.Interface, label string) *RemoveLabel {
	return &RemoveLabel{OpBase: dag.NewOpBase(RemoveLabelOp, author, time.Now().Unix()), Label: label}
}
func (op *RemoveLabel) Id() entity.Id   { return dag.IdOperation(op, &op.OpBase) }
func (op *RemoveLabel) Validate() error { return op.OpBase.Validate(op, RemoveLabelOp) }
func (op *RemoveLabel) Apply(s *Snapshot) {
	out := s.Labels[:0:0]
	for _, l := range s.Labels {
		if l != op.Label {
			out = append(out, l)
		}
	}
	s.Labels = out
	s.UpdatedAt = op.Time()
}

// RecordOrigin appends a provenance event. The log is append-only.
type RecordOrigin struct {
	dag.OpBase
	Actor      string `json:"actor"`
	ActorKind  string `json:"actor_kind"`
	SourcePath string `json:"source_path"`
	Trigger    string `json:"trigger"`
}

func NewRecordOrigin(author identity.Interface, actor, actorKind, sourcePath, trigger string) *RecordOrigin {
	return &RecordOrigin{
		OpBase:     dag.NewOpBase(RecordOriginOp, author, time.Now().Unix()),
		Actor:      actor,
		ActorKind:  actorKind,
		SourcePath: sourcePath,
		Trigger:    trigger,
	}
}
func (op *RecordOrigin) Id() entity.Id { return dag.IdOperation(op, &op.OpBase) }
func (op *RecordOrigin) Validate() error {
	if op.Trigger == "" {
		return fmt.Errorf("origin trigger required")
	}
	return op.OpBase.Validate(op, RecordOriginOp)
}
func (op *RecordOrigin) Apply(s *Snapshot) {
	s.Provenance = append(s.Provenance, OriginEvent{
		Actor:      op.Actor,
		ActorKind:  op.ActorKind,
		SourcePath: op.SourcePath,
		Trigger:    op.Trigger,
		Time:       op.Time(),
	})
	s.UpdatedAt = op.Time()
}

// FirstHeading returns the text of the first markdown H1 ("# ...") line in
// body, or "" if there is none.
func FirstHeading(body string) string {
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") {
			return strings.TrimSpace(trimmed[2:])
		}
	}
	return ""
}

// DeriveTitle picks a title from body: the first H1, else the first non-empty
// line, else "". Used when `new` is called without an explicit --title.
func DeriveTitle(body string) string {
	if h := FirstHeading(body); h != "" {
		return h
	}
	for _, line := range strings.Split(body, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// NormalizeTitle folds a title into its duplicate-grouping key: lowercased,
// surrounding whitespace trimmed, internal whitespace runs collapsed to a single
// space. Punctuation is significant. This is deliberately codepoint-simple (no
// Unicode NFC folding) so it needs nothing beyond the standard library;
// canonical/fuzzy matching is the deferred search-index tier.
func NormalizeTitle(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// operationUnmarshaler decodes a stored op back into a concrete type.
func operationUnmarshaler(raw json.RawMessage, _ entity.Resolvers) (dag.Operation, error) {
	var t struct {
		OperationType dag.OperationType `json:"type"`
	}
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, err
	}
	var op dag.Operation
	switch t.OperationType {
	case CreateOp:
		op = &Create{}
	case SetStatusOp:
		op = &SetStatus{}
	case SetBodyOp:
		op = &SetBody{}
	case SetTitleOp:
		op = &SetTitle{}
	case AddLinkOp:
		op = &AddLink{}
	case RemoveLinkOp:
		op = &RemoveLink{}
	case TombstoneOp:
		op = &Tombstone{}
	case RestoreOp:
		op = &Restore{}
	case AddLabelOp:
		op = &AddLabel{}
	case RemoveLabelOp:
		op = &RemoveLabel{}
	case RecordOriginOp:
		op = &RecordOrigin{}
	case SetKindOp:
		op = &SetKind{}
	case AckMergeOp:
		op = &AckMerge{}
	case TrackOp:
		op = &Track{}
	case UntrackOp:
		op = &Untrack{}
	case ReattributeOp:
		op = &Reattribute{}
	case ArchiveOp:
		op = &Archive{}
	case UnarchiveOp:
		op = &Unarchive{}
	case SetContentTypeOp:
		op = &SetContentType{}
	default:
		return nil, fmt.Errorf("unknown operation type %v", t.OperationType)
	}
	if err := json.Unmarshal(raw, op); err != nil {
		return nil, err
	}
	return op, nil
}
