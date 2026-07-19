// Package mcpserver is a thin MCP adapter over internal/store. It exposes a
// curated set of agentic tools; the store remains the only capability core.
package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/git-bug/git-bug/entity"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/trevor-vaughan/kref/internal/commentguard"
	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/entryguard"
	"github.com/trevor-vaughan/kref/internal/scan"
	"github.com/trevor-vaughan/kref/internal/store"
	"github.com/trevor-vaughan/kref/internal/textpatch"
	"github.com/trevor-vaughan/kref/internal/todoguard"
)

// Config configures the kref MCP server. Dir is the pinned repository (locked
// mode, the default). AllowRoots (from --allow) enables static global mode.
// ClientRoots (from --client-roots) enables dynamic mode where each call is
// confined to the client's advertised roots. AllowRoots and ClientRoots are
// mutually exclusive; Serve rejects both being set.
type Config struct {
	Dir         string
	Version     string
	AllowRoots  []string
	ClientRoots bool
}

// New builds the kref MCP server. dir is the pinned repository (locked mode);
// allowRoots, when non-empty, enables global mode where each tool call passes a
// per-call dir that must resolve inside an allowed root.
func New(cfg Config) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "kref", Version: cfg.Version}, nil)
	dp := newDirPolicy(cfg.Dir, cfg.AllowRoots, cfg.ClientRoots)
	mcp.AddTool(s, &mcp.Tool{
		Name: "kref_remember",
		Description: "Save a new knowledge-base entry (a memory). Returns its id. If the body " +
			"trips the secret scanner on a syncable tier the entry is HELD for human review " +
			"(quarantined) instead of saved: you get a quarantine id, a review thread opens, " +
			"and it is created only on human approval — nothing is lost. A private tier " +
			"cannot push, so it saves normally there.",
	}, remember(dp))
	mcp.AddTool(s, &mcp.Tool{
		Name: "kref_recall",
		Description: "Find entries by a title/body substring and optional label/tier/kind " +
			"filters. Each hit reports its id, tier/status, kind, version, updated date, " +
			"labels, and (for a search) how many times the query matched; pass limit to cap " +
			"the number of hits (the highest-relevance ones), which reports how many were held back.",
	}, recall(dp))
	mcp.AddTool(s, &mcp.Tool{
		Name: "kref_get",
		Description: "Read one entry in full by its id (or id prefix). Returns its kind, " +
			"version, content-type, updated date, labels, and links alongside the body.",
	}, get(dp))
	mcp.AddTool(s, &mcp.Tool{
		Name: "kref_update",
		Description: "Replace an entry's body (and optionally its title). For a " +
			"kind:todo entry, if_version is REQUIRED: pass the version you read from " +
			"kref_get/kref_recall — the write is refused as stale if the entry has " +
			"moved on since, so a concurrent edit is never clobbered. If the new body trips " +
			"the secret scanner on a syncable entry the update is HELD for human review " +
			"(quarantined) and the live entry is left untouched until a human approves it. " +
			"Prefer kref_patch for small edits. Optionally manage metadata in the same " +
			"call: add_labels/remove_labels (string arrays) and add_links/remove_links " +
			"(add_links is [{to, type?}] with type defaulting to \"relates\"; remove_links " +
			"is an array of target ids). body may be omitted for a metadata-only update. " +
			"Label and link changes apply to the live entry even when the body is held for " +
			"review; a secret in an add_labels value on a syncable tier is refused.",
	}, update(dp))
	mcp.AddTool(s, &mcp.Tool{
		Name: "kref_patch",
		Description: "Edit an entry's body by applying a unified diff. Provide " +
			"standard @@ hunks; line numbers are treated as hints only — each hunk " +
			"is located by its context/removal lines, which must match the current " +
			"body (exactly, or up to trailing whitespace). Hunks apply in order, " +
			"all-or-nothing, producing one new body version. If the patched body trips the " +
			"secret scanner on a syncable entry the update is HELD for human review " +
			"(quarantined), leaving the live entry untouched until approved. Prefer this over " +
			"kref_update for small edits — a stale or ambiguous hunk fails loudly " +
			"instead of overwriting concurrent changes.",
	}, patchTool(dp))
	mcp.AddTool(s, &mcp.Tool{
		Name: "kref_lifecycle",
		Description: "Manage an entry's lifecycle. action is one of: set_status " +
			"(requires status: open|active|accepted|superseded|obsolete), delete " +
			"(reversible tombstone), restore (undo delete), archive (hide from " +
			"listings, status kept), unarchive. Hard-delete (purge) and tier moves " +
			"(retier) are deliberately not available to agents.",
	}, lifecycle(dp))
	mcp.AddTool(s, &mcp.Tool{
		Name: "kref_comment",
		Description: "Add or manage a threaded comment on an entry. action is one of: " +
			"add ({body, question?, reply_to?} — question marks a thread open until " +
			"resolved, reply_to nests under another comment), resolve ({target, note?} " +
			"— resolves a question, optionally posting a closing note reply first), " +
			"edit ({target, body}), delete ({target}, a reversible tombstone). If an added " +
			"comment body trips the secret scanner on a syncable entry it is HELD for human " +
			"review (quarantined) and posted only on approval; a private entry cannot push, " +
			"so it posts normally there.",
	}, comment(dp))
	mcp.AddTool(s, &mcp.Tool{
		Name:        "kref_supersede",
		Description: "Mark <old> superseded by <new> (links them and retires <old>).",
	}, supersede(dp))
	mcp.AddTool(s, &mcp.Tool{
		Name: "kref_quarantine",
		Description: "Read the human-review queue for writes held by the secret scanner. " +
			"action is one of: list (every pending item — its id, the held op and target, " +
			"and the flagging rule), show ({id} — one item's findings and metadata). This is " +
			"READ-ONLY situational awareness: the proposed content is withheld (it contains the " +
			"flagged secret) and approve/reject are deliberately NOT available to agents — a " +
			"human decides at the CLI (kref quarantine approve|reject).",
	}, quarantineRead(dp))
	return s
}

