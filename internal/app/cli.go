package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const help = `tailchase — tailcat alias, identity and service manager

  tailchase tui
  tailchase key list
  tailchase key add NAME --role client|server --file KEY.private.json
  tailchase key generate NAME --role client|server
  tailchase key show NAME [--private]
  tailchase key delete NAME
  tailchase alias add NAME --type tc|nodekey --value VALUE
  tailchase alias set NAME [--name NEW_NAME] [--type tc|nodekey] [--value VALUE]
  tailchase alias delete NAME
  tailchase alias list
  tailchase alias show NAME
  tailchase service add ID --kind forward --remote ALIAS --ports 8080:80 [--client-key KEY]
  tailchase service add ID --kind serve --server-key KEY --ports 80,443
  tailchase service show ID
      [--bind 127.0.0.1] [--allow ALIAS,...] [--allow-anyone]
      [--ssh-authorized-keys SOURCE]
  tailchase allow SERVICE [ALIAS ...] [--anyone]
  tailchase enable|disable SERVICE
  tailchase export --output FILE
  tailchase import FILE [--dry-run]
  tailchase ssh ALIAS [--user USER] [--client-key KEY]
  tailchase daemon
  tailchase status

TAILCHASE_CONFIG_DIR overrides the platform config directory.
Existing tailcat keys appear as tailcat:NAME, alongside managed keys.
TAILCHASE_TAILCAT_KEYS_DIR overrides discovery; an empty value disables it.
Services are initially disabled. Empty allow selections deny all clients.
Aliases store a name, type (tc or nodekey), and value. Local keys belong to connections/services.
The remote subcommand remains a compatibility synonym for alias.
Exports contain all aliases and unnamed public values from Keys, encrypted with a password.
Export asks for a password twice; import asks once. Private keys and services are not exported.
key show reveals saved server addresses without starting serve (addresses are sensitive).
`

type options struct {
	pos   []string
	flags map[string]string
}

