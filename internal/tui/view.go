package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"lazytodo/internal/model"
)

func oneLine(s string) string { return strings.Join(strings.Fields(model.Plain(s)), " ") }
func fit(s string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(s, width, "…")
}
func pad(s string, width int) string {
	s = fit(s, width)
	return s + strings.Repeat(" ", max(0, width-ansi.StringWidth(s)))
}
func wrapped(s string, width int) []string {
	s = strings.ReplaceAll(model.Plain(s), "\t", "    ")
	return strings.Split(ansi.Wrap(s, max(1, width), ""), "\n")
}
func (m *app) tint(s string, muted bool) string {
	if !m.color {
		return s
	}
	ld := lipgloss.LightDark(m.dark)
	color := ld(lipgloss.Color("#333333"), lipgloss.Color("#dedede"))
	if muted {
		color = ld(lipgloss.Color("#686868"), lipgloss.Color("#999999"))
	}
	return lipgloss.NewStyle().Foreground(color).Render(s)
}
func (m *app) strong(s string) string {
	if !m.color {
		return s
	}
	return lipgloss.NewStyle().Bold(true).Render(m.tint(s, false))
}
func (m *app) styleInput() {
	styles := textinput.Styles{}
	if m.color {
		styles = textinput.DefaultStyles(m.dark)
		ld := lipgloss.LightDark(m.dark)
		styles.Focused.Prompt = lipgloss.NewStyle().Foreground(ld(lipgloss.Color("#333333"), lipgloss.Color("#dedede"))).Bold(true)
		styles.Focused.Text = lipgloss.NewStyle()
		styles.Cursor.Color = ld(lipgloss.Color("#333333"), lipgloss.Color("#dedede"))
	}
	styles.Cursor.Blink = false
	m.input.SetStyles(styles)
}
func (m *app) bodyHeight() int    { return max(1, m.height-5) }
func (m *app) contentHeight() int { return max(1, m.bodyHeight()-3) }
func (m *app) listWidth() int {
	if m.width < 90 {
		return m.width
	}
	return m.width * 43 / 100
}
func (m *app) previewWidth() int {
	if m.width < 90 {
		return max(1, m.width-2)
	}
	return max(1, m.width-m.listWidth()-2)
}
func (m *app) clamp() {
	v := m.current()
	rows := m.contentHeight()
	v.selected = min(max(0, v.selected), max(0, len(v.items)-1))
	if v.selected < v.scroll {
		v.scroll = v.selected
	}
	if v.selected >= v.scroll+rows {
		v.scroll = v.selected - rows + 1
	}
	v.scroll = min(max(0, v.scroll), max(0, len(v.items)-rows))
	v.preview = min(max(0, v.preview), max(0, len(m.previewLines(m.previewWidth()))-rows))
	if m.overlay == viewsOverlay {
		if m.overlayCursor < m.overlayScroll {
			m.overlayScroll = m.overlayCursor
		}
		if m.overlayCursor >= m.overlayScroll+rows {
			m.overlayScroll = m.overlayCursor - rows + 1
		}
	}
	m.overlayScroll = max(0, m.overlayScroll)
}

