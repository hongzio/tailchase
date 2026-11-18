package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Explicit identities stay pinned. Address-only remotes use the native client
// default when available, otherwise an explicit ephemeral identity.
func (s Store) clientIdentity(c Config, r Remote) (string, error) {
	name := r.Client
	if name == "" {
		name = nativeID("client-default")
		if _, ok := c.Keys[name]; !ok {
			if s.NativeDir != "" {
				_, err := os.Stat(filepath.Join(s.NativeDir, "client-default.private.json"))
				if err == nil || !errors.Is(err, os.ErrNotExist) {
					return "", fmt.Errorf("default client identity is unreadable or invalid")
				}
			}
			return "", nil
		}
	}
	k, ok := c.Keys[name]
	if !ok || k.Role != "client" || len(k.Private) == 0 {
		return "", fmt.Errorf("client identity %s unavailable", name)
	}
	return name, nil
}
func (s Store) serviceIdentity(c Config, v Service) (string, error) {
	r := c.Remotes[v.Remote]
	if v.ClientKey != "" {
		r.Client = v.ClientKey
	}
	if v.Kind != "serve" {
		return s.clientIdentity(c, r)
	}
	key := serverIdentity(c, v)
	if key == "" || c.Keys[key].Role != "server" || len(c.Keys[key].Private) == 0 {
		return "", fmt.Errorf("server identity %s unavailable", key)
	}
	return key, nil
}

// Older profiles keep their server identity on a remote; new services own the
// identity reference directly and do not need a remote alias.
func serverIdentity(c Config, v Service) string {
	if v.ServerKey != "" {
		return v.ServerKey
	}
	return c.Remotes[v.Remote].Server
}