func parse(args []string) (options, error) {
	o := options{flags: map[string]string{}}
	bools := map[string]bool{"private": true, "dry-run": true, "allow-anyone": true, "anyone": true}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "--") {
			o.pos = append(o.pos, a)
			continue
		}
		k, v, eq := strings.Cut(strings.TrimPrefix(a, "--"), "=")
		if !eq {
			if bools[k] {
				v = "true"
			} else {
				i++
				if i >= len(args) {
					return o, fmt.Errorf("--%s requires a value", k)
				}
				v = args[i]
			}
		}

		if bools[k] && v != "true" && v != "false" {
			return o, fmt.Errorf("--%s must be true or false", k)
		}
		if _, ok := o.flags[k]; ok {
			return o, fmt.Errorf("duplicate flag --%s", k)
		}
		o.flags[k] = v
	}
	return o, nil
}
func (o options) check(n int, allowed ...string) error {
	if len(o.pos) != n {
		return errors.New("wrong number of arguments; see --help")
	}
	for k := range o.flags {
		found := false
		for _, a := range allowed {
			if k == a {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("unknown flag --%s", k)
		}
	}
	return nil
}
func split(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}
func Run(args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "help" {
		_, err := fmt.Fprint(out, help)
		return err
	}
	s, err := DefaultStore()
	if err != nil {
		return err
	}
	return runWithStore(s, args, in, out)
}

// CLI and TUI share command validation and mutation paths.
func runWithStore(s Store, args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		_, err := fmt.Fprint(out, help)
		return err
	}
	var err error
	command := args[0]
	if command == "alias" {
		command = "remote"
	}
	args = args[1:]
	if command == "tui" {
		if len(args) > 0 {
			return errors.New("tui takes no arguments")
		}
		return s.tui(in, out)
	}
	if command == "daemon" {
		if len(args) > 0 {
			return errors.New("daemon takes no arguments")
		}
		return s.daemon()
	}
	o, err := parse(args)
	if err != nil {
		return err
	}
	switch command {
	case "key", "remote", "service":
		if len(o.pos) < 1 {
			return errors.New("subcommand required")
		}
		sub := o.pos[0]
		o.pos = o.pos[1:]
		switch command {
		case "key":
			return s.keyCommand(sub, o, out)
		case "remote":
			if sub == "delete" {
				if err := o.check(1); err != nil {
					return err
				}
				return s.deleteAlias(o.pos[0])
			}
			if sub == "show" {
				if err = o.check(1); err != nil {
					return err
				}
				c, e := s.Load()
				if e != nil {
					return e
				}
				r, ok := c.Remotes[o.pos[0]]
				if !ok {
					return errors.New("alias not found")
				}
				return json.NewEncoder(out).Encode(r)
			}
			if sub == "list" {
				if err = o.check(0); err != nil {
					return err
				}
				c, e := s.Load()
				if e != nil {
					return e
				}
				for _, n := range names(c.Remotes) {
					r := c.Remotes[n]
					value := aliasValue(c, r)
					fmt.Fprintf(out, "%s\t%s\t%s\n", n, aliasType(r), value)
				}
				return nil
			}
			if sub != "add" && sub != "set" {
				return errors.New("unknown remote command")
			}
			if err = o.check(1, "name", "type", "value", "address", "client", "server", "peer", "peer-key"); err != nil {
				return err
			}
			if sub == "set" && len(o.flags) == 0 {
				return errors.New("provide at least one field to update")
			}
			if _, a := o.flags["peer"]; a {
				if _, b := o.flags["peer-key"]; b {
					return errors.New("choose --peer or --peer-key, not both")
				}
			}
			return s.Update(func(c *Config) error {
				name := o.pos[0]
				r, exists := c.Remotes[name]
				if sub == "add" && exists {
					return fmt.Errorf("alias exists: %s", name)
				}
				if sub == "set" && !exists {
					return errors.New("alias not found")
				}
				newName := name
				if value, renaming := o.flags["name"]; renaming {
					if sub != "set" {
						return errors.New("--name is only supported by alias set")
					}
					newName = value
					if !validName.MatchString(newName) {
						return fmt.Errorf("invalid alias name %q", newName)
					}
					if _, exists := c.Remotes[newName]; exists && newName != name {
						return fmt.Errorf("alias exists: %s", newName)
					}
					if len(o.flags) == 1 {
						return renameAlias(c, name, newName)
					}
				}
				_, hasType := o.flags["type"]
				_, hasValue := o.flags["value"]
				if hasType || hasValue {
					for _, flag := range []string{"address", "client", "server", "peer", "peer-key"} {
						if _, ok := o.flags[flag]; ok {
							return errors.New("use only --type and --value for an alias")
						}
					}
					next := Remote{Type: aliasType(r), Value: aliasValue(*c, r)}
					if hasType {
						next.Type = o.flags["type"]
					}
					if hasValue {
						next.Value = strings.TrimSpace(o.flags["value"])
					}
					if next.Type == aliasNodekey {
						next.Value = normalizePeerKey(next.Value)
					}
					if e := validateAlias(next); e != nil {
						return e
					}
					if exists {
						if e := checkPeerChange(*c, name, next); e != nil {
							return e
						}
					}
					c.Remotes[name] = next
					return renameAlias(c, name, newName)
				}
				if len(o.flags) == 0 {
					return errors.New("alias requires --type tc|nodekey and --value")
				}
				// Backward-compatible flags operate on the old representation,
				// then save converts it into independent typed aliases.
				if r.Type == aliasTC {
					r = Remote{Address: r.Value}
				} else if r.Type == aliasNodekey {
					r = Remote{PeerPublic: r.Value}
				}
				if v, ok := o.flags["address"]; ok {
					r.Address = v
				}
				if v, ok := o.flags["client"]; ok {
					r.Client = resolveIdentity(*c, v)
				}
				if v, ok := o.flags["server"]; ok {
					r.Server = resolveIdentity(*c, v)
				}
				if v, ok := o.flags["peer"]; ok {
					r.Peer = resolveIdentity(*c, v)
					r.PeerPublic = ""
				}
				if v, ok := o.flags["peer-key"]; ok {
					r.Peer = ""
					r.PeerPublic = normalizePeerKey(v)
				}
				if r.Peer != "" {
					k, ok := c.Keys[r.Peer]
					if !ok || !isPeerIdentity(k) {
						return fmt.Errorf("peer identity %s not found or has no usable public key", r.Peer)
					}
				}
				if r.PeerPublic != "" && !validPublic(r.PeerPublic) {
					return fmt.Errorf("alias %s: peer key must be nodekey: followed by 64 hexadecimal digits", name)
				}
				if exists {
					if err := checkPeerChange(*c, name, r); err != nil {
						return err
					}
				}
				c.Remotes[name] = r
				return renameAlias(c, name, newName)
			})

		case "service":
			if sub == "show" {
				if err = o.check(1); err != nil {
					return err
				}
				c, e := s.Load()
				if e != nil {
					return e
				}
				v, ok := c.Services[o.pos[0]]
				if !ok {
					return errors.New("service not found")
				}
				address := ""
				if r, e := s.runtime(); e == nil && v.Enabled && r.Services[o.pos[0]].State == "running" {
					address = r.Addresses[o.pos[0]]
				}
				return json.NewEncoder(out).Encode(struct {
					Service Service `json:"service"`
					Address string  `json:"address,omitempty"`
				}{v, address})
			}
			if sub != "add" {
				return errors.New("unknown service command")
			}
			if err = o.check(1, "kind", "remote", "client-key", "server-key", "ports", "bind", "allow", "allow-anyone", "ssh-authorized-keys"); err != nil {
				return err
			}
			return s.Update(func(c *Config) error {
				n := o.pos[0]
				if _, ok := c.Services[n]; ok {
					return fmt.Errorf("service exists: %s", n)
				}
				c.Services[n] = Service{Kind: o.flags["kind"], Remote: o.flags["remote"], ClientKey: resolveIdentity(*c, o.flags["client-key"]), ServerKey: resolveIdentity(*c, o.flags["server-key"]), Ports: split(o.flags["ports"]), Bind: o.flags["bind"], Allow: split(o.flags["allow"]), AllowAnyone: o.flags["allow-anyone"] == "true", SSHAuthorizedKeys: o.flags["ssh-authorized-keys"]}
				return nil
			})
		}
	case "allow":
		if len(o.pos) == 0 {
			return errors.New("service required")
		}
		for k := range o.flags {
			if k != "anyone" {
				return fmt.Errorf("unknown flag --%s", k)
			}
		}
		return s.Update(func(c *Config) error {
			v, ok := c.Services[o.pos[0]]
			if !ok || v.Kind != "serve" {
				return errors.New("serve service required")
			}
			v.Allow = append([]string(nil), o.pos[1:]...)
			v.AllowAnyone = o.flags["anyone"] == "true"
			c.Services[o.pos[0]] = v
			return nil
		})
	case "enable", "disable":
		if err = o.check(1); err != nil {
			return err
		}
		return s.Update(func(c *Config) error {
			v, ok := c.Services[o.pos[0]]
			if !ok {
				return errors.New("service not found")
			}
			if command == "enable" {
				if _, e := s.command(*c, v); e != nil {
					return e
				}
			}
			v.Enabled = command == "enable"
			c.Services[o.pos[0]] = v
			return nil
		})
	case "export":
		if err = o.check(0, "output"); err != nil {
			return err
		}
		if o.flags["output"] == "" {
			return errors.New("--output required")
		}
		password, confirmation, err := readBundlePasswords(in, out, true)
		if err != nil {
			return err
		}
		count, err := s.exportAliases(o.flags["output"], password, confirmation)
		if err == nil {
			fmt.Fprintf(out, "Exported %d alias entries (encrypted).\n", count)
		}
		return err
	case "import":
		if err = o.check(1, "dry-run"); err != nil {
			return err
		}
		password, _, err := readBundlePasswords(in, out, false)
		if err != nil {
			return err
		}
		dryRun := o.flags["dry-run"] == "true"
		summary, err := s.importAliases(o.pos[0], password, dryRun)
		if err != nil {
			return err
		}
		if dryRun {
			fmt.Fprintln(out, "Import validated; no changes.", summary)
		} else {
			fmt.Fprintln(out, "Imported aliases:", summary)
		}
		return nil
	case "status":
		if err = o.check(0); err != nil {
			return err
		}
		return s.status(out)
	case "ssh":
		if err = o.check(1, "user", "client-key"); err != nil {
			return err
		}
		cmd, cleanup, e := s.prepareSSHWithIdentity(o.pos[0], o.flags["user"], o.flags["client-key"])
		if e != nil {
			return e
		}
		defer cleanup()
		cmd.Stdin = in
		cmd.Stdout = out
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	return fmt.Errorf("unknown command %s; see --help", command)
}
func (s Store) materialize(k Identity) (string, func(), error) {
	if len(k.Private) == 0 {
		return "", func() {}, errors.New("private identity unavailable")
	}
	if err := os.MkdirAll(s.Dir, 0700); err != nil {
		return "", func() {}, err
	}
	f, e := os.CreateTemp(s.Dir, ".key-*.private.json")
	if e != nil {
		return "", func() {}, e
	}
	cleanup := func() { os.Remove(f.Name()) }
	_, e = f.Write(k.Private)
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		cleanup()
	}
	return f.Name(), cleanup, e
}
func (s Store) keyCommand(sub string, o options, out io.Writer) error {
	if sub == "delete" {
		if e := o.check(1); e != nil {
			return e
		}
		return s.Update(func(c *Config) error {
			name := resolveIdentity(*c, o.pos[0])
			k, ok := c.Keys[name]
			if !ok {
				return errors.New("identity not found")
			}
			if k.Source == "tailcat" {
				return errors.New("native tailcat keys are read-only here; delete them with tailcat genkey --delete --key=NAME")
			}
			used := []string{}
			for _, alias := range names(c.Remotes) {
				r := c.Remotes[alias]
				if r.Client == name || r.Server == name || r.Peer == name {
					used = append(used, alias)
				}
			}
			if len(used) > 0 {
				return fmt.Errorf("identity %s is used by aliases: %s", name, strings.Join(used, ", "))
			}
			services := []string{}
			for _, id := range names(c.Services) {
				v := c.Services[id]
				if v.Kind == "serve" && serverIdentity(*c, v) == name || v.ClientKey == name {
					services = append(services, id)
				}
			}
			if len(services) > 0 {
				return fmt.Errorf("identity %s is used by services: %s", name, strings.Join(services, ", "))
			}
			delete(c.Keys, name)
			return nil
		})
	}

	if sub == "list" {
		if e := o.check(0); e != nil {
			return e
		}
		c, e := s.Load()
		if e != nil {
			return e
		}
		for _, warning := range c.Warnings {
			fmt.Fprintln(out, "warning:", warning)
		}
		for _, n := range names(c.Keys) {
			k := c.Keys[n]
			fmt.Fprintf(out, "%s\t%s\t%s\tsource=%s\ttype=%s\tprivate=%t\n", n, k.Role, fingerprint(k), keySource(k), identityKind(k), len(k.Private) > 0)
		}
		return nil
	}
	if sub == "show" {
		if e := o.check(1, "private"); e != nil {
			return e
		}
		c, e := s.Load()
		if e != nil {
			return e
		}
		k, ok := c.Keys[resolveIdentity(c, o.pos[0])]
		if !ok {
			return errors.New("identity not found")
		}
		if o.flags["private"] == "true" && len(k.Private) == 0 {
			return errors.New("identity has no available private key")
		}
		preview := savedKeyAddress(k)
		if o.flags["private"] != "true" {
			k.Private = nil
		}
		return json.NewEncoder(out).Encode(struct {
			Identity
			addressPreview
		}{k, preview})
	}
	if sub != "add" && sub != "generate" {
		return errors.New("unknown key command")
	}
	allowed := []string{"role", "file", "public"}
	if sub == "generate" {
		allowed = []string{"role"}
	}
	if e := o.check(1, allowed...); e != nil {
		return e
	}
	if _, ok := o.flags["public"]; ok {
		return errors.New("public nodekeys belong in Alias: use alias add NAME --type nodekey --value nodekey:HEX")
	}
	role := o.flags["role"]
	if role != "server" && role != "client" {
		return errors.New("--role must be server or client")
	}
	return s.Update(func(c *Config) error {
		name := o.pos[0]
		if !validName.MatchString(name) {
			return errors.New("invalid identity name")
		}
		if _, ok := c.Keys[name]; ok {
			return errors.New("identity already exists")
		}

		var b []byte
		var e error
		if sub == "generate" {
			dir, e := os.MkdirTemp(s.Dir, ".generate-")
			if e != nil {
				return e
			}
			defer os.RemoveAll(dir)
			p := filepath.Join(dir, "key.private.json")
			args := []string{"genkey", "--key=" + p}
			if role == "client" {
				args = append(args, "--client")
			}
			if e = exec.Command("tailcat", args...).Run(); e != nil {
				return errors.New("tailcat key generation failed")
			}
			b, e = os.ReadFile(p)
			if e != nil {
				return e
			}
		} else {
			if o.flags["file"] == "" {
				return errors.New("--file required")
			}
			b, e = os.ReadFile(o.flags["file"])
			if e != nil {
				return e
			}
		}
		k := Identity{Role: role, Private: json.RawMessage(b)}
		pub, err := privatePublic(k.Private)
		if err != nil {
			return err
		}
		k.Public = pub
		if !json.Valid(b) {
			return errors.New("key file is not JSON")
		}
		if role == "client" {
			p, clean, e := s.materialize(k)
			if e != nil {
				return e
			}
			defer clean()
			pub, e := exec.Command("tailcat", "--key="+p, "printpub").Output()
			if e != nil {
				return errors.New("tailcat rejected client key")
			}
			k.Public = strings.TrimSpace(string(pub))
		}
		for n, old := range c.Keys {
			if old.Source != "tailcat" && samePrivate(k.Private, old.Private) {
				return fmt.Errorf("key already stored as %s", n)
			}
		}
		c.Keys[name] = k
		return nil
	})
}

func (s Store) prepareSSH(alias, user string) (*exec.Cmd, func(), error) {
	return s.prepareSSHWithIdentity(alias, user, "")
}
func (s Store) prepareSSHWithIdentity(alias, user, client string) (*exec.Cmd, func(), error) {
	noop := func() {}
	c, e := s.Load()
	if e != nil {
		return nil, noop, e
	}
	r, ok := c.Remotes[alias]
	if !ok || aliasAddress(r) == "" {
		return nil, noop, errors.New("TC address alias required")
	}
	if user != "" && !validName.MatchString(user) {
		return nil, noop, errors.New("invalid SSH user")
	}
	if client != "" {
		r.Client = resolveIdentity(c, client)
	}
	name, e := s.clientIdentity(c, r)
	if e != nil {
		return nil, noop, e
	}
	keyFlag := "--key=new"
	cleanup := noop
	if name != "" {
		var path string
		path, cleanup, e = s.materialize(c.Keys[name])
		if e != nil {
			return nil, noop, e
		}
		keyFlag = "--key=" + path
	}

	target := aliasAddress(r)
	if user != "" {
		target = user + "@" + target
	}
	return exec.Command("tailcat", "ssh", keyFlag, target), cleanup, nil
}
