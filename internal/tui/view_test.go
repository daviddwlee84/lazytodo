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
