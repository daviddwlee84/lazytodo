package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"lazytodo/internal/config"
	"lazytodo/internal/source"
)

func setupFixture(t *testing.T) *setupApp {
	t.Helper()
	t.Setenv("GH_HOST", "")
	t.Setenv("GITLAB_HOST", "")
	t.Setenv("NO_COLOR", "1")
	draft, err := config.ReadSourceDraft(filepath.Join(t.TempDir(), "new", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	m := newSetup(context.Background(), draft, setupBackend{
		lookPath: func(name string) (string, error) { return "/fake/" + name, nil },
		discover: func(_ context.Context, target source.Target, limit int) (source.Discovery, error) {
			return source.Discovery{Target: target, Account: "fictional", Candidates: []source.Candidate{{Kind: target.Kind, Host: target.Host, Scope: "team/repo", UpdatedAt: time.Unix(1, 0)}}}, nil
		},
		save: func(ctx context.Context, plan *config.SourcePlan) (config.SourceSave, error) { return plan.Save(ctx) },
	})
	t.Cleanup(m.cancel)
	return m
}

func setupPress(m *setupApp, key string) tea.Cmd {
	k := tea.Key{Text: key}
	switch key {
	case "enter":
		k = tea.Key{Code: tea.KeyEnter}
	case "esc":
		k = tea.Key{Code: tea.KeyEscape}
	case "up":
		k = tea.Key{Code: tea.KeyUp}
	case "down":
		k = tea.Key{Code: tea.KeyDown}
	case "left":
		k = tea.Key{Code: tea.KeyLeft}
	case "right":
		k = tea.Key{Code: tea.KeyRight}
	case "home":
		k = tea.Key{Code: tea.KeyHome}
	case "end":
		k = tea.Key{Code: tea.KeyEnd}
	case "tab":
		k = tea.Key{Code: tea.KeyTab}
	case "shift+tab":
		k = tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}
	case "ctrl+c":
		k = tea.Key{Code: 'c', Mod: tea.ModCtrl}
	default:
		k.Code = []rune(key)[0]
	}
	_, cmd := m.Update(tea.KeyPressMsg(k))
	return cmd
}

func setupExecute(m *setupApp, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			setupExecute(m, c)
		}
		return
	}
	m.Update(msg)
}
func setupLoad(m *setupApp) {
	m.Update(setupToolsMsg{true, true})
	setupExecute(m, setupPress(m, "enter"))
}
func setupChooseReview(t *testing.T, m *setupApp) {
	t.Helper()
	setupLoad(m)
	setupPress(m, " ")
	setupPress(m, "enter")
	if m.stage != setupReview || m.plan == nil {
		t.Fatalf("review unavailable: %s", m.notice)
	}
}
func setupNoFile(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("unexpected filesystem write at %s: %v", path, err)
	}
}

func TestSetupDefersToolsAndDiscovery(t *testing.T) {
	m := setupFixture(t)
	lookups, reads := 0, 0
	m.backend.lookPath = func(name string) (string, error) { lookups++; return name, nil }
	m.backend.discover = func(_ context.Context, target source.Target, limit int) (source.Discovery, error) {
		reads++
		return source.Discovery{Target: target}, nil
	}
	_ = m.View()
	init := m.Init()
	if lookups != 0 || reads != 0 {
		t.Fatal("constructor/view/init performed effects")
	}
	batch := init().(tea.BatchMsg)
	m.Update(batch[0]()) // The other command waits for context cancellation.
	if lookups != 2 || reads != 0 || !m.targets[0].selected || !m.targets[1].selected {
		t.Fatal("installed defaults or deferred detection incorrect")
	}
	cmd := setupPress(m, "enter")
	if reads != 0 || m.stage != setupCandidates || !m.targets[0].loading {
		t.Fatal("discovery ran synchronously")
	}
	setupExecute(m, cmd)
	if reads != 2 {
		t.Fatalf("reads = %d", reads)
	}
	if len(m.selected) != 0 {
		t.Fatal("repositories selected without user action")
	}
	setupNoFile(t, filepath.Dir(m.draft.Path))
}

