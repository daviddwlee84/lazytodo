package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"lazytodo/internal/model"
)

type keyScope uint16

const (
	navigation keyScope = 1 << iota
	searchInput
	helpKeys
	viewKeys
	sourceKeys
	repoKeys
	tagKeys
	allKeys = navigation | searchInput | helpKeys | viewKeys | sourceKeys | repoKeys | tagKeys
)

type binding struct {
	id          string
	keys        []string
	hint, label string
	scope       keyScope
	footer      bool
	available   func(*app) bool
}

// One registry owns dispatch, applicability, footer hints and expanded help.
var bindings = []binding{
	{"cancel", []string{"ctrl+c"}, "Ctrl+C", "cancel and quit", allKeys, false, nil},
	{"accept-search", []string{"enter"}, "Enter", "accept search", searchInput, true, nil},
	{"clear-search", []string{"esc"}, "Esc", "clear search", searchInput, true, nil},
	{"up", []string{"up", "k"}, "↑/k", "up", navigation | helpKeys | viewKeys | sourceKeys | tagKeys, false, nil},
	{"down", []string{"down", "j"}, "↓/j", "down", navigation | helpKeys | viewKeys | sourceKeys | tagKeys, true, nil},
	{"top", []string{"home", "gg"}, "Home/gg", "first / scroll top", navigation | helpKeys | viewKeys | sourceKeys | tagKeys, false, nil},
	{"bottom", []string{"end", "G"}, "End/G", "last / scroll bottom", navigation | helpKeys | viewKeys | sourceKeys | tagKeys, false, nil},
	{"prefix", []string{"g"}, "g", "begin gg (Esc cancels)", navigation | helpKeys | viewKeys | sourceKeys | tagKeys, false, nil},
	{"all-sources", []string{"esc"}, "Esc", "all sources", navigation, true, func(m *app) bool { return m.canLeaveScope() }},
	{"back", []string{"esc"}, "Esc", "back / clear search", navigation, false, nil},
	{"close", []string{"esc", "q", "?"}, "Esc/q/?", "close overlay", helpKeys | sourceKeys | tagKeys, true, nil},
	{"close", []string{"esc", "q", "v"}, "Esc/q/v", "close picker", viewKeys, true, nil},
	{"choose-view", []string{"enter"}, "Enter", "use view", viewKeys, true, nil},
	{"focus", []string{"tab", "shift+tab"}, "Tab/Shift+Tab", "switch pane", navigation, true, nil},
	{"left", []string{"left", "h"}, "←/h", "focus list", navigation, false, nil},
	{"right", []string{"right", "l"}, "→/l", "focus preview", navigation, false, nil},
	{"search", []string{"/"}, "/", "search", navigation, true, nil},
	{"views", []string{"v"}, "v", "views", navigation, true, nil},
	{"sources", []string{"s"}, "s", "sources", navigation, true, nil},
	{"choose-source", []string{"enter"}, "Enter", "focus source", sourceKeys, true, nil},
	{"edit-sources", []string{"e"}, "e", "edit sources", sourceKeys, true, func(m *app) bool {
		return m.editorFactory != nil && !m.editorRequested && !m.editingConfig && !m.repoPending
	}},
	{"tags", []string{"t"}, "t", "tags", navigation, true, func(m *app) bool { _, ok := m.selected(); return ok }},
	{"choose-tag", []string{"enter"}, "Enter", "filter label", tagKeys, true, func(m *app) bool { return len(m.tagLabels) > 0 }},
	{"current-repo", []string{"c"}, "c", "current repo", navigation, true, func(m *app) bool { return m.repoReady && len(m.repoResult.Targets) > 0 }},
	{"repo-ignore", []string{"esc", "q"}, "Esc", "ignore this time", repoKeys, true, func(m *app) bool { return !m.repoPending }},
	{"repo-next", []string{"tab", "right", "l", "down", "j"}, "Tab/→", "choose", repoKeys, true, func(m *app) bool { return !m.repoPending }},
	{"repo-prev", []string{"shift+tab", "left", "h", "up", "k"}, "←", "previous", repoKeys, false, func(m *app) bool { return !m.repoPending }},
	{"repo-choose", []string{"enter"}, "Enter", "confirm", repoKeys, true, func(m *app) bool { return !m.repoPending }},
	{"inspect", []string{"enter"}, "Enter", "inspect", navigation, false, func(m *app) bool { _, ok := m.selected(); return ok }},
	{"pin", []string{"p"}, "p", "pin", navigation, true, func(m *app) bool { i, ok := m.selected(); return ok && m.pins[i.Ref] == 0 }},
	{"open", []string{"o"}, "o", "open URL", navigation, true, func(m *app) bool { i, ok := m.selected(); return ok && i.URL != "" && m.opens[i.Ref] == 0 }},
	{"copy-url", []string{"y"}, "y", "copy URL", navigation, false, func(m *app) bool { i, ok := m.selected(); return ok && i.URL != "" }},
	{"copy-body", []string{"Y"}, "Y", "copy body (markdown)", navigation, false, func(m *app) bool { i, ok := m.selected(); return ok && i.Body != "" }},
	{"mouse", []string{"M"}, "M", "mouse capture on/off", navigation, false, nil},
	{"refresh", []string{"r"}, "r", "refresh", navigation, true, func(m *app) bool {
		return !m.fetching && !m.current().loading && !m.editingConfig && !m.editorRequested && m.configError == ""
	}},
	{"next-page", []string{"ctrl+n"}, "Ctrl+N", "next page", navigation, true, func(m *app) bool { v := m.current(); return !v.loading && v.offset+pageSize < v.total }},
	{"previous-page", []string{"ctrl+p"}, "Ctrl+P", "previous page", navigation, true, func(m *app) bool { v := m.current(); return !v.loading && v.offset > 0 }},
	{"help", []string{"?"}, "?", "help", navigation, true, nil},
	{"quit", []string{"q"}, "q", "quit", navigation, true, nil},
}

