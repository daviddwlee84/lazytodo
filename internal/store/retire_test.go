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
	// A configuration still listing the old alias resolves the same target.
	if n := readCount(t, path, old); n != 1 {
		t.Fatalf("old alias lost the shared target: %d", n)
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

// work.toml and personal.toml list the same repository under different IDs.
// Alternating refreshes and a failed refresh must never hide either view.
func TestSameTargetInTwoConfigurationsIsShared(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cache.sqlite")
	work := config.Source{ID: "work-repo", Kind: "github", Host: "github.com", Scope: "fictional/shared"}
	home := work
	home.ID = "home-repo"
	check := func(step string) {
		t.Helper()
		for _, s := range []config.Source{work, home} {
			r, e := Read(ctx, path, []config.Source{s}, search.Query{})
			if e != nil || len(r.Items) != 2 {
				t.Fatalf("%s: %s sees %d items (%v)", step, s.ID, len(r.Items), e)
			}
			for _, i := range r.Items {
				if i.SourceID != s.ID {
					t.Fatalf("%s: item labeled %q for %s", step, i.SourceID, s.ID)
				}
			}
			if st, ok := r.Sources[s.ID]; !ok || st.Status.ID != s.ID || !st.Status.Complete {
				t.Fatalf("%s: status for %s = %+v", step, s.ID, st)
			}
			q, _ := search.Parse("source:" + s.ID)
			if r, _ = Read(ctx, path, []config.Source{s}, q); len(r.Items) != 2 {
				t.Fatalf("%s: source filter for %s lost rows", step, s.ID)
			}
		}
	}
	for n, s := range []config.Source{work, home, work, home} {
		if e := writeFixture(t, path, s, []config.Source{s}, "s1", "s2"); e != nil {
			t.Fatal(n, e)
		}
		check("refresh " + s.ID)
	}
	// A failed refresh from the other configuration records status on the
	// shared target without shadowing its last-good snapshot.
	cached, _ := Sources(ctx, path, []config.Source{work})
	failed := cached[work.ID].Status
	failed.Error = "fictional outage"
	if e := WriteStatus(ctx, path, work, failed); e != nil {
		t.Fatal(e)
	}
	check("failed refresh")
	if st, _ := Sources(ctx, path, []config.Source{home}); st[home.ID].Status.Error == "" {
		t.Fatal("shared target status not visible to the other configuration")
	}
	// A different configured target claiming the same identities still fails.
	other := config.Source{ID: "other", Kind: "github", Host: "github.com", Scope: "fictional/other"}
	if e := writeFixture(t, path, other, []config.Source{home, other}, "s1"); e == nil {
		t.Fatal("overlap with an aliased configured target accepted")
	}
	check("rejected overlap")
}

func TestTargetSpellingCaseSharesCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.sqlite")
	upper := config.Source{ID: "a", Kind: "github", Host: "GitHub.com", Scope: "Fictional/Repo"}
	lower := config.Source{ID: "b", Kind: "github", Host: "github.com", Scope: "fictional/repo"}
	if e := writeFixture(t, path, upper, []config.Source{upper}, "r1"); e != nil {
		t.Fatal(e)
	}
	if e := writeFixture(t, path, lower, []config.Source{lower}, "r1"); e != nil {
		t.Fatal(e)
	}
	if readCount(t, path, upper) != 1 || readCount(t, path, lower) != 1 {
		t.Fatal("case spellings of one repository erased each other")
	}
}
