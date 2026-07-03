package bridge

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/git-bug/git-bug/entity"
	"github.com/pmezard/go-difflib/difflib"

	"github.com/trevor-vaughan/kref/internal/content"
	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/scan"
	"github.com/trevor-vaughan/kref/internal/store"
)

// markerRe matches a kref-id trailer: an HTML comment carrying a 64-hex id,
// alone on a line.
var markerRe = regexp.MustCompile(`(?m)^[ \t]*<!--[ \t]*kref-id:[ \t]*([0-9a-fA-F]{64})[ \t]*-->[ \t]*$`)

// SplitMarker returns the last well-formed kref-id in content (or "" if none) and
// the body with ONLY that trailer line removed — text before and after the
// trailer is preserved and stitched together, so content appended after the
// trailer is not silently dropped.
func SplitMarker(content []byte) (string, []byte) {
	locs := markerRe.FindAllSubmatchIndex(content, -1)
	if len(locs) == 0 {
		return "", content
	}
	last := locs[len(locs)-1]
	id := string(content[last[2]:last[3]])

	before := bytes.TrimRight(content[:last[0]], "\n")
	after := bytes.TrimLeft(content[last[1]:], "\n")

	var body []byte
	switch {
	case len(before) > 0 && len(after) > 0:
		body = append(append(append([]byte{}, before...), '\n'), after...)
	case len(before) > 0:
		body = append([]byte{}, before...)
	default:
		body = append([]byte{}, after...)
	}
	body = bytes.TrimRight(body, "\n")
	if len(body) > 0 {
		body = append(body, '\n')
	}
	return id, body
}

// IDFromFile reads the kref-id trailer from a markdown file and returns the entry
// id it points to. Used to address an entry by the file it was ingested from.
func IDFromFile(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	id, _ := SplitMarker(content)
	if id == "" {
		return "", fmt.Errorf("no kref-id trailer in %s", path)
	}
	return id, nil
}

// withMarker returns body with a kref-id trailer appended (blank line + comment).
func withMarker(body []byte, id string) []byte {
	out := bytes.TrimRight(body, "\n")
	return append(out, []byte("\n\n<!-- kref-id: "+id+" -->\n")...)
}

// IngestResult reports the outcome of ingesting one file.
type IngestResult struct {
	Path        string    `json:"path"`
	ID          entity.Id `json:"id"`
	Title       string    `json:"title"`
	Tier        string    `json:"tier"`
	TierType    string    `json:"tier_type"`
	Quarantined bool      `json:"quarantined"`
	Action      string    `json:"action"` // created | updated | unchanged | quarantined | error
	ContentType string    `json:"content_type,omitempty"`
	Unscanned   bool      `json:"unscanned,omitempty"` // scanner unavailable; content stored without a secret scan
	Error       string    `json:"error,omitempty"`
}

// titleFromMarkdown returns the first ATX H1, else the file base name.
func titleFromMarkdown(content []byte, path string) string {
	sc := bufio.NewScanner(bytes.NewReader(content))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(line[2:])
		}
	}
	return strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
}