// canLeaveScope is the outermost Esc step: selection, preview focus and the
// search filter unwind first, then a source scope returns to All sources.
func (m *app) canLeaveScope() bool {
	return m.scopeSource != "" && m.focus == listPane && m.current().filter == "" && !m.sel.active && !m.sel.dragging
}

func (m *app) scope() keyScope {
	if m.searching {
		return searchInput
	}
	switch m.overlay {
	case helpOverlay:
		return helpKeys
	case viewsOverlay:
		return viewKeys
	case sourcesOverlay:
		return sourceKeys
	case repoOverlay:
		return repoKeys
	case tagsOverlay:
		return tagKeys
	}
	return navigation
}
func (b binding) applies(m *app, scope keyScope) bool {
	return b.scope&scope != 0 && (b.available == nil || b.available(m))
}
func (m *app) key(msg tea.KeyPressMsg) tea.Cmd {
	m.userTouched = true
	key := msg.String()
	if m.gPending {
		m.gPending = false
		if key == "g" {
			key = "gg"
		} else if key == "esc" {
			return nil
		}
	}
	for _, b := range bindings {
		if !b.applies(m, m.scope()) {
			continue
		}
		for _, k := range b.keys {
			if key == k {
				return m.action(b.id)
			}
		}
	}
	// Printable shortcuts, cursor keys and paste belong solely to the input.
	if m.searching {
		return m.updateInput(msg)
	}
	return nil
}
func (m *app) action(id string) tea.Cmd {
	switch id {
	case "cancel", "quit":
		m.cancel()
		return tea.Quit
	case "prefix":
		m.gPending = true
		m.gGeneration++
		generation := m.gGeneration
		return tea.Tick(time.Second, func(time.Time) tea.Msg { return gTimeoutMsg(generation) })
	case "search":
		m.searching, m.gPending = true, false
		m.input.SetValue(m.current().filter)
		m.input.CursorEnd()
		return m.input.Focus()
	case "accept-search":
		m.searching = false
		m.input.Blur()
	case "clear-search":
		m.searching = false
		m.input.Blur()
		m.input.SetValue("")
		if m.current().filter != "" {
			m.current().filter = ""
			return m.query(true)
		}
	case "back":
		if m.sel.active || m.sel.dragging {
			m.sel = previewSelection{}
		} else if m.focus == previewPane {
			m.focus = listPane
		} else if m.current().filter != "" {
			m.current().filter = ""
			m.input.SetValue("")
			return m.query(true)
		}
	case "all-sources":
		m.notice = "All sources; s or c returns to a repository."
		return m.changeScope("", false)
	case "focus":
		m.focus = 1 - m.focus
	case "left":
		m.focus = listPane
	case "right":
		m.focus = previewPane
	case "help":
		m.overlay, m.overlayScroll, m.gPending = helpOverlay, 0, false
	case "sources":
		m.openSourcePicker()
	case "choose-source":
		return m.chooseSource()
	case "edit-sources":
		return m.beginConfigEdit()
	case "tags":
		m.openTags()
	case "choose-tag":
		return m.chooseTag()
	case "current-repo":
		return m.openRepoContext()
	case "repo-ignore":
		return m.repoAction("ignore")
	case "repo-next":
		return m.repoAction("next")
	case "repo-prev":
		return m.repoAction("previous")
	case "repo-choose":
		return m.repoAction("choose")
	case "views":
		m.overlay, m.overlayCursor, m.overlayScroll, m.gPending = viewsOverlay, m.active, 0, false
	case "close":
		if m.editorRequested {
			m.editorRequested = false
			m.notice = "Editor request cancelled."
		}
		m.overlay, m.gPending = noOverlay, false
	case "choose-view":
		return m.switchView(m.overlayCursor)
	case "inspect":
		return m.inspect()
	case "pin":
		return m.pin()
	case "open":
		return m.open()
	case "copy-url":
		if item, ok := m.selected(); ok {
			return m.copyText(oneLine(item.URL), "URL")
		}
	case "copy-body":
		if item, ok := m.selected(); ok {
			return m.copyText(strings.TrimSpace(model.Plain(item.Body)), "body")
		}
	case "mouse":
		m.mouse = !m.mouse
		m.sel = previewSelection{}
		m.notice = "Mouse capture on: click, wheel, drag in preview copies; M turns it off."
		if !m.mouse {
			m.notice = "Mouse capture off — terminal selection works; M re-enables."
		}
	case "refresh":
		m.notice = ""
		return tea.Batch(m.query(false), m.fetchSource(m.scopeSource, false))
	case "next-page":
		return m.page(1)
	case "previous-page":
		return m.page(-1)
	case "up", "down", "top", "bottom":
		m.move(id)
	}
	return nil
}
func (m *app) move(id string) {
	delta := 1
	if id == "up" {
		delta = -1
	}
	move := func(current, limit int) int {
		switch id {
		case "top":
			return 0
		case "bottom":
			return max(0, limit)
		}
		return min(max(0, current+delta), max(0, limit))
	}
	if m.overlay == viewsOverlay {
		m.overlayCursor = move(m.overlayCursor, len(m.views)-1)
	} else if m.overlay == sourcesOverlay {
		m.sourceCursor = move(m.sourceCursor, len(m.config.Sources))
	} else if m.overlay == tagsOverlay {
		m.tagCursor = move(m.tagCursor, len(m.tagLabels)-1)
	} else if m.overlay != noOverlay {
		m.overlayScroll = move(m.overlayScroll, max(0, len(m.overlayLines(max(1, m.width-4)))-m.contentHeight()))
	} else if m.focus == previewPane {
		m.current().preview = move(m.current().preview, max(0, len(m.previewLines(m.previewWidth()))-m.contentHeight()))
	} else {
		m.selectIndex(move(m.current().selected, len(m.current().items)-1))
	}
	m.clamp()
}
func (m *app) hints(scope keyScope, footer bool) []string {
	var result []string
	for _, b := range bindings {
		if !b.applies(m, scope) || (footer && !b.footer) {
			continue
		}
		result = append(result, m.hintText(b, scope, footer))
	}
	return result
}
func (m *app) hintText(b binding, scope keyScope, footer bool) string {
	label := b.label
	if b.id == "down" {
		if footer {
			label = "move"
		}
		if m.focus == previewPane && scope == navigation {
			label = "scroll preview"
		}
	}
	hint := b.hint
	if b.id == "focus" && footer {
		hint, label = b.keys[0], "pane"
	}
	if b.id == "down" && footer {
		hint = "↑↓/jk"
	}
	return hint + " " + label
}

