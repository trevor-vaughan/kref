package scan

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/trevor-vaughan/kref/internal/xdg"
)

// ErrMissing means the betterleaks binary could not be found at all — as
// opposed to a scan that ran and failed. Callers pick their policy: ingest
// warns and proceeds unscanned, while the push boundary stays fail-closed.
var ErrMissing = errors.New(
	"betterleaks not found — secret scanning unavailable; install it: " +
		"`go install github.com/betterleaks/betterleaks@latest` (or set KREF_BETTERLEAKS)")

// Finding is one betterleaks detection.
// Field names match betterleaks' JSON report schema (PascalCase), which is
// kept compatible with the gitleaks v8 report it succeeds.
type Finding struct {
	RuleID      string `json:"RuleID"`
	Description string `json:"Description"`
	Secret      string `json:"Secret"`
	StartLine   int    `json:"StartLine"`
}

// betterleaksBin returns the betterleaks executable to invoke, resolving ambient
// state for resolveBetterleaks. os.Executable may fail on exotic platforms; an
// empty path simply skips the sibling lookup.
func betterleaksBin() string {
	exe, _ := os.Executable()
	return resolveBetterleaks(os.Getenv("KREF_BETTERLEAKS"), exe)
}

// resolveBetterleaks picks the betterleaks executable to invoke, in precedence
// order:
//  1. env (KREF_BETTERLEAKS) — the pinned, project-local build provisioned by
//     `task tools`, or an operator override;
//  2. a betterleaks sitting next to the kref binary — so the documented
//     `./bin/kref` + `./bin/betterleaks` source build (and the lefthook hooks,
//     which call kref by absolute path) work without KREF_BETTERLEAKS or a PATH
//     entry;
//  3. "betterleaks" resolved from PATH.
func resolveBetterleaks(env, exePath string) string {
	if env != "" {
		return env
	}
	if exePath != "" {
		if sib := filepath.Join(filepath.Dir(exePath), "betterleaks"); isExecutableFile(sib) {
			return sib
		}
	}
	return "betterleaks"
}

// isExecutableFile reports whether p is a regular file with an execute bit set.
func isExecutableFile(p string) bool {
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0o111 != 0
}

// Scan runs betterleaks over content and returns any findings.
// A non-empty result means the content should be blocked or routed to private.
//
// Requires betterleaks, provisioned by `task tools` into ./bin (or on PATH; set
// KREF_BETTERLEAKS to override). Invocation:
//
//	betterleaks stdin --no-banner -f json -r <tmp>
//
// Exit codes: 0 = clean, 1 = leaks found. Any other non-zero exit is an error.
func Scan(content []byte) ([]Finding, error) {
	report, err := os.CreateTemp(xdg.CacheTempDir(), "kref-betterleaks-*.json")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.Remove(report.Name()) }()
	_ = report.Close()

	cmd := exec.Command(betterleaksBin(), "stdin", "--no-banner", "-f", "json", "-r", report.Name())
	cmd.Stdin = bytes.NewReader(content)
	cmd.Stderr = io.Discard

	runErr := cmd.Run()

	var exitErr *exec.ExitError
	if runErr != nil && !errors.As(runErr, &exitErr) {
		// Binary not found gets the typed error so callers can choose their
		// policy (ingest warns and proceeds unscanned; push stays fail-closed).
		if errors.Is(runErr, exec.ErrNotFound) || errors.Is(runErr, os.ErrNotExist) {
			return nil, fmt.Errorf("%w (looked for %q): %v", ErrMissing, betterleaksBin(), runErr)
		}
		// Other non-exit error (e.g. I/O failure).
		return nil, fmt.Errorf("running betterleaks: %w", runErr)
	}
	if exitErr != nil && exitErr.ExitCode() != 1 {
		// Exit codes other than 0 (clean) and 1 (leaks found) are unexpected.
		return nil, fmt.Errorf("betterleaks scan failed (exit %d): %w", exitErr.ExitCode(), runErr)
	}

	data, err := os.ReadFile(report.Name())
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
	}
	var findings []Finding
	if err := json.Unmarshal(data, &findings); err != nil {
		return nil, err
	}
	return findings, nil
}
