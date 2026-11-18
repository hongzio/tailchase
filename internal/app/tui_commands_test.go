package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func setField(t *testing.T, f *uiForm, name, value string) {
	t.Helper()
	for i := range f.fields {
		if f.fields[i].name == name {
			f.fields[i].input.SetValue(value)
			return
		}
	}
	t.Fatalf("missing field %s", name)
}
func openUIOperation(t *testing.T, m tuiModel, id string) tuiModel {
	t.Helper()
	next, _ := m.beginOperation(id)
	m = next.(tuiModel)
	if m.form == nil {
		t.Fatalf("%s did not open a form", id)
	}
	return m
}
func runUIForm(t *testing.T, m tuiModel) tuiModel {
	t.Helper()
	next, cmd := m.submitForm()
	if cmd == nil {
		t.Fatal("no form command", m.form.err)
	}
	m = next.(tuiModel)
	msg := cmd()
	done, ok := msg.(uiCommandDone)
	if !ok {
		t.Fatalf("unexpected message %T", msg)
	}
	if done.err != nil {
		t.Fatal(done.err)
	}
	next, _ = m.Update(done)
	m = next.(tuiModel)
	m.config, _ = m.store.Load()
	return m
}
func TestTUIAliasCreationFromEmptyStoreWithoutDaemon(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	m := newTUI(s, empty())
	m.tab = 1
	m, _ = press(m, 'i')
	if m.form == nil {
		t.Fatal("i did not open alias creation")
	}
	// Shortcut characters must be entered as ordinary form text.
	for _, r := range "quiet-home" {
		m, _ = press(m, r)
	}
	setField(t, m.form, "value", "tc-example")
	m = runUIForm(t, m)
	r, ok := m.config.Remotes["quiet-home"]
	if !ok || r.Client != "" || r.Server != "" {
		t.Fatal("alias-only creation failed")
	}
	if _, e := os.Stat(filepath.Join(s.Dir, "runtime.json")); !os.IsNotExist(e) {
		t.Fatal("alias creation required or started daemon")
	}
}
func TestTUIServiceCreationAndExplicitEnableDisable(t *testing.T) {
	m := testUI(t)
	m = openUIOperation(t, m, "service.add")
	for k, v := range map[string]string{"name": "proxy", "remote": "home", "ports": "8080:80"} {
		setField(t, m.form, k, v)
	}
	m = runUIForm(t, m)
	if m.config.Services["proxy"].Enabled || m.config.Services["proxy"].Kind != "forward" {
		t.Fatal("service defaults wrong")
	}
	for _, op := range []string{"enable", "disable"} {
		m.result = nil
		m = openUIOperation(t, m, op)
		setField(t, m.form, "service", "proxy")
		m = runUIForm(t, m)
		if m.config.Services["proxy"].Enabled != (op == "enable") {
			t.Fatal(op, "did not apply explicit state")
		}
	}
	m.result = nil
	m = openUIOperation(t, m, "allow")
	setField(t, m.form, "service", "web")
	setField(t, m.form, "mode", "anyone")
	m = runUIForm(t, m)
	if !m.config.Services["web"].AllowAnyone || len(m.config.Services["web"].Allow) > 0 {
		t.Fatal("anyone mode not applied")
	}
}
func TestTUIBundleExportValidateAndImport(t *testing.T) {
	source := newTUI(Store{Dir: t.TempDir()}, empty())
	if err := source.store.Update(func(c *Config) error { c.Remotes["home"] = Remote{Type: aliasTC, Value: "tc-example"}; return nil }); err != nil {
		t.Fatal(err)
	}
	source.config, _ = source.store.Load()
	path := filepath.Join(t.TempDir(), "aliases.enc")
	source = openUIOperation(t, source, "export")
	setField(t, source.form, "output", path)
	setField(t, source.form, "password", "test-password")
	setField(t, source.form, "confirmation", "test-password")
	source = runUIForm(t, source)
	dst := newTUI(Store{Dir: t.TempDir()}, empty())
	dst = openUIOperation(t, dst, "import")
	setField(t, dst.form, "file", path)
	setField(t, dst.form, "password", "test-password")
	setField(t, dst.form, "dry-run", "true")
	dst = runUIForm(t, dst)
	if dst.form == nil || dst.result == nil || len(dst.config.Remotes) > 0 {
		t.Fatal("dry run changed store or lost form")
	}
	if dst.form.value("password") != "" {
		t.Fatal("password retained after submission")
	}
	dst, _ = press(dst, tea.KeyEsc)
	setField(t, dst.form, "dry-run", "false")
	setField(t, dst.form, "password", "test-password")
	dst = runUIForm(t, dst)
	if dst.config.Remotes["home"].Value != "tc-example" || len(dst.config.Keys) != 0 || len(dst.config.Services) != 0 {
		t.Fatal("import did not add only aliases")
	}
}

