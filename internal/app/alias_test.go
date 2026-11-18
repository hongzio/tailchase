package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestTypedAliasFormsAndIndependentValues(t *testing.T) {
	m := newTUI(Store{Dir: t.TempDir()}, empty())
	m.tab = 1
	m = openUIOperation(t, m, "remote.add")
	fields := []string{}
	for _, f := range m.form.fields {
		fields = append(fields, f.name)
	}
	if !reflect.DeepEqual(fields, []string{"name", "type", "value"}) {
		t.Fatal(fields)
	}
	pub := testIdentity("client", 81).Public
	setField(t, m.form, "name", "laptop")
	setField(t, m.form, "type", aliasNodekey)
	setField(t, m.form, "value", pub)
	m = runUIForm(t, m)
	if len(m.config.Keys) != 0 || m.config.Remotes["laptop"] != (Remote{Type: aliasNodekey, Value: pub}) {
		t.Fatal("alias coupled to catalog identity")
	}
	if !strings.Contains(m.row("laptop"), "laptop · nodekey · "+pub) {
		t.Fatal("row does not show name/type/value")
	}
	m.result = nil
	m, _ = press(m, 'e')
	if m.form.target != "laptop" || len(m.form.fields) != 3 {
		t.Fatal("editing requires alias reselection or unrelated fields")
	}
}

