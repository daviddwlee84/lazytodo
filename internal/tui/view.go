package tui

import (
	"fmt"
	"strings"

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
	lead, viewName, sep, scope := m.headerParts()
	header := lead + viewName + sep + scope
	if m.scopeSource != "" && m.scopeSource == m.repoSourceID {
		header += "  (cwd)"
	}
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
			name = "Sources · Enter focuses · cached status"
		}
		if m.overlay == repoOverlay {
			name = "Current repository · explicit registration"
		}
		if m.overlay == tagsOverlay {
			name = "Tags · exact label filters"
		}
		lines := m.overlayLines(max(1, m.width-2))
		start := min(m.overlayScroll, max(0, len(lines)-m.contentHeight()))
		body = m.renderPane(name, lines[start:], m.width, true)
	} else if m.width >= 90 {
		left := m.renderPane("Items", m.listLines(m.listWidth()-2), m.listWidth(), m.focus == listPane)
		right := m.renderPane(m.previewTitle(), m.visiblePreview(), m.width-m.listWidth(), m.focus == previewPane)
		body = lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	} else if m.focus == listPane {
		body = m.renderPane("Items", m.listLines(max(1, m.width-2)), m.width, true)
	} else {
		body = m.renderPane(m.previewTitle(), m.visiblePreview(), m.width, true)
	}
	summary := m.sourceSummary()
	if m.sourceError != "" {
		summary = "Source status error: " + oneLine(m.sourceError)
	}
	status := m.countLabel()
	if m.configError != "" {
		status = "Configuration invalid/not applied · refresh paused · s then e to repair"
	} else if m.gPending {
		status = "g … (g: first, Esc: cancel)"
	} else if v.err != "" {
		status = "Query failed: " + oneLine(v.err)
	} else if m.notice != "" {
		status += "  ·  " + oneLine(m.notice)
	}
	// Show pagination when no operation outcome needs the status line.
	if v.total > pageSize && m.notice == "" && v.err == "" && !m.gPending && m.configError == "" {
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
	if m.mouse {
		// Cell motion: click, release, wheel and drag motion (for preview copy).
		view.MouseMode = tea.MouseModeCellMotion
	}
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
			border = ld(lipgloss.Color("#376bb0"), lipgloss.Color("#739bda"))
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
			return wrapped("No sources configured.\n\nRun: lazytodo init\nChoose recent repositories/projects using existing gh/glab logins.\n\nManual setup: lazytodo config edit\nMissing tools/login get setup instructions. Normal startup discovers nothing.", width)
		}
		if len(m.config.Active()) == 0 {
			return wrapped("All sources are disabled. Enable a source in config.toml, or browse locally pinned items with the views picker.", width)
		}
		if v.err != "" {
			return wrapped("Could not load items.\n"+v.err+"\nRefresh to retry.", width)
		}
		if v.filter != "" {
			return wrapped("No items match this search.\nEsc clears the search, or choose another view with v.", width)
		}
		if m.scopeSource != "" || v.base != "" {
			// An empty view or repository is not a search result: say where you are
			// and how to widen it.
			where := "No items in the " + oneLine(v.name) + " view"
			if m.scopeSource != "" {
				where += " for " + oneLine(m.scopeName())
			}
			lines := []string{where + "."}
			if m.scopeSource != "" {
				if st := m.sourceStatus(m.scopeSource); st.ObservedAt == nil || !st.Complete {
					lines = append(lines, "This source has no complete snapshot yet; r fetches it.")
				}
				lines = append(lines, "Esc (or s → All sources) returns to every source.")
			}
			if v.base != "" {
				lines = append(lines, "v switches views; All includes closed items.")
			}
			return wrapped(strings.Join(lines, "\n"), width)
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
	// Size the repo column to the widest visible label instead of always padding to the cap.
	repoWidth := 0
	if m.scopeSource == "" {
		for index := v.scroll; index < len(v.items) && index < v.scroll+m.contentHeight(); index++ {
			repoWidth = max(repoWidth, ansi.StringWidth(m.repoLabel(v.items[index])))
		}
		repoWidth = min(repoWidth, min(32, max(12, width/4)))
	}
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
		metadata := ""
		if width >= 40 {
			metadata = m.stateLabel(item.State) + " "
		}
		if m.scopeSource == "" {
			metadata += pad(m.repoLabel(item), repoWidth) + " "
		}
		if width >= 55 {
			metadata += pad(nativeNumber(item), 7) + " "
		}
		lead := prefix + pin + " " + metadata
		line := fit(lead+m.titleWithTags(item, label, max(1, width-ansi.StringWidth(lead))), width)
		if index == v.selected {
			line = pad(line, width)
			if m.color {
				ld := lipgloss.LightDark(m.dark)
				line = lipgloss.NewStyle().Foreground(ld(lipgloss.Color("#202020"), lipgloss.Color("#eeeeee"))).Background(ld(lipgloss.Color("#e4edf9"), lipgloss.Color("#293c57"))).Render(line)
			}
		}
		lines = append(lines, line)
	}
	return lines
}
func (m *app) visiblePreview() []string {
	lines := m.previewLines(m.previewWidth())
	start := min(m.current().preview, max(0, len(lines)-1))
	visible := lines[start:min(len(lines), start+m.contentHeight())]
	if m.sel.active {
		if item, ok := m.selected(); ok && item.Ref == m.sel.ref {
			visible = append([]string(nil), visible...)
			for i := range visible {
				visible[i] = m.highlight(visible[i], start+i)
			}
		}
	}
	return visible
}

