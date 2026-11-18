package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"strconv"
	"strings"
)

const bundleVersion = 2
const maxBundleEntries = 10000

type BundleAlias struct {
	Name  string `json:"name,omitempty"`
	Type  string `json:"type"`
	Value string `json:"value"`
}
type Bundle struct {
	Version int           `json:"version"`
	Aliases []BundleAlias `json:"aliases"`
}
type ImportSummary struct{ Added, SkippedKeys, SkippedAliases, Renamed int }

func bundleValueKey(kind, value string) string {
	if kind == aliasNodekey {
		value = normalizePeerKey(value)
	}
	return kind + "\x00" + value
}
func keyAliasValues(c Config) map[string]bool {
	values := map[string]bool{}
	for _, k := range c.Keys {
		if validPublic(k.Public) {
			values[bundleValueKey(aliasNodekey, k.Public)] = true
		}
		if address := savedKeyAddress(k).Address; address != "" {
			values[bundleValueKey(aliasTC, address)] = true
		}
	}
	return values
}
func (b Bundle) Validate() error {
	if b.Version != bundleVersion {
		return fmt.Errorf("unsupported alias bundle version %d", b.Version)
	}
	if b.Aliases == nil {
		return fmt.Errorf("aliases must be an array")
	}
	if len(b.Aliases) > maxBundleEntries {
		return fmt.Errorf("too many alias entries (maximum %d)", maxBundleEntries)
	}
	names := map[string]bool{}
	for _, a := range b.Aliases {
		if a.Name != "" {
			if !validName.MatchString(a.Name) {
				return fmt.Errorf("invalid alias name %q", a.Name)
			}
			if names[a.Name] {
				return fmt.Errorf("duplicate alias name %s", a.Name)
			}
			names[a.Name] = true
		}
		if len(a.Value) > 8192 {
			return fmt.Errorf("alias value exceeds maximum length")
		}
		if err := validateAlias(Remote{Type: a.Type, Value: a.Value}); err != nil {
			return fmt.Errorf("invalid alias entry: %w", err)
		}
		if a.Type == "" {
			return fmt.Errorf("alias type is required")
		}
	}
	return nil
}
func exportBundle(c Config) (Bundle, error) {
	if err := c.Validate(); err != nil {
		return Bundle{}, err
	}
	c = normalizeAliases(c)
	b := Bundle{Version: bundleVersion, Aliases: []BundleAlias{}}
	for _, name := range names(c.Remotes) {
		r := c.Remotes[name]
		b.Aliases = append(b.Aliases, BundleAlias{Name: name, Type: aliasType(r), Value: aliasValue(c, r)})
	}
	seen := map[string]bool{}
	for _, name := range names(c.Keys) {
		k := c.Keys[name]
		for _, r := range []Remote{{Type: aliasTC, Value: savedKeyAddress(k).Address}, {Type: aliasNodekey, Value: k.Public}} {
			if r.Value == "" {
				continue
			}
			key := bundleValueKey(r.Type, r.Value)
			if seen[key] {
				continue
			}
			seen[key] = true
			b.Aliases = append(b.Aliases, BundleAlias{Type: r.Type, Value: r.Value})
		}
	}
	return b, b.Validate()
}
func mergeBundle(c *Config, b Bundle) (ImportSummary, error) {
	var summary ImportSummary
	if err := b.Validate(); err != nil {
		return summary, err
	}
	next := *c
	next.Remotes = maps.Clone(c.Remotes)
	reserved := map[string]bool{}
	for _, a := range b.Aliases {
		if a.Name != "" {
			reserved[a.Name] = true
		}
	}
	local := keyAliasValues(*c)
	existing := map[string]bool{}
	for _, r := range next.Remotes {
		existing[bundleValueKey(aliasType(r), aliasValue(next, r))] = true
	}
	for _, a := range b.Aliases {
		if a.Type == aliasNodekey {
			a.Value = normalizePeerKey(a.Value)
		}
		valueKey := bundleValueKey(a.Type, a.Value)
		if local[valueKey] {
			summary.SkippedKeys++
			continue
		}
		name := a.Name
		if name == "" {
			if existing[valueKey] {
				summary.SkippedAliases++
				continue
			}
			name = freeAliasNameExcluding(next, "unnamed-"+a.Type, reserved)
		} else if old, ok := next.Remotes[name]; ok {
			if bundleValueKey(aliasType(old), aliasValue(next, old)) == valueKey || importedAliasCopyExists(next, name, valueKey, reserved) {
				summary.SkippedAliases++
				continue
			}
			name = freeAliasNameExcluding(next, name, reserved)
			summary.Renamed++
		}
		next.Remotes[name] = Remote{Type: a.Type, Value: a.Value}
		existing[valueKey] = true
		summary.Added++
	}
	if err := next.Validate(); err != nil {
		return ImportSummary{}, err
	}
	*c = next
	return summary, nil
}
func samePrivate(a, b json.RawMessage) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	var aa, bb bytes.Buffer
	_ = json.Compact(&aa, a)
	_ = json.Compact(&bb, b)
	return bytes.Equal(aa.Bytes(), bb.Bytes())
}

// Find the collision copy from a previous import without collapsing distinct
// explicit names that happen to share a value. Reserve incoming names first so
// generated names never consume a later named entry in the same file.
func importedAliasCopyExists(c Config, base, valueKey string, reserved map[string]bool) bool {
	for name, r := range c.Remotes {
		if reserved[name] || bundleValueKey(aliasType(r), aliasValue(c, r)) != valueKey {
			continue
		}
		split := strings.LastIndexByte(name, '-')
		if split < 0 {
			continue
		}
		suffix := name[split:]
		n, err := strconv.Atoi(suffix[1:])
		if err != nil || n < 2 || strconv.Itoa(n) != suffix[1:] {
			continue
		}
		if base[:min(len(base), 64-len(suffix))]+suffix == name {
			return true
		}
	}
	return false
}
