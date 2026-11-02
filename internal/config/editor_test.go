package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestEditorArgs(t *testing.T) {
	for _, tt := range []struct {
		input string
		want  []string
	}{
		{`code --wait`, []string{"code", "--wait"}},
		{`"/Applications/My Editor" --wait 'two words'`, []string{"/Applications/My Editor", "--wait", "two words"}},
		{`vim "$HOME" ';' '$(touch nope)'`, []string{"vim", "$HOME", ";", "$(touch nope)"}},
		{`editor a\ b "a\b" ''`, []string{"editor", "a b", `a\b`, ""}},
	} {
		got, e := editorArgs(tt.input)
		if e != nil || !reflect.DeepEqual(got, tt.want) {
			t.Fatalf("%q: %#v %v", tt.input, got, e)
		}
	}
	for _, value := range []string{"", `"unterminated`, `editor\`, `''`} {
		if _, e := editorArgs(value); e == nil {
			t.Fatalf("accepted %q", value)
		}
	}
}
func TestEditorPrecedence(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	if Editor() != "vi" {
		t.Fatal(Editor())
	}
	t.Setenv("EDITOR", "nano")
	if Editor() != "nano" {
		t.Fatal(Editor())
	}
	t.Setenv("VISUAL", "code --wait")
	if Editor() != "code --wait" {
		t.Fatal(Editor())
	}
}
func TestSelectedPathSharedAndNoCreation(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg"))
	env := filepath.Join(dir, "env.toml")
	flag := filepath.Join(dir, "flag.toml")
	t.Setenv("LAZYTODO_CONFIG", env)
	p, explicit, e := SelectPath(flag, true)
	if e != nil || p != flag || !explicit {
		t.Fatal(p, explicit, e)
	}
	p, explicit, e = SelectPath("", false)
	if e != nil || p != env || !explicit {
		t.Fatal(p, explicit, e)
	}
	if _, _, e = SelectPath("", true); e == nil {
		t.Fatal("empty flag accepted")
	}
	os.Unsetenv("LAZYTODO_CONFIG")
	p, explicit, e = SelectPath("", false)
	if e != nil || p != filepath.Join(dir, "xdg/lazytodo/config.toml") || explicit {
		t.Fatal(p, explicit, e)
	}
	if _, e = os.Stat(filepath.Dir(p)); !os.IsNotExist(e) {
		t.Fatal("created directories")
	}
}

func runEditor(t *testing.T, path, mode string) error {
	t.Helper()
	t.Setenv("LAZYTODO_TEST_EDITOR", mode)
	var out, errOut bytes.Buffer
	editor := fmt.Sprintf("%q -test.run=^TestEditorProcess$ --", os.Args[0])
	return Edit(context.Background(), path, editor, strings.NewReader(""), &out, &errOut)
}
func TestEditorCreatesOnlySelectedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested with spaces", "config.toml")
	if e := runEditor(t, path, "noop"); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(path)
	if e != nil || string(b) != Example {
		t.Fatal(string(b), e)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
}
func TestEditorCanRepairBrokenConfigAndPreservesComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(path, []byte("invalid = ["), 0600)
	t.Setenv("LAZYTODO_REFRESH_INTERVAL", "bad environment override")
	if e := runEditor(t, path, "repair"); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(path)
	expected := "# keep this comment\nrefresh_interval = '0s'\n"
	if string(b) != expected {
		t.Fatal(string(b))
	}
	if e := runEditor(t, path, "noop"); e != nil {
		t.Fatal(e)
	}
	b, _ = os.ReadFile(path)
	if string(b) != expected {
		t.Fatal("rewrote editor content")
	}
}
func TestEditorInvalidChangesRetainedAndNotMaskedByEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	t.Setenv("LAZYTODO_REFRESH_INTERVAL", "5m")
	e := runEditor(t, path, "invalid")
	var invalid *InvalidEdit
	if !errors.As(e, &invalid) {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "not-a-duration") {
		t.Fatal("discarded edit")
	}
	e = runEditor(t, path, "bad-view")
	if !errors.As(e, &invalid) {
		t.Fatal("invalid view accepted", e)
	}
}
func TestEditorFailureKeepsChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	e := runEditor(t, path, "fail")
	if e == nil || !strings.Contains(e.Error(), "editor failed") {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "# changed despite editor failure\n" {
		t.Fatal(string(b))
	}
}
func TestBadEditorOrCancelledContextDoesNotCreateConfig(t *testing.T) {
	for _, editor := range []string{"definitely-no-such-lazytodo-editor", `"unfinished`} {
		dir := t.TempDir()
		path := filepath.Join(dir, "new/config.toml")
		e := Edit(context.Background(), path, editor, nil, &bytes.Buffer{}, &bytes.Buffer{})
		if e == nil {
			t.Fatal("accepted", editor)
		}
		if _, e = os.Stat(filepath.Dir(path)); !os.IsNotExist(e) {
			t.Fatal("created files before editor validation")
		}
	}
	path := filepath.Join(t.TempDir(), "new/config.toml")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e := Edit(ctx, path, "vi", nil, &bytes.Buffer{}, &bytes.Buffer{})
	if !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Dir(path)); !os.IsNotExist(e) {
		t.Fatal("cancelled call created files")
	}
}

func TestEditorProcess(t *testing.T) {
	mode := os.Getenv("LAZYTODO_TEST_EDITOR")
	if mode == "" {
		return
	}
	path := os.Args[len(os.Args)-1]
	var text string
	switch mode {
	case "noop":
		os.Exit(0)
	case "repair":
		text = "# keep this comment\nrefresh_interval = '0s'\n"
	case "invalid":
		text = "refresh_interval = 'not-a-duration'\n"
	case "bad-view":
		text = "[[views]]\nname='Bad'\nquery='unknown:value'\n"
	case "fail":
		text = "# changed despite editor failure\n"
	default:
		os.Exit(5)
	}
	if e := os.WriteFile(path, []byte(text), 0600); e != nil {
		os.Exit(6)
	}
	if mode == "fail" {
		os.Exit(7)
	}
	os.Exit(0)
}
