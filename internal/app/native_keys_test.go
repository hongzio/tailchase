package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func nativeTestStore(t *testing.T) Store {
	t.Helper()
	return Store{Dir: filepath.Join(t.TempDir(), "tailchase"), NativeDir: t.TempDir()}
}
func writeNativeFixture(t *testing.T, s Store, name, role string, seed byte) Identity {
	t.Helper()
	k := testIdentity(role, seed)
	var raw map[string]any
	if e := json.Unmarshal(k.Private, &raw); e != nil {
		t.Fatal(e)
	}
	pub := raw["Public"].(map[string]any)
	if role == "server" {
		pub["RegionID"] = -1
	}
	b, e := json.Marshal(raw)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(s.NativeDir, name+".private.json")
	if e = os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
	identity, e := nativeIdentity(name, b)
	if e != nil {
		t.Fatal(e)
	}
	return identity
}
func TestNativeKeysVisibleWithoutImportOrWrites(t *testing.T) {
	s := nativeTestStore(t)
	client := writeNativeFixture(t, s, "client-default", "client", 9)
	server := writeNativeFixture(t, s, "default", "server", 10)
	original, _ := os.ReadFile(filepath.Join(s.NativeDir, "default.private.json"))
	c, e := s.Load()
	if e != nil {
		t.Fatal(e)
	}
	if c.Keys["tailcat:default"].Role != "server" || c.Keys["tailcat:client-default"].Role != "client" {
		t.Fatal("native roles not detected")
	}
	if c.Keys["tailcat:client-default"].Public != client.Public || c.Keys["tailcat:default"].Public != server.Public {
		t.Fatal("identity changed during discovery")
	}
	if _, e = os.Stat(s.Dir); !os.IsNotExist(e) {
		t.Fatal("listing wrote tailchase configuration")
	}
	after, _ := os.ReadFile(filepath.Join(s.NativeDir, "default.private.json"))
	if !bytes.Equal(original, after) {
		t.Fatal("native file changed")
	}
	var out bytes.Buffer
	if e = runWithStore(s, []string{"key", "list"}, strings.NewReader(""), &out); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(out.String(), "source=tailcat") || strings.Contains(out.String(), "privkey:") {
		t.Fatal("native list lacks source or leaks key")
	}
	out.Reset()
	if e = runWithStore(s, []string{"key", "show", "client-default"}, strings.NewReader(""), &out); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(out.String(), client.Public) || strings.Contains(out.String(), "privkey:") {
		t.Fatal("short-name public inspection failed")
	}
	m := newTUI(s, c)
	m.tab = 2
	if !strings.Contains(m.View().Content, "tailcat:default") {
		t.Fatal("native keys not visible in TUI")
	}
	form, e := m.operationForm("remote.add")
	if e != nil {
		t.Fatal(e)
	}
	for _, f := range form.fields {
		if f.name == "client" && !contains(f.choices, "tailcat:client-default") {
			t.Fatal("native client absent from alias choices")
		}

	}
	serviceForm, e := m.operationForm("service.add")
	if e != nil {
		t.Fatal(e)
	}
	for _, f := range serviceForm.fields {
		if f.name == "server-key" && !contains(f.choices, "tailcat:default") {
			t.Fatal("native server absent from serve choices")
		}
	}
}
func TestNativeReferencesPersistMetadataOnly(t *testing.T) {
	s := nativeTestStore(t)
	key := writeNativeFixture(t, s, "client-default", "client", 11)
	writeNativeFixture(t, s, "unused", "server", 12)
	if e := runWithStore(s, []string{"alias", "add", "home", "--type", "tc", "--value", "tc-example"}, strings.NewReader(""), &bytes.Buffer{}); e != nil {
		t.Fatal(e)
	}
	if e := runWithStore(s, []string{"service", "add", "fwd", "--kind", "forward", "--remote", "home", "--client-key", "client-default", "--ports", "80"}, strings.NewReader(""), &bytes.Buffer{}); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(s.Dir, "config.json"))
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(b), "privkey:") || strings.Contains(string(b), "tailcat:unused") {
		t.Fatal("unrelated or private native key material persisted")
	}
	var stored Config
	if e = json.Unmarshal(b, &stored); e != nil {
		t.Fatal(e)
	}
	k := stored.Keys["tailcat:client-default"]
	if k.Source != "tailcat" || k.NativeName != "client-default" || len(k.Private) != 0 {
		t.Fatal("reference missing or private copied")
	}
	c, e := s.Load()
	if e != nil {
		t.Fatal(e)
	}
	if c.Services["fwd"].ClientKey != "tailcat:client-default" || !samePrivate(c.Keys["tailcat:client-default"].Private, key.Private) {
		t.Fatal("persisted reference did not resolve")
	}
}
func TestNativeRemovalAndRotationCannotSwapPinnedIdentity(t *testing.T) {
	for _, change := range []string{"remove", "rotate", "corrupt"} {
		t.Run(change, func(t *testing.T) {
			s := nativeTestStore(t)
			old := writeNativeFixture(t, s, "client", "client", 13)
			if e := s.Update(func(c *Config) error {
				c.Remotes["home"] = Remote{Type: aliasTC, Value: "tc-example"}
				c.Services["fwd"] = Service{Kind: "forward", Remote: "home", ClientKey: "tailcat:client", Ports: []string{"80"}}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			path := filepath.Join(s.NativeDir, "client.private.json")
			switch change {
			case "remove":
				if e := os.Remove(path); e != nil {
					t.Fatal(e)
				}
			case "rotate":
				writeNativeFixture(t, s, "client", "client", 14)
			case "corrupt":
				if e := os.WriteFile(path, []byte(`{"Private":"PRIVATE_SECRET_DO_NOT_LOG"}`), 0600); e != nil {
					t.Fatal(e)
				}
			}
			c, e := s.Load()
			if e != nil {
				t.Fatal(e)
			}
			k := c.Keys["tailcat:client"]
			if k.Public != old.Public || len(k.Private) > 0 || len(c.Warnings) == 0 {
				t.Fatal("missing/changed native key was silently replaced")
			}
			if strings.Contains(strings.Join(c.Warnings, " "), "PRIVATE_SECRET_DO_NOT_LOG") {
				t.Fatal("raw corrupt key leaked into diagnostics")
			}
			if _, e = s.command(c, c.Services["fwd"]); e == nil {
				t.Fatal("unavailable native key started a service")
			}
		})
	}
}
func TestNativeAndManagedNamesDoNotCollide(t *testing.T) {
	s := nativeTestStore(t)
	native := writeNativeFixture(t, s, "default", "client", 15)
	managed := testIdentity("client", 16)
	if e := s.Update(func(c *Config) error { c.Keys["default"] = managed; return nil }); e != nil {
		t.Fatal(e)
	}
	c, e := s.Load()
	if e != nil {
		t.Fatal(e)
	}
	if c.Keys["default"].Public != managed.Public || c.Keys["tailcat:default"].Public != native.Public {
		t.Fatal("key source collision")
	}
	if resolveIdentity(c, "default") != "default" || resolveIdentity(c, "tailcat:default") != "tailcat:default" {
		t.Fatal("ambiguous key resolution")
	}
}
func TestNativeBundleContainsOnlyPortablePublicValues(t *testing.T) {
	s := nativeTestStore(t)
	key := writeNativeFixture(t, s, "client", "client", 17)
	if err := s.Update(func(c *Config) error { c.Remotes["home"] = Remote{Type: aliasTC, Value: "tc-example"}; return nil }); err != nil {
		t.Fatal(err)
	}
	c, _ := s.Load()
	b, err := exportBundle(c)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(b)
	for _, secret := range []string{"privkey:", "tailcat:client", "native_name", "services"} {
		if bytes.Contains(data, []byte(secret)) {
			t.Fatal("non-public data exported")
		}
	}
	dst := empty()
	if _, err := mergeBundle(&dst, b); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range dst.Remotes {
		if r.Type == aliasNodekey && r.Value == key.Public {
			found = true
		}
	}
	if !found || len(dst.Keys) != 0 {
		t.Fatal("native public value not imported as alias")
	}
	summary, err := mergeBundle(&c, b)
	if err != nil || summary.SkippedKeys == 0 {
		t.Fatal("native Keys duplicates not skipped", err)
	}
}

func TestNativeRoleUsesRelayMetadataNotFilename(t *testing.T) {
	s := nativeTestStore(t)
	writeNativeFixture(t, s, "looks-like-client", "server", 18)
	writeNativeFixture(t, s, "looks-like-server", "client", 19)
	// Embedded-region servers do not need a nonzero RegionID.
	k := testIdentity("server", 20)
	var data map[string]any
	_ = json.Unmarshal(k.Private, &data)
	data["Public"].(map[string]any)["Region"] = []any{map[string]any{"Nodes": []any{map[string]any{"HostName": "relay.example.com"}}}}
	b, _ := json.Marshal(data)
	if e := os.WriteFile(filepath.Join(s.NativeDir, "embedded.private.json"), b, 0600); e != nil {
		t.Fatal(e)
	}
	c, e := s.Load()
	if e != nil {
		t.Fatal(e)
	}
	if c.Keys["tailcat:looks-like-client"].Role != "server" || c.Keys["tailcat:looks-like-server"].Role != "client" || c.Keys["tailcat:embedded"].Role != "server" {
		t.Fatal("incorrect native key role inference")
	}
}
func TestNativeDiscoveryFailureDoesNotHideManagedKeys(t *testing.T) {
	s := nativeTestStore(t)
	if e := os.WriteFile(filepath.Join(s.NativeDir, "broken.private.json"), []byte("invalid-secret-json"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := s.Update(func(c *Config) error { c.Keys["managed"] = testIdentity("client", 21); return nil }); e != nil {
		t.Fatal(e)
	}
	c, e := s.Load()
	if e != nil {
		t.Fatal(e)
	}
	if len(c.Keys) != 1 || len(c.Warnings) == 0 || strings.Contains(strings.Join(c.Warnings, " "), "invalid-secret-json") {
		t.Fatal("bad native file blocked or leaked into managed catalog")
	}
}

func TestAliasBundleRejectsHostIdentityFields(t *testing.T) {
	var b Bundle
	if err := decode([]byte(`{"version":2,"aliases":[],"keys":{"tailcat:client":{"source":"tailcat"}}}`), &b); err == nil {
		t.Fatal("bundle accepted host identity fields")
	}
	c := empty()
	k := testIdentity("client", 22)
	k.Source = "tailcat"
	k.NativeName = "../escape"
	c.Keys["tailcat:client"] = k
	if c.Validate() == nil {
		t.Fatal("native name traversal accepted")
	}
}
