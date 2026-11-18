package app

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

type uiField struct {
	name, label string
	input       textinput.Model
	choices     []string
	multi       bool
	required    bool
}
type uiChoice struct {
	field    int
	search   textinput.Model
	cursor   int
	selected map[string]bool
	prefix   string
}
type uiForm struct {
	operation, title string
	target           string
	fields           []uiField
	focus            int
	choice           *uiChoice
	err              string
	insert           bool
	prefix           string
}

func field(name, label, value string, required bool) uiField {
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = "(optional)"
	if required {
		input.Placeholder = "Required"
	}
	input.CharLimit = 8192
	input.SetWidth(60)
	input.SetValue(value)
	return uiField{name: name, label: label, input: input, required: required}
}
func selectField(name, label, value string, choices []string, multi, required bool) uiField {
	f := field(name, label, value, required)
	f.choices = append([]string{}, choices...)
	f.multi = multi
	if value == "" && len(choices) > 0 && !multi {
		f.input.SetValue(choices[0])
	}
	return f
}
func (f *uiForm) value(name string) string {
	for _, v := range f.fields {
		if v.name == name {
			return v.input.Value()
		}
	}
	return ""
}
func (f *uiForm) focusField() tea.Cmd {
	if f.focus >= len(f.fields) || f.fields[f.focus].choices != nil {
		f.insert = false
	}
	for i := range f.fields {
		f.fields[i].input.Blur()
	}
	if f.insert && f.focus < len(f.fields) && f.fields[f.focus].choices == nil {
		return f.fields[f.focus].input.Focus()
	}
	return nil
}
func (m tuiModel) operationForm(id string) (*uiForm, error) {
	f := &uiForm{operation: id}
	clients, servers := []string{}, []string{}
	for _, n := range names(m.config.Keys) {
		if m.config.Keys[n].Role == "client" {

			if len(m.config.Keys[n].Private) > 0 {
				clients = append(clients, n)
			}
		} else if len(m.config.Keys[n].Private) > 0 {
			servers = append(servers, n)
		}
	}
	aliases := names(m.config.Remotes)
	services := names(m.config.Services)
	serveIDs, allowAliases := []string{}, aliasesOfType(m.config, aliasNodekey)
	for _, n := range services {
		if m.config.Services[n].Kind == "serve" {
			serveIDs = append(serveIDs, n)
		}
	}

	remote, key, service := "", "", ""
	if m.tab == 1 {
		remote = m.current()
	}
	if m.tab == 2 {
		key = m.current()
	}
	if m.tab == 0 {
		service = m.current()
	}
	switch id {
	case "remote.add", "remote.edit":
		f.title = "Add alias"
		r := Remote{Type: aliasTC}
		if id == "remote.edit" {
			if remote == "" && len(aliases) > 0 {
				remote = aliases[0]
			}
			r = m.config.Remotes[remote]
			f.title = "Edit alias"
			f.fields = append(f.fields, selectField("remote", "Alias to edit", remote, aliases, false, true), field("name", "Name", remote, true))
		} else {
			f.fields = append(f.fields, field("name", "Name", "", true))
		}
		f.fields = append(f.fields, selectField("type", "Type", aliasType(r), []string{aliasTC, aliasNodekey}, false, true), field("value", "Value", aliasValue(m.config, r), true))

	case "key.generate", "key.add", "key.create":
		f.title = "Generate identity"
		f.fields = []uiField{field("name", "Key name", "", true), selectField("role", "Role", "client", []string{"client", "server"}, false, true)}
		if id == "key.add" {
			f.title = "Import existing key"
			f.fields = append(f.fields, field("file", "Private JSON file path", "", true))
		}

		if id == "key.create" {
			f.title = "Add local private key"
			f.fields = append(f.fields, selectField("type", "Method", "generate", []string{"generate", "file"}, false, true), field("file", "Private JSON file path", "", true))
		}
	case "service.add":
		f.title = "Add service"
		f.fields = []uiField{field("name", "Service name", "", true), selectField("kind", "Kind", "forward", []string{"forward", "serve"}, false, true), selectField("remote", "TC address alias", "", aliasesOfType(m.config, aliasTC), false, true), selectField("client-key", "Local client identity (optional)", "", choiceValues(clients, true), false, false), selectField("server-key", "Server identity", "", servers, false, true), field("ports", "Ports / mappings (comma-separated)", "", true), field("bind", "Forward bind address", "127.0.0.1", false), selectField("allow", "Allowed client aliases", "", allowAliases, true, false), selectField("allow-anyone", "Allow anyone", "false", []string{"false", "true"}, false, false), field("ssh-authorized-keys", "SSH authorized key sources", "", false)}
	case "remote.show", "ssh":
		f.title = "Show alias (reveals address)"
		choices := aliases
		if id == "ssh" {
			choices = aliasesOfType(m.config, aliasTC)
			if !containsString(choices, remote) {
				remote = ""
			}
		}
		f.fields = []uiField{selectField("remote", "Alias", remote, choices, false, true)}
		if id == "ssh" {
			f.title = "Open SSH session"
			f.fields = append(f.fields, field("user", "SSH username", "", false), selectField("client-key", "Local client identity (optional)", "", choiceValues(clients, true), false, false))
		}

	case "key.show", "key.private":
		f.title = "Key details / saved server address"
		if id == "key.private" {
			f.title = "Reveal private identity (sensitive)"
		}
		f.fields = []uiField{selectField("key", "Identity", key, names(m.config.Keys), false, true)}
	case "remote.delete":
		f.title = "Delete alias"
		f.fields = []uiField{selectField("remote", "Alias to delete", remote, aliases, false, true)}
	case "key.delete":
		managed := []string{}
		for _, n := range names(m.config.Keys) {
			if m.config.Keys[n].Source != "tailcat" {
				managed = append(managed, n)
			}
		}
		if m.config.Keys[key].Source == "tailcat" {
			key = ""
		}
		f.title = "Delete managed identity"
		f.fields = []uiField{selectField("key", "Identity to delete", key, managed, false, true)}
	case "service.show":
		f.title = "Service details / server address"
		f.fields = []uiField{selectField("service", "Service", service, services, false, true)}
	case "enable", "disable":
		f.title = strings.ToUpper(id[:1]) + id[1:] + " service"
		f.fields = []uiField{selectField("service", "Service", service, services, false, true)}
	case "allow":
		f.title = "Set allowed clients"
		if m.config.Services[service].Kind != "serve" {
			service = ""
		}
		if service == "" && len(serveIDs) > 0 {
			service = serveIDs[0]
		}
		selected := ""
		mode := "selected"
		if service != "" {
			v := m.config.Services[service]
			selected = strings.Join(v.Allow, ",")
			if v.AllowAnyone {
				mode = "anyone"
			}
		}
		f.fields = []uiField{selectField("service", "Serve service", service, serveIDs, false, true), selectField("mode", "Access mode", mode, []string{"selected", "anyone"}, false, true), selectField("allow", "Allowed client aliases (empty = deny all)", selected, allowAliases, true, false)}
	case "export":
		f.title = "Export all aliases (encrypted)"
		f.fields = []uiField{field("output", "Output file (must not exist)", "", true), passwordField("password", "Encryption password"), passwordField("confirmation", "Confirm password")}
	case "import":
		f.title = "Import aliases (encrypted)"
		f.fields = []uiField{field("file", "Encrypted bundle file path", "", true), passwordField("password", "Decryption password"), selectField("dry-run", "Validate only (no writes)", "false", []string{"false", "true"}, false, false)}
	default:
		return nil, fmt.Errorf("unknown UI operation %s", id)
	}
	return f, nil
}
func (f *uiForm) arguments() ([]string, error) {
	for _, i := range f.visibleIndices() {
		if i == len(f.fields) {
			continue
		}
		v := f.fields[i]
		if v.required {
			if e := requiredValue(f, v.name); e != nil {
				return nil, e
			}
		}
	}
	value := f.value
	args := []string{}
	add := func(name string) {
		if v := value(name); v != "" {
			args = append(args, "--"+name+"="+v)
		}
	}
	switch f.operation {
	case "remote.add", "remote.edit":
		name := value("name")
		action := "add"
		if f.operation == "remote.edit" {
			action = "set"
			name = f.target
			if name == "" {
				name = value("remote")
			}
		}
		args = []string{"alias", action, name, "--type=" + value("type"), "--value=" + value("value")}
		if f.operation == "remote.edit" {
			args = append(args, "--name="+value("name"))
		}
	case "key.create":
		action := "add"
		if value("type") == "generate" {
			action = "generate"
		}
		args = []string{"key", action, value("name")}
		add("role")
		switch value("type") {
		case "file":
			add("file")
		case "generate":
		default:
			return nil, fmt.Errorf("choose generate or file")
		}
	case "key.add", "key.generate":
		_, sub, _ := strings.Cut(f.operation, ".")
		args = []string{"key", sub, value("name")}
		add("role")
		if sub == "add" {
			add("file")
		}
	case "service.add":
		args = []string{"service", "add", value("name")}
		add("kind")
		add("ports")
		if value("kind") == "forward" {
			args = append(args, "--remote="+value("remote"))
			add("bind")
			add("client-key")
		} else {
			add("server-key")
			add("ssh-authorized-keys")
			if value("allow-anyone") == "true" {
				args = append(args, "--allow-anyone")
			} else {
				add("allow")
			}
		}
	case "remote.show":
		args = []string{"remote", "show", value("remote")}
	case "key.show", "key.private":
		args = []string{"key", "show", value("key")}
		if f.operation == "key.private" {
			args = append(args, "--private")
		}
	case "remote.delete":
		name := f.target
		if name == "" {
			name = value("remote")
		}
		if name == "" {
			return nil, fmt.Errorf("select an alias to delete")
		}
		args = []string{"alias", "delete", name}
	case "key.delete":
		key := f.target
		if key == "" {
			key = value("key")
		}
		if key == "" {
			return nil, fmt.Errorf("select an identity to delete")
		}
		args = []string{"key", "delete", key}
	case "service.show":
		args = []string{"service", "show", value("service")}
	case "enable", "disable":
		args = []string{f.operation, value("service")}
	case "allow":
		args = []string{"allow", value("service")}
		if value("mode") == "anyone" {
			args = append(args, "--anyone")
		} else {
			args = append(args, split(value("allow"))...)
		}
	case "export":
		if err := validateBundlePassword(value("password")); err != nil {
			return nil, err
		}
		if value("password") != value("confirmation") {
			return nil, fmt.Errorf("passwords do not match")
		}
		args = []string{"export", "--output=" + value("output")}
	case "import":
		if err := validateBundlePassword(value("password")); err != nil {
			return nil, err
		}
		args = []string{"import", value("file")}
		if value("dry-run") == "true" {
			args = append(args, "--dry-run")
		}
	case "ssh":
		remote := value("remote")
		if f.target != "" {
			remote = f.target
		}
		args = []string{"ssh", remote}
		add("user")
		add("client-key")
	default:
		return nil, fmt.Errorf("unknown UI form %s", f.operation)
	}
	return args, nil
}
func (m tuiModel) updateForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	f := m.form
	if f.choice != nil {
		return m.updateChoice(msg)
	}
	if key, ok := msg.(tea.KeyPressMsg); ok {
		if m.busy {
			return m, nil
		}
		if !f.insert {
			pending := f.prefix
			f.prefix = ""
			visible := f.visibleIndices()
			slot := 0
			for i, index := range visible {
				if index == f.focus {
					slot = i
				}
			}
			if vimMove(key.String(), &slot, len(visible), max(1, (m.height-11)/2), pending, &f.prefix) {
				f.focus = visible[slot]
				return m, f.focusField()
			}
			switch key.String() {
			case "j", "l":
				f.moveFocus(1)
				return m, f.focusField()
			case "k", "h":
				f.moveFocus(-1)
				return m, f.focusField()
			case "i", "a", "I", "A":
				if f.focus < len(f.fields) && f.fields[f.focus].choices == nil {
					f.insert = true
					if key.String() == "I" {
						f.fields[f.focus].input.CursorStart()
					}
					if key.String() == "A" {
						f.fields[f.focus].input.CursorEnd()
					}
					return m, f.focusField()
				}
			case "q":
				m.form = nil
				return m, nil
			}
		}
		switch key.String() {
		case "esc":
			if f.insert {
				f.insert = false
				return m, f.focusField()
			}
			m.form = nil
			return m, nil
		case "ctrl+s":
			return m.submitForm()
		case "tab", "down":
			f.moveFocus(1)
			return m, f.focusField()
		case "shift+tab", "up":
			f.moveFocus(-1)
			return m, f.focusField()
		case "enter":
			if f.focus == len(f.fields) {
				return m.submitForm()
			}
			if f.fields[f.focus].choices != nil {
				search := textinput.New()
				search.Prompt = "/ "
				search.Placeholder = "Type to filter choices…"
				search.SetWidth(max(10, m.width-12))
				selected := map[string]bool{}
				v := f.fields[f.focus]
				if v.multi {
					for _, n := range split(v.input.Value()) {
						selected[n] = true
					}
				} else {
					selected[v.input.Value()] = true
				}
				cursor := 0
				if !v.multi {
					for i, n := range v.choices {
						if n == v.input.Value() {
							cursor = i
							break
						}
					}
				}
				f.choice = &uiChoice{field: f.focus, search: search, selected: selected, cursor: cursor}
				return m, nil
			}
			f.moveFocus(1)
			return m, f.focusField()
		}
	}
	if f.insert && f.focus < len(f.fields) && f.fields[f.focus].choices == nil {
		var cmd tea.Cmd
		f.fields[f.focus].input.SetWidth(max(10, m.width-10))
		f.fields[f.focus].input, cmd = f.fields[f.focus].input.Update(msg)
		return m, cmd
	}
	return m, nil
}
func (f *uiForm) choiceItems() []string {
	c := f.choice
	if c == nil {
		return nil
	}
	items := []string{}
	filter := strings.ToLower(c.search.Value())
	for _, s := range f.fields[c.field].choices {
		if strings.Contains(strings.ToLower(s), filter) {
			items = append(items, s)
		}
	}
	return items
}
func (m tuiModel) updateChoice(msg tea.Msg) (tea.Model, tea.Cmd) {
	f := m.form
	c := f.choice
	v := &f.fields[c.field]
	items := f.choiceItems()
	if key, ok := msg.(tea.KeyPressMsg); ok {
		if c.search.Focused() {
			switch key.String() {
			case "esc", "enter":
				c.search.Blur()
				return m, nil
			}
		} else {
			pending := c.prefix
			c.prefix = ""
			if vimMove(key.String(), &c.cursor, len(items), max(1, m.height-10), pending, &c.prefix) {
				return m, nil
			}
			switch key.String() {
			case "/", "i":
				return m, c.search.Focus()
			case "j":
				c.cursor = min(max(0, len(items)-1), c.cursor+1)
				return m, nil
			case "k":
				c.cursor = max(0, c.cursor-1)
				return m, nil
			case "q":
				f.choice = nil
				return m, f.focusField()
			}
			switch key.String() {
			case "esc":
				f.choice = nil
				return m, f.focusField()
			case "up":
				c.cursor = max(0, c.cursor-1)
				return m, nil
			case "down":
				c.cursor = min(max(0, len(items)-1), c.cursor+1)
				return m, nil
			case "space", " ":
				if v.multi && len(items) > 0 {
					n := items[c.cursor]
					c.selected[n] = !c.selected[n]
					return m, nil
				}
			case "enter":
				if v.multi {
					selection := []string{}
					for _, n := range names(c.selected) {
						if c.selected[n] {
							selection = append(selection, n)
						}
					}
					v.input.SetValue(strings.Join(selection, ","))
				} else {
					if len(items) == 0 {
						return m, nil
					}
					v.input.SetValue(items[c.cursor])
				}
				if f.operation == "remote.edit" && v.name == "remote" {
					r := m.config.Remotes[v.input.Value()]
					for i := range f.fields {
						switch f.fields[i].name {
						case "name":
							f.fields[i].input.SetValue(v.input.Value())
						case "type":
							f.fields[i].input.SetValue(aliasType(r))
						case "value":
							f.fields[i].input.SetValue(aliasValue(m.config, r))
						}
					}
				}
				if f.operation == "allow" && v.name == "service" {
					policy := m.config.Services[v.input.Value()]
					for i := range f.fields {
						switch f.fields[i].name {
						case "allow":
							f.fields[i].input.SetValue(strings.Join(policy.Allow, ","))
						case "mode":
							mode := "selected"
							if policy.AllowAnyone {
								mode = "anyone"
							}
							f.fields[i].input.SetValue(mode)
						}
					}
				}
				f.choice = nil
				return m, f.focusField()
			}
		}
	}

	before := c.search.Value()
	var cmd tea.Cmd
	c.search, cmd = c.search.Update(msg)
	if c.search.Value() != before {
		c.cursor = 0
	}
	return m, cmd
}

