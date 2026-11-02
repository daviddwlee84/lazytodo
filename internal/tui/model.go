// Package tui provides the keyboard-first, read-only inbox. Only pins are local writes.
package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"lazytodo/internal/config"
	"lazytodo/internal/model"
	"lazytodo/internal/repoctx"
	"lazytodo/internal/service"
)

const pageSize = 100

// backend keeps terminal state tests independent of disk, providers and native openers.
type backend interface {
	Query(context.Context, service.QueryRequest) (service.QueryResult, error)
	Sources(context.Context) ([]model.SourceStatus, error)
	Fetch(context.Context, string, bool) (service.FetchResult, error)
	Pin(context.Context, string, bool) error
	Open(context.Context, string) error
	Show(context.Context, string) (model.Item, error)
}

// Run owns and cancels all in-flight operations, including on ordinary quit.
func Run(ctx context.Context, svc *service.Service) error {
	if svc == nil {
		return errors.New("TUI requires a service")
	}
	m := newModel(ctx, svc, svc.Config)
	m.configureRepoContext(svc)
	m.configureSourceEditor(svc)
	defer m.cancel()
	_, err := tea.NewProgram(m, tea.WithContext(ctx)).Run()
	m.cancel()
	m.finishRepoRegistration()
	return err
}

type pane uint8

const (
	listPane pane = iota
	previewPane
)

type overlay uint8

const (
	noOverlay overlay = iota
	helpOverlay
	viewsOverlay
	sourcesOverlay
	repoOverlay
	tagsOverlay
)

type viewState struct {
	name, base, filter        string
	items                     []model.Item
	total, offset             int
	revision                  string
	selected, scroll, preview int
	selectedRef               string
	loading                   bool
	err                       string
}

func (v *viewState) query() string { return strings.TrimSpace(v.base + " " + v.filter) }

type app struct {
	ctx                                    context.Context
	cancel                                 context.CancelFunc
	backend                                backend
	config                                 config.Config
	views                                  []viewState
	active                                 int
	focus                                  pane
	overlay                                overlay
	overlayCursor, overlayScroll           int
	input                                  textinput.Model
	searching                              bool
	width, height                          int
	dark, color                            bool
	started                                bool
	gPending                               bool
	gGeneration                            uint64
	queryGeneration                        uint64
	queryCancel                            context.CancelFunc
	sourceGeneration                       uint64
	fetchGeneration                        uint64
	fetching                               bool
	detailGeneration                       uint64
	detailPending                          bool
	operationGeneration                    uint64
	pins                                   map[string]uint64
	opens                                  map[string]uint64
	sources                                []model.SourceStatus
	sourceError, notice, lastFetchError    string
	tickInterval                           time.Duration
	scopeSource                            string
	scopedViews                            map[string][]viewState
	sourceCursor                           int
	userTouched                            bool
	repoLookup                             func(context.Context) (repoctx.Result, error)
	repoRegister                           func(context.Context, backend, repoctx.Target) (repoRegistration, error)
	repoResult                             repoctx.Result
	repoReady, repoIgnored, repoPending    bool
	repoCursor, repoButton                 int
	repoConfirm                            bool
	repoSourceID, repoNote, repoConfigPath string
	repoReceipt                            *repoRegistrationReceipt
	repoRegistrationHandled                bool
	pendingFetchID                         string
	fetchSourceID                          string
	fetchCancel                            context.CancelFunc
	repoGeneration                         uint64
	editorFactory                          func() tea.ExecCommand
	reloadConfig                           func(backend) (backend, config.Config, error)
	editorRequested, editingConfig         bool
	editorGeneration                       uint64
	configError                            string
	tagLabels                              []model.Label
	tagCursor                              int
	tagTitle, tagSourceID                  string
}

