package tui

import (
	"strings"
	"testing"

	"lazytodo/internal/model"
)

func TestPreviewUnknownCollectionsAndListProvenance(t *testing.T) {
	m, f := testModel(t, 1)
	load(t, m)
	preview := strings.Join(m.previewLines(100), "\n")
	if !strings.Contains(preview, "Labels: unknown") || !strings.Contains(preview, "Assignees: unknown") {
		t.Fatal(preview)
	}
	f.items[0].Labels = []model.Label{}
	f.items[0].Assignees = []model.Actor{}
	load(t, m)
	preview = strings.Join(m.previewLines(100), "\n")
	if !strings.Contains(preview, "Labels: none") || !strings.Contains(preview, "Assignees: none") {
		t.Fatal(preview)
	}
	rows := strings.Join(m.listLines(60), "\n")
	if !strings.Contains(rows, "work") || !strings.Contains(rows, "open") {
		t.Fatal(rows)
	}
}
func TestCompleteEmptyDistinctFromNeverFetched(t *testing.T) {
	m, _ := testModel(t, 0)
	load(t, m)
	m.sources = []model.SourceStatus{{ID: "work", Complete: true, State: "fresh"}}
	text := strings.Join(m.listLines(80), "\n")
	if !strings.Contains(text, "last complete source snapshots") {
		t.Fatal(text)
	}
	m.sources[0].Complete = false
	text = strings.Join(m.listLines(80), "\n")
	if !strings.Contains(text, "No cached items yet") {
		t.Fatal(text)
	}
}

// Esc unwinds selection → preview → search → source scope, one step per press.
func TestEscUnwindsToAllSources(t *testing.T) {
	m, f := testModel(t, 3)
	load(t, m)
	execute(t, m, m.changeScope("work", true))
	if m.scopeSource != "work" || !strings.Contains(m.footer(), "Esc all sources") {
		t.Fatalf("scoped footer: %q", m.footer())
	}
	m.current().filter = "Item"
	m.focus = previewPane
	press(m, "esc")
	if m.focus != listPane || m.scopeSource != "work" {
		t.Fatal("first Esc should only leave the preview")
	}
	if strings.Contains(m.footer(), "all sources") {
		t.Fatal("unscope hint shown while a search filter is active")
	}
	execute(t, m, press(m, "esc"))
	if m.current().filter != "" || m.scopeSource != "work" {
		t.Fatal("second Esc should only clear the search")
	}
	execute(t, m, press(m, "esc"))
	if m.scopeSource != "" || m.scopeName() != "All sources" {
		t.Fatal("third Esc should return to All sources")
	}
	if press(m, "esc") != nil || m.ctx.Err() != nil {
		t.Fatal("Esc at the top level must be inert")
	}
	if f.fetches != 0 {
		t.Fatal("leaving scope fetched")
	}
}

func TestEmptyStatesExplainScopeAndView(t *testing.T) {
	m, _ := testModel(t, 0)
	m.active = len(m.views) - 1 // Open: base query, no user search
	load(t, m)
	text := strings.Join(m.listLines(80), "\n")
	if strings.Contains(text, "search") || !strings.Contains(text, "No items in the Open view.") || !strings.Contains(text, "All includes closed") {
		t.Fatal(text)
	}
	execute(t, m, m.changeScope("work", true))
	text = strings.Join(m.listLines(80), "\n")
	if !strings.Contains(text, "for work") && !strings.Contains(text, "No items in the Open view for") {
		t.Fatal(text)
	}
	if !strings.Contains(text, "Esc (or s → All sources)") || !strings.Contains(text, "r fetches it") {
		t.Fatal(text)
	}
	m.current().filter = "nothing"
	if text = strings.Join(m.listLines(80), "\n"); !strings.Contains(text, "No items match this search.") {
		t.Fatal(text)
	}
}