func (m *app) View() tea.View {
	v := m.current()
	header := "lazytodo  /  " + oneLine(v.name) + "  ·  read-only inbox"
	if m.fetching {
		header += "  ·  refreshing…"
	} else if v.loading {
		header += "  ·  loading cache…"
	}
	query := "All cached items"
	if v.query() != "" {
		query = "Query: " + oneLine(v.query())
	}
	if m.searching {
		query = m.input.View()
	} else {
		query = m.tint(query, true)
	}
	var body string
	if m.overlay != noOverlay {
		name := "Help"
		if m.overlay == viewsOverlay {
			name = "Views"
		}
		if m.overlay == sourcesOverlay {
			name = "Sources · cached status (no live probes)"
		}
		lines := m.overlayLines(max(1, m.width-2))
		start := min(m.overlayScroll, max(0, len(lines)-m.contentHeight()))
		body = m.renderPane(name, lines[start:], m.width, true)
	} else if m.width >= 90 {
		left := m.renderPane("List", m.listLines(m.listWidth()-2), m.listWidth(), m.focus == listPane)
		right := m.renderPane("Preview", m.visiblePreview(), m.width-m.listWidth(), m.focus == previewPane)
		body = lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	} else if m.focus == listPane {
		body = m.renderPane("List", m.listLines(max(1, m.width-2)), m.width, true)
	} else {
		body = m.renderPane("Preview", m.visiblePreview(), m.width, true)
	}
	summary := m.sourceSummary()
	if m.sourceError != "" {
		summary = "Source status error: " + oneLine(m.sourceError)
	}
	status := m.countLabel()
	if m.gPending {
		status = "g … (g: first, Esc: cancel)"
	} else if v.err != "" {
		status = "Query failed: " + oneLine(v.err)
	} else if m.notice != "" {
		status += "  ·  " + oneLine(m.notice)
	}
	// Show pagination when no operation outcome needs the status line.
	if v.total > pageSize && m.notice == "" && v.err == "" && !m.gPending {
		var pageHints []string
		for _, b := range bindings {
			if (b.id == "next-page" || b.id == "previous-page") && b.applies(m, navigation) {
				pageHints = append(pageHints, b.hint+" "+b.label)
			}
		}
		status = m.countLabel() + "  " + strings.Join(pageHints, "  ")
	}
	lines := []string{m.strong(fit(header, m.width)), fit(query, m.width), body, m.tint(fit(summary, m.width), true), fit(status, m.width), m.tint(fit(m.footer(), m.width), true)}
	text := strings.Join(lines, "\n")
	if m.height < 8 || m.width < 10 {
		text = strings.Join([]string{oneLine(v.name), m.countLabel(), m.footer()}, "\n")
	}
	// Bound every line by terminal cells, including borders, wide glyphs and tiny resizes.
	out := strings.Split(text, "\n")
	if len(out) > m.height {
		out = out[:m.height]
	}
	for i := range out {
		out[i] = fit(out[i], m.width)
	}
	view := tea.NewView(strings.Join(out, "\n"))
	view.AltScreen = true
	return view
}
func (m *app) renderPane(title string, lines []string, width int, focused bool) string {
	height := m.bodyHeight()
	inner := max(1, width-2)
	focus := "  "
	if focused {
		focus = "> "
	}
	content := []string{m.strong(pad(focus+title, inner))}
	for i := 0; i < max(0, height-3); i++ {
		line := ""
		if i < len(lines) {
			line = lines[i]
		}
		content = append(content, pad(line, inner))
	}
	style := lipgloss.NewStyle().Border(lipgloss.RoundedBorder())
	if m.color {
		ld := lipgloss.LightDark(m.dark)
		border := ld(lipgloss.Color("#b5b5b5"), lipgloss.Color("#555555"))
		if focused {
			border = ld(lipgloss.Color("#4d4d4d"), lipgloss.Color("#bdbdbd"))
		}
		style = style.BorderForeground(border)
	}
	return style.Render(strings.Join(content, "\n"))
}
func (m *app) listLines(width int) []string {
	v := m.current()
	if len(v.items) == 0 {
		if v.loading {
			return wrapped("Loading cached items… Navigation remains available.", width)
		}
		if len(m.config.Sources) == 0 {
			return wrapped("No sources configured.\n\nUse lazytodo config example for a template and lazytodo config show to find config.toml. Add a source there.\n\nCredentials stay with gh/glab; nothing is discovered automatically.", width)
		}
		if len(m.config.Active()) == 0 {
			return wrapped("All sources are disabled. Enable a source in config.toml, or browse locally pinned items with the views picker.", width)
		}
		if v.err != "" {
			return wrapped("Could not load items.\n"+v.err+"\nRefresh to retry.", width)
		}
		if v.query() != "" {
			return wrapped("No items match this view.\nClear the search or choose another view.", width)
		}
		complete := len(m.sources) > 0
		for _, source := range m.sources {
			complete = complete && source.Complete
		}
		if complete {
			return wrapped("No items in the last complete source snapshots.\nSee Sources for scope, observation times and refresh errors.", width)
		}
		return wrapped("No cached items yet.\nRefresh to fetch configured sources.\nOpen Sources for unknown, stale or failed source status.", width)
	}
	lines := make([]string, 0, m.contentHeight())
	for index := v.scroll; index < len(v.items) && len(lines) < m.contentHeight(); index++ {
		item := v.items[index]
		prefix := "  "
		if index == v.selected {
			prefix = "> "
		}
		pin := " "
		if item.Pinned {
			pin = "*"
		}
		if m.pins[item.Ref] != 0 {
			pin = "~"
		}
		label := oneLine(item.Title)
		if label == "" {
			label = oneLine(item.DisplayRef)
		}
		if label == "" {
			label = "Unavailable pinned item"
		}
		metadata := pad(oneLine(item.SourceID), min(8, max(1, width/5))) + " "
		if width >= 40 {
			metadata += pad(oneLine(item.State), 7) + " "
		}
		line := fit(prefix+pin+" "+metadata+label, width)
		if index == v.selected {
			line = pad(line, width)
			if m.color {
				ld := lipgloss.LightDark(m.dark)
				line = lipgloss.NewStyle().Foreground(ld(lipgloss.Color("#202020"), lipgloss.Color("#eeeeee"))).Background(ld(lipgloss.Color("#e8e8e8"), lipgloss.Color("#333333"))).Render(line)
			}
		}
		lines = append(lines, line)
	}
	return lines
}
func (m *app) visiblePreview() []string {
	lines := m.previewLines(m.previewWidth())
	start := min(m.current().preview, max(0, len(lines)-1))
	return lines[start:]
}
func (m *app) previewLines(width int) []string {
	item, ok := m.selected()
	if !ok {
		return wrapped("Select an item to preview its cached content.\n\nEnter inspects; opening the native URL is always explicit.", width)
	}
	var sections []string
	sections = append(sections, oneLine(item.Title))
	state := item.State
	if state == "" {
		state = "unknown"
	}
	meta := oneLine(item.DisplayRef) + "  ·  " + oneLine(state)
	if item.NativeType != "" {
		meta += "  ·  " + oneLine(item.NativeType)
	}
	if item.NativeState != "" {
		meta += " (" + oneLine(item.NativeState) + ")"
	}
	if item.Pinned {
		meta += "  ·  pinned locally"
	}
	sections = append(sections, meta, "Source: "+oneLine(item.SourceID)+"  "+oneLine(item.Scope))
	if !item.Available {
		sections = append(sections, "Unavailable: this local pin is absent from the current source snapshot.")
	}
	if item.Labels == nil {
		sections = append(sections, "Labels: unknown / unsupported")
	} else if len(item.Labels) == 0 {
		sections = append(sections, "Labels: none")
	} else {
		var labels []string
		for _, l := range item.Labels {
			labels = append(labels, oneLine(l.Name))
		}
		sections = append(sections, "Labels: "+strings.Join(labels, ", "))
	}
	if item.Assignees == nil {
		sections = append(sections, "Assignees: unknown / unsupported")
	} else if len(item.Assignees) == 0 {
		sections = append(sections, "Assignees: none")
	} else {
		var names []string
		for _, a := range item.Assignees {
			names = append(names, oneLine(a.Name))
		}
		sections = append(sections, "Assignees: "+strings.Join(names, ", "))
	}
	if len(item.Delegates) > 0 {
		var names []string
		for _, a := range item.Delegates {
			names = append(names, oneLine(a.Name))
		}
		sections = append(sections, "Delegates: "+strings.Join(names, ", "))
	}
	if item.UpdatedAt != nil {
		sections = append(sections, "Updated: "+item.UpdatedAt.Format(time.RFC3339))
	}
	if !item.ObservedAt.IsZero() {
		sections = append(sections, "Cached: "+item.ObservedAt.Format(time.RFC3339))
	}
	if item.URL != "" {
		sections = append(sections, "URL: "+oneLine(item.URL))
	}
	sections = append(sections, "")
	body := strings.TrimSpace(model.Plain(item.Body))
	if body == "" {
		body = "No body in cached content."
	}
	sections = append(sections, body)
	return wrapped(strings.Join(sections, "\n"), width)
}
func (m *app) overlayLines(width int) []string {
	switch m.overlay {
	case viewsOverlay:
		var lines []string
		for index, v := range m.views {
			prefix := "  "
			if index == m.overlayCursor {
				prefix = "> "
			}
			label := prefix + oneLine(v.name)
			if v.base != "" {
				label += "  ·  " + oneLine(v.base)
			}
			if v.filter != "" {
				label += "  / " + oneLine(v.filter)
			}
			lines = append(lines, fit(label, width))
		}
		return lines
	case sourcesOverlay:
		if len(m.sources) == 0 {
			return wrapped("No configured sources. Use lazytodo config example for a template and lazytodo config show for its path. This screen never probes providers.", width)
		}
		var lines []string
		if m.sourceError != "" {
			lines = append(lines, wrapped("Status error: "+m.sourceError, width)...)
			lines = append(lines, "")
		}
		for _, s := range m.sources {
			state := s.State
			if state == "" {
				state = "unknown"
			}
			label := fmt.Sprintf("%s · %s · %s · %d items", oneLine(s.ID), oneLine(s.Kind), oneLine(state), s.Count)
			lines = append(lines, wrapped(label, width)...)
			if s.Scope != "" {
				lines = append(lines, wrapped("  "+s.Scope, width)...)
			}
			if s.ObservedAt == nil {
				lines = append(lines, "  Cache age: unknown")
			} else {
				lines = append(lines, wrapped("  Observed: "+s.ObservedAt.Format(time.RFC3339), width)...)
			}
			if s.LastAttempt != nil {
				lines = append(lines, wrapped("  Last attempt: "+s.LastAttempt.Format(time.RFC3339), width)...)
			}
			if s.RetryAfter != nil {
				lines = append(lines, wrapped("  Retry after: "+s.RetryAfter.Format(time.RFC3339), width)...)
			}
			if s.Error != "" {
				lines = append(lines, wrapped("  Error: "+s.Error, width)...)
			}
			lines = append(lines, "")
		}
		return lines
	case helpOverlay:
		lines := []string{"NAVIGATION"}
		for _, hint := range m.hints(navigation, false) {
			lines = append(lines, wrapped(hint, width)...)
		}
		lines = append(lines, "", "SEARCH (printable keys always type)")
		for _, hint := range m.hints(searchInput, false) {
			lines = append(lines, wrapped(hint, width)...)
		}
		lines = append(lines, wrapped("Cursor edits do not reset selection. Changed text does. Searches include cached body text. Filters: source:, state:, label:, pinned:.", width)...)
		lines = append(lines, "", "CONTEXT")
		lines = append(lines, wrapped("* local pin; ~ pin pending. The > pane heading indicates focus; the > row indicates selection. Narrow screens show only the focused pane. Source unknown/stale/error states remain visible; Sources shows cached details without probes. Pinning never changes the source. Refresh follows the selected identity; cache revision changes reset paging. Automatic refresh respects each source interval (0s disables it).", width)...)
		return lines
	}
	return nil
}
