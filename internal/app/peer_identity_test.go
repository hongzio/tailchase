package app

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestPublicOnlyPeerAliasCanBeAllowedThroughTUI(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if e := s.Update(func(c *Config) error { c.Keys["host"] = testIdentity("server", 33); return nil }); e != nil {
		t.Fatal(e)
	}
	c, _ := s.Load()
	m := newTUI(s, c)
	peer := testIdentity("client", 34)
	m.tab = 1
	m, _ = press(m, 'i')
	setField(t, m.form, "name", "laptop")
	setField(t, m.form, "type", aliasNodekey)
	setField(t, m.form, "value", peer.Public)
	m = runUIForm(t, m)
	r := m.config.Remotes["laptop"]
	if r.Client != "" || r.Address != "" || r.Type != aliasNodekey || r.Value != peer.Public || peerPublic(m.config, "laptop") != peer.Public {
		t.Fatal("peer alias required local key or address")
	}
	if len(m.config.Keys) != 1 {
		t.Fatal("peer private identity created")
	}
	m.result = nil
	m.tab = 0
	m = openUIOperation(t, m, "service.add")
	for k, v := range map[string]string{"name": "local", "kind": "serve", "server-key": "host", "ports": "4376"} {
		setField(t, m.form, k, v)
	}
	for i, f := range m.form.fields {
		if f.name == "allow" {
			m.form.focus = i
		}
	}
	m, _ = press(m, tea.KeyEnter)
	if !reflect.DeepEqual(m.form.choiceItems(), []string{"laptop"}) {
		t.Fatal("public peer missing from allow choices")
	}
	m, _ = press(m, tea.KeySpace)
	m, _ = press(m, tea.KeyEnter)
	m = runUIForm(t, m)
	args, e := s.command(m.config, m.config.Services["local"])
	if e != nil || !contains(args, "--allow="+peer.Public) {
		t.Fatal("peer nodekey not used for authorization", e, args)
	}
	if strings.Contains(m.row("local"), peer.Public) {
		t.Fatal("public key should be represented by fingerprint in lists")
	}
	data, _ := os.ReadFile(filepath.Join(s.Dir, "config.json"))
	if bytes.Contains(data, peer.Private) {
		t.Fatal("peer private key stored")
	}
	m.result = nil
	m, _ = press(m, 'a')
	if m.picker == nil || strings.HasPrefix(m.row("laptop"), "[-]") {
		t.Fatal("quick picker disabled a public-only peer")
	}
}
func TestRemoteEditAddsPeerKeyWithoutReselectingAlias(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if e := s.Update(func(c *Config) error { c.Remotes["axis"] = Remote{Address: "secret-address"}; return nil }); e != nil {
		t.Fatal(e)
	}
	c, _ := s.Load()
	m := newTUI(s, c)
	m.tab = 1
	m, _ = press(m, 'e')
	if m.form == nil || m.form.target != "axis" {
		t.Fatal("e did not bind selected alias")
	}
	for _, f := range m.form.fields {
		if f.name == "remote" {
			t.Fatal("edit asks to select the same alias again")
		}
	}
	pub := testIdentity("client", 35).Public
	if e := s.Update(func(c *Config) error { c.Keys["axis-peer"] = Identity{Role: "client", Public: pub}; return nil }); e != nil {
		t.Fatal(e)
	}
	setField(t, m.form, "type", aliasNodekey)
	setField(t, m.form, "value", pub)
	m = runUIForm(t, m)
	r := m.config.Remotes["axis"]
	if r.Type != aliasNodekey || r.Value != pub || r.Address != "" {
		t.Fatal("type/value edit failed to replace old address")
	}
}
func TestPeerIdentityIsDistinctFromOutboundIdentity(t *testing.T) {
	c := fixture()
	pub := testIdentity("client", 36).Public
	r := c.Remotes["friend"]
	r.PeerPublic = pub
	c.Remotes["friend"] = r
	s := Store{Dir: t.TempDir()}
	args, e := s.command(c, c.Services["web"])
	if e != nil {
		t.Fatal(e)
	}
	if !contains(args, "--allow="+pub) || strings.Contains(strings.Join(args, " "), c.Keys["peer"].Public) {
		t.Fatal("allow uses local client identity instead of explicit peer")
	}
	if identity, e := s.clientIdentity(c, r); e != nil || identity != "peer" {
		t.Fatal("peer public key replaced outbound private identity", e)
	}
	r.Client = ""
	c.Remotes["friend"] = r
	if _, e = s.clientIdentity(c, r); e != nil {
		t.Fatal("public-only peer prevented normal client fallback", e)
	}
}
func TestPeerKeysValidateAndFailedUpdatesDoNotWrite(t *testing.T) {
	for _, pub := range []string{"tc-address", "privkey:" + strings.Repeat("a", 64), "nodekey:bad", "nodekey:" + strings.Repeat("z", 64)} {
		t.Run(pub[:min(12, len(pub))], func(t *testing.T) {
			s := Store{Dir: t.TempDir()}
			e := runWithStore(s, []string{"remote", "add", "peer", "--peer-key", pub}, strings.NewReader(""), &bytes.Buffer{})
			if e == nil {
				t.Fatal("invalid peer key accepted")
			}
			if _, e = os.Stat(filepath.Join(s.Dir, "config.json")); !os.IsNotExist(e) {
				t.Fatal("invalid alias persisted")
			}
		})
	}
}
func TestAllowedPeerChangesRequirePolicyUpdate(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	pub := testIdentity("client", 37).Public
	if e := s.Update(func(c *Config) error {
		c.Keys["host"] = testIdentity("server", 38)
		c.Remotes["peer"] = Remote{PeerPublic: pub}
		c.Services["local"] = Service{Kind: "serve", ServerKey: "host", Ports: []string{"80"}, Allow: []string{"peer"}}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	before, _ := os.ReadFile(filepath.Join(s.Dir, "config.json"))
	e := runWithStore(s, []string{"remote", "set", "peer", "--peer-key", testIdentity("client", 39).Public}, strings.NewReader(""), &bytes.Buffer{})
	if e == nil || !strings.Contains(e.Error(), "local") {
		t.Fatal("allowed identity changed silently", e)
	}
	after, _ := os.ReadFile(filepath.Join(s.Dir, "config.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("blocked update changed config")
	}
	if e = runWithStore(s, []string{"remote", "set", "peer", "--address", "example.com"}, strings.NewReader(""), &bytes.Buffer{}); e != nil {
		t.Fatal("unrelated address edit blocked", e)
	}
	c, _ := s.Load()
	draft := allowDraft{service: "local", original: c.Services["local"], config: c, selected: map[string]bool{"peer": true}}
	// Simulate an external file edit while the quick picker is open.
	if e = s.Update(func(c *Config) error {
		r := c.Remotes["peer"]
		r.Peer = ""
		r.PeerPublic = testIdentity("client", 40).Public
		c.Remotes["peer"] = r
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if e = s.saveAllow(draft); e == nil {
		t.Fatal("stale peer selection saved")
	}
}
func TestPublicPeerBundlesRoundTripWithoutPrivateKeys(t *testing.T) {
	c := empty()
	pub := testIdentity("client", 41).Public
	c.Remotes["laptop"] = Remote{PeerPublic: pub}
	b, err := exportBundle(c)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Aliases) != 1 || b.Aliases[0] != (BundleAlias{Name: "laptop", Type: aliasNodekey, Value: pub}) {
		t.Fatal("standalone nodekey not preserved")
	}
	dst := empty()
	if _, err := mergeBundle(&dst, b); err != nil {
		t.Fatal(err)
	}
	if peerPublic(dst, "laptop") != pub || len(dst.Keys) != 0 {
		t.Fatal("public import changed Keys")
	}
}

func TestEmptyAllowPickerExplainsPublicPeerRegistration(t *testing.T) {
	m := newTUI(Store{Dir: t.TempDir()}, empty())
	m = openUIOperation(t, m, "service.add")
	setField(t, m.form, "kind", "serve")
	for i, f := range m.form.fields {
		if f.name == "allow" {
			m.form.focus = i
		}
	}
	m, _ = press(m, tea.KeyEnter)
	if !strings.Contains(m.View().Content, "Type nodekey") {
		t.Fatal("empty picker gives no peer registration guidance")
	}
}