func newModel(ctx context.Context, b backend, c config.Config) *app {
	owned, cancel := context.WithCancel(ctx)
	input := textinput.New()
	input.Prompt = "/ "
	input.Placeholder = "Search titles, body, labels; state:open source:work"
	input.CharLimit = 1024
	input.SetVirtualCursor(true)
	input.SetWidth(70)
	_, noColor := os.LookupEnv("NO_COLOR")
	m := &app{ctx: owned, cancel: cancel, backend: b, config: c,
		views: []viewState{{name: "All"}, {name: "Pinned", base: "pinned:true"}},
		input: input, width: 80, height: 24, dark: true,
		color: c.Color != "never" && (c.Color == "always" || !noColor),
		pins:  make(map[string]uint64), opens: make(map[string]uint64),
	}
	for _, v := range c.Views {
		m.views = append(m.views, viewState{name: v.Name, base: v.Query})
	}
	open := -1
	for n, v := range m.views {
		if v.name == "Open" {
			open = n
		}
	}
	if open < 0 {
		open = len(m.views)
		m.views = append(m.views, viewState{name: "Open", base: "state:open"})
	}
	m.active = open
	m.scopedViews = map[string][]viewState{}
	for _, s := range c.Sources {
		state := "unknown"
		if s.Disabled {
			state = "disabled"
		}
		m.sources = append(m.sources, model.SourceStatus{ID: s.ID, Kind: s.Kind, Scope: s.Scope, State: state})
		if d := s.Interval(c.Interval()); !s.Disabled && d > 0 && (m.tickInterval == 0 || d < m.tickInterval) {
			m.tickInterval = d
		}
	}
	if m.tickInterval > 30*time.Second {
		m.tickInterval = 30 * time.Second
	}
	if m.tickInterval > 0 && m.tickInterval < time.Second {
		m.tickInterval = time.Second
	}
	m.styleInput()
	return m
}

// Init schedules a message, not cache/provider work: the initial shell renders first.
func (m *app) Init() tea.Cmd { return func() tea.Msg { return startMsg{} } }

type startMsg struct{}
type tickMsg struct{}
type gTimeoutMsg uint64
type queryMsg struct {
	generation uint64
	view       int
	request    service.QueryRequest
	result     service.QueryResult
	err        error
}
type sourcesMsg struct {
	generation uint64
	sources    []model.SourceStatus
	err        error
}
type fetchMsg struct {
	generation uint64
	result     service.FetchResult
	err        error
}
type pinMsg struct {
	generation uint64
	ref        string
	add        bool
	err        error
}
type openMsg struct {
	generation uint64
	ref        string
	err        error
}
type detailMsg struct {
	generation uint64
	ref        string
	item       model.Item
	err        error
}

