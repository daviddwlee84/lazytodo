package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"lazytodo/internal/config"
	"lazytodo/internal/model"
	"lazytodo/internal/source"
)

// RunSetup owns a standalone terminal reader. Only the explicit review Save
// action writes, through SourcePlan.Save; discovery never fetches issue content.
func RunSetup(ctx context.Context, draft *config.SourceDraft, fetcher *source.Fetcher) (config.SourceSave, error) {
	if draft == nil || fetcher == nil {
		return config.SourceSave{}, errors.New("setup requires a source draft and fetcher")
	}
	m := newSetup(ctx, draft, setupBackend{exec.LookPath, fetcher.Discover,
		func(ctx context.Context, p *config.SourcePlan) (config.SourceSave, error) { return p.Save(ctx) }})
	defer m.cancel()
	// Let the model cancel work and receive the save receipt before quitting.
	// Killing Bubble Tea with the caller's context could hide a completed rename.
	_, err := tea.NewProgram(m, tea.WithContext(context.WithoutCancel(ctx)), tea.WithFilter(setupEventFilter)).Run()
	m.cancel()
	// Also reconcile a write if a terminal error stopped the event loop.
	if m.receipt != nil {
		select {
		case <-m.receipt.started:
			saved := <-m.receipt.done
			if saved.err == nil {
				return saved.result, nil
			}
		default:
		}
	}
	return m.result, err
}

// Injection is limited to the three effects; planning and validation stay real.
type setupBackend struct {
	lookPath func(string) (string, error)
	discover func(context.Context, source.Target, int) (source.Discovery, error)
	save     func(context.Context, *config.SourcePlan) (config.SourceSave, error)
}

type setupStage uint8

const (
	setupTargets setupStage = iota
	setupCandidates
	setupReview
)

type setupTarget struct {
	target              source.Target
	installed, selected bool
	touched, loading    bool
	generation          uint64
	limit               int
	cancel              context.CancelFunc
	discovery           source.Discovery
	err                 error
}

type setupApp struct {
	ctx                   context.Context
	cancel                context.CancelFunc
	backend               setupBackend
	draft                 *config.SourceDraft
	stage                 setupStage
	targets               []setupTarget
	toolsReady            bool
	targetCursor, cursor  int
	scroll, reviewScroll  int
	width, height         int
	dark, color           bool
	input                 textinput.Model
	editing, filtering    bool
	filter                string
	selected              map[string]source.Candidate
	configured            map[string]config.Source
	plan                  *config.SourcePlan
	button                int
	saving, cancelPending bool
	finished              bool
	saveCancel            context.CancelFunc
	receipt               *setupSaveReceipt
	result                config.SourceSave
	notice                string
	help                  bool
	helpScroll            int
}

type setupToolsMsg [2]bool
type setupCancelMsg struct{}
type setupDiscoveryMsg struct {
	index      int
	generation uint64
	target     source.Target
	result     source.Discovery
	err        error
}
type setupSavedMsg struct {
	result config.SourceSave
	err    error
}
type setupSaveReceipt struct {
	started chan struct{}
	done    chan setupSavedMsg
}

func newSetup(ctx context.Context, draft *config.SourceDraft, b setupBackend) *setupApp {
	owned, cancel := context.WithCancel(ctx)
	input := textinput.New()
	input.CharLimit = 1024
	input.SetVirtualCursor(true)
	input.SetWidth(70)
	_, noColor := os.LookupEnv("NO_COLOR")
	m := &setupApp{ctx: owned, cancel: cancel, backend: b, draft: draft,
		width: 80, height: 24, dark: true, input: input,
		color:    draft.Config.Color != "never" && (draft.Config.Color == "always" || !noColor),
		selected: map[string]source.Candidate{}, configured: map[string]config.Source{},
		result: config.SourceSave{Path: draft.Path}}
	for _, s := range draft.Config.Sources {
		m.configured[config.SourceKey(s)] = s
	}
	for _, kind := range []string{"github", "gitlab"} {
		host := kind + ".com"
		env := "GH_HOST"
		if kind == "gitlab" {
			env = "GITLAB_HOST"
		}
		if value, err := setupHost(kind, os.Getenv(env)); err == nil {
			host = value
		}
		for _, s := range draft.Config.Sources {
			if s.Kind == kind {
				host = s.Host
				if host == "" {
					host = kind + ".com"
				}
				break
			}
		}
		m.targets = append(m.targets, setupTarget{target: source.Target{Kind: kind, Host: host}, limit: 200})
	}
	m.styleSetupInput()
	return m
}

