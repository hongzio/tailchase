package app

import (
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

type tuiTick time.Time
type tuiSnapshot struct {
	config  Config
	runtime Runtime
	online  bool
	err     error
}
type tuiAction struct {
	err         error
	message     string
	closePicker bool
}
type allowDraft struct {
	service  string
	original Service
	config   Config
	selected map[string]bool
}
type tuiModel struct {
	store         Store
	config        Config
	runtime       Runtime
	online        bool
	tab           int
	cursor        int
	width, height int
	search        textinput.Model
	searching     bool
	prefix        string
	deleteTarget  string
	picker        *allowDraft
	busy          bool
	notice        string
	failed        bool
	form          *uiForm
	menu          *uiMenu
	result        *uiResult
}

func newTUI(s Store, c Config) tuiModel {
	c = normalizeAliases(c)
	search := textinput.New()
	search.Prompt = "/ "
	search.Placeholder = "Filter by name…"
	search.CharLimit = 64
	search.SetWidth(40)
	return tuiModel{store: s, config: c, width: 100, height: 30, search: search}
}
func (s Store) tui(in io.Reader, out io.Writer) error {
	c, err := s.Load()
	if err != nil {
		return err
	}
	_, err = tea.NewProgram(newTUI(s, c), tea.WithInput(in), tea.WithOutput(out)).Run()
	return err
}
func (m tuiModel) Init() tea.Cmd { return tea.Batch(m.load(), nextTUITick()) }
func nextTUITick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tuiTick(t) })
}
func (m tuiModel) load() tea.Cmd {
	s := m.store
	return func() tea.Msg { c, e := s.Load(); r, re := s.runtime(); return tuiSnapshot{c, r, re == nil, e} }
}
func (m tuiModel) rows() []string {
	var all []string
	switch {
	case m.picker != nil:
		all = aliasesOfType(m.picker.config, aliasNodekey)
	case m.tab == 0:
		all = names(m.config.Services)
	case m.tab == 1:
		all = names(m.config.Remotes)
	default:
		all = names(m.config.Keys)
	}
	result := []string{}
	filter := strings.ToLower(m.search.Value())
	for _, n := range all {
		if strings.Contains(strings.ToLower(n), filter) {
			result = append(result, n)
		}
	}
	return result
}
func (m *tuiModel) clamp() { m.cursor = max(0, min(m.cursor, len(m.rows())-1)) }
func (m tuiModel) current() string {
	rows := m.rows()
	if m.cursor < 0 || m.cursor >= len(rows) {
		return ""
	}
	return rows[m.cursor]
}
func (m *tuiModel) feedback(err error, message string) {
	m.failed = err != nil
	if err != nil {
		m.notice = err.Error()
	} else {
		m.notice = message
	}
}
func (m *tuiModel) resetSearch() {
	m.search.Reset()
	m.search.Blur()
	m.searching = false
	m.cursor = 0
}
func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.search.SetWidth(max(10, min(50, m.width-10)))
		return m, nil
	case tuiTick:
		return m, tea.Batch(m.load(), nextTUITick())
	case tuiSnapshot:
		m.runtime = msg.runtime
		m.online = msg.online
		if msg.err != nil {
			m.feedback(msg.err, "")
			return m, nil
		}
		selected := m.current()
		m.config = msg.config
		if m.picker == nil {
			for i, n := range m.rows() {
				if n == selected {
					m.cursor = i
					break
				}
			}
		}
		m.clamp()
		return m, nil
	case uiCommandDone:
		return m.commandDone(msg)
	case uiSSHReady:
		if msg.err != nil {
			m.busy = false
			if m.form != nil {
				m.form.err = msg.err.Error()
			}
			return m, nil
		}
		return m, tea.ExecProcess(msg.cmd, func(err error) tea.Msg {
			msg.cleanup()
			return uiCommandDone{title: "SSH session", output: "Returned from SSH session.", err: err}
		})
	case tuiAction:
		m.busy = false
		m.feedback(msg.err, msg.message)
		if msg.err == nil && msg.closePicker {
			m.picker = nil
			m.resetSearch()
		}
		return m, m.load()
	case tea.KeyPressMsg:
		key := msg.String()
		if key == "ctrl+c" {
			return m, tea.Quit
		}
		if m.width < 50 || m.height < 16 {
			if key == "q" {
				return m, tea.Quit
			}
			return m, nil
		}
		if m.menu != nil {
			return m.updateMenu(msg)
		}
		if key == ":" && !m.busy && !m.searching && (m.form == nil || (!m.form.insert && (m.form.choice == nil || !m.form.choice.search.Focused()))) {
			return m, m.openMenu()
		}
		if m.result != nil {
			return m.updateResult(msg)
		}
		if m.form != nil {
			return m.updateForm(msg)
		}
		if m.searching {
			switch key {
			case "esc":
				m.resetSearch()
				return m, nil
			case "enter":
				m.search.Blur()
				m.searching = false
				m.clamp()
				return m, nil
			}
			var cmd tea.Cmd
			m.search, cmd = m.search.Update(msg)
			m.cursor = 0
			return m, cmd
		}
		pending := m.prefix
		m.prefix = ""
		if pending == "g" && (key == "t" || key == "T") {
			if key == "t" {
				key = "l"
			} else {
				key = "h"
			}
		} else if vimMove(key, &m.cursor, len(m.rows()), m.pageSize(), pending, &m.prefix) {
			return m, nil
		}
		switch key {
		case "i", "o":
			if m.picker == nil && !m.busy {
				return m.beginOperation([]string{"service.add", "remote.add", "key.create"}[m.tab])
			}
		case "e":
			if m.picker == nil && m.tab == 1 && !m.busy {
				return m.editSelectedRemote()
			}
		case "?":
			if m.picker == nil {
				return m.beginOperation("help")
			}
		case "v":
			if m.picker == nil && !m.busy {
				id := m.current()
				if id == "" {
					m.feedback(fmt.Errorf("Select an entry to inspect"), "")
					return m, nil
				}
				if m.tab == 0 {
					return m.executeUI("Service details / server address", []string{"service", "show", id}, false)
				}
				if m.tab == 1 {
					return m.executeUI("Alias details", []string{"remote", "show", id}, false)
				}
				return m.executeUI("Key details / saved server address", []string{"key", "show", id}, false)
			}
		case "d":
			if m.picker == nil && (m.tab == 1 || m.tab == 2) && !m.busy {
				if pending == "d" && m.deleteTarget == m.current() {
					if m.tab == 1 {
						return m.deleteSelectedAlias()
					}
					return m.deleteSelectedIdentity()
				}
				m.prefix = "d"
				m.deleteTarget = m.current()
			}
		case "/":
			m.searching = true
			return m, m.search.Focus()
		case "up", "k", "N":
			m.cursor--
			m.clamp()
		case "down", "j", "n":
			m.cursor++
			m.clamp()
		case "home":
			m.cursor = 0
		case "end", "G":
			m.cursor = max(0, len(m.rows())-1)
		case "pgup":
			m.cursor -= m.pageSize()
			m.clamp()
		case "pgdown":
			m.cursor += m.pageSize()
			m.clamp()
		case "esc":
			if m.busy {
				return m, nil
			}
			if m.picker != nil {
				m.picker = nil
				m.resetSearch()
				m.feedback(nil, "Allow changes discarded")
			} else {
				m.resetSearch()
			}
		case "q":
			if m.busy {
				return m, nil
			}
			if m.picker != nil {
				if !m.busy {
					m.picker = nil
					m.resetSearch()
					m.feedback(nil, "Allow changes discarded")
				}
			} else {
				return m, tea.Quit
			}
		case "tab", "right", "l", "shift+tab", "left", "h":
			if m.picker != nil {
				return m, nil
			}
			step := 1
			if key == "left" || key == "shift+tab" || key == "h" {
				step = -1
			}
			m.tab = (m.tab + step + 3) % 3
			m.resetSearch()
			m.notice = ""
		case "1", "2", "3":
			if m.picker != nil {
				return m, nil
			}
			m.tab = int(key[0] - '1')
			m.resetSearch()
			m.notice = ""
		case "r":
			return m, m.load()
		case "a":
			if m.busy || m.picker != nil || m.tab != 0 {
				return m, nil
			}
			id := m.current()
			v, ok := m.config.Services[id]
			if !ok || v.Kind != "serve" {
				m.feedback(fmt.Errorf("Select a serve service to edit allowed clients"), "")
				return m, nil
			}
			draft := &allowDraft{service: id, original: v, config: m.config, selected: map[string]bool{}}
			for _, n := range v.Allow {
				draft.selected[n] = true
			}
			m.picker = draft
			m.resetSearch()
			m.notice = ""
		case "space", " ", "enter", "ctrl+s":
			if m.busy {
				return m, nil
			}
			if m.picker != nil {
				if key == "enter" || key == "ctrl+s" {
					m.busy = true
					draft := *m.picker
					draft.selected = cloneSelection(draft.selected)
					s := m.store
					return m, func() tea.Msg {
						err := s.saveAllow(draft)
						return tuiAction{err, "Allowed clients saved; enabled service will restart", true}
					}
				}
				n := m.current()
				if n == "" {
					return m, nil
				}
				if peerPublic(m.picker.config, n) == "" {
					m.feedback(fmt.Errorf("%s has no peer public key; add a nodekey alias in Alias (i)", n), "")
					return m, nil
				}
				m.picker.selected[n] = !m.picker.selected[n]
				m.notice = ""
				return m, nil
			}
			if m.tab != 0 || key == "ctrl+s" {
				return m, nil
			}
			id := m.current()
			if id == "" {
				return m, nil
			}
			original := m.config.Services[id]
			s := m.store
			m.busy = true
			return m, func() tea.Msg {
				err := s.toggleService(id, original)
				return tuiAction{err, "Service setting saved; daemon will reconcile automatically", false}
			}
		}
		return m, nil
	}
	if m.menu != nil {
		return m.updateMenu(msg)
	}
	if m.form != nil {
		return m.updateForm(msg)
	}
	var cmd tea.Cmd
	m.search, cmd = m.search.Update(msg)
	return m, cmd
}
func cloneSelection(src map[string]bool) map[string]bool {
	dst := map[string]bool{}
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
func (s Store) toggleService(id string, original Service) error {
	return s.Update(func(c *Config) error {
		v, ok := c.Services[id]
		if !ok || !reflect.DeepEqual(v, original) {
			return fmt.Errorf("Service changed elsewhere; refresh and try again")
		}
		if !v.Enabled {
			if _, e := s.command(*c, v); e != nil {
				return e
			}
		}
		v.Enabled = !v.Enabled
		c.Services[id] = v
		return nil
	})
}
func (s Store) saveAllow(d allowDraft) error {
	return s.Update(func(c *Config) error {
		v, ok := c.Services[d.service]
		if !ok || !reflect.DeepEqual(v, d.original) {
			return fmt.Errorf("Service changed elsewhere; cancel and reopen the picker")
		}
		selected := []string{}
		for _, n := range names(d.selected) {
			if !d.selected[n] {
				continue
			}
			before, exists := d.config.Remotes[n]
			after, now := c.Remotes[n]
			if !exists || !now || peerPublic(d.config, n) == "" || peerPublic(d.config, n) != peerPublic(*c, n) || before.Type != after.Type || before.Value != after.Value || before.Peer != after.Peer || before.PeerPublic != after.PeerPublic || before.Peer == "" && before.PeerPublic == "" && before.Client != after.Client {
				return fmt.Errorf("Client identity for %s changed; cancel and reopen the picker", n)
			}
			selected = append(selected, n)
		}
		v.Allow = selected
		v.AllowAnyone = false
		c.Services[d.service] = v
		return nil
	})
}
