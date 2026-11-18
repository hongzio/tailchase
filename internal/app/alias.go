package app

import (
	"fmt"
	"maps"
	"strings"
)

const (
	aliasTC      = "tc"
	aliasNodekey = "nodekey"
)

// Legacy fields are read only for pre-union configurations. New aliases store
// exactly a type and value; their name is the map key.
func aliasType(r Remote) string {
	if r.Type != "" {
		return r.Type
	}
	if r.Address != "" {
		return aliasTC
	}
	return aliasTC
}
func aliasValue(c Config, r Remote) string {
	if r.Type != "" {
		return r.Value
	}
	if r.Address != "" {
		return r.Address
	}
	return peerPublicForRemote(c, r)
}
func aliasAddress(r Remote) string {
	if r.Type == aliasTC {
		return r.Value
	}
	if r.Type != "" {
		return ""
	}
	return r.Address
}
func validateAlias(r Remote) error {
	if r.Type == "" {
		if r.Value != "" {
			return fmt.Errorf("type is required")
		}
		return nil
	}
	if r.Address != "" || r.Client != "" || r.Server != "" || r.Peer != "" || r.PeerPublic != "" {
		return fmt.Errorf("typed aliases contain only type and value")
	}
	switch r.Type {
	case aliasTC:
		if !strings.HasPrefix(r.Value, "tc") || len(r.Value) <= 2 || strings.ContainsAny(r.Value, " \t\n\r") {
			return fmt.Errorf("value must be a TC address starting with tc")
		}
	case aliasNodekey:
		if !validPublic(r.Value) {
			return fmt.Errorf("value must be nodekey: followed by 64 hexadecimal digits")
		}
	default:
		return fmt.Errorf("type must be tc or nodekey")
	}
	return nil
}

// Convert legacy records without losing authorization. A record that had both
// an address and an authorization identity becomes two independently named
// aliases, and allow lists are repointed to the nodekey alias. Native key
// discovery must precede this conversion so public identities can be resolved.
// Legacy non-TC/empty placeholders remain readable until explicitly edited.
func normalizeAliases(c Config) Config {
	c.Keys = maps.Clone(c.Keys)
	c.Remotes = maps.Clone(c.Remotes)
	c.Services = maps.Clone(c.Services)
	for _, name := range names(c.Remotes) {
		r := c.Remotes[name]
		if r.Type != "" {
			continue
		}
		pub := peerPublicForRemote(c, r)
		hasTC := strings.HasPrefix(r.Address, "tc") && len(r.Address) > 2
		peerName := name
		if r.Address != "" && pub != "" {
			peerName = freeAliasName(c, name+"-nodekey")
			c.Remotes[peerName] = Remote{Type: aliasNodekey, Value: pub}
		}
		for id, v := range c.Services {
			changed := false
			if v.Remote == name {
				if v.Kind == "serve" && r.Server != "" {
					v.ServerKey = r.Server
					v.Remote = ""
					changed = true
				}
				if v.Kind == "forward" && r.Client != "" && v.ClientKey == "" {
					v.ClientKey = r.Client
					changed = true
				}
			}
			for i, a := range v.Allow {
				if a == name && peerName != name {
					v.Allow = append([]string(nil), v.Allow...)
					v.Allow[i] = peerName
					changed = true
				}
			}
			if changed {
				c.Services[id] = v
			}
		}
		switch {
		case hasTC:
			c.Remotes[name] = Remote{Type: aliasTC, Value: r.Address}
		case r.Address == "" && pub != "":
			c.Remotes[name] = Remote{Type: aliasNodekey, Value: pub}
		default:
			// Keep old opaque addresses usable, but detach identities from the record.
			// They can be replaced with a TC address in the type/value editor.
			r.Client = ""
			r.Server = ""
			r.Peer = ""
			r.PeerPublic = ""
			c.Remotes[name] = r
		}
	}
	return movePublicKeysToAliases(c)
}
func freeAliasName(c Config, base string) string {
	return freeAliasNameExcluding(c, base, nil)
}
func freeAliasNameExcluding(c Config, base string, reserved map[string]bool) string {
	base = base[:min(64, len(base))]
	for n := 1; ; n++ {
		candidate := base
		if n > 1 {
			suffix := fmt.Sprintf("-%d", n)
			candidate = base[:min(len(base), 64-len(suffix))] + suffix
		}
		if _, ok := c.Remotes[candidate]; !ok && !reserved[candidate] {
			return candidate
		}
	}
}
func aliasesOfType(c Config, kind string) []string {
	out := []string{}
	for _, n := range names(c.Remotes) {
		r := c.Remotes[n]
		if kind == aliasTC && aliasAddress(r) != "" || kind == aliasNodekey && aliasType(r) == aliasNodekey && peerPublic(c, n) != "" {
			out = append(out, n)
		}
	}
	return out
}