func TestTypedAliasUseIsRestrictedByType(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	pub := testIdentity("client", 82).Public
	if err := s.Update(func(c *Config) error {
		c.Keys["host"] = testIdentity("server", 83)
		c.Remotes["server"] = Remote{Type: aliasTC, Value: "tc-example"}
		c.Remotes["laptop"] = Remote{Type: aliasNodekey, Value: pub}
		c.Services["serve"] = Service{Kind: "serve", ServerKey: "host", Ports: []string{"80"}, Allow: []string{"laptop"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	c, _ := s.Load()
	m := newTUI(s, c)
	f, _ := m.operationForm("service.add")
	for _, field := range f.fields {
		if field.name == "remote" && !reflect.DeepEqual(field.choices, []string{"server"}) {
			t.Fatal("non-TC forward choice", field.choices)
		}
		if field.name == "allow" && !reflect.DeepEqual(field.choices, []string{"laptop"}) {
			t.Fatal("non-nodekey allow choice", field.choices)
		}
	}
	f, _ = m.operationForm("ssh")
	if !reflect.DeepEqual(f.fields[0].choices, []string{"server"}) {
		t.Fatal("SSH accepts nodekey")
	}
	if _, cleanup, err := s.prepareSSH("laptop", ""); err == nil {
		cleanup()
		t.Fatal("SSH accepted nodekey")
	}
	if _, err := s.command(c, Service{Kind: "forward", Remote: "laptop", Ports: []string{"80"}}); err == nil {
		t.Fatal("forward accepted nodekey")
	}
	args, err := s.command(c, c.Services["serve"])
	if err != nil || !contains(args, "--allow="+pub) {
		t.Fatal(args, err)
	}
	before, _ := os.ReadFile(filepath.Join(s.Dir, "config.json"))
	for _, args := range [][]string{
		{"allow", "serve", "server"},
		{"alias", "set", "laptop", "--type", "tc", "--value", "tc-other"},
		{"alias", "add", "bad", "--type", "tc", "--value", pub},
		{"alias", "add", "bad", "--type", "nodekey", "--value", "tc-example"},
		{"alias", "add", "empty", "--type", "tc", "--value", ""},
		{"alias", "add", "mixed", "--type", "nodekey", "--value", pub, "--client", "host"},
	} {
		if err := runWithStore(s, args, strings.NewReader(""), &bytes.Buffer{}); err == nil {
			t.Fatal("invalid operation accepted", args[0:2])
		}
		after, _ := os.ReadFile(filepath.Join(s.Dir, "config.json"))
		if !bytes.Equal(before, after) {
			t.Fatal("failed operation wrote config")
		}
	}
}

func TestTypedAliasBundleNeedsNoIdentityCatalog(t *testing.T) {
	for _, r := range []Remote{{Type: aliasTC, Value: "tc-example"}, {Type: aliasNodekey, Value: testIdentity("client", 84).Public}} {
		c := empty()
		c.Remotes["original"] = r
		b, err := exportBundle(c)
		if err != nil {
			t.Fatal(err)
		}
		if len(b.Aliases) != 1 {
			t.Fatal("standalone alias unexpectedly exports keys")
		}
		raw, _ := json.Marshal(b)
		var decoded Bundle
		if err := decode(raw, &decoded); err != nil {
			t.Fatal(err)
		}
		dst := empty()
		if _, err := mergeBundle(&dst, decoded); err != nil {
			t.Fatal(err)
		}
		if dst.Remotes["original"] != r {
			t.Fatal("type/value changed on transfer")
		}
	}
}

func TestLegacyMixedAliasesMigrateWithoutChangingAuthorization(t *testing.T) {
	c := empty()
	c.Keys["client"] = testIdentity("client", 85)
	c.Keys["server"] = testIdentity("server", 86)
	pub := testIdentity("client", 87).Public
	c.Remotes["home"] = Remote{Address: "tc-old-address", Client: "client", Server: "server", PeerPublic: pub}
	c.Remotes["home-nodekey"] = Remote{Type: aliasNodekey, Value: testIdentity("client", 88).Public}
	c.Services["fwd"] = Service{Kind: "forward", Remote: "home", Ports: []string{"8080:80"}}
	c.Services["serve"] = Service{Kind: "serve", Remote: "home", Ports: []string{"80"}, Allow: []string{"home"}}
	original, _ := json.Marshal(c)
	s := Store{Dir: t.TempDir()}
	path := filepath.Join(s.Dir, "config.json")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Remotes["home"] != (Remote{Type: aliasTC, Value: "tc-old-address"}) {
		t.Fatal("TC alias not converted")
	}
	if loaded.Remotes["home-nodekey-2"] != (Remote{Type: aliasNodekey, Value: pub}) {
		t.Fatal("nodekey alias collision or lost value")
	}
	if loaded.Services["fwd"].ClientKey != "client" || loaded.Services["serve"].ServerKey != "server" || loaded.Services["serve"].Remote != "" {
		t.Fatal("local identities not moved to services")
	}
	if !reflect.DeepEqual(loaded.Services["serve"].Allow, []string{"home-nodekey-2"}) {
		t.Fatal("allow references lost")
	}
	args, err := s.command(loaded, loaded.Services["serve"])
	if err != nil || !contains(args, "--allow="+pub) {
		t.Fatal("authorization changed", args, err)
	}
	raw, _ := os.ReadFile(path)
	if !bytes.Equal(original, raw) {
		t.Fatal("Load wrote config")
	}
	clone := normalizeAliases(c)
	if c.Services["serve"].Allow[0] != "home" || c.Remotes["home"].PeerPublic != pub {
		t.Fatal("migration changed caller snapshot")
	}
	if !reflect.DeepEqual(clone, normalizeAliases(clone)) {
		t.Fatal("migration is not idempotent")
	}
	if err := s.Update(func(c *Config) error { return nil }); err != nil {
		t.Fatal(err)
	}
	saved, _ := os.ReadFile(path)
	var persisted struct {
		Remotes map[string]map[string]any `json:"remotes"`
	}
	if err := json.Unmarshal(saved, &persisted); err != nil {
		t.Fatal(err)
	}
	for name, record := range persisted.Remotes {
		if len(record) != 2 || record["type"] == nil || record["value"] == nil {
			t.Fatal("persisted alias contains fields beyond type/value", name)
		}
	}

	again, err := s.Load()
	if err != nil || !reflect.DeepEqual(loaded.Remotes, again.Remotes) {
		t.Fatal("migration unstable", err)
	}
}

func TestOneTCAliasSupportsIndependentLocalClientKeys(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if err := s.Update(func(c *Config) error {
		c.Keys["alice"] = testIdentity("client", 89)
		c.Keys["bob"] = testIdentity("client", 90)
		c.Remotes["server"] = Remote{Type: aliasTC, Value: "tc-example"}
		for _, name := range []string{"alice", "bob"} {
			c.Services[name] = Service{Kind: "forward", Remote: "server", ClientKey: name, Ports: []string{"80"}}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	c, _ := s.Load()
	for _, name := range []string{"alice", "bob"} {
		args, err := s.command(c, c.Services[name])
		if err != nil || !contains(args, "--key="+name) {
			t.Fatal(args, err)
		}
		cmd, cleanup, err := s.prepareSSHWithIdentity("server", "", name)
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(strings.TrimPrefix(cmd.Args[2], "--key="))
		cleanup()
		if err != nil || !samePrivate(data, c.Keys[name].Private) {
			t.Fatal("SSH ignored independent client identity", err)
		}
	}
}
