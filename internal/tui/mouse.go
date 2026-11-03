package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Mouse input reuses the keyboard actions: every click or wheel event resolves
// to an existing action/selection function, so applicability and generation
// guards stay in one place. Geometry comes from the same helpers View uses.

// rowHit describes what a click on one overlay line selects.
type rowHit struct {
	cursor int       // picker cursor index, or -1 when the line is not selectable
	spans  []hitSpan // optional horizontal targets (buttons) within the line
}
type hitSpan struct{ from, to, id int }

var noHit = rowHit{cursor: -1}

func padHits(hits []rowHit, n int) []rowHit {
	for len(hits) < n {
		hits = append(hits, noHit)
	}
	return hits[:n]
}

// buttonSpans returns the cell ranges of buttons joined by sep.
func buttonSpans(buttons []string, sep string) []hitSpan {
	var spans []hitSpan
	x := 0
	for i, b := range buttons {
		w := ansi.StringWidth(b)
		spans = append(spans, hitSpan{x, x + w, i})
		x += w + ansi.StringWidth(sep)
	}
	return spans
}

type region uint8

const (
	regionNone region = iota
	regionHeader
	regionQuery
	regionList
	regionPreview
	regionOverlay
	regionSummary
	regionStatus
	regionFooter
)

// hit is a terminal cell resolved against the current layout. row is the
// content row inside a pane (-1 pane title, -2 top border, -3 bottom border);
// col is the cell column inside the pane border.
type hit struct {
	region   region
	row, col int
}

const bodyTop = 2 // header and query lines precede the bordered panes

func (m *app) tiny() bool      { return m.height < 8 || m.width < 10 }
func (m *app) contentTop() int { return bodyTop + 2 } // border + pane title
func (m *app) wide() bool      { return m.width >= 90 }
func (m *app) previewLeft() int { // first inner cell of the preview pane
	if m.wide() {
		return m.listWidth() + 1
	}
	return 1
}

func (m *app) hitTest(x, y int) hit {
	if m.tiny() || x < 0 || y < 0 || x >= m.width || y >= m.height {
		return hit{}
	}
	bodyEnd := bodyTop + m.bodyHeight()
	switch {
	case y == 0:
		return hit{region: regionHeader, col: x}
	case y == 1:
		return hit{region: regionQuery, col: x}
	case y == bodyEnd:
		return hit{region: regionSummary, col: x}
	case y == bodyEnd+1:
		return hit{region: regionStatus, col: x}
	case y == bodyEnd+2:
		return hit{region: regionFooter, col: x}
	case y < bodyTop || y > bodyEnd:
		return hit{}
	}
	row := y - m.contentTop()
	if row >= m.contentHeight() {
		row = -3
	}
	if m.overlay != noOverlay {
		return hit{region: regionOverlay, row: row, col: x - 1}
	}
	if m.wide() {
		if x < m.listWidth() {
			return hit{region: regionList, row: row, col: x - 1}
		}
		return hit{region: regionPreview, row: row, col: x - m.previewLeft()}
	}
	if m.focus == listPane {
		return hit{region: regionList, row: row, col: x - 1}
	}
	return hit{region: regionPreview, row: row, col: x - 1}
}

func (m *app) handleMouse(msg tea.MouseMsg) tea.Cmd {
	if !m.mouse || m.tiny() {
		return nil
	}
	ev := msg.Mouse()
	switch msg.(type) {
	case tea.MouseWheelMsg:
		return m.wheel(ev)
	case tea.MouseClickMsg:
		if ev.Button == tea.MouseLeft {
			return m.click(ev)
		}
	case tea.MouseMotionMsg:
		if m.sel.dragging {
			m.dragTo(ev)
		}
	case tea.MouseReleaseMsg:
		if m.sel.dragging {
			return m.finishDrag(ev)
		}
	}
	return nil
}