// Ingest reads a markdown file, scans it, and stores it as an entry of the given kind.
// A kref-id trailer makes re-ingest idempotent: an unchanged file is a no-op, a
// changed file updates the same entry. On a secret hit an UNMARKED file is
// quarantined to private; a MARKED file fails closed (D3) so a secret never
// reaches the syncable tier its entry already lives in.
// actor and actorKind identify who triggered the ingest (passed through to RecordOrigin).
// kind is the entry kind to set on create, and to re-kind to when a re-ingest
// finds the stored entry has a different kind. An empty kind means "unspecified":
// on create it falls back to "document"; on re-ingest it leaves the stored kind
// untouched (so the kind-less post-commit hook does not revert a deliberate kind).
func Ingest(s *store.Store, path string, tier entry.Tier, kind, actor, actorKind string) (IngestResult, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return IngestResult{}, err
	}
	markerID, rawBody := SplitMarker(raw)
	// Canonicalize: trailing newlines are not semantically meaningful for the
	// stored body, and SplitMarker normalizes them only when a marker is
	// present. Trim uniformly so create-time and re-ingest-time bodies compare
	// equal (otherwise a marker-less file with no trailing newline would report
	// a spurious "updated" on its first re-ingest).
	body := bytes.TrimRight(rawBody, "\n")

	ctype, err := content.Detect(path, body)
	if err != nil {
		return IngestResult{}, fmt.Errorf("%s: %w", path, err)
	}
	markdown := content.IsMarkdown(ctype)

	findings, err := scan.Scan(body)
	unscanned := false
	if errors.Is(err, scan.ErrMissing) {
		// Warn-not-fail policy: without a scanner there is nothing to gate on,
		// so the content is stored as requested and the result is flagged so
		// the summary can warn loudly. The push boundary stays fail-closed.
		unscanned, err = true, nil
		findings = nil
	}
	if err != nil {
		return IngestResult{}, err
	}
	secret := len(findings) > 0

	// Resolve a marker to its stored entry up front: both the fail-closed secret
	// guard and the update path below need to know the entry's tier.
	var existing *entry.Snapshot
	if markerID != "" {
		if snap, gErr := s.Get(entity.Id(markerID)); gErr == nil {
			existing = snap
		}
	}

	// Fail-closed (D3): a secret in a marked file must not be written into — or
	// kept headed for — a tier that can push. A file already quarantined into the
	// private tier is the exception: private structurally cannot have a remote, so
	// re-confirming it pushes nothing and must stay re-runnable (an unchanged file
	// is a no-op; an edited one updates the still-private entry). A marker pointing
	// at a purged entry also fails closed — we cannot prove where the secret lands.
	if secret && markerID != "" && (existing == nil || existing.Tier != string(entry.TierPrivate)) {
		return IngestResult{}, fmt.Errorf(
			"secret detected in %s, which maps to entry %s — refusing to update "+
				"(would push the secret to that tier's remote); rotate the secret "+
				"and `kref purge %s --gc`, then re-ingest", path, markerID, markerID)
	}

	title := titleFromMarkdown(body, path)

	// Update path: a marker that still resolves to a stored entry.
	if existing != nil {
		id := existing.ID
		kindChanged := kind != "" && kind != existing.Kind
		action := "unchanged"
		if string(body) != existing.Body || title != existing.Title || kindChanged {
			if uErr := s.Update(id, string(body), title); uErr != nil {
				return IngestResult{}, uErr
			}
			if kindChanged {
				if kErr := s.SetKind(id, kind); kErr != nil {
					return IngestResult{}, kErr
				}
			}
			action = "updated"
		}
		// Report the freshly-derived title: on "updated" it is the new
		// title we just stored; on "unchanged" it equals existing.Title.
		if action == "updated" {
			if err := s.RecordOrigin(id, actor, actorKind, s.RepoRelative(path), "ingest"); err != nil {
				return IngestResult{}, err
			}
		}
		return IngestResult{Path: path, ID: id, Title: title, Tier: existing.Tier, TierType: existing.TierType, Action: action, ContentType: existing.ContentType, Unscanned: unscanned}, nil
	}
	// marker present but entry gone (purged): fall through to create.

	// Create path. An unspecified kind defaults to "document".
	k := kind
	if k == "" {
		k = "document"
	}
	action := "created"
	if secret {
		tier = entry.TierPrivate
		action = "quarantined"
	}
	id, err := s.AddWithContentType(tier, k, title, string(body), ctype)
	if err != nil {
		return IngestResult{}, err
	}
	if markdown {
		if err := os.WriteFile(path, withMarker(body, id.String()), 0o644); err != nil {
			return IngestResult{}, fmt.Errorf("stamp kref-id marker into %s: %w", path, err)
		}
	}
	if err := s.RecordOrigin(id, actor, actorKind, s.RepoRelative(path), "ingest"); err != nil {
		return IngestResult{}, err
	}
	return IngestResult{Path: path, ID: id, Title: title, Tier: string(tier), TierType: string(s.TierType(tier)), Quarantined: secret, Action: action, ContentType: ctype, Unscanned: unscanned}, nil
}

