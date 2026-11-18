package app

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

func (m tuiModel) modalView(title string, lines []string, footer string) string {
	height := max(4, m.height-5)
	inner := max(1, m.width-4)
	if len(lines) > height-2 {
		lines = lines[:height-2]
	}
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, inner, "…")
	}
	body := uiBorder.Width(m.width).Height(height).Render(strings.Join(lines, "\n"))
	return uiAccent.Render("TAILCHASE / "+clean(title)) + "\n" + body + "\n" + uiMuted.Render(footer)
}
func (m tuiModel) menuView() string {
	items := m.menu.items()
	count := max(1, m.height-10)
	start := max(0, m.menu.cursor-count+1)
	end := min(len(items), start+count)
	lines := []string{m.menu.search.View(), ""}
	for i := start; i < end; i++ {
		line := "  " + items[i].label
		if i == m.menu.cursor {
			line = uiSelected.Width(m.width - 4).Render("› " + items[i].label)
		}
		lines = append(lines, line)
	}
	if len(items) == 0 {
		lines = append(lines, "No matching actions.")
	}
	help := "COMMAND · Type command · Ctrl+n/p Choose · Enter Run · Esc Normal"
	if !m.menu.search.Focused() {
		help = "NORMAL · j/k Move · gg/G Ends · i Search · Enter Run · q Back"
	}
	return m.modalView("ALL ACTIONS", lines, help)
}
func (m tuiModel) formView() string {
	f := m.form
	if f.choice != nil {
		c := f.choice
		field := f.fields[c.field]
		items := f.choiceItems()
		count := max(1, m.height-10)
		start := max(0, c.cursor-count+1)
		end := min(len(items), start+count)
		lines := []string{c.search.View(), ""}
		for i := start; i < end; i++ {
			id := items[i]
			label := id
			if label == "" {
				label = "(none)"
			}
			line := "  " + check(c.selected[id]) + " " + label
			if i == c.cursor {
				line = uiSelected.Width(m.width - 4).Render("› " + check(c.selected[id]) + " " + label)
			}
			lines = append(lines, line)
		}
		if len(items) == 0 {
			if len(field.choices) > 0 {
				lines = append(lines, "No matches. Clear the search.")
			} else if field.name == "allow" {
				lines = append(lines, "No nodekey aliases.", "Alias → i: choose Type nodekey.", "Paste the public nodekey as Value.")
			} else {
				lines = append(lines, "No choices available. Add a key or alias first.")
			}
		}
		help := "NORMAL · j/k Move · gg/G Ends · / Search · Enter Choose · q Cancel"
		if field.multi {
			help = "NORMAL · j/k Move · / Search · Space Select · Enter Apply · q Cancel"
		}
		if c.search.Focused() {
			help = "SEARCH · Type to filter · Enter / Esc Normal"
		}
		return m.modalView(field.label, lines, help)
	}

	count := max(1, (m.height-11)/2)
	visible := f.visibleIndices()
	focused := 0
	for slot, index := range visible {
		if index == f.focus {
			focused = slot
		}
	}
	start := max(0, focused-count+1)
	end := min(len(visible), start+count)
	lines := []string{}
	for _, i := range visible[start:end] {
		if i == len(f.fields) {
			button := "[ Submit ]"
			if f.operation == "key.delete" {
				button = "[ Delete key ]"
			}
			if f.operation == "remote.delete" {
				button = "[ Delete alias ]"
			}
			if f.operation == "import" && f.value("dry-run") == "true" {
				button = "[ Validate only ]"
			}
			if f.operation == "ssh" {
				button = "[ Connect ]"
			}
			if f.focus == i {
				button = uiSelected.Render(button)
			}
			lines = append(lines, button, "")
			continue
		}
		field := f.fields[i]
		label := field.label
		if f.operation == "service.add" && field.name == "ports" {
			if f.value("kind") == "serve" {
				label = "Local ports / services (comma-separated)"
			} else {
				label = "Port mappings (local:remote)"
			}
		}
		if field.required {
			label += " *"
		}
		prefix := "  "
		if i == f.focus {
			prefix = "› "
		}
		value := field.input.View()
		if field.choices != nil {
			value = orDash(field.input.Value()) + "  [Enter to choose]"
		}
		if i == f.focus {
			label = uiAccent.Render(prefix + label)
		} else {
			label = uiMuted.Render(prefix + label)
		}
		lines = append(lines, label, "  "+value)
	}

	if f.err != "" {
		lines = append(lines, uiError.Render(clean(f.err)))
	}
	if m.busy {
		lines = append(lines, uiAccent.Render("Working…"))
	}
	help := "NORMAL · j/k Fields · gg/G Ends · i Edit · Enter Choose · :w Save · :q Cancel"
	if f.insert {
		help = "INSERT · Type value · Enter Next · Esc Normal"
	}
	return m.modalView(f.title, lines, help)
}
func (m tuiModel) resultLines() []string {
	if m.result == nil {
		return nil
	}
	lines := []string{}
	for _, line := range strings.Split(m.result.content, "\n") {
		wrapped := ansi.Hardwrap(clean(line), max(1, m.width-4), true)
		lines = append(lines, strings.Split(wrapped, "\n")...)
	}
	return lines
}
func (m tuiModel) resultView() string {
	lines := m.resultLines()
	count := max(1, m.height-7)
	start := max(0, min(m.result.offset, len(lines)-count))
	end := min(len(lines), start+count)
	return m.modalView(m.result.title, lines[start:end], fmt.Sprintf("j/k · gg/G · Ctrl+d/u Scroll · q Back    %d–%d of %d", start+1, end, len(lines)))
}