func setupHost(kind, host string) (string, error) {
	if host == "" {
		return "", errors.New("host cannot be empty")
	}
	c := config.Config{Color: "auto", RefreshInterval: "5m", Sources: []config.Source{{ID: "setup", Kind: kind, Host: host, Scope: "owner/repo"}}}
	if err := c.Validate(""); err != nil {
		return "", err
	}
	return strings.TrimSuffix(c.Sources[0].Host, "."), nil
}

func (m *setupApp) Init() tea.Cmd {
	lookup, ctx := m.backend.lookPath, m.ctx
	return tea.Batch(func() tea.Msg {
		_, gh := lookup("gh")
		_, glab := lookup("glab")
		return setupToolsMsg{gh == nil, glab == nil}
	}, func() tea.Msg { <-ctx.Done(); return setupCancelMsg{} })
}

func setupEventFilter(current tea.Model, msg tea.Msg) tea.Msg {
	m := current.(*setupApp)
	switch msg.(type) {
	case tea.QuitMsg, tea.InterruptMsg:
		if !m.finished {
			return setupCancelMsg{}
		}
	}
	return msg
}

func (m *setupApp) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case setupToolsMsg:
		m.toolsReady = true
		for i := range m.targets {
			t := &m.targets[i]
			t.installed = msg[i]
			if !t.touched {
				t.selected = msg[i]
			}
			if !msg[i] {
				t.selected = false
			}
		}
	case setupCancelMsg:
		return m, m.quitSetup()
	case setupDiscoveryMsg:
		if msg.index < 0 || msg.index >= len(m.targets) {
			break
		}
		t := &m.targets[msg.index]
		if t.generation != msg.generation || t.target != msg.target || !t.loading {
			break
		}
		ref := m.focusedKey()
		t.loading, t.err = false, msg.err
		if t.cancel != nil {
			t.cancel()
			t.cancel = nil
		}
		if msg.err != nil {
			// Failed refreshes retain previously usable rows, explicitly marked partial.
			msg.result.Candidates = setupMerge(t.discovery.Candidates, msg.result.Candidates)
			msg.result.Truncated = true
			if msg.result.Account == "" {
				msg.result.Account = t.discovery.Account
			}
		}
		t.discovery = msg.result
		m.restoreFocus(ref)
	case setupSavedMsg:
		if !m.saving {
			break
		}
		m.saving = false
		if msg.err == nil {
			m.result, m.finished = msg.result, true
			m.cancel()
			return m, tea.Quit
		}
		m.notice = "Save failed: " + msg.err.Error()
		m.button = 0
		if m.cancelPending {
			return m, m.quitSetup()
		}
	case tea.WindowSizeMsg:
		m.width, m.height = max(1, msg.Width), max(1, msg.Height)
		m.input.SetWidth(max(1, m.width-8))
	case tea.BackgroundColorMsg:
		m.dark = msg.IsDark()
		m.styleSetupInput()
	case tea.KeyPressMsg:
		return m, m.setupKey(msg)
	case tea.MouseMsg:
		return m, m.setupMouse(msg)
	default:
		if m.editing || m.filtering {
			return m, m.setupInput(msg)
		}
	}
	return m, nil
}

func (m *setupApp) quitSetup() tea.Cmd {
	if m.saving {
		m.cancelPending = true
		m.saveCancel()
		m.notice = "Cancelling; waiting for the actual save outcome…"
		return nil
	}
	m.finished = true
	m.cancel()
	return tea.Quit
}

