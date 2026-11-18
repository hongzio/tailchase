package app

import (
	"bytes"
	"crypto/ecdh"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

func fixture() Config {
	c := empty()
	c.Keys["host"] = testIdentity("server", 1)
	c.Keys["me"] = testIdentity("client", 2)
	c.Keys["peer"] = testIdentity("client", 3)
	c.Remotes["home"] = Remote{Address: "secret-address", Server: "host", Client: "me"}
	c.Remotes["friend"] = Remote{Client: "peer"}
	c.Services["web"] = Service{Kind: "serve", Remote: "home", Ports: []string{"80"}, Allow: []string{"friend"}, Enabled: true}
	return c
}
func TestAllowCommandConstruction(t *testing.T) {
	c := fixture()
	s := Store{Dir: t.TempDir()}
	v := c.Services["web"]
	c.Remotes["same-peer"] = c.Remotes["friend"]
	v.Allow = append(v.Allow, "same-peer")
	args, e := s.command(c, v)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Count(strings.Join(args, " "), c.Keys["peer"].Public) != 1 {
		t.Fatal("not deduplicated", args)
	}
	v.Allow = nil
	args, _ = s.command(c, v)
	if !contains(args, "--allow=none") {
		t.Fatal("empty policy allowed all", args)
	}
	v.AllowAnyone = true
	args, _ = s.command(c, v)
	for _, a := range args {
		if strings.HasPrefix(a, "--allow=") {
			t.Fatal("anyone policy has filter")
		}
	}
	v.AllowAnyone = false
	v.Ports = []string{"ssh"}
	c.Services["web"] = v
	if c.Validate() == nil {
		t.Fatal("SSH without authorized keys accepted")
	}
}
func contains(a []string, v string) bool {
	for _, s := range a {
		if s == v {
			return true
		}
	}
	return false
}
func TestConcurrentUpdates(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- s.Update(func(c *Config) error { c.Remotes[string(rune('a'+i))] = Remote{}; return nil })
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	c, e := s.Load()
	if e != nil || len(c.Remotes) != 8 {
		t.Fatal("lost update", e, len(c.Remotes))
	}
}
func testIdentity(role string, seed byte) Identity {
	raw := bytes.Repeat([]byte{seed}, 32)
	k, _ := ecdh.X25519().NewPrivateKey(raw)
	pub := "nodekey:" + hex.EncodeToString(k.PublicKey().Bytes())
	b, _ := json.Marshal(map[string]any{"Private": "privkey:" + hex.EncodeToString(raw), "Public": map[string]string{"ServerPublic": pub}})
	return Identity{Role: role, Public: pub, Private: b}
}

func TestPrivateIdentityValidation(t *testing.T) {
	c := fixture()
	k := c.Keys["me"]
	k.Public = c.Keys["peer"].Public
	c.Keys["me"] = k
	if c.Validate() == nil {
		t.Fatal("public/private mismatch accepted")
	}
	c = fixture()
	k = c.Keys["me"]
	k.Private = json.RawMessage(`{"Private":"privkey:00"}`)
	c.Keys["me"] = k
	if c.Validate() == nil {
		t.Fatal("invalid key accepted")
	}
}

func TestInvalidDryRunFlagCannotImport(t *testing.T) {
	_, e := parse([]string{"bundle.json", "--dry-run=typo"})
	if e == nil {
		t.Fatal("invalid dry-run value accepted")
	}
}