// previewTitle reports the scroll position once the preview overflows.
func (m *app) previewTitle() string {
	total, rows := len(m.previewLines(m.previewWidth())), m.contentHeight()
	if total <= rows {
		return "Preview"
	}
	start := min(m.current().preview, max(0, total-1))
	return fmt.Sprintf("Preview · %d–%d/%d", start+1, min(total, start+rows), total)
}
func (m *app) previewLines(width int) []string {
	item, ok := m.selected()
	if !ok {
		return wrapped("Select an item to preview its cached content.\n\nEnter inspects; opening the native URL is always explicit.", width)
	}
	var lines []string
	for _, l := range wrapped(oneLine(item.Title), width) {
		lines = append(lines, m.strong(l))
	}
	state := item.State
	if state == "" {
		state = "unknown"
	}
	// Each part is sanitized by oneLine before styling; wrap without re-sanitizing.
	meta := oneLine(item.DisplayRef) + "  ·  " + m.stateColor(state, oneLine(state))
	if item.NativeType != "" {
		meta += "  ·  " + oneLine(item.NativeType)
	}
	if item.NativeState != "" && item.NativeState != item.State && !(item.State == "open" && item.NativeState == "opened") {
		meta += " (" + oneLine(item.NativeState) + ")"
	}
	if item.Pinned {
		meta += "  ·  pinned locally"
	}
	lines = append(lines, strings.Split(ansi.Wrap(meta, max(1, width), ""), "\n")...)
	// field renders a dim "Key:" with its value; the colon keeps plain-text greps stable.
	field := func(key, value string) {
		for n, l := range wrapped(key+": "+value, width) {
			if n == 0 && strings.HasPrefix(l, key+":") {
				l = m.tint(key+":", true) + strings.TrimPrefix(l, key+":")
			}
			lines = append(lines, l)
		}
	}
	field("Source", oneLine(item.Origin.Provider)+" · "+oneLine(item.Origin.Authority))
	if !item.Available {
		lines = append(lines, wrapped("Unavailable: this local pin is absent from the current source snapshot.", width)...)
	}
	if hints := m.priorityHints(item); len(hints) > 0 {
		names := make([]string, 0, len(hints))
		for _, l := range hints {
			names = append(names, oneLine(l.Name))
		}
		field("Priority labels", strings.Join(names, ", "))
	}
	switch {
	case item.Labels == nil:
		field("Labels", "unknown / unsupported")
	case len(item.Labels) == 0:
		field("Labels", "none")
	default:
		rules := m.priorityLabelsFor(item.SourceID)
		chips := make([]string, 0, len(item.Labels))
		for _, l := range m.orderedLabels(item) {
			chips = append(chips, m.labelChip(l, model.PriorityLabel(l.Name, rules)))
		}
		lines = append(lines, strings.Split(ansi.Wrap(m.tint("Labels:", true)+" "+strings.Join(chips, " "), max(1, width), ""), "\n")...)
	}
	actors := func(key string, list []model.Actor, unknown bool) {
		if unknown {
			field(key, "unknown / unsupported")
			return
		}
		if len(list) == 0 {
			field(key, "none")
			return
		}
		names := make([]string, 0, len(list))
		for _, a := range list {
			names = append(names, oneLine(a.Name))
		}
		field(key, strings.Join(names, ", "))
	}
	actors("Assignees", item.Assignees, item.Assignees == nil)
	if len(item.Delegates) > 0 {
		actors("Delegates", item.Delegates, false)
	}
	var times []string
	if item.UpdatedAt != nil {
		times = append(times, "Updated: "+relativeTime(*item.UpdatedAt))
	}
	if !item.ObservedAt.IsZero() {
		times = append(times, "Cached: "+relativeTime(item.ObservedAt)+" · "+m.sourceStatus(item.SourceID).State)
	}
	if len(times) > 0 {
		lines = append(lines, m.tint(fit(strings.Join(times, "  |  "), width), true))
	}
	if item.URL != "" {
		field("URL", oneLine(item.URL))
	}
	lines = append(lines, m.tint(strings.Repeat("─", max(1, width)), true))
	body := strings.TrimSpace(model.Plain(item.Body))
	if body == "" {
		return append(lines, m.tint("No body in cached content.", true))
	}
	return append(lines, m.renderBody(item.Ref, body, width)...)
}
func (m *app) overlayLines(width int) []string {
	lines, _ := m.overlayRows(width)
	return lines
}

