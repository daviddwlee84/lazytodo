// Package model defines the read-side projection, not a task lifecycle.
package model

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const SchemaVersion = 1

type Origin struct {
	Provider  string `json:"provider"`
	Authority string `json:"authority"`
	Namespace string `json:"namespace"`
	ID        string `json:"id"`
}

func (o Origin) Ref() string {
	b, _ := json.Marshal([]string{o.Provider, o.Authority, o.Namespace, o.ID})
	return "lt1_" + base64.RawURLEncoding.EncodeToString(b)
}

func ParseRef(ref string) (Origin, error) {
	if !strings.HasPrefix(ref, "lt1_") {
		return Origin{}, errors.New("expected a canonical lt1_ reference from query output")
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(ref, "lt1_"))
	if err != nil {
		return Origin{}, errors.New("invalid item reference")
	}
	var p []string
	if json.Unmarshal(b, &p) != nil || len(p) != 4 {
		return Origin{}, errors.New("invalid item reference")
	}
	o := Origin{p[0], p[1], p[2], p[3]}
	if err := o.Validate(); err != nil || o.Ref() != ref {
		return Origin{}, errors.New("invalid item reference")
	}
	return o, nil
}

func (o Origin) Validate() error {
	for _, s := range []string{o.Provider, o.Authority, o.Namespace, o.ID} {
		if strings.TrimSpace(s) == "" || len(s) > 2048 || strings.ContainsAny(s, "\x00\r\n") {
			return errors.New("origin requires nonempty provider, authority, namespace and id")
		}
	}
	return nil
}

type Label struct {
	ID    string `json:"id,omitempty"`
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}
type Actor struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name"`
	Kind string `json:"kind,omitempty"`
}
type Item struct {
	Ref         string     `json:"ref"`
	Origin      Origin     `json:"origin"`
	SourceID    string     `json:"source_id"`
	Scope       string     `json:"scope"`
	DisplayRef  string     `json:"display_ref"`
	Title       string     `json:"title"`
	Body        string     `json:"body,omitempty"`
	URL         string     `json:"url,omitempty"`
	NativeType  string     `json:"native_type"`
	NativeState string     `json:"native_state"`
	State       string     `json:"state"`
	Labels      []Label    `json:"labels"`
	Assignees   []Actor    `json:"assignees"`
	Delegates   []Actor    `json:"delegates"`
	CreatedAt   *time.Time `json:"created_at,omitempty"`
	UpdatedAt   *time.Time `json:"updated_at,omitempty"`
	ObservedAt  time.Time  `json:"observed_at"`
	Pinned      bool       `json:"pinned"`
	Available   bool       `json:"available"`
}

type Snapshot struct {
	SchemaVersion int       `json:"schema_version"`
	Scope         string    `json:"scope"`
	ObservedAt    time.Time `json:"observed_at"`
	Complete      bool      `json:"complete"`
	Items         []Item    `json:"items"`
}

func (s *Snapshot) Validate(now time.Time) error {
	if s.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported snapshot schema_version %d", s.SchemaVersion)
	}
	if !s.Complete {
		return errors.New("incomplete snapshot: retaining previous data")
	}
	if strings.TrimSpace(s.Scope) == "" || s.ObservedAt.IsZero() || s.ObservedAt.After(now.Add(5*time.Minute)) {
		return errors.New("snapshot requires scope and a valid observation time")
	}
	if s.Items == nil {
		return errors.New("snapshot items must be an array, not null or missing")
	}
	seen := map[string]bool{}
	for n := range s.Items {
		i := &s.Items[n]
		if err := i.Origin.Validate(); err != nil {
			return fmt.Errorf("item %d: %w", n, err)
		}
		ref := i.Origin.Ref()
		if i.Ref != "" && i.Ref != ref {
			return fmt.Errorf("item %d: ref disagrees with origin", n)
		}
		if seen[ref] {
			return errors.New("duplicate canonical identity in snapshot")
		}
		seen[ref] = true
		if strings.TrimSpace(i.Title) == "" {
			return fmt.Errorf("item %d: title is required", n)
		}
		if i.State != "open" && i.State != "closed" && i.State != "unknown" {
			return fmt.Errorf("item %d: state must be open, closed or unknown", n)
		}
		if i.Scope != "" && i.Scope != s.Scope {
			return fmt.Errorf("item %d: scope disagrees with snapshot", n)
		}
		i.Ref = ref
		i.Scope = s.Scope
		i.ObservedAt = s.ObservedAt
		i.Available = true
		i.Pinned = false
	}
	return nil
}

type SourceStatus struct {
	ID          string     `json:"id"`
	Kind        string     `json:"kind"`
	Scope       string     `json:"scope"`
	State       string     `json:"state"`
	Complete    bool       `json:"complete"`
	Count       int        `json:"count"`
	Revision    string     `json:"revision,omitempty"`
	ObservedAt  *time.Time `json:"observed_at,omitempty"`
	LastAttempt *time.Time `json:"last_attempt,omitempty"`
	LastSuccess *time.Time `json:"last_success,omitempty"`
	RetryAfter  *time.Time `json:"retry_after,omitempty"`
	Error       string     `json:"error,omitempty"`
}

func Normalize(s string) string { return cases.Fold().String(norm.NFKC.String(s)) }
func Digest(s string) string    { b := sha256.Sum256([]byte(s)); return hex.EncodeToString(b[:]) }

// Plain strips control sequences supplied by providers. Keep Unicode and line breaks.
func Plain(s string) string {
	var out strings.Builder
	for n := 0; n < len(s); {
		if s[n] == 0x1b {
			n++
			if n >= len(s) {
				break
			}
			switch s[n] {
			case '[':
				n++
				for n < len(s) {
					c := s[n]
					n++
					if c >= 0x40 && c <= 0x7e {
						break
					}
				}
			case ']', 'P', '^', '_', 'X':
				n++
				for n < len(s) {
					if s[n] == 7 {
						n++
						break
					}
					if s[n] == 0x1b && n+1 < len(s) && s[n+1] == '\\' {
						n += 2
						break
					}
					n++
				}
			default:
				n++
			}
			continue
		}
		out.WriteByte(s[n])
		n++
	}
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) {
			return -1
		}
		return r
	}, out.String())
}
