package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lazytodo/internal/model"
	"lazytodo/internal/service"
)

func isolated(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	for _, v := range []string{"XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"} {
		t.Setenv(v, filepath.Join(dir, v))
	}
	t.Setenv("LAZYTODO_CONFIG", filepath.Join(dir, "absent"))
	return dir
}
func invoke(args ...string) (int, string, string) {
	var out, err bytes.Buffer
	code := Execute(context.Background(), args, strings.NewReader(""), &out, &err)
	return code, out.String(), err.String()
}
func TestStaticPathsWithBrokenConfig(t *testing.T) {
	dir := isolated(t)
	bad := filepath.Join(dir, "bad.toml")
	os.WriteFile(bad, []byte("not valid [toml"), 0600)
	for _, args := range [][]string{{"--help"}, {"version", "--json"}, {"skill", "print"}, {"skill", "print", "next", "--json"}, {"config", "example", "--json"}} {
		args = append(args, "--config", bad)
		code, out, err := invoke(args...)
		if code != 0 || err != "" || out == "" {
			t.Fatalf("%v: %d %q %q", args, code, out, err)
		}
		if strings.Contains(strings.Join(args, " "), "--json") {
			if !json.Valid([]byte(out)) {
				t.Fatal(out)
			}
		}
	}
	if _, e := os.Stat(os.Getenv("XDG_CACHE_HOME")); !os.IsNotExist(e) {
		t.Fatal("static path created cache")
	}
}
func TestMachineErrorsAndNoTUI(t *testing.T) {
	isolated(t)
	for _, args := range [][]string{{"query", "--json", "--typo"}, {"query", "--json", "--limit", "0"}, {"tui", "--json"}, {"--json"}, {"pin", "--json"}, {"skill", "print", "bad", "--json"}} {
		code, out, err := invoke(args...)
		if code != 2 || out != "" || !json.Valid([]byte(err)) {
			t.Fatalf("%v: %d stdout=%q stderr=%q", args, code, out, err)
		}
	}
}
func TestSnapshotEndToEnd(t *testing.T) {
	dir := isolated(t)
	now := time.Now().UTC()
	snap := model.Snapshot{SchemaVersion: 1, Scope: "demo/project", ObservedAt: now, Complete: true, Items: []model.Item{{Origin: model.Origin{Provider: "demo", Authority: "example.invalid", Namespace: "one", ID: "42"}, Title: "登入 issue", Body: "full detail", NativeType: "Issue", NativeState: "opened", State: "open", Labels: []model.Label{{Name: "needs review"}}, Assignees: []model.Actor{}}}}
	b, _ := json.Marshal(snap)
	snapshotPath := filepath.Join(dir, "snapshot.json")
	os.WriteFile(snapshotPath, b, 0600)
	cfg := filepath.Join(dir, "config.toml")
	os.WriteFile(cfg, []byte("refresh_interval='0s'\n[[sources]]\nid='demo'\nkind='snapshot'\npath='snapshot.json'\n"), 0600)
	run := func(args ...string) string {
		t.Helper()
		args = append(args, "--config", cfg, "--json")
		code, out, err := invoke(args...)
		if code != 0 || err != "" || !json.Valid([]byte(out)) {
			t.Fatalf("%v: %d %q %q", args, code, out, err)
		}
		return out
	}
	run("fetch")
	out := run("query", `登入 label:"needs review"`)
	var env struct {
		SchemaVersion int                 `json:"schema_version"`
		Data          service.QueryResult `json:"data"`
	}
	if e := json.Unmarshal([]byte(out), &env); e != nil {
		t.Fatal(e)
	}
	if env.Data.Total != 1 || env.Data.Items[0].Body != "" {
		t.Fatal(env)
	}
	ref := env.Data.Items[0].Ref
	run("pin", "add", ref)
	if !strings.Contains(run("show", ref), "full detail") {
		t.Fatal("missing body")
	}
	run("query", "pinned:true")
	run("sources")
	run("config", "show")
	// Removing the producer proves all read entry points are cache-only.
	os.Remove(snapshotPath)
	run("query")
	run("show", ref)
	run("sources")
	code, out, err := invoke("fetch", "--config", cfg, "--json")
	if code != 1 || !json.Valid([]byte(out)) || !json.Valid([]byte(err)) {
		t.Fatalf("partial contract %d %q %q", code, out, err)
	}
	run("query")
}
func TestExplicitFlagOverridesEnvironment(t *testing.T) {
	dir := isolated(t)
	cfg := filepath.Join(dir, "config.toml")
	os.WriteFile(cfg, []byte("refresh_interval='10m'"), 0600)
	t.Setenv("LAZYTODO_REFRESH_INTERVAL", "bad")
	code, out, err := invoke("config", "show", "--config", cfg, "--refresh-interval", "0s", "--json")
	if code != 0 || err != "" || !strings.Contains(out, `"refresh_interval":"0s"`) {
		t.Fatal(code, out, err)
	}
}