func (m *setupApp) discoverSetup(more bool) tea.Cmd {
	var cmds []tea.Cmd
	for i := range m.targets {
		t := &m.targets[i]
		if !t.selected || !t.installed || (more && (!t.discovery.Truncated || t.limit >= 1000)) {
			continue
		}
		if t.cancel != nil {
			t.cancel()
		}
		if more {
			t.limit = min(1000, t.limit+200)
		}
		t.generation++
		t.loading, t.err = true, nil
		ctx, cancel := context.WithCancel(m.ctx)
		t.cancel = cancel
		index, generation, target, limit, discover := i, t.generation, t.target, t.limit, m.backend.discover
		cmds = append(cmds, func() tea.Msg {
			result, err := discover(ctx, target, limit)
			return setupDiscoveryMsg{index, generation, target, result, err}
		})
	}
	return tea.Batch(cmds...)
}

func setupSource(c source.Candidate) config.Source {
	return config.Source{Kind: c.Kind, Host: c.Host, Scope: c.Scope}
}
func setupKey(c source.Candidate) string { return config.SourceKey(setupSource(c)) }
func setupMerge(a, b []source.Candidate) []source.Candidate {
	byKey := make(map[string]source.Candidate, len(a)+len(b))
	for _, c := range a {
		byKey[setupKey(c)] = c
	}
	for _, c := range b {
		byKey[setupKey(c)] = c
	}
	out := make([]source.Candidate, 0, len(byKey))
	for _, c := range byKey {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if !a.UpdatedAt.Equal(b.UpdatedAt) {
			return a.UpdatedAt.After(b.UpdatedAt)
		}
		if a.Host != b.Host {
			return a.Host < b.Host
		}
		if a.Scope != b.Scope {
			return a.Scope < b.Scope
		}
		return a.Kind < b.Kind
	})
	return out
}
func (m *setupApp) candidates() []source.Candidate {
	var rows []source.Candidate
	for _, t := range m.targets {
		if t.selected {
			rows = append(rows, t.discovery.Candidates...)
		}
	}
	rows = setupMerge(nil, rows)
	query := model.Normalize(m.filter)
	if query == "" {
		return rows
	}
	visible := rows[:0]
	for _, c := range rows {
		if strings.Contains(model.Normalize(c.Kind+" "+c.Host+"/"+c.Scope), query) {
			visible = append(visible, c)
		}
	}
	return visible
}
func (m *setupApp) focusedKey() string {
	rows := m.candidates()
	if m.cursor >= 0 && m.cursor < len(rows) {
		return setupKey(rows[m.cursor])
	}
	return ""
}
func (m *setupApp) restoreFocus(ref string) {
	rows := m.candidates()
	m.cursor = min(max(0, m.cursor), max(0, len(rows)-1))
	for i, c := range rows {
		if setupKey(c) == ref {
			m.cursor = i
			break
		}
	}
}
func (m *setupApp) selectedSources() []config.Source {
	out := make([]config.Source, 0, len(m.selected))
	for _, c := range m.selected {
		out = append(out, setupSource(c))
	}
	sort.Slice(out, func(i, j int) bool { return config.SourceKey(out[i]) < config.SourceKey(out[j]) })
	return out
}

func (m *setupApp) setupInput(msg tea.Msg) tea.Cmd {
	before := m.input.Value()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if m.filtering && before != m.input.Value() {
		m.filter = m.input.Value()
		m.cursor, m.scroll = 0, 0
	}
	return cmd
}

// The same effective bindings drive dispatch, the footer and expanded help.
type setupBinding struct{ id, keys, hint string }

