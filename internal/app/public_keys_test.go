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

func TestPublicKeyCatalogMigratesToNodekeyAlias(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	pub := testIdentity("client", 42).Public
	c := empty()
	c.Keys["laptop-key"] = Identity{Role: "client", Public: pub}
	path := filepath.Join(s.Dir, "config.json")
	original, _ := json.Marshal(c)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Keys) != 0 || loaded.Remotes["laptop-key"] != (Remote{Type: aliasNodekey, Value: pub}) {
		t.Fatal("public key not moved to Alias")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(original, after) {
		t.Fatal("Load wrote migration")
	}
	var list bytes.Buffer
	if err := runWithStore(s, []string{"key", "list"}, strings.NewReader(""), &list); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(list.String(), "laptop-key") {
		t.Fatal("public key remains in Keys list")
	}
	var aliases bytes.Buffer
	if err := runWithStore(s, []string{"alias", "list"}, strings.NewReader(""), &aliases); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(aliases.String(), "laptop-key\tnodekey\t"+pub) {
		t.Fatal("public value lost")
	}
	if err := s.Update(func(c *Config) error { return nil }); err != nil {
		t.Fatal(err)
	}
	again, err := s.Load()
	if err != nil || len(again.Keys) != 0 || again.Remotes["laptop-key"].Value != pub {
		t.Fatal("migration not durable", err)
	}
}

func TestKeysFormOffersOnlyLocalGenerationOrFile(t *testing.T) {
	m := newTUI(Store{Dir: t.TempDir()}, empty())
	m.tab = 2
	m, _ = press(m, 'i')
	if m.form == nil || m.form.operation != "key.create" {
		t.Fatal("Keys add missing")
	}
	for _, f := range m.form.fields {
		if f.name == "public" {
			t.Fatal("public field remains in Keys")
		}
		if f.name == "type" && !reflect.DeepEqual(f.choices, []string{"generate", "file"}) {
			t.Fatal("unexpected key creation choices", f.choices)
		}
	}
	setField(t, m.form, "name", "local")
	setField(t, m.form, "type", "public")
	if _, err := m.form.arguments(); err == nil {
		t.Fatal("hidden public mode still works")
	}
	if _, err := m.operationForm("key.public"); err == nil {
		t.Fatal("public form still available")
	}
	for _, op := range uiOperations {
		if op.id == "key.public" || strings.Contains(op.label, "public nodekey") {
			t.Fatal("public action remains")
		}
	}
	if strings.Contains(vimHelp, ":public") {
		t.Fatal("public shortcut remains in help")
	}
	m.form = nil
	if strings.Contains(m.View().Content, ":public") {
		t.Fatal("public shortcut remains in footer")
	}
}

