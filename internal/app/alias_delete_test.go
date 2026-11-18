package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAliasDeletionPreservesKeysAndOtherAliases(t *testing.T) {
	for _, kind := range []string{aliasTC, aliasNodekey} {
		t.Run(kind, func(t *testing.T) {
			s := Store{Dir: t.TempDir()}
			key := testIdentity("client", 92)
			value := "tc-example"
			if kind == aliasNodekey {
				value = key.Public
			}
			if err := s.Update(func(c *Config) error {
				c.Keys["local"] = key
				c.Remotes["target"] = Remote{Type: kind, Value: value}
				c.Remotes["other"] = Remote{Type: aliasTC, Value: "tc-other"}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := runWithStore(s, []string{"alias", "delete", "target"}, strings.NewReader(""), &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
			c, err := s.Load()
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := c.Remotes["target"]; ok {
				t.Fatal("alias still exists")
			}
			if len(c.Remotes) != 1 || c.Remotes["other"].Value != "tc-other" || !samePrivate(c.Keys["local"].Private, key.Private) {
				t.Fatal("deletion changed unrelated data")
			}
			if err := s.deleteAlias("target"); err == nil {
				t.Fatal("missing alias deletion succeeded")
			}
		})
	}
}

func TestReferencedAliasesCannotBeDeletedEvenWhenServicesAreDisabled(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if err := s.Update(func(c *Config) error {
		c.Keys["server"] = testIdentity("server", 93)
		c.Remotes["target"] = Remote{Type: aliasTC, Value: "tc-example"}
		c.Remotes["peer"] = Remote{Type: aliasNodekey, Value: testIdentity("client", 94).Public}
		c.Services["forward"] = Service{Kind: "forward", Remote: "target", Ports: []string{"80"}}
		c.Services["serve"] = Service{Kind: "serve", ServerKey: "server", Allow: []string{"peer"}, Ports: []string{"80"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Dir, "config.json")
	before, _ := os.ReadFile(path)
	for name, id := range map[string]string{"target": "forward", "peer": "serve"} {
		err := s.deleteAlias(name)
		if err == nil || !strings.Contains(err.Error(), id) {
			t.Fatal("missing reference protection", err)
		}
		after, _ := os.ReadFile(path)
		if !bytes.Equal(before, after) {
			t.Fatal("blocked deletion changed config")
		}
	}
}

func TestTUIAliasDeleteConfirmationTargetsSelectionAndSupportsCancel(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if err := s.Update(func(c *Config) error {
		c.Remotes["alpha"] = Remote{Type: aliasTC, Value: "tc-a"}
		c.Remotes["beta"] = Remote{Type: aliasNodekey, Value: testIdentity("client", 95).Public}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	c, _ := s.Load()
	m := newTUI(s, c)
	m.tab = 1
	m, _ = typeVim(t, m, "/beta\n")
	m, _ = press(m, 'd')
	if m.form != nil {
		t.Fatal("single d deletes")
	}
	m, cmd := press(m, 'd')
	if cmd != nil || m.form == nil || m.form.operation != "remote.delete" || m.form.target != "beta" || len(m.form.fields) != 0 {
		t.Fatal("dd did not bind selected alias")
	}
	if !strings.Contains(m.View().Content, "Delete alias: beta") {
		t.Fatal("confirmation does not identify target")
	}
	m, _ = typeVim(t, m, ":q\n")
	c, _ = s.Load()
	if _, ok := c.Remotes["beta"]; !ok {
		t.Fatal("cancel deleted alias")
	}
	m, _ = typeVim(t, m, ":delete\n")
	m = runUIForm(t, m)
	if _, ok := m.config.Remotes["beta"]; ok {
		t.Fatal("confirmed deletion did not persist")
	}
	if m.config.Remotes["alpha"].Value != "tc-a" {
		t.Fatal("wrong alias deleted")
	}
}

func TestAliasDeleteChecksReferencesAddedAfterConfirmation(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if err := s.Update(func(c *Config) error { c.Remotes["target"] = Remote{Type: aliasTC, Value: "tc-example"}; return nil }); err != nil {
		t.Fatal(err)
	}
	c, _ := s.Load()
	m := newTUI(s, c)
	m.tab = 1
	m, _ = typeVim(t, m, "dd")
	if err := s.Update(func(c *Config) error {
		c.Services["new-forward"] = Service{Kind: "forward", Remote: "target", Ports: []string{"80"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	next, cmd := m.submitForm()
	m = next.(tuiModel)
	next, _ = m.Update(cmd())
	m = next.(tuiModel)
	if m.form == nil || !strings.Contains(m.form.err, "new-forward") {
		t.Fatal("stale confirmation bypassed reference checks")
	}
	c, _ = s.Load()
	if _, ok := c.Remotes["target"]; !ok {
		t.Fatal("referenced alias removed")
	}
}

func TestDeletingMigratedPublicAliasDoesNotResurrectIt(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	c := empty()
	c.Keys["old-peer"] = Identity{Role: "client", Public: testIdentity("client", 96).Public}
	raw, _ := json.Marshal(c)
	if err := os.WriteFile(filepath.Join(s.Dir, "config.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.deleteAlias("old-peer"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		loaded, err := s.Load()
		if err != nil || len(loaded.Remotes) != 0 || len(loaded.Keys) != 0 {
			t.Fatal("deleted migration reappeared", err)
		}
	}
}
