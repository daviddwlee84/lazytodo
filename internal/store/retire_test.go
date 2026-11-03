package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"lazytodo/internal/config"
	"lazytodo/internal/model"
	"lazytodo/internal/search"
)

func fixtureSnapshot(refs ...string) model.Snapshot {
	snap := model.Snapshot{SchemaVersion: model.SchemaVersion, Complete: true}
	for _, ref := range refs {
		snap.Items = append(snap.Items, model.Item{Ref: ref, Title: "fixture " + ref, State: "open"})
	}
	return snap
}

func writeFixture(t *testing.T, path string, s config.Source, active []config.Source, refs ...string) error {
	t.Helper()
	return WriteSnapshot(context.Background(), path, s, fixtureSnapshot(refs...), model.SourceStatus{ID: s.ID, State: "fresh", Complete: true, Count: len(refs)}, active)
}

func readCount(t *testing.T, path string, active ...config.Source) int {
	t.Helper()
	r, e := Read(context.Background(), path, active, search.Query{})
	if e != nil {
		t.Fatal(e)
	}
	return len(r.Items)
}

// Two configuration files share one XDG cache; neither fetch may erase the other's last-good rows.
func TestWriteKeepsSourcesOfOtherConfigurations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.sqlite")
	work := config.Source{ID: "work", Kind: "github", Host: "github.com", Scope: "fictional/work"}
	home := config.Source{ID: "home", Kind: "github", Host: "github.com", Scope: "fictional/home"}
	if e := writeFixture(t, path, work, []config.Source{work}, "w1", "w2"); e != nil {
		t.Fatal(e)
	}
	if e := writeFixture(t, path, home, []config.Source{home}, "h1"); e != nil {
		t.Fatal(e)
	}
	if n := readCount(t, path, work); n != 2 {
		t.Fatalf("work rows erased by another config: %d", n)
	}
	if n := readCount(t, path, home); n != 1 {
		t.Fatalf("home rows: %d", n)
	}
}

func TestWriteRetiresRetargetedAndYieldsUnconfiguredAlias(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.sqlite")
	old := config.Source{ID: "old", Kind: "github", Host: "github.com", Scope: "fictional/repo"}
	if e := writeFixture(t, path, old, []config.Source{old}, "r1"); e != nil {
		t.Fatal(e)
	}
	// Alias rename: the unconfigured old alias yields to the same native identity.
	renamed := old
	renamed.ID = "renamed"
	if e := writeFixture(t, path, renamed, []config.Source{renamed}, "r1"); e != nil {
		t.Fatal("rename blocked by unconfigured alias:", e)
	}
	if n := readCount(t, path, old); n != 0 {
		t.Fatalf("old alias rows kept: %d", n)
	}
	// A configured, still-active source claiming the identity remains an error.
	other := config.Source{ID: "other", Kind: "github", Host: "github.com", Scope: "fictional/other"}
	e := writeFixture(t, path, other, []config.Source{renamed, other}, "r1")
	if e == nil || !strings.Contains(e.Error(), "overlaps") {
		t.Fatal("configured overlap accepted:", e)
	}
	if n := readCount(t, path, renamed); n != 1 {
		t.Fatalf("failed write changed last-good rows: %d", n)
	}
	// Retargeting an active ID retires its old-target rows.
	retargeted := renamed
	retargeted.Scope = "fictional/moved"
	if e := writeFixture(t, path, other, []config.Source{retargeted, other}, "o1"); e != nil {
		t.Fatal(e)
	}
	r, _ := Read(context.Background(), path, []config.Source{renamed}, search.Query{})
	if _, ok := r.Sources["renamed"]; ok {
		t.Fatal("retargeted source status retained")
	}
}