func TestInlinePeerMigrationPreservesAuthorizationAndIsReadOnly(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	pub := testIdentity("client", 44).Public
	c := empty()
	c.Keys["host"] = testIdentity("server", 45)
	c.Remotes["laptop"] = Remote{PeerPublic: pub}
	c.Services["local"] = Service{Kind: "serve", ServerKey: "host", Ports: []string{"80"}, Allow: []string{"laptop"}}
	original, _ := json.Marshal(c)
	path := filepath.Join(s.Dir, "config.json")
	if e := os.WriteFile(path, original, 0600); e != nil {
		t.Fatal(e)
	}
	loaded, e := s.Load()
	if e != nil {
		t.Fatal(e)
	}
	if loaded.Remotes["laptop"].Type != aliasNodekey || loaded.Remotes["laptop"].Value != pub || loaded.Remotes["laptop"].PeerPublic != "" {
		t.Fatal("legacy peer not normalized into public key record")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(original, after) {
		t.Fatal("reading configuration wrote a migration")
	}
	if c.Remotes["laptop"].PeerPublic != pub || len(c.Keys) != 1 {
		t.Fatal("migration mutated a caller's snapshot")
	}
	args, e := s.command(loaded, loaded.Services["local"])
	if e != nil || !contains(args, "--allow="+pub) {
		t.Fatal("migration changed allow authorization", e, args)
	}
	if e = s.Update(func(c *Config) error { return nil }); e != nil {
		t.Fatal(e)
	}
	saved, _ := os.ReadFile(path)
	if bytes.Contains(saved, []byte("peer_public_key")) {
		t.Fatal("legacy inline public key persisted again")
	}
	again, e := s.Load()
	if e != nil || again.Remotes["laptop"].Type != aliasNodekey || peerPublic(again, "laptop") != pub {
		t.Fatal("migration not stable across saves", e)
	}
}
func TestLegacyAliasesCopyValuesWithoutChangingCatalog(t *testing.T) {
	pub := testIdentity("client", 46).Public
	c := empty()
	c.Keys["peer-a"] = testIdentity("client", 47)
	c.Remotes["a"] = Remote{PeerPublic: pub}
	c.Remotes["z"] = Remote{PeerPublic: pub}
	migrated := normalizeAliases(c)
	if migrated.Remotes["a"] != (Remote{Type: aliasNodekey, Value: pub}) || migrated.Remotes["z"] != migrated.Remotes["a"] {
		t.Fatal("legacy values not preserved")
	}
	if len(migrated.Keys) != 1 || migrated.Keys["peer-a"].Public != c.Keys["peer-a"].Public {
		t.Fatal("alias migration modified the Keys catalog")
	}
}

func TestPeerOnlyBundleIncludesPublicValuesWithoutPrivateMaterial(t *testing.T) {
	c := empty()
	key := testIdentity("client", 48)
	c.Keys["identity"] = key
	c.Remotes["peer"] = Remote{Peer: "identity"}
	b, err := exportBundle(c)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(b)
	if bytes.Contains(data, []byte("privkey:")) || bytes.Contains(data, []byte("native_name")) {
		t.Fatal("export leaked key material or local metadata")
	}
	dst := empty()
	if _, err := mergeBundle(&dst, b); err != nil {
		t.Fatal(err)
	}
	if len(dst.Keys) != 0 || peerPublic(dst, "peer") != key.Public {
		t.Fatal("public transfer changed keys or lost alias")
	}
}

func TestNativePeerReferencePersistsOnlyPublicMetadata(t *testing.T) {
	s := nativeTestStore(t)
	writeNativeFixture(t, s, "client-default", "client", 49)
	if e := runWithStore(s, []string{"remote", "add", "self", "--peer", "tailcat:client-default"}, strings.NewReader(""), &bytes.Buffer{}); e != nil {
		t.Fatal(e)
	}
	raw, _ := os.ReadFile(filepath.Join(s.Dir, "config.json"))
	if bytes.Contains(raw, []byte("privkey:")) {
		t.Fatal("native peer private copied")
	}
	c, e := s.Load()
	if e != nil || c.Remotes["self"].Type != aliasNodekey || peerPublic(c, "self") == "" {
		t.Fatal("native peer metadata not retained", e)
	}
}
func TestPublicRegistrationValidationAndUnknownPeer(t *testing.T) {
	for _, args := range [][]string{{"key", "add", "bad", "--public", "privkey:" + strings.Repeat("a", 64)}, {"key", "add", "bad", "--public", "nodekey:bad"}, {"key", "add", "bad", "--public", testIdentity("client", 50).Public, "--file", "private.json"}, {"remote", "add", "bad", "--peer", "missing"}} {
		s := Store{Dir: t.TempDir()}
		if e := runWithStore(s, args, strings.NewReader(""), &bytes.Buffer{}); e == nil {
			t.Fatal("invalid public registration or reference accepted", args[0])
		}
		if _, e := os.Stat(filepath.Join(s.Dir, "config.json")); !os.IsNotExist(e) {
			t.Fatal("invalid registration wrote config")
		}
	}
}

func TestPublicRegistrationPointsToAliasWithoutWriting(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	pub := testIdentity("client", 53).Public
	err := runWithStore(s, []string{"key", "add", "peer", "--public", pub}, strings.NewReader(""), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "alias add NAME --type nodekey") {
		t.Fatal("missing Alias guidance", err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "config.json")); !os.IsNotExist(err) {
		t.Fatal("rejected registration wrote config")
	}
}

func TestPublicKeyMigrationPreservesLocalMissingCredentialsAndCollisions(t *testing.T) {
	c := empty()
	pub := testIdentity("client", 55).Public
	c.Keys["peer"] = Identity{Role: "client", Public: pub}
	c.Keys["missing"] = Identity{Role: "client", Public: testIdentity("client", 56).Public}
	c.Keys["private"] = testIdentity("server", 57)
	c.Remotes["peer"] = Remote{Type: aliasTC, Value: "tc-existing"}
	c.Remotes["peer-nodekey"] = Remote{Type: aliasNodekey, Value: testIdentity("client", 58).Public}
	c.Services["fwd"] = Service{Kind: "forward", Remote: "peer", ClientKey: "missing", Ports: []string{"80"}}
	migrated := normalizeAliases(c)
	if migrated.Remotes["peer-nodekey-2"].Value != pub || migrated.Remotes["peer"] != c.Remotes["peer"] {
		t.Fatal("collision overwrote an alias")
	}
	if _, ok := migrated.Keys["peer"]; ok {
		t.Fatal("public catalog record remains")
	}
	if !reflect.DeepEqual(migrated.Keys["missing"], c.Keys["missing"]) {
		t.Fatal("missing local credential metadata lost")
	}
	if identityKind(migrated.Keys["missing"]) != "unavailable" {
		t.Fatal("missing local key presented as public catalog")
	}
	if !samePrivate(migrated.Keys["private"].Private, c.Keys["private"].Private) {
		t.Fatal("private key changed")
	}
	if len(c.Keys) != 3 || len(c.Remotes) != 2 {
		t.Fatal("migration mutated input snapshot")
	}
	if !reflect.DeepEqual(migrated, normalizeAliases(migrated)) {
		t.Fatal("migration not idempotent")
	}
	if _, err := (Store{}).command(migrated, migrated.Services["fwd"]); err == nil {
		t.Fatal("missing private key silently replaced")
	}
}

func TestLegacyPublicServerAnnotationCanAuthorizePeerButNotServe(t *testing.T) {
	c := empty()
	c.Keys["old-public"] = Identity{Role: "server", Public: testIdentity("client", 54).Public}
	c.Remotes["peer"] = Remote{Peer: "old-public"}
	if e := c.Validate(); e != nil {
		t.Fatal("legacy public annotation blocks roleless peer identity", e)
	}
	m := newTUI(Store{Dir: t.TempDir()}, c)
	f, e := m.operationForm("remote.add")
	if e != nil {
		t.Fatal(e)
	}
	for _, field := range f.fields {
		if field.name == "peer" && !contains(field.choices, "old-public") {
			t.Fatal("public server annotation hidden from peer selector")
		}
	}
	f, e = m.operationForm("service.add")
	if e != nil {
		t.Fatal(e)
	}
	for _, field := range f.fields {
		if field.name == "server-key" && contains(field.choices, "old-public") {
			t.Fatal("public-only key offered as local server credential")
		}
	}
}

func TestPublicKeyMigrationReusesAliasesAndSanitizesPortableNames(t *testing.T) {
	c := empty()
	pub := testIdentity("client", 59).Public
	c.Keys["duplicate"] = Identity{Role: "client", Public: pub}
	c.Keys["tailcat:my public.key"] = Identity{Role: "client", Public: testIdentity("client", 60).Public}
	c.Remotes["laptop"] = Remote{Type: aliasNodekey, Value: pub}
	migrated := normalizeAliases(c)
	if len(migrated.Keys) != 0 || len(migrated.Remotes) != 2 {
		t.Fatal("duplicate alias or public catalog remains")
	}
	if migrated.Remotes["laptop"].Value != pub || migrated.Remotes["tailcat-my-public-key"].Value != c.Keys["tailcat:my public.key"].Public {
		t.Fatal("migration changed values")
	}
	if err := migrated.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestKeysAddImportsPrivateFile(t *testing.T) {
	m := newTUI(Store{Dir: t.TempDir()}, empty())
	m.tab = 2
	m, _ = press(m, 'i')
	key := testIdentity("server", 61)
	path := filepath.Join(t.TempDir(), "local.private.json")
	if err := os.WriteFile(path, key.Private, 0600); err != nil {
		t.Fatal(err)
	}
	setField(t, m.form, "name", "local")
	setField(t, m.form, "role", "server")
	setField(t, m.form, "type", "file")
	setField(t, m.form, "file", path)
	m = runUIForm(t, m)
	if !samePrivate(m.config.Keys["local"].Private, key.Private) || len(m.config.Remotes) != 0 {
		t.Fatal("Keys import lost private material or created Alias")
	}
	m.result = nil
	m.form = nil
	m, _ = typeVim(t, m, ":public\n")
	if m.form != nil || m.busy {
		t.Fatal("removed public shortcut still executes")
	}
}
