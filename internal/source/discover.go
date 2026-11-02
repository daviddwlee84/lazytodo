package source

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"lazytodo/internal/config"
)

const discoveryScanLimit = 2000
const discoveryPageSize = 100
const discoveryTimeout = 45 * time.Second

// Target is an explicitly selected provider authority, never a checkout-derived
// repository. An empty host uses the provider's public host, as in config.Source.
type Target struct {
	Kind string `json:"kind"`
	Host string `json:"host"`
}

// Candidate is native repository/project metadata, not an issue observation.
// Private includes GitLab's internal visibility: anything not public.
type Candidate struct {
	Kind      string    `json:"kind"`
	Host      string    `json:"host"`
	Scope     string    `json:"scope"`
	UpdatedAt time.Time `json:"updated_at"`
	Private   bool      `json:"private"`
}

// Discovery lists eligible memberships, not all accessible or watched projects.
// Scanned counts inspected records, including filtered and duplicate records.
// Truncated marks a bounded or failed read; callers may still use Candidates.
type Discovery struct {
	Target     Target      `json:"target"`
	Account    string      `json:"account"`
	Candidates []Candidate `json:"candidates"`
	Truncated  bool        `json:"truncated"`
	Scanned    int         `json:"scanned"`
	Order      string      `json:"order"`
}

// Discover performs only fixed native metadata GETs. It neither inspects auth
// configuration nor writes sources, and preserves validated choices on failure.
func (f *Fetcher) Discover(ctx context.Context, target Target, limit int) (result Discovery, err error) {
	ctx, cancel := context.WithTimeout(ctx, discoveryTimeout)
	defer cancel()
	result = Discovery{Target: target, Candidates: []Candidate{}}
	defer func() {
		if err == nil {
			err = ctx.Err()
		}
		if err != nil {
			result.Truncated = true
		}
		sort.Slice(result.Candidates, func(i, j int) bool {
			a, b := result.Candidates[i], result.Candidates[j]
			if !a.UpdatedAt.Equal(b.UpdatedAt) {
				return a.UpdatedAt.After(b.UpdatedAt)
			}
			return a.Scope < b.Scope
		})
	}()
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if target.Kind != "github" && target.Kind != "gitlab" {
		return result, failure("unsupported_source", "Discovery supports only GitHub and GitLab")
	}
	if limit < 1 || limit > 1000 {
		return result, failure("invalid_config", "Discovery limit must be between 1 and 1000")
	}
	if target.Host == "" {
		target.Host = target.Kind + ".com"
	}
	target.Host = strings.ToLower(strings.TrimSuffix(target.Host, "."))
	if !hostname.MatchString(target.Host) {
		return result, failure("invalid_config", "Discovery host must be a hostname, not a URL")
	}
	result.Target = target
	runner := f.Runner
	if runner == nil {
		runner = commandRunner{limit: maxBytes}
	}
	state := &fetchState{runner: runner, cfg: config.Source{Kind: target.Kind, Host: target.Host}}
	endpoint := "user/repos?affiliation=owner,collaborator,organization_member&sort=updated&direction=desc&per_page=100&page="
	result.Order = "GitHub repository updated_at"
	if target.Kind == "gitlab" {
		endpoint = "projects?membership=true&archived=false&order_by=last_activity_at&sort=desc&per_page=100&page="
		result.Order = "GitLab project last_activity_at"
	}
	result.Account, err = state.discoveryAccount(ctx)
	if err != nil {
		return result, err
	}
	seen := map[string]int{}
	for page := 1; ; {
		body, headers, err := state.discoveryGET(ctx, endpoint+strconv.Itoa(page))
		if err != nil {
			return result, err
		}
		var records []json.RawMessage
		if json.Unmarshal(body, &records) != nil || records == nil {
			return result, invalidResponse()
		}
		for _, raw := range records {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			result.Scanned++
			candidate, eligible, err := discoveryCandidate(target, raw)
			if err != nil {
				return result, err
			}
			if eligible {
				if index, exists := seen[candidate.Scope]; exists {
					previous := result.Candidates[index]
					if candidate.UpdatedAt.After(previous.UpdatedAt) {
						result.Candidates[index] = candidate
					} else if candidate.UpdatedAt.Equal(previous.UpdatedAt) && candidate.Private {
						result.Candidates[index].Private = true
					}
				} else {
					seen[candidate.Scope] = len(result.Candidates)
					result.Candidates = append(result.Candidates, candidate)
				}
			}
			// A cutoff is deliberately not advertised as a complete inventory,
			// even when it happens to coincide with the last returned record.
			if len(result.Candidates) >= limit || result.Scanned >= discoveryScanLimit {
				result.Truncated = true
				return result, nil
			}
		}
		next, err := githubNext(headers, page)
		if err == nil && target.Kind == "gitlab" {
			next, err = discoveryGitLabNext(headers, page, len(records), next)
		}
		if err != nil {
			return result, err
		}
		if next == 0 {
			return result, nil
		}
		if len(records) == 0 {
			return result, invalidResponse()
		}
		page = next
	}
}

