package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"lazytodo/internal/config"
	"lazytodo/internal/repoctx"
	"lazytodo/internal/service"
)

type repoDetectedMsg struct {
	result     repoctx.Result
	err        error
	generation uint64
}
type repoRegistration struct {
	backend  backend
	config   config.Config
	sourceID string
	saved    config.SourceSave
}
type repoAddedMsg struct {
	registration repoRegistration
	err          error
}
type repoRegistrationReceipt struct {
	started chan struct{}
	done    chan repoAddedMsg
}

func (m *app) configureRepoContext(svc *service.Service) {
	m.repoConfigPath = svc.Paths.Config
	if !svc.Config.RepoContext {
		return
	}
	configured := append([]config.Source(nil), svc.Config.Sources...)
	m.repoLookup = func(ctx context.Context) (repoctx.Result, error) {
		cwd, e := os.Getwd()
		if e != nil {
			return repoctx.Result{Preferred: -1}, fmt.Errorf("cannot determine current directory")
		}
		return repoctx.Detect(ctx, cwd, configured)
	}
	m.repoRegister = func(ctx context.Context, b backend, target repoctx.Target) (repoRegistration, error) {
		current, ok := b.(*service.Service)
		if !ok {
			return repoRegistration{}, fmt.Errorf("source registration is unavailable")
		}
		next, saved, id, e := current.RegisterSource(ctx, config.Source{Kind: target.Kind, Host: target.Host, Scope: target.Scope})
		result := repoRegistration{saved: saved, sourceID: id}
		if next != nil {
			result.backend = next
			result.config = next.Config
		}
		return result, e
	}
}
func (m *app) detectRepo() tea.Cmd {
	if m.repoLookup == nil {
		return nil
	}
	m.repoGeneration++
	lookup, ctx, generation := m.repoLookup, m.ctx, m.repoGeneration
	return func() tea.Msg { r, e := lookup(ctx); return repoDetectedMsg{result: r, err: e, generation: generation} }
}
func (m *app) finishRepoRegistration() {
	if m.repoReceipt == nil || m.repoRegistrationHandled {
		return
	}
	select {
	case <-m.repoReceipt.started:
		result := <-m.repoReceipt.done
		if result.registration.saved.Changed {
			fmt.Fprintf(os.Stderr, "Source registration was saved to %q before exit. Run fetch to verify/update it.\n", m.repoConfigPath)
		}
	default:
	}
}
func (m *app) matchedRepo(target repoctx.Target) (config.Source, bool) {
	key := config.SourceKey(config.Source{Kind: target.Kind, Host: target.Host, Scope: target.Scope})
	for _, s := range m.config.Sources {
		if config.SourceKey(s) == key {
			return s, true
		}
	}
	return config.Source{}, false
}
func (m *app) acceptRepoDetection(msg repoDetectedMsg) tea.Cmd {
	m.repoReady = true
	m.repoResult = msg.result
	if msg.err != nil {
		m.repoNote = "Current-repo context unavailable; global browsing remains available."
		return nil
	}
	m.repoNote = msg.result.Reason
	if len(msg.result.Targets) == 0 {
		return nil
	}
	if msg.result.Preferred >= 0 && msg.result.Preferred < len(msg.result.Targets) {
		m.repoCursor = msg.result.Preferred
		if existing, ok := m.matchedRepo(msg.result.Targets[m.repoCursor]); ok {
			if existing.Disabled {
				m.repoNote = "Current repo is configured but disabled; enable it with config edit."
				if !m.userTouched {
					m.notice = m.repoNote
				}
				return nil
			}
			m.repoSourceID = existing.ID
			if !m.userTouched && m.overlay == noOverlay {
				return m.changeScope(existing.ID, true)
			}
			m.repoNote = "Current repo available: c focuses it."
			return nil
		}
	}
	if !m.userTouched && m.overlay == noOverlay && !m.repoIgnored {
		return m.openRepoContext()
	}
	m.repoNote = "Current repo is not selected/configured; press c to review."
	m.notice = m.repoNote
	return nil
}
func (m *app) openRepoContext() tea.Cmd {
	if !m.repoReady || len(m.repoResult.Targets) == 0 {
		m.notice = m.repoNote
		return nil
	}
	m.repoIgnored = false
	m.repoButton = 0
	if m.repoResult.Preferred >= 0 && m.repoResult.Preferred < len(m.repoResult.Targets) {
		m.repoCursor = m.repoResult.Preferred
		if existing, ok := m.matchedRepo(m.repoResult.Targets[m.repoCursor]); ok {
			if existing.Disabled {
				m.notice = "Current repo is disabled. Use config edit to enable it explicitly."
				return nil
			}
			m.repoSourceID = existing.ID
			return m.changeScope(existing.ID, false)
		}
		m.repoConfirm = true
	} else {
		m.repoConfirm = false
		m.repoCursor = 0
	}
	m.overlay = repoOverlay
	m.notice = ""
	return nil
}
func (m *app) ignoreRepo() tea.Cmd {
	m.repoIgnored = true
	m.overlay = noOverlay
	m.notice = "Current repo ignored for this session; c revisits it."
	return nil
}
func (m *app) repoAction(action string) tea.Cmd {
	if m.repoPending {
		return nil
	}
	switch action {
	case "ignore":
		return m.ignoreRepo()
	case "previous", "next":
		if m.repoConfirm {
			m.repoButton = 1 - m.repoButton
		} else {
			delta := 1
			if action == "previous" {
				delta = -1
			}
			m.repoCursor = min(max(0, m.repoCursor+delta), len(m.repoResult.Targets)-1)
		}
	case "choose":
		if !m.repoConfirm {
			target := m.repoResult.Targets[m.repoCursor]
			if existing, ok := m.matchedRepo(target); ok {
				if existing.Disabled {
					m.notice = "This source is disabled; use config edit to enable it."
					return nil
				}
				m.repoResult.Preferred = m.repoCursor
				m.repoSourceID = existing.ID
				return m.changeScope(existing.ID, false)
			}
			m.repoConfirm = true
			m.repoButton = 0
			return nil
		}
		if m.repoButton == 0 {
			return m.ignoreRepo()
		}
		return m.registerRepo()
	}
	return nil
}
func (m *app) registerRepo() tea.Cmd {
	if m.configError != "" || m.editorRequested || m.editingConfig || m.repoPending || m.repoRegister == nil || m.repoCursor < 0 || m.repoCursor >= len(m.repoResult.Targets) {
		return nil
	}
	// Do not allow a hidden action on a tiny terminal that cannot show its target.
	if m.width < 45 || m.height < 14 || len(m.repoLines(max(1, m.width-2))) > m.contentHeight() {
		m.notice = "Resize to review the repository and config destination before adding."
		return nil
	}
	m.repoPending = true
	m.repoRegistrationHandled = false
	m.notice = "Saving source registration; cancellation may leave a saved registration."
	receipt := &repoRegistrationReceipt{make(chan struct{}), make(chan repoAddedMsg, 1)}
	m.repoReceipt = receipt
	apply, b, target, ctx := m.repoRegister, m.backend, m.repoResult.Targets[m.repoCursor], m.ctx
	return func() tea.Msg {
		close(receipt.started)
		r, e := apply(ctx, b, target)
		msg := repoAddedMsg{r, e}
		receipt.done <- msg
		return msg
	}
}
func (m *app) acceptRepoRegistration(msg repoAddedMsg) tea.Cmd {
	m.repoPending = false
	m.repoRegistrationHandled = true
	if msg.err != nil {
		m.notice = "Source registration failed: " + msg.err.Error()
		return nil
	}
	if msg.registration.backend == nil {
		m.notice = "Source saved; restart and fetch to load its configuration."
		return nil
	}
	m.backend = msg.registration.backend
	m.config = msg.registration.config
	m.repoSourceID = msg.registration.sourceID
	wasIdle := m.tickInterval == 0
	m.tickInterval = 0
	for _, s := range m.config.Active() {
		if d := s.Interval(m.config.Interval()); d > 0 && (m.tickInterval == 0 || d < m.tickInterval) {
			m.tickInterval = d
		}
	}
	if m.tickInterval > 30*time.Second {
		m.tickInterval = 30 * time.Second
	}
	var tick tea.Cmd
	if wasIdle {
		tick = m.nextTick()
	}
	m.repoResult.Preferred = m.repoCursor
	m.overlay = noOverlay
	m.notice = "Source registered locally; fetching this repository…"
	query := m.changeScope(m.repoSourceID, true)
	if m.fetching {
		m.pendingFetchID = m.repoSourceID
		return tea.Batch(query, m.readSources(), tick)
	}
	return tea.Batch(query, m.readSources(), m.fetchSource(m.repoSourceID, false), tick)
}

