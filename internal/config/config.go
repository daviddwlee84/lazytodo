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
	"lazytodo/internal/search"
)

type Source struct {
	ID              string    `toml:"id" json:"id"`
	Kind            string    `toml:"kind" json:"kind"`
	Host            string    `toml:"host" json:"host,omitempty"`
	Scope           string    `toml:"scope" json:"scope,omitempty"`
	Path            string    `toml:"path" json:"path,omitempty"`
	Disabled        bool      `toml:"disabled" json:"disabled"`
	RefreshInterval string    `toml:"refresh_interval" json:"refresh_interval,omitempty"`
	PriorityLabels  *[]string `toml:"priority_labels,omitempty" json:"priority_labels,omitempty"`
	// TokenEnv names the environment variable holding an API token for sources
	// without a native CLI (Linear). The secret itself never belongs in TOML.
	TokenEnv string `toml:"token_env,omitempty" json:"token_env,omitempty"`
}

// Fingerprint identifies the fetch target independently of the source alias.
// Forge hosts and repository/project paths are case-insensitive, so spellings
// of one repository across configuration files share one cached target.
func (s Source) Fingerprint() string {
	host, scope := s.Host, s.Scope
	if s.Kind != "snapshot" {
		host, scope = strings.TrimSuffix(strings.ToLower(host), "."), strings.ToLower(scope)
	}
	b, _ := json.Marshal([]string{s.Kind, host, scope, s.Path})
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
	RepoContext     bool     `toml:"repo_context" json:"repo_context"`
	RefreshInterval string   `toml:"refresh_interval" json:"refresh_interval"`
	Color           string   `toml:"color" json:"color"`
	Mouse           bool     `toml:"mouse" json:"mouse"`
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

// SelectPath resolves the same file for reads and edits, without parsing it.
func SelectPath(flag string, changed bool) (path string, explicit bool, err error) {
	selected, explicit := flag, changed
	if !changed {
		selected, explicit = os.LookupEnv("LAZYTODO_CONFIG")
	}
	if explicit {
		if selected == "" {
			return "", true, errors.New("explicit config path cannot be empty")
		}
		path, err = filepath.Abs(selected)
		return path, true, err
	}
	paths, err := ResolvePaths()
	return paths.Config, false, err
}

// LoadFile validates the file itself, without environment or flag overrides.
func LoadFile(path string) (Config, error) {
	c, err := readFile(path, true)
	if err != nil {
		return c, err
	}
	return c, c.Validate(filepath.Dir(path))
}

// Load never creates files. explicit=false permits a missing default config.
func Load(path string, explicit bool) (Config, error) {
	return LoadWithOverrides(path, explicit, nil, nil)
}
func readFile(path string, explicit bool) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil && !(os.IsNotExist(err) && !explicit) {
		return Config{}, fmt.Errorf("read configuration: %w", err)
	}
	return parseFile(b)
}
func parseFile(b []byte) (Config, error) {
	c := Config{RepoContext: true, RefreshInterval: "5m", Color: "auto", Mouse: true, Sources: []Source{}, Views: []View{}}
	if len(b) > 0 {
		dec := toml.NewDecoder(strings.NewReader(string(b))).DisallowUnknownFields()
		if err := dec.Decode(&c); err != nil {
			return c, fmt.Errorf("invalid TOML configuration: %w", err)
		}
	}
	return c, nil
}

func LoadWithOverrides(path string, explicit bool, color, interval *string) (Config, error) {
	c, err := readFile(path, explicit)
	if err != nil {
		return c, err
	}
	return effective(c, path, color, interval)
}

// LoadForReload validates the edited file before applying runtime overrides, in
// one read, so flags cannot mask invalid saved content after an editor returns.
func LoadForReload(path string, color, interval *string) (Config, error) {
	c, err := readFile(path, true)
	if err != nil {
		return c, err
	}
	if err = c.Validate(filepath.Dir(path)); err != nil {
		return c, err
	}
	return effective(c, path, color, interval)
}
func effective(c Config, path string, color, interval *string) (Config, error) {
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
var envName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,127}$`)

// LinearScope is "workspace-url-key/TEAMKEY".
var LinearScope = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}/[A-Za-z0-9]{1,16}$`)

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
		if s.PriorityLabels != nil {
			seen := map[string]bool{}
			for _, name := range *s.PriorityLabels {
				if strings.TrimSpace(name) == "" || model.Plain(name) != name || strings.ContainsAny(name, "\n\r\t") || seen[name] {
					return fmt.Errorf("source %s: priority_labels must contain unique, nonempty printable label names", s.ID)
				}
				seen[name] = true
			}
		}
		if s.Kind != "linear" && s.TokenEnv != "" {
			return fmt.Errorf("source %s: token_env applies only to linear sources", s.ID)
		}
		switch s.Kind {
		case "linear":
			if s.Host == "" {
				s.Host = "linear.app"
			}
			s.Host = strings.TrimSuffix(strings.ToLower(s.Host), ".")
			if s.Host != "linear.app" {
				return fmt.Errorf("source %s: linear host must be linear.app", s.ID)
			}
			if !LinearScope.MatchString(s.Scope) {
				return fmt.Errorf("source %s: linear scope must be workspace-url-key/TEAMKEY", s.ID)
			}
			if !envName.MatchString(s.TokenEnv) {
				return fmt.Errorf("source %s: linear requires token_env naming an environment variable (e.g. LINEAR_API_KEY)", s.ID)
			}
			if s.Path != "" {
				return fmt.Errorf("source %s: path applies only to snapshots", s.ID)
			}
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
			return fmt.Errorf("source %s: supported kinds are github, gitlab, linear, snapshot", s.ID)
		}
		if !s.Disabled {
			// Case-insensitive for forges: Owner/Repo and owner/repo would claim
			// the same native identities.
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
		if _, err := search.Parse(v.Query); err != nil {
			return fmt.Errorf("view %s: %w", v.Name, err)
		}
		views[v.Name] = true
	}
	return nil
}

const Example = `# Credentials remain with gh/glab. No source is registered automatically.
refresh_interval = "5m"
color = "auto"
repo_context = true # Read local Git context in the TUI; false disables suggestions.
mouse = true # TUI click/wheel/drag-copy; false (or --mouse=false, or M) keeps native terminal selection.
# Prefer lazytodo init to select real sources. Examples below remain disabled.

# [[sources]]
# id = "work-github"
# kind = "github"
# host = "github.com"
# scope = "owner/repository"
# disabled = true # Replace the scope, then explicitly enable it.
# priority_labels = ["P0", "P1", "urgent"] # Optional exact names; [] disables hints.

# [[sources]]
# id = "work-gitlab"
# kind = "gitlab"
# host = "gitlab.com"
# scope = "group/project"
# disabled = true # Replace the host/scope, then explicitly enable it.

# [[sources]]
# id = "work-linear"
# kind = "linear"
# scope = "workspace-url-key/TEAMKEY"
# token_env = "LINEAR_API_KEY" # Name of the env var holding a personal API key; never the key itself.
# disabled = true # Replace the scope, export the variable, then explicitly enable it.

# [[sources]]
# id = "snapshot"
# kind = "snapshot"
# path = "/absolute/path/to/snapshot.json"

[[views]]
name = "Open"
query = "state:open"
`