func (m *setupApp) setupBindings() []setupBinding {
	if m.saving {
		return []setupBinding{{"quit", "ctrl+c|esc|q", "Ctrl+C cancel pending save"}}
	}
	if m.editing || m.filtering {
		return []setupBinding{{"accept", "enter", "Enter accept"}, {"input-back", "esc", "Esc discard/clear"}, {"quit", "ctrl+c", "Ctrl+C cancel"}}
	}
	if m.help {
		return []setupBinding{{"help-back", "esc|q|?", "Esc close help"}, {"up", "up|k", "↑/k scroll"}, {"down", "down|j", "↓/j scroll"}, {"quit", "ctrl+c", "Ctrl+C cancel"}}
	}
	var keys []setupBinding
	switch m.stage {
	case setupTargets:
		keys = []setupBinding{{"next", "enter", "Enter discover"}, {"toggle", "space", "Space select"}, {"edit", "e", "e host"}}
	case setupCandidates:
		keys = []setupBinding{{"next", "enter", "Enter review"}, {"toggle", "space", "Space select"}, {"filter", "/", "/ filter"}, {"retry", "r", "r retry"}}
		for _, t := range m.targets {
			if t.selected && t.discovery.Truncated && t.limit < 1000 && !t.loading {
				keys = append(keys, setupBinding{"more", "m", "m more"})
				break
			}
		}
	case setupReview:
		keys = []setupBinding{{"next", "enter", "Enter activate"}, {"button-next", "tab|right|l", "Tab/→ button"}, {"button-prev", "shift+tab|left|h", "← button"}}
	}
	keys = append(keys, setupBinding{"back", "esc", "Esc back"}, setupBinding{"quit", "q|ctrl+c", "q cancel"}, setupBinding{"help", "?", "? help"}, setupBinding{"up", "up|k", "↑/k move"}, setupBinding{"down", "down|j", "↓/j move"}, setupBinding{"first", "home", "Home first"}, setupBinding{"last", "end|G", "End/G last"})
	return keys
}

func (m *setupApp) setupKey(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	if key == " " {
		key = "space"
	}
	for _, b := range m.setupBindings() {
		for _, match := range strings.Split(b.keys, "|") {
			if match == key {
				return m.setupAction(b.id)
			}
		}
	}
	if m.editing || m.filtering {
		return m.setupInput(msg)
	}
	return nil
}

func (m *setupApp) setupAction(action string) tea.Cmd {
	switch action {
	case "quit":
		return m.quitSetup()
	case "help":
		m.help, m.helpScroll = true, 0
	case "help-back":
		m.help = false
	case "accept":
		if m.editing {
			t := &m.targets[m.targetCursor]
			host, err := setupHost(t.target.Kind, m.input.Value())
			if err != nil {
				m.notice = err.Error()
				return nil
			}
			if t.target.Host != host {
				t.target.Host, t.discovery, t.err, t.limit = host, source.Discovery{}, nil, 200
			}
		}
		m.editing, m.filtering = false, false
		m.input.Blur()
		m.notice = ""
	case "input-back":
		if m.filtering {
			m.filter, m.cursor, m.scroll = "", 0, 0
		}
		m.editing, m.filtering = false, false
		m.input.Blur()
		m.notice = ""
	case "edit":
		m.editing = true
		m.input.Prompt = "Host: "
		m.input.SetValue(m.targets[m.targetCursor].target.Host)
		m.input.CursorEnd()
		return m.input.Focus()
	case "filter":
		m.filtering = true
		m.input.Prompt = "/ "
		m.input.SetValue(m.filter)
		m.input.CursorEnd()
		return m.input.Focus()
	case "toggle":
		if m.stage == setupTargets {
			t := &m.targets[m.targetCursor]
			t.touched = true
			if t.installed {
				t.selected = !t.selected
			}
		} else {
			rows := m.candidates()
			if m.cursor < len(rows) {
				c := rows[m.cursor]
				key := setupKey(c)
				if _, exists := m.configured[key]; exists {
					m.notice = "Already configured (including disabled sources); unchanged."
					break
				}
				if _, exists := m.selected[key]; exists {
					delete(m.selected, key)
				} else {
					m.selected[key] = c
				}
				m.notice = ""
			}
		}
	case "next":
		switch m.stage {
		case setupTargets:
			if !m.toolsReady {
				m.notice = "Checking for gh and glab…"
				return nil
			}
			count := 0
			for i := range m.targets {
				t := &m.targets[i]
				if !t.selected {
					continue
				}
				host, err := setupHost(t.target.Kind, t.target.Host)
				if err != nil {
					m.notice = err.Error()
					return nil
				}
				t.target.Host = host
				count++
			}
			if count == 0 {
				m.notice = "Select an installed provider, or use the manual commands below."
				return nil
			}
			// Upstream changes cannot silently save selections from a removed target.
			for key, c := range m.selected {
				active := false
				for _, t := range m.targets {
					active = active || (t.selected && t.target.Kind == c.Kind && t.target.Host == c.Host)
				}
				if !active {
					delete(m.selected, key)
				}
			}
			m.stage, m.notice = setupCandidates, ""
			return m.discoverSetup(false)
		case setupCandidates:
			if len(m.selected) == 0 {
				m.notice = "Select at least one new repository/project with Space."
				return nil
			}
			plan, err := m.draft.PlanSources(m.selectedSources())
			if err != nil {
				m.notice = err.Error()
				return nil
			}
			m.plan, m.stage, m.button, m.reviewScroll, m.notice = plan, setupReview, 0, 0, ""
		case setupReview:
			switch m.button {
			case 0:
				m.stage, m.notice = setupCandidates, ""
			case 1:
				return m.quitSetup()
			case 2:
				return m.saveSetup()
			}
		}
	case "back":
		if m.stage == setupTargets {
			return m.quitSetup()
		}
		if m.stage == setupCandidates {
			for i := range m.targets {
				t := &m.targets[i]
				if t.cancel != nil {
					t.cancel()
					t.cancel = nil
				}
				t.generation++
				t.loading = false
			}
		}
		m.stage--
		m.notice = ""
	case "retry":
		return m.discoverSetup(false)
	case "more":
		return m.discoverSetup(true)
	case "button-next":
		m.button = (m.button + 1) % 3
	case "button-prev":
		m.button = (m.button + 2) % 3
	case "up", "down", "first", "last":
		position, limit := &m.cursor, len(m.candidates())-1
		if m.help {
			position, limit = &m.helpScroll, len(m.setupHelp())-1
		} else if m.stage == setupTargets {
			position, limit = &m.targetCursor, 1
		} else if m.stage == setupReview {
			position, limit = &m.reviewScroll, len(m.reviewLines())-1
		}
		switch action {
		case "up":
			*position--
		case "down":
			*position++
		case "first":
			*position = 0
		case "last":
			*position = limit
		}
		*position = min(max(0, *position), max(0, limit))
	}
	return nil
}

