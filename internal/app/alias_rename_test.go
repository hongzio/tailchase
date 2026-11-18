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

func renameFixture(t *testing.T) Store {
	t.Helper()
	s := Store{Dir: t.TempDir()}
	if err := s.Update(func(c *Config) error {
		c.Keys["host"] = testIdentity("server", 101)
		c.Remotes["server"] = Remote{Type: aliasTC, Value: "tc-example"}
		c.Remotes["laptop"] = Remote{Type: aliasNodekey, Value: testIdentity("client", 102).Public}
		c.Services["fwd"] = Service{Kind: "forward", Remote: "server", Ports: []string{"8080:80"}, Enabled: true}
		c.Services["serve"] = Service{Kind: "serve", ServerKey: "host", Allow: []string{"laptop"}, Ports: []string{"80"}, Enabled: true}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAliasRenameUpdatesConsumersWithoutChangingBehavior(t *testing.T) {
	s := renameFixture(t)
	before, _ := s.Load()
	commands := map[string][]string{}
	for id, v := range before.Services {
		args, err := s.command(before, v)
		if err != nil {
			t.Fatal(err)
		}
		commands[id] = args
	}
	for old, newName := range map[string]string{"server": "office", "laptop": "peer"} {
		if err := runWithStore(s, []string{"alias", "set", old, "--name", newName}, strings.NewReader(""), &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
	}
	after, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := after.Remotes["server"]; ok {
		t.Fatal("old TC name remains")
	}
	if _, ok := after.Remotes["laptop"]; ok {
		t.Fatal("old nodekey name remains")
	}
	if after.Remotes["office"] != before.Remotes["server"] || after.Remotes["peer"] != before.Remotes["laptop"] {
		t.Fatal("rename changed alias values")
	}
	if after.Services["fwd"].Remote != "office" || !reflect.DeepEqual(after.Services["serve"].Allow, []string{"peer"}) {
		t.Fatal("references not rewritten")
	}
	for id, v := range after.Services {
		args, err := s.command(after, v)
		if err != nil || !reflect.DeepEqual(commands[id], args) || !v.Enabled {
			t.Fatal("rename changed running service behavior", id, err)
		}
	}
	if !samePrivate(before.Keys["host"].Private, after.Keys["host"].Private) {
		t.Fatal("private key changed")
	}
}

func TestAliasRenameFailuresDoNotWrite(t *testing.T) {
	s := renameFixture(t)
	path := filepath.Join(s.Dir, "config.json")
	before, _ := os.ReadFile(path)
	for _, args := range [][]string{
		{"alias", "set", "server", "--name", "laptop"},
		{"alias", "set", "server", "--name", ""},
		{"alias", "set", "server", "--name", "invalid name"},
		{"alias", "set", "missing", "--name", "new"},
		{"alias", "set", "laptop", "--name", "peer", "--value", testIdentity("client", 103).Public},
		{"alias", "set", "server", "--name", "office", "--type", "nodekey", "--value", testIdentity("client", 103).Public},
		{"alias", "add", "new", "--name", "other", "--type", "tc", "--value", "tc-example"},
	} {
		if err := runWithStore(s, args, strings.NewReader(""), &bytes.Buffer{}); err == nil {
			t.Fatal("invalid rename accepted", args[:3])
		}
		after, _ := os.ReadFile(path)
		if !bytes.Equal(before, after) {
			t.Fatal("failed rename changed config")
		}
	}
	if err := runWithStore(s, []string{"alias", "set", "server", "--name", "server"}, strings.NewReader(""), &bytes.Buffer{}); err != nil {
		t.Fatal("same-name save failed", err)
	}
}

func TestTUIAliasEditIncludesNameAndSavesAllFields(t *testing.T) {
	s := renameFixture(t)
	c, _ := s.Load()
	m := newTUI(s, c)
	m.tab = 1
	m, _ = typeVim(t, m, "/server\n")
	m, _ = press(m, 'e')
	if m.form.target != "server" || m.form.value("name") != "server" {
		t.Fatal("edit did not bind original name")
	}
	fields := []string{}
	for _, f := range m.form.fields {
		fields = append(fields, f.name)
	}
	if !reflect.DeepEqual(fields, []string{"name", "type", "value"}) {
		t.Fatal("unexpected edit fields", fields)
	}
	setField(t, m.form, "name", "office")
	setField(t, m.form, "value", "tc-new")
	m = runUIForm(t, m)
	if m.config.Remotes["office"] != (Remote{Type: aliasTC, Value: "tc-new"}) || m.config.Services["fwd"].Remote != "office" {
		t.Fatal("edit did not save name/value/references")
	}
}

func TestTUIAliasEditorRefreshesNameWhenSelectingAnotherAlias(t *testing.T) {
	s := renameFixture(t)
	c, _ := s.Load()
	m := newTUI(s, c)
	m = openUIOperation(t, m, "remote.edit")
	m.form.focus = 0
	m, _ = press(m, tea.KeyEnter)
	m, _ = press(m, 'G')
	m, _ = press(m, tea.KeyEnter)
	if m.form.value("remote") != "server" || m.form.value("name") != "server" || m.form.value("type") != aliasTC || m.form.value("value") != "tc-example" {
		t.Fatal("selector left stale editable fields")
	}
	setField(t, m.form, "name", "office")
	m = runUIForm(t, m)
	if m.config.Remotes["laptop"].Type != aliasNodekey || m.config.Services["fwd"].Remote != "office" {
		t.Fatal("generic edit renamed wrong alias")
	}
}
