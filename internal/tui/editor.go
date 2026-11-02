package tui

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"

	tea "charm.land/bubbletea/v2"
	"github.com/muesli/cancelreader"
	"lazytodo/internal/config"
	"lazytodo/internal/model"
	"lazytodo/internal/repoctx"
	"lazytodo/internal/service"
)

type configEditorDoneMsg struct {
	generation uint64
	err        error
}
type configReloadedMsg struct {
	generation           uint64
	backend              backend
	config               config.Config
	editorErr, reloadErr error
}

// Bubble Tea releases its terminal before Run and reacquires it afterward.
// This shares the standalone editor/validation operation, including repair of
// invalid files, and acknowledges its result while the child owns the terminal.
type configEditCommand struct {
	ctx          context.Context
	path, editor string
	in           io.Reader
	out, errOut  io.Writer
}

func (c *configEditCommand) SetStdin(r io.Reader)  { c.in = r }
func (c *configEditCommand) SetStdout(w io.Writer) { c.out = w }
func (c *configEditCommand) SetStderr(w io.Writer) { c.errOut = w }
func (c *configEditCommand) Run() error {
	err := config.Edit(c.ctx, c.path, c.editor, c.in, c.out, c.errOut)
	if c.ctx.Err() != nil {
		return c.ctx.Err()
	}
	if err != nil {
		fmt.Fprintln(c.errOut, model.Plain(err.Error()))
	}
	fmt.Fprint(c.out, "\nPress Enter to return to lazytodo…")
	reader, e := cancelreader.NewReader(c.in)
	if e != nil {
		return errors.Join(err, e)
	}
	defer reader.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-c.ctx.Done():
			reader.Cancel()
		case <-done:
		}
	}()
	_, e = bufio.NewReader(reader).ReadString('\n')
	if c.ctx.Err() != nil {
		return c.ctx.Err()
	}
	if e != nil && !errors.Is(e, io.EOF) {
		return errors.Join(err, e)
	}
	return err
}

