package model

import (
	"strings"
	"testing"
	"time"
)

func TestReferences(t *testing.T) {
	o := Origin{"github", "github.com", "issues", "123"}
	p, e := ParseRef(o.Ref())
	if e != nil || p != o {
		t.Fatal(p, e)
	}
	other := o
	other.Authority = "forge.example"
	if o.Ref() == other.Ref() {
		t.Fatal("instance collision")
	}
	if _, e = ParseRef("123"); e == nil {
		t.Fatal("bare ID accepted")
	}
}
func TestPlain(t *testing.T) {
	s := Plain("hello\x1b]52;c;secret\a\x1b[2J中文\nnext\u009b‮")
	if strings.ContainsAny(s, "\x1b\u009b‮") || strings.Contains(s, "secret") || !strings.Contains(s, "中文\nnext") {
		t.Fatal(s)
	}
}
func TestSnapshotValidate(t *testing.T) {
	now := time.Now()
	s := Snapshot{SchemaVersion: 1, Scope: "a/b", ObservedAt: now, Complete: true, Items: []Item{}}
	if e := s.Validate(now); e != nil {
		t.Fatal(e)
	}
	s.Items = nil
	if e := s.Validate(now); e == nil {
		t.Fatal("null items accepted")
	}
}
