package tui

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"lazytodo/internal/model"
)

func (m *app) priorityLabelsFor(sourceID string) *[]string {
	for _, s := range m.config.Sources {
		if s.ID == sourceID {
			return s.PriorityLabels
		}
	}
	return nil
}
func (m *app) priorityHints(item model.Item) []model.Label {
	var out []model.Label
	for _, l := range item.Labels {
		if model.PriorityLabel(l.Name, m.priorityLabelsFor(item.SourceID)) {
			out = append(out, l)
		}
	}
	return out
}
func (m *app) orderedLabels(item model.Item) []model.Label {
	out := make([]model.Label, 0, len(item.Labels))
	rules := m.priorityLabelsFor(item.SourceID)
	for _, priority := range []bool{true, false} {
		for _, l := range item.Labels {
			if model.PriorityLabel(l.Name, rules) == priority {
				out = append(out, l)
			}
		}
	}
	return out
}

// Only a native six-digit RGB color reaches the renderer. Pick the more
// contrasting black/white text; no arbitrary source-provided ANSI/CSS is used.
func labelColors(value string) (string, string, bool) {
	hex := strings.TrimPrefix(value, "#")
	if len(hex) != 6 {
		return "", "", false
	}
	for _, r := range hex {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return "", "", false
		}
	}
	rgb, e := strconv.ParseUint(hex, 16, 24)
	if e != nil {
		return "", "", false
	}
	linear := func(v uint64) float64 {
		x := float64(v) / 255
		if x <= 0.04045 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	luminance := 0.2126*linear(rgb>>16) + 0.7152*linear((rgb>>8)&255) + 0.0722*linear(rgb&255)
	fg := "#ffffff"
	if luminance > 0.179 {
		fg = "#000000"
	}
	return "#" + strings.ToLower(hex), fg, true
}
func (m *app) labelChip(label model.Label, priority bool) string {
	text := "[" + oneLine(label.Name) + "]"
	if !m.color {
		return text
	}
	style := lipgloss.NewStyle().Bold(priority)
	if bg, fg, ok := labelColors(label.Color); ok {
		style = style.Background(lipgloss.Color(bg)).Foreground(lipgloss.Color(fg))
	} else if priority {
		style = style.Foreground(lipgloss.LightDark(m.dark)(lipgloss.Color("#895b14"), lipgloss.Color("#dfb76d")))
	} else {
		style = style.Foreground(lipgloss.LightDark(m.dark)(lipgloss.Color("#52616e"), lipgloss.Color("#a6b5c3")))
	}
	return style.Render(text)
}
func (m *app) compactTags(item model.Item, width int) string {
	if width < 4 || len(item.Labels) == 0 {
		return ""
	}
	labels := m.orderedLabels(item)
	rules := m.priorityLabelsFor(item.SourceID)
	var parts []string
	used := 0
	for n, l := range labels {
		if n >= 2 {
			break
		}
		chip := m.labelChip(l, model.PriorityLabel(l.Name, rules))
		suffix := ""
		if len(labels) > n+1 {
			suffix = fmt.Sprintf(" +%d", len(labels)-n-1)
		}
		extra := 0
		if len(parts) > 0 {
			extra = 1
		}
		if used+extra+ansi.StringWidth(chip)+len(suffix) > width {
			break
		}
		parts = append(parts, chip)
		used += extra + ansi.StringWidth(chip)
	}
	if len(parts) == 0 {
		suffix := ""
		if len(labels) > 1 {
			suffix = fmt.Sprintf(" +%d", len(labels)-1)
		}
		if len(suffix) >= width {
			return fit(fmt.Sprintf("+%d", len(labels)), width)
		}
		return fit(m.labelChip(labels[0], model.PriorityLabel(labels[0].Name, rules)), width-len(suffix)) + suffix
	}
	text := strings.Join(parts, " ")
	if len(parts) < len(labels) {
		text += fmt.Sprintf(" +%d", len(labels)-len(parts))
	}
	return text
}
func (m *app) titleWithTags(item model.Item, title string, width int) string {
	if len(item.Labels) == 0 || width < 14 {
		return fit(title, width)
	}
	if width < 24 {
		hints := m.priorityHints(item)
		if len(hints) == 0 {
			return fit(title, width)
		}
		chip := fit(m.labelChip(hints[0], true), min(10, width/3))
		return pad(title, width-ansi.StringWidth(chip)-1) + " " + chip
	}
	budget := min(28, max(8, width/3))
	tags := m.compactTags(item, budget)
	if tags == "" {
		return fit(title, width)
	}
	return pad(title, max(1, width-ansi.StringWidth(tags)-1)) + " " + tags
}
func (m *app) openTags() {
	item, ok := m.selected()
	if !ok {
		return
	}
	m.tagLabels = nil
	if item.Labels != nil {
		m.tagLabels = append([]model.Label{}, item.Labels...)
	}
	m.tagTitle = item.DisplayRef
	m.tagSourceID = item.SourceID
	m.tagCursor = 0
	m.overlayScroll = 0
	m.gPending = false
	m.overlay = tagsOverlay
}
func (m *app) chooseTag() tea.Cmd {
	if m.tagCursor < 0 || m.tagCursor >= len(m.tagLabels) {
		return nil
	}
	token, ok := model.LabelQuery(m.tagLabels[m.tagCursor].Name)
	if !ok {
		m.notice = "This label contains control text and cannot be inserted into the search field."
		return nil
	}
	filter := m.current().filter
	if !strings.Contains(filter, token) {
		filter = strings.TrimSpace(filter + " " + token)
	}
	m.current().filter = filter
	m.input.SetValue(filter)
	m.overlay = noOverlay
	m.notice = "Exact label filter added; source unchanged."
	return m.query(true)
}
func (m *app) tagLines(width int) []string {
	lines, _ := m.tagRows(width)
	return lines
}
func (m *app) tagRows(width int) ([]string, []rowHit) {
	lines := wrapped("Labels on "+m.tagTitle+" · Enter adds an exact label: filter", width)
	if m.tagLabels == nil {
		return append(lines, "Labels are unknown or unsupported by this source."), nil
	}
	if len(m.tagLabels) == 0 {
		return append(lines, "No labels on this item."), nil
	}
	hits := padHits(nil, len(lines))
	capacity := max(1, m.contentHeight()-len(lines)-3)
	start := min(max(0, m.tagCursor-capacity+1), max(0, len(m.tagLabels)-capacity))
	for n := start; n < len(m.tagLabels) && n < start+capacity; n++ {
		l := m.tagLabels[n]
		prefix := "  "
		if n == m.tagCursor {
			prefix = "> "
		}
		priority := model.PriorityLabel(l.Name, m.priorityLabelsFor(m.tagSourceID))
		note := ""
		if priority {
			note = "  priority label"
		}
		lines = append(lines, fit(prefix+m.labelChip(l, priority)+note, width))
		hits = append(hits, rowHit{cursor: n})
	}
	lines = append(lines, "", "Priority labels are display hints, not cross-source ranks.")
	return lines, padHits(hits, len(lines))
}
