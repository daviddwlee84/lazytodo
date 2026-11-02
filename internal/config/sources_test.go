package config

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofrs/flock"
)

func native(kind, host, scope string) Source { return Source{Kind: kind, Host: host, Scope: scope} }
func TestSourcePlanNoWriteBeforeSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new/config.toml")
	d, e := ReadSourceDraft(path)
	if e != nil {
		t.Fatal(e)
	}
	p, e := d.PlanSources([]Source{native("github", "github.com", "owner/repo"), native("gitlab", "gitlab.example.com", "group/sub/project")})
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Added) != 2 {
		t.Fatal(p)
	}
	if _, e = os.Stat(filepath.Dir(path)); !os.IsNotExist(e) {
		t.Fatal("planning created files")
	}
	r, e := p.Save(context.Background())
	if e != nil || !r.Changed {
		t.Fatal(r, e)
	}
	c, e := LoadFile(path)
	if e != nil || len(c.Sources) != 2 {
		t.Fatal(c, e)
	}
	if c.Sources[0].ID == "" || c.Sources[1].Host != "gitlab.example.com" {
		t.Fatal(c)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
}
func TestSourceSavePreservesFileAndSkipsExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := []byte("# keep comments\nrefresh_interval='30m'\n[[sources]]\nid='my-alias'\nkind='github'\nhost='github.com'\nscope='owner/repo'\ndisabled=true\n[[views]]\nname='Mine'\nquery='label:bug'\n")
	os.WriteFile(path, original, 0600)
	d, e := ReadSourceDraft(path)
	if e != nil {
		t.Fatal(e)
	}
	p, e := d.PlanSources([]Source{native("github", "GITHUB.COM", "Owner/Repo"), native("gitlab", "gitlab.com", "group/project")})
	if e != nil || len(p.Added) != 1 || len(p.Skipped) != 1 || !p.Skipped[0].Disabled {
		t.Fatal(p, e)
	}
	r, e := p.Save(context.Background())
	if e != nil || !r.Changed {
		t.Fatal(r, e)
	}
	saved, _ := os.ReadFile(path)
	if !bytes.HasPrefix(saved, original) {
		t.Fatal("existing TOML rewritten")
	}
	c, e := LoadFile(path)
	if e != nil || c.RefreshInterval != "30m" || c.Sources[0].ID != "my-alias" || !c.Sources[0].Disabled || len(c.Views) != 1 {
		t.Fatal(c, e)
	}
	d, e = ReadSourceDraft(path)
	if e != nil {
		t.Fatal(e)
	}
	p, e = d.PlanSources([]Source{native("github", "github.com", "owner/repo")})
	if e != nil {
		t.Fatal(e)
	}
	r, e = p.Save(context.Background())
	if e != nil || r.Changed {
		t.Fatal(r, e)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(saved, after) {
		t.Fatal("no-op rewrote config")
	}
}
func TestSourceSaveDetectsInterveningWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(path, []byte("# initial\n"), 0600)
	d, e := ReadSourceDraft(path)
	if e != nil {
		t.Fatal(e)
	}
	p, e := d.PlanSources([]Source{native("github", "", "owner/repo")})
	if e != nil {
		t.Fatal(e)
	}
	changed := []byte("# someone else's edit\n")
	os.WriteFile(path, changed, 0600)
	_, e = p.Save(context.Background())
	if !errors.Is(e, ErrConfigChanged) {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(changed, after) {
		t.Fatal("clobbered intervening edit")
	}
}
func TestSourceSaveCancellationAndInvalidInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new/config.toml")
	d, e := ReadSourceDraft(path)
	if e != nil {
		t.Fatal(e)
	}
	for _, s := range []Source{native("github", "https://github.com", "o/r"), native("github", "", "missing-owner"), native("gitlab", "", "../project"), {Kind: "snapshot", Path: "/tmp/foo"}} {
		if _, e = d.PlanSources([]Source{s}); e == nil {
			t.Fatal("accepted", s)
		}
	}
	p, e := d.PlanSources([]Source{native("github", "", "o/r")})
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, e = p.Save(ctx)
	if !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Dir(path)); !os.IsNotExist(e) {
		t.Fatal("cancelled save created files")
	}
}
func TestSourceSavePreservesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "managed.toml")
	path := filepath.Join(dir, "config.toml")
	os.WriteFile(target, []byte("# managed\n"), 0600)
	if e := os.Symlink(target, path); e != nil {
		t.Fatal(e)
	}
	d, e := ReadSourceDraft(path)
	if e != nil {
		t.Fatal(e)
	}
	p, e := d.PlanSources([]Source{native("github", "", "o/r")})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.Save(context.Background()); e != nil {
		t.Fatal(e)
	}
	info, _ := os.Lstat(path)
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("replaced symlink")
	}
	b, _ := os.ReadFile(target)
	if !strings.Contains(string(b), "[[sources]]") {
		t.Fatal(string(b))
	}
}
func TestSourceSaveConcurrentWriterAndMalformedConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(path, []byte("# initial\n"), 0600)
	d, e := ReadSourceDraft(path)
	if e != nil {
		t.Fatal(e)
	}
	p, e := d.PlanSources([]Source{native("github", "", "o/r")})
	if e != nil {
		t.Fatal(e)
	}
	lock := flock.New(d.target + ".lock")
	if e = lock.Lock(); e != nil {
		t.Fatal(e)
	}
	_, e = p.Save(context.Background())
	lock.Unlock()
	if e == nil {
		t.Fatal("ignored writer lock")
	}
	os.WriteFile(path, []byte("bad = ["), 0600)
	if _, e = ReadSourceDraft(path); e == nil || !strings.Contains(e.Error(), "config edit") {
		t.Fatal(e)
	}
}
func TestSourcePlanUnsupportedInlineStructureIsNotClobbered(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := []byte("sources = []\n")
	os.WriteFile(path, original, 0600)
	d, e := ReadSourceDraft(path)
	if e != nil {
		t.Fatal(e)
	}
	_, e = d.PlanSources([]Source{native("github", "", "o/r")})
	if e == nil {
		t.Fatal("expected preserved-structure warning")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(original, after) {
		t.Fatal("rewrote inline structure")
	}
}
