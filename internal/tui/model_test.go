package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"lazytodo/internal/config"
	"lazytodo/internal/model"
	"lazytodo/internal/service"
)

type fakeBackend struct {
	items                                    []model.Item
	revision                                 string
	requests                                 []service.QueryRequest
	queryErr                                 error
	fetches, pins, opens, shows, sourceReads int
	due                                      []bool
}

func (f *fakeBackend) Query(_ context.Context, req service.QueryRequest) (service.QueryResult, error) {
	f.requests = append(f.requests, req)
	if f.queryErr != nil {
		return service.QueryResult{}, f.queryErr
	}
	if req.Revision != "" && req.Revision != f.revision {
		return service.QueryResult{Revision: f.revision}, service.Fail("revision_changed", "cache revision changed")
	}
	start := min(req.Offset, len(f.items))
	end := min(start+req.Limit, len(f.items))
	return service.QueryResult{Items: append([]model.Item(nil), f.items[start:end]...), Total: len(f.items), Returned: end - start, Offset: start, Limit: req.Limit, Truncated: end < len(f.items), Revision: f.revision}, nil
}
func (f *fakeBackend) Sources(context.Context) ([]model.SourceStatus, error) {
	f.sourceReads++
	return []model.SourceStatus{{ID: "work", State: "unknown"}}, nil
}
func (f *fakeBackend) Fetch(_ context.Context, _ string, due bool) (service.FetchResult, error) {
	f.fetches++
	f.due = append(f.due, due)
	return service.FetchResult{}, nil
}
func (f *fakeBackend) Pin(_ context.Context, ref string, add bool) error {
	f.pins++
	for i := range f.items {
		if f.items[i].Ref == ref {
			f.items[i].Pinned = add
		}
	}
	return nil
}
func (f *fakeBackend) Open(context.Context, string) error { f.opens++; return nil }
func (f *fakeBackend) Show(_ context.Context, ref string) (model.Item, error) {
	f.shows++
	for _, item := range f.items {
		if item.Ref == ref {
			return item, nil
		}
	}
	return model.Item{}, errors.New("not found")
}
func items(n int) []model.Item {
	result := make([]model.Item, n)
	for i := range result {
		result[i] = model.Item{Ref: fmt.Sprintf("ref-%d", i), Title: fmt.Sprintf("Item %d", i), Body: strings.Repeat("body line\n", 80), SourceID: "work", State: "open", URL: "https://example.test/item", Available: true}
	}
	return result
}
func testModel(t *testing.T, n int) (*app, *fakeBackend) {
	t.Helper()
	f := &fakeBackend{items: items(n), revision: "one"}
	m := newModel(context.Background(), f, config.Config{Color: "never", RefreshInterval: "5m", Sources: []config.Source{{ID: "work", Kind: "snapshot"}}, Views: []config.View{{Name: "Open", Query: "state:open"}}})
	// General transition tests start in All; default Open has dedicated coverage.
	m.active = 0
	t.Cleanup(m.cancel)
	return m, f
}
func execute(t *testing.T, m *app, cmd tea.Cmd) tea.Cmd {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected command")
	}
	_, next := m.Update(cmd())
	return next
}
func load(t *testing.T, m *app) { t.Helper(); execute(t, m, m.query(false)) }
func press(m *app, key string) tea.Cmd {
	k := tea.Key{Text: key}
	switch key {
	case "enter":
		k = tea.Key{Code: tea.KeyEnter}
	case "esc":
		k = tea.Key{Code: tea.KeyEscape}
	case "left":
		k = tea.Key{Code: tea.KeyLeft}
	case "right":
		k = tea.Key{Code: tea.KeyRight}
	case "up":
		k = tea.Key{Code: tea.KeyUp}
	case "down":
		k = tea.Key{Code: tea.KeyDown}
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
	case "ctrl+n":
		k = tea.Key{Code: 'n', Mod: tea.ModCtrl}
	case "ctrl+p":
		k = tea.Key{Code: 'p', Mod: tea.ModCtrl}
	default:
		runes := []rune(key)
		if len(runes) > 0 {
			k.Code = runes[0]
		}
	}
	_, cmd := m.Update(tea.KeyPressMsg(k))
	return cmd
}

