package service

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReloadConfigIsImmutableAndHonorsExplicitFlags(t *testing.T) {
	s, _ := setup(t)
	s.Paths.Config = filepath.Join(t.TempDir(), "config.toml")
	body := []byte("refresh_interval='30m'\ncolor='always'\nrepo_context=false\n[[sources]]\nid='new'\nkind='github'\nscope='owner/repo'\npriority_labels=['urgent']\n")
	os.WriteFile(s.Paths.Config, body, 0600)
	zero, never := "0s", "never"
	s.IntervalOverride = &zero
	s.ColorOverride = &never
	next, e := s.ReloadConfig()
	if e != nil {
		t.Fatal(e)
	}
	if next == s || next.Config.RefreshInterval != "0s" || next.Config.Color != "never" || next.Config.RepoContext || next.Config.Sources[0].ID != "new" {
		t.Fatal(next.Config)
	}
	if s.Config.Sources[0].ID != "a" {
		t.Fatal("mutated running service")
	}
}
func TestReloadCannotMaskInvalidFileWithOverrides(t *testing.T) {
	s, _ := setup(t)
	s.Paths.Config = filepath.Join(t.TempDir(), "config.toml")
	zero := "0s"
	s.IntervalOverride = &zero
	os.WriteFile(s.Paths.Config, []byte("refresh_interval='invalid'\n"), 0600)
	if _, e := s.ReloadConfig(); e == nil {
		t.Fatal("override masked invalid edited file")
	}
	if s.Config.RefreshInterval != "5m" {
		t.Fatal("old settings changed on failure")
	}
}
