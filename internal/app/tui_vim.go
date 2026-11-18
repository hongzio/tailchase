package app

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

const vimHelp = `Vim controls

h/l  Previous/next tab (also gt/gT)
j/k  Next/previous row or field
gg/G  First/last row or field
Ctrl+d/u  Half page down/up
Ctrl+f/b  Page down/up
/  Search; Enter browse; n/N next/previous match
i/o  Add in current tab
e  Edit selected alias
dd  Delete selected alias / managed key (confirmation required)
v  Show selected entry
Space  Toggle service / checkbox
a  Allowed-client picker
:  Command / all actions; Ctrl+n/p select, Enter run
Esc  Leave input mode; q close / quit

Forms: i/a edit, I/A start/end, Esc normal mode, j/k fields
Choices: j/k move, / search, Space select, Enter apply
:w  Save form / allowed clients / selection
:q  Cancel / close; :qa quit application
:new :edit :delete :ssh :private
:import :export :daemon :status :allow :help
:services :alias :keys
All CLI operations are also available by their action IDs.`

// Navigation shared by lists, choices, forms and result views. A lone g waits
// for its second key, so it cannot move the cursor before gg is complete.
func vimMove(key string, cursor *int, total, page int, pending string, prefix *string) bool {
	switch key {
	case "g":
		if pending == "g" {
			*cursor = 0
		} else {
			*prefix = "g"
		}
	case "G":
		*cursor = max(0, total-1)
	case "ctrl+d":
		*cursor += max(1, page/2)
	case "ctrl+u":
		*cursor -= max(1, page/2)
	case "ctrl+f":
		*cursor += max(1, page)
	case "ctrl+b":
		*cursor -= max(1, page)
	default:
		return false
	}
	*cursor = max(0, min(*cursor, max(0, total-1)))
	return true
}

func (m tuiModel) exCommand(command string) (tea.Model, tea.Cmd, bool) {
	command = strings.TrimSpace(strings.TrimPrefix(command, ":"))
	aliases := map[string]string{"private": "key.private"}
	if command == "q" || command == "q!" || command == "qa" || command == "qa!" {
		m.menu = nil
		if m.busy {
			return m, nil, true
		}
		if command == "qa" || command == "qa!" {
			return m, tea.Quit, true
		}
		if m.result != nil {
			m.result = nil
		} else if m.form != nil {
			if m.form.choice != nil {
				m.form.choice = nil
			} else {
				m.form = nil
			}
		} else if m.picker != nil {
			m.picker = nil
			m.resetSearch()
		} else {
			return m, tea.Quit, true
		}
		return m, nil, true
	}
	if command == "w" {
		m.menu = nil
		if m.busy {
			return m, nil, true
		}
		if m.form != nil {
			if m.form.choice != nil {
				next, cmd := m.updateChoice(tea.KeyPressMsg{Code: tea.KeyEnter})
				return next, cmd, true
			}
			next, cmd := m.submitForm()
			return next, cmd, true
		}
		if m.picker != nil {
			next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			return next, cmd, true
		}
		m.feedback(nil, "No pending changes")
		return m, nil, true
	}
	for i, name := range []string{"services", "alias", "keys"} {
		if command == name {
			m.menu = nil
			if m.form != nil || m.picker != nil {
				m.feedback(nil, "Close the current draft with :q first")
				return m, nil, true
			}
			m.result = nil
			m.tab = i
			m.resetSearch()
			return m, nil, true
		}
	}
	if m.form != nil || m.picker != nil || m.result != nil {
		return m, nil, false
	}
	switch command {
	case "new":
		m.menu = nil
		next, cmd := m.beginOperation([]string{"service.add", "remote.add", "key.create"}[m.tab])
		return next, cmd, true
	case "edit":
		m.menu = nil
		var next tea.Model
		var cmd tea.Cmd
		if m.tab == 1 {
			next, cmd = m.editSelectedRemote()
		} else {
			next, cmd = m.beginOperation("remote.edit")
		}
		return next, cmd, true
	case "delete":
		m.menu = nil
		var next tea.Model
		var cmd tea.Cmd
		if m.tab == 1 {
			next, cmd = m.deleteSelectedAlias()
		} else if m.tab == 2 {
			next, cmd = m.deleteSelectedIdentity()
		} else {
			next, cmd = m.beginOperation("key.delete")
		}
		return next, cmd, true
	case "ssh":
		if m.tab == 1 {
			m.menu = nil
			next, cmd := m.connectSelectedRemote()
			return next, cmd, true
		}
	}
	id := command
	if alias, ok := aliases[id]; ok {
		id = alias
	}
	for _, op := range uiOperations {
		if op.id == id {
			m.menu = nil
			next, cmd := m.beginOperation(id)
			return next, cmd, true
		}
	}
	return m, nil, false
}
