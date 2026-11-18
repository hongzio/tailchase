package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

type uiOperation struct{ id, label string }

var uiOperations = []uiOperation{
	{"remote.add", "Alias · Add name / type / value"}, {"remote.edit", "Alias · Edit name / type / value"}, {"remote.delete", "Alias · Delete alias"}, {"remote.list", "Alias · List aliases"}, {"remote.show", "Alias · Show type and value"},
	{"key.create", "Key · Generate or import local private key"}, {"key.generate", "Key · Generate client/server identity"}, {"key.add", "Key · Import existing key file"}, {"key.list", "Key · List identities"}, {"key.show", "Key · Show details / saved server address"}, {"key.private", "Key · Reveal private identity"}, {"key.delete", "Key · Delete managed identity"},
	{"service.add", "Service · Add forward/serve"}, {"service.show", "Service · Show details / server address"}, {"allow", "Service · Configure allowed clients / anyone"}, {"enable", "Service · Enable"}, {"disable", "Service · Disable"},
	{"export", "Bundle · Export all aliases (encrypted)"}, {"import", "Bundle · Decrypt and import aliases"}, {"ssh", "Alias · Open SSH session"},
	{"daemon", "Daemon · Start in background"}, {"status", "Daemon · Show status"}, {"help", "Help · All commands"},
}

type uiMenu struct {
	search textinput.Model
	cursor int
	prefix string
}
type uiResult struct {
	title, content string
	offset         int
	prefix         string
}
type uiCommandDone struct {
	title, output string
	err           error
	keepForm      bool
}
type uiSSHReady struct {
	cmd     *exec.Cmd
	cleanup func()
	err     error
}

func (m *tuiModel) openMenu() tea.Cmd {
	m.prefix = ""
	m.deleteTarget = ""
	input := textinput.New()
	input.Prompt = ": "
	input.Placeholder = "Command or action…"
	input.SetWidth(max(10, m.width-10))
	input.CharLimit = 64
	m.menu = &uiMenu{search: input}
	return m.menu.search.Focus()
}
func (p uiMenu) items() []uiOperation {
	out := []uiOperation{}
	for _, op := range uiOperations {
		if strings.Contains(strings.ToLower(op.label+" "+op.id), strings.ToLower(p.search.Value())) {
			out = append(out, op)
		}
	}
	return out
}
func (m tuiModel) updateMenu(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		if !m.menu.search.Focused() {
			pending := m.menu.prefix
			m.menu.prefix = ""
			if vimMove(key.String(), &m.menu.cursor, len(m.menu.items()), max(1, m.height-10), pending, &m.menu.prefix) {
				return m, nil
			}
			switch key.String() {
			case "j":
				m.menu.cursor = min(max(0, len(m.menu.items())-1), m.menu.cursor+1)
				return m, nil
			case "k":
				m.menu.cursor = max(0, m.menu.cursor-1)
				return m, nil
			case "i", "/", ":":
				return m, m.menu.search.Focus()
			case "q":
				m.menu = nil
				return m, nil
			}
		}
		switch key.String() {
		case "esc":
			if m.menu.search.Focused() {
				m.menu.search.Blur()
			} else {
				m.menu = nil
			}
			return m, nil
		case "up", "ctrl+p":
			m.menu.cursor = max(0, m.menu.cursor-1)
			return m, nil
		case "down", "ctrl+n":
			m.menu.cursor = min(max(0, len(m.menu.items())-1), m.menu.cursor+1)
			return m, nil
		case "enter":
			if next, cmd, ok := m.exCommand(m.menu.search.Value()); ok {
				return next, cmd
			}
			if m.form != nil || m.picker != nil || m.result != nil {
				return m, nil
			}
			items := m.menu.items()
			if len(items) == 0 {
				return m, nil
			}
			id := items[m.menu.cursor].id
			m.menu = nil
			return m.beginOperation(id)
		}
	}
	before := m.menu.search.Value()
	var cmd tea.Cmd
	m.menu.search, cmd = m.menu.search.Update(msg)
	if m.menu.search.Value() != before {
		m.menu.cursor = 0
	}
	return m, cmd
}
func (m tuiModel) beginOperation(id string) (tea.Model, tea.Cmd) {
	if m.busy {
		return m, nil
	}
	switch id {
	case "remote.list":
		return m.executeUI("Aliases", []string{"remote", "list"}, false)
	case "key.list":
		return m.executeUI("Identities", []string{"key", "list"}, false)
	case "status":
		return m.executeUI("Daemon status", []string{"status"}, false)
	case "help":
		m.result = &uiResult{title: "All commands", content: vimHelp + "\n\nCLI reference\n\n" + help}
		return m, nil
	case "daemon":
		m.busy = true
		s := m.store
		return m, func() tea.Msg {
			exe, e := os.Executable()
			if e == nil {
				e = s.startBackgroundDaemon(exec.Command(exe, "daemon"))
			}
			return uiCommandDone{title: "Daemon", output: "Daemon running in background. It remains active after the TUI closes.", err: e}
		}
	default:
		form, e := m.operationForm(id)
		if e != nil {
			m.feedback(e, "")
			return m, nil
		}
		m.form = form
		m.notice = ""
		m.form.insert = true
		return m, m.form.focusField()
	}
}
func (m tuiModel) executeUI(title string, args []string, keep bool) (tea.Model, tea.Cmd) {
	m.busy = true
	s := m.store
	return m, func() tea.Msg {
		var output bytes.Buffer
		e := runWithStore(s, args, strings.NewReader(""), &output)
		return uiCommandDone{title, output.String(), e, keep}
	}
}
func prettyResult(s string) string {
	var b bytes.Buffer
	if json.Indent(&b, []byte(s), "", "  ") == nil {
		return b.String()
	}
	return s
}
func (m tuiModel) updateResult(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		pending := m.result.prefix
		m.result.prefix = ""
		if vimMove(key.String(), &m.result.offset, max(1, len(m.resultLines())-max(1, m.height-7)+1), max(1, m.height-7), pending, &m.result.prefix) {
			return m, nil
		}
		switch key.String() {
		case "esc", "q", "enter":
			m.result = nil
			return m, nil
		case "up", "k":
			m.result.offset = max(0, m.result.offset-1)
		case "down", "j":
			m.result.offset++
		case "pgdown":
			m.result.offset += max(1, m.height-7)
		case "pgup":
			m.result.offset = max(0, m.result.offset-max(1, m.height-7))
		case "home":
			m.result.offset = 0
		case "end", "G":
			m.result.offset = len(m.resultLines())
		}
	}
	if m.result != nil {
		m.result.offset = max(0, min(m.result.offset, max(0, len(m.resultLines())-max(1, m.height-7))))
	}
	return m, nil
}
func (m tuiModel) submitForm() (tea.Model, tea.Cmd) {
	f := m.form
	args, e := f.arguments()
	if e != nil {
		f.err = e.Error()
		return m, nil
	}
	if f.operation == "export" || f.operation == "import" {
		m.busy = true
		s := m.store
		password, confirmation := f.value("password"), f.value("confirmation")
		operation, path, title := f.operation, f.value("file"), f.title
		if operation == "export" {
			path = f.value("output")
		}
		dryRun := f.value("dry-run") == "true"
		return m, func() tea.Msg {
			if operation == "export" {
				count, err := s.exportAliases(path, password, confirmation)
				return uiCommandDone{title: title, output: fmt.Sprintf("Exported %d alias entries (encrypted).", count), err: err}
			}
			summary, err := s.importAliases(path, password, dryRun)
			output := "Imported aliases: " + summary.String()
			if dryRun {
				output = "Import validated; no changes. " + summary.String()
			}
			return uiCommandDone{title: title, output: output, err: err, keepForm: dryRun}
		}
	}
	if f.operation == "ssh" {
		m.busy = true
		s := m.store
		alias, user := args[1], f.value("user")
		return m, func() tea.Msg {
			cmd, cleanup, e := s.prepareSSHWithIdentity(alias, user, f.value("client-key"))
			return uiSSHReady{cmd, cleanup, e}
		}
	}
	return m.executeUI(f.title, args, false)
}
func (m tuiModel) commandDone(msg uiCommandDone) (tea.Model, tea.Cmd) {
	m.busy = false
	if m.form != nil {
		m.form.clearPasswords()
	}
	if msg.err != nil {
		if m.form != nil {
			m.form.err = msg.err.Error()
		} else {
			m.feedback(msg.err, "")
		}
		return m, nil
	}
	if !msg.keepForm {
		m.form = nil
	}
	output := strings.TrimSpace(msg.output)
	if output == "" {
		output = "Completed successfully."
	}
	m.result = &uiResult{title: msg.title, content: prettyResult(output)}
	m.feedback(nil, "Completed")
	return m, m.load()
}
func choiceValues(values []string, optional bool) []string {
	if optional {
		return append([]string{""}, values...)
	}
	return values
}
func requiredValue(f *uiForm, name string) error {
	value := f.value(name)
	if (name == "password" || name == "confirmation") && value != "" {
		return nil
	}
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", name)
	}
	return nil
}

