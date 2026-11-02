package tui

import (
	"lazytodo/internal/config"
	"testing"
)

func TestReloadRetargetDoesNotDisplayOldRows(t *testing.T) {
	m, f := testModel(t, 1)
	load(t, m)
	if len(m.current().items) != 1 {
		t.Fatal("fixture")
	}
	cfg := m.config
	cfg.Sources = append([]config.Source(nil), cfg.Sources...)
	cfg.Sources[0].Path = "/different/snapshot.json"
	m.editingConfig = true
	m.editorGeneration = 1
	m.applyReload(configReloadedMsg{generation: 1, backend: f, config: cfg})
	if len(m.current().items) != 0 || m.current().offset != 0 {
		t.Fatal("retarget showed old rows before the new query")
	}
}
