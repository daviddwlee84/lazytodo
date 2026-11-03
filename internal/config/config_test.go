package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestXDGAndNoCreation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "relative")
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache-override"))
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	p, e := ResolvePaths()
	if e != nil {
		t.Fatal(e)
	}
	if p.Config != filepath.Join(home, ".config/lazytodo/config.toml") {
		t.Fatal(p)
	}
	if p.Cache != filepath.Join(home, "cache-override/lazytodo/cache.sqlite") {
		t.Fatal(p)
	}
	if _, e = os.Stat(filepath.Dir(p.Cache)); !os.IsNotExist(e) {
		t.Fatal("created directory")
	}
}
func TestPrecedenceAndValidation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if e := os.WriteFile(path, []byte("refresh_interval='10m'\n[[sources]]\nid='file'\nkind='snapshot'\npath='snapshot.json'\n"), 0600); e != nil {
		t.Fatal(e)
	}
	t.Setenv("LAZYTODO_REFRESH_INTERVAL", "bad")
	zero := "0s"
	c, e := LoadWithOverrides(path, true, nil, &zero)
	if e != nil || c.Interval() != 0 || c.Sources[0].Path != filepath.Join(dir, "snapshot.json") {
		t.Fatal(c, e)
	}
	if _, e = Load(path, true); e == nil {
		t.Fatal("invalid env accepted")
	}
	t.Setenv("LAZYTODO_REFRESH_INTERVAL", "5m")
	if _, e = Load(filepath.Join(dir, "missing"), false); e != nil {
		t.Fatal(e)
	}
	if _, e = Load(filepath.Join(dir, "missing"), true); e == nil {
		t.Fatal("explicit missing ignored")
	}
}
func TestDuplicateAndUnknownFields(t *testing.T) {
	c := Config{RefreshInterval: "5m", Color: "auto", Sources: []Source{{ID: "a", Kind: "github", Scope: "o/r"}, {ID: "b", Kind: "github", Scope: "o/r"}}}
	if e := c.Validate(t.TempDir()); e == nil {
		t.Fatal("duplicate accepted")
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(path, []byte("token='secret'\n"), 0600)
	if _, e := Load(path, true); e == nil {
		t.Fatal("unknown secret field accepted")
	}
}

func TestDuplicateTargetIgnoresForgeCase(t *testing.T) {
	c := Config{RefreshInterval: "5m", Color: "auto", Sources: []Source{
		{ID: "a", Kind: "github", Scope: "Fictional/Repo"},
		{ID: "b", Kind: "github", Host: "GitHub.com", Scope: "fictional/repo"},
	}}
	if e := c.Validate(t.TempDir()); e == nil {
		t.Fatal("case-variant duplicate target accepted")
	}
	c.Sources[1].Disabled = true
	if e := c.Validate(t.TempDir()); e != nil {
		t.Fatal("disabled duplicate rejected:", e)
	}
}
