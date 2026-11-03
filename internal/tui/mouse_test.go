package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"lazytodo/internal/model"
	"lazytodo/internal/repoctx"
)

// Geometry at 120x30: wide layout, list x∈[0,51), preview inner from x=52,
// content rows from y=4 (22 rows), footer at y=29.
func mouseModel(t *testing.T, n int) (*app, *fakeBackend) {
	t.Helper()
	m, f := testModel(t, n)
	m.mouse = true
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	load(t, m)
	return m, f
}
func leftClick(m *app, x, y int) tea.Cmd {
	_, cmd := m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	return cmd
}
func wheel(m *app, x, y int, button tea.MouseButton) {
	m.Update(tea.MouseWheelMsg{X: x, Y: y, Button: button})
}
func runBatch(m *app, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			runBatch(m, c)
		}
		return
	}
	m.Update(msg)
}
func footerX(t *testing.T, m *app, id string) int {
	t.Helper()
	x := 0
	for _, s := range m.footerSegments() {
		if s.id == id {
			return x
		}
		x += ansi.StringWidth(s.text) + ansi.StringWidth(footerSeparator)
	}
	t.Fatalf("footer has no %s segment", id)
	return 0
}

func TestMouseClickSelectsAndSecondClickInspects(t *testing.T) {
	m, f := mouseModel(t, 5)
	m.focus = previewPane
	if leftClick(m, 5, 4+2) != nil || m.current().selected != 2 || m.focus != listPane {
		t.Fatal("row click did not select/focus the list")
	}
	cmd := leftClick(m, 5, 4+2)
	if cmd == nil || m.focus != previewPane {
		t.Fatal("second click on the selected row did not inspect")
	}
	runBatch(m, cmd)
	if f.shows != 1 {
		t.Fatal("inspect did not read the item")
	}
	// Rows past the end and the pane title are inert.
	leftClick(m, 5, 4+10)
	if m.current().selected != 2 {
		t.Fatal("click past the last row moved selection")
	}
}

func TestMouseWheelTargetsPaneUnderPointer(t *testing.T) {
	m, _ := mouseModel(t, 5)
	wheel(m, 5, 6, tea.MouseWheelDown)
	wheel(m, 5, 6, tea.MouseWheelDown)
	if m.current().selected != 2 {
		t.Fatal("list wheel did not move selection")
	}
	wheel(m, 60, 6, tea.MouseWheelDown)
	if m.current().preview != 3 || m.current().selected != 2 {
		t.Fatal("preview wheel did not scroll the preview only")
	}
	wheel(m, 60, 6, tea.MouseWheelUp)
	if m.current().preview != 0 {
		t.Fatal("preview wheel up")
	}
}

func TestMouseOverlayPickersAndFooter(t *testing.T) {
	m, f := mouseModel(t, 3)
	runBatch(m, leftClick(m, footerX(t, m, "views"), 29))
	if m.overlay != viewsOverlay {
		t.Fatal("footer click did not open views")
	}
	leftClick(m, 3, 4+1)
	if m.overlayCursor != 1 || m.overlay != viewsOverlay {
		t.Fatal("first picker click should only move the cursor")
	}
	runBatch(m, leftClick(m, 3, 4+1))
	if m.active != 1 || m.overlay != noOverlay {
		t.Fatal("second picker click did not choose the view")
	}
	// Quit is never a click target.
	if leftClick(m, footerX(t, m, "quit"), 29) != nil || m.ctx.Err() != nil {
		t.Fatal("footer click quit the TUI")
	}
	// Header: the view name opens views, the scope name opens sources.
	lead, view, sep, _ := m.headerParts()
	leftClick(m, ansi.StringWidth(lead), 0)
	if m.overlay != viewsOverlay {
		t.Fatal("header view click")
	}
	m.overlay = noOverlay
	leftClick(m, ansi.StringWidth(lead+view+sep), 0)
	if m.overlay != sourcesOverlay {
		t.Fatal("header scope click")
	}
	// Sources picker rows start after two intro lines; row 1 is the first source.
	leftClick(m, 3, 4+3)
	if m.sourceCursor != 1 {
		t.Fatalf("source click cursor = %d", m.sourceCursor)
	}
	runBatch(m, leftClick(m, 3, 4+3))
	if m.scopeSource != "work" || m.overlay != noOverlay {
		t.Fatal("second source click did not focus the source")
	}
	if f.fetches != 0 {
		t.Fatal("mouse navigation fetched")
	}
}

