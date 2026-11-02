package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPriorityLabelRulesAndSourceIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	base := "[[sources]]\nid='work'\nkind='github'\nscope='owner/repo'\n"
	os.WriteFile(path, []byte(base), 0600)
	auto, e := LoadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if auto.Sources[0].PriorityLabels != nil {
		t.Fatal("absence is not automatic")
	}
	os.WriteFile(path, []byte(base+"priority_labels=[]\n"), 0600)
	off, e := LoadFile(path)
	if e != nil || off.Sources[0].PriorityLabels == nil || len(*off.Sources[0].PriorityLabels) != 0 {
		t.Fatal(off, e)
	}
	if auto.Sources[0].Fingerprint() != off.Sources[0].Fingerprint() {
		t.Fatal("presentation rules invalidated source cache")
	}
	os.WriteFile(path, []byte(base+"priority_labels=['urgent','urgent']\n"), 0600)
	if _, e = LoadFile(path); e == nil {
		t.Fatal("duplicate accepted")
	}
}
func TestPriorityRulesRoundTripInSourceWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	d, e := ReadSourceDraft(path)
	if e != nil {
		t.Fatal(e)
	}
	off := []string{}
	custom := []string{"urgent"}
	p, e := d.PlanSources([]Source{{Kind: "github", Scope: "owner/auto"}, {Kind: "github", Scope: "owner/off", PriorityLabels: &off}, {Kind: "github", Scope: "owner/custom", PriorityLabels: &custom}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.Save(context.Background()); e != nil {
		t.Fatal(e)
	}
	c, e := LoadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if c.Sources[0].PriorityLabels != nil || c.Sources[1].PriorityLabels == nil || len(*c.Sources[1].PriorityLabels) != 0 || (*c.Sources[2].PriorityLabels)[0] != "urgent" {
		t.Fatal("lost nil/empty/custom semantics", c)
	}
}