func TestTUIChoiceEditorPreservesFilteredSelections(t *testing.T) {
	m := testUI(t)
	m = openUIOperation(t, m, "allow")
	m.form.focus = 2
	m, _ = press(m, tea.KeyEnter)
	if m.form.choice == nil {
		t.Fatal("choice picker not opened")
	}
	m, _ = press(m, '/')
	for _, r := range "home" {
		m, _ = press(m, r)
	}
	if !reflect.DeepEqual(m.form.choiceItems(), []string{"home-nodekey"}) {
		t.Fatal("choice filtering failed")
	}
	m, _ = press(m, tea.KeyEnter) // Leave search mode.
	m, _ = press(m, tea.KeySpace)
	m, _ = press(m, tea.KeyEnter)
	if m.form.value("allow") != "friend,home-nodekey" {
		t.Fatal("hidden selection was lost", m.form.value("allow"))
	}
	m, _ = press(m, tea.KeyEnter)
	m, _ = press(m, tea.KeySpace)
	m, _ = press(m, tea.KeyEsc)
	if m.form.value("allow") != "friend,home-nodekey" {
		t.Fatal("choice cancel committed draft")
	}
}
func TestTUIRevealResultsAreExplicitAndDismissed(t *testing.T) {
	m := testUI(t)
	m.tab = 2
	if strings.Contains(m.View().Content, "privkey:") {
		t.Fatal("private key shown by default")
	}
	m = openUIOperation(t, m, "key.show")
	setField(t, m.form, "key", "me")
	m = runUIForm(t, m)
	if strings.Contains(m.result.content, "privkey:") {
		t.Fatal("public key inspection leaked private material")
	}
	m, _ = press(m, tea.KeyEsc)
	m = openUIOperation(t, m, "key.private")
	setField(t, m.form, "key", "me")
	m = runUIForm(t, m)
	if !strings.Contains(m.result.content, "privkey:") {
		t.Fatal("explicit private reveal missing")
	}
	m, _ = press(m, tea.KeyEsc)
	if m.result != nil || strings.Contains(m.View().Content, "privkey:") {
		t.Fatal("private result retained on screen")
	}
	m = openUIOperation(t, m, "remote.show")
	setField(t, m.form, "remote", "home")
	m = runUIForm(t, m)
	if !strings.Contains(m.result.content, "secret-address") {
		t.Fatal("explicit remote show did not reveal address")
	}
}
func TestTUIFormFailureRetainsInputAndStore(t *testing.T) {
	m := testUI(t)
	before, _ := m.store.Load()
	m = openUIOperation(t, m, "remote.add")
	setField(t, m.form, "name", "home")
	setField(t, m.form, "value", "tc-example")
	next, cmd := m.submitForm()
	m = next.(tuiModel)
	next, _ = m.Update(cmd())
	m = next.(tuiModel)
	if m.form == nil || m.form.err == "" || m.form.value("name") != "home" {
		t.Fatal("failed submission lost form")
	}
	after, _ := m.store.Load()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("failed form mutated store")
	}
}
func TestTUIAllActionsHaveWorkingEntryPoints(t *testing.T) {
	for _, op := range uiOperations {
		if op.id == "daemon" {
			continue
		}
		t.Run(op.id, func(t *testing.T) {
			m := testUI(t)
			next, cmd := m.beginOperation(op.id)
			m = next.(tuiModel)
			if m.form == nil && m.result == nil && cmd == nil {
				t.Fatal("action has no implementation")
			}
			if m.form != nil {
				for _, size := range [][2]int{{120, 34}, {50, 16}} {
					m.width = size[0]
					m.height = size[1]
					if len(strings.Split(m.View().Content, "\n")) > m.height {
						t.Fatal("form overflow")
					}
				}
			}
		})
	}
	m := testUI(t)
	m.openMenu()
	for _, r := range "import existing" {
		next, _ := m.updateMenu(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = next.(tuiModel)
	}
	if len(m.menu.items()) != 1 || m.menu.items()[0].id != "key.add" {
		t.Fatal("palette action filtering failed")
	}
}
func TestTUIKeyFileImportAndGeneration(t *testing.T) {
	m := testUI(t)
	key := testIdentity("server", 7)
	path := filepath.Join(t.TempDir(), "new key.json")
	if e := os.WriteFile(path, key.Private, 0600); e != nil {
		t.Fatal(e)
	}
	m = openUIOperation(t, m, "key.add")
	setField(t, m.form, "name", "imported")
	setField(t, m.form, "role", "server")
	setField(t, m.form, "file", path)
	m = runUIForm(t, m)
	if m.config.Keys["imported"].Public != key.Public {
		t.Fatal("key file not imported")
	}
	if runtime.GOOS == "windows" {
		return
	}
	dir := t.TempDir()
	generated := testIdentity("client", 8)
	fixturePath := filepath.Join(dir, "generated.json")
	if e := os.WriteFile(fixturePath, generated.Private, 0600); e != nil {
		t.Fatal(e)
	}
	t.Setenv("TAILCHASE_TEST_KEY", fixturePath)
	script := "#!/bin/sh\nif [ \"$1\" = genkey ]; then\n  for arg in \"$@\"; do case \"$arg\" in --key=*) cp \"$TAILCHASE_TEST_KEY\" \"${arg#--key=}\" ;; esac; done\nelse\n  echo '" + generated.Public + "'\nfi\n"
	if e := os.WriteFile(filepath.Join(dir, "tailcat"), []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	m.result = nil
	m = openUIOperation(t, m, "key.generate")
	setField(t, m.form, "name", "generated")
	m = runUIForm(t, m)
	if m.config.Keys["generated"].Public != generated.Public {
		t.Fatal("key generation not applied")
	}
	// The same SSH preparation path is used by CLI and the suspended TUI session.
	cmd, cleanup, e := m.store.prepareSSH("home", "alice")
	if e != nil {
		t.Fatal(e)
	}
	defer cleanup()
	if !strings.HasSuffix(cmd.Args[len(cmd.Args)-1], "alice@secret-address") {
		t.Fatal("SSH target incorrect")
	}
	if e = cmd.Run(); e != nil {
		t.Fatal(e)
	}
}
func TestTUIBackgroundDaemonSurvivesUIAndUsesItsStore(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses POSIX termination")
	}
	s := Store{Dir: t.TempDir()}
	cmd := exec.Command(os.Args[0], "-test.run=^TestDaemonHelper$")
	cmd.Env = append(os.Environ(), "TAILCHASE_TEST_DAEMON=1")
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})
	if e := s.startBackgroundDaemon(cmd); e != nil {
		t.Fatal(e)
	}
	if _, e := s.runtime(); e != nil {
		t.Fatal("daemon not ready", e)
	}
	// A second launch is a no-op, even when its executable would be invalid.
	if e := s.startBackgroundDaemon(exec.Command("no-such-tailchase")); e != nil {
		t.Fatal(e)
	}
	if e := cmd.Process.Signal(syscall.SIGTERM); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, e := os.Stat(filepath.Join(s.Dir, "runtime.json")); os.IsNotExist(e) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("background daemon did not shut down")
}