func TestSetupHostDefaultsAndMissingTools(t *testing.T) {
	m := setupFixture(t)
	t.Setenv("GH_HOST", "ENV.example")
	t.Setenv("GITLAB_HOST", "https://invalid.example/path")
	draft := &config.SourceDraft{Path: m.draft.Path, Config: config.Config{Color: "never", Sources: []config.Source{
		{Kind: "github", Host: "first.example", Scope: "team/a", Disabled: true},
		{Kind: "github", Host: "second.example", Scope: "team/b"},
	}}}
	n := newSetup(context.Background(), draft, m.backend)
	defer n.cancel()
	if n.targets[0].target.Host != "first.example" || n.targets[1].target.Host != "gitlab.com" {
		t.Fatalf("defaults = %+v", n.targets)
	}
	draft.Config.Sources = nil
	env := newSetup(context.Background(), draft, m.backend)
	defer env.cancel()
	if env.targets[0].target.Host != "env.example" {
		t.Fatal("valid native host env ignored")
	}
	m.Update(setupToolsMsg{false, true})
	setupPress(m, " ")
	if m.targets[0].selected {
		t.Fatal("missing tool selected")
	}
	m.Update(setupToolsMsg{false, false})
	setupPress(m, "enter")
	if m.stage != setupTargets {
		t.Fatal("advanced with no installed tools")
	}
	view := m.View().Content
	for _, text := range []string{"brew install", "cli.github.com", "gitlab-org/cli", "lazytodo config edit", "lazytodo sources add", m.draft.Path} {
		if !strings.Contains(strings.ReplaceAll(view, "\n", ""), text) {
			t.Errorf("missing guidance %q", text)
		}
	}
}

func TestSetupTextOwnsShortcutsAndPaste(t *testing.T) {
	m := setupFixture(t)
	m.Update(setupToolsMsg{true, true})
	setupPress(m, "e")
	m.input.SetValue("")
	for _, key := range []string{"j", "q", " ", "/", "?"} {
		setupPress(m, key)
	}
	if m.input.Value() != "jq /?" || m.finished || !m.editing || m.targetCursor != 0 {
		t.Fatalf("host text lost ownership: %q", m.input.Value())
	}
	setupPress(m, "enter")
	if !m.editing || m.notice == "" {
		t.Fatal("invalid host accepted")
	}
	m.Update(tea.PasteMsg{Content: "q\njk\n/"})
	if m.stage != setupTargets || m.finished {
		t.Fatal("paste activated action")
	}
	setupPress(m, "esc")
	if m.targets[0].target.Host != "github.com" {
		t.Fatal("discard changed host")
	}
	setupExecute(m, setupPress(m, "enter"))
	setupPress(m, "/")
	for _, key := range []string{"j", "q", " ", "/", "?"} {
		setupPress(m, key)
	}
	if m.filter != "jq /?" || m.finished || len(m.selected) != 0 || m.help {
		t.Fatalf("filter text lost ownership: %q", m.filter)
	}
	setupPress(m, "enter")
	if m.filtering || m.stage != setupCandidates {
		t.Fatal("filter Enter advanced review")
	}
}

