package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gofrs/flock"
	"github.com/pelletier/go-toml/v2"
	"lazytodo/internal/model"
)

var ErrConfigChanged = errors.New("configuration changed since setup began; restart setup to review the current file")

type SourceDraft struct {
	Path     string
	Config   Config
	original []byte
	exists   bool
	target   string
	mode     os.FileMode
}
type SourcePlan struct {
	Path    string
	Added   []Source
	Skipped []Source
	draft   *SourceDraft
	content []byte
}
type SourceSave struct {
	Path    string   `json:"path"`
	Added   []Source `json:"added"`
	Skipped []Source `json:"skipped"`
	Changed bool     `json:"changed"`
}

// ReadSourceDraft is read-only and ignores environment overrides: setup edits
// the selected file, not a flattened effective configuration.
func ReadSourceDraft(path string) (*SourceDraft, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("configuration path must be absolute")
	}
	target, err := sourceTarget(path)
	if err != nil {
		return nil, err
	}
	original, err := os.ReadFile(target)
	exists := err == nil
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read configuration: %w", err)
	}
	c, err := parseFile(original)
	if err != nil {
		return nil, fmt.Errorf("%w; repair with lazytodo config edit", err)
	}
	if err = c.Validate(filepath.Dir(path)); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w; repair with lazytodo config edit", err)
	}
	mode := os.FileMode(0600)
	if exists {
		info, e := os.Stat(target)
		if e != nil {
			return nil, e
		}
		if !info.Mode().IsRegular() {
			return nil, errors.New("configuration must be a regular file")
		}
		mode = info.Mode().Perm()
	}
	return &SourceDraft{Path: path, Config: c, original: original, exists: exists, target: target, mode: mode}, nil
}

func sourceTarget(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	// Do not replace a dangling symlink with a new configuration file.
	if info, e := os.Lstat(path); e == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("configuration symlink has no existing target; repair it before setup")
	}
	parent := filepath.Dir(path)
	if parent == path {
		return "", err
	}
	resolvedParent, e := sourceTarget(parent)
	if e != nil {
		return "", e
	}
	return filepath.Join(resolvedParent, filepath.Base(path)), nil
}

func SourceKey(s Source) string {
	return s.Kind + "\x00" + strings.TrimSuffix(strings.ToLower(s.Host), ".") + "\x00" + strings.ToLower(s.Scope)
}
func sourceID(s Source) string {
	slug := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, strings.ToLower(s.Scope))
	slug = strings.Trim(slug, "-")
	if len(slug) > 36 {
		slug = strings.TrimRight(slug[:36], "-")
	}
	if slug == "" {
		slug = "source"
	}
	prefix := "gh"
	if s.Kind == "gitlab" {
		prefix = "gl"
	}
	return prefix + "-" + slug + "-" + model.Digest(SourceKey(s))[:8]
}

// PlanSources is pure. It appends native source tables without reserializing
// existing comments/preferences. Already configured targets, even disabled,
// are skipped rather than silently changed or re-enabled.
func (d *SourceDraft) PlanSources(add []Source) (*SourcePlan, error) {
	plan := &SourcePlan{Path: d.Path, Added: []Source{}, Skipped: []Source{}, draft: d}
	known := map[string]Source{}
	for _, s := range d.Config.Sources {
		known[SourceKey(s)] = s
	}
	for _, s := range add {
		if s.Kind != "github" && s.Kind != "gitlab" {
			return nil, errors.New("source setup supports github and gitlab; use config edit for snapshots")
		}
		if s.Host == "" {
			s.Host = s.Kind + ".com"
		}
		s.Host = strings.ToLower(s.Host)
		if s.ID == "" {
			s.ID = sourceID(s)
		}
		check := Config{RefreshInterval: "5m", Color: "auto", Sources: []Source{s}}
		if err := check.Validate(filepath.Dir(d.Path)); err != nil {
			return nil, err
		}
		s = check.Sources[0]
		if existing, ok := known[SourceKey(s)]; ok {
			plan.Skipped = append(plan.Skipped, existing)
			continue
		}
		known[SourceKey(s)] = s
		plan.Added = append(plan.Added, s)
	}
	if len(plan.Added) == 0 {
		return plan, nil
	}
	base := d.original
	if !d.exists {
		base = []byte(Example)
	}
	block, err := toml.Marshal(struct {
		Sources []Source `toml:"sources"`
	}{plan.Added})
	if err != nil {
		return nil, err
	}
	plan.content = append(append(append([]byte(nil), base...), []byte("\n\n# Sources added by lazytodo setup. Credentials stay with gh/glab.\n")...), block...)
	c, err := parseFile(plan.content)
	if err == nil {
		err = c.Validate(filepath.Dir(d.Path))
	}
	if err != nil {
		return nil, fmt.Errorf("cannot append sources while preserving this TOML structure: %w; use config edit", err)
	}
	return plan, nil
}

func (d *SourceDraft) unchanged() error {
	target, e := sourceTarget(d.Path)
	if e != nil {
		return e
	}
	if target != d.target {
		return ErrConfigChanged
	}
	current, e := os.ReadFile(target)
	exists := e == nil
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	if exists != d.exists || !bytes.Equal(current, d.original) {
		return ErrConfigChanged
	}
	return nil
}

// Save commits the reviewed bytes atomically under a cooperative writer lock.
// Direct external editors cannot share this lock; compare again just before rename.
func (p *SourcePlan) Save(ctx context.Context) (SourceSave, error) {
	result := SourceSave{Path: p.Path, Added: p.Added, Skipped: p.Skipped}
	if p.draft == nil {
		return result, errors.New("missing source draft")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := p.draft.unchanged(); err != nil {
		return result, err
	}
	if len(p.Added) == 0 {
		return result, nil
	}
	target := p.draft.target
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return result, err
	}
	lock := flock.New(target+".lock", flock.SetPermissions(0600))
	ok, err := lock.TryLock()
	if err != nil {
		return result, err
	}
	if !ok {
		return result, errors.New("another configuration writer is active; retry setup")
	}
	defer lock.Unlock()
	if err = p.draft.unchanged(); err != nil {
		return result, err
	}
	temp, err := os.CreateTemp(filepath.Dir(target), ".lazytodo-config-*")
	if err != nil {
		return result, err
	}
	defer os.Remove(temp.Name())
	if err = temp.Chmod(p.draft.mode); err != nil {
		temp.Close()
		return result, err
	}
	if _, err = temp.Write(p.content); err != nil {
		temp.Close()
		return result, err
	}
	if err = temp.Sync(); err != nil {
		temp.Close()
		return result, err
	}
	if err = temp.Close(); err != nil {
		return result, err
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if err = p.draft.unchanged(); err != nil {
		return result, err
	}
	if err = os.Rename(temp.Name(), target); err != nil {
		return result, err
	}
	result.Changed = true
	return result, nil
}
