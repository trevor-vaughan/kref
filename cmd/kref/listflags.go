package main

import (
	"github.com/spf13/cobra"

	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/render"
	"github.com/trevor-vaughan/kref/internal/store"
)

// listSelection is the entry-selection flag set shared by bare `kref` (the
// interactive cockpit) and `kref list` (the static table): which entries to
// show, and in what order. Presentation flags (--columns, --wide) and the
// list-only modes (--check, --new) stay on the command that defines them.
type listSelection struct {
	kind           string
	status         string
	tier           string
	sortBy         string
	labels         []string
	includeDeleted bool
	all            bool
	archived       bool
	openQuestions  bool
	unsigned       bool
}

// register defines the selection flags on c and wires their completions.
func (f *listSelection) register(c *cobra.Command, dir *string) {
	c.Flags().StringVar(&f.kind, "kind", "", "filter by kind")
	c.Flags().StringVar(&f.status, "status", "", "filter by status")
	c.Flags().StringVar(&f.tier, "tier", "", "filter by tier (kref tier list shows them)")
	c.Flags().StringArrayVar(&f.labels, "label", nil, "filter by label (repeatable, AND)")
	c.Flags().BoolVar(&f.includeDeleted, "include-deleted", false, "include soft-deleted (tombstoned) entries")
	c.Flags().BoolVar(&f.all, "all", false, "show everything: superseded + tombstoned, uncollapsed")
	c.Flags().BoolVar(&f.archived, "archived", false, "show only archived entries")
	c.Flags().BoolVar(&f.openQuestions, "open-questions", false, "only entries with an unresolved question comment")
	// No backquotes in this description: pflag reads a back-quoted word as the
	// flag's argument NAME, which renders a bool flag as "--unsigned kref show".
	//
	// "any commit" is the point: the verdict covers the entry's whole operation
	// chain, so an entry whose early history predates the signing key stays
	// findable however many signed edits land on top of it.
	//
	// It deliberately does not claim to be `kref resign`'s input set — resign
	// additionally refuses pushed, foreign-authored and quarantined entries, and
	// it does act on bad/untrusted ones, which this filter excludes because the
	// ⚠ marker already surfaces them unprompted.
	c.Flags().BoolVar(&f.unsigned, "unsigned", false, "only entries with any unsigned commit in their history")
	c.Flags().StringVar(&f.sortBy, "sort", "edited", "order by a field, e.g. --sort title or --sort tier — dates put newest first; :asc/:desc overrides")
	registerEntryFlagCompletions(c, dir)
	_ = c.RegisterFlagCompletionFunc("status", fixedFlag(statusValues))
	_ = c.RegisterFlagCompletionFunc("sort", fixedFlag(sortFlagValues()))
}

// filter resolves the flags into a store filter. The tier name is resolved
// through the store so an unknown tier fails loudly instead of silently
// matching nothing.
func (f *listSelection) filter(s *store.Store) (store.ListFilter, error) {
	var t entry.Tier
	if f.tier != "" {
		tdef, err := s.TierDef(f.tier)
		if err != nil {
			return store.ListFilter{}, err
		}
		t = tdef.Name
	}
	return store.ListFilter{
		Kind: f.kind, Status: f.status, Tier: t, Labels: f.labels,
		IncludeDelete: f.includeDeleted || f.all, ArchivedOnly: f.archived, IncludeArchived: f.all,
		OpenQuestionsOnly: f.openQuestions, UnsignedOnly: f.unsigned,
		// The surfaces this filter serves read the verdict: the aligned table
		// renders the ⚠ marker from it, --json emits it as sig_state, and the
		// cockpit's S toggle filters on it. (--plain has no signature column and
		// does not.) Commands that never show it — tidy, the quarantine queue,
		// completion — leave this false and pay no verification subprocess.
		WithSigState: true,
	}, nil
}

// sortSpec parses --sort. An empty value means "no explicit order": the
// renderer applies its own default.
func (f *listSelection) sortSpec() (*render.SortSpec, error) {
	if f.sortBy == "" {
		return nil, nil
	}
	return render.ParseSort(f.sortBy)
}
