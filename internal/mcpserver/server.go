// Package mcpserver is a thin MCP adapter over internal/store. It exposes a
// curated set of agentic tools; the store remains the only capability core.
package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/store"
	"github.com/trevor-vaughan/kref/internal/textpatch"
)

// New builds the kref MCP server for a repository directory.
func New(dir, version string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "kref", Version: version}, nil)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "kref_remember",
		Description: "Save a new knowledge-base entry (a memory). Returns its id.",
	}, remember(dir))
	mcp.AddTool(s, &mcp.Tool{
		Name:        "kref_recall",
		Description: "Find entries by a title/body substring and optional label/tier/kind filters.",
	}, recall(dir))
	mcp.AddTool(s, &mcp.Tool{
		Name:        "kref_get",
		Description: "Read one entry in full by its id (or id prefix).",
	}, get(dir))
	mcp.AddTool(s, &mcp.Tool{
		Name:        "kref_update",
		Description: "Replace an entry's body (and optionally its title).",
	}, update(dir))
	mcp.AddTool(s, &mcp.Tool{
		Name: "kref_patch",
		Description: "Edit an entry's body by applying a unified diff. Provide " +
			"standard @@ hunks; line numbers are treated as hints only — each hunk " +
			"is located by its context/removal lines, which must match the current " +
			"body (exactly, or up to trailing whitespace). Hunks apply in order, " +
			"all-or-nothing, producing one new body version. Prefer this over " +
			"kref_update for small edits — a stale or ambiguous hunk fails loudly " +
			"instead of overwriting concurrent changes.",
	}, patchTool(dir))
	mcp.AddTool(s, &mcp.Tool{
		Name: "kref_lifecycle",
		Description: "Manage an entry's lifecycle. action is one of: set_status " +
			"(requires status: open|active|accepted|superseded|obsolete), delete " +
			"(reversible tombstone), restore (undo delete), archive (hide from " +
			"listings, status kept), unarchive. Hard-delete (purge) and tier moves " +
			"(retier) are deliberately not available to agents.",
	}, lifecycle(dir))
	mcp.AddTool(s, &mcp.Tool{
		Name:        "kref_supersede",
		Description: "Mark <old> superseded by <new> (links them and retires <old>).",
	}, supersede(dir))
	return s
}

// Serve runs the kref MCP server over stdio until the host closes the connection.
// A host ending the session by closing its end of the pipe is a clean shutdown,
// not a failure, so the resulting EOF is not propagated as an error.
func Serve(ctx context.Context, dir, version string) error {
	if err := New(dir, version).Run(ctx, &mcp.StdioTransport{}); err != nil && !gracefulClose(err) {
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

type rememberParams struct {
	Kind   string   `json:"kind,omitempty"`
	Title  string   `json:"title,omitempty"`
	Body   string   `json:"body,omitempty"`
	Tier   string   `json:"tier,omitempty"`
	Labels []string `json:"labels,omitempty"`
}

func remember(dir string) mcp.ToolHandlerFor[rememberParams, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, p rememberParams) (*mcp.CallToolResult, any, error) {
		st, err := store.Open(dir)
		if err != nil {
			return nil, nil, err
		}
		defer st.Close()

		tier := entry.TierPersonal
		if p.Tier != "" {
			d, err := st.DeclaredTier(p.Tier)
			if err != nil {
				return toolError("%v", err), nil, nil
			}
			tier = d.Name
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
		id, err := st.Add(tier, kind, title, p.Body)
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
		return textResult("remembered " + id.String()), nil, nil
	}
}

type recallParams struct {
	Search string `json:"search,omitempty"`
	Label  string `json:"label,omitempty"`
	Tier   string `json:"tier,omitempty"`
	Kind   string `json:"kind,omitempty"`
}

func recall(dir string) mcp.ToolHandlerFor[recallParams, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, p recallParams) (*mcp.CallToolResult, any, error) {
		st, err := store.Open(dir)
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
		items, err := st.List(f)
		if err != nil {
			return nil, nil, err
		}
		if len(items) == 0 {
			return textResult("no matching entries"), nil, nil
		}
		var b strings.Builder
		for _, it := range items {
			fmt.Fprintf(&b, "%s  [%s/%s]  %s\n", it.ID.String(), it.Tier, it.Status, it.Title)
		}
		return textResult(b.String()), nil, nil
	}
}

type getParams struct {
	ID string `json:"id"`
}

func get(dir string) mcp.ToolHandlerFor[getParams, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, p getParams) (*mcp.CallToolResult, any, error) {
		st, err := store.Open(dir)
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
		var b strings.Builder
		fmt.Fprintf(&b, "%s [%s/%s] %s\n", snap.ID.String(), snap.Tier, snap.Status, snap.Title)
		if len(snap.Labels) > 0 {
			fmt.Fprintf(&b, "labels: %s\n", strings.Join(snap.Labels, ", "))
		}
		fmt.Fprintf(&b, "\n%s\n", snap.Body)
		return textResult(b.String()), nil, nil
	}
}

