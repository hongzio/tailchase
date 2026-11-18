package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func typeVim(t *testing.T, m tuiModel, keys string) (tuiModel, tea.Cmd) {
	t.Helper()
	var cmd tea.Cmd
	for _, r := range keys {
		if r == '\n' {
			r = tea.KeyEnter
		}
		m, cmd = press(m, r)
	}
	return m, cmd
}

func TestVimNavigationAndDeleteChord(t *testing.T) {
	m := testUI(t)
	m.tab = 2
	m, _ = press(m, 'G')
	last := m.cursor
	m, _ = press(m, 'g')
	if m.cursor != last {
		t.Fatal("single g moved cursor")
	}
	m, _ = press(m, 'g')
	if m.cursor != 0 {
		t.Fatal("gg did not move to first")
	}
	m, _ = press(m, 'd')
	if m.form != nil || m.busy {
		t.Fatal("single d started operation")
	}
	m, _ = press(m, 'j')
	m, _ = press(m, 'd')
	if m.form != nil {
		t.Fatal("interrupted dd deleted a different row")
	}
	m, _ = press(m, 'd')
	if m.form == nil || m.form.target != m.current() {
		t.Fatal("dd did not open confirmation for current row")
	}
	m, _ = typeVim(t, m, ":q\n")
	m, _ = typeVim(t, m, "gt")
	if m.tab != 0 {
		t.Fatal("gt did not change tab")
	}
	m, _ = press(m, 'h')
	if m.tab != 2 {
		t.Fatal("h did not wrap tab")
	}
}

func TestVimFormModesAndExSave(t *testing.T) {
	m := newTUI(Store{Dir: t.TempDir()}, empty())
	m.tab = 1
	m, _ = press(m, 'i')
	m, _ = typeVim(t, m, "hjkl:q")
	if m.form.value("name") != "hjkl:q" {
		t.Fatal("insert mode intercepted text")
	}
	m, _ = press(m, tea.KeyEsc)
	if m.form == nil || m.form.insert {
		t.Fatal("Esc did not enter normal mode")
	}
	m, _ = press(m, 'j')
	if m.form.focus != 1 {
		t.Fatal("j did not move field")
	}
	setField(t, m.form, "value", "tc-example")
	m, _ = typeVim(t, m, ":w\n")
	if !m.busy {
		t.Fatal("w did not submit")
	}
	// Actual alias name validation is shared with the CLI on submission.
}

func TestVimChoiceAndActionNavigation(t *testing.T) {
	m := testUI(t)
	m = openUIOperation(t, m, "allow")
	m.form.focus = 2
	m, _ = press(m, tea.KeyEnter)
	m, _ = press(m, 'j')
	if m.form.choice.cursor != 1 || m.form.choice.search.Value() != "" {
		t.Fatal("j inserted text into choice")
	}
	m, _ = typeVim(t, m, "/home")
	m, _ = press(m, tea.KeyEnter)
	if len(m.form.choiceItems()) != 1 {
		t.Fatal("choice search did not filter")
	}
	m, _ = press(m, tea.KeySpace)
	m, _ = typeVim(t, m, ":w\n")
	if m.form.choice != nil || m.form.value("allow") != "friend,home-nodekey" {
		t.Fatal("w did not apply filtered selection")
	}
	m, _ = typeVim(t, m, ":q\n")
	m, _ = press(m, ':')
	m, _ = press(m, tea.KeyEsc)
	m, _ = press(m, 'j')
	if m.menu.cursor != 1 {
		t.Fatal("j did not move action menu")
	}
	m, _ = press(m, 'G')
	m, _ = typeVim(t, m, "gg")
	if m.menu.cursor != 0 {
		t.Fatal("action gg failed")
	}
}

func TestVimResultPaging(t *testing.T) {
	m := testUI(t)
	m.result = &uiResult{content: strings.Repeat("line\n", 100)}
	m, _ = press(m, 'G')
	last := m.result.offset
	m, _ = press(m, 'g')
	if m.result.offset != last {
		t.Fatal("single g moved result")
	}
	m, _ = press(m, 'g')
	if m.result.offset != 0 {
		t.Fatal("result gg failed")
	}
	next, _ := m.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	m = next.(tuiModel)
	if m.result.offset != max(1, (m.height-7)/2) {
		t.Fatal("half page failed")
	}
}

func TestVimCommandsExposeEveryAction(t *testing.T) {
	for _, op := range uiOperations {
		t.Run(op.id, func(t *testing.T) {
			m := testUI(t)
			m, cmd := typeVim(t, m, ":"+op.id+"\n")
			if m.menu != nil || (m.form == nil && m.result == nil && cmd == nil) {
				t.Fatal("command did not dispatch action")
			}
		})
	}
}

func TestVimSaveAndCancelAlias(t *testing.T) {
	m := newTUI(Store{Dir: t.TempDir()}, empty())
	m.tab = 1
	m, _ = typeVim(t, m, "ilaptop")
	m, _ = press(m, tea.KeyEsc)
	setField(t, m.form, "value", "tc-example")
	m, cmd := typeVim(t, m, ":w\n")
	if cmd == nil {
		t.Fatal("missing save command")
	}
	done := cmd().(uiCommandDone)
	if done.err != nil {
		t.Fatal(done.err)
	}
	next, _ := m.Update(done)
	m = next.(tuiModel)
	c, err := m.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Remotes["laptop"]; !ok {
		t.Fatal("w failed to persist alias")
	}
	m, _ = press(m, 'q')
	m, _ = typeVim(t, m, "iunsaved")
	m, _ = press(m, tea.KeyEsc)
	// An action cannot replace an existing form and silently discard edits.
	m, _ = typeVim(t, m, ":key.generate\n")
	if m.form == nil || m.form.operation != "remote.add" {
		t.Fatal("command replaced draft")
	}
	m, _ = press(m, tea.KeyEsc)
	m, _ = press(m, 'q') // Close the command overlay.
	m, _ = typeVim(t, m, ":q\n")
	if m.form != nil {
		t.Fatal("q failed to discard form")
	}
	c, _ = m.store.Load()
	if _, ok := c.Remotes["unsaved"]; ok {
		t.Fatal("q persisted discarded alias")
	}
}

func TestVimAllowWriteUsesExistingValidation(t *testing.T) {
	m := testUI(t)
	m, _ = press(m, 'a')
	m, _ = press(m, tea.KeySpace) // Remove friend.
	m, cmd := typeVim(t, m, ":w\n")
	if cmd == nil {
		t.Fatal("missing allow save")
	}
	done := cmd().(tuiAction)
	if done.err != nil {
		t.Fatal(done.err)
	}
	next, _ := m.Update(done)
	m = next.(tuiModel)
	c, _ := m.store.Load()
	if m.picker != nil || len(c.Services["web"].Allow) != 0 {
		t.Fatal("w did not save allow draft")
	}
}
