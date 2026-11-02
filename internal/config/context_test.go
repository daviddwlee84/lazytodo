package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepoContextDefaultAndOptOut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	c, e := Load(path, false)
	if e != nil || !c.RepoContext {
		t.Fatal(c, e)
	}
	os.WriteFile(path, []byte("repo_context=false\n"), 0600)
	c, e = Load(path, true)
	if e != nil || c.RepoContext {
		t.Fatal(c, e)
	}
}
func TestUncommentedNativeExamplesRemainDisabled(t *testing.T) {
	parts := strings.Split(Example, "# [[sources]]")
	if len(parts) < 3 {
		t.Fatal("missing native examples")
	}
	for _, part := range parts[1:3] {
		block := "[[sources]]" + strings.ReplaceAll(part, "\n# ", "\n")
		c, e := parseFile([]byte(block))
		if e != nil {
			t.Fatal(e)
		}
		if e = c.Validate(t.TempDir()); e != nil {
			t.Fatal(e)
		}
		if len(c.Sources) != 1 || !c.Sources[0].Disabled || len(c.Active()) != 0 {
			t.Fatal("example activated", c)
		}
	}
}