func (m *setupApp) setupSmall() bool {
	return m.width < 32 || m.height < 8 || len(wrapped("Config: "+m.draft.Path, m.width))+7 > m.height
}

func (m *setupApp) saveSetup() tea.Cmd {
	if m.saving || m.plan == nil {
		return nil
	}
	if m.setupSmall() {
		m.notice = "Resize the terminal to review the destination before saving."
		return nil
	}
	m.saving, m.notice = true, "Saving reviewed sources…"
	ctx, cancel := context.WithCancel(m.ctx)
	m.saveCancel = cancel
	receipt := &setupSaveReceipt{make(chan struct{}), make(chan setupSavedMsg, 1)}
	m.receipt = receipt
	plan, save := m.plan, m.backend.save
	return func() tea.Msg {
		close(receipt.started)
		defer cancel()
		var msg setupSavedMsg
		if err := ctx.Err(); err != nil {
			msg.err = err
		} else {
			msg.result, msg.err = save(ctx, plan)
		}
		receipt.done <- msg
		return msg
	}
}

func (m *setupApp) styleSetupInput() {
	styles := textinput.Styles{}
	if m.color {
		styles = textinput.DefaultStyles(m.dark)
		neutral := lipgloss.LightDark(m.dark)(lipgloss.Color("#333333"), lipgloss.Color("#dedede"))
		styles.Focused.Prompt = lipgloss.NewStyle().Foreground(neutral).Bold(true)
		styles.Focused.Text = lipgloss.NewStyle()
		styles.Cursor.Color = neutral
	}
	styles.Cursor.Blink = false
	m.input.SetStyles(styles)
}

func setupCLI(kind string) string {
	if kind == "gitlab" {
		return "glab"
	}
	return "gh"
}
func setupLogin(t source.Target) string {
	return setupCLI(t.Kind) + " auth login --hostname " + oneLine(t.Host)
}
func setupManual() []string {
	return []string{"Manual: lazytodo config edit", "lazytodo sources add --provider github --scope owner/repo"}
}