type updateParams struct {
	ID    string `json:"id"`
	Body  string `json:"body"`
	Title string `json:"title,omitempty"`
}

func update(dir string) mcp.ToolHandlerFor[updateParams, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, p updateParams) (*mcp.CallToolResult, any, error) {
		st, err := store.Open(dir)
		if err != nil {
			return nil, nil, err
		}
		defer st.Close()
		id, err := st.Resolve(p.ID)
		if err != nil {
			return toolError("%v", err), nil, nil
		}
		if err := st.Update(id, p.Body, p.Title); err != nil {
			return nil, nil, err
		}
		return textResult("updated " + id.String()), nil, nil
	}
}

type patchParams struct {
	ID   string `json:"id"`
	Diff string `json:"diff"`
}

func patchTool(dir string) mcp.ToolHandlerFor[patchParams, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, p patchParams) (*mcp.CallToolResult, any, error) {
		st, err := store.Open(dir)
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
		newBody, err := textpatch.Apply(snap.Body, p.Diff)
		if err != nil {
			return toolError("%v", err), nil, nil
		}
		if strings.TrimSpace(newBody) == "" {
			return toolError("patch would leave the body empty — use kref_update if that is intended"), nil, nil
		}
		if err := st.Update(id, newBody, ""); err != nil {
			return nil, nil, err
		}
		versions, err := st.BodyVersions(id)
		if err != nil {
			return nil, nil, err
		}
		return textResult(fmt.Sprintf("patched %s to version %d",
			id.String(), len(versions))), nil, nil
	}
}

type lifecycleParams struct {
	ID     string `json:"id"`
	Action string `json:"action"`
	Status string `json:"status,omitempty"`
}

func lifecycle(dir string) mcp.ToolHandlerFor[lifecycleParams, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, p lifecycleParams) (*mcp.CallToolResult, any, error) {
		st, err := store.Open(dir)
		if err != nil {
			return nil, nil, err
		}
		defer st.Close()
		id, err := st.Resolve(p.ID)
		if err != nil {
			return toolError("%v", err), nil, nil
		}
		var opErr error
		switch p.Action {
		case "set_status":
			valid := false
			for _, v := range entry.Statuses {
				if p.Status == v {
					valid = true
					break
				}
			}
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

type supersedeParams struct {
	Old string `json:"old"`
	New string `json:"new"`
}

func supersede(dir string) mcp.ToolHandlerFor[supersedeParams, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, p supersedeParams) (*mcp.CallToolResult, any, error) {
		st, err := store.Open(dir)
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
		if err := st.Supersede(oldID, newID); err != nil {
			return toolError("%v", err), nil, nil
		}
		return textResult("superseded " + oldID.String() + " by " + newID.String()), nil, nil
	}
}
