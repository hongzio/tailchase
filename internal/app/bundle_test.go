package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"charm.land/bubbles/v2/textinput"
)

func addressBookFixture(t *testing.T) Config {
	t.Helper()
	c := empty()
	k := testIdentity("server", 111)
	var raw map[string]any
	if err := json.Unmarshal(k.Private, &raw); err != nil {
		t.Fatal(err)
	}
	raw["Public"].(map[string]any)["RegionID"] = 1
	k.Private, _ = json.Marshal(raw)
	c.Keys["host"] = k
	c.Keys["local"] = testIdentity("client", 112)
	c.Remotes["office"] = Remote{Type: aliasTC, Value: "tc-office-secret"}
	c.Remotes["laptop"] = Remote{Type: aliasNodekey, Value: testIdentity("client", 113).Public}
	c.Services["web"] = Service{Kind: "serve", ServerKey: "host", Ports: []string{"80"}, Allow: []string{"laptop"}, Enabled: true}
	return c
}
func TestExportIncludesEveryAliasAndUnnamedKeyAddresses(t *testing.T) {
	c := addressBookFixture(t)
	b, err := exportBundle(c)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Aliases) != 5 {
		t.Fatal("missing named or unnamed entries", len(b.Aliases))
	}
	named, unnamed := map[string]BundleAlias{}, map[string]bool{}
	for _, a := range b.Aliases {
		if a.Name != "" {
			named[a.Name] = a
		} else {
			unnamed[bundleValueKey(a.Type, a.Value)] = true
		}
	}
	for name, r := range c.Remotes {
		if named[name] != (BundleAlias{Name: name, Type: r.Type, Value: r.Value}) {
			t.Fatal("named alias lost", name)
		}
	}
	if !unnamed[bundleValueKey(aliasTC, savedKeyAddress(c.Keys["host"]).Address)] || !unnamed[bundleValueKey(aliasNodekey, c.Keys["host"].Public)] || !unnamed[bundleValueKey(aliasNodekey, c.Keys["local"].Public)] {
		t.Fatal("Keys addresses/public identities not exported anonymously")
	}
	data, _ := json.Marshal(b)
	for _, secret := range []string{"privkey:", "private", "services", "host", "local", "source", "native_name"} {
		if bytes.Contains(data, []byte(`"`+secret+`"`)) {
			t.Fatal("unexpected local metadata", secret)
		}
	}
	if bytes.Contains(data, []byte("privkey:")) {
		t.Fatal("private key exported")
	}
}
func TestEncryptedBundleRoundTripRandomnessAndAuthentication(t *testing.T) {
	b, err := exportBundle(addressBookFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	password := " long unicode 비밀번호 "
	encrypted, err := encryptBundle(b, password)
	if err != nil {
		t.Fatal(err)
	}
	again, err := encryptBundle(b, password)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(encrypted, again) {
		t.Fatal("encryption reused randomness")
	}
	for _, a := range b.Aliases {
		if bytes.Contains(encrypted, []byte(a.Value)) {
			t.Fatal("plaintext address in encrypted file")
		}
	}
	if bytes.Contains(encrypted, []byte(password)) {
		t.Fatal("password in encrypted file")
	}
	decoded, err := decryptBundle(encrypted, password)
	if err != nil || !reflect.DeepEqual(decoded, b) {
		t.Fatal("round trip failed", err)
	}
	if _, err := decryptBundle(encrypted, "wrong"); err == nil {
		t.Fatal("wrong password accepted")
	}
	var envelope encryptedBundle
	if err := json.Unmarshal(encrypted, &envelope); err != nil {
		t.Fatal(err)
	}
	envelope.Payload[len(envelope.Payload)-1] ^= 1
	tampered, _ := json.Marshal(envelope)
	if _, err := decryptBundle(tampered, password); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
	envelope.Salt[0] ^= 1
	envelope.Payload[len(envelope.Payload)-1] ^= 1
	tampered, _ = json.Marshal(envelope)
	if _, err := decryptBundle(tampered, password); err == nil {
		t.Fatal("tampered salt accepted")
	}
}
func TestImportSkipsKeysPreservesNamesAndAssignsTemporaryNames(t *testing.T) {
	source := addressBookFixture(t)
	b, _ := exportBundle(source)
	dst := empty()
	dst.Keys = source.Keys
	dst.Remotes["office"] = Remote{Type: aliasTC, Value: "tc-local"}
	summary, err := mergeBundle(&dst, b)
	if err != nil {
		t.Fatal(err)
	}
	if summary.SkippedKeys != 3 || summary.Added != 2 || summary.Renamed != 1 || dst.Remotes["office"].Value != "tc-local" || dst.Remotes["office-2"].Value != "tc-office-secret" {
		t.Fatal("incorrect Keys filtering/name collision handling", summary)
	}
	if len(dst.Keys) != 2 || len(dst.Services) != 0 {
		t.Fatal("import changed local Keys/services")
	}
	again, err := mergeBundle(&dst, b)
	if err != nil || again.Added != 0 {
		t.Fatal("repeat import duplicated aliases", err)
	}
	fresh := empty()
	summary, err = mergeBundle(&fresh, b)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Added != 5 || len(fresh.Keys) != 0 || len(fresh.Services) != 0 {
		t.Fatal("address book imported credential/service records", summary)
	}
	for name, r := range fresh.Remotes {
		if name == "" || !validName.MatchString(name) {
			t.Fatal("unnamed entry has no usable temporary name")
		}
		if _, named := source.Remotes[name]; !named && !strings.HasPrefix(name, "unnamed-") {
			t.Fatal("temporary naming not applied")
		}
		if r.Type != "tc" && r.Type != "nodekey" {
			t.Fatal("invalid imported type")
		}
	}
}
func TestImportKeepsDistinctNamedAliasesSharingAValue(t *testing.T) {
	b := Bundle{Version: bundleVersion, Aliases: []BundleAlias{{Name: "one", Type: aliasTC, Value: "tc-same"}, {Name: "two", Type: aliasTC, Value: "tc-same"}, {Type: aliasTC, Value: "tc-same"}}}
	dst := empty()
	summary, err := mergeBundle(&dst, b)
	if err != nil || len(dst.Remotes) != 2 || summary.SkippedAliases != 1 {
		t.Fatal("named aliases lost or duplicate unnamed alias added", summary, err)
	}
}
func TestEncryptedFileFailuresPreserveFilesAndConfig(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	source := addressBookFixture(t)
	if err := s.Update(func(c *Config) error { *c = source; return nil }); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "aliases.enc")
	if _, err := s.exportAliases(path, "pass", "different"); err == nil {
		t.Fatal("mismatched confirmation accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("mismatch created file")
	}
	count, err := s.exportAliases(path, "pass", "pass")
	if err != nil || count != 5 {
		t.Fatal(count, err)
	}
	encrypted, _ := os.ReadFile(path)
	if _, err := s.exportAliases(path, "pass", "pass"); err == nil {
		t.Fatal("existing export overwritten")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(encrypted, after) {
		t.Fatal("existing encrypted file changed")
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("export permissions", err)
	}
	configPath := filepath.Join(s.Dir, "config.json")
	before, _ := os.ReadFile(configPath)
	if _, err := s.importAliases(path, "wrong", false); err == nil {
		t.Fatal("wrong password imported")
	}
	after, _ = os.ReadFile(configPath)
	if !bytes.Equal(before, after) {
		t.Fatal("failed import modified config")
	}
	missing := Store{Dir: filepath.Join(t.TempDir(), "missing")}
	if _, err := missing.importAliases(path, "wrong", false); err == nil {
		t.Fatal("wrong password accepted")
	}
	if _, err := os.Stat(missing.Dir); !os.IsNotExist(err) {
		t.Fatal("decryption failure created store")
	}
	if _, err := missing.importAliases(path, "pass", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(missing.Dir); !os.IsNotExist(err) {
		t.Fatal("dry run created store")
	}
	summary, err := missing.importAliases(path, "pass", false)
	if err != nil || summary.Added != 5 {
		t.Fatal(summary, err)
	}
}
func TestBundleValidationIsAtomicAndRejectsUnsupportedInputs(t *testing.T) {
	c := empty()
	c.Remotes["keep"] = Remote{Type: aliasTC, Value: "tc-keep"}
	original, _ := json.Marshal(c)
	for _, bad := range []Bundle{
		{Version: 1, Aliases: []BundleAlias{}},
		{Version: bundleVersion},
		{Version: bundleVersion, Aliases: []BundleAlias{{Name: "good", Type: aliasTC, Value: "tc-good"}, {Name: "bad", Type: aliasNodekey, Value: "bad"}}},
		{Version: bundleVersion, Aliases: []BundleAlias{{Name: "bad name", Type: aliasTC, Value: "tc-good"}}},
		{Version: bundleVersion, Aliases: []BundleAlias{{Name: "dup", Type: aliasTC, Value: "tc-a"}, {Name: "dup", Type: aliasTC, Value: "tc-b"}}},
	} {
		if _, err := mergeBundle(&c, bad); err == nil {
			t.Fatal("invalid bundle accepted")
		}
		after, _ := json.Marshal(c)
		if !bytes.Equal(original, after) {
			t.Fatal("failed merge partially changed aliases")
		}
	}
	for _, raw := range [][]byte{[]byte(`{"version":1,"config":{}}`), []byte(`{"format":"unknown"}`), bytes.Repeat([]byte("x"), maxBundleFileBytes+1)} {
		if _, err := decryptBundle(raw, "pass"); err == nil {
			t.Fatal("invalid encrypted format accepted")
		}
	}
}
func TestCLIPasswordPromptsAndDryRun(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if err := s.Update(func(c *Config) error { *c = addressBookFixture(t); return nil }); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "aliases.enc")
	var out bytes.Buffer
	if err := runWithStore(s, []string{"export", "--output", path}, strings.NewReader("cli-secret\ncli-secret\n"), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Confirm password:") || strings.Contains(out.String(), "cli-secret") {
		t.Fatal("confirmation missing or password echoed")
	}
	dest := Store{Dir: filepath.Join(t.TempDir(), "target")}
	out.Reset()
	if err := runWithStore(dest, []string{"import", path, "--dry-run"}, strings.NewReader("cli-secret\n"), &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dest.Dir); !os.IsNotExist(err) {
		t.Fatal("CLI dry run wrote config")
	}
	if err := runWithStore(dest, []string{"import", path}, strings.NewReader("cli-secret\n"), &out); err != nil {
		t.Fatal(err)
	}
	loaded, err := dest.Load()
	if err != nil || len(loaded.Remotes) != 5 || len(loaded.Keys) != 0 || len(loaded.Services) != 0 {
		t.Fatal("CLI import incorrect", err)
	}
	if strings.Contains(out.String(), "cli-secret") || strings.Contains(out.String(), "tc-office-secret") {
		t.Fatal("CLI output leaked secrets")
	}
	if _, _, err := readBundlePasswords(strings.NewReader("one\ntwo\n"), &bytes.Buffer{}, true); err == nil {
		t.Fatal("CLI mismatch accepted")
	}
	if _, _, err := readBundlePasswords(strings.NewReader("\n\n"), &bytes.Buffer{}, true); err == nil {
		t.Fatal("empty password accepted")
	}
}
func TestTUIPasswordFieldsAreMaskedAndNeverEnterArgs(t *testing.T) {
	m := newTUI(Store{Dir: t.TempDir()}, empty())
	for _, op := range []string{"export", "import"} {
		m = openUIOperation(t, m, op)
		if op == "export" {
			setField(t, m.form, "output", "aliases.enc")
			setField(t, m.form, "confirmation", "secret-password")
		} else {
			setField(t, m.form, "file", "aliases.enc")
		}
		setField(t, m.form, "password", "secret-password")
		for i, field := range m.form.fields {
			if field.name == "password" || field.name == "confirmation" {
				if field.input.EchoMode != textinput.EchoPassword {
					t.Fatal("password field unmasked")
				}
				m.form.focus = i
				m.form.focusField()
				if strings.Contains(m.View().Content, "secret-password") {
					t.Fatal("password appears in view")
				}
			}
		}
		args, err := m.form.arguments()
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(strings.Join(args, " "), "secret-password") {
			t.Fatal("password entered CLI argv")
		}
	}
}

func TestImportPreservesAllNamedEntriesDuringCollisionAllocation(t *testing.T) {
	c := empty()
	c.Remotes["one"] = Remote{Type: aliasTC, Value: "tc-local-one"}
	c.Remotes["two"] = Remote{Type: aliasTC, Value: "tc-local-two"}
	b := Bundle{Version: bundleVersion, Aliases: []BundleAlias{
		{Name: "one", Type: aliasTC, Value: "tc-shared"},
		{Name: "two", Type: aliasTC, Value: "tc-shared"},
		{Name: "one-2", Type: aliasTC, Value: "tc-shared"},
		{Type: aliasNodekey, Value: testIdentity("client", 114).Public},
		{Name: "unnamed-nodekey", Type: aliasTC, Value: "tc-named"},
	}}
	summary, err := mergeBundle(&c, b)
	if err != nil || summary.Added != 5 {
		t.Fatal("explicit names collapsed or consumed", summary, err)
	}
	for _, name := range []string{"one-3", "two-2", "one-2"} {
		if c.Remotes[name].Value != "tc-shared" {
			t.Fatal("explicit alias not preserved", name)
		}
	}
	if c.Remotes["unnamed-nodekey"].Value != "tc-named" || c.Remotes["unnamed-nodekey-2"].Type != aliasNodekey {
		t.Fatal("unnamed item consumed an explicit name")
	}
	again, err := mergeBundle(&c, b)
	if err != nil || again.Added != 0 {
		t.Fatal("repeated import duplicated collision aliases", again, err)
	}
}