func (f *uiForm) visibleIndices() []int {
	out := []int{}
	for i, field := range f.fields {
		if f.operation == "key.create" {
			if field.name == "file" && f.value("type") != "file" {
				continue
			}
		}
		if f.operation == "service.add" {
			if f.value("kind") == "forward" && (field.name == "server-key" || field.name == "allow" || field.name == "allow-anyone" || field.name == "ssh-authorized-keys") {
				continue
			}
			if f.value("kind") == "serve" && (field.name == "bind" || field.name == "remote" || field.name == "client-key") {
				continue
			}
		}
		out = append(out, i)
	}
	return append(out, len(f.fields))
}
func (f *uiForm) moveFocus(delta int) {
	visible := f.visibleIndices()
	current := 0
	for i, index := range visible {
		if index == f.focus {
			current = i
			break
		}
	}
	f.focus = visible[(current+delta+len(visible))%len(visible)]
}

func passwordField(name, label string) uiField {
	f := field(name, label, "", true)
	f.input.EchoMode = textinput.EchoPassword
	f.input.EchoCharacter = '*'
	f.input.CharLimit = 1024
	return f
}
func (f *uiForm) clearPasswords() {
	for i := range f.fields {
		if f.fields[i].input.EchoMode == textinput.EchoPassword {
			f.fields[i].input.Reset()
		}
	}
}