func (m *setupApp) targetLines() []string {
	if m.editing {
		return []string{"Edit " + m.targets[m.targetCursor].target.Kind + " host (hostname, not URL).", m.input.View(), "Active native credentials will be reused on this host."}
	}
	lines := []string{"Reuse active gh/glab credentials on the explicit hosts below.", "Scope metadata only; no issues fetched, no login/install is executed.", ""}
	for i, t := range m.targets {
		focus, check, status := "  ", " ", "checking CLI…"
		if i == m.targetCursor {
			focus = "> "
		}
		if t.selected {
			check = "x"
		}
		if m.toolsReady {
			status = "installed"
			if !t.installed {
				status = "missing " + setupCLI(t.target.Kind)
			}
		}
		lines = append(lines, fmt.Sprintf("%s[%s] %s  %s  (%s)", focus, check, t.target.Kind, oneLine(t.target.Host), status))
	}
	lines = append(lines, "", "Missing tools: macOS: brew install gh glab", "Other systems: https://cli.github.com/", "GitLab CLI: https://gitlab.com/gitlab-org/cli")
	return append(lines, setupManual()...)
}

func (m *setupApp) discoveryLines() []string {
	var lines []string
	for _, t := range m.targets {
		if !t.selected {
			continue
		}
		d := t.discovery
		status := fmt.Sprintf("%d candidates, %d scanned", len(d.Candidates), d.Scanned)
		if d.Truncated {
			status += ", truncated"
		}
		status += fmt.Sprintf(" (limit %d)", t.limit)
		if t.loading {
			status = "loading… · " + status
		}
		if t.err != nil {
			status = "WARNING partial/failed · " + status
		}
		account := oneLine(d.Account)
		if account == "" {
			account = "account unknown"
		}
		lines = append(lines, oneLine(t.target.Kind+" "+t.target.Host+" · "+account), status)
		if t.err != nil {
			lines = append(lines, "Warning: "+oneLine(t.err.Error()), "Check credentials: "+setupLogin(t.target))
		}
	}
	return lines
}
func (m *setupApp) reviewLines() []string {
	if m.plan == nil {
		return nil
	}
	lines := []string{fmt.Sprintf("%d new sources; %d already configured/skipped.", len(m.plan.Added), len(m.plan.Skipped)), "Only append these source registrations; credentials remain with gh/glab."}
	for _, group := range []struct {
		label   string
		sources []config.Source
	}{{"ADD", m.plan.Added}, {"SKIP", m.plan.Skipped}} {
		for _, s := range group.sources {
			lines = append(lines, wrapped(group.label+" "+s.Kind+" | "+s.Host+" | "+s.Scope, m.width)...)
		}
	}
	return lines
}
func (m *setupApp) setupHelp() []string {
	// Help describes the underlying stage, not just its own close bindings.
	copy := *m
	copy.help = false
	lines := []string{"Setup keys (current stage)"}
	for _, b := range copy.setupBindings() {
		lines = append(lines, b.hint)
	}
	lines = append(lines, "", "[x] selected; [=] already configured (disabled sources stay disabled).", "No repositories/projects are selected automatically.", "Filtering is local and Unicode-normalized; hidden selections persist.", "Review lists every selection, including those hidden by the filter.", "GitHub: repository updated_at. GitLab: project last_activity_at.", "Neither timestamp is an issue's update time.", "Partial/failed discovery retains usable rows; r retries; m raises limit", "by 200 up to 1000 when truncated. At 1000 use filter/manual setup.", "Archived and no-issues projects are excluded by discovery.", "Missing CLI: macOS brew install gh glab", "Other systems: https://cli.github.com/ or https://gitlab.com/gitlab-org/cli")
	lines = append(lines, setupManual()...)
	for _, line := range m.discoveryLines() {
		lines = append(lines, wrapped(line, m.width)...)
	}
	for _, t := range m.targets {
		if t.selected {
			lines = append(lines, setupLogin(t.target))
		}
	}
	return lines
}

// setupHit describes what a click on one rendered wizard line selects.
type setupHitKind uint8

const (
	setupHitNone setupHitKind = iota
	setupHitTarget
	setupHitCandidate
	setupHitButtons
	setupHitFilter
)

type setupHit struct {
	kind  setupHitKind
	index int
	spans []hitSpan
}