// Serve runs the kref MCP server over stdio until the host closes the connection.
// A host ending the session by closing its end of the pipe is a clean shutdown,
// not a failure, so the resulting EOF is not propagated as an error.
func Serve(ctx context.Context, dir, version string, allowRoots []string, clientRoots bool) error {
	if clientRoots && len(allowRoots) > 0 {
		return errors.New("--allow and --client-roots are mutually exclusive")
	}
	for _, r := range allowRoots {
		if !filepath.IsAbs(r) {
			return fmt.Errorf("--allow root must be an absolute path: %q", r)
		}
		if fi, err := os.Stat(r); err != nil || !fi.IsDir() {
			return fmt.Errorf("--allow root does not exist or is not a directory: %q", r)
		}
	}
	srv := New(Config{Dir: dir, Version: version, AllowRoots: allowRoots, ClientRoots: clientRoots})
	if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil && !gracefulClose(err) {
		return err
	}
	return nil
}

// gracefulClose reports whether err is the benign end-of-session signal an stdio
// MCP host produces by closing its end of the pipe. The go-sdk surfaces this as
// "server is closing: EOF", but the closing sentinel lives in an internal
// package (not importable) and the underlying io.EOF is rendered with %v rather
// than %w — so neither is reachable through errors.Is, and we fall back to
// matching the EOF cause in the message. A malformed-input or write error (which
// does not end in EOF) is deliberately left to propagate as a real failure.
func gracefulClose(err error) bool {
	return err != nil && (errors.Is(err, io.EOF) || strings.HasSuffix(err.Error(), "EOF"))
}

func textResult(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

func toolError(format string, a ...any) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(format, a...)}}}
}

// guardTodo applies the todo write-boundary guard for an MCP write. It returns
// the body to write. On a lint rejection it preserves the rejected body to a
// recovery file and returns a non-nil tool-error result the caller must return;
// in that case the returned string is empty and no write should occur.
func guardTodo(id, kind, body string) (string, *mcp.CallToolResult) {
	out, err := todoguard.Guard(kind, body, todoguard.Options{})
	var rej *todoguard.RejectedError
	if errors.As(err, &rej) {
		path, werr := todoguard.WriteRejected(id, rej.Body)
		if werr != nil {
			return "", toolError("%v (could not save recovery file: %v)", rej, werr)
		}
		return "", toolError("%v\nthe rejected body was saved to %s", rej, path)
	}
	if err != nil {
		return "", toolError("%v", err)
	}
	return out, nil
}

// dirParam is embedded in every tool's params so a host may pass a per-call
// repository path. In locked mode (the default) it must equal the server's
// pinned repo; global multi-repo mode is future work. The go-sdk promotes this
// embedded field into each tool's schema.
type dirParam struct {
	Dir string `json:"dir,omitempty" jsonschema:"optional repository path; must equal the server's pinned repository (multi-repo host configs); rejected otherwise"`
}

// dirPolicy decides which repository a tool call operates on. In locked mode
// (roots empty) a per-call dir must equal the pinned repo (piece B). In global
// mode (roots non-empty, via `kref mcp --allow`) a per-call dir is required and
// must resolve, canonicalized and segment-checked, inside an allowed root.
type dirPolicy struct {
	pinned      string   // the --dir repo; locked mode's single allowed repo
	roots       []string // canonicalized --allow roots; non-empty => static global mode
	clientRoots bool     // --client-roots => per-call boundary from the client
}

func newDirPolicy(pinned string, allowRoots []string, clientRoots bool) dirPolicy {
	dp := dirPolicy{pinned: pinned, clientRoots: clientRoots}
	for _, r := range allowRoots {
		dp.roots = append(dp.roots, canonicalDir(r))
	}
	return dp
}

func (dp dirPolicy) resolve(ctx context.Context, sess *mcp.ServerSession, callDir string) (string, bool, error) {
	if dp.clientRoots {
		roots, err := fetchClientRoots(ctx, sess)
		if err != nil {
			return "", false, fmt.Errorf("could not read the client's advertised roots: %w", err)
		}
		if len(roots) == 0 {
			return "", false, errors.New("this server was started with --client-roots but the client advertised no usable file:// roots")
		}
		eff, err := matchRoots(roots, callDir)
		if err != nil {
			return "", false, err
		}
		home := len(roots) == 1 && eff == roots[0]
		return eff, !home, nil
	}
	if len(dp.roots) == 0 { // locked mode
		if callDir == "" {
			return dp.pinned, false, nil
		}
		if canonicalDir(dp.pinned) != canonicalDir(callDir) {
			return "", false, errors.New("dir does not match this server's repository (it was pinned at startup via --dir/KREF_DIR); cross-repo addressing needs global mode (kref mcp --allow)")
		}
		return dp.pinned, false, nil
	}
	eff, err := matchRoots(dp.roots, callDir)
	if err != nil {
		return "", false, err
	}
	return eff, true, nil
}

// fetchClientRoots asks the connected client for its advertised roots and
// returns the canonicalized local paths of the file:// ones. A transport error
// is returned to the caller (fail closed); unusable URIs are skipped.
func fetchClientRoots(ctx context.Context, sess *mcp.ServerSession) ([]string, error) {
	if sess == nil {
		return nil, errors.New("no client session")
	}
	res, err := sess.ListRoots(ctx, nil)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, r := range res.Roots {
		if p, ok := fileURIToPath(r.URI); ok {
			out = append(out, canonicalDir(p))
		}
	}
	return out, nil
}

