package app

import (
	"fmt"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

var (
	uiAccent   = lipgloss.NewStyle().Foreground(lipgloss.Color("#79E2C5")).Bold(true)
	uiMuted    = lipgloss.NewStyle().Foreground(lipgloss.Color("#8192A2"))
	uiError    = lipgloss.NewStyle().Foreground(lipgloss.Color("#F28FAD"))
	uiSelected = lipgloss.NewStyle().Foreground(lipgloss.Color("#E6F4F1")).Background(lipgloss.Color("#183244")).Bold(true)
	uiBorder   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#3B5366")).Padding(0, 1)
)

func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, ansi.Strip(s))
}
func (m tuiModel) pageSize() int { return max(1, m.height-14) }
func (m tuiModel) state(id string) string {
	v := m.config.Services[id]
	if !v.Enabled {
		return "disabled"
	}
	if !m.online {
		return "pending · daemon offline"
	}
	s, ok := m.runtime.Services[id]
	if !ok || s.State == "disabled" {
		return "pending"
	}
	return clean(s.State)
}
func check(on bool) string {
	if on {
		return "[x]"
	}
	return "[ ]"
}
func labelValue(label, value string) string {
	if value == "" {
		value = "—"
	}
	return uiMuted.Render(label) + "  " + clean(value)
}
func (m tuiModel) row(id string) string {
	if m.picker != nil {
		if peerPublic(m.picker.config, id) == "" {
			return "[-] " + id + uiMuted.Render(" · no peer public key")
		}
		return check(m.picker.selected[id]) + " " + id + " · " + peerFingerprint(m.picker.config, id)
	}

	switch m.tab {
	case 0:
		v := m.config.Services[id]
		return fmt.Sprintf("%s %-18s %-8s %s", check(v.Enabled), id, v.Kind, m.state(id))
	case 1:
		r := m.config.Remotes[id]
		return id + " · " + aliasType(r) + " · " + aliasValue(m.config, r)
	default:
		k := m.config.Keys[id]
		usage := k.Role + " · "
		return id + " · " + usage + identityKind(k) + " · " + keySource(k) + " · " + fingerprint(k)
	}
}
func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
func (m tuiModel) listPanel(width, height int) string {
	inner := max(1, width-4)
	rows := m.rows()
	title := []string{"SERVICES", "ALIASES", "KEYS"}[m.tab]
	if m.picker != nil {
		title = "CLIENT ALIASES"
	}
	lines := []string{uiAccent.Render(fmt.Sprintf("%s  %d", title, len(rows)))}
	count := max(1, height-4)
	start := 0
	if m.cursor >= count {
		start = m.cursor - count + 1
	}
	end := min(len(rows), start+count)
	for i := start; i < end; i++ {
		line := "  " + m.row(rows[i])
		if i == m.cursor {
			line = "› " + m.row(rows[i])
		}
		line = ansi.Truncate(line, inner, "…")
		if i == m.cursor {
			line = uiSelected.Width(inner).Render(line)
		}
		lines = append(lines, line)
	}
	if len(rows) == 0 {
		message := "No matches. Esc clears the filter."
		if m.search.Value() == "" {
			message = "Nothing here yet. Press n to add an entry."
		}
		lines = append(lines, uiMuted.Render(ansi.Truncate(message, inner, "…")))
	}
	for len(lines) < height-3 {
		lines = append(lines, "")
	}
	from := 0
	if len(rows) > 0 {
		from = start + 1
	}
	lines = append(lines, uiMuted.Render(fmt.Sprintf("%d–%d of %d", from, end, len(rows))))
	return uiBorder.Width(width).Height(height).Render(strings.Join(lines, "\n"))
}
func (m tuiModel) detailLines() []string {
	id := m.current()
	if m.picker != nil {
		d := m.picker
		count := 0
		for _, v := range d.selected {
			if v {
				count++
			}
		}
		policy := "DENY ALL"
		if count > 0 || len(d.original.PublicKeys) > 0 {
			policy = fmt.Sprintf("%d aliases + %d manual keys", count, len(d.original.PublicKeys))
		}
		lines := []string{uiAccent.Render("ACCESS POLICY"), "", labelValue("Service", d.service), labelValue("Draft", policy), "", "Space toggles an alias.", ":w saves the selection.", "q discards all changes.", "", "Saving replaces allow-anyone", "with selected-client access.", fmt.Sprintf("Manual keys preserved: %d", len(d.original.PublicKeys))}
		if id != "" {
			lines = append(lines, "", labelValue("Alias", id))
			if peerPublic(d.config, id) != "" {
				lines = append(lines, labelValue("Peer fingerprint", peerFingerprint(d.config, id)))
			} else {
				lines = append(lines, uiMuted.Render("Alias → i: add a nodekey alias."))
			}
		}

		return lines
	}
	if id == "" {
		return []string{uiAccent.Render("GET STARTED"), "", "Add keys and aliases", "with i in each tab, then add", "a forward or serve service.", "", ":  All actions", "", "j/k navigate · / search", "h/l switches sections."}
	}
	switch m.tab {
	case 0:
		v := m.config.Services[id]
		desired := "OFF"
		if v.Enabled {
			desired = "ON"
		}
		lines := []string{uiAccent.Render("SERVICE DETAILS"), "", labelValue("Name", id), labelValue("Kind", v.Kind)}
		if v.Kind == "serve" {
			lines = append(lines, labelValue("Server key", serverIdentity(m.config, v)))
		} else {
			lines = append(lines, labelValue("Alias", v.Remote))
		}
		lines = append(lines, labelValue("Desired", desired), labelValue("Actual", m.state(id)), labelValue("Ports", strings.Join(v.Ports, ", ")))
		if v.Kind == "serve" {
			policy := "deny all"
			if v.AllowAnyone {
				policy = "anyone"
			} else if len(v.Allow)+len(v.PublicKeys) > 0 {
				policy = fmt.Sprintf("%d aliases + %d manual keys", len(v.Allow), len(v.PublicKeys))
			}
			lines = append(lines, labelValue("Allow", policy), "", uiMuted.Render("a Allow clients · v Show tc address"))
		} else {
			bind := v.Bind
			if bind == "" {
				bind = "127.0.0.1"
			}
			lines = append(lines, labelValue("Bind", bind))
		}
		if m.online {
			s := m.runtime.Services[id]
			if s.Error != "" {
				lines = append(lines, "", uiError.Render(clean(s.Error)))
			}
			if s.Attempts > 0 {
				lines = append(lines, labelValue("Retries", fmt.Sprint(s.Attempts)))
			}
			if !s.NextRetry.IsZero() {
				lines = append(lines, labelValue("Retry at", s.NextRetry.Local().Format("15:04:05")))
			}
		}
		return append(lines, "", uiMuted.Render("Space / Enter  Toggle desired state"))
	case 1:
		r := m.config.Remotes[id]
		lines := []string{uiAccent.Render("ALIAS DETAILS"), "", labelValue("Name", id), labelValue("Type", aliasType(r)), labelValue("Value", aliasValue(m.config, r)), "", "v  Show full value"}
		if aliasType(r) == aliasTC {
			lines = append(lines, ":ssh  Connect using this TC address")
		} else {
			lines = append(lines, "Use in a serve service's allowed clients.")
		}
		return lines
	default:
		k := m.config.Keys[id]
		private := "unavailable"
		if len(k.Private) > 0 {
			private = "stored locally"
		}
		lines := []string{uiAccent.Render("IDENTITY DETAILS"), "", labelValue("Name", id), labelValue("Type", identityKind(k))}
		lines = append(lines, labelValue("Usage", k.Role))
		return append(lines, labelValue("Source", keySource(k)), labelValue("Fingerprint", fingerprint(k)), labelValue("Private key", private), "", "Private material stays hidden.", "", "Inspect identity:", "v Details / tc address · :private · dd Delete")
	}
}
func (m tuiModel) detailsPanel(width, height int) string {
	inner := max(1, width-4)
	lines := m.detailLines()
	if len(lines) > height-2 {
		lines = lines[:height-2]
	}
	for i, s := range lines {
		lines[i] = ansi.Truncate(s, inner, "…")
	}
	return uiBorder.Width(width).Height(height).Render(strings.Join(lines, "\n"))
}
func (m tuiModel) View() tea.View {
	var content string
	if m.width < 50 || m.height < 16 {
		content = "TAILCHASE\n\nResize terminal to at least 50 × 16.\nq / Ctrl+C quit"
	} else if m.menu != nil {
		content = m.menuView()
	} else if m.result != nil {
		content = m.resultView()
	} else if m.form != nil {
		content = m.formView()
	} else {
		status := uiMuted.Render("○ daemon offline")
		if m.online {
			status = uiAccent.Render("● daemon online")
			if m.runtime.ConfigError != "" {
				status = uiError.Render("● daemon configuration error")
			}
		}
		header := uiAccent.Render("TAILCHASE") + uiMuted.Render("  /  tailcat connection manager") + "    " + status
		tabs := []string{}
		for i, label := range []string{"1 Services", "2 Alias", "3 Keys"} {
			if m.tab == i {
				tabs = append(tabs, uiAccent.Underline(true).Render(label))
			} else {
				tabs = append(tabs, uiMuted.Render(label))
			}
		}
		navigation := strings.Join(tabs, "    ")
		if m.picker != nil {
			navigation = uiAccent.Render("← SERVICES / ALLOWED CLIENTS") + "  " + clean(m.picker.service)
		}
		search := uiMuted.Render("/ Search")
		if m.searching || m.search.Value() != "" {
			search = m.search.View()
		}
		panelHeight := m.height - 9
		var panels string
		if m.width >= 90 {
			left := m.width * 3 / 5
			panels = lipgloss.JoinHorizontal(lipgloss.Top, m.listPanel(left, panelHeight), m.detailsPanel(m.width-left, panelHeight))
		} else {
			panels = m.listPanel(m.width, panelHeight)
		}
		notice := m.notice
		if m.prefix != "" {
			notice = "NORMAL · " + m.prefix + "…"
		}
		if notice == "" && m.runtime.ConfigError != "" {
			notice = m.runtime.ConfigError
		}
		if notice == "" && len(m.config.Warnings) > 0 {
			notice = "Keys: " + m.config.Warnings[0]
		}
		if m.busy {
			notice = "Saving…"
		}
		if notice == "" {
			if m.picker != nil {
				notice = "Draft only · :w saves · q cancels"
			} else {
				notice = "Desired state is saved independently of the daemon's actual process state."
			}
		}
		if m.failed && !m.busy {
			notice = uiError.Render(clean(notice))
		} else {
			notice = uiMuted.Render(clean(notice))
		}
		help := "i New · j/k Move · h/l Tabs · Space Toggle · a Allow · : Actions · q Quit"
		if m.tab != 0 && m.picker == nil {
			help = "i New · j/k Move · h/l Tabs · v Show · / Search · : Actions · q Quit"
		}
		if m.tab == 1 && m.picker == nil {
			help = "i New · e Edit · dd Delete · v Show · :ssh · : Actions · q Quit"
		}
		if m.tab == 2 && m.picker == nil {
			help = "i Add · v Show · dd Delete · j/k Move · h/l Tabs · : Actions · q Quit"
		}
		if m.picker != nil {
			help = "j/k Move · gg/G Ends · Space Select · / Search · :w Save · q Cancel"
			count := 0
			for _, on := range m.picker.selected {
				if on {
					count++
				}
			}
			policy := "selected clients"
			if count == 0 && len(m.picker.original.PublicKeys) == 0 {
				policy = "DENY ALL"
			}
			if m.picker.original.AllowAnyone {
				policy += " · replaces allow-anyone"
			}
			notice = fmt.Sprintf("%d selected · %s · %d manual keys preserved", count, policy, len(m.picker.original.PublicKeys)) + "  " + notice
		}
		if m.searching {
			help = "Type to filter   Enter Browse results   Esc Clear filter   Ctrl+C Quit"
		}
		content = strings.Join([]string{header, "", navigation, search, panels, notice, uiMuted.Render(help)}, "\n")
	}
	lines := strings.Split(content, "\n")
	if len(lines) > m.height {
		lines = lines[:max(0, m.height)]
	}
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, max(0, m.width), "…")
	}
	view := tea.NewView(strings.Join(lines, "\n"))
	view.AltScreen = true
	return view
}
