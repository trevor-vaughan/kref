package config

import (
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// migrations transforms a decoded document node from schema version (i+1) to
// (i+2). Append here (and bump CurrentVersion in config.go) to add a step; each
// migration may add/rename/remove/transform keys. Empty at v1 — the machinery
// and the newer-than-binary guard are what matter now.
var migrations = []func(*yaml.Node) error{}

// Migrate brings config bytes up to CurrentVersion, reporting whether anything
// changed (so callers can decide whether to rewrite). A version NEWER than this
// binary is a hard error — never downgrade a file written by a newer kref.
func Migrate(b []byte) (out []byte, changed bool, err error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, false, fmt.Errorf("config: %w", err)
	}
	v := readVersion(&doc)
	if v > CurrentVersion {
		return nil, false, fmt.Errorf("config version %d is newer than this kref (supports up to %d); upgrade kref", v, CurrentVersion)
	}
	if v == CurrentVersion {
		return b, false, nil
	}
	start := v
	if start < 1 {
		start = 1
	}
	for i := start - 1; i < len(migrations); i++ {
		if err := migrations[i](&doc); err != nil {
			return nil, false, err
		}
	}
	setVersion(&doc, CurrentVersion)
	out, err = yaml.Marshal(&doc)
	if err != nil {
		return nil, false, err
	}
	return out, true, nil
}

func mappingOf(doc *yaml.Node) *yaml.Node {
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		return doc.Content[0]
	}
	return doc
}

// readVersion returns the top-level `version:` int, or 0 when absent/unparseable.
func readVersion(doc *yaml.Node) int {
	m := mappingOf(doc)
	if m == nil || m.Kind != yaml.MappingNode {
		return 0
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == "version" {
			n, err := strconv.Atoi(strings.TrimSpace(m.Content[i+1].Value))
			if err != nil {
				return 0
			}
			return n
		}
	}
	return 0
}

// setVersion sets (or inserts, at the top) the `version:` key.
func setVersion(doc *yaml.Node, v int) {
	m := mappingOf(doc)
	if m == nil {
		return
	}
	if m.Kind != yaml.MappingNode {
		m.Kind = yaml.MappingNode
		m.Tag = "!!map"
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == "version" {
			m.Content[i+1].Value = strconv.Itoa(v)
			m.Content[i+1].Tag = "!!int"
			return
		}
	}
	key := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "version"}
	val := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(v)}
	m.Content = append([]*yaml.Node{key, val}, m.Content...)
}