func TestSetupPartialResultsSortAndLateMessages(t *testing.T) {
	m := setupFixture(t)
	m.Update(setupToolsMsg{true, true})
	cmd := setupPress(m, "enter")
	batch := cmd().(tea.BatchMsg)
	old := batch[0]().(setupDiscoveryMsg)
	gh, gl := m.targets[0].target, m.targets[1].target
	a := source.Candidate{Kind: gh.Kind, Host: gh.Host, Scope: "team/a", UpdatedAt: time.Unix(20, 0)}
	b := source.Candidate{Kind: gl.Kind, Host: gl.Host, Scope: "team/b", UpdatedAt: time.Unix(30, 0)}
	m.Update(setupDiscoveryMsg{0, old.generation, gh, source.Discovery{Target: gh, Account: "alice", Candidates: []source.Candidate{a}, Scanned: 5}, errors.New("page two failed")})
	if !m.targets[0].discovery.Truncated || m.targets[0].err == nil {
		t.Fatal("partial result called complete")
	}
	m.Update(setupDiscoveryMsg{1, m.targets[1].generation, gl, source.Discovery{Target: gl, Account: "bob", Candidates: []source.Candidate{b}}, nil})
	rows := m.candidates()
	if len(rows) != 2 || rows[0] != b || rows[1] != a {
		t.Fatalf("combined order = %+v", rows)
	}
	setupPress(m, " ")
	selected := m.focusedKey()
	retry := setupPress(m, "r")
	m.Update(old)
	if m.targets[0].discovery.Candidates[0] != a || !m.targets[0].loading {
		t.Fatal("old completion replaced refreshed state")
	}
	commands := retry().(tea.BatchMsg)
	fresh := commands[0]().(setupDiscoveryMsg)
	fresh.result.Candidates = nil
	fresh.err = errors.New("authentication failed")
	m.Update(fresh)
	if len(m.candidates()) != 2 || m.focusedKey() != selected {
		t.Fatal("failed target discarded rows or focus")
	}
	view := m.View().Content
	for _, text := range []string{"WARNING", "gh auth login --hostname github.com", "updated_at", "last_activity_at", "not issue time"} {
		if !strings.Contains(view, text) {
			t.Errorf("missing discovery context %q", text)
		}
	}
	setupPress(m, "esc")
	late := commands[1]().(setupDiscoveryMsg)
	m.Update(late)
	if m.stage != setupTargets || m.targets[1].loading || len(m.selected) != 1 {
		t.Fatal("late result reopened stage or lost selections")
	}
}

func TestSetupFilterSelectionsAndConfiguredSources(t *testing.T) {
	m := setupFixture(t)
	setupLoad(m)
	gh := &m.targets[0]
	gh.discovery.Candidates = []source.Candidate{
		{Kind: "github", Host: "github.com", Scope: "team/Café"},
		{Kind: "github", Host: "github.com", Scope: "team/other"},
	}
	m.targets[1].selected = false
	configured := setupSource(gh.discovery.Candidates[1])
	configured.Disabled = true
	m.configured[config.SourceKey(configured)] = configured
	m.cursor = 1
	setupPress(m, " ")
	if len(m.selected) != 0 || !strings.Contains(m.View().Content, "configured, disabled") {
		t.Fatal("configured row was selectable or unmarked")
	}
	m.cursor = 0
	setupPress(m, " ")
	setupPress(m, "/")
	m.input.SetValue("CAFÉ")
	m.filter = m.input.Value()
	if got := m.candidates(); len(got) != 1 || got[0].Scope != "team/Café" {
		t.Fatalf("normalization = %+v", got)
	}
	m.input.SetValue("absent")
	m.filter = "absent"
	setupPress(m, "enter")
	setupPress(m, " ")
	if len(m.selected) != 1 {
		t.Fatal("empty filter toggled hidden selection")
	}
	setupPress(m, "enter")
	if m.stage != setupReview || len(m.plan.Added) != 1 || m.plan.Added[0].Scope != "team/Café" {
		t.Fatal("review omitted hidden selected source")
	}
	if !strings.Contains(m.View().Content, "team/Café") {
		t.Fatal("review does not name hidden selection")
	}
	if len(m.draft.Config.Sources) != 0 {
		t.Fatal("review mutated draft")
	}
	setupPress(m, "esc")
	if m.filter != "absent" || len(m.selected) != 1 {
		t.Fatal("Back discarded filter or picks")
	}
}

func TestSetupMoreBoundsAndPreservesSelections(t *testing.T) {
	m := setupFixture(t)
	setupLoad(m)
	setupPress(m, " ")
	key := m.focusedKey()
	m.targets[0].discovery.Truncated = true
	m.backend.discover = func(_ context.Context, target source.Target, limit int) (source.Discovery, error) {
		if limit > 1000 || limit < 200 {
			t.Fatalf("limit = %d", limit)
		}
		return source.Discovery{Target: target, Truncated: true}, nil
	}
	for want := 400; want <= 1000; want += 200 {
		setupExecute(m, setupPress(m, "m"))
		if m.targets[0].limit != want {
			t.Fatalf("limit = %d, want %d", m.targets[0].limit, want)
		}
		if _, ok := m.selected[key]; !ok {
			t.Fatal("refresh removed selected reference")
		}
	}
	if setupPress(m, "m") != nil {
		t.Fatal("more exceeded bound")
	}
	setupPress(m, "esc")
	setupPress(m, "e")
	m.input.SetValue("other.example")
	setupPress(m, "enter")
	setupPress(m, "enter")
	if len(m.selected) != 0 {
		t.Fatal("changed target retained hidden old-host selections")
	}
}