func (m *app) configureSourceEditor(svc *service.Service) {
	path := svc.Paths.Config
	m.editorFactory = func() tea.ExecCommand { return &configEditCommand{ctx: m.ctx, path: path, editor: config.Editor()} }
	m.reloadConfig = func(b backend) (backend, config.Config, error) {
		current, ok := b.(*service.Service)
		if !ok {
			return nil, config.Config{}, errors.New("configuration reload is unavailable")
		}
		next, e := current.ReloadConfig()
		if e != nil {
			return nil, config.Config{}, e
		}
		return next, next.Config, nil
	}
}
func (m *app) beginConfigEdit() tea.Cmd {
	if m.editorFactory == nil || m.reloadConfig == nil || m.repoPending || m.editorRequested || m.editingConfig {
		return nil
	}
	m.editorRequested = true
	m.editorGeneration++
	if m.queryCancel != nil {
		m.queryCancel()
	}
	m.queryGeneration++
	m.sourceGeneration++
	m.detailGeneration++
	m.repoGeneration++
	m.pendingFetchID = ""
	if m.fetching {
		if m.fetchCancel != nil {
			m.fetchCancel()
		}
		m.notice = "Stopping refresh before opening the editor… Esc cancels the editor request."
		return nil
	}
	return m.launchConfigEditor()
}
func (m *app) launchConfigEditor() tea.Cmd {
	if !m.editorRequested || m.editorFactory == nil {
		return nil
	}
	m.editorRequested = false
	m.editingConfig = true
	generation := m.editorGeneration
	return tea.Exec(m.editorFactory(), func(err error) tea.Msg { return configEditorDoneMsg{generation, err} })
}
func (m *app) afterEditor(msg configEditorDoneMsg) tea.Cmd {
	if msg.generation != m.editorGeneration || !m.editingConfig {
		return nil
	}
	m.notice = "Reloading configuration…"
	reload, b, generation := m.reloadConfig, m.backend, msg.generation
	return func() tea.Msg {
		next, c, e := reload(b)
		return configReloadedMsg{generation: generation, backend: next, config: c, editorErr: msg.err, reloadErr: e}
	}
}
func configTargets(c config.Config) map[string]string {
	out := map[string]string{}
	for _, s := range c.Active() {
		out[s.ID] = s.Fingerprint()
	}
	return out
}
func equalTargets(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for id, fp := range a {
		if b[id] != fp {
			return false
		}
	}
	return true
}
func mergeViewStates(old, definitions []viewState, keepData bool) []viewState {
	result := make([]viewState, len(definitions))
	for n, definition := range definitions {
		result[n] = definition
		for _, prior := range old {
			if prior.name != definition.name || prior.base != definition.base {
				continue
			}
			result[n] = prior
			result[n].loading = false
			result[n].err = ""
			if !keepData {
				result[n].items = nil
				result[n].total = 0
				result[n].offset = 0
				result[n].selected = 0
				result[n].scroll = 0
				result[n].preview = 0
				result[n].revision = ""
			}
			break
		}
	}
	return result
}
func (m *app) applyReload(msg configReloadedMsg) tea.Cmd {
	if msg.generation != m.editorGeneration || !m.editingConfig {
		return nil
	}
	m.editingConfig = false
	m.editorRequested = false
	if msg.reloadErr != nil || msg.backend == nil {
		reason := "reload returned no service"
		if msg.reloadErr != nil {
			reason = model.Plain(msg.reloadErr.Error())
		}
		m.configError = reason
		m.notice = "Configuration not applied; cached view retained, refresh paused. Press e in Sources to repair."
		m.overlay = sourcesOverlay
		return tea.Batch(m.query(false), m.readSources())
	}
	name, scope := m.current().name, m.scopeSource
	selectedSource := ""
	if m.sourceCursor > 0 && m.sourceCursor <= len(m.config.Sources) {
		selectedSource = m.config.Sources[m.sourceCursor-1].ID
	}
	oldTargets, newTargets := configTargets(m.config), configTargets(msg.config)
	allCompatible := equalTargets(oldTargets, newTargets)
	m.scopedViews[scope] = m.views
	fresh := newModel(m.ctx, msg.backend, msg.config)
	defer fresh.cancel()
	nextScopes := map[string][]viewState{}
	for id, views := range m.scopedViews {
		if id != "" {
			if _, exists := newTargets[id]; !exists {
				continue
			}
		}
		compatible := allCompatible
		if id != "" {
			compatible = oldTargets[id] == newTargets[id]
		}
		nextScopes[id] = mergeViewStates(views, fresh.views, compatible)
	}
	if scope != "" {
		if _, exists := newTargets[scope]; !exists {
			scope = ""
		}
	}
	if _, exists := nextScopes[scope]; !exists {
		nextScopes[scope] = mergeViewStates(nil, fresh.views, false)
	}
	m.scopeSource = scope
	m.scopedViews = nextScopes
	m.views = nextScopes[scope]
	m.active = fresh.active
	for n, v := range m.views {
		if v.name == name {
			m.active = n
			break
		}
	}
	wasIdle := m.tickInterval == 0
	m.backend = msg.backend
	m.config = msg.config
	m.tickInterval = fresh.tickInterval
	m.color = fresh.color
	m.styleInput()
	m.sources = fresh.sources
	m.sourceError = ""
	m.configError = ""
	m.lastFetchError = ""
	m.sourceCursor = 0
	for n, s := range m.config.Sources {
		if s.ID == selectedSource {
			m.sourceCursor = n + 1
		}
	}
	m.overlay = sourcesOverlay
	m.overlayScroll = 0
	m.searching = false
	m.input.Blur()
	m.input.SetValue(m.current().filter)
	m.repoLookup = nil
	m.repoRegister = nil
	m.repoReady = false
	m.repoSourceID = ""
	m.repoResult = repoctx.Result{Preferred: -1}
	m.repoGeneration++
	if svc, ok := msg.backend.(*service.Service); ok {
		m.configureRepoContext(svc)
		m.configureSourceEditor(svc)
	}
	m.notice = "Configuration reloaded. Cached results refreshed; r fetches the focused scope."
	if msg.editorErr != nil {
		m.notice = "Editor reported an error; valid saved configuration was reloaded."
	}
	var tick tea.Cmd
	if wasIdle {
		tick = m.nextTick()
	}
	return tea.Batch(m.query(false), m.readSources(), m.detectRepo(), tick)
}