// IngestPaths ingests each path (a file, or a directory walked recursively for
// *.md, skipping .git and .kref). A missing path errors unless skipMissing, in
// which case it is omitted. Per-file failures (e.g. the fail-closed secret
// guard) become an "error" result rather than aborting the batch; the caller
// decides the exit code from the presence of any error result.
// actor and actorKind are forwarded to each Ingest call for provenance recording.
func IngestPaths(s *store.Store, paths []string, tier entry.Tier, kind string, skipMissing bool, actor, actorKind string) ([]IngestResult, error) {
	var files []string
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			if os.IsNotExist(err) && skipMissing {
				continue
			}
			return nil, err
		}
		if !info.IsDir() {
			files = append(files, p)
			continue
		}
		walkErr := filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if name := d.Name(); name == ".git" || name == ".kref" {
					return fs.SkipDir
				}
				return nil
			}
			if filepath.Ext(path) == ".md" {
				files = append(files, path)
			}
			return nil
		})
		if walkErr != nil {
			return nil, walkErr
		}
	}

	results := make([]IngestResult, 0, len(files))
	for _, f := range files {
		res, err := Ingest(s, f, tier, kind, actor, actorKind)
		if err != nil {
			results = append(results, IngestResult{Path: f, Action: "error", Error: err.Error()})
			continue
		}
		results = append(results, res)
	}
	return results, nil
}

// EnsureKrefIgnored makes sure the .kref/ working-tree directory is ignored,
// using the repo's .git/info/exclude rather than a committed .gitignore. The
// ignore is an under-the-hood mechanism: it stays out of the tracked tree (and
// out of git log/blame), matching kref's purpose of not cluttering the repo.
// It is local to each clone (info/exclude does not travel), so callers that
// materialize .kref/ files re-invoke this per machine. Idempotent.
func EnsureKrefIgnored(dir string) error {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--git-path", "info/exclude").Output()
	if err != nil {
		return fmt.Errorf("locate git info/exclude under %s: %w", dir, err)
	}
	exclude := strings.TrimSpace(string(out))
	if !filepath.IsAbs(exclude) {
		exclude = filepath.Join(dir, exclude)
	}
	existing, _ := os.ReadFile(exclude)
	sc := bufio.NewScanner(bytes.NewReader(existing))
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == ".kref/" {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(exclude, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString("\n# kref knowledge store (synced via git refs, not the working tree)\n.kref/\n"); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// AnchorForTracking returns the working-copy path to ingest and track for src.
// A path inside the repo is returned unchanged (tracked in place). A path
// outside the repo (a "floater") is copied under <root>/.kref/<base>,
// disambiguating the filename on collision, with .kref/ ensured ignored; the
// copy's path is returned. The original file is never moved or modified.
func AnchorForTracking(s *store.Store, src string) (string, error) {
	root := s.Root()
	abs, err := filepath.Abs(src)
	if err != nil {
		return "", err
	}
	if rel, relErr := filepath.Rel(root, abs); relErr == nil &&
		rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return src, nil // already inside the repo: track it in place
	}
	// Floater: copy under .kref/, which is a disposable label location, so a
	// filename collision is resolved by picking a free name (the trailer, not
	// the path, is identity).
	if err := EnsureKrefIgnored(root); err != nil {
		return "", err
	}
	krefDir := filepath.Join(root, ".kref")
	if err := os.MkdirAll(krefDir, 0o755); err != nil {
		return "", err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	dest := uniquePath(filepath.Join(krefDir, filepath.Base(src)))
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		return "", err
	}
	return dest, nil
}

// ReconcileResult reports the outcome of reconciling one tracked entry.
type ReconcileResult struct {
	ID             entity.Id `json:"id"`
	Path           string    `json:"path"` // repo-relative tracked path
	Title          string    `json:"title"`
	Action         string    `json:"action"` // synced | unchanged | relocated | missing | ambiguous | error
	TrailerMissing bool      `json:"trailer_missing,omitempty"`
	Forced         bool      `json:"forced,omitempty"`    // a secret finding was overridden via force
	Unscanned      bool      `json:"unscanned,omitempty"` // scanner unavailable; pulled without a secret scan
	Error          string    `json:"error,omitempty"`
}

// ReconcileEntry pulls the tracked file at snap.TrackedPath into entry snap.ID
// (file -> entry only; it never writes the file). It is entry-driven: the entry
// is identified by the tracking record, not the file's trailer, so a file that
// lost its trailer still updates the right entry (and the missing trailer is
// reported, not repaired). A secret in the file fails closed unless force is
// set. A missing file yields action "missing" (the caller may self-heal).
func ReconcileEntry(s *store.Store, snap *entry.Snapshot, dryRun, force bool, actor, actorKind string) (ReconcileResult, error) {
	res := ReconcileResult{ID: snap.ID, Path: snap.TrackedPath, Title: snap.Title}
	absPath := filepath.Join(s.Root(), filepath.FromSlash(snap.TrackedPath))
	raw, err := os.ReadFile(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			res.Action = "missing"
			return res, nil
		}
		return res, err
	}

	markerID, rawBody := SplitMarker(raw)
	body := bytes.TrimRight(rawBody, "\n")
	res.TrailerMissing = markerID != snap.ID.String()

	findings, err := scan.Scan(body)
	if errors.Is(err, scan.ErrMissing) {
		res.Unscanned = true
		findings, err = nil, nil
	}
	if err != nil {
		return res, err
	}
	if len(findings) > 0 && !force {
		res.Action = "error"
		res.Error = fmt.Sprintf(
			"secret detected in %s (entry %s, tier %s) — not pulled; rotate it or re-run with --force",
			snap.TrackedPath, snap.ID, snap.Tier)
		return res, nil
	}
	res.Forced = len(findings) > 0 // reachable with findings only when force overrode them

	title := titleFromMarkdown(body, absPath)
	res.Title = title
	if string(body) == snap.Body && title == snap.Title {
		res.Action = "unchanged"
		return res, nil
	}
	if !dryRun {
		if err := s.Update(snap.ID, string(body), title); err != nil {
			return res, err
		}
		if err := s.RecordOrigin(snap.ID, actor, actorKind, snap.TrackedPath, "reconcile"); err != nil {
			return res, err
		}
	}
	res.Action = "synced"
	return res, nil
}

// DriftState reports whether a tracked entry's file diverges from the entry,
// without pulling: "missing" when nothing is at TrackedPath, "in-sync" when the
// file body (trailer stripped) and derived title match the entry, else
// "drifted". Read-only; used by show and list --check.
func DriftState(s *store.Store, snap *entry.Snapshot) (string, error) {
	absPath := filepath.Join(s.Root(), filepath.FromSlash(snap.TrackedPath))
	raw, err := os.ReadFile(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "missing", nil
		}
		return "", err
	}
	_, rawBody := SplitMarker(raw)
	body := bytes.TrimRight(rawBody, "\n")
	title := titleFromMarkdown(body, absPath)
	if string(body) == snap.Body && title == snap.Title {
		return "in-sync", nil
	}
	return "drifted", nil
}