func TestSetupReviewDefaultsBackAndOnlySaveWrites(t *testing.T) {
	m := setupFixture(t)
	before := m.draft.Config
	setupChooseReview(t, m)
	if m.button != 0 || !strings.Contains(m.View().Content, "[Back]") {
		t.Fatal("review defaults to Save")
	}
	setupPress(m, "enter")
	if m.stage != setupCandidates || m.saving {
		t.Fatal("default Enter did not go back")
	}
	setupNoFile(t, filepath.Dir(m.draft.Path))
	setupPress(m, "enter")
	setupPress(m, "shift+tab")
	if m.button != 2 {
		t.Fatal("reverse Tab cannot focus Save")
	}
	cmd := setupPress(m, "enter")
	if !m.saving || m.result.Changed || len(m.result.Added) != 0 {
		t.Fatal("save published prematurely")
	}
	if setupPress(m, "enter") != nil {
		t.Fatal("duplicate submit allowed")
	}
	setupExecute(m, cmd)
	if !m.finished || !m.result.Changed || len(m.result.Added) != 1 {
		t.Fatalf("saved result = %+v", m.result)
	}
	data, err := os.ReadFile(m.draft.Path)
	if err != nil || !strings.Contains(string(data), "team/repo") {
		t.Fatalf("save: %s, %v", data, err)
	}
	if !reflect.DeepEqual(before, m.draft.Config) {
		t.Fatal("wizard mutated source draft")
	}
}

func TestSetupCancelBeforeSaveDoesNotWrite(t *testing.T) {
	for _, stage := range []string{"targets", "candidates", "review", "queued save"} {
		t.Run(stage, func(t *testing.T) {
			m := setupFixture(t)
			if stage == "candidates" {
				setupLoad(m)
			}
			if stage == "review" || stage == "queued save" {
				setupChooseReview(t, m)
			}
			var save tea.Cmd
			if stage == "queued save" {
				m.button = 2
				save = setupPress(m, "enter")
			}
			setupPress(m, "ctrl+c")
			setupExecute(m, save)
			if !m.finished || m.result.Changed || m.result.Path != m.draft.Path || len(m.result.Added) != 0 {
				t.Fatalf("cancel result = %+v", m.result)
			}
			setupNoFile(t, filepath.Dir(m.draft.Path))
		})
	}
}

func TestSetupCancellationWaitsForActualSaveOutcome(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(fmt.Sprint(committed), func(t *testing.T) {
			m := setupFixture(t)
			setupChooseReview(t, m)
			started, release := make(chan struct{}), make(chan struct{})
			m.backend.save = func(ctx context.Context, p *config.SourcePlan) (config.SourceSave, error) {
				close(started)
				<-release
				if committed {
					return config.SourceSave{Path: p.Path, Added: p.Added, Changed: true}, nil
				}
				return config.SourceSave{Path: p.Path, Added: p.Added}, ctx.Err()
			}
			m.button = 2
			cmd := setupPress(m, "enter")
			message := make(chan tea.Msg, 1)
			go func() { message <- cmd() }()
			<-started
			if setupPress(m, "ctrl+c") != nil || m.finished || !m.saving {
				t.Fatal("cancel quit before write outcome")
			}
			if _, ok := setupEventFilter(m, tea.InterruptMsg{}).(setupCancelMsg); !ok {
				t.Fatal("signal bypasses save reconciliation")
			}
			m.Update(setupCancelMsg{})
			close(release)
			m.Update(<-message)
			if !m.finished || m.result.Changed != committed {
				t.Fatalf("outcome after cancellation = %+v", m.result)
			}
			if !committed && len(m.result.Added) != 0 {
				t.Fatal("failed save published plan as result")
			}
		})
	}
}

