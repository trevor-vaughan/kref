// Package content classifies and validates entry body content types. It is
// pure (stdlib only) so entry/bridge can validate without importing any
// renderer. The registry here is the single source of truth for which text
// content types kref supports; binary content is rejected outright.
package content

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Default is the content type assumed for entries created before content-type
// existed, and for create/ingest paths that do not specify one.
const Default = "text/markdown"

// ErrBinary is returned when content is not valid UTF-8 text.
var ErrBinary = errors.New("binary or non-UTF-8 content is not supported (text only)")

// typeInfo describes one supported text content type.
type typeInfo struct {
	lexer string // chroma lexer name; "" means render verbatim (or, for markdown, via glamour)
	md    bool   // markdown: glamour-rendered and trailer-eligible on ingest
}

// registry is the single source of truth for supported text content types.
var registry = map[string]typeInfo{
	"text/markdown":      {lexer: "", md: true},
	"text/plain":         {lexer: ""},
	"application/json":   {lexer: "json"},
	"application/yaml":   {lexer: "yaml"},
	"application/toml":   {lexer: "toml"},
	"text/x-go":          {lexer: "go"},
	"text/x-python":      {lexer: "python"},
	"text/x-shellscript": {lexer: "bash"},
	"text/javascript":    {lexer: "javascript"},
	"text/x-typescript":  {lexer: "typescript"},
}

// extMap maps lower-case file extensions to canonical content types.
var extMap = map[string]string{
	".md":       "text/markdown",
	".markdown": "text/markdown",
	".txt":      "text/plain",
	".json":     "application/json",
	".yaml":     "application/yaml",
	".yml":      "application/yaml",
	".toml":     "application/toml",
	".go":       "text/x-go",
	".py":       "text/x-python",
	".sh":       "text/x-shellscript",
	".bash":     "text/x-shellscript",
	".js":       "text/javascript",
	".ts":       "text/x-typescript",
}

// Canonical normalizes a content type (lower-cased, parameters stripped) and
// errors if it is not a supported text type.
func Canonical(ct string) (string, error) {
	base := strings.ToLower(strings.TrimSpace(ct))
	if i := strings.IndexByte(base, ';'); i >= 0 {
		base = strings.TrimSpace(base[:i])
	}
	if _, ok := registry[base]; !ok {
		return "", fmt.Errorf("unsupported content type %q (text types only, e.g. text/markdown, application/json)", ct)
	}
	return base, nil
}

// Lexer returns the chroma lexer name for a content type, or "" if it has none
// (markdown, plain, or an unknown type).
func Lexer(ct string) string {
	base, err := Canonical(ct)
	if err != nil {
		return ""
	}
	return registry[base].lexer
}

// IsMarkdown reports whether a content type is markdown.
func IsMarkdown(ct string) bool {
	base, err := Canonical(ct)
	if err != nil {
		return false
	}
	return registry[base].md
}

// EnsureText returns ErrBinary if body is not valid UTF-8 or contains a NUL byte.
func EnsureText(body []byte) error {
	if !utf8.Valid(body) || bytes.IndexByte(body, 0) >= 0 {
		return ErrBinary
	}
	return nil
}

// Detect rejects binary content, then resolves a content type from the file
// extension, falling back to text/plain for unknown (but textual) files.
func Detect(path string, body []byte) (string, error) {
	if err := EnsureText(body); err != nil {
		return "", err
	}
	if ct, ok := extMap[strings.ToLower(filepath.Ext(path))]; ok {
		return ct, nil
	}
	return "text/plain", nil
}

// RegisteredTypes returns every canonical content type (for consistency tests).
func RegisteredTypes() []string {
	out := make([]string, 0, len(registry))
	for ct := range registry {
		out = append(out, ct)
	}
	return out
}

// ExtensionTypes returns the extension→content-type map (for consistency tests).
func ExtensionTypes() map[string]string {
	out := make(map[string]string, len(extMap))
	for k, v := range extMap {
		out[k] = v
	}
	return out
}
