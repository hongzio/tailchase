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

func TestManagedIdentityDeletionAndReferenceProtection(t *testing.T) {
	for _, role := range []string{"client", "server"} {
		t.Run(role, func(t *testing.T) {
			s := Store{Dir: t.TempDir()}
			key := testIdentity(role, 23)
			if e := s.Update(func(c *Config) error { c.Keys["test"] = key; c.Keys["other"] = testIdentity("client", 24); return nil }); e != nil {
				t.Fatal(e)
			}
			if e := runWithStore(s, []string{"key", "delete", "test"}, strings.NewReader(""), &bytes.Buffer{}); e != nil {
				t.Fatal(e)
			}
			c, _ := s.Load()
			if _, ok := c.Keys["test"]; ok {
				t.Fatal("key not deleted")
			}
			if len(c.Keys) != 1 || c.Keys["other"].Public == "" {
				t.Fatal("unrelated key removed")
			}
			if e := s.Update(func(c *Config) error {
				c.Keys["test"] = key
				c.Remotes["target"] = Remote{Type: aliasTC, Value: "tc-example"}
				if role == "client" {
					c.Services["home"] = Service{Kind: "forward", Remote: "target", ClientKey: "test", Ports: []string{"80"}}
				} else {
					c.Services["home"] = Service{Kind: "serve", ServerKey: "test", Ports: []string{"80"}}
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			before, _ := os.ReadFile(filepath.Join(s.Dir, "config.json"))
			e := runWithStore(s, []string{"key", "delete", "test"}, strings.NewReader(""), &bytes.Buffer{})
			if e == nil || !strings.Contains(e.Error(), "home") {
				t.Fatal("referenced key deletion was not blocked", e)
			}
			after, _ := os.ReadFile(filepath.Join(s.Dir, "config.json"))
			if !bytes.Equal(before, after) {
				t.Fatal("blocked deletion changed configuration")
			}
		})
	}
}
func TestNativeIdentityCannotBeDeletedByTailchase(t *testing.T) {
	s := nativeTestStore(t)
	writeNativeFixture(t, s, "default", "server", 25)
	path := filepath.Join(s.NativeDir, "default.private.json")
	before, _ := os.ReadFile(path)
	for _, id := range []string{"default", "tailcat:default"} {
		if e := runWithStore(s, []string{"key", "delete", id}, strings.NewReader(""), &bytes.Buffer{}); e == nil {
			t.Fatal("native deletion was allowed")
		}
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("native file modified")
	}
}
func TestTUIDeleteTargetsSelectedKeyAndCanCancel(t *testing.T) {
	m := testUI(t)
	if e := m.store.Update(func(c *Config) error { c.Keys["test"] = testIdentity("client", 26); return nil }); e != nil {
		t.Fatal(e)
	}
	m.config, _ = m.store.Load()
	m.tab = 2
	for i, id := range m.rows() {
		if id == "test" {
			m.cursor = i
		}
	}
	before, _ := m.store.Load()
	m, cmd := typeVim(t, m, "dd")
	if cmd != nil || m.form == nil || m.form.target != "test" || len(m.form.fields) != 0 {
		t.Fatal("delete did not target selected key directly")
	}
	m, _ = press(m, tea.KeyEsc)
	after, _ := m.store.Load()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("cancel deleted key")
	}
	m, _ = typeVim(t, m, "dd")
	m = runUIForm(t, m)
	if _, ok := m.config.Keys["test"]; ok {
		t.Fatal("confirmed TUI delete did not remove key")
	}
	m.result = nil
	m = openUIOperation(t, m, "key.delete")
	if m.form == nil {
		t.Fatal("palette deletion unavailable")
	}
}
