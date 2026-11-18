package app

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestServeCreationNeedsServerIdentityButNoRemote(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if e := s.Update(func(c *Config) error { c.Keys["host"] = testIdentity("server", 29); return nil }); e != nil {
		t.Fatal(e)
	}
	c, _ := s.Load()
	m := newTUI(s, c)
	m = openUIOperation(t, m, "service.add")
	for name, value := range map[string]string{"name": "local-web", "kind": "serve", "server-key": "host", "ports": "4376"} {
		setField(t, m.form, name, value)
	}
	for _, i := range m.form.visibleIndices() {
		if i == len(m.form.fields) {
			continue
		}
		if m.form.fields[i].name == "remote" {
			t.Fatal("serve still asks for remote alias")
		}
	}
	args, e := m.form.arguments()
	if e != nil {
		t.Fatal("serve form requires hidden remote", e)
	}
	for _, arg := range args {
		if strings.HasPrefix(arg, "--remote") {
			t.Fatal("serve emits a remote argument")
		}
	}
	m = runUIForm(t, m)
	v := m.config.Services["local-web"]
	if v.Remote != "" || v.ServerKey != "host" || len(m.config.Remotes) != 0 {
		t.Fatal("serve registration created a remote dependency")
	}
	command, e := s.command(m.config, v)
	if e != nil || !contains(command, "--key=host") || !contains(command, "4376") || !contains(command, "--allow=none") {
		t.Fatal("invalid local serve command", command, e)
	}
	var out bytes.Buffer
	if e = runWithStore(s, []string{"key", "delete", "host"}, strings.NewReader(""), &out); e == nil || !strings.Contains(e.Error(), "local-web") {
		t.Fatal("server key used by direct service could be deleted", e)
	}
}
func TestForwardFormDoesNotRequireHiddenServerKey(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	c := empty()
	c.Remotes["home"] = Remote{Address: "example.com"}
	m := newTUI(s, c)
	m = openUIOperation(t, m, "service.add")
	setField(t, m.form, "name", "web")
	setField(t, m.form, "ports", "14376:4376")
	args, e := m.form.arguments()
	if e != nil {
		t.Fatal(e)
	}
	for _, a := range args {
		if strings.HasPrefix(a, "--server-key") {
			t.Fatal("forward requires a server identity")
		}
	}
	if !contains(args, "--remote=home") {
		t.Fatal("forward lacks target remote")
	}
}
func TestDirectServeValidationAndLegacyCompatibility(t *testing.T) {
	c := fixture()
	legacy := c.Services["web"]
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
	s := Store{Dir: t.TempDir()}
	if key, e := s.serviceIdentity(c, legacy); e != nil || key != "host" {
		t.Fatal("legacy server key no longer resolves", key, e)
	}
	c.Services["direct"] = Service{Kind: "serve", ServerKey: "host", Ports: []string{"80"}, Enabled: true}
	if c.Validate() == nil {
		t.Fatal("same server identity enabled twice")
	}
	legacy.Enabled = false
	c.Services["web"] = legacy
	if e := c.Validate(); e != nil {
		t.Fatal("direct local server rejected", e)
	}
	v := c.Services["direct"]
	v.ServerKey = "me"
	c.Services["direct"] = v
	if c.Validate() == nil {
		t.Fatal("client identity accepted for server")
	}
	v.ServerKey = "host"
	v.Remote = "home"
	c.Services["direct"] = v
	if c.Validate() == nil {
		t.Fatal("ambiguous serve target accepted")
	}
}
func TestNativeDirectServeKeyIsPinnedWithoutPrivateCopy(t *testing.T) {
	s := nativeTestStore(t)
	k := writeNativeFixture(t, s, "default", "server", 30)
	if e := runWithStore(s, []string{"service", "add", "local", "--kind", "serve", "--server-key", "default", "--ports", "4376"}, strings.NewReader(""), &bytes.Buffer{}); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(filepath.Join(s.Dir, "config.json"))
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(raw), "privkey:") {
		t.Fatal("native server private key copied")
	}
	var stored Config
	_ = json.Unmarshal(raw, &stored)
	if stored.Keys["tailcat:default"].Public != k.Public || stored.Keys["tailcat:default"].Source != "tailcat" {
		t.Fatal("direct native server reference not persisted")
	}
	writeNativeFixture(t, s, "default", "server", 31)
	c, _ := s.Load()
	if _, e = s.command(c, c.Services["local"]); e == nil {
		t.Fatal("rotated server key silently substituted")
	}
}
func TestDirectServeDaemonPublishesAddressWithoutCreatingRemote(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake tailcat uses POSIX shell")
	}
	s := Store{Dir: t.TempDir()}
	if e := s.Update(func(c *Config) error {
		c.Keys["host"] = testIdentity("server", 32)
		c.Services["local"] = Service{Kind: "serve", ServerKey: "host", Ports: []string{"4376"}, Enabled: true}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	fake := t.TempDir()
	if e := os.WriteFile(filepath.Join(fake, "tailcat"), []byte("#!/bin/sh\nprintf '{\"listenAddr\":\"secret-test-address\"}\\n'\nexec sleep 60\n"), 0700); e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestDaemonHelper$")
	cmd.Env = append(os.Environ(), "TAILCHASE_TEST_DAEMON=1", "PATH="+fake+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})
	if e := s.startBackgroundDaemon(cmd); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(6 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		r, e := s.runtime()
		if e == nil && r.Addresses["local"] == "secret-test-address" {
			ready = true
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if !ready {
		t.Fatal("serve address was not captured")
	}
	c, e := s.Load()
	if e != nil || len(c.Remotes) != 0 {
		t.Fatal("daemon created a dummy remote", e)
	}
	m := newTUI(s, c)
	m, show := press(m, 'v')
	if show == nil || m.form != nil {
		t.Fatal("service inspection reopened selector")
	}
	next, _ := m.Update(show())
	m = next.(tuiModel)
	if m.result == nil || !strings.Contains(m.result.content, "secret-test-address") {
		t.Fatal("service address not visible on explicit inspection")
	}
	m.result = nil
	if strings.Contains(m.View().Content, "secret-test-address") {
		t.Fatal("address leaked on main screen")
	}
	if e = cmd.Process.Signal(syscall.SIGTERM); e != nil {
		t.Fatal(e)
	}
}