func (m *app) wheel(ev tea.Mouse) tea.Cmd {
	delta := 0
	switch ev.Button {
	case tea.MouseWheelUp:
		delta = -1
	case tea.MouseWheelDown:
		delta = 1
	default:
		return nil
	}
	m.userTouched = true
	direction := "down"
	if delta < 0 {
		direction = "up"
	}
	switch h := m.hitTest(ev.X, ev.Y); h.region {
	case regionOverlay:
		m.move(direction)
	case regionList:
		m.selectIndex(m.current().selected + delta)
	case regionPreview:
		m.current().preview += 3 * delta
		m.clamp()
	}
	return nil
}

func (m *app) click(ev tea.Mouse) tea.Cmd {
	m.userTouched = true
	m.gPending = false
	h := m.hitTest(ev.X, ev.Y)
	if h.region == regionFooter {
		// Resolve against the footer the user is looking at (search keys included).
		return m.clickFooter(h.col)
	}
	if m.searching {
		if h.region == regionQuery {
			return nil
		}
		m.action("accept-search")
	}
	if h.region != regionPreview {
		m.sel = previewSelection{}
	}
	switch h.region {
	case regionHeader:
		return m.clickHeader(h.col)
	case regionSummary:
		if m.overlay == noOverlay {
			return m.action("sources")
		}
	case regionList:
		m.focus = listPane
		v := m.current()
		index := v.scroll + h.row
		if h.row < 0 || index >= len(v.items) {
			return nil
		}
		if index == v.selected {
			return m.inspect()
		}
		m.selectIndex(index)
	case regionPreview:
		m.focus = previewPane
		m.sel = previewSelection{}
		if h.row >= 0 {
			if item, ok := m.selected(); ok {
				pos := textPos{line: m.previewStart() + h.row, col: max(0, h.col)}
				m.sel = previewSelection{dragging: true, ref: item.Ref, anchor: pos, head: pos}
			}
		}
	case regionOverlay:
		return m.clickOverlay(h)
	}
	return nil
}

// headerParts splits the header so the view and scope names are click targets.
func (m *app) headerParts() (lead, view, sep, scope string) {
	return "lazytodo  /  ", oneLine(m.current().name), "  /  ", oneLine(m.scopeName())
}
func (m *app) clickHeader(x int) tea.Cmd {
	if m.overlay != noOverlay {
		return nil
	}
	lead, view, sep, scope := m.headerParts()
	start := ansi.StringWidth(lead)
	end := start + ansi.StringWidth(view)
	if x >= start && x < end {
		return m.action("views")
	}
	start = end + ansi.StringWidth(sep)
	if x >= start && x < start+ansi.StringWidth(scope) {
		return m.action("sources")
	}
	return nil
}

// Footer clicks never quit or cancel; movement hints are not targets either.
var footerInert = map[string]bool{"quit": true, "cancel": true, "up": true, "down": true, "prefix": true, "top": true, "bottom": true}

func (m *app) clickFooter(x int) tea.Cmd {
	if m.gPending {
		return nil
	}
	pos := 0
	for _, s := range m.footerSegments() {
		w := ansi.StringWidth(s.text)
		if x >= pos && x < pos+w {
			if footerInert[s.id] {
				return nil
			}
			return m.action(s.id)
		}
		pos += w + ansi.StringWidth(footerSeparator)
		if pos > m.width {
			break
		}
	}
	return nil
}

func (m *app) clickOverlay(h hit) tea.Cmd {
	if h.row < 0 {
		return nil
	}
	lines, hits := m.overlayRows(max(1, m.width-2))
	index := min(m.overlayScroll, max(0, len(lines)-m.contentHeight())) + h.row
	if index < 0 || index >= len(hits) {
		return nil
	}
	target := hits[index]
	for _, s := range target.spans {
		if h.col < s.from || h.col >= s.to || m.overlay != repoOverlay || m.repoPending {
			continue
		}
		// Two-step like the keyboard: the first click focuses a button, a click
		// on the focused button confirms it (registration keeps its guards).
		if m.repoButton == s.id {
			return m.repoAction("choose")
		}
		m.repoButton = s.id
		return nil
	}
	if target.cursor < 0 {
		return nil
	}
	switch m.overlay {
	case viewsOverlay:
		if m.overlayCursor == target.cursor {
			return m.switchView(target.cursor)
		}
		m.overlayCursor = target.cursor
		m.clamp()
	case sourcesOverlay:
		if m.sourceCursor == target.cursor {
			return m.chooseSource()
		}
		m.sourceCursor = target.cursor
	case tagsOverlay:
		if m.tagCursor == target.cursor {
			return m.chooseTag()
		}
		m.tagCursor = target.cursor
	case repoOverlay:
		if m.repoPending {
			return nil
		}
		if m.repoCursor == target.cursor {
			return m.repoAction("choose")
		}
		m.repoCursor = target.cursor
	}
	return nil
}

