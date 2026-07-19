package entry

import (
	"time"

	"github.com/git-bug/git-bug/entity"
)

// Link is a typed edge to another entry.
type Link struct {
	To   string `json:"to"`
	Type string `json:"type"` // relates | parent-child | supersedes | derived-from
}

// Comment is one append-only note on an entry. Question comments stay open
// until a ResolveComment op flips them; ReplyTo builds a thread tree.
type Comment struct {
	ID          string    `json:"id"`
	Author      string    `json:"author"`
	AuthorEmail string    `json:"author_email"`
	AuthorKind  string    `json:"author_kind"` // human | agent
	Body        string    `json:"body"`
	Time        time.Time `json:"time"`
	Question    bool      `json:"question"`
	ReplyTo     string    `json:"reply_to,omitempty"`
	Resolved    bool      `json:"resolved"`
	ResolvedBy  string    `json:"resolved_by,omitempty"`
	ResolvedAt  time.Time `json:"resolved_at,omitzero"`
	Edited      bool      `json:"edited,omitempty"`
	EditedAt    time.Time `json:"edited_at,omitzero"`
	Deleted     bool      `json:"deleted,omitempty"`
	DeletedBy   string    `json:"deleted_by,omitempty"`
	DeletedAt   time.Time `json:"deleted_at,omitzero"`
}

// OriginEvent is one entry in an entry's append-only provenance log.
type OriginEvent struct {
	Actor      string    `json:"actor"`
	ActorKind  string    `json:"actor_kind"` // human | agent
	SourcePath string    `json:"source_path"`
	Trigger    string    `json:"trigger"` // create | ingest | retier
	Time       time.Time `json:"time"`
}

// Statuses is the closed lifecycle vocabulary an entry's Status may take.
// CLI parsing/completion and the MCP lifecycle tool all validate against
// this one list so the vocabulary cannot drift between surfaces.
var Statuses = []string{"open", "active", "accepted", "superseded", "obsolete"}

// Snapshot is the compiled, read-only view of an entry.
type Snapshot struct {
	ID             entity.Id     `json:"id"`
	Kind           string        `json:"kind"`
	Title          string        `json:"title"`
	Status         string        `json:"status"`    // open | active | accepted | superseded | obsolete | deleted
	Tier           string        `json:"tier"`      // set by the store from the namespace it was read under
	TierType       string        `json:"tier_type"` // resolved tier type (private|personal|shared); drives glyph/color
	Body           string        `json:"body"`
	Version        int           `json:"version"`      // head body-version count (number of SetBody ops); the vN kref log shows, and the todo CAS token
	ContentType    string        `json:"content_type"` // text/markdown by default; selects the show renderer
	Links          []Link        `json:"links"`
	Labels         []string      `json:"labels"`
	Provenance     []OriginEvent `json:"provenance"`
	Comments       []Comment     `json:"comments,omitempty"`
	Merged         bool          `json:"merged"` // set by the store from the commit graph; not from Compile
	AckedMerges    []string      `json:"-"`      // merge-commit hashes acknowledged via kref resolve; drives merge detection
	Deleted        bool          `json:"deleted"`
	Archived       bool          `json:"archived"`     // hidden from normal list; independent of status
	Tracked        bool          `json:"tracked"`      // kept in sync with a local file
	TrackedPath    string        `json:"tracked_path"` // repo-relative working-copy path; empty when not tracked
	CreatedAt      time.Time     `json:"created_at"`
	UpdatedAt      time.Time     `json:"updated_at"`
	EditedAt       time.Time     `json:"edited_at"`        // last body-edit time (SetBody); Compile falls it back to CreatedAt when the body was never set
	CreatedBy      string        `json:"created_by"`       // author display name, from the Create operation
	CreatedByEmail string        `json:"created_by_email"` // author email, from the Create operation
}
