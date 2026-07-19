package main

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/git-bug/git-bug/entity"
	"github.com/spf13/cobra"

	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/render"
	"github.com/trevor-vaughan/kref/internal/store"
)

// completionLimit caps how many entry ids tab-completion offers at once. A repo
// can hold hundreds of entries; dumping all of them scrolls the prompt away, so
// completion shows the most recent and points at `kref list` for the full set.
const completionLimit = 10

// statusValues is the closed lifecycle vocabulary. parseStatus validates
// against it and shell completion offers it; the canonical list lives in the
// entry package so the MCP surface shares it too.
var statusValues = entry.Statuses

// tierNamesWhere returns resolved tier names passing filter, store-backed so
// custom tiers complete like built-ins. Errors fall back to the built-ins so
// completion still answers outside an initialized repo.
func tierNamesWhere(dir *string, filter func(entry.TierDef) bool) []string {
	defs := entry.BuiltinTierDefs()
	if s, err := store.Open(*dir); err == nil {
		defs = s.Tiers()
		_ = s.Close()
	}
	var out []string
	for _, d := range defs {
		if filter(d) {
			out = append(out, string(d.Name))
		}
	}
	return out
}

func allTierNames(dir *string) []string {
	return tierNamesWhere(dir, func(entry.TierDef) bool { return true })
}

func declaredTierNames(dir *string) []string {
	return tierNamesWhere(dir, func(d entry.TierDef) bool { return d.Declared })
}

// remoteTierNames drops private-typed tiers, which can never sync, and
// undeclared tiers, which cannot take a remote.
func remoteTierNames(dir *string) []string {
	return tierNamesWhere(dir, func(d entry.TierDef) bool { return d.Declared && d.Type != entry.TierPrivate })
}

// customTierNames returns only removable (declared, non-built-in) tiers.
func customTierNames(dir *string) []string {
	return tierNamesWhere(dir, func(d entry.TierDef) bool { return d.Declared && !d.Builtin() })
}

// listAll, listArchived, and listDeleted are the candidate entry sets for id
// completion. Most commands act on live entries; restore acts on tombstoned ones
// and unarchive on archived ones, so each gets the set it can actually accept.
func listAll(s *store.Store) ([]store.Excerpt, error) {
	return s.ListForCompletion(store.ListFilter{})
}

func listArchived(s *store.Store) ([]store.Excerpt, error) {
	return s.ListForCompletion(store.ListFilter{ArchivedOnly: true})
}

func listDeleted(s *store.Store) ([]store.Excerpt, error) {
	items, err := s.ListForCompletion(store.ListFilter{IncludeDelete: true})
	if err != nil {
		return nil, err
	}
	deleted := make([]store.Excerpt, 0, len(items))
	for _, e := range items {
		if e.Deleted {
			deleted = append(deleted, e)
		}
	}
	return deleted, nil
}

// entrySource pairs a candidate-entry lister with the ActiveHelp shown when the
// store holds none of that kind. Without it, an empty set yields a silent
// completion that reads as an unresponsive shell; the hint says why nothing was
// offered and points at the way forward.
type entrySource struct {
	list  func(*store.Store) ([]store.Excerpt, error)
	empty string
}

func listTodos(s *store.Store) ([]store.Excerpt, error) {
	return s.ListForCompletion(store.ListFilter{Kind: "todo"})
}

var (
	sourceAll      = entrySource{listAll, "no entries yet — create one with `kref new` or `kref ingest`"}
	sourceArchived = entrySource{listArchived, "no archived entries — `kref list --archived` lists them once you have some"}
	sourceDeleted  = entrySource{listDeleted, "no deleted entries to restore"}
	sourceTodo     = entrySource{listTodos, "no kind:todo entry — create one with `kref new --kind todo`"}
)