// matchRoots resolves callDir against a set of canonicalized roots using the
// segment-aware prefix rule shared by static (--allow) and client-advertised
// modes. An empty callDir defaults to the sole root, or is refused when there
// is more than one. A relative or out-of-bounds callDir is refused.
func matchRoots(roots []string, callDir string) (string, error) {
	if callDir == "" {
		if len(roots) == 1 {
			return roots[0], nil
		}
		return "", errors.New("dir is required: this server allows multiple repository roots — pass the target repo's absolute path")
	}
	if !filepath.IsAbs(callDir) {
		return "", errors.New("dir must be an absolute path in global mode")
	}
	c := canonicalDir(callDir)
	for _, r := range roots {
		if c == r || strings.HasPrefix(c, r+string(os.PathSeparator)) {
			return c, nil
		}
	}
	return "", errors.New("dir is outside the server's allowed roots")
}

// fileURIToPath converts an MCP root URI to a local filesystem path. It accepts
// only file:// URIs on the local host (empty or "localhost"); anything else —
// a remote host, a non-file scheme, or an unparseable URI — is rejected so it
// is skipped rather than treated as a usable root.
func fileURIToPath(uri string) (string, bool) {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return "", false
	}
	if u.Host != "" && u.Host != "localhost" {
		return "", false
	}
	if u.Path == "" {
		return "", false
	}
	return u.Path, true
}

// isPrivateTyped reports whether a tier's resolved type is private — the
// non-syncable, host-bound class (private, the quarantine system tier, and the
// future agent tier). A global/client-roots MCP server does not serve these
// except for the client's own sole root.
func isPrivateTyped(tierType string) bool { return tierType == string(entry.TierPrivate) }

// restrictedTierMsg is the refusal a by-id or create MCP op returns when its
// target lives in (or would create in) a private-typed tier under a restricted
// (global/client-roots, non-home) call.
const restrictedTierMsg = "that entry is in a private-typed tier, which a global/client-roots MCP server does not serve (only the client's own single root gets private-tier access)"

// canonicalDir renders p for comparison: absolute, symlinks resolved when the
// path exists, else lexically cleaned. A non-existent callDir simply fails the
// equality — no special existence signal.
func canonicalDir(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return filepath.Clean(abs)
}

type rememberParams struct {
	dirParam
	Kind   string   `json:"kind,omitempty"`
	Title  string   `json:"title,omitempty"`
	Body   string   `json:"body,omitempty"`
	Tier   string   `json:"tier,omitempty"`
	Labels []string `json:"labels,omitempty"`
}

func remember(dp dirPolicy) mcp.ToolHandlerFor[rememberParams, any] {
	return func(ctx context.Context, req *mcp.CallToolRequest, p rememberParams) (*mcp.CallToolResult, any, error) {
		effDir, restricted, derr := dp.resolve(ctx, req.Session, p.Dir)
		if derr != nil {
			return toolError("%v", derr), nil, nil
		}
		st, err := store.Open(effDir)
		if err != nil {
			return nil, nil, err
		}
		defer st.Close()

		tier, tierType := entry.TierPersonal, entry.TierPersonal
		if p.Tier != "" {
			d, err := st.DeclaredTier(p.Tier)
			if err != nil {
				return toolError("%v", err), nil, nil
			}
			tier, tierType = d.Name, d.Type
		}
		if restricted && isPrivateTyped(string(tierType)) {
			return toolError("%s", restrictedTierMsg), nil, nil
		}
		title := p.Title
		if title == "" {
			title = entry.DeriveTitle(p.Body)
		}
		if title == "" {
			return toolError("provide a title, or a body to derive one from"), nil, nil
		}
		kind := p.Kind
		if kind == "" {
			kind = "document"
		}
		// A secret-bearing body into a syncable tier is diverted into the
		// quarantine review queue as a draft (approval retiers it to `tier`),
		// not written to `tier`.
		target := &entry.Snapshot{Tier: string(tier), TierType: string(tierType)}
		unscanned, done := parkEntry(target, p.Body, func(f []scan.Finding) (store.Parked, error) {
			return st.QuarantineNewEntry(tier, kind, title, p.Body, "", f, "agent")
		})
		if done != nil {
			return done, nil, nil
		}
		body, rejected := guardTodo("new", kind, p.Body)
		if rejected != nil {
			return rejected, nil, nil
		}
		id, err := st.Add(tier, kind, title, body)
		if err != nil {
			return nil, nil, err
		}
		for _, l := range p.Labels {
			if err := st.AddLabel(id, l); err != nil {
				return nil, nil, err
			}
		}
		if err := st.RecordOrigin(id, "mcp", "agent", "", "create"); err != nil {
			return nil, nil, err
		}
		msg := "remembered " + id.String()
		if unscanned {
			msg += unscannedNote
		}
		return textResult(msg), nil, nil
	}
}