// Preview text selection, in preview content coordinates (a previewLines
// index and a cell column) so scrolling keeps the selected text.
type textPos struct{ line, col int }
type previewSelection struct {
	dragging, active bool
	ref              string
	anchor, head     textPos
}

func (m *app) previewStart() int {
	return min(m.current().preview, max(0, len(m.previewLines(m.previewWidth()))-1))
}
func (m *app) dragTo(ev tea.Mouse) {
	if item, ok := m.selected(); !ok || item.Ref != m.sel.ref {
		m.sel = previewSelection{}
		return
	}
	row, rows := ev.Y-m.contentTop(), m.contentHeight()
	// Dragging past the pane edge scrolls one line per motion event.
	if row < 0 {
		m.current().preview--
		row = 0
	} else if row >= rows {
		m.current().preview++
		row = rows - 1
	}
	m.clamp()
	col := min(max(0, ev.X-m.previewLeft()), max(0, m.previewWidth()-1))
	m.sel.head = textPos{line: m.previewStart() + row, col: col}
	m.sel.active = m.sel.head != m.sel.anchor
}
func (m *app) finishDrag(ev tea.Mouse) tea.Cmd {
	m.dragTo(ev)
	m.sel.dragging = false
	if !m.sel.active {
		m.sel = previewSelection{}
		return nil
	}
	return m.copyText(m.selectedPreviewText(), "selection")
}

// bounds orders the selection; the end column is inclusive, as dragged.
func (s previewSelection) bounds() (textPos, textPos) {
	a, b := s.anchor, s.head
	if b.line < a.line || (b.line == a.line && b.col < a.col) {
		a, b = b, a
	}
	return a, b
}
func (s previewSelection) columns(line int) (int, int, bool) {
	if !s.active {
		return 0, 0, false
	}
	a, b := s.bounds()
	if line < a.line || line > b.line {
		return 0, 0, false
	}
	from, to := 0, 1<<30
	if line == a.line {
		from = a.col
	}
	if line == b.line {
		to = b.col + 1
	}
	return from, to, true
}
func (m *app) selectedPreviewText() string {
	if item, ok := m.selected(); !ok || item.Ref != m.sel.ref {
		return ""
	}
	lines := m.previewLines(m.previewWidth())
	var out []string
	for n := range lines {
		if from, to, ok := m.sel.columns(n); ok {
			out = append(out, strings.TrimRight(ansi.Cut(ansi.Strip(lines[n]), from, to), " "))
		}
	}
	return strings.Join(out, "\n")
}
func (m *app) highlight(line string, n int) string {
	from, to, ok := m.sel.columns(n)
	if !ok {
		return line
	}
	width := ansi.StringWidth(line)
	to = min(to, width)
	if from >= to {
		return line
	}
	marked := lipgloss.NewStyle().Reverse(true).Render(ansi.Strip(ansi.Cut(line, from, to)))
	return ansi.Cut(line, 0, from) + marked + ansi.Cut(line, to, width)
}

type clipboardMsg struct {
	generation uint64
	err        error
}

// copyText writes via OSC 52 and, locally, the native clipboard.
func (m *app) copyText(text, what string) tea.Cmd {
	if strings.TrimSpace(text) == "" {
		m.notice = "Nothing to copy."
		return nil
	}
	m.operationGeneration++
	generation := m.operationGeneration
	m.notice = fmt.Sprintf("Copied %s (%d characters).", what, utf8.RuneCountInString(text))
	cmds := []tea.Cmd{tea.SetClipboard(text)}
	if write := m.clipboardWrite; write != nil {
		cmds = append(cmds, func() tea.Msg { return clipboardMsg{generation, write(text)} })
	}
	return tea.Batch(cmds...)
}