func TestMouseTagPickerAddsExactFilter(t *testing.T) {
	m, f := mouseModel(t, 2)
	f.items[0].Labels = []model.Label{{Name: "needs review"}, {Name: "P1"}}
	load(t, m)
	press(m, "t")
	_, hits := m.tagRows(m.width - 2)
	row := -1
	for i, h := range hits {
		if h.cursor == 1 {
			row = i
		}
	}
	if row < 0 {
		t.Fatal("tag rows have no hit for label 1")
	}
	leftClick(m, 3, 4+row)
	runBatch(m, leftClick(m, 3, 4+row))
	if m.overlay != noOverlay || !strings.Contains(m.current().filter, "label:") {
		t.Fatalf("tag click filter = %q", m.current().filter)
	}
}

func TestMouseSearchTinyAndDisabled(t *testing.T) {
	m, _ := mouseModel(t, 3)
	press(m, "/")
	leftClick(m, 3, 1)
	if !m.searching {
		t.Fatal("click on the query line ended search editing")
	}
	leftClick(m, 5, 4+1)
	if m.searching || m.current().selected != 1 {
		t.Fatal("click outside the input should accept search and apply")
	}
	m.Update(tea.WindowSizeMsg{Width: 9, Height: 7})
	leftClick(m, 2, 2)
	if m.current().selected != 1 {
		t.Fatal("tiny layout accepted a click")
	}
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	if m.View().MouseMode != tea.MouseModeCellMotion {
		t.Fatal("mouse mode not requested")
	}
	press(m, "M")
	if m.mouse || m.View().MouseMode != tea.MouseModeNone {
		t.Fatal("M did not release mouse capture")
	}
	leftClick(m, 5, 4+2)
	if m.current().selected != 1 {
		t.Fatal("click handled while capture is off")
	}
}

func TestMouseDragCopiesPreviewSelection(t *testing.T) {
	m, _ := mouseModel(t, 2)
	var copied []string
	m.clipboardWrite = func(s string) error { copied = append(copied, s); return nil }
	lines := m.previewLines(m.previewWidth())
	leftClick(m, 52, 4)
	if !m.sel.dragging || m.focus != previewPane {
		t.Fatal("press did not start a preview drag")
	}
	m.Update(tea.MouseMotionMsg{X: 52 + 3, Y: 5, Button: tea.MouseLeft})
	_, cmd := m.Update(tea.MouseReleaseMsg{X: 52 + 3, Y: 5, Button: tea.MouseLeft})
	if cmd == nil || !m.sel.active || m.sel.dragging {
		t.Fatal("release did not finish a selection with a copy command")
	}
	want := strings.TrimRight(ansi.Strip(lines[0]), " ") + "\n" + strings.TrimRight(ansi.Cut(ansi.Strip(lines[1]), 0, 4), " ")
	runBatch(m, cmd)
	if len(copied) != 1 || copied[0] != want {
		t.Fatalf("copied %q, want %q", copied, want)
	}
	if !strings.Contains(m.notice, "Copied selection") {
		t.Fatal(m.notice)
	}
	// The highlight survives a frame; Esc clears it before changing focus.
	_ = m.View()
	press(m, "esc")
	if m.sel.active || m.focus != previewPane {
		t.Fatal("Esc should clear the selection first")
	}
	// Dragging below the pane auto-scrolls the preview.
	leftClick(m, 52, 4+5)
	m.Update(tea.MouseMotionMsg{X: 52, Y: 29, Button: tea.MouseLeft})
	if m.current().preview != 1 || !m.sel.active {
		t.Fatal("drag past the pane edge did not scroll")
	}
	// A plain click (no motion) selects nothing and copies nothing.
	m.Update(tea.MouseReleaseMsg{X: 52, Y: 29, Button: tea.MouseLeft})
	copied = nil
	leftClick(m, 53, 6)
	_, cmd = m.Update(tea.MouseReleaseMsg{X: 53, Y: 6, Button: tea.MouseLeft})
	if cmd != nil || m.sel.active || len(copied) != 0 {
		t.Fatal("plain click copied")
	}
}

func TestKeyboardCopyURLAndBody(t *testing.T) {
	m, _ := mouseModel(t, 1)
	var copied []string
	m.clipboardWrite = func(s string) error { copied = append(copied, s); return nil }
	runBatch(m, press(m, "y"))
	runBatch(m, press(m, "Y"))
	if len(copied) != 2 || copied[0] != "https://example.test/item" || !strings.HasPrefix(copied[1], "body line") {
		t.Fatalf("copied %q", copied)
	}
}

