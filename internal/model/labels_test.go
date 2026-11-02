package model_test

import (
	"lazytodo/internal/model"
	"lazytodo/internal/search"
	"testing"
)

func TestPriorityLabelsAreConservativeHints(t *testing.T) {
	for _, s := range []string{"P0", "p4", "priority::High", "Priority: P1", "priority/low", "priority-critical"} {
		if !model.PriorityLabel(s, nil) {
			t.Fatal(s)
		}
	}
	for _, s := range []string{"P10", "P1 bug", "urgent", "high", "phase1", "priority::", "priority:", "not-priority:high"} {
		if model.PriorityLabel(s, nil) {
			t.Fatal("guessed priority", s)
		}
	}
	custom := []string{"urgent", "待處理"}
	if !model.PriorityLabel("urgent", &custom) || model.PriorityLabel("Urgent", &custom) || model.PriorityLabel("P1", &custom) {
		t.Fatal("explicit rules not exact")
	}
	off := []string{}
	if model.PriorityLabel("P1", &off) {
		t.Fatal("empty rules did not disable detection")
	}
}
func TestLabelQueryExactRoundTrip(t *testing.T) {
	for _, name := range []string{`needs review`, `priority::high`, `待處理`, `a"b\c`, `j/q`} {
		token, ok := model.LabelQuery(name)
		if !ok {
			t.Fatal(name)
		}
		q, e := search.Parse(token)
		if e != nil || !q.Match(model.Item{Labels: []model.Label{{Name: name}}}) {
			t.Fatal(token, e)
		}
	}
	for _, name := range []string{"", "bad\nname", "escape\x1b[31m", "tab\tname"} {
		if _, ok := model.LabelQuery(name); ok {
			t.Fatal("unsafe input", name)
		}
	}
}