// setupBox is the cell range of the "[x]" checkbox on target/candidate rows.
const setupBoxFrom, setupBoxTo = 2, 5

func (m *setupApp) View() tea.View {
	lines, _ := m.frame()
	if m.color && len(lines) > 0 {
		lines[0] = lipgloss.NewStyle().Bold(true).Render(lines[0])
	}
	view := tea.NewView(strings.Join(lines, "\n"))
	view.AltScreen = true
	if m.draft.Config.Mouse {
		view.MouseMode = tea.MouseModeCellMotion
	}
	return view
}

// frame renders the wizard and, in parallel, the click target of each line, so
// mouse hit testing never recomputes scroll windows or truncation separately.
func (m *setupApp) frame() ([]string, []setupHit) {
	stage := []string{"1/3 Targets", "2/3 Candidates", "3/3 Review"}[m.stage]
	header := append([]string{"lazytodo setup · " + stage}, wrapped("Config: "+m.draft.Path, m.width)...)
	var body, suffix []string
	var bodyHits []setupHit
	var suffixHits []setupHit
	if m.help {
		lines := m.setupHelp()
		start := min(m.helpScroll, max(0, len(lines)-max(1, m.height-len(header)-2)))
		body = lines[start:]
	} else {
		switch m.stage {
		case setupTargets:
			body = m.targetLines()
			if !m.editing {
				// targetLines: three intro lines, then one row per target.
				for i := range m.targets {
					for len(bodyHits) < 3+i {
						bodyHits = append(bodyHits, setupHit{})
					}
					bodyHits = append(bodyHits, setupHit{kind: setupHitTarget, index: i})
				}
			}
		case setupCandidates:
			header = append(header, m.discoveryLines()...)
			header = append(header, "Order: GitHub updated_at / GitLab last_activity_at (not issue time).")
			filter := "Filter: " + oneLine(m.filter)
			if m.filtering {
				filter = m.input.View()
			}
			rows := m.candidates()
			if len(header)+6 > m.height {
				// Keep the list and input usable; expanded status remains in help.
				header = []string{"lazytodo setup · " + stage}
				for _, t := range m.targets {
					if !t.selected {
						continue
					}
					state := "ready"
					if t.discovery.Truncated {
						state = "truncated"
					}
					if t.loading {
						state = "loading"
					}
					if t.err != nil {
						state = "WARNING partial/failed"
					}
					header = append(header, state+" · "+oneLine(t.target.Host))
				}
			}
			header = append(header, fmt.Sprintf("%d visible · %d selected (including hidden)", len(rows), len(m.selected)), filter)
			available := max(1, m.height-len(header)-3)
			start := min(m.scroll, max(0, len(rows)-available))
			if m.cursor < start {
				start = m.cursor
			}
			if m.cursor >= start+available {
				start = m.cursor - available + 1
			}
			start = max(0, start)
			for i := start; i < len(rows) && i < start+available; i++ {
				c := rows[i]
				key := setupKey(c)
				focus, mark, note := "  ", " ", ""
				if i == m.cursor {
					focus = "> "
				}
				if c.Private {
					note = " [private]"
				}
				if _, ok := m.selected[key]; ok {
					mark = "x"
				}
				if existing, ok := m.configured[key]; ok {
					mark, note = "=", " [configured]"
					if existing.Disabled {
						note = " [configured, disabled]"
					}
				}
				stamp := "unknown"
				if !c.UpdatedAt.IsZero() {
					stamp = c.UpdatedAt.UTC().Format("2006-01-02")
				}
				body = append(body, focus+"["+mark+"] "+stamp+" "+oneLine(c.Host+"/"+c.Scope)+note)
				bodyHits = append(bodyHits, setupHit{kind: setupHitCandidate, index: i})
			}
			if len(rows) == 0 {
				body = []string{"No visible candidates yet. Clear / filter, r retry, or Esc choose hosts.", "Check setup/authentication for the selected hosts:"}
				for _, t := range m.targets {
					if t.selected && t.err == nil {
						body = append(body, setupLogin(t.target))
					}
				}
				body = append(body, setupManual()...)
				bodyHits = nil
			}
		case setupReview:
			// The wrapped header names the exact configuration destination.
			lines := m.reviewLines()
			available := max(1, m.height-len(header)-5)
			start := min(m.reviewScroll, max(0, len(lines)-available))
			body = lines[start:min(len(lines), start+available)]
			buttons := []string{" Back ", " Cancel ", " Save "}
			buttons[m.button] = "[" + strings.TrimSpace(buttons[m.button]) + "]"
			suffix = []string{fmt.Sprintf("Review lines %d–%d/%d · ↑↓/jk scroll", min(start+1, len(lines)), min(start+available, len(lines)), len(lines)), strings.Join(buttons, "  ")}
			suffixHits = []setupHit{{}, {kind: setupHitButtons, spans: buttonSpans(buttons, "  ")}}
		}
	}
	footer := []string{}
	for _, b := range m.setupBindings() {
		if b.id == "up" || b.id == "down" || b.id == "first" || b.id == "last" {
			continue
		}
		footer = append(footer, b.hint)
	}
	footerText := strings.Join(footer, " · ")
	if ansi.StringWidth(footerText) > m.width && !m.editing && !m.filtering && !m.saving && !m.help {
		footer = nil
		for _, b := range m.setupBindings() {
			switch b.id {
			case "next", "toggle", "filter", "edit", "button-next", "back", "quit", "help":
				footer = append(footer, b.hint)
			}
		}
		footerText = strings.Join(footer, " · ")
	}
	room := max(0, m.height-len(header)-len(suffix)-2)
	if len(body) > room {
		body = body[:room]
	}
	hits := make([]setupHit, len(header))
	if m.stage == setupCandidates && !m.help && len(header) > 0 {
		hits[len(header)-1] = setupHit{kind: setupHitFilter}
	}
	for i := range body {
		h := setupHit{}
		if i < len(bodyHits) {
			h = bodyHits[i]
		}
		hits = append(hits, h)
	}
	lines := append(header, body...)
	lines = append(lines, suffix...)
	hits = append(hits, suffixHits...)
	for len(hits) < len(lines) {
		hits = append(hits, setupHit{})
	}
	lines = append(lines, oneLine(m.notice), footerText)
	hits = append(hits, setupHit{}, setupHit{})
	if m.setupSmall() {
		lines = []string{"Setup " + stage, "Resize terminal", "Esc back; Ctrl+C cancel"}
		hits = make([]setupHit, len(lines))
	}
	if len(lines) > m.height {
		lines, hits = lines[:m.height], hits[:m.height]
	}
	for i := range lines {
		lines[i] = fit(lines[i], m.width)
	}
	return lines, hits
}