func TestTUIInputBlinkDoesNotResetMenuOrChoiceSelection(t *testing.T) {
	m := testUI(t)
	m.openMenu()
	m, _ = press(m, tea.KeyDown)
	next, _ := m.Update(struct{}{})
	m = next.(tuiModel)
	if m.menu.cursor != 1 {
		t.Fatal("background input message reset palette cursor")
	}
	m.menu = nil
	m = openUIOperation(t, m, "service.add")
	m.form.focus = 1
	m, _ = press(m, tea.KeyEnter)
	m, _ = press(m, tea.KeyDown)
	next, _ = m.Update(struct{}{})
	m = next.(tuiModel)
	if m.form.choice.cursor != 1 {
		t.Fatal("background input message reset choice cursor")
	}
}

func TestTUIResultScrollingClampsAtEnd(t *testing.T) {
	m := testUI(t)
	m.width = 50
	m.height = 16
	m.result = &uiResult{title: "Long result", content: strings.Repeat("a", 500)}
	m, _ = press(m, tea.KeyEnd)
	end := m.result.offset
	for i := 0; i < 50; i++ {
		m, _ = press(m, tea.KeyDown)
	}
	if m.result.offset != end {
		t.Fatal("scroll escaped lower bound")
	}
	m, _ = press(m, tea.KeyUp)
	if m.result.offset != max(0, end-1) {
		t.Fatal("scroll did not move back from bottom")
	}
	for _, line := range strings.Split(m.View().Content, "\n") {
		if ansi.StringWidth(line) > m.width {
			t.Fatal("wrapped result overflow")
		}
	}
}

func TestTUIShowShortcutInspectsSelectedEntryDirectly(t *testing.T) {
	for _, test := range []struct {
		tab  int
		id   string
		want string
	}{{2, "me", fixture().Keys["me"].Public}, {1, "home", "secret-address"}} {
		t.Run(test.id, func(t *testing.T) {
			m := testUI(t)
			m.tab = test.tab
			for i, id := range m.rows() {
				if id == test.id {
					m.cursor = i
					break
				}
			}
			m, cmd := press(m, 'v')
			if m.form != nil || cmd == nil {
				t.Fatal("show shortcut reopened a selection form")
			}
			next, _ := m.Update(cmd())
			m = next.(tuiModel)
			if m.result == nil || !strings.Contains(m.result.content, test.want) {
				t.Fatal("selected entry was not shown")
			}
			if strings.Contains(m.result.content, "privkey:") {
				t.Fatal("show shortcut revealed a private key")
			}
		})
	}
	m := newTUI(Store{Dir: t.TempDir()}, empty())
	m.tab = 2
	m, cmd := press(m, 'v')
	if cmd != nil || m.form != nil || !m.failed {
		t.Fatal("empty selection started a lookup")
	}
	m = testUI(t)
	m.tab = 2
	m.busy = true
	_, cmd = press(m, 'v')
	if cmd != nil {
		t.Fatal("overlapping show command started")
	}
}