func (s *fetchState) discoveryGET(ctx context.Context, endpoint string) ([]byte, http.Header, error) {
	command := "gh"
	if s.cfg.Kind == "gitlab" {
		command = "glab"
	}
	return s.api(ctx, command, []string{"api", "--hostname", s.cfg.Host, "--method", "GET", "--include", endpoint}, nil)
}

func (s *fetchState) discoveryAccount(ctx context.Context) (string, error) {
	body, _, err := s.discoveryGET(ctx, "user")
	if err != nil {
		return "", err
	}
	var user *struct {
		Login    string `json:"login"`
		Username string `json:"username"`
	}
	if json.Unmarshal(body, &user) != nil || user == nil {
		return "", invalidResponse()
	}
	account := user.Login
	if s.cfg.Kind == "gitlab" {
		account = user.Username
	}
	if account == "" || strings.ContainsAny(account, "/?#\\{}:") || strings.ContainsFunc(account, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) {
		return "", invalidResponse()
	}
	return account, nil
}

func discoveryCandidate(target Target, raw json.RawMessage) (Candidate, bool, error) {
	// Pointers distinguish required booleans/timestamps from absent or null
	// metadata. GitLab issues_enabled is optional across API versions.
	var native *struct {
		ID                json.Number `json:"id"`
		FullName          string      `json:"full_name"`
		PathWithNamespace string      `json:"path_with_namespace"`
		UpdatedAt         *time.Time  `json:"updated_at"`
		LastActivityAt    *time.Time  `json:"last_activity_at"`
		Private           *bool       `json:"private"`
		Visibility        string      `json:"visibility"`
		Archived          *bool       `json:"archived"`
		HasIssues         *bool       `json:"has_issues"`
		IssuesEnabled     *bool       `json:"issues_enabled"`
	}
	if json.Unmarshal(raw, &native) != nil || native == nil || !positiveID(native.ID.String()) || native.Archived == nil {
		return Candidate{}, false, invalidResponse()
	}
	scope, updated := native.FullName, native.UpdatedAt
	private, issues := false, true
	if target.Kind == "github" {
		if native.Private == nil || native.HasIssues == nil {
			return Candidate{}, false, invalidResponse()
		}
		private, issues = *native.Private, *native.HasIssues
	} else {
		scope, updated = native.PathWithNamespace, native.LastActivityAt
		switch native.Visibility {
		case "public":
		case "private", "internal":
			private = true
		default:
			return Candidate{}, false, invalidResponse()
		}
		if native.IssuesEnabled != nil {
			issues = *native.IssuesEnabled
		}
	}
	if !validScope(target.Kind, scope) || updated == nil || updated.IsZero() {
		return Candidate{}, false, invalidResponse()
	}
	return Candidate{Kind: target.Kind, Host: target.Host, Scope: scope, UpdatedAt: updated.UTC(), Private: private}, !*native.Archived && issues, nil
}

// Link contributes only a validated page number. GitLab can omit pagination
// headers on large inventories; a full headerless page is not proof of EOF.
func discoveryGitLabNext(headers http.Header, current, count, linkNext int) (int, error) {
	values, present := headers[http.CanonicalHeaderKey("X-Next-Page")]
	if present {
		if len(values) != 1 {
			return 0, invalidResponse()
		}
		value := strings.TrimSpace(values[0])
		if value != "" {
			next, err := strconv.Atoi(value)
			if err != nil || !positiveID(value) || next <= current || (linkNext != 0 && linkNext != next) {
				return 0, invalidResponse()
			}
			if next > maxPages {
				return 0, failure("page_limit", "Source exceeded the pagination safety limit; previous data retained")
			}
			return next, nil
		}
	}
	if linkNext != 0 {
		return linkNext, nil
	}
	if !present && count >= discoveryPageSize {
		if current >= maxPages {
			return 0, failure("page_limit", "Source exceeded the pagination safety limit; previous data retained")
		}
		return current + 1, nil
	}
	return 0, nil
}