// offerEntryIDs returns entry-id completions (short id + title description) drawn
// from list, filtered by the typed prefix. A word that looks like a file path
// defers to the shell's file completion, since the id commands also accept the
// file an entry came from (see resolveArg). Store errors yield no suggestions
// rather than a completion error, so tab-completion stays quiet outside a repo.
func offerEntryIDs(dir *string, toComplete string, src entrySource) ([]string, cobra.ShellCompDirective) {
	if strings.ContainsRune(toComplete, '/') || strings.HasSuffix(toComplete, ".md") {
		return nil, cobra.ShellCompDirectiveDefault
	}
	s, err := store.Open(*dir)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	defer s.Close()
	items, err := src.list(s)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	// An empty candidate set is reported as guidance rather than silence. The
	// prefix filter below stays silent on a non-empty set with no prefix match,
	// matching normal shell "no completion" behaviour.
	if len(items) == 0 {
		return cobra.AppendActiveHelp(nil, src.empty), cobra.ShellCompDirectiveNoFileComp
	}
	// Most-recently-updated first, id breaking ties for stable output. The shell
	// would otherwise sort by the inserted id (i.e. by hash); KeepOrder in the
	// directive tells it to preserve this order, and the date in the description
	// makes the ordering legible.
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if !a.UpdatedAt.Equal(b.UpdatedAt) {
			return a.UpdatedAt.After(b.UpdatedAt)
		}
		return a.ID.String() < b.ID.String()
	})
	directive := cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveKeepOrder
	out := make([]string, 0, completionLimit)
	matched := 0
	for _, e := range items {
		short := render.ShortID(e.ID)
		if !strings.HasPrefix(short, toComplete) {
			continue
		}
		matched++
		if len(out) < completionLimit {
			out = append(out, short+"\t"+e.UpdatedAt.Format("2006-01-02")+"  "+e.Title)
		}
	}
	if matched > completionLimit {
		out = cobra.AppendActiveHelp(out, fmt.Sprintf("%d most recent of %d — run `kref list` to see all", completionLimit, matched))
	}
	return out, directive
}

// entryArgs builds a ValidArgsFunction offering entry ids for up to maxArgs
// positionals (maxArgs 0 = unbounded, for variadic id commands like update).
func entryArgs(dir *string, maxArgs int, src entrySource) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if maxArgs > 0 && len(args) >= maxArgs {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return offerEntryIDs(dir, toComplete, src)
	}
}

// favoriteArgs completes the <name> of `kref fav rm`: the favorite names in the
// layer the command will act on (the shared project entry with --shared, else
// the user config). Each candidate carries its target's short id as the
// description. An empty layer yields an ActiveHelp hint instead of silence, so a
// bare TAB explains why nothing was offered rather than reading as a dead shell.
func favoriteArgs(dir *string, shared *bool) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) >= 1 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		wantShared := shared != nil && *shared
		layer := "user"
		if wantShared {
			layer = "shared"
		}
		s, err := store.Open(*dir)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		defer s.Close()
		favs := s.Favorites()
		names := make([]string, 0, len(favs))
		for name, id := range favs {
			if s.FavoriteOrigin(name) != layer || !strings.HasPrefix(name, toComplete) {
				continue
			}
			names = append(names, name+"\t"+render.ShortID(entity.Id(id)))
		}
		if len(names) == 0 {
			hint := "no favorites yet — add one with `kref fav add <id> <name>`"
			if wantShared {
				hint = "no shared favorites — add one with `kref fav add <id> <name> --shared`"
			}
			return cobra.AppendActiveHelp(nil, hint), cobra.ShellCompDirectiveNoFileComp
		}
		sort.Strings(names)
		return names, cobra.ShellCompDirectiveNoFileComp
	}
}

// noPositionalHelp completes a flag-driven command that takes no positionals:
// it offers nothing and suppresses file completion, but surfaces an ActiveHelp
// line so a bare TAB explains how to drive the command instead of looking like
// an unresponsive shell.
func noPositionalHelp(hint string) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return cobra.AppendActiveHelp(nil, hint), cobra.ShellCompDirectiveNoFileComp
	}
}

// fixedValues filters a fixed vocabulary by the typed prefix.
func fixedValues(values []string, toComplete string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if strings.HasPrefix(v, toComplete) {
			out = append(out, v)
		}
	}
	return out
}

// entryThenEnum offers entry ids for the first positional and a fixed vocabulary
// for the second (status <id> <status>, retier <id|path> <tier>).
func entryThenEnum(dir *string, values []string) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		switch len(args) {
		case 0:
			return offerEntryIDs(dir, toComplete, sourceAll)
		case 1:
			return fixedValues(values, toComplete), cobra.ShellCompDirectiveNoFileComp
		default:
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
	}
}

// entryThenEnumFn is entryThenEnum with a lazily-computed vocabulary
// (store-backed enums must be read at completion time, not command-
// construction time).
func entryThenEnumFn(dir *string, values func() []string) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		switch len(args) {
		case 0:
			return offerEntryIDs(dir, toComplete, sourceAll)
		case 1:
			return fixedValues(values(), toComplete), cobra.ShellCompDirectiveNoFileComp
		default:
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
	}
}

