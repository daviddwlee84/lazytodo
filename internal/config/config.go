package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
	"lazytodo/internal/model"
)

type Source struct {
	ID              string `toml:"id" json:"id"`
	Kind            string `toml:"kind" json:"kind"`
	Host            string `toml:"host" json:"host,omitempty"`
	Scope           string `toml:"scope" json:"scope,omitempty"`
	Path            string `toml:"path" json:"path,omitempty"`
	Disabled        bool   `toml:"disabled" json:"disabled"`
	RefreshInterval string `toml:"refresh_interval" json:"refresh_interval,omitempty"`
}

func (s Source) Fingerprint() string {
	b, _ := json.Marshal([]string{s.Kind, s.Host, s.Scope, s.Path})
	return model.Digest(string(b))
}
func (s Source) Interval(fallback time.Duration) time.Duration {
	if s.RefreshInterval != "" {
		d, _ := time.ParseDuration(s.RefreshInterval)
		return d
	}
	return fallback
}

type View struct {
	Name  string `toml:"name" json:"name"`
	Query string `toml:"query" json:"query"`
}
type Config struct {
	RefreshInterval string   `toml:"refresh_interval" json:"refresh_interval"`
	Color           string   `toml:"color" json:"color"`
	Sources         []Source `toml:"sources" json:"sources"`
	Views           []View   `toml:"views" json:"views"`
}

func (c Config) Interval() time.Duration { d, _ := time.ParseDuration(c.RefreshInterval); return d }
func (c Config) Active() []Source {
	var ss []Source
	for _, s := range c.Sources {
		if !s.Disabled {
			ss = append(ss, s)
		}
	}
	return ss
}

type Paths struct {
	Config string `json:"config"`
	Cache  string `json:"cache"`
	Data   string `json:"data"`
	State  string `json:"state"`
}

func ResolvePaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return Paths{}, errors.New("an absolute HOME is required")
	}
	base := func(env, fallback string) string {
		v := os.Getenv(env)
		if filepath.IsAbs(v) {
			return filepath.Join(v, "lazytodo")
		}
		return filepath.Join(home, fallback, "lazytodo")
	}
	return Paths{filepath.Join(base("XDG_CONFIG_HOME", ".config"), "config.toml"), filepath.Join(base("XDG_CACHE_HOME", ".cache"), "cache.sqlite"), filepath.Join(base("XDG_DATA_HOME", ".local/share"), "local.sqlite"), base("XDG_STATE_HOME", ".local/state")}, nil
}

// Load never creates files. explicit=false permits a missing default config.
func Load(path string, explicit bool) (Config, error) {
	return LoadWithOverrides(path, explicit, nil, nil)
}
func LoadWithOverrides(path string, explicit bool, color, interval *string) (Config, error) {
	c := Config{RefreshInterval: "5m", Color: "auto", Sources: []Source{}, Views: []View{}}
	b, err := os.ReadFile(path)
	if err != nil && !(os.IsNotExist(err) && !explicit) {
		return c, fmt.Errorf("read configuration: %w", err)
	}
	if err == nil {
		dec := toml.NewDecoder(strings.NewReader(string(b))).DisallowUnknownFields()
		if err := dec.Decode(&c); err != nil {
			return c, fmt.Errorf("invalid TOML configuration: %w", err)
		}
	}
	if v, ok := os.LookupEnv("LAZYTODO_REFRESH_INTERVAL"); ok {
		c.RefreshInterval = v
	}
	if v, ok := os.LookupEnv("LAZYTODO_COLOR"); ok {
		c.Color = v
	}
	if color != nil {
		c.Color = *color
	}
	if interval != nil {
		c.RefreshInterval = *interval
	}
	return c, c.Validate(filepath.Dir(path))
}

var alias = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
var host = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.-]*(?::[0-9]+)?$`)

func validInterval(v string) bool {
	d, e := time.ParseDuration(v)
	return e == nil && (d == 0 || d >= time.Second)
}
func (c *Config) Validate(base string) error {
	if !validInterval(c.RefreshInterval) {
		return errors.New("refresh_interval must be 0s or a duration of at least 1s")
	}
	if c.Color != "auto" && c.Color != "always" && c.Color != "never" {
		return errors.New("color must be auto, always or never")
	}
	ids, targets := map[string]bool{}, map[string]bool{}
	for n := range c.Sources {
		s := &c.Sources[n]
		if !alias.MatchString(s.ID) || ids[s.ID] {
			return errors.New("source id must be unique and contain only letters, numbers, '-' or '_'")
		}
		ids[s.ID] = true
		if s.RefreshInterval != "" && !validInterval(s.RefreshInterval) {
			return fmt.Errorf("source %s: invalid refresh_interval", s.ID)
		}
		switch s.Kind {
		case "github", "gitlab":
			if s.Host == "" {
				s.Host = s.Kind + ".com"
			}
			s.Host = strings.ToLower(s.Host)
			if !host.MatchString(s.Host) {
				return fmt.Errorf("source %s: host must be a hostname, not a URL", s.ID)
			}
			parts := strings.Split(s.Scope, "/")
			if len(parts) < 2 || (s.Kind == "github" && len(parts) != 2) {
				return fmt.Errorf("source %s: scope must be a full repository/project path", s.ID)
			}
			for _, p := range parts {
				if p == "" || p == "." || p == ".." || strings.ContainsAny(p, "?#\\\x00\r\n{}:") || strings.ContainsAny(p, " \t") {
					return fmt.Errorf("source %s: invalid scope", s.ID)
				}
			}
			if s.Path != "" {
				return fmt.Errorf("source %s: path applies only to snapshots", s.ID)
			}
		case "snapshot":
			if s.Path == "" {
				return fmt.Errorf("source %s: snapshot path is required", s.ID)
			}
			if !filepath.IsAbs(s.Path) {
				s.Path = filepath.Join(base, s.Path)
			}
			s.Path = filepath.Clean(s.Path)
			if s.Host != "" {
				return fmt.Errorf("source %s: snapshot host is declared in its item origins", s.ID)
			}
		default:
			return fmt.Errorf("source %s: supported kinds are github, gitlab, snapshot", s.ID)
		}
		if !s.Disabled {
			key := s.Fingerprint()
			if targets[key] {
				return errors.New("duplicate source target")
			}
			targets[key] = true
		}
	}
	views := map[string]bool{"All": true, "Pinned": true}
	for _, v := range c.Views {
		if strings.TrimSpace(v.Name) == "" || views[v.Name] {
			return errors.New("view names must be nonempty and unique (All and Pinned are reserved)")
		}
		views[v.Name] = true
	}
	return nil
}

const Example = `# Credentials remain with gh/glab. No source is discovered automatically.
refresh_interval = "5m"
color = "auto"

# [[sources]]
# id = "work-github"
# kind = "github"
# host = "github.com"
# scope = "owner/repository"

# [[sources]]
# id = "work-gitlab"
# kind = "gitlab"
# host = "gitlab.com"
# scope = "group/project"

# [[sources]]
# id = "snapshot"
# kind = "snapshot"
# path = "/absolute/path/to/snapshot.json"

[[views]]
name = "Open"
query = "state:open"
`
