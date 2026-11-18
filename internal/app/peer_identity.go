package app

import (
	"fmt"
	"strings"
)

// Peer public identities authorize incoming clients. They are distinct from the
// local private identity used for outbound SSH/forward connections.
func peerPublic(c Config, alias string) string {
	r, ok := c.Remotes[alias]
	if !ok {
		return ""
	}
	return peerPublicForRemote(c, r)
}
func peerPublicForRemote(c Config, r Remote) string {
	if r.Type == aliasNodekey {
		return r.Value
	}
	if r.Type != "" {
		return ""
	}
	if r.Peer != "" {
		return c.Keys[r.Peer].Public
	}
	if r.PeerPublic != "" {
		return r.PeerPublic
	}
	return c.Keys[r.Client].Public
}

func peerFingerprint(c Config, alias string) string {
	pub := peerPublic(c, alias)
	if pub == "" {
		return ""
	}
	return fingerprint(Identity{Public: pub})
}
func normalizePeerKey(value string) string {
	value = strings.TrimSpace(value)
	if hex, ok := strings.CutPrefix(value, "nodekey:"); ok {
		return "nodekey:" + strings.ToLower(hex)
	}
	return value
}
func checkPeerChange(c Config, alias string, next Remote) error {
	old := peerPublic(c, alias)
	pub := peerPublicForRemote(c, next)
	if old == pub {
		return nil
	}
	used := []string{}
	for _, id := range names(c.Services) {
		for _, a := range c.Services[id].Allow {
			if a == alias {
				used = append(used, id)
				break
			}
		}
	}
	if len(used) > 0 {
		return fmt.Errorf("alias %s is used by allow policies: %s; remove it from those policies before changing its peer key", alias, strings.Join(used, ", "))
	}
	return nil
}

// Public-only node identities have no encoded client/server role. Keep legacy
// role annotations compatible while restricting local private credentials to
// their configured outbound usage.
func isPeerIdentity(k Identity) bool {
	return validPublic(k.Public) && (k.Role == "client" || len(k.Private) == 0 && k.Source == "")
}
