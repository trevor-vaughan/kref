// Package entryguard enforces the secret policy for an entry-body write, shared
// by every agent boundary that accepts a body (the MCP kref_remember,
// kref_update, and kref_patch tools). It mirrors commentguard: a fail-closed
// check with no-work-lost recovery. Unlike a comment, an entry body is also
// scanned at the push boundary, so this write-time gate is the early warning and
// push is the backstop; keeping the same refuse-and-preserve policy means an
// agent meets one consistent rule wherever it writes.
package entryguard

import (
	"errors"
	"fmt"
	"strings"

	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/scan"
)

// RefusedError reports that an entry body was refused because it carries a
// secret and its tier is syncable. Its message names the offending finding(s) —
// rule and line — but never the secret value itself. The caller diverts the
// flagged write into the quarantine review queue, so the body is preserved there
// (not by this guard); nothing is lost.
type RefusedError struct {
	Findings []scan.Finding
	Tier     string
}

func (e *RefusedError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "secret detected in the entry body — refusing to write it to the %s tier (it can push to a remote):", e.Tier)
	for _, f := range e.Findings {
		fmt.Fprintf(&b, "\n  line %d: %s: %s", f.StartLine, f.RuleID, f.Description)
	}
	b.WriteString("\nif this is a false positive, resubmit with force to override.")
	return b.String()
}

// Check enforces the entry-body secret policy for a write to snap. A secret in
// the body is refused when the entry is on a syncable tier (private cannot push,
// so it is allowed) — returning a *RefusedError; the caller diverts the flagged
// write into the quarantine review queue, which preserves the body. force
// overrides the refusal for a betterleaks false positive: the body is stored
// as-is and no scan runs. A missing scanner is warn-not-fail: the body is stored
// and unscanned is true so the caller can flag it (the push boundary still
// refuses to push an unscanned or secret-bearing body).
func Check(snap *entry.Snapshot, body string, force bool) (unscanned bool, err error) {
	if force {
		return false, nil
	}
	findings, serr := scan.Scan([]byte(body))
	if errors.Is(serr, scan.ErrMissing) {
		return true, nil
	}
	if serr != nil {
		return false, fmt.Errorf("secret scan failed: %w", serr)
	}
	if len(findings) == 0 || snap.TierType == string(entry.TierPrivate) {
		return false, nil
	}
	return false, &RefusedError{Findings: findings, Tier: snap.Tier}
}
