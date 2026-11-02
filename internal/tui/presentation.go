package tui

import (
	"fmt"
	"path"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"lazytodo/internal/config"
	"lazytodo/internal/model"
)

func (m *app) repoLabel(item model.Item) string {
	scope := item.Scope
	if scope == "" {
		for _, s := range m.config.Sources {
			if s.ID == item.SourceID {
				scope = s.Scope
				break
			}
		}
	}
	if scope == "" {
		return oneLine(item.SourceID)
	}
	label := path.Base(scope)
	for _, s := range m.config.Sources {
		if s.Disabled || s.ID == item.SourceID || !strings.EqualFold(path.Base(s.Scope), label) {
			continue
		}
		if !strings.EqualFold(s.Scope, scope) {
			return oneLine(scope)
		}
		if s.Host != "" && !strings.EqualFold(s.Host, item.Origin.Authority) {
			return oneLine(item.Origin.Authority + "/" + scope)
		}
	}
	return oneLine(label)
}
func nativeNumber(item model.Item) string {
	if at := strings.LastIndex(item.DisplayRef, "#"); at >= 0 {
		return oneLine(item.DisplayRef[at:])
	}
	return ""
}
func (m *app) stateLabel(state string) string {
	text := pad(oneLine(state), 7)
	if !m.color {
		return text
	}
	ld := lipgloss.LightDark(m.dark)
	color := ld(lipgloss.Color("#277969"), lipgloss.Color("#65b9a4"))
	if state == "closed" {
		color = ld(lipgloss.Color("#75808a"), lipgloss.Color("#919daa"))
	}
	if state == "unknown" {
		color = ld(lipgloss.Color("#8b631e"), lipgloss.Color("#d7ad64"))
	}
	return lipgloss.NewStyle().Foreground(color).Render(text)
}
func relativeTime(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	age := time.Since(t)
	if age < -time.Minute {
		return "ahead of local clock"
	}
	switch {
	case age < time.Minute:
		return "just now"
	case age < time.Hour:
		return fmt.Sprintf("%dm ago", int(age.Minutes()))
	case age < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(age.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(age.Hours()/24))
	}
}
func (m *app) sourceStatus(id string) model.SourceStatus {
	for _, s := range m.sources {
		if s.ID == id {
			return s
		}
	}
	return model.SourceStatus{ID: id, State: "unknown"}
}
func sourceScope(s config.Source, status model.SourceStatus) string {
	if s.Scope != "" {
		return s.Scope
	}
	if status.Scope != "" {
		return status.Scope
	}
	return s.ID
}
func (m *app) sourcePickerLines(width int) []string {
	active := m.config.Sources
	lines := []string{"Enter focuses · e edits sources (including disabled) · Esc returns", ""}
	capacity := max(1, m.contentHeight()-10)
	start := min(max(0, m.sourceCursor-capacity+1), max(0, len(active)+1-capacity))
	for index := start; index < len(active)+1 && index < start+capacity; index++ {
		prefix := "  "
		if index == m.sourceCursor {
			prefix = "> "
		}
		if index == 0 {
			lines = append(lines, prefix+"All sources")
			continue
		}
		s := active[index-1]
		st := m.sourceStatus(s.ID)
		if s.Disabled {
			st.State = "disabled"
		}
		line := fmt.Sprintf("%s%s  ·  %s  ·  %d cached", prefix, sourceScope(s, st), st.State, st.Count)
		if s.ID == m.repoSourceID {
			line += "  (cwd)"
		}
		lines = append(lines, fit(prefix+oneLine(strings.TrimPrefix(line, prefix)), width))
	}
	lines = append(lines, "")
	if m.sourceCursor > 0 && m.sourceCursor <= len(active) {
		s := active[m.sourceCursor-1]
		st := m.sourceStatus(s.ID)
		if s.Disabled {
			st.State = "disabled"
		}
		lines = append(lines, wrapped(s.Kind+" · "+s.Host+"/"+sourceScope(s, st), width)...)
		if st.ObservedAt != nil {
			lines = append(lines, "Observed: "+st.ObservedAt.Format(time.RFC3339))
		} else {
			lines = append(lines, "Observed: unknown; no complete snapshot yet")
		}
		if st.Error != "" {
			lines = append(lines, wrapped("Error: "+st.Error, width)...)
		}
		if s.Scope == "owner/repository" || s.Scope == "group/project" {
			lines = append(lines, wrapped("Looks like an example scope. Replace it or set disabled = true with config edit.", width)...)
		}
		if st.RetryAfter != nil {
			lines = append(lines, "Retry after: "+st.RetryAfter.Format(time.RFC3339))
		}
	} else {
		lines = append(lines, m.sourceSummary())
		if m.lastFetchError != "" {
			lines = append(lines, wrapped("Last refresh: "+m.lastFetchError, width)...)
		}
	}
	if m.configError != "" {
		lines = append(lines, wrapped("Config not applied: "+m.configError+". Press e to repair; refresh is paused.", width)...)
	}
	if m.sourceError != "" {
		lines = append(lines, wrapped("Status: "+m.sourceError, width)...)
	}
	if m.repoReady && m.repoResult.Root != "" && len(m.repoResult.Targets) == 0 {
		lines = append(lines, wrapped("Cwd: "+m.repoNote, width)...)
	}
	return lines
}
