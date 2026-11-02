package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"lazytodo/internal/config"
)

func TestRegisterSourceReturnsNewServiceWithoutFetching(t *testing.T) {
	s, f := setup(t)
	s.Paths.Config = filepath.Join(t.TempDir(), "config.toml")
	s.Config.RefreshInterval = "0s"
	s.Config.Color = "never"
	original := len(s.Config.Sources)
	next, saved, id, e := s.RegisterSource(context.Background(), config.Source{Kind: "github", Host: "github.com", Scope: "owner/project"})
	if e != nil || !saved.Changed || id == "" {
		t.Fatal(saved, id, e)
	}
	if next == s || len(s.Config.Sources) != original || next.Config.RefreshInterval != "0s" || next.Config.Color != "never" {
		t.Fatal("mutated service or lost runtime overrides")
	}
	if f.calls != 0 {
		t.Fatal("registration fetched")
	}
	if _, e = os.Stat(s.Paths.Cache); !os.IsNotExist(e) {
		t.Fatal("registration initialized cache")
	}
	c, e := config.LoadFile(s.Paths.Config)
	if e != nil || len(c.Sources) != 1 || c.Sources[0].ID != id {
		t.Fatal(c, e)
	}
	_, again, same, e := next.RegisterSource(context.Background(), config.Source{Kind: "github", Host: "github.com", Scope: "owner/project"})
	if e != nil || again.Changed || same != id {
		t.Fatal(again, same, e)
	}
}
func TestRegisterSourceNeverEnablesDisabledTarget(t *testing.T) {
	s, f := setup(t)
	s.Paths.Config = filepath.Join(t.TempDir(), "config.toml")
	body := []byte("[[sources]]\nid='off'\nkind='github'\nscope='owner/project'\ndisabled=true\n")
	os.WriteFile(s.Paths.Config, body, 0600)
	_, saved, _, e := s.RegisterSource(context.Background(), config.Source{Kind: "github", Host: "github.com", Scope: "owner/project"})
	if e == nil || saved.Changed || f.calls != 0 {
		t.Fatal(saved, e)
	}
	after, _ := os.ReadFile(s.Paths.Config)
	if string(after) != string(body) {
		t.Fatal("disabled config changed")
	}
}
