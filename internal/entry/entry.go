package entry

import (
	"github.com/git-bug/git-bug/entities/identity"
	"github.com/git-bug/git-bug/entity"
	"github.com/git-bug/git-bug/entity/dag"
	"github.com/git-bug/git-bug/repository"
)

// Tier is a visibility tier, realized as a git ref namespace.
type Tier string

const (
	TierPrivate  Tier = "private"
	TierPersonal Tier = "personal"
	TierShared   Tier = "shared"
	// TierQuarantine is a reserved, private-typed SYSTEM tier holding writes that
	// tripped the secret scanner and await human review. It is never a user write
	// target and is hidden from listings.
	TierQuarantine Tier = "quarantine"
)

func (t Tier) Namespace() string { return "kref-" + string(t) }

// AllTiers is the full set of tiers (private first so Get/List see it).
func AllTiers() []Tier { return []Tier{TierPrivate, TierPersonal, TierShared} }

// Definition returns the dag.Definition for a tier (same ops, distinct namespace).
func Definition(t Tier) dag.Definition {
	return dag.Definition{
		Typename:             "kref entry",
		Namespace:            t.Namespace(),
		OperationUnmarshaler: operationUnmarshaler,
		FormatVersion:        1,
	}
}

// Entry wraps a dag.Entity with kref semantics.
type Entry struct {
	*dag.Entity
}

func wrap(e *dag.Entity) *Entry { return &Entry{Entity: e} }

// New creates an empty entry in the given tier.
func New(t Tier) *Entry { return wrap(dag.New(Definition(t))) }

// WrapForRead returns the wrapper function dag.ReadAll requires.
func WrapForRead() func(*dag.Entity) *Entry { return wrap }

// Compile folds the operation DAG into a Snapshot.
func (e *Entry) Compile() *Snapshot {
	snap := &Snapshot{ID: e.Id()}
	for _, op := range e.Operations() {
		//nolint:forcetypeassert // every op in this entry's DAG is one of our
		// Operation types by construction; a foreign op is a programmer error.
		op.(Operation).Apply(snap)
	}
	if snap.EditedAt.IsZero() {
		snap.EditedAt = snap.CreatedAt
	}
	return snap
}

func resolvers(repo repository.ClockedRepo) entity.Resolvers {
	return entity.Resolvers{
		&identity.Identity{}: identity.NewSimpleResolver(repo),
	}
}

// Read loads an entry by id from a tier.
func Read(repo repository.ClockedRepo, t Tier, id entity.Id) (*Entry, error) {
	return dag.Read(Definition(t), wrap, repo, resolvers(repo), id)
}
