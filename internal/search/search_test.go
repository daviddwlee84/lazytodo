package search

import (
	"lazytodo/internal/model"
	"testing"
)

func TestParseAndMatch(t *testing.T) {
	i := model.Item{Title: "登入 Café 👩🏽‍💻", Body: "Fix retries timeout: error", SourceID: "work", State: "open", Pinned: true, Labels: []model.Label{{Name: "workflow::todo"}, {Name: "needs review"}}}
	for _, text := range []string{`登入 label:"needs review" source:work pinned:true`, `CAFÉ state:open`, `label:workflow::todo`, `"Fix retries"`, `"timeout: error"`} {
		q, e := Parse(text)
		if e != nil || !q.Match(i) {
			t.Fatalf("%q: %v", text, e)
		}
	}
	for _, text := range []string{`wat:x`, `label:`, `"unfinished`, `pinned:yes`, `pinned:true pinned:false`, `state:done`} {
		if _, e := Parse(text); e == nil {
			t.Fatalf("accepted %q", text)
		}
	}
	q, _ := Parse(`label:"Needs Review"`)
	if q.Match(i) {
		t.Fatal("label match lost exact case")
	}
}