type recallParams struct {
	dirParam
	Search string `json:"search,omitempty"`
	Label  string `json:"label,omitempty"`
	Tier   string `json:"tier,omitempty"`
	Kind   string `json:"kind,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// recallHit pairs a snapshot with its query-match count (-1 when the recall had
// no search term, so no count applies).
type recallHit struct {
	snap    *entry.Snapshot
	matches int
}

func recall(dp dirPolicy) mcp.ToolHandlerFor[recallParams, any] {
	return func(ctx context.Context, req *mcp.CallToolRequest, p recallParams) (*mcp.CallToolResult, any, error) {
		effDir, restricted, derr := dp.resolve(ctx, req.Session, p.Dir)
		if derr != nil {
			return toolError("%v", derr), nil, nil
		}
		st, err := store.Open(effDir)
		if err != nil {
			return nil, nil, err
		}
		defer st.Close()
		f := store.ListFilter{Search: p.Search, Kind: p.Kind}
		if p.Label != "" {
			f.Labels = []string{p.Label}
		}
		if p.Tier != "" {
			d, err := st.TierDef(p.Tier)
			if err != nil {
				return toolError("%v", err), nil, nil
			}
			f.Tier = d.Name
		}
		// A search term yields per-entry match counts (relevance-ordered); a
		// filter-only recall has nothing to count and stays list-ordered.
		var hits []recallHit
		if strings.TrimSpace(p.Search) != "" {
			results, err := st.Search(f)
			if err != nil {
				return nil, nil, err
			}
			for _, r := range results {
				hits = append(hits, recallHit{snap: r.Snapshot, matches: r.Matches})
			}
		} else {
			items, err := st.List(f)
			if err != nil {
				return nil, nil, err
			}
			for _, it := range items {
				hits = append(hits, recallHit{snap: it, matches: -1})
			}
		}
		if restricted {
			kept := hits[:0]
			for _, h := range hits {
				if !isPrivateTyped(h.snap.TierType) {
					kept = append(kept, h)
				}
			}
			hits = kept
		}
		if len(hits) == 0 {
			return textResult("no matching entries"), nil, nil
		}
		total := len(hits)
		truncated := p.Limit > 0 && total > p.Limit
		if truncated {
			hits = hits[:p.Limit]
		}
		var b strings.Builder
		for _, h := range hits {
			s := h.snap
			fmt.Fprintf(&b, "%s  [%s/%s]  kind:%s  v%d  updated %s",
				s.ID.String(), s.Tier, s.Status, s.Kind, s.Version, s.UpdatedAt.Format("2006-01-02"))
			if len(s.Labels) > 0 {
				fmt.Fprintf(&b, "  labels:%s", strings.Join(s.Labels, ","))
			}
			if h.matches >= 0 {
				fmt.Fprintf(&b, "  matches:%d", h.matches)
			}
			fmt.Fprintf(&b, "  %s\n", s.Title)
		}
		if truncated {
			fmt.Fprintf(&b, "\nshowing %d of %d (raise limit for more)\n", p.Limit, total)
		}
		return textResult(b.String()), nil, nil
	}
}

type getParams struct {
	dirParam
	ID string `json:"id"`
}

func get(dp dirPolicy) mcp.ToolHandlerFor[getParams, any] {
	return func(ctx context.Context, req *mcp.CallToolRequest, p getParams) (*mcp.CallToolResult, any, error) {
		effDir, restricted, derr := dp.resolve(ctx, req.Session, p.Dir)
		if derr != nil {
			return toolError("%v", derr), nil, nil
		}
		st, err := store.Open(effDir)
		if err != nil {
			return nil, nil, err
		}
		defer st.Close()
		id, err := st.Resolve(p.ID)
		if err != nil {
			return toolError("%v", err), nil, nil
		}
		snap, err := st.Get(id)
		if err != nil {
			return toolError("%v", err), nil, nil
		}
		if restricted && isPrivateTyped(snap.TierType) {
			return toolError("%s", restrictedTierMsg), nil, nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%s [%s/%s] %s\n", snap.ID.String(), snap.Tier, snap.Status, snap.Title)
		fmt.Fprintf(&b, "kind: %s\n", snap.Kind)
		// The version is the CAS token kref_update's if_version wants for a todo.
		fmt.Fprintf(&b, "version: %d\n", snap.Version)
		fmt.Fprintf(&b, "content-type: %s\n", snap.ContentType)
		fmt.Fprintf(&b, "updated: %s\n", snap.UpdatedAt.Format("2006-01-02"))
		if len(snap.Labels) > 0 {
			fmt.Fprintf(&b, "labels: %s\n", strings.Join(snap.Labels, ", "))
		}
		if len(snap.Links) > 0 {
			parts := make([]string, len(snap.Links))
			for i, l := range snap.Links {
				parts[i] = l.Type + " " + l.To
			}
			fmt.Fprintf(&b, "links: %s\n", strings.Join(parts, ", "))
		}
		fmt.Fprintf(&b, "\n%s\n", snap.Body)
		return textResult(b.String()), nil, nil
	}
}

type updateParams struct {
	dirParam
	ID           string    `json:"id"`
	Body         string    `json:"body,omitempty"`
	Title        string    `json:"title,omitempty"`
	IfVersion    *int      `json:"if_version,omitempty"`
	AddLabels    []string  `json:"add_labels,omitempty"`
	RemoveLabels []string  `json:"remove_labels,omitempty"`
	AddLinks     []linkArg `json:"add_links,omitempty"`
	RemoveLinks  []string  `json:"remove_links,omitempty"`
}

type linkArg struct {
	To   string `json:"to"`
	Type string `json:"type,omitempty"`
}

// metaPlan is the validated, ready-to-apply set of label/link mutations for an
// update: link targets are already resolved to ids so applying them cannot fail
// on a bad reference after the body has been written.
type metaPlan struct {
	addLabels    []string
	removeLabels []string
	addLinks     []resolvedLink
	removeLinks  []string // resolved target ids
	warnings     []string
}

type resolvedLink struct {
	to  string // resolved target id
	typ string
}

func (p metaPlan) empty() bool {
	return len(p.addLabels) == 0 && len(p.removeLabels) == 0 &&
		len(p.addLinks) == 0 && len(p.removeLinks) == 0
}

// validateMeta resolves the label/link operations for an update WITHOUT writing
// anything: link targets are resolved to ids (an unresolvable target is a tool
// error) and a public→private link records a leak warning. Applying the returned
// plan afterwards cannot fail on a bad reference. The add_labels secret scan is
// added on top of this (a secret-bearing label on a syncable tier is refused).
func validateMeta(st *store.Store, snap *entry.Snapshot, p updateParams) (metaPlan, *mcp.CallToolResult) {
	plan := metaPlan{addLabels: p.AddLabels, removeLabels: p.RemoveLabels}
	// An agent-written label value syncs, so scan it: on a syncable tier a secret
	// is refused (private cannot push, so it is allowed). No force (MCP has none)
	// and no park (a label is trivial to re-enter). The value is never echoed.
	if snap.TierType != string(entry.TierPrivate) {
		for _, l := range p.AddLabels {
			findings, serr := scan.Scan([]byte(l))
			if errors.Is(serr, scan.ErrMissing) {
				plan.warnings = append(plan.warnings, "warning: betterleaks unavailable — label stored UNSCANNED")
				continue
			}
			if serr != nil {
				return metaPlan{}, toolError("secret scan failed: %v", serr)
			}
			if len(findings) > 0 {
				var b strings.Builder
				fmt.Fprintf(&b, "secret detected in an add_labels value — refusing to write it to the %s tier (it can push to a remote):", snap.Tier)
				for _, f := range findings {
					fmt.Fprintf(&b, "\n  %s: %s", f.RuleID, f.Description)
				}
				return metaPlan{}, toolError("%s", b.String())
			}
		}
	}
	for _, la := range p.AddLinks {
		to, err := st.Resolve(la.To)
		if err != nil {
			return metaPlan{}, toolError("add_links: %v", err)
		}
		typ := la.Type
		if typ == "" {
			typ = "relates"
		}
		leak, lerr := st.LinkWouldLeak(snap.ID, to)
		if lerr != nil {
			return metaPlan{}, toolError("add_links: %v", lerr)
		}
		if leak {
			plan.warnings = append(plan.warnings,
				fmt.Sprintf("warning: link to %s exposes a more-private id on push", to))
		}
		plan.addLinks = append(plan.addLinks, resolvedLink{to: to.String(), typ: typ})
	}
	for _, rl := range p.RemoveLinks {
		to, err := st.Resolve(rl)
		if err != nil {
			return metaPlan{}, toolError("remove_links: %v", err)
		}
		plan.removeLinks = append(plan.removeLinks, to.String())
	}
	return plan, nil
}

// applyMeta writes the validated plan to the live entry and returns a one-line
// summary of what changed (empty when the plan is empty).
func applyMeta(st *store.Store, id entity.Id, plan metaPlan) (string, error) {
	for _, l := range plan.addLabels {
		if err := st.AddLabel(id, l); err != nil {
			return "", err
		}
	}
	for _, l := range plan.removeLabels {
		if err := st.RemoveLabel(id, l); err != nil {
			return "", err
		}
	}
	for _, l := range plan.addLinks {
		if err := st.AddLink(id, l.to, l.typ); err != nil {
			return "", err
		}
	}
	for _, to := range plan.removeLinks {
		if err := st.RemoveLink(id, to); err != nil {
			return "", err
		}
	}
	var segs []string
	if n := len(plan.addLabels); n > 0 {
		segs = append(segs, fmt.Sprintf("+%d label(s)", n))
	}
	if n := len(plan.removeLabels); n > 0 {
		segs = append(segs, fmt.Sprintf("-%d label(s)", n))
	}
	if n := len(plan.addLinks); n > 0 {
		segs = append(segs, fmt.Sprintf("+%d link(s)", n))
	}
	if n := len(plan.removeLinks); n > 0 {
		segs = append(segs, fmt.Sprintf("-%d link(s)", n))
	}
	if len(segs) == 0 {
		return "", nil
	}
	return " (" + strings.Join(segs, ", ") + ")", nil
}

func update(dp dirPolicy) mcp.ToolHandlerFor[updateParams, any] {
	return func(ctx context.Context, req *mcp.CallToolRequest, p updateParams) (*mcp.CallToolResult, any, error) {
		effDir, restricted, derr := dp.resolve(ctx, req.Session, p.Dir)
		if derr != nil {
			return toolError("%v", derr), nil, nil
		}
		st, err := store.Open(effDir)
		if err != nil {
			return nil, nil, err
		}
		defer st.Close()
		id, err := st.Resolve(p.ID)
		if err != nil {
			return toolError("%v", err), nil, nil
		}
		snap, err := st.Get(id)
		if err != nil {
			return toolError("%v", err), nil, nil
		}
		if restricted && isPrivateTyped(snap.TierType) {
			return toolError("%s", restrictedTierMsg), nil, nil
		}

		hasBody := p.Body != ""
		// Validate metadata first (no writes): resolve link targets and scan
		// add_labels. A hard body reject below applies no metadata (atomic); a
		// parked body still applies it (metadata is not secret-bearing content).
		plan, verr := validateMeta(st, snap, p)
		if verr != nil {
			return verr, nil, nil
		}
		if !hasBody && plan.empty() {
			return toolError("nothing to update: provide body and/or add_labels/remove_labels/add_links/remove_links"), nil, nil
		}

		bodyMsg := ""
		if hasBody {
			// CAS (spec §8 step 3): a full-body todo write must declare the version
			// it read (from kref_get / kref_recall), closing the clobber hole that
			// dropped versions on the todo. Required for a todo; ignored otherwise.
			if snap.Kind == todoguard.TodoKind {
				if p.IfVersion == nil {
					return toolError("kref_update requires if_version for a todo entry: pass the version from kref_get/kref_recall (the entry is at v%d)", snap.Version), nil, nil
				}
				if cerr := todoguard.CheckVersion(snap.Kind, *p.IfVersion, snap.Version); cerr != nil {
					path, werr := todoguard.WriteRejected(id.String(), p.Body)
					if werr != nil {
						return toolError("%v (could not save recovery file: %v)", cerr, werr), nil, nil
					}
					return toolError("%v\nthe rejected body was saved to %s", cerr, path), nil, nil
				}
			}
			unscanned, cerr := entryguard.Check(snap, p.Body, false)
			var refused *entryguard.RefusedError
			if errors.As(cerr, &refused) {
				parked, perr := st.QuarantineUpdate(id, p.Body, snap.Version, refused.Findings, "agent")
				if perr != nil {
					return toolError("quarantine failed: %v", perr), nil, nil
				}
				// Body held for review; metadata still applies to the live entry.
				bodyMsg = quarantineText(parked)
			} else if cerr != nil {
				return toolError("%v", cerr), nil, nil
			} else {
				body, rejected := guardTodo(id.String(), snap.Kind, p.Body)
				if rejected != nil {
					return rejected, nil, nil // malformed todo: hard reject, no metadata
				}
				if err := st.Update(id, body, p.Title); err != nil {
					return nil, nil, err
				}
				bodyMsg = "updated " + id.String()
				if unscanned {
					bodyMsg += unscannedNote
				}
			}
		}

		metaMsg, err := applyMeta(st, id, plan)
		if err != nil {
			return nil, nil, err
		}
		if bodyMsg == "" {
			bodyMsg = "updated " + id.String()
		}
		out := bodyMsg + metaMsg
		if len(plan.warnings) > 0 {
			out += "\n" + strings.Join(plan.warnings, "\n")
		}
		return textResult(out), nil, nil
	}
}

type patchParams struct {
	dirParam
	ID   string `json:"id"`
	Diff string `json:"diff"`
}

func patchTool(dp dirPolicy) mcp.ToolHandlerFor[patchParams, any] {
	return func(ctx context.Context, req *mcp.CallToolRequest, p patchParams) (*mcp.CallToolResult, any, error) {
		effDir, restricted, derr := dp.resolve(ctx, req.Session, p.Dir)
		if derr != nil {
			return toolError("%v", derr), nil, nil
		}
		st, err := store.Open(effDir)
		if err != nil {
			return nil, nil, err
		}
		defer st.Close()
		id, err := st.Resolve(p.ID)
		if err != nil {
			return toolError("%v", err), nil, nil
		}
		snap, err := st.Get(id)
		if err != nil {
			return toolError("%v", err), nil, nil
		}
		if restricted && isPrivateTyped(snap.TierType) {
			return toolError("%s", restrictedTierMsg), nil, nil
		}
		newBody, err := textpatch.Apply(snap.Body, p.Diff)
		if err != nil {
			return toolError("%v", err), nil, nil
		}
		if strings.TrimSpace(newBody) == "" {
			return toolError("patch would leave the body empty — use kref_update if that is intended"), nil, nil
		}
		unscanned, done := parkEntry(snap, newBody, func(f []scan.Finding) (store.Parked, error) {
			return st.QuarantineUpdate(id, newBody, snap.Version, f, "agent")
		})
		if done != nil {
			return done, nil, nil
		}
		guarded, rejected := guardTodo(id.String(), snap.Kind, newBody)
		if rejected != nil {
			return rejected, nil, nil
		}
		if err := st.Update(id, guarded, ""); err != nil {
			return nil, nil, err
		}
		versions, err := st.BodyVersions(id)
		if err != nil {
			return nil, nil, err
		}
		msg := fmt.Sprintf("patched %s to version %d", id.String(), len(versions))
		if unscanned {
			msg += unscannedNote
		}
		return textResult(msg), nil, nil
	}
}

type lifecycleParams struct {
	dirParam
	ID     string `json:"id"`
	Action string `json:"action"`
	Status string `json:"status,omitempty"`
}

func lifecycle(dp dirPolicy) mcp.ToolHandlerFor[lifecycleParams, any] {
	return func(ctx context.Context, req *mcp.CallToolRequest, p lifecycleParams) (*mcp.CallToolResult, any, error) {
		effDir, restricted, derr := dp.resolve(ctx, req.Session, p.Dir)
		if derr != nil {
			return toolError("%v", derr), nil, nil
		}
		st, err := store.Open(effDir)
		if err != nil {
			return nil, nil, err
		}
		defer st.Close()
		id, err := st.Resolve(p.ID)
		if err != nil {
			return toolError("%v", err), nil, nil
		}
		if restricted {
			snap, err := st.Get(id)
			if err != nil {
				return toolError("%v", err), nil, nil
			}
			if isPrivateTyped(snap.TierType) {
				return toolError("%s", restrictedTierMsg), nil, nil
			}
		}
		var opErr error
		switch p.Action {
		case "set_status":
			valid := slices.Contains(entry.Statuses, p.Status)
			if !valid {
				return toolError("set_status requires status to be one of %s (got %q)",
					strings.Join(entry.Statuses, "|"), p.Status), nil, nil
			}
			opErr = st.SetStatus(id, p.Status)
		case "delete":
			opErr = st.Tombstone(id)
		case "restore":
			opErr = st.Restore(id)
		case "archive":
			opErr = st.Archive(id)
		case "unarchive":
			opErr = st.Unarchive(id)
		default:
			return toolError("unknown action %q (want set_status|delete|restore|archive|unarchive)", p.Action), nil, nil
		}
		if opErr != nil {
			return toolError("%v", opErr), nil, nil
		}
		if p.Action == "set_status" {
			return textResult(fmt.Sprintf("set %s to %s", id.String(), p.Status)), nil, nil
		}
		return textResult(fmt.Sprintf("%s applied to %s", p.Action, id.String())), nil, nil
	}
}

type quarantineParams struct {
	dirParam
	Action string `json:"action"`       // "list" | "show"
	ID     string `json:"id,omitempty"` // required for show
}

// quarantineRead serves the read-only review queue to an agent. It never returns
// the parked content or a finding's secret value — approve/reject stay a human
// decision at the CLI — so an agent gains situational awareness (what entry/op is
// held, and which rule flagged it) without the secret leaking through MCP.
func quarantineRead(dp dirPolicy) mcp.ToolHandlerFor[quarantineParams, any] {
	return func(ctx context.Context, req *mcp.CallToolRequest, p quarantineParams) (*mcp.CallToolResult, any, error) {
		effDir, restricted, derr := dp.resolve(ctx, req.Session, p.Dir)
		if derr != nil {
			return toolError("%v", derr), nil, nil
		}
		if restricted {
			return toolError("the review queue is not served by a global/client-roots MCP server"), nil, nil
		}
		st, err := store.Open(effDir)
		if err != nil {
			return nil, nil, err
		}
		defer st.Close()
		switch p.Action {
		case "list":
			items, qerr := st.QuarantineQueue()
			if qerr != nil {
				return toolError("%v", qerr), nil, nil
			}
			return textResult(formatQuarantineQueue(items)), nil, nil
		case "show":
			if p.ID == "" {
				return toolError("show requires id (the quarantine item)"), nil, nil
			}
			id, rerr := st.Resolve(p.ID)
			if rerr != nil {
				return toolError("%v", rerr), nil, nil
			}
			d, derr := st.QuarantineDetail(id)
			if derr != nil {
				return toolError("%v", derr), nil, nil
			}
			return textResult(formatQuarantineDetail(d)), nil, nil
		default:
			return toolError("unknown action %q (want list|show); approve/reject are human-only at the CLI", p.Action), nil, nil
		}
	}
}

func formatFindings(w *strings.Builder, findings []scan.Finding) {
	for _, f := range findings {
		fmt.Fprintf(w, "  finding: %s (line %d)", f.RuleID, f.StartLine)
		if f.Description != "" {
			fmt.Fprintf(w, " — %s", f.Description)
		}
		w.WriteString("\n")
	}
}

func formatQuarantineQueue(items []store.QuarantineItem) string {
	if len(items) == 0 {
		return "no writes awaiting review."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d write(s) awaiting HUMAN review (approve/reject is a CLI/human action):\n", len(items))
	for _, it := range items {
		if it.HeldOp {
			fmt.Fprintf(&b, "%s  held %s → %s", it.ID, it.OpKind, it.Target)
			if it.TargetTitle != "" {
				fmt.Fprintf(&b, " %q", it.TargetTitle)
			}
		} else {
			fmt.Fprintf(&b, "%s  new %s → %s %q", it.ID, it.Kind, it.DestTier, it.Title)
		}
		if len(it.Findings) > 0 {
			fmt.Fprintf(&b, "  [%s]", it.Findings[0].RuleID)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func formatQuarantineDetail(d store.QuarantineDetail) string {
	var b strings.Builder
	it := d.Item
	if it.HeldOp {
		fmt.Fprintf(&b, "held %s → %s", it.OpKind, it.Target)
		if it.TargetTitle != "" {
			fmt.Fprintf(&b, " %q", it.TargetTitle)
		}
		b.WriteString("\n")
	} else {
		fmt.Fprintf(&b, "new %s draft → %s %q\n", it.Kind, it.DestTier, it.Title)
	}
	formatFindings(&b, it.Findings)
	b.WriteString("\nproposed content: withheld (it contains the flagged secret). " +
		"A human reviews and approves/rejects it at the CLI (kref quarantine approve|reject); agents cannot approve.")
	return b.String()
}

// unscannedNote is appended to a comment write's success message when the secret
// scanner was unavailable, so the caller knows the body went in unscanned.
const unscannedNote = " (stored UNSCANNED: betterleaks unavailable)"

// quarantinedResult renders the non-error MCP result for a parked write: the
// write was held for human review, not applied and not lost. An agent should
// discuss it on the entry's review thread rather than treat it as a failure.
func quarantineText(p store.Parked) string {
	return fmt.Sprintf(
		"quarantined as %s (%d findings) — held for human review, not applied and "+
			"not lost. Discuss on the entry's review thread; a human approves it via "+
			"kref quarantine (see kref quarantine show %s).", p.ItemID, len(p.Findings), p.ItemID)
}

func quarantinedResult(p store.Parked) *mcp.CallToolResult {
	return textResult(quarantineText(p))
}

// parkEntry runs the entry-body guard; on a finding it DIVERTS the write into
// the quarantine review queue via parkFn (which performs the store-side park)
// and returns a non-error "quarantined" result. A clean body returns
// (unscanned, nil) and the caller proceeds with the real write.
func parkEntry(snap *entry.Snapshot, body string, parkFn func([]scan.Finding) (store.Parked, error)) (unscanned bool, done *mcp.CallToolResult) {
	unscanned, err := entryguard.Check(snap, body, false)
	var refused *entryguard.RefusedError
	if errors.As(err, &refused) {
		parked, perr := parkFn(refused.Findings)
		if perr != nil {
			return false, toolError("quarantine failed: %v", perr)
		}
		return false, quarantinedResult(parked)
	}
	if err != nil {
		return false, toolError("%v", err)
	}
	return unscanned, nil
}

// parkComment is parkEntry for a comment body (add, edit, and resolve-note): a
// flagged body is diverted into the quarantine review queue via parkFn. MCP
// never forces — approval stays a human decision at the CLI.
func parkComment(snap *entry.Snapshot, body string, parkFn func([]scan.Finding) (store.Parked, error)) (unscanned bool, done *mcp.CallToolResult) {
	unscanned, err := commentguard.Check(snap, body, false)
	var refused *commentguard.RefusedError
	if errors.As(err, &refused) {
		parked, perr := parkFn(refused.Findings)
		if perr != nil {
			return false, toolError("quarantine failed: %v", perr)
		}
		return false, quarantinedResult(parked)
	}
	if err != nil {
		return false, toolError("%v", err)
	}
	return unscanned, nil
}

type commentParams struct {
	dirParam
	ID       string `json:"id"`
	Action   string `json:"action"`
	Body     string `json:"body,omitempty"`
	Target   string `json:"target,omitempty"`
	Question bool   `json:"question,omitempty"`
	ReplyTo  string `json:"reply_to,omitempty"`
	Note     string `json:"note,omitempty"`
}

func comment(dp dirPolicy) mcp.ToolHandlerFor[commentParams, any] {
	return func(ctx context.Context, req *mcp.CallToolRequest, p commentParams) (*mcp.CallToolResult, any, error) {
		effDir, restricted, derr := dp.resolve(ctx, req.Session, p.Dir)
		if derr != nil {
			return toolError("%v", derr), nil, nil
		}
		st, err := store.Open(effDir)
		if err != nil {
			return nil, nil, err
		}
		defer st.Close()
		id, err := st.Resolve(p.ID)
		if err != nil {
			return toolError("%v", err), nil, nil
		}
		snap, err := st.Get(id)
		if err != nil {
			return toolError("%v", err), nil, nil
		}
		if restricted && isPrivateTyped(snap.TierType) {
			return toolError("%s", restrictedTierMsg), nil, nil
		}
		switch p.Action {
		case "add":
			if strings.TrimSpace(p.Body) == "" {
				return toolError("add requires a non-empty body"), nil, nil
			}
			unscanned, done := parkComment(snap, p.Body, func(f []scan.Finding) (store.Parked, error) {
				return st.QuarantineComment(id, p.Body, p.Question, p.ReplyTo, f, "agent")
			})
			if done != nil {
				return done, nil, nil
			}
			cid, err := st.AddComment(id, "agent", p.Body, p.Question, p.ReplyTo)
			if err != nil {
				return toolError("%v", err), nil, nil
			}
			msg := fmt.Sprintf("added comment %s to %s", cid, id.String())
			if unscanned {
				msg += unscannedNote
			}
			return textResult(msg), nil, nil
		case "edit":
			if p.Target == "" {
				return toolError("edit requires target (the comment id to edit)"), nil, nil
			}
			if strings.TrimSpace(p.Body) == "" {
				return toolError("edit requires a non-empty body"), nil, nil
			}
			unscanned, done := parkComment(snap, p.Body, func(f []scan.Finding) (store.Parked, error) {
				return st.QuarantineEditComment(id, p.Target, p.Body, f, "agent")
			})
			if done != nil {
				return done, nil, nil
			}
			if err := st.EditComment(id, p.Target, p.Body); err != nil {
				return toolError("%v", err), nil, nil
			}
			msg := "edited comment " + p.Target
			if unscanned {
				msg += unscannedNote
			}
			return textResult(msg), nil, nil
		case "resolve":
			if p.Target == "" {
				return toolError("resolve requires target (the question comment id to resolve)"), nil, nil
			}
			unscanned := false
			if strings.TrimSpace(p.Note) != "" {
				u, done := parkComment(snap, p.Note, func(f []scan.Finding) (store.Parked, error) {
					return st.QuarantineResolveNote(id, p.Target, p.Note, f, "agent")
				})
				if done != nil {
					return done, nil, nil
				}
				unscanned = u
				if _, err := st.AddComment(id, "agent", p.Note, false, p.Target); err != nil {
					return toolError("%v", err), nil, nil
				}
			}
			if err := st.ResolveComment(id, p.Target); err != nil {
				return toolError("%v", err), nil, nil
			}
			msg := "resolved comment " + p.Target
			if unscanned {
				msg += unscannedNote
			}
			return textResult(msg), nil, nil
		case "delete":
			if p.Target == "" {
				return toolError("delete requires target (the comment id to delete)"), nil, nil
			}
			if err := st.DeleteComment(id, p.Target); err != nil {
				return toolError("%v", err), nil, nil
			}
			return textResult("deleted comment " + p.Target), nil, nil
		default:
			return toolError("unknown action %q (want add|resolve|edit|delete)", p.Action), nil, nil
		}
	}
}

type supersedeParams struct {
	dirParam
	Old string `json:"old"`
	New string `json:"new"`
}

func supersede(dp dirPolicy) mcp.ToolHandlerFor[supersedeParams, any] {
	return func(ctx context.Context, req *mcp.CallToolRequest, p supersedeParams) (*mcp.CallToolResult, any, error) {
		effDir, restricted, derr := dp.resolve(ctx, req.Session, p.Dir)
		if derr != nil {
			return toolError("%v", derr), nil, nil
		}
		st, err := store.Open(effDir)
		if err != nil {
			return nil, nil, err
		}
		defer st.Close()
		oldID, err := st.Resolve(p.Old)
		if err != nil {
			return toolError("old: %v", err), nil, nil
		}
		newID, err := st.Resolve(p.New)
		if err != nil {
			return toolError("new: %v", err), nil, nil
		}
		if restricted {
			for _, id := range []entity.Id{oldID, newID} {
				snap, err := st.Get(id)
				if err != nil {
					return toolError("%v", err), nil, nil
				}
				if isPrivateTyped(snap.TierType) {
					return toolError("%s", restrictedTierMsg), nil, nil
				}
			}
		}
		if err := st.Supersede(oldID, newID); err != nil {
			return toolError("%v", err), nil, nil
		}
		return textResult("superseded " + oldID.String() + " by " + newID.String()), nil, nil
	}
}