func (m *setupApp) setupMouse(msg tea.MouseMsg) tea.Cmd {
	if !m.draft.Config.Mouse || m.editing || m.filtering || m.saving || m.setupSmall() {
		return nil
	}
	ev := msg.Mouse()
	switch msg.(type) {
	case tea.MouseWheelMsg:
		switch ev.Button {
		case tea.MouseWheelUp:
			return m.setupAction("up")
		case tea.MouseWheelDown:
			return m.setupAction("down")
		}
	case tea.MouseClickMsg:
		if ev.Button != tea.MouseLeft || m.help {
			return nil
		}
		_, hits := m.frame()
		if ev.Y < 0 || ev.Y >= len(hits) {
			return nil
		}
		h := hits[ev.Y]
		box := ev.X >= setupBoxFrom && ev.X < setupBoxTo
		switch h.kind {
		case setupHitTarget:
			// A click on the checkbox, or on the focused row, toggles like Space.
			if box || m.targetCursor == h.index {
				m.targetCursor = h.index
				return m.setupAction("toggle")
			}
			m.targetCursor = h.index
		case setupHitCandidate:
			if box || m.cursor == h.index {
				m.cursor = h.index
				return m.setupAction("toggle")
			}
			m.cursor = h.index
		case setupHitFilter:
			return m.setupAction("filter")
		case setupHitButtons:
			for _, s := range h.spans {
				if ev.X >= s.from && ev.X < s.to {
					// Review keeps Back as default; Save needs a focused second click.
					if m.button == s.id {
						return m.setupAction("next")
					}
					m.button = s.id
				}
			}
		}
	}
	return nil
}