// overlayRows returns overlay lines and, in parallel, what a click on each line
// selects. The same loop builds both, so scroll windows are never recomputed.
func (m *app) overlayRows(width int) ([]string, []rowHit) {
	switch m.overlay {
	case viewsOverlay:
		var lines []string
		var hits []rowHit
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
			hits = append(hits, rowHit{cursor: index})
		}
		return lines, hits
	case sourcesOverlay:
		if len(m.config.Sources) == 0 {
			lines := wrapped("No sources configured. Press e to edit without leaving the TUI, or use lazytodo init for guided selection. This screen never probes providers.", width)
			if m.configError != "" {
				lines = append(lines, wrapped("Config not applied: "+m.configError, width)...)
			}
			return lines, nil
		}
		return m.sourcePickerRows(width)
	case repoOverlay:
		return m.repoRows(width)
	case tagsOverlay:
		return m.tagRows(width)
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
		lines = append(lines, "", "SOURCES / TAGS", "s then e edits local sources and reloads valid settings on return.", "t shows the selected item's labels; Enter adds an exact label filter.", "Priority labels are display hints, not shared urgency or sorting ranks.")
		lines = append(lines, "", "MOUSE")
		lines = append(lines, wrapped("Click selects (click the selected row again to inspect); click a picker row twice to choose; wheel scrolls the pane under the pointer; click footer hints or the view/scope in the header. Drag in the preview to select text; release copies it (OSC 52 plus the native clipboard). y copies the URL, Y the raw body. M turns mouse capture off so the terminal's own selection works; while capture is on, hold Shift (Option in iTerm2) to drag a native selection. Config: mouse = false or --mouse=false.", width)...)
		lines = append(lines, "", "CONTEXT")
		lines = append(lines, wrapped("* local pin; ~ pin pending. The > pane heading indicates focus; the > row indicates selection. Narrow screens show only the focused pane. Source unknown/stale/error states remain visible; Sources shows cached details without probes. Pinning never changes the source. Refresh follows the selected identity; cache revision changes reset paging. Automatic refresh respects each source interval (0s disables it).", width)...)
		return lines, nil
	}
	return nil, nil
}