func TestStartupDefersAllBackendWork(t *testing.T) {
	m, f := testModel(t, 3)
	frame := m.View().Content
	if !strings.Contains(frame, "lazytodo") {
		t.Fatal("missing initial shell")
	}
	_, cmd := m.Update(m.Init()())
	if len(f.requests)+f.fetches+f.sourceReads != 0 {
		t.Fatal("startup performed synchronous work")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 4 {
		t.Fatalf("startup commands = %#v", batch)
	}
	// The cache and fetch effects are independent, not a blocking sequence.
	execute(t, m, batch[0])
	if len(m.current().items) != 3 || !m.fetching {
		t.Fatal("cache should render while fetch is pending")
	}
	execute(t, m, batch[2])
	if f.fetches != 1 || !f.due[0] {
		t.Fatal("startup must fetch due sources only")
	}
	if f.requests[0].Limit != pageSize || !f.requests[0].IncludeBody {
		t.Fatal("TUI queries must be bounded and include bodies")
	}
}

func TestSearchOwnsPrintableInputAndCursorEdits(t *testing.T) {
	m, f := testModel(t, 3)
	load(t, m)
	press(m, "/")
	for _, r := range "jkhql/?vpsro" {
		press(m, string(r))
	}
	if m.input.Value() != "jkhql/?vpsro" || !m.searching || m.overlay != noOverlay || m.focus != listPane {
		t.Fatalf("input lost ownership: %q", m.input.Value())
	}
	if f.fetches+f.pins+f.opens+f.shows != 0 || m.ctx.Err() != nil {
		t.Fatal("typing activated an operation")
	}
	load(t, m)
	m.selectIndex(2)
	generation := m.queryGeneration
	press(m, "left")
	press(m, "home")
	if m.current().selected != 2 || m.queryGeneration != generation {
		t.Fatal("cursor-only edits reset selection or query")
	}
	press(m, "enter")
	if m.searching || m.focus != listPane || f.shows != 0 {
		t.Fatal("Enter should accept, not inspect")
	}
	press(m, "/")
	press(m, "esc")
	if m.searching || m.current().filter != "" || len(m.current().items) != 0 || m.current().selected != 0 {
		t.Fatal("Esc must clear changed search and reset results")
	}
}

func TestPasteNeverSubmitsOrQuits(t *testing.T) {
	m, f := testModel(t, 2)
	load(t, m)
	press(m, "/")
	m.Update(tea.PasteMsg{Content: "q\njk\n/\x1b]52;c;malicious\a"})
	if !m.searching || m.ctx.Err() != nil || f.shows != 0 || f.opens != 0 {
		t.Fatal("paste activated a binding")
	}
	if strings.Contains(m.input.Value(), "\x1b") {
		t.Fatal("input retained an escape sequence")
	}
}

func TestRefreshSelectionFollowsIdentityAndFailureKeepsRows(t *testing.T) {
	m, f := testModel(t, 5)
	load(t, m)
	m.selectIndex(3)
	m.current().preview = 4
	selected := m.current().selectedRef
	f.items[0], f.items[3] = f.items[3], f.items[0]
	load(t, m)
	if m.current().selectedRef != selected || m.current().selected != 0 || m.current().preview != 4 {
		t.Fatal("refresh lost selected identity or preview scroll")
	}
	f.queryErr = errors.New("cache unavailable")
	load(t, m)
	if len(m.current().items) != 5 || m.current().selectedRef != selected || m.current().err == "" {
		t.Fatal("failed refresh discarded useful context")
	}
	f.queryErr = nil
	f.items = nil
	load(t, m)
	if len(m.current().items) != 0 || m.current().selectedRef != "" || m.current().preview != 0 {
		t.Fatal("successful empty result did not clear old rows")
	}
}

func TestSupersededSuccessAndErrorCannotChangeCurrentQuery(t *testing.T) {
	m, f := testModel(t, 3)
	old := m.query(false)
	oldSuccess := old().(queryMsg)
	oldFailure := oldSuccess
	oldFailure.err = errors.New("late failure")
	m.current().filter = "new"
	current := m.query(true)
	execute(t, m, current)
	m.selectIndex(1)
	for _, msg := range []queryMsg{oldSuccess, oldFailure} {
		m.Update(msg)
		if m.current().err != "" || m.current().loading || m.current().selected != 1 {
			t.Fatal("stale query changed state")
		}
	}
	if len(f.requests) != 2 {
		t.Fatal("unexpected backend calls")
	}
	oldView := m.query(false)().(queryMsg)
	m.switchView(1)
	m.Update(oldView)
	if len(m.current().items) != 0 || !m.current().loading {
		t.Fatal("old view reply populated new view")
	}
}

func TestPerViewQuerySelectionAndScrollRestore(t *testing.T) {
	m, _ := testModel(t, 50)
	load(t, m)
	m.current().filter = "first"
	m.selectIndex(30)
	m.current().preview = 7
	old := *m.current()
	execute(t, m, m.switchView(2))
	m.current().filter = "second"
	m.selectIndex(2)
	execute(t, m, m.switchView(0))
	v := m.current()
	if v.filter != old.filter || v.selectedRef != old.selectedRef || v.scroll != old.scroll || v.preview != old.preview {
		t.Fatal("view context was not restored")
	}
	if m.views[2].query() != "state:open second" {
		t.Fatal("saved-view predicate was lost")
	}
	press(m, "?")
	press(m, "j")
	press(m, "esc")
	if m.current().selectedRef != old.selectedRef || m.current().preview != old.preview || m.current().filter != old.filter {
		t.Fatal("help did not restore context")
	}
}

func TestPagesAreReachableAndRevisionChangeRestarts(t *testing.T) {
	m, f := testModel(t, 205)
	load(t, m)
	if !strings.Contains(m.View().Content, "Ctrl+N") || !strings.Contains(m.View().Content, "of 205") {
		t.Fatal("remaining pages are not advertised")
	}
	execute(t, m, press(m, "ctrl+n"))
	if m.current().offset != 100 || m.current().items[0].Ref != "ref-100" {
		t.Fatal("next page failed")
	}
	execute(t, m, press(m, "ctrl+n"))
	if m.current().offset != 200 || len(m.current().items) != 5 || press(m, "ctrl+n") != nil {
		t.Fatal("last page failed")
	}
	execute(t, m, press(m, "ctrl+p"))
	if m.current().offset != 100 {
		t.Fatal("previous page failed")
	}
	f.revision = "two"
	retry := execute(t, m, m.query(false))
	if m.current().offset != 0 || retry == nil {
		t.Fatal("revision mismatch did not reset page")
	}
	execute(t, m, retry)
	if m.current().revision != "two" || m.current().items[0].Ref != "ref-0" || f.requests[len(f.requests)-1].Revision != "" {
		t.Fatal("revision retry did not read an unpinned first page")
	}
}

func TestPendingMutationsAndRefreshAreNotDuplicated(t *testing.T) {
	m, f := testModel(t, 3)
	load(t, m)
	pin := press(m, "p")
	if press(m, "p") != nil {
		t.Fatal("duplicate pin command")
	}
	requery := execute(t, m, pin)
	if f.pins != 1 || len(m.pins) != 0 {
		t.Fatal("pin receipt not reconciled")
	}
	execute(t, m, requery)
	if !m.current().items[0].Pinned {
		t.Fatal("pin not visible")
	}
	open := press(m, "o")
	if press(m, "o") != nil {
		t.Fatal("duplicate open command")
	}
	execute(t, m, open)
	if f.opens != 1 {
		t.Fatal("explicit open did not call backend")
	}
	press(m, "r")
	if press(m, "r") != nil || m.fetch(true) != nil {
		t.Fatal("duplicate refresh command")
	}
	m.Update(fetchMsg{generation: m.fetchGeneration - 1, err: errors.New("old")})
	if !m.fetching || strings.Contains(m.notice, "old") {
		t.Fatal("stale fetch changed pending state")
	}
}

func TestLateDetailsDoNotReplaceNewSelection(t *testing.T) {
	m, _ := testModel(t, 3)
	load(t, m)
	old := press(m, "enter")()
	m.selectIndex(1)
	m.Update(old)
	if m.current().items[1].Ref != "ref-1" || m.detailPending {
		t.Fatal("late detail replaced selection")
	}
	m.Update(detailMsg{generation: m.detailGeneration - 1, ref: "ref-0", err: errors.New("late detail")})
	if strings.Contains(m.notice, "late") {
		t.Fatal("late detail error leaked")
	}
}

func TestSourcesOverlayNeverProbesAndStaleErrorsIgnored(t *testing.T) {
	m, f := testModel(t, 0)
	load(t, m)
	if cmd := press(m, "s"); cmd != nil {
		t.Fatal("opening sources should not start I/O")
	}
	if f.sourceReads != 0 || f.fetches != 0 || !strings.Contains(m.View().Content, "unknown") {
		t.Fatal("source overlay probed or lost unknown state")
	}
	old := m.readSources()().(sourcesMsg)
	m.readSources()
	old.err = errors.New("late source error")
	m.Update(old)
	if m.sourceError != "" {
		t.Fatal("late source error leaked")
	}
	m.sources = []model.SourceStatus{{ID: "work", State: "stale", Error: "provider unavailable\x1b[31m"}}
	m.sourceCursor = 1
	content := m.View().Content
	if !strings.Contains(content, "stale") || !strings.Contains(content, "provider unavailable") || strings.Contains(content, "\x1b") {
		t.Fatal("source error was not visible and sanitized")
	}
}

func TestNoConfigAndIntervalZero(t *testing.T) {
	f := &fakeBackend{}
	m := newModel(context.Background(), f, config.Config{Color: "never", RefreshInterval: "0s"})
	defer m.cancel()
	load(t, m)
	if !strings.Contains(m.View().Content, "No sources configured") || !strings.Contains(m.View().Content, "lazytodo init") {
		t.Fatal("missing setup guidance")
	}
	if m.fetch(true) != nil || m.nextTick() != nil {
		t.Fatal("unconfigured app must not fetch or tick")
	}
	m.config.Sources = []config.Source{{ID: "off"}}
	if m.fetch(true) != nil {
		t.Fatal("interval zero must suppress automatic fetch")
	}
	if m.fetch(false) == nil {
		t.Fatal("manual refresh should work with interval zero")
	}
	withOverride := newModel(context.Background(), f, config.Config{RefreshInterval: "0s", Sources: []config.Source{{ID: "on", RefreshInterval: "2s"}}})
	defer withOverride.cancel()
	if withOverride.tickInterval != 2*time.Second || withOverride.fetch(true) == nil {
		t.Fatal("per-source interval should override zero default")
	}
}

func TestNavigationAndCancellation(t *testing.T) {
	m, _ := testModel(t, 35)
	load(t, m)
	press(m, "G")
	if m.current().selected != 34 {
		t.Fatal("G should select last")
	}
	press(m, "g")
	press(m, "g")
	if m.current().selected != 0 {
		t.Fatal("gg should select first")
	}
	press(m, "j")
	press(m, "down")
	press(m, "k")
	if m.current().selected != 1 {
		t.Fatal("arrows and Vim navigation differ")
	}
	press(m, "tab")
	press(m, "j")
	if m.focus != previewPane || m.current().preview != 1 || m.current().selected != 1 {
		t.Fatal("preview should scroll, not select")
	}
	press(m, "shift+tab")
	if m.focus != listPane {
		t.Fatal("Shift+Tab should switch focus")
	}
	press(m, "l")
	press(m, "h")
	if m.focus != listPane {
		t.Fatal("h/l focus failed")
	}
	press(m, "/")
	cmd := press(m, "ctrl+c")
	if cmd == nil || !errors.Is(m.ctx.Err(), context.Canceled) {
		t.Fatal("Ctrl+C must cancel owned operations even in input")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("Ctrl+C must exit")
	}
}

func TestTinyResizeUnicodeAndSanitization(t *testing.T) {
	m, f := testModel(t, 2)
	f.items[0].Title = "專案 é 👩🏽‍💻 \x1b]52;c;evil\a title"
	f.items[0].Body = "專案 é 👩🏽‍💻\n" + strings.Repeat("body\t", 30) + "\x1b[2J"
	load(t, m)
	for _, size := range [][2]int{{0, 0}, {1, 1}, {2, 4}, {8, 8}, {20, 10}, {80, 24}, {89, 24}, {90, 24}, {120, 40}} {
		for _, focus := range []pane{listPane, previewPane} {
			m.focus = focus
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			content := m.View().Content
			if !utf8.ValidString(content) || strings.Contains(content, "\x1b") {
				t.Fatalf("invalid or unsafe text at %v", size)
			}
			lines := strings.Split(content, "\n")
			if len(lines) > max(1, size[1]) {
				t.Fatalf("too many lines at %v: %d", size, len(lines))
			}
			for _, line := range lines {
				if ansi.StringWidth(line) > max(1, size[0]) {
					t.Fatalf("line overflow at %v: %q", size, line)
				}
			}
		}
	}
	if !strings.Contains(m.View().Content, "專案 é 👩🏽‍💻") {
		t.Fatal("Unicode graphemes were lost")
	}
	for width := 1; width < 18; width++ {
		truncated := fit("👩🏽‍💻👩🏽‍💻👩🏽‍💻", width)
		stripped := strings.ReplaceAll(strings.TrimSuffix(truncated, "…"), "👩🏽‍💻", "")
		if stripped != "" {
			t.Fatalf("grapheme split at width %d: %q", width, truncated)
		}
	}
}

func TestBindingRegistryHasNoActiveConflicts(t *testing.T) {
	m, _ := testModel(t, 205)
	load(t, m)
	for _, scope := range []keyScope{navigation, searchInput, helpKeys, viewKeys, sourceKeys} {
		seen := map[string]string{}
		for _, b := range bindings {
			if !b.applies(m, scope) {
				continue
			}
			for _, key := range b.keys {
				if other := seen[key]; other != "" {
					t.Errorf("scope %d: %s belongs to %s and %s", scope, key, other, b.id)
				}
				seen[key] = b.id
			}
		}
	}
}

func TestNoColorAndAdaptiveBackground(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	m := newModel(context.Background(), &fakeBackend{}, config.Config{Color: "auto"})
	defer m.cancel()
	if m.color || strings.Contains(m.View().Content, "\x1b") {
		t.Fatal("NO_COLOR ignored")
	}
	m.color = true
	m.dark = false
	light := m.View().Content
	m.dark = true
	if m.View().Content == light {
		t.Fatal("light and dark backgrounds should adapt")
	}
}
