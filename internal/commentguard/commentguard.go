// Package commentguard enforces the secret policy for a comment write, shared by
// every boundary that accepts a comment body (the CLI, the MCP tool). It mirrors
// todoguard: a fail-closed check with no-work-lost recovery. A comment cannot be
// tiered independently of its parent entry, and the push boundary does not scan
// comment bodies, so this write-time scan is the primary gate against a secret
// reaching a syncable tier — refuse it there, but never lose the author's text.
package commentguard

import (
	"errors"
	"fmt"
	"strings"

	"github.com/trevor-vaughan/kref/internal/entry"
	"github.com/trevor-vaughan/kref/internal/scan"
)

// RefusedError reports that a comment body was refused because it carries a
// secret and its parent entry is on a syncable tier. Its message names the
// offending finding(s) — rule and line — but never the secret value itself. The
// caller diverts the flagged write into the quarantine review queue, so the body
// is preserved there (not by this guard); nothing is lost.
type RefusedError struct {
	Findings []scan.Finding
	Tier     string
}

func (e *RefusedError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "secret detected in the comment body — refusing to write it to the %s tier (it can push to a remote):", e.Tier)
	for _, f := range e.Findings {
		fmt.Fprintf(&b, "\n  line %d: %s: %s", f.StartLine, f.RuleID, f.Description)
	}
	b.WriteString("\nif this is a false positive, resubmit with force to override.")
	return b.String()
}

// Check enforces the comment secret policy for a write to snap. A secret in the
// body is refused when the entry is on a syncable tier (private cannot push, so
// it is allowed) — returning a *RefusedError; the caller diverts the flagged
// write into the quarantine review queue, which preserves the body. force
// overrides the refusal for a betterleaks false positive: the body is stored
// as-is and no scan runs. A missing scanner is warn-not-fail: the body is stored
// and unscanned is true so the caller can flag it (the push boundary would still
// block an entry-body secret, though not a comment one).
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