func (m *app) current() *viewState { return &m.views[m.active] }
func (m *app) selected() (model.Item, bool) {
	v := m.current()
	if len(v.items) == 0 || v.selected < 0 || v.selected >= len(v.items) {
		return model.Item{}, false
	}
	return v.items[v.selected], true
}
func (m *app) nextTick() tea.Cmd {
	if m.tickInterval == 0 {
		return nil
	}
	return tea.Tick(m.tickInterval, func(time.Time) tea.Msg { return tickMsg{} })
}
func (m *app) query(reset bool) tea.Cmd {
	if m.queryCancel != nil {
		m.queryCancel()
	}
	m.queryGeneration++
	m.detailGeneration++
	m.detailPending = false
	v := m.current()
	if reset {
		v.offset, v.selected, v.scroll, v.preview = 0, 0, 0, 0
		v.selectedRef, v.revision = "", ""
		v.items, v.total = nil, 0
	}
	v.loading, v.err = true, ""
	text := v.query()
	if m.scopeSource != "" {
		text = strings.TrimSpace("source:" + m.scopeSource + " " + text)
	}
	req := service.QueryRequest{Text: text, Limit: pageSize, Offset: v.offset, IncludeBody: true}
	if v.offset > 0 {
		req.Revision = v.revision
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.queryCancel = cancel
	generation, active, b := m.queryGeneration, m.active, m.backend
	return func() tea.Msg {
		result, err := b.Query(ctx, req)
		return queryMsg{generation, active, req, result, err}
	}
}
func (m *app) readSources() tea.Cmd {
	m.sourceGeneration++
	generation, ctx, b := m.sourceGeneration, m.ctx, m.backend
	return func() tea.Msg { sources, err := b.Sources(ctx); return sourcesMsg{generation, sources, err} }
}
func (m *app) fetch(dueOnly bool) tea.Cmd { return m.fetchSource("", dueOnly) }
func (m *app) fetchSource(sourceID string, dueOnly bool) tea.Cmd {
	if m.editingConfig || m.editorRequested || m.configError != "" || m.fetching || len(m.config.Active()) == 0 || (dueOnly && m.tickInterval == 0) {
		return nil
	}
	m.fetching = true
	m.fetchSourceID = sourceID
	m.fetchGeneration++
	ctx, cancel := context.WithCancel(m.ctx)
	m.fetchCancel = cancel
	generation, b := m.fetchGeneration, m.backend
	return func() tea.Msg {
		defer cancel()
		result, err := b.Fetch(ctx, sourceID, dueOnly)
		return fetchMsg{generation, result, err}
	}
}
func (m *app) pin() tea.Cmd {
	item, ok := m.selected()
	if !ok || m.pins[item.Ref] != 0 {
		return nil
	}
	m.operationGeneration++
	generation, ctx, b, add := m.operationGeneration, m.ctx, m.backend, !item.Pinned
	m.pins[item.Ref] = generation
	m.notice = "Updating local pin…"
	return func() tea.Msg { return pinMsg{generation, item.Ref, add, b.Pin(ctx, item.Ref, add)} }
}
func (m *app) open() tea.Cmd {
	item, ok := m.selected()
	if !ok || item.URL == "" || m.opens[item.Ref] != 0 {
		return nil
	}
	m.operationGeneration++
	generation, ctx, b := m.operationGeneration, m.ctx, m.backend
	m.opens[item.Ref] = generation
	m.notice = "Opening native URL…"
	return func() tea.Msg { return openMsg{generation, item.Ref, b.Open(ctx, item.Ref)} }
}
func (m *app) inspect() tea.Cmd {
	m.focus = previewPane
	item, ok := m.selected()
	if !ok || m.detailPending {
		return nil
	}
	m.detailGeneration++
	m.detailPending = true
	generation, ctx, b := m.detailGeneration, m.ctx, m.backend
	return func() tea.Msg {
		itemResult, err := b.Show(ctx, item.Ref)
		return detailMsg{generation, item.Ref, itemResult, err}
	}
}

func (m *app) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case startMsg:
		if m.started {
			return m, nil
		}
		m.started = true
		return m, tea.Batch(m.query(false), m.readSources(), m.fetch(true), m.nextTick(), m.detectRepo())
	case repoDetectedMsg:
		if msg.generation != m.repoGeneration {
			return m, nil
		}
		return m, m.acceptRepoDetection(msg)
	case configEditorDoneMsg:
		return m, m.afterEditor(msg)
	case configReloadedMsg:
		return m, m.applyReload(msg)
	case repoAddedMsg:
		return m, m.acceptRepoRegistration(msg)
	case tea.WindowSizeMsg:
		m.width, m.height = max(1, msg.Width), max(1, msg.Height)
		m.input.SetWidth(max(1, m.width-4))
		m.clamp()
	case tea.BackgroundColorMsg:
		m.dark = msg.IsDark()
		m.styleInput()
	case tickMsg:
		return m, tea.Batch(m.fetch(true), m.nextTick())
	case gTimeoutMsg:
		if uint64(msg) == m.gGeneration {
			m.gPending = false
		}
	case queryMsg:
		if msg.generation != m.queryGeneration || msg.view != m.active {
			return m, nil
		}
		v := m.current()
		v.loading = false
		var serviceErr *service.Error
		changed := errors.As(msg.err, &serviceErr) && serviceErr.Code == "revision_changed"
		if msg.request.Offset > 0 && (changed || (msg.err == nil && msg.request.Revision != "" && msg.request.Revision != msg.result.Revision)) {
			m.notice = "Cache changed; returned to the first page."
			return m, m.query(true)
		}
		if msg.err != nil {
			v.err = model.Plain(msg.err.Error())
			return m, nil
		}
		oldRef, oldIndex := v.selectedRef, v.selected
		v.items, v.total, v.revision, v.offset = msg.result.Items, msg.result.Total, msg.result.Revision, msg.result.Offset
		v.err = ""
		v.selected = min(oldIndex, max(0, len(v.items)-1))
		for i := range v.items {
			if v.items[i].Ref == oldRef {
				v.selected = i
				break
			}
		}
		v.selectedRef = ""
		if item, ok := m.selected(); ok {
			v.selectedRef = item.Ref
		}
		if oldRef != v.selectedRef {
			v.preview = 0
		}
		m.clamp()
	case sourcesMsg:
		if msg.generation != m.sourceGeneration {
			return m, nil
		}
		if msg.err != nil {
			m.sourceError = model.Plain(msg.err.Error())
		} else {
			m.sources, m.sourceError = msg.sources, ""
		}
	case fetchMsg:
		if msg.generation != m.fetchGeneration {
			return m, nil
		}
		m.fetching = false
		m.fetchCancel = nil
		m.fetchSourceID = ""
		if m.editorRequested {
			return m, m.launchConfigEditor()
		}
		m.lastFetchError = ""
		if msg.err != nil {
			m.lastFetchError = model.Plain(msg.err.Error())
			m.notice = "Refresh incomplete — see s for source errors."
		} else {
			m.notice = "Refresh complete."
		}
		query, statuses := m.query(false), m.readSources()
		if m.pendingFetchID != "" {
			id := m.pendingFetchID
			m.pendingFetchID = ""
			return m, tea.Batch(query, statuses, m.fetchSource(id, false))
		}
		return m, tea.Batch(query, statuses)
	case pinMsg:
		if m.pins[msg.ref] != msg.generation {
			return m, nil
		}
		delete(m.pins, msg.ref)
		if msg.err != nil {
			m.notice = "Pin failed: " + model.Plain(msg.err.Error())
			return m, nil
		}
		for v := range m.views {
			for i := range m.views[v].items {
				if m.views[v].items[i].Ref == msg.ref {
					m.views[v].items[i].Pinned = msg.add
				}
			}
		}
		m.notice = "Local pin removed."
		if msg.add {
			m.notice = "Pinned locally; source unchanged."
		}
		return m, m.query(false)
	case openMsg:
		if m.opens[msg.ref] != msg.generation {
			return m, nil
		}
		delete(m.opens, msg.ref)
		if msg.err != nil {
			m.notice = "Open failed: " + model.Plain(msg.err.Error())
		} else {
			m.notice = "Opened native URL."
		}
	case detailMsg:
		item, ok := m.selected()
		if msg.generation != m.detailGeneration || !ok || item.Ref != msg.ref {
			return m, nil
		}
		m.detailPending = false
		if msg.err != nil {
			m.notice = "Inspect failed: " + model.Plain(msg.err.Error())
		} else {
			m.current().items[m.current().selected] = msg.item
			m.clamp()
		}
	case tea.KeyPressMsg:
		return m, m.key(msg)
	case tea.PasteMsg:
		if m.searching {
			return m, m.updateInput(tea.PasteMsg{Content: model.Plain(msg.Content)})
		}
	default:
		if m.searching {
			return m, m.updateInput(msg)
		}
	}
	return m, nil
}

