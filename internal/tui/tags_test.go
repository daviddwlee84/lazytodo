package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"lazytodo/internal/model"
)

func TestTagsRenderAndFilterWithoutChangingSource(t *testing.T) {
	m, f := testModel(t, 1)
	f.items[0].DisplayRef = "owner/repo#1"
	f.items[0].Labels = []model.Label{{Name: "bug", Color: "ff0000"}, {Name: "P1", Color: "#ffff00"}, {Name: `needs "review"`}}
	load(t, m)
	if hints := m.priorityHints(f.items[0]); len(hints) != 1 || hints[0].Name != "P1" {
		t.Fatal(hints)
	}
	line := m.titleWithTags(f.items[0], "Useful title", 60)
	if !strings.Contains(line, "[P1]") || ansi.StringWidth(line) > 60 {
		t.Fatal(line)
	}
	preview := strings.Join(m.previewLines(100), "\n")
	if !strings.Contains(preview, "Priority labels: P1") {
		t.Fatal(preview)
	}
	press(m, "t")
	if m.overlay != tagsOverlay {
		t.Fatal("tags did not open")
	}
	press(m, "G")
	execute(t, m, press(m, "enter"))
	if m.overlay != noOverlay || m.current().filter != `label:"needs \"review\""` {
		t.Fatal(m.current().filter)
	}
	if f.fetches != 0 || f.pins != 0 || f.opens != 0 {
		t.Fatal("filter mutated source")
	}
	if f.items[0].Labels[0].Name != "bug" {
		t.Fatal("changed native label order")
	}
}
func TestTagsRespectPerSourceOverridesAndNil(t *testing.T) {
	m, f := testModel(t, 1)
	off := []string{}
	m.config.Sources[0].PriorityLabels = &off
	f.items[0].Labels = []model.Label{{Name: "P1"}}
	if len(m.priorityHints(f.items[0])) != 0 {
		t.Fatal("ignored source override")
	}
	f.items[0].Labels = []model.Label{}
	load(t, m)
	press(m, "t")
	if m.tagLabels == nil || !strings.Contains(strings.Join(m.tagLines(80), " "), "No labels") {
		t.Fatal("empty became unknown")
	}
	press(m, "esc")
	f.items[0].Labels = nil
	load(t, m)
	press(m, "t")
	if !strings.Contains(strings.Join(m.tagLines(80), " "), "unknown") {
		t.Fatal("unknown became empty")
	}
}
func TestLabelColorsAndWidths(t *testing.T) {
	for _, bad := range []string{"red", "#fff", "+fffff", "#fffxyz", "\x1b[31m"} {
		if _, _, ok := labelColors(bad); ok {
			t.Fatal("accepted color", bad)
		}
	}
	if _, fg, ok := labelColors("#ffffff"); !ok || fg != "#000000" {
		t.Fatal(fg)
	}
	if _, fg, ok := labelColors("000000"); !ok || fg != "#ffffff" {
		t.Fatal(fg)
	}
	m, _ := testModel(t, 0)
	item := model.Item{Labels: []model.Label{{Name: "中文👩🏽‍💻長標籤"}, {Name: "P1"}, {Name: "another"}}}
	for _, width := range []int{8, 12, 24, 80} {
		if got := m.compactTags(item, width); ansi.StringWidth(got) > width {
			t.Fatal(width, got)
		}
	}
}
func TestTagPickerRetainsOriginalItemDuringRefresh(t *testing.T) {
	m, f := testModel(t, 1)
	f.items[0].Labels = []model.Label{{Name: "original"}}
	load(t, m)
	press(m, "t")
	f.items[0].Labels = []model.Label{{Name: "replacement"}}
	load(t, m)
	execute(t, m, press(m, "enter"))
	if m.current().filter != `label:"original"` {
		t.Fatal("retargeted overlay", m.current().filter)
	}
}