func TestSetupSaveFailureKeepsReviewAndDetectsChangedConfig(t *testing.T) {
	m := setupFixture(t)
	setupChooseReview(t, m)
	if err := os.MkdirAll(filepath.Dir(m.draft.Path), 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte("# edited elsewhere\n" + config.Example)
	if err := os.WriteFile(m.draft.Path, original, 0600); err != nil {
		t.Fatal(err)
	}
	m.button = 2
	setupExecute(m, setupPress(m, "enter"))
	if m.stage != setupReview || m.saving || m.finished || m.button != 0 || len(m.selected) != 1 {
		t.Fatal("save failure discarded editable review")
	}
	if !strings.Contains(m.notice, "restart setup") || m.result.Changed || len(m.result.Added) != 0 {
		t.Fatalf("failure result = %+v / %s", m.result, m.notice)
	}
	got, err := os.ReadFile(m.draft.Path)
	if err != nil || string(got) != string(original) {
		t.Fatal("changed file overwritten")
	}
}

func TestSetupResizeUnicodeHelpAndSanitization(t *testing.T) {
	m := setupFixture(t)
	setupLoad(m)
	m.targets[0].discovery.Account = "fictional\x1b]52;c;payload\a"
	m.targets[0].discovery.Candidates = nil
	for i := 0; i < 80; i++ {
		m.targets[0].discovery.Candidates = append(m.targets[0].discovery.Candidates, source.Candidate{Kind: "github", Host: "github.com", Scope: fmt.Sprintf("專案/é👩🏽‍💻-%03d", i)})
	}
	setupPress(m, "G")
	if m.cursor != len(m.candidates())-1 {
		t.Fatal("End/G did not move to last candidate")
	}
	setupPress(m, " ")
	for _, size := range [][2]int{{80, 24}, {40, 12}, {1, 1}, {0, 0}, {120, 35}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for _, stage := range []setupStage{setupTargets, setupCandidates, setupReview} {
			m.stage = stage
			if stage == setupReview {
				m.plan, _ = m.draft.PlanSources(m.selectedSources())
			}
			for _, help := range []bool{false, true} {
				m.help = help
				view := m.View()
				if !view.AltScreen {
					t.Fatal("standalone screen missing")
				}
				lines := strings.Split(view.Content, "\n")
				if len(lines) > max(1, size[1]) {
					t.Fatalf("height exceeded at %v", size)
				}
				for _, line := range lines {
					if ansi.StringWidth(line) > max(1, size[0]) {
						t.Fatalf("width exceeded at %v: %q", size, line)
					}
				}
				if strings.Contains(view.Content, "\x1b") {
					t.Fatalf("untrusted ANSI or NO_COLOR ignored: %q", view.Content)
				}
			}
		}
	}
}

func TestSetupSmallTerminalCannotSaveHiddenReview(t *testing.T) {
	m := setupFixture(t)
	setupChooseReview(t, m)
	m.button = 2
	m.Update(tea.WindowSizeMsg{Width: 12, Height: 4})
	if cmd := setupPress(m, "enter"); cmd != nil || m.saving {
		t.Fatal("offscreen Save was activated")
	}
	setupNoFile(t, filepath.Dir(m.draft.Path))
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if !strings.Contains(m.View().Content, "[Save]") {
		t.Fatal("resize lost review focus")
	}
}

func TestSetupNarrowInputRemainsVisible(t *testing.T) {
	m := setupFixture(t)
	m.draft.Path = "/tmp/config.toml"
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	setupPress(m, "e")
	if !strings.Contains(m.View().Content, "Host:") {
		t.Fatal("host field clipped")
	}
	setupPress(m, "esc")
	setupLoad(m)
	setupPress(m, "/")
	setupPress(m, "q")
	if !strings.Contains(m.View().Content, "/ q") {
		t.Fatal("filter field clipped")
	}
}

func TestSetupAllFailedProvidesManualAndLoginGuidance(t *testing.T) {
	m := setupFixture(t)
	m.backend.discover = func(_ context.Context, target source.Target, limit int) (source.Discovery, error) {
		return source.Discovery{Target: target}, errors.New("authentication required")
	}
	setupLoad(m)
	view := m.View().Content
	for _, text := range []string{"gh auth login --hostname github.com", "glab auth login --hostname gitlab.com", "r retry", "lazytodo config edit", "lazytodo sources add"} {
		if !strings.Contains(view, text) {
			t.Errorf("missing empty-state help %q: %s", text, view)
		}
	}
}