func containsString(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

// Standalone public catalog entries are address-book aliases. Metadata for local
// keys that are unavailable remains pinned when a service still requires it.
func movePublicKeysToAliases(c Config) Config {
	local := map[string]bool{}
	for _, v := range c.Services {
		local[v.ClientKey] = true
		if v.Kind == "serve" {
			local[serverIdentity(c, v)] = true
		}
	}
	for _, r := range c.Remotes {
		local[r.Client] = true
		local[r.Server] = true
	}
	for _, name := range names(c.Keys) {
		k := c.Keys[name]
		if len(k.Private) > 0 || k.Source != "" || local[name] {
			continue
		}
		found := false
		for _, r := range c.Remotes {
			if r.Type == aliasNodekey && r.Value == k.Public {
				found = true
				break
			}
		}
		if !found {
			base := strings.Map(func(r rune) rune {
				if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
					return r
				}
				return '-'
			}, name)
			alias := base
			if _, exists := c.Remotes[alias]; exists {
				alias = base + "-nodekey"
			}
			alias = freeAliasName(c, alias)
			c.Remotes[alias] = Remote{Type: aliasNodekey, Value: k.Public}
		}
		delete(c.Keys, name)
	}
	return c
}

func (s Store) deleteAlias(name string) error {
	return s.Update(func(c *Config) error {
		if _, exists := c.Remotes[name]; !exists {
			return fmt.Errorf("alias not found: %s", name)
		}
		used := []string{}
		for _, id := range names(c.Services) {
			v := c.Services[id]
			if v.Remote == name || containsString(v.Allow, name) {
				used = append(used, id)
			}
		}
		if len(used) > 0 {
			return fmt.Errorf("alias %s is used by services: %s; remove those references before deleting it", name, strings.Join(used, ", "))
		}
		delete(c.Remotes, name)
		return nil
	})
}

// Rename under the caller's config lock so services never observe dangling
// references. Renaming changes labels, while nodekey policy checks stay in the
// normal value-update path.
func renameAlias(c *Config, oldName, newName string) error {
	if !validName.MatchString(newName) {
		return fmt.Errorf("invalid alias name %q", newName)
	}
	r, exists := c.Remotes[oldName]
	if !exists {
		return fmt.Errorf("alias not found: %s", oldName)
	}
	if newName == oldName {
		return nil
	}
	if _, exists := c.Remotes[newName]; exists {
		return fmt.Errorf("alias exists: %s", newName)
	}
	c.Remotes[newName] = r
	delete(c.Remotes, oldName)
	for id, v := range c.Services {
		changed := false
		if v.Remote == oldName {
			v.Remote = newName
			changed = true
		}
		for i, name := range v.Allow {
			if name == oldName {
				v.Allow = append([]string(nil), v.Allow...)
				v.Allow[i] = newName
				changed = true
			}
		}
		if changed {
			c.Services[id] = v
		}
	}
	return nil
}
