package entry

import (
	"fmt"
	"regexp"
	"sort"
)

// TierDef describes one visibility tier: its name (which is also the ref
// namespace suffix, refs/kref-<name>/) and its type, which drives rendering
// (glyph, color, ordering) and sync semantics. Built-in tiers have Name == Type.
type TierDef struct {
	Name     Tier `json:"name"`
	Type     Tier `json:"type"`     // TierPrivate | TierPersonal | TierShared
	Declared bool `json:"declared"` // false = discovered from refs, absent from config
}

// Builtin reports whether the def is one of the three built-in tiers.
func (d TierDef) Builtin() bool {
	return d.Name == TierPrivate || d.Name == TierPersonal || d.Name == TierShared
}

// System reports whether the def is a reserved system tier (currently only
// quarantine): private-typed, code-declared, hidden, never a user write target.
func (d TierDef) System() bool { return d.Name == TierQuarantine }

// IsSystemTier reports whether a tier name is a reserved system tier.
func IsSystemTier(t Tier) bool { return TierDef{Name: t}.System() }

// BuiltinTierDefs returns the three built-in tiers in display order.
func BuiltinTierDefs() []TierDef {
	return []TierDef{
		{Name: TierPrivate, Type: TierPrivate, Declared: true},
		{Name: TierPersonal, Type: TierPersonal, Declared: true},
		{Name: TierShared, Type: TierShared, Declared: true},
	}
}

// SystemTierDefs returns the reserved system tiers (hidden from user listings):
// private-typed, so they inherit the non-syncable guarantee everywhere that
// checks TierPrivate (push, remotes).
func SystemTierDefs() []TierDef {
	return []TierDef{{Name: TierQuarantine, Type: TierPrivate, Declared: true}}
}

// BuiltinTierDefsWithSystem returns the three user built-ins plus the reserved
// system tiers. resolveTiers seeds from this so the store recognises the
// quarantine namespace on every open.
func BuiltinTierDefsWithSystem() []TierDef {
	return append(BuiltinTierDefs(), SystemTierDefs()...)
}

// tierNameRe is the custom-tier name shape: lowercase letter first, then
// lowercase letters, digits, or dashes; 2–32 characters total.
var tierNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)

// reservedTierNames cannot be used for custom tiers: the built-ins, plus the
// bookkeeping ref namespaces (refs/kref-pushed/*) and config keys
// (kref.incoming.*) a custom tier's name must never collide with.
var reservedTierNames = map[string]bool{
	"private": true, "personal": true, "shared": true,
	"pushed": true, "incoming": true, "quarantine": true,
}

// ValidateTierName checks a custom tier name against the shape rule and the
// reserved list.
func ValidateTierName(name string) error {
	if reservedTierNames[name] {
		return fmt.Errorf("tier name %q is reserved", name)
	}
	if !tierNameRe.MatchString(name) {
		return fmt.Errorf("invalid tier name %q (want 2-32 chars: a lowercase letter, then lowercase letters, digits, or dashes)", name)
	}
	return nil
}

// ValidTierType reports whether t is a type a custom tier may take. Private is
// excluded by design: the "structurally cannot leave" guarantee stays unique
// to the singular built-in private tier.
func ValidTierType(t Tier) bool { return t == TierPersonal || t == TierShared }

// SortTierDefs orders defs for display: private first, then personal-typed,
// then shared-typed; within a type the built-in leads and customs follow
// alphabetically.
func SortTierDefs(defs []TierDef) {
	rank := func(t Tier) int {
		switch t {
		case TierPrivate:
			return 0
		case TierPersonal:
			return 1
		default:
			return 2
		}
	}
	sort.SliceStable(defs, func(i, j int) bool {
		a, b := defs[i], defs[j]
		if ra, rb := rank(a.Type), rank(b.Type); ra != rb {
			return ra < rb
		}
		if a.Builtin() != b.Builtin() {
			return a.Builtin()
		}
		return a.Name < b.Name
	})
}