func (m *app) updateInput(msg tea.Msg) tea.Cmd {
	before := m.input.Value()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if before != m.input.Value() {
		m.current().filter = m.input.Value()
		m.notice = ""
		return tea.Batch(cmd, m.query(true))
	}
	return cmd
}
func (m *app) selectIndex(index int) {
	v := m.current()
	oldRef := v.selectedRef
	v.selected = min(max(0, index), max(0, len(v.items)-1))
	v.selectedRef = ""
	if item, ok := m.selected(); ok {
		v.selectedRef = item.Ref
	}
	if oldRef != v.selectedRef {
		v.preview = 0
		m.detailGeneration++
		m.detailPending = false
	}
	m.clamp()
}
func (m *app) switchView(index int) tea.Cmd {
	if index < 0 || index >= len(m.views) {
		return nil
	}
	m.active, m.overlay, m.gPending = index, noOverlay, false
	m.input.SetValue(m.current().filter)
	return m.query(false)
}
func (m *app) page(delta int) tea.Cmd {
	v := m.current()
	if v.loading {
		return nil
	}
	offset := v.offset + delta*pageSize
	if offset < 0 || offset >= v.total {
		return nil
	}
	v.offset, v.selected, v.scroll, v.preview = offset, 0, 0, 0
	v.items, v.selectedRef = nil, ""
	return m.query(false)
}
func (m *app) sourceSummary() string {
	statuses := append([]model.SourceStatus(nil), m.sources...)
	seen := map[string]bool{}
	for _, s := range statuses {
		seen[s.ID] = true
	}
	for _, s := range m.config.Sources {
		if s.Disabled && !seen[s.ID] {
			statuses = append(statuses, model.SourceStatus{ID: s.ID, State: "disabled"})
		}
	}
	if len(statuses) == 0 {
		return "No sources configured"
	}
	counts := map[string]int{}
	for _, s := range statuses {
		state := s.State
		if state == "" {
			state = "unknown"
		}
		if s.Error != "" {
			state = "failed"
		}
		counts[state]++
	}
	parts := []string{fmt.Sprintf("%d sources", len(statuses))}
	for _, state := range []string{"failed", "unknown", "stale", "fresh", "disabled"} {
		if counts[state] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[state], state))
		}
	}
	return strings.Join(parts, " · ") + "  |  s sources"
}
func (m *app) countLabel() string {
	v := m.current()
	if v.total == 0 {
		return "0 items"
	}
	start := v.offset + 1
	if len(v.items) == 0 {
		start = v.offset
	}
	return fmt.Sprintf("%d–%d of %d · page %d/%d", start, v.offset+len(v.items), v.total, v.offset/pageSize+1, (v.total+pageSize-1)/pageSize)
}
