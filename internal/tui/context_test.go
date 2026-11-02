package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"lazytodo/internal/config"
	"lazytodo/internal/model"
	"lazytodo/internal/repoctx"
)

func repoFixture(t *testing.T) (*app, *fakeBackend, repoctx.Result) {
	t.Helper()
	f := &fakeBackend{items: items(2), revision: "one"}
	c := config.Config{RepoContext: true, Color: "never", RefreshInterval: "0s", Sources: []config.Source{{ID: "project", Kind: "github", Host: "github.com", Scope: "owner/project"}, {ID: "other", Kind: "github", Host: "github.com", Scope: "owner/other"}}}
	m := newModel(context.Background(), f, c)
	t.Cleanup(m.cancel)
	return m, f, repoctx.Result{Root: "/fictional/project", Preferred: 0, Targets: []repoctx.Target{{Kind: "github", Host: "github.com", Scope: "owner/project", Remote: "origin"}}}
}
func TestDefaultOpenAndConfiguredCwdScope(t *testing.T) {
	m, f, r := repoFixture(t)
	if m.current().name != "Open" || m.current().base != "state:open" {
		t.Fatal("wrong default", m.current())
	}
	cmd := m.acceptRepoDetection(repoDetectedMsg{result: r})
	execute(t, m, cmd)
	if m.scopeSource != "project" || m.repoSourceID != "project" {
		t.Fatal(m.scopeSource)
	}
	if got := f.requests[len(f.requests)-1].Text; got != "source:project state:open" {
		t.Fatal(got)
	}
	if !strings.Contains(m.View().Content, "(cwd)") {
		t.Fatal("cwd context hidden")
	}
}
func TestLateCwdDoesNotStealUserContext(t *testing.T) {
	m, _, r := repoFixture(t)
	press(m, "/")
	if cmd := m.acceptRepoDetection(repoDetectedMsg{result: r}); cmd != nil {
		t.Fatal("late result changed view")
	}
	if m.scopeSource != "" || !m.searching {
		t.Fatal("stole active filter")
	}
	press(m, "esc")
	execute(t, m, press(m, "c"))
	if m.scopeSource != "project" {
		t.Fatal(m.scopeSource)
	}
}
func TestUnknownCwdPromptsWithIgnoreDefault(t *testing.T) {
	m, _, r := repoFixture(t)
	r.Targets[0].Scope = "owner/new"
	m.repoConfigPath = "/fictional/config.toml"
	calls := 0
	m.repoRegister = func(context.Context, backend, repoctx.Target) (repoRegistration, error) {
		calls++
		return repoRegistration{}, nil
	}
	m.acceptRepoDetection(repoDetectedMsg{result: r})
	if m.overlay != repoOverlay || m.repoButton != 0 {
		t.Fatal("missing safe prompt")
	}
	press(m, "enter")
	if !m.repoIgnored || m.overlay != noOverlay || calls != 0 {
		t.Fatal("default Enter wrote instead of ignored")
	}
	press(m, "c")
	if m.overlay != repoOverlay {
		t.Fatal("cannot revisit ignored context")
	}
}
func TestSourcePickerAndViewsAreIndependent(t *testing.T) {
	m, f, _ := repoFixture(t)
	load(t, m)
	press(m, "s")
	press(m, "j")
	execute(t, m, press(m, "enter"))
	if m.scopeSource != "project" || m.current().name != "Open" {
		t.Fatal("lost view on scope selection")
	}
	execute(t, m, m.switchView(0))
	if got := f.requests[len(f.requests)-1].Text; got != "source:project" {
		t.Fatal("All erased scope", got)
	}
	m.current().filter = "remember"
	execute(t, m, m.changeScope("other", false))
	if m.current().filter != "" {
		t.Fatal("scope leaked filter")
	}
	execute(t, m, m.changeScope("project", false))
	if m.current().filter != "remember" {
		t.Fatal("per-scope filter lost")
	}
	press(m, "s")
	press(m, "home")
	execute(t, m, press(m, "enter"))
	if m.scopeSource != "" {
		t.Fatal("cannot return to all sources")
	}
}
func TestDisabledCwdAndAmbiguityDoNotAutoWrite(t *testing.T) {
	m, _, r := repoFixture(t)
	m.config.Sources[0].Disabled = true
	m.acceptRepoDetection(repoDetectedMsg{result: r})
	if m.overlay != noOverlay || m.scopeSource != "" || !strings.Contains(m.repoNote, "disabled") {
		t.Fatal("disabled source activated")
	}
	r.Preferred = -1
	r.Targets = append(r.Targets, repoctx.Target{Kind: "github", Host: "github.com", Scope: "owner/other", Remote: "backup"})
	m.config.Sources[0].Disabled = false
	m.acceptRepoDetection(repoDetectedMsg{result: r})
	if m.overlay != repoOverlay || m.repoConfirm {
		t.Fatal("ambiguous context guessed")
	}
	press(m, "j")
	execute(t, m, press(m, "enter"))
	if m.scopeSource != "other" {
		t.Fatal(m.scopeSource)
	}
}
func TestRegistrationQueuesTargetFetchAndKeepsOldBackendImmutable(t *testing.T) {
	m, f, r := repoFixture(t)
	r.Targets[0].Scope = "owner/new"
	m.repoConfigPath = "/config.toml"
	m.acceptRepoDetection(repoDetectedMsg{result: r})
	next := &fakeBackend{items: items(1), revision: "two"}
	cfg := m.config
	cfg.Sources = append(append([]config.Source(nil), cfg.Sources...), config.Source{ID: "new", Kind: "github", Host: "github.com", Scope: "owner/new"})
	m.repoRegister = func(_ context.Context, b backend, target repoctx.Target) (repoRegistration, error) {
		if b != f || target.Scope != "owner/new" {
			t.Fatal("wrong registration target")
		}
		return repoRegistration{backend: next, config: cfg, sourceID: "new", saved: config.SourceSave{Changed: true}}, nil
	}
	m.fetching = true
	m.fetchGeneration = 4
	press(m, "tab")
	cmd := press(m, "enter")
	if cmd == nil || !m.repoPending {
		t.Fatal("did not submit")
	}
	if press(m, "enter") != nil {
		t.Fatal("duplicate submit")
	}
	m.Update(cmd())
	if m.backend != next || m.pendingFetchID != "new" || m.scopeSource != "new" {
		t.Fatal("registration not applied")
	}
	if len(f.items) != 2 || next.fetches != 0 {
		t.Fatal("fetched before current refresh ended")
	}
	_, effect := m.Update(fetchMsg{generation: 4})
	if effect == nil || !m.fetching || m.fetchSourceID != "new" || m.pendingFetchID != "" {
		t.Fatal("new source fetch not queued correctly")
	}
}
func TestRepoPromptHiddenTargetCannotSubmit(t *testing.T) {
	m, _, r := repoFixture(t)
	r.Targets[0].Scope = "owner/new"
	m.repoRegister = func(context.Context, backend, repoctx.Target) (repoRegistration, error) {
		t.Fatal("hidden submit")
		return repoRegistration{}, nil
	}
	m.acceptRepoDetection(repoDetectedMsg{result: r})
	m.Update(tea.WindowSizeMsg{Width: 20, Height: 5})
	press(m, "tab")
	if press(m, "enter") != nil || m.repoPending {
		t.Fatal("submitted clipped target")
	}
}
func TestRepoLabelsAreHumanReadableAndDisambiguated(t *testing.T) {
	m, _, _ := repoFixture(t)
	i := model.Item{SourceID: "project", Scope: "owner/project", Origin: model.Origin{Authority: "github.com"}, DisplayRef: "owner/project#42"}
	if m.repoLabel(i) != "project" || nativeNumber(i) != "#42" {
		t.Fatal("opaque row identity")
	}
	m.config.Sources = append(m.config.Sources, config.Source{ID: "same-name", Kind: "github", Host: "github.com", Scope: "someone/project"})
	if m.repoLabel(i) != "owner/project" {
		t.Fatal("ambiguous repo name")
	}
	m.sources = []model.SourceStatus{{ID: "hash-that-must-not-fill-footer", State: "fresh"}, {ID: "another-hash", State: "failed", Error: "error"}}
	summary := m.sourceSummary()
	if strings.Contains(summary, "hash") || !strings.Contains(summary, "1 failed") {
		t.Fatal(summary)
	}
}