// BuildTrailerIndex walks root for *.md files (including .kref/, skipping .git),
// reads each kref-id trailer, and maps entry id -> repo-relative paths. Files
// without a trailer are skipped; an id mapping to more than one path means the
// file was copied. Used to self-heal a moved tracked file during reconcile.
func BuildTrailerIndex(root string) (map[string][]string, error) {
	index := map[string][]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".md" {
			return nil
		}
		raw, rErr := os.ReadFile(path)
		if rErr != nil {
			return rErr
		}
		id, _ := SplitMarker(raw)
		if id == "" {
			return nil
		}
		rel, rErr := filepath.Rel(root, path)
		if rErr != nil {
			return rErr
		}
		index[id] = append(index[id], filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return index, nil
}

// Reconcile reconciles one tracked entry, self-healing a moved file. If the
// tracked file is present it behaves like ReconcileEntry. If it is missing, the
// trailer index locates the entry's file: exactly one match re-points
// TrackedPath (Store.Track) and reconciles from there (action "relocated");
// several matches yield "ambiguous"; none yields "missing". Build index with
// BuildTrailerIndex (lazily — only needed when a tracked file is missing).
func Reconcile(s *store.Store, snap *entry.Snapshot, index map[string][]string, dryRun, force bool, actor, actorKind string) (ReconcileResult, error) {
	absPath := filepath.Join(s.Root(), filepath.FromSlash(snap.TrackedPath))
	if _, err := os.Stat(absPath); err == nil {
		return ReconcileEntry(s, snap, dryRun, force, actor, actorKind)
	} else if !os.IsNotExist(err) {
		return ReconcileResult{ID: snap.ID, Path: snap.TrackedPath, Title: snap.Title}, err
	}

	res := ReconcileResult{ID: snap.ID, Path: snap.TrackedPath, Title: snap.Title}
	paths := index[snap.ID.String()]
	switch len(paths) {
	case 0:
		res.Action = "missing"
		return res, nil
	case 1:
		newPath := paths[0]
		if dryRun {
			res.Action = "relocated"
			res.Path = newPath
			return res, nil
		}
		if err := s.Track(snap.ID, newPath); err != nil {
			return res, err
		}
		updated, err := s.Get(snap.ID)
		if err != nil {
			return res, err
		}
		r, err := ReconcileEntry(s, updated, dryRun, force, actor, actorKind)
		if err != nil {
			return r, err
		}
		// The move is the headline; only relabel a clean pull (keep an error
		// such as a secret fail-closed as-is).
		if r.Action == "synced" || r.Action == "unchanged" {
			r.Action = "relocated"
		}
		r.Path = newPath
		return r, nil
	default:
		res.Action = "ambiguous"
		res.Error = fmt.Sprintf("entry %s maps to %d files: %s", snap.ID, len(paths), strings.Join(paths, ", "))
		return res, nil
	}
}

// WriteBackResult reports the outcome of writing one tracked entry back to its
// file (entry → file).
type WriteBackResult struct {
	ID     entity.Id `json:"id"`
	Path   string    `json:"path"` // repo-relative tracked path
	Title  string    `json:"title"`
	Action string    `json:"action"` // in-sync | written | missing | diverged | forced
	Diff   string    `json:"diff,omitempty"`
}

// WriteBack pushes entry snap.ID's body out to its tracked file (entry → file).
// It writes only when the file is a safe fast-forward — its current body is a
// past version of the entry (F ∈ BodyVersions), so the file has no unsynced
// edits — or, with force, when the file has diverged. A diverged file (its body
// matches no past entry version) without force is reported with a unified
// entry↔file diff and left untouched. A missing file is reported and skipped (no
// re-create). dryRun computes the action and any diff without writing. No secret
// scan: the body is already stored and the write touches only local disk.
func WriteBack(s *store.Store, snap *entry.Snapshot, dryRun, force bool, actor, actorKind string) (WriteBackResult, error) {
	res := WriteBackResult{ID: snap.ID, Path: snap.TrackedPath, Title: snap.Title}
	absPath := filepath.Join(s.Root(), filepath.FromSlash(snap.TrackedPath))
	raw, err := os.ReadFile(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			res.Action = "missing"
			return res, nil
		}
		return res, err
	}

	_, rawBody := SplitMarker(raw)
	fileBody := string(bytes.TrimRight(rawBody, "\n"))
	if fileBody == snap.Body { // snap.Body is already trailing-newline-trimmed at store time
		res.Action = "in-sync"
		return res, nil
	}

	versions, err := s.BodyVersions(snap.ID)
	if err != nil {
		return res, err
	}
	inHistory := false
	for _, v := range versions {
		if strings.TrimRight(v.Body, "\n") == fileBody {
			inHistory = true
			break
		}
	}

	writeBack := func() error {
		if dryRun {
			return nil
		}
		if err := os.WriteFile(absPath, withMarker([]byte(snap.Body), snap.ID.String()), 0o644); err != nil {
			return err
		}
		return s.RecordOrigin(snap.ID, actor, actorKind, snap.TrackedPath, "writeback")
	}

	if inHistory {
		res.Action = "written"
		return res, writeBack()
	}

	// Diverged: the file holds content no past entry version had.
	diff, err := unifiedDiff(snap.Body, fileBody)
	if err != nil {
		return res, err
	}
	res.Diff = diff
	if force {
		res.Action = "forced"
		return res, writeBack()
	}
	res.Action = "diverged"
	return res, nil
}

// unifiedDiff renders a unified diff of the entry head vs the file body. It lives
// here (not in render, which is pure stdlib+entry) because SyncEntry produces it.
func unifiedDiff(entryBody, fileBody string) (string, error) {
	return difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(entryBody),
		B:        difflib.SplitLines(fileBody),
		FromFile: "entry",
		ToFile:   "file",
		Context:  3,
	})
}

// uniquePath returns p if no file exists there, else inserts -2, -3, ... before
// the extension until it finds a free path.
func uniquePath(p string) string {
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return p
	}
	ext := filepath.Ext(p)
	stem := strings.TrimSuffix(p, ext)
	for i := 2; ; i++ {
		cand := fmt.Sprintf("%s-%d%s", stem, i, ext)
		if _, err := os.Stat(cand); os.IsNotExist(err) {
			return cand
		}
	}
}
