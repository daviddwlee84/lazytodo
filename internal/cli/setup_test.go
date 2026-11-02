package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lazytodo/internal/config"
)

func TestInitRejectsNonTTYAndMachineBeforeWrites(t *testing.T) {
	dir := isolated(t)
	path := filepath.Join(dir, "new/config.toml")
	for _, args := range [][]string{{"init", "--config", path}, {"init", "--config", path, "--json"}} {
		code, out, err := invoke(args...)
		if code != 2 || out != "" || !strings.Contains(err, "requires input/output terminals") {
			t.Fatal(code, out, err)
		}
	}
	if _, e := os.Stat(filepath.Dir(path)); !os.IsNotExist(e) {
		t.Fatal("guard created configuration")
	}
}
func TestSourcesAddIsOfflineAndIdempotent(t *testing.T) {
	dir := isolated(t)
	path := filepath.Join(dir, "new/config.toml")
	t.Setenv("PATH", t.TempDir()) // no gh/glab available or needed
	args := []string{"sources", "add", "--provider", "github", "--scope", "owner/repo", "--scope", "owner/other", "--config", path, "--json"}
	code, out, err := invoke(args...)
	if code != 0 || err != "" {
		t.Fatal(code, out, err)
	}
	var result struct {
		Data config.SourceSave `json:"data"`
	}
	if e := json.Unmarshal([]byte(out), &result); e != nil {
		t.Fatal(e)
	}
	if !result.Data.Changed || len(result.Data.Added) != 2 {
		t.Fatal(result)
	}
	first, _ := os.ReadFile(path)
	code, out, err = invoke(args...)
	if code != 0 || err != "" {
		t.Fatal(code, out, err)
	}
	if e := json.Unmarshal([]byte(out), &result); e != nil {
		t.Fatal(e)
	}
	if result.Data.Changed || len(result.Data.Skipped) != 2 {
		t.Fatal(result)
	}
	second, _ := os.ReadFile(path)
	if string(first) != string(second) {
		t.Fatal("idempotent add rewrote config")
	}
	for _, key := range []string{"XDG_CACHE_HOME", "XDG_DATA_HOME"} {
		if _, e := os.Stat(os.Getenv(key)); !os.IsNotExist(e) {
			t.Fatal("initialized runtime storage", key)
		}
	}
}
func TestSourceSetupInputErrorsBeforeConfigWrite(t *testing.T) {
	dir := isolated(t)
	path := filepath.Join(dir, "new/config.toml")
	tests := [][]string{{"sources", "add"}, {"sources", "add", "--provider", "github"}, {"sources", "add", "--provider", "github", "--scope", "bad"}, {"sources", "discover", "--provider", "unsupported"}, {"sources", "discover", "--provider", "github", "--host", "https://github.com"}, {"sources", "discover", "--provider", "github", "--limit", "0"}}
	for _, args := range tests {
		args = append(args, "--config", path, "--json")
		code, out, err := invoke(args...)
		if code != 2 || out != "" || !json.Valid([]byte(err)) {
			t.Fatal(args, code, out, err)
		}
	}
	if _, e := os.Stat(filepath.Dir(path)); !os.IsNotExist(e) {
		t.Fatal("invalid request wrote config")
	}
}
func TestSourceSetupHelpIsStatic(t *testing.T) {
	dir := isolated(t)
	path := filepath.Join(dir, "bad.toml")
	os.WriteFile(path, []byte("invalid = ["), 0600)
	for _, args := range [][]string{{"init", "--help"}, {"sources", "discover", "--help"}, {"sources", "add", "--help"}} {
		args = append(args, "--config", path)
		code, out, err := invoke(args...)
		if code != 0 || out == "" || err != "" {
			t.Fatal(args, code, out, err)
		}
	}
}