func (m tuiModel) deleteSelectedIdentity() (tea.Model, tea.Cmd) {
	id := m.current()
	if id == "" {
		m.feedback(fmt.Errorf("Select an identity to delete"), "")
		return m, nil
	}
	if m.config.Keys[id].Source == "tailcat" {
		m.feedback(fmt.Errorf("Native tailcat keys are read-only; delete them with tailcat genkey --delete --key=NAME"), "")
		return m, nil
	}
	f, _ := m.operationForm("key.delete")
	f.target = id
	f.title = "Delete identity: " + id
	f.fields = nil
	f.focus = 0
	m.form = f
	return m, nil
}

func (m tuiModel) connectSelectedRemote() (tea.Model, tea.Cmd) {
	if m.current() == "" || aliasAddress(m.config.Remotes[m.current()]) == "" {
		m.feedback(fmt.Errorf("Select a TC address alias to connect"), "")
		return m, nil
	}
	f, _ := m.operationForm("ssh")
	f.target = m.current()
	f.title = "SSH: " + f.target
	f.fields = f.fields[1:]
	m.form = f
	m.form.insert = true
	return m, m.form.focusField()
}

func (m tuiModel) editSelectedRemote() (tea.Model, tea.Cmd) {
	if m.current() == "" {
		m.feedback(fmt.Errorf("Select an alias to edit"), "")
		return m, nil
	}
	f, _ := m.operationForm("remote.edit")
	f.target = m.current()
	f.title = "Edit alias: " + f.target
	f.fields = f.fields[1:]
	m.form = f
	m.form.insert = true
	return m, m.form.focusField()
}

func (m tuiModel) deleteSelectedAlias() (tea.Model, tea.Cmd) {
	id := m.current()
	if id == "" {
		m.feedback(fmt.Errorf("Select an alias to delete"), "")
		return m, nil
	}
	f, _ := m.operationForm("remote.delete")
	f.target = id
	f.title = "Delete alias: " + id
	f.fields = nil
	f.focus = 0
	m.form = f
	return m, nil
}
