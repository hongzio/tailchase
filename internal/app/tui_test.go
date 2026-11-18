package app

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func testUI(t *testing.T) tuiModel {
	t.Helper()
	s := Store{Dir: t.TempDir()}
	c := fixture()
	if e := s.Update(func(dst *Config) error { *dst = c; return nil }); e != nil {
		t.Fatal(e)
	}
	return newTUI(s, c)
}
func press(m tuiModel, code rune) (tuiModel, tea.Cmd) {
	key := tea.KeyPressMsg{Code: code}
	if unicode.IsPrint(code) {
		key.Text = string(code)
	}
	next, cmd := m.Update(key)
	return next.(tuiModel), cmd
}
func TestTUIServiceToggleAndConflict(t *testing.T) {
	m := testUI(t)
	m, cmd := press(m, tea.KeySpace)
	if cmd == nil {
		t.Fatal("Space did not toggle service")
	}
	msg := cmd().(tuiAction)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	m0, e := m.store.Load()
	if e != nil {
		t.Fatal(e)
	}
	if m0.Services["web"].Enabled {
		t.Fatal("service not disabled")
	}
	// A second command from the stale snapshot must not reverse the first change.
	if e = m.store.toggleService("web", m.config.Services["web"]); e == nil {
		t.Fatal("stale service toggle overwrote a concurrent change")
	}
	next, _ := m.Update(msg)
	if next.(tuiModel).busy {
		t.Fatal("save completion left UI busy")
	}
}
func TestTUIAllowSearchCancelAndSave(t *testing.T) {
	m := testUI(t)
	m, _ = press(m, 'a')
	if m.picker == nil {
		t.Fatal("allow picker not opened")
	}
	before, _ := m.store.Load()
	// Search accepts shortcuts as literal text and preserves off-screen selection.
	m, _ = press(m, '/')
	m, _ = press(m, 'h')
	if m.picker == nil || m.tab != 0 || len(m.rows()) != 1 || m.rows()[0] != "home-nodekey" {
		t.Fatal("search dispatched a shortcut or failed", m.rows())
	}
	m, _ = press(m, tea.KeyEnter)
	m, _ = press(m, tea.KeySpace)
	if !m.picker.selected["home-nodekey"] || !m.picker.selected["friend"] {
		t.Fatal("filter lost hidden selection")
	}
	after, _ := m.store.Load()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("draft selection wrote configuration")
	}
	m, _ = press(m, tea.KeyEsc)
	after, _ = m.store.Load()
	if m.picker != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("cancel saved a draft")
	}
	m, _ = press(m, 'a')
	m, _ = press(m, tea.KeySpace) // friend is first; remove the existing selection
	m, cmd := press(m, tea.KeyEnter)
	if cmd == nil {
		t.Fatal("Enter did not save")
	}
	msg := cmd().(tuiAction)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	after, _ = m.store.Load()
	v := after.Services["web"]
	if len(v.Allow) > 0 || v.AllowAnyone {
		t.Fatal("empty selection did not deny all")
	}
	args, _ := m.store.command(after, v)
	if !contains(args, "--allow=none") {
		t.Fatal("empty policy opened access")
	}
	next, _ := m.Update(msg)
	if next.(tuiModel).picker != nil {
		t.Fatal("saved picker did not close")
	}
}
func TestTUIAllowPreservesManualKeysAndRejectsChangedIdentity(t *testing.T) {
	m := testUI(t)
	manual := m.config.Keys["me"].Public
	if e := m.store.Update(func(c *Config) error {
		v := c.Services["web"]
		v.PublicKeys = []string{manual}
		c.Services["web"] = v
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	m.config, _ = m.store.Load()
	m, _ = press(m, 'a')
	d := *m.picker
	// The daemon may update an address while the picker is open.
	if e := m.store.Update(func(c *Config) error {
		r := c.Remotes["home"]
		r.Address = "new-address"
		c.Remotes["home"] = r
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if e := m.store.saveAllow(d); e != nil {
		t.Fatal("unrelated address change blocked save", e)
	}
	c, _ := m.store.Load()
	if !reflect.DeepEqual(c.Services["web"].PublicKeys, []string{manual}) {
		t.Fatal("manual keys lost")
	}
	if e := m.store.Update(func(c *Config) error {
		r := c.Remotes["friend"]
		r.Value = c.Keys["me"].Public
		c.Remotes["friend"] = r
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if e := m.store.saveAllow(d); e == nil {
		t.Fatal("changed client identity accepted")
	}
}
func TestTUIDisabledAliasAndKeyboardNavigation(t *testing.T) {
	m := testUI(t)
	m.config.Remotes["aaa-no-client"] = Remote{Server: "host"}
	m, _ = press(m, 'a')
	if containsString(m.rows(), "aaa-no-client") {
		t.Fatal("non-nodekey alias offered for allow")
	}
	m, _ = press(m, tea.KeySpace)
	m, _ = press(m, tea.KeyEsc)
	m, _ = press(m, tea.KeyTab)
	if m.tab != 1 {
		t.Fatal("Tab did not select remotes")
	}
	m, _ = press(m, '3')
	if m.tab != 2 {
		t.Fatal("key tab shortcut failed")
	}
	m, _ = press(m, tea.KeyDown)
	if m.cursor != 1 {
		t.Fatal("arrow navigation failed")
	}
	m.search.SetValue("host")
	m.cursor = 0
	next, _ := m.Update(tuiSnapshot{config: m.config})
	if next.(tuiModel).current() != "host" {
		t.Fatal("refresh lost filtered selection")
	}
}
func TestTUIResponsiveViewsAndNoSecrets(t *testing.T) {
	m := testUI(t)
	for _, size := range [][2]int{{120, 36}, {90, 24}, {60, 20}, {50, 16}, {24, 8}} {
		for tab := 0; tab < 3; tab++ {
			t.Run(fmt.Sprint(size, tab), func(t *testing.T) {
				m.tab = tab
				m.width = size[0]
				m.height = size[1]
				view := m.View()
				if !view.AltScreen {
					t.Fatal("alternate screen disabled")
				}
				if len(strings.Split(view.Content, "\n")) > m.height {
					t.Fatal("view overflows height")
				}
				for _, line := range strings.Split(view.Content, "\n") {
					if ansi.StringWidth(line) > m.width {
						t.Fatal("view overflows width")
					}
				}
				if (tab != 1 && strings.Contains(view.Content, "secret-address")) || strings.Contains(view.Content, "privkey:") {
					t.Fatal("view exposes secrets")
				}
			})
		}
	}
	m.width = 100
	m.height = 30
	m.tab = 0
	m, _ = press(m, 'a')
	m.picker.selected = map[string]bool{}
	if !strings.Contains(m.View().Content, "DENY ALL") {
		t.Fatal("empty selection has no deny-all indication")
	}
	// Tiny terminals must not let invisible actions mutate the configuration.
	m.width = 20
	m.height = 5
	m, _ = press(m, tea.KeySpace)
	if len(m.picker.selected) > 0 {
		t.Fatal("hidden picker accepted a mutation")
	}
}
func TestTUILargeListsScrollAndEmptyViews(t *testing.T) {
	m := testUI(t)
	for i := 0; i < 80; i++ {
		m.config.Remotes[fmt.Sprintf("remote-%02d", i)] = Remote{}
	}
	m.tab = 1
	m, _ = press(m, tea.KeyEnd)
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "remote-79") {
		t.Fatal("selected row is outside viewport")
	}
	m.search.SetValue("no-match")
	m.clamp()
	if !strings.Contains(m.View().Content, "No matches") {
		t.Fatal("no empty search state")
	}
	m = newTUI(Store{Dir: t.TempDir()}, empty())
	if !strings.Contains(m.View().Content, "GET STARTED") {
		t.Fatal("no onboarding for empty config")
	}
}

func TestTUIPanelsFillTheirAllocatedSpace(t *testing.T) {
	m := testUI(t)
	for _, panel := range []string{m.listPanel(72, 25), m.detailsPanel(48, 25)} {
		if lipgloss.Height(panel) != 25 {
			t.Fatal("panels have uneven heights", lipgloss.Height(panel))
		}
	}
	if lipgloss.Width(m.listPanel(72, 25)) != 72 || lipgloss.Width(m.detailsPanel(48, 25)) != 48 {
		t.Fatal("panel widths do not fill split layout")
	}
}

func TestTUISeparatesDesiredAndActualStates(t *testing.T) {
	m := testUI(t)
	if m.state("web") != "pending · daemon offline" {
		t.Fatal("offline process shown as running")
	}
	m.online = true
	m.runtime.Services = map[string]State{"web": {State: "disabled"}}
	if m.state("web") != "pending" {
		t.Fatal("stale disabled state ignored desired ON")
	}
	m.runtime.Services["web"] = State{State: "retrying", Error: "temporary error"}
	if m.state("web") != "retrying" {
		t.Fatal("runtime state not displayed")
	}
	m.search.SetValue("web")
	next, _ := m.Update(tuiSnapshot{online: false, err: fmt.Errorf("invalid configuration")})
	if next.(tuiModel).online {
		t.Fatal("failed refresh kept stale online state")
	}
}
