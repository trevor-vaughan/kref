package hooks

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// InstallPath is the conventional lefthook config filename. lefthook searches
// the dot-prefixed variant as readily as the bare one; the dotfile keeps the
// repo root tidy.
const InstallPath = ".lefthook.yml"

// DefaultIngestPaths are the directories the post-commit hook watches for
// changed markdown when no --ingest-path override is supplied.
var DefaultIngestPaths = []string{"docs/superpowers/plans", "specs", ".specify", "openspec"}

// Render returns a lefthook configuration wiring kref to git lifecycle events.
// krefPath is the absolute path to the kref binary (os.Executable at install time)
// so the hooks work even when kref is not on PATH (e.g. the ./bin/kref install).
// ingestPaths controls which directories the post-commit hook watches; nil or
// empty uses DefaultIngestPaths.
func Render(krefPath string, ingestPaths []string) string {
	if len(ingestPaths) == 0 {
		ingestPaths = DefaultIngestPaths
	}
	return fmt.Sprintf(`# Managed by `+"`kref hooks install`"+`. Run by lefthook (https://lefthook.dev).
post-merge:
  commands:
    kref-sync-pull:
      run: %[1]s sync pull
post-checkout:
  commands:
    kref-sync-pull:
      run: %[1]s sync pull
pre-push:
  commands:
    kref-sync-push:
      run: %[1]s sync push
post-commit:
  commands:
    kref-ingest:
      files: git diff-tree --no-commit-id --name-only -r HEAD -- %[2]s
      glob: "*.md"
      run: %[1]s ingest --skip-missing {files}
`, krefPath, strings.Join(ingestPaths, " "))
}

// Merge folds kref's managed commands (every command whose key starts with "kref-")
// from generated into existing, preserving all other hooks, commands, and the
// existing file's comments/order. When existing is empty it returns generated
// unchanged. Only the first YAML document is considered; lefthook configs are
// single-document, so a `---`-separated second document would be dropped.
func Merge(existing []byte, generated string) ([]byte, error) {
	if strings.TrimSpace(string(existing)) == "" {
		return []byte(generated), nil
	}
	generated = stripLeadingComments(generated)
	var curDoc, genDoc yaml.Node
	if err := yaml.Unmarshal(existing, &curDoc); err != nil {
		return nil, fmt.Errorf("parse existing %s: %w", InstallPath, err)
	}
	if err := yaml.Unmarshal([]byte(generated), &genDoc); err != nil {
		return nil, fmt.Errorf("parse generated config: %w", err)
	}
	curRoot, genRoot := mappingRoot(&curDoc), mappingRoot(&genDoc)
	if curRoot == nil || genRoot == nil {
		return nil, fmt.Errorf("lefthook config is not a YAML mapping")
	}
	for i := 0; i+1 < len(genRoot.Content); i += 2 {
		hookKey, hookVal := genRoot.Content[i], genRoot.Content[i+1]
		if curHook := mapGet(curRoot, hookKey.Value); curHook != nil {
			mergeHookCommands(curHook, hookVal)
		} else {
			curRoot.Content = append(curRoot.Content, hookKey, hookVal) // hook absent: add wholesale
		}
	}
	// Encode with 2-space indent to match Render() and kref's other YAML/JSON
	// output (yaml.Marshal defaults to 4 spaces).
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&curDoc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// mappingRoot returns the root mapping node of a parsed YAML document, or nil.
func mappingRoot(doc *yaml.Node) *yaml.Node {
	if doc.Kind == yaml.DocumentNode && len(doc.Content) == 1 && doc.Content[0].Kind == yaml.MappingNode {
		return doc.Content[0]
	}
	return nil
}

// mapGet returns the value node for key in a mapping node, or nil.
func mapGet(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// stripLeadingComments removes leading blank and `#` comment lines so a merge
// does not graft kref's managed banner into the middle of the user's file. The
// fresh-install path returns the banner-bearing text before this runs.
func stripLeadingComments(s string) string {
	lines := strings.Split(s, "\n")
	i := 0
	for i < len(lines) {
		t := strings.TrimSpace(lines[i])
		if t == "" || strings.HasPrefix(t, "#") {
			i++
			continue
		}
		break
	}
	return strings.Join(lines[i:], "\n")
}

// mergeHookCommands replaces kref-* command entries under curHook's `commands`
// mapping with those from genHook, preserving foreign commands.
func mergeHookCommands(curHook, genHook *yaml.Node) {
	genCmds := mapGet(genHook, "commands")
	if genCmds == nil {
		return
	}
	curCmds := mapGet(curHook, "commands")
	if curCmds == nil {
		curHook.Content = append(curHook.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "commands"}, genCmds)
		return
	}
	kept := make([]*yaml.Node, 0, len(curCmds.Content))
	for i := 0; i+1 < len(curCmds.Content); i += 2 {
		if !strings.HasPrefix(curCmds.Content[i].Value, "kref-") {
			kept = append(kept, curCmds.Content[i], curCmds.Content[i+1])
		}
	}
	for i := 0; i+1 < len(genCmds.Content); i += 2 {
		kept = append(kept, genCmds.Content[i], genCmds.Content[i+1])
	}
	curCmds.Content = kept
}
