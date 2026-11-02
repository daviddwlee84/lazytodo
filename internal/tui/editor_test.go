package tui

import (
	"context"
	"errors"
	"io"
	"testing"

	tea "charm.land/bubbletea/v2"
	"lazytodo/internal/config"
	"lazytodo/internal/service"
)

type fakeEditor struct{}

func (*fakeEditor) Run() error          { return nil }
func (*fakeEditor) SetStdin(io.Reader)  {}
func (*fakeEditor) SetStdout(io.Writer) {}
func (*fakeEditor) SetStderr(io.Writer) {}
func enableEditor(m *app, next backend, c config.Config) {
	m.editorFactory = func() tea.ExecCommand { return &fakeEditor{} }
	m.reloadConfig = func(backend) (backend, config.Config, error) { return next, c, nil }
}
func TestEditorWaitsForFetchAndCanCancelRequest(t *testing.T) {
	m, f := testModel(t, 1)
	enableEditor(m, f, m.config)
	cancelled := false
	m.fetching = true
	m.fetchGeneration = 2
	m.fetchCancel = func() { cancelled = true }
	press(m, "s")
	if press(m, "e") != nil || !m.editorRequested || !cancelled {
		t.Fatal("did not quiesce fetch")
	}
	press(m, "esc")
	if m.editorRequested {
		t.Fatal("closed editor request still pending")
	}
	_, cmd := m.Update(fetchMsg{generation: 2, err: context.Canceled})
	if m.editingConfig || cmd == nil {
		t.Fatal("late fetch reopened editor")
	}
}
func TestEditorLaunchAfterFetchAndStaleResultsIgnored(t *testing.T) {
	m, f := testModel(t, 1)
	enableEditor(m, f, m.config)
	m.fetching = true
	m.fetchGeneration = 3
	m.fetchCancel = func() {}
	press(m, "s")
	press(m, "e")
	_, cmd := m.Update(fetchMsg{generation: 3, err: context.Canceled})
	if cmd == nil || !m.editingConfig {
		t.Fatal("editor did not launch")
	}
	if m.afterEditor(configEditorDoneMsg{generation: m.editorGeneration - 1}) != nil {
		t.Fatal("accepted stale editor result")
	}
}
func TestEditorReloadPreservesValidContextAndDropsDisabledScope(t *testing.T) {
	m, f, r := repoFixture(t)
	execute(t, m, m.acceptRepoDetection(repoDetectedMsg{result: r}))
	m.current().filter = "remember"
	old := m.backend
	cfg := m.config
	cfg.Sources = append([]config.Source(nil), cfg.Sources...)
	cfg.Sources[0].Disabled = true
	next := &fakeBackend{revision: "two"}
	m.editorGeneration = 1
	m.editingConfig = true
	m.sourceCursor = 1
	cmd := m.applyReload(configReloadedMsg{generation: 1, backend: next, config: cfg})
	if cmd == nil || m.backend == old || m.scopeSource != "" || m.overlay != sourcesOverlay || m.sourceCursor != 1 {
		t.Fatal("disabled scope remained active")
	}
	if len(f.items) == 0 {
		t.Fatal("mutated old backend")
	}
	if press(m, "enter") != nil || m.scopeSource != "" {
		t.Fatal("disabled source focused")
	}
}
func TestEditorValidReloadKeepsScopeSelectionAndEmptyConfigRemainsEditable(t *testing.T) {
	m, f, r := repoFixture(t)
	execute(t, m, m.acceptRepoDetection(repoDetectedMsg{result: r}))
	m.current().selectedRef = "kept"
	m.current().filter = "text"
	enableEditor(m, f, m.config)
	m.editingConfig = true
	m.editorGeneration = 2
	m.applyReload(configReloadedMsg{generation: 2, backend: f, config: m.config})
	if m.scopeSource != "project" || m.current().filter != "text" || m.current().selectedRef != "kept" {
		t.Fatal("lost stable context")
	}
	empty := config.Config{Color: "never", RefreshInterval: "0s"}
	m.editingConfig = true
	m.editorGeneration++
	m.applyReload(configReloadedMsg{generation: m.editorGeneration, backend: f, config: empty})
	if len(m.config.Sources) != 0 || m.scopeSource != "" {
		t.Fatal("removed source remains")
	}
	if press(m, "e") == nil {
		t.Fatal("cannot edit after last source removed")
	}
}
func TestInvalidEditedConfigKeepsCacheAndPausesFetch(t *testing.T) {
	m, f := testModel(t, 1)
	load(t, m)
	m.editingConfig = true
	m.editorGeneration = 1
	old := m.backend
	cmd := m.applyReload(configReloadedMsg{generation: 1, reloadErr: errors.New("invalid TOML")})
	if cmd == nil || m.backend != old || m.configError == "" || len(m.current().items) != 1 {
		t.Fatal("lost old runtime state")
	}
	if m.fetch(false) != nil || m.fetch(true) != nil || f.fetches != 0 {
		t.Fatal("invalid settings allowed fetch")
	}
}
func TestReloadCanApplyValidFileAfterEditorFailure(t *testing.T) {
	m, f := testModel(t, 1)
	m.editingConfig = true
	m.editorGeneration = 1
	cfg := m.config
	cfg.Color = "never"
	m.applyReload(configReloadedMsg{generation: 1, backend: f, config: cfg, editorErr: errors.New("editor exited")})
	if m.configError != "" || m.editingConfig {
		t.Fatal("valid saved changes rejected")
	}
}
func TestReloadInvalidatesSourceAndRepoGenerations(t *testing.T) {
	m, f := testModel(t, 1)
	enableEditor(m, f, m.config)
	oldQuery, oldSources, oldRepo := m.queryGeneration, m.sourceGeneration, m.repoGeneration
	press(m, "s")
	press(m, "e")
	if m.queryGeneration == oldQuery || m.sourceGeneration == oldSources || m.repoGeneration == oldRepo {
		t.Fatal("late replies not invalidated")
	}
	m.Update(queryMsg{generation: oldQuery, view: m.active, err: service.Fail("late", "should be ignored")})
	if m.current().err != "" {
		t.Fatal("old query error resurfaced")
	}
}
