package app

import (
	"bytes"
	"crypto/ecdh"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const schema = 1

var validName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

type Identity struct {
	Role       string          `json:"role"`
	Public     string          `json:"public,omitempty"`
	Private    json.RawMessage `json:"private,omitempty"`
	Source     string          `json:"source,omitempty"`
	NativeName string          `json:"native_name,omitempty"`
}
type Remote struct {
	Type       string `json:"type,omitempty"`
	Value      string `json:"value,omitempty"`
	Address    string `json:"address,omitempty"`
	Client     string `json:"client,omitempty"`
	Server     string `json:"server,omitempty"`
	PeerPublic string `json:"peer_public_key,omitempty"`
	Peer       string `json:"peer,omitempty"`
}
type Service struct {
	Kind              string   `json:"kind"`
	Remote            string   `json:"remote,omitempty"`
	ClientKey         string   `json:"client_key,omitempty"`
	ServerKey         string   `json:"server_key,omitempty"`
	Enabled           bool     `json:"enabled"`
	Ports             []string `json:"ports"`
	Bind              string   `json:"bind,omitempty"`
	Allow             []string `json:"allow,omitempty"`
	PublicKeys        []string `json:"public_keys,omitempty"`
	AllowAnyone       bool     `json:"allow_anyone,omitempty"`
	SSHAuthorizedKeys string   `json:"ssh_authorized_keys,omitempty"`
}
type Config struct {
	Version  int                 `json:"version"`
	Keys     map[string]Identity `json:"keys"`
	Remotes  map[string]Remote   `json:"remotes"`
	Services map[string]Service  `json:"services"`
	Warnings []string            `json:"-"`
}
type Store struct {
	Dir       string
	NativeDir string
}

func DefaultStore() (Store, error) {
	base, err := os.UserConfigDir()
	dir := os.Getenv("TAILCHASE_CONFIG_DIR")
	if dir == "" {
		if err != nil {
			return Store{}, err
		}
		dir = filepath.Join(base, "tailchase")
	}
	native := ""
	if err == nil {
		native = filepath.Join(base, "tailcat", "keys")
	}
	if override, ok := os.LookupEnv("TAILCHASE_TAILCAT_KEYS_DIR"); ok {
		native = override
	}
	return Store{Dir: dir, NativeDir: native}, nil
}
func empty() Config {
	return Config{Version: schema, Keys: map[string]Identity{}, Remotes: map[string]Remote{}, Services: map[string]Service{}}
}
func (s Store) Load() (Config, error) {
	b, err := os.ReadFile(filepath.Join(s.Dir, "config.json"))
	if errors.Is(err, os.ErrNotExist) {
		return s.withNativeKeys(empty()), nil
	}
	if err != nil {
		return Config{}, err
	}
	c := empty()
	if err = decode(b, &c); err != nil {
		return c, err
	}
	if err = c.Validate(); err != nil {
		return c, err
	}
	c = normalizeAliases(s.withNativeKeys(c))
	return c, c.Validate()
}
func decode(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("expected exactly one JSON object")
	}
	return nil
}
func (c Config) Validate() error {
	if c.Keys == nil || c.Remotes == nil || c.Services == nil {
		return errors.New("configuration maps must not be null")
	}
	if c.Version != schema {
		return fmt.Errorf("unsupported schema version %d", c.Version)
	}
	for n, k := range c.Keys {
		if !validIdentityName(n) {
			return fmt.Errorf("invalid identity name %q", n)
		}
		if k.Source != "" && k.Source != "tailcat" {
			return fmt.Errorf("invalid identity source for %s", n)
		}
		if k.Source == "tailcat" {
			if !validNativeName(k.NativeName) || n != nativeID(k.NativeName) {
				return fmt.Errorf("invalid native identity reference %s", n)
			}
		} else if k.NativeName != "" {
			return fmt.Errorf("unexpected native identity name for %s", n)
		}
		if k.Role != "client" && k.Role != "server" {
			return fmt.Errorf("invalid identity role for %s", n)
		}
		if len(k.Private) > 0 {
			pub, err := privatePublic(k.Private)
			if err != nil {
				return fmt.Errorf("identity %s: %w", n, err)
			}
			if k.Public != pub {
				return fmt.Errorf("identity %s: public/private mismatch", n)
			}
		}
		if !validPublic(k.Public) {
			return fmt.Errorf("identity %s needs a valid nodekey public key", n)
		}
	}
	for n, r := range c.Remotes {
		if err := validateAlias(r); err != nil {
			return fmt.Errorf("alias %s: %w", n, err)
		}
		if r.Peer != "" {
			k, ok := c.Keys[r.Peer]
			if !ok || !isPeerIdentity(k) {
				return fmt.Errorf("alias %s: missing peer public identity %s", n, r.Peer)
			}
			if r.PeerPublic != "" && r.PeerPublic != k.Public {
				return fmt.Errorf("alias %s: conflicting peer identities", n)
			}
		}
		if r.PeerPublic != "" && !validPublic(r.PeerPublic) {
			return fmt.Errorf("alias %s: peer key must be nodekey: followed by 64 hexadecimal digits", n)
		}
		if strings.HasPrefix(r.Address, "-") || strings.ContainsAny(r.Address, " \t\n\r") {
			return fmt.Errorf("alias %s: invalid address", n)
		}
		if !validName.MatchString(n) {
			return fmt.Errorf("invalid alias %q", n)
		}
		for role, key := range map[string]string{"client": r.Client, "server": r.Server} {
			if key != "" {
				k, ok := c.Keys[key]
				if !ok || k.Role != role {
					return fmt.Errorf("alias %s: missing %s identity %s", n, role, key)
				}
			}
		}
	}

	activeServers := map[string]string{}
	for n, v := range c.Services {
		if !validName.MatchString(n) {
			return fmt.Errorf("invalid service name %q", n)
		}
		r, remoteExists := c.Remotes[v.Remote]
		if len(v.Ports) == 0 {
			return fmt.Errorf("service %s: ports/services required", n)
		}
		for _, p := range v.Ports {
			if p == "" || strings.HasPrefix(p, "-") {
				return fmt.Errorf("service %s: invalid port/service", n)
			}
		}
		if v.ClientKey != "" {
			k, ok := c.Keys[v.ClientKey]
			if v.Kind != "forward" || !ok || k.Role != "client" {
				return fmt.Errorf("service %s: invalid local client identity %s", n, v.ClientKey)
			}
		}
		switch v.Kind {
		case "forward":
			if !remoteExists {
				return fmt.Errorf("forward %s: missing remote alias", n)
			}
			if v.ServerKey != "" {
				return fmt.Errorf("forward %s cannot specify a server key", n)
			}
			if v.Bind != "" && net.ParseIP(v.Bind) == nil && v.Bind != "localhost" {
				return errors.New("bind must be an IP address or localhost")
			}
			for _, p := range v.Ports {
				if !validMapping(p) {
					return fmt.Errorf("invalid forward mapping %q", p)
				}
			}
			if aliasAddress(r) == "" {
				return fmt.Errorf("forward %s needs a TC address alias", n)
			}
		case "serve":
			if v.ServerKey != "" && v.Remote != "" {
				return fmt.Errorf("serve %s uses a server key directly; omit remote alias", n)
			}
			if v.ServerKey == "" && !remoteExists {
				return fmt.Errorf("serve %s needs a server key", n)
			}
			key := serverIdentity(c, v)
			identity, exists := c.Keys[key]
			if key == "" || !exists || identity.Role != "server" {
				return fmt.Errorf("serve %s needs a valid server identity", n)
			}
			if v.Enabled {
				if other, ok := activeServers[identity.Public]; ok {
					return fmt.Errorf("serve %s and %s share an active server identity", n, other)
				}
				activeServers[identity.Public] = n
			}

			for _, p := range v.Ports {
				for _, part := range strings.Split(p, ",") {
					if part != "all" && part != "exit-node" && part != "ssh" && part != "files" && !validPortRange(part) {
						return fmt.Errorf("invalid served port/service %q", part)
					}
					if part == "exec" || part == "no-auth-ssh" {
						return errors.New("exec/no-auth-ssh are not supported")
					}
					if part == "ssh" && v.SSHAuthorizedKeys == "" {
						return errors.New("serve ssh requires --ssh-authorized-keys")
					}
				}
			}
		default:
			return fmt.Errorf("unknown service kind %q", v.Kind)
		}
		if v.AllowAnyone && (len(v.Allow) > 0 || len(v.PublicKeys) > 0) {
			return errors.New("allow-anyone cannot be combined with selected clients")
		}
		for _, a := range v.Allow {
			if !validPublic(peerPublic(c, a)) {
				return fmt.Errorf("allow alias %s has no client public identity", a)
			}
		}

		for _, p := range v.PublicKeys {
			if !validPublic(p) {
				return errors.New("allow public key must be a valid nodekey")
			}
		}
	}
	return nil
}
func (s Store) save(c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	c = normalizeAliases(c)
	if err := c.Validate(); err != nil {
		return err
	}
	persisted := c
	persisted.Keys = map[string]Identity{}
	referenced := map[string]bool{}
	for _, r := range c.Remotes {
		referenced[r.Client] = true
		referenced[r.Server] = true
		referenced[r.Peer] = true
	}
	for _, v := range c.Services {
		referenced[v.ClientKey] = true
		if v.Kind == "serve" {
			referenced[serverIdentity(c, v)] = true
		}
	}
	for n, k := range c.Keys {
		if k.Source == "tailcat" {
			if !referenced[n] {
				continue
			}
			k.Private = nil
		}
		persisted.Keys[n] = k
	}
	b, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(s.Dir, "config.json"), b)
}
func atomicWrite(path string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".tailchase-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}
func (s Store) lock(name string) (func(), error) {
	if err := os.MkdirAll(s.Dir, 0700); err != nil {
		return nil, err
	}
	return acquireLock(filepath.Join(s.Dir, name+".lock"))
}
func (s Store) Update(fn func(*Config) error) error {
	unlock, err := s.lock("config")
	if err != nil {
		return err
	}
	defer unlock()
	c, err := s.Load()
	if err != nil {
		return err
	}
	if err = fn(&c); err != nil {
		return err
	}
	return s.save(c)
}
func fingerprint(k Identity) string {
	data := []byte(k.Public)
	if len(data) == 0 {
		data = k.Private
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:8])
}
func names[M ~map[string]V, V any](m M) []string {
	out := make([]string, 0, len(m))
	for n := range m {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

// Tailcat private key JSON uses the WireGuard/X25519 node identity.
func privatePublic(b json.RawMessage) (string, error) {
	var key struct {
		Private string
		Public  struct {
			ServerPublic      string
			ServerDiscoPublic string
			PresharedKey      string
		}
	}
	if json.Unmarshal(b, &key) != nil || !strings.HasPrefix(key.Private, "privkey:") {
		return "", errors.New("invalid tailcat private key JSON")
	}
	raw, err := hex.DecodeString(strings.TrimPrefix(key.Private, "privkey:"))
	if err != nil {
		return "", errors.New("invalid private key encoding")
	}
	priv, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		return "", errors.New("invalid private key length")
	}
	pub := "nodekey:" + hex.EncodeToString(priv.PublicKey().Bytes())
	if pub != key.Public.ServerPublic {
		return "", errors.New("private key does not match stored public key")
	}
	return pub, nil
}

func validPublic(s string) bool {
	if !strings.HasPrefix(s, "nodekey:") {
		return false
	}
	b, e := hex.DecodeString(strings.TrimPrefix(s, "nodekey:"))
	return e == nil && len(b) == 32
}
func port(s string, zero bool) bool {
	n, e := strconv.Atoi(s)
	return e == nil && n <= 65535 && (n > 0 || zero && n == 0)
}
func validPortRange(s string) bool {
	a, b, ok := strings.Cut(s, "-")
	if !ok {
		return port(s, false)
	}
	lo, e := strconv.Atoi(a)
	hi, ee := strconv.Atoi(b)
	return e == nil && ee == nil && lo > 0 && hi <= 65535 && lo <= hi
}
func validMapping(s string) bool {
	p := strings.Split(s, ":")
	switch len(p) {
	case 1:
		return port(p[0], false)
	case 2:
		return port(p[0], true) && port(p[1], false)
	case 3:
		return port(p[0], true) && net.ParseIP(p[1]) != nil && port(p[2], false)
	}
	return false
}