func (m *app) changeScope(id string, preferOpen bool) tea.Cmd {
	if id != "" {
		found := false
		for _, s := range m.config.Active() {
			if s.ID == id {
				found = true
			}
		}
		if !found {
			m.notice = "Source is no longer active."
			return nil
		}
	}
	if id != m.scopeSource {
		m.scopedViews[m.scopeSource] = m.views
		if saved, ok := m.scopedViews[id]; ok {
			m.views = saved
		} else {
			next := make([]viewState, len(m.views))
			for n, v := range m.views {
				next[n] = viewState{name: v.name, base: v.base}
			}
			m.views = next
		}
		m.scopeSource = id
	}
	if preferOpen {
		for n, v := range m.views {
			if v.name == "Open" {
				m.active = n
				break
			}
		}
	}
	m.overlay = noOverlay
	m.searching = false
	m.input.Blur()
	m.input.SetValue(m.current().filter)
	m.focus = listPane
	return m.query(false)
}
func (m *app) openSourcePicker() {
	m.overlay = sourcesOverlay
	m.overlayScroll = 0
	m.sourceCursor = 0
	m.gPending = false
	for n, s := range m.config.Sources {
		if s.ID == m.scopeSource {
			m.sourceCursor = n + 1
		}
	}
}
func (m *app) chooseSource() tea.Cmd {
	if m.sourceCursor == 0 {
		return m.changeScope("", false)
	}
	sources := m.config.Sources
	index := m.sourceCursor - 1
	if index < 0 || index >= len(sources) {
		return nil
	}
	if sources[index].Disabled {
		m.notice = "This source is disabled. Press e to edit its configuration."
		return nil
	}
	return m.changeScope(sources[index].ID, false)
}
func (m *app) scopeName() string {
	if m.scopeSource == "" {
		return "All sources"
	}
	for _, s := range m.config.Sources {
		if s.ID == m.scopeSource {
			return s.Scope
		}
	}
	return m.scopeSource
}
func (m *app) repoLines(width int) []string {
	var lines []string
	if !m.repoConfirm {
		lines = append(lines, "Several Git remotes are available. Choose a repository:", "")
		capacity := max(1, m.contentHeight()-5)
		start := min(max(0, m.repoCursor-capacity+1), max(0, len(m.repoResult.Targets)-capacity))
		for n := start; n < len(m.repoResult.Targets) && n < start+capacity; n++ {
			t := m.repoResult.Targets[n]
			prefix := "  "
			if n == m.repoCursor {
				prefix = "> "
			}
			lines = append(lines, fit(prefix+t.Kind+" · "+t.Host+"/"+t.Scope, width))
		}
		lines = append(lines, "", "Enter reviews/selects; Esc ignores for this session.")
		return lines
	}
	if m.repoCursor < 0 || m.repoCursor >= len(m.repoResult.Targets) {
		return nil
	}
	target := m.repoResult.Targets[m.repoCursor]
	lines = append(lines, wrapped("Current repository: "+target.Scope, width)...)
	lines = append(lines, wrapped("Provider: "+target.Kind+" · "+target.Host+" · remote "+target.Remote, width)...)
	lines = append(lines, wrapped("Config: "+m.repoConfigPath, width)...)
	lines = append(lines, "", "Add a local source registration, then fetch its issues.", "Native source content will not be modified.", "")
	if m.repoPending {
		lines = append(lines, "Saving registration…")
	} else {
		buttons := []string{" Ignore this time ", " Add + fetch "}
		buttons[m.repoButton] = "[" + strings.TrimSpace(buttons[m.repoButton]) + "]"
		lines = append(lines, strings.Join(buttons, "    "), "Tab/←→ choose · Enter confirm · Esc ignore")
	}
	return lines
}