func TestMouseRepoButtonsNeedTwoClicks(t *testing.T) {
	m, _ := mouseModel(t, 1)
	registered := 0
	m.repoRegister = func(context.Context, backend, repoctx.Target) (repoRegistration, error) {
		registered++
		return repoRegistration{}, nil
	}
	m.repoReady, m.repoConfirm, m.overlay = true, true, repoOverlay
	m.repoResult = repoctx.Result{Preferred: 0, Targets: []repoctx.Target{{Kind: "github", Host: "github.com", Scope: "fictional/repo", Remote: "origin"}}}
	lines, hits := m.repoRows(m.width - 2)
	row := -1
	for i, h := range hits {
		if len(h.spans) == 2 {
			row = i
		}
	}
	if row < 0 || row >= len(lines) {
		t.Fatal("no button row")
	}
	add := hits[row].spans[1]
	if leftClick(m, 1+add.from, 4+row) != nil || m.repoButton != 1 {
		t.Fatal("first click should only focus Add + fetch")
	}
	cmd := leftClick(m, 1+add.from, 4+row)
	if cmd == nil || !m.repoPending {
		t.Fatal("second click did not confirm")
	}
	cmd()
	if registered != 1 {
		t.Fatal("registration not invoked exactly once")
	}
}

func TestRenderedMarkdownKeepsOnlySGR(t *testing.T) {
	hostile := "\x1b]8;;https://evil.test\x07click\x1b]8;;\x07\x1b[2J\x1b[31mred\x1b[0m\u009b2J\x1bPdcs\x1b\\ok"
	if got := sgrOnly(hostile); got != "click\x1b[31mred\x1b[0m2Jok" {
		t.Fatalf("sgrOnly = %q", got)
	}
	m, _ := mouseModel(t, 1)
	m.color = true
	body := "## Heading\n\n**bold** and [link](https://example.test/x)\n\n| a | b |\n|---|---|\n| 1 | 2 |"
	first := m.renderBody("ref", body, 60)
	if strings.Contains(strings.Join(first, "\n"), "\x1b]") {
		t.Fatal("renderer output kept an OSC sequence")
	}
	if plain := ansi.Strip(strings.Join(first, "\n")); strings.Contains(plain, "**") || !strings.Contains(plain, "Heading") {
		t.Fatalf("markdown not rendered: %q", plain)
	}
	again := m.renderBody("ref", body, 60)
	if &first[0] != &again[0] {
		t.Fatal("unchanged render was not cached")
	}
	if wider := m.renderBody("ref", body, 70); &wider[0] == &first[0] {
		t.Fatal("width change reused cache")
	}
}

func TestSetupMouseTogglesAndSaveNeedsTwoClicks(t *testing.T) {
	m := setupFixture(t)
	m.draft.Config.Mouse = true
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	setupLoad(m)
	lines, hits := m.frame()
	row := -1
	for i, h := range hits {
		if h.kind == setupHitCandidate {
			row = i
			break
		}
	}
	if row < 0 {
		t.Fatalf("no candidate row in:\n%s", strings.Join(lines, "\n"))
	}
	m.Update(tea.MouseClickMsg{X: setupBoxFrom, Y: row, Button: tea.MouseLeft})
	if len(m.selected) != 1 {
		t.Fatal("checkbox click did not select the candidate")
	}
	setupPress(m, "enter")
	if m.stage != setupReview {
		t.Fatal(m.notice)
	}
	_, hits = m.frame()
	var save hitSpan
	row = -1
	for i, h := range hits {
		if h.kind == setupHitButtons {
			row, save = i, h.spans[2]
		}
	}
	if row < 0 {
		t.Fatal("no review buttons")
	}
	m.Update(tea.MouseClickMsg{X: save.from, Y: row, Button: tea.MouseLeft})
	if m.button != 2 || m.saving {
		t.Fatal("first click on Save must only focus it")
	}
	_, cmd := m.Update(tea.MouseClickMsg{X: save.from, Y: row, Button: tea.MouseLeft})
	if !m.saving || cmd == nil {
		t.Fatal("second click on focused Save did not save")
	}
	setupExecute(m, cmd)
	if !m.finished || !m.result.Changed {
		t.Fatalf("save result %+v", m.result)
	}
	if m.View().MouseMode != tea.MouseModeCellMotion {
		t.Fatal("setup did not request mouse mode")
	}
}
