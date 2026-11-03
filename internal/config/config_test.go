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

func TestLinearSourceValidation(t *testing.T) {
	ok := Source{ID: "ln", Kind: "linear", Scope: "acme/ENG", TokenEnv: "LINEAR_API_KEY"}
	c := Config{RefreshInterval: "5m", Color: "auto", Sources: []Source{ok}}
	if e := c.Validate(t.TempDir()); e != nil || c.Sources[0].Host != "linear.app" {
		t.Fatal("valid linear source rejected:", e, c.Sources[0].Host)
	}
	for name, s := range map[string]Source{
		"other host":        {ID: "ln", Kind: "linear", Host: "linear.example", Scope: "acme/ENG", TokenEnv: "LINEAR_API_KEY"},
		"team only":         {ID: "ln", Kind: "linear", Scope: "ENG", TokenEnv: "LINEAR_API_KEY"},
		"nested scope":      {ID: "ln", Kind: "linear", Scope: "acme/ENG/x", TokenEnv: "LINEAR_API_KEY"},
		"uppercase urlkey":  {ID: "ln", Kind: "linear", Scope: "Acme/ENG", TokenEnv: "LINEAR_API_KEY"},
		"missing token_env": {ID: "ln", Kind: "linear", Scope: "acme/ENG"},
		"token as value":    {ID: "ln", Kind: "linear", Scope: "acme/ENG", TokenEnv: "lin_api_secret"},
		"path":              {ID: "ln", Kind: "linear", Scope: "acme/ENG", TokenEnv: "LINEAR_API_KEY", Path: "/x"},
		"token_env on gh":   {ID: "gh", Kind: "github", Scope: "o/r", TokenEnv: "GH_TOKEN"},
	} {
		c := Config{RefreshInterval: "5m", Color: "auto", Sources: []Source{s}}
		if e := c.Validate(t.TempDir()); e == nil {
			t.Fatal("accepted", name)
		}
	}
	dup := Config{RefreshInterval: "5m", Color: "auto", Sources: []Source{ok, {ID: "b", Kind: "linear", Scope: "acme/eng", TokenEnv: "OTHER_KEY"}}}
	if e := dup.Validate(t.TempDir()); e == nil {
		t.Fatal("case-variant duplicate linear team accepted")
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(path, []byte("[[sources]]\nid='ln'\nkind='linear'\nscope='acme/ENG'\ntoken_env='LINEAR_API_KEY'\n"), 0600)
	if c, e := Load(path, true); e != nil || c.Sources[0].TokenEnv != "LINEAR_API_KEY" {
		t.Fatal("linear TOML not loaded:", e)
	}
}