// footerSegment keeps the binding behind each visible hint so a click runs it.
type footerSegment struct{ id, text string }

const footerSeparator = "  "

func (m *app) footerSegments() []footerSegment {
	scope := m.scope()
	var all []footerSegment
	for _, b := range bindings {
		if !b.applies(m, scope) || !b.footer {
			continue
		}
		all = append(all, footerSegment{b.id, m.hintText(b, scope, true)})
	}
	if ansi.StringWidth(joinSegments(all)) <= m.width || scope != navigation {
		return all
	}
	// Preserve an escape hatch in narrow terminals, still using the same registry.
	var essential []footerSegment
	for _, b := range bindings {
		core := b.id == "search" || b.id == "help" || b.id == "quit" || b.id == "all-sources"
		action := m.width >= 60 && (b.id == "sources" || b.id == "views" || b.id == "pin" || b.id == "tags")
		if b.applies(m, navigation) && (core || action) {
			essential = append(essential, footerSegment{b.id, b.hint + " " + b.label})
		}
	}
	return essential
}
func joinSegments(segments []footerSegment) string {
	parts := make([]string, len(segments))
	for i, s := range segments {
		parts[i] = s.text
	}
	return strings.Join(parts, footerSeparator)
}
func (m *app) footer() string { return joinSegments(m.footerSegments()) }
