package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenViewAvailableWithoutConfiguredViews(t *testing.T) {
	dir := isolated(t)
	path := filepath.Join(dir, "config.toml")
	os.WriteFile(path, []byte("repo_context=true\n"), 0600)
	t.Setenv("PATH", t.TempDir()) // CLI queries must not invoke Git or source tools.
	code, out, err := invoke("--config", path, "query", "--view", "Open", "--json")
	if code != 0 || err != "" || !json.Valid([]byte(out)) {
		t.Fatal(code, out, err)
	}
	if _, e := os.Stat(os.Getenv("XDG_CACHE_HOME")); !os.IsNotExist(e) {
		t.Fatal("cache-only command created data")
	}
}