// nthEnumFn offers a lazily-computed vocabulary only at positional index n
// (remote set's <tier>), suppressing file completion at every other position.
// The vocabulary is a func for the same reason as entryThenEnumFn.
func nthEnumFn(n int, values func() []string) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == n {
			return fixedValues(values(), toComplete), cobra.ShellCompDirectiveNoFileComp
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
}

// distinctStoreField returns the distinct non-empty values a field takes across
// every entry in the store (archived included), in first-seen order. It is the
// raw material for the discovery completions (--kind, --label), which answer
// "what do I actually have?".
func distinctStoreField(dir *string, field func(store.Excerpt) []string) ([]string, error) {
	s, err := store.Open(*dir)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	items, err := s.ListExcerpts(store.ListFilter{})
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, e := range items {
		for _, v := range field(e) {
			if v == "" || seen[v] {
				continue
			}
			seen[v] = true
			out = append(out, v)
		}
	}
	return out, nil
}

// completeStoreField completes a flag value from the distinct values a field
// takes across the store — the discovery win for --kind and --label. Returns
// nothing on any error so completion stays quiet outside a repo.
func completeStoreField(dir *string, field func(store.Excerpt) []string) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		values, err := distinctStoreField(dir, field)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return fixedValues(values, toComplete), cobra.ShellCompDirectiveNoFileComp
	}
}

// completeKindWithDefault completes --kind from the kinds already in the store,
// falling back to the flag's own default when the store holds none — so a first
// ingest into a fresh repo still tab-completes to a sensible starting kind
// instead of offering nothing. The default is read from the flag rather than
// hardcoded, so it tracks the flag definition if that ever changes.
func completeKindWithDefault(dir *string) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		kinds, err := distinctStoreField(dir, func(e store.Excerpt) []string { return []string{e.Kind} })
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		if len(kinds) == 0 {
			if f := cmd.Flag("kind"); f != nil && f.DefValue != "" {
				kinds = []string{f.DefValue}
			}
		}
		return fixedValues(kinds, toComplete), cobra.ShellCompDirectiveNoFileComp
	}
}

// completeCommandAliases offers subcommand aliases as first-word completions.
// Cobra's built-in completion offers only each subcommand's canonical Name()
// (completions.go), so `kref imp<TAB>` would not surface `import` (an alias of
// ingest). Set as the root command's ValidArgsFunction, this runs after cobra's
// subcommand-name pass and appends the matching aliases with each command's
// Short as the description. It fires only for the first word (no args yet).
func completeCommandAliases(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []string
	for _, sub := range cmd.Commands() {
		if !sub.IsAvailableCommand() {
			continue
		}
		for _, alias := range sub.Aliases {
			if strings.HasPrefix(alias, toComplete) {
				out = append(out, alias+"\t"+sub.Short)
			}
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// completeColumns completes list's --columns flag, whose value is a
// comma-separated column list (the `=` form only — NoOptDefVal means a bare
// `--columns` never consumes the next word). Only the segment after the last
// comma is matched, already-chosen columns are dropped, and each candidate
// carries the typed prefix so the shell inserts a valid composite value.
// NoSpace keeps the cursor in place for chaining another `,column`.
func completeColumns(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	prefix, last := "", toComplete
	if i := strings.LastIndex(toComplete, ","); i >= 0 {
		prefix, last = toComplete[:i+1], toComplete[i+1:]
	}
	chosen := map[string]bool{}
	for p := range strings.SplitSeq(prefix, ",") {
		if p = strings.TrimSpace(p); p != "" {
			chosen[p] = true
		}
	}
	var out []string
	for _, c := range render.AllColumns {
		name := string(c)
		if chosen[name] || !strings.HasPrefix(name, last) {
			continue
		}
		out = append(out, prefix+name+"\t"+render.ColumnDescription(c))
	}
	return out, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace
}

// sortFlagValues is the --sort completion vocabulary: any extra command-
// specific keys first (search's "matches"), then the shared sortable fields,
// then each key's non-default direction form (dates default to :desc, so
// their explicit suffix worth offering is :asc).
func sortFlagValues(extra ...string) []string {
	keys := slices.Concat(extra, render.SortKeys())
	out := make([]string, 0, len(keys)*2)
	out = append(out, keys...)
	for _, k := range keys {
		suffix := ":desc"
		if render.SortBareDesc(k) {
			suffix = ":asc"
		}
		out = append(out, k+suffix)
	}
	return out
}

// fixedFlag completes a flag value from a fixed vocabulary (--tier, --status).
func fixedFlag(values []string) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return fixedValues(values, toComplete), cobra.ShellCompDirectiveNoFileComp
	}
}
