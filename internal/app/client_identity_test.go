package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddressOnlySSHAndForwardUseEphemeralWhenNoDefault(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if e := s.Update(func(c *Config) error {
		c.Remotes["home"] = Remote{Address: "example.com"}
		c.Services["web"] = Service{Kind: "forward", Remote: "home", Ports: []string{"8080:80"}}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	cmd, cleanup, e := s.prepareSSH("home", "alice")
	if e != nil {
		t.Fatal(e)
	}
	defer cleanup()
	if !contains(cmd.Args, "--key=new") || cmd.Args[len(cmd.Args)-1] != "alice@example.com" {
		t.Fatal("incorrect address-only SSH arguments", cmd.Args)
	}
	c, _ := s.Load()
	args, e := s.command(c, c.Services["web"])
	if e != nil || !contains(args, "--key=new") {
		t.Fatal("address-only forward unavailable", e, args)
	}
	files, _ := filepath.Glob(filepath.Join(s.Dir, ".key-*"))
	if len(files) > 0 {
		t.Fatal("ephemeral SSH wrote a persistent key")
	}
}
func TestAddressOnlySSHUsesNativeDefaultAndCleansTemporaryKey(t *testing.T) {
	s := nativeTestStore(t)
	key := writeNativeFixture(t, s, "client-default", "client", 27)
	if e := s.Update(func(c *Config) error { c.Remotes["home"] = Remote{Address: "example.com"}; return nil }); e != nil {
		t.Fatal(e)
	}
	cmd, cleanup, e := s.prepareSSH("home", "")
	if e != nil {
		t.Fatal(e)
	}
	path := strings.TrimPrefix(cmd.Args[2], "--key=")
	data, e := os.ReadFile(path)
	if e != nil || !samePrivate(data, key.Private) {
		t.Fatal("native default was not used", e)
	}
	cleanup()
	if _, e = os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("temporary key retained")
	}
	c, _ := s.Load()
	args, _ := s.command(c, Service{Kind: "forward", Remote: "home", Ports: []string{"80"}})
	if !contains(args, "--key=tailcat:client-default") {
		t.Fatal("forward default identity not selected")
	}
}
func TestClientIdentityDoesNotFallbackFromInvalidOrExplicitKey(t *testing.T) {
	s := nativeTestStore(t)
	if e := os.WriteFile(filepath.Join(s.NativeDir, "client-default.private.json"), []byte("invalid"), 0600); e != nil {
		t.Fatal(e)
	}
	c, _ := s.Load()
	if _, e := s.clientIdentity(c, Remote{}); e == nil {
		t.Fatal("invalid default silently replaced with ephemeral identity")
	}
	writeNativeFixture(t, s, "client-default", "client", 28)
	c, _ = s.Load()
	if _, e := s.clientIdentity(c, Remote{Client: "missing"}); e == nil {
		t.Fatal("explicit unavailable key silently replaced with default")
	}
}
func TestSelectedRemoteSSHShortcutDoesNotAskForAliasAgain(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if e := s.Update(func(c *Config) error { c.Remotes["home"] = Remote{Address: "example.com"}; return nil }); e != nil {
		t.Fatal(e)
	}
	c, _ := s.Load()
	m := newTUI(s, c)
	m.tab = 1
	m, _ = typeVim(t, m, ":ssh\n")
	if m.form == nil || m.form.target != "home" {
		t.Fatal("selected remote was not bound")
	}
	for _, f := range m.form.fields {
		if f.name == "remote" {
			t.Fatal("SSH asks to select the highlighted alias again")
		}
	}
	next, submit := m.submitForm()
	m = next.(tuiModel)
	ready, ok := submit().(uiSSHReady)
	if !ok || ready.err != nil {
		t.Fatal("address-only SSH preparation failed", ready.err)
	}
	defer ready.cleanup()
	if !contains(ready.cmd.Args, "--key=new") {
		t.Fatal("unexpected identity fallback")
	}
	var out bytes.Buffer
	if e := runWithStore(s, []string{"service", "add", "web", "--kind", "forward", "--remote", "home", "--ports", "80"}, strings.NewReader(""), &out); e != nil {
		t.Fatal("CLI forward registration still requires client key", e)
	}
}
