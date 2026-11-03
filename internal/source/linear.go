package source

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"lazytodo/internal/model"
)

// linearEndpoint is fixed. Linear is a single hosted authority; lazytodo never
// sends the token anywhere else.
const linearEndpoint = "https://api.linear.app/graphql"
const linearAuthority = "linear.app"

// Complexity is bounded per request: 50 issues x 50 labels stays well below
// Linear's single-query complexity limit.
const linearTeamQuery = `query LazytodoTeam($key: String!) {
  viewer { organization { id urlKey } }
  teams(filter: {key: {eqIgnoreCase: $key}}, first: 2) { nodes { id key } }
}`

const linearIssuesQuery = `query LazytodoIssues($teamId: ID!, $after: String) {
  issues(filter: {team: {id: {eq: $teamId}}}, includeArchived: true, orderBy: createdAt, first: 50, after: $after) {
    nodes {
      id identifier title description url createdAt updatedAt archivedAt trashed
      state { name type }
      labels(first: 50) { nodes { id name color } pageInfo { hasNextPage } }
      assignee { id name displayName app }
      delegate { id name displayName app }
    }
    pageInfo { hasNextPage endCursor }
  }
}`

type linearUser struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	App         bool   `json:"app"`
}

type linearIssue struct {
	ID          string     `json:"id"`
	Identifier  string     `json:"identifier"`
	Title       string     `json:"title"`
	Description *string    `json:"description"`
	URL         string     `json:"url"`
	CreatedAt   *time.Time `json:"createdAt"`
	UpdatedAt   *time.Time `json:"updatedAt"`
	ArchivedAt  *time.Time `json:"archivedAt"`
	Trashed     *bool      `json:"trashed"`
	State       *struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"state"`
	Labels *struct {
		Nodes []*struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Color string `json:"color"`
		} `json:"nodes"`
		PageInfo *pageInfo `json:"pageInfo"`
	} `json:"labels"`
	Assignee *linearUser `json:"assignee"`
	Delegate *linearUser `json:"delegate"`
}

func (s *fetchState) linear(ctx context.Context) (model.Snapshot, error) {
	workspace, teamKey, _ := strings.Cut(s.cfg.Scope, "/")
	var team struct {
		Viewer *struct {
			Organization *struct {
				ID     string `json:"id"`
				URLKey string `json:"urlKey"`
			} `json:"organization"`
		} `json:"viewer"`
		Teams *struct {
			Nodes []*struct {
				ID  string `json:"id"`
				Key string `json:"key"`
			} `json:"nodes"`
		} `json:"teams"`
	}
	if err := s.linearGraphQL(ctx, linearTeamQuery, map[string]any{"key": teamKey}, &team); err != nil {
		return model.Snapshot{}, err
	}
	if team.Viewer == nil || team.Viewer.Organization == nil || team.Viewer.Organization.ID == "" || team.Viewer.Organization.URLKey == "" || team.Teams == nil || team.Teams.Nodes == nil {
		return model.Snapshot{}, invalidResponse()
	}
	org := team.Viewer.Organization
	if !strings.EqualFold(org.URLKey, workspace) {
		return model.Snapshot{}, failure("scope_mismatch", "Linear API key belongs to a different workspace than the configured scope")
	}
	if len(team.Teams.Nodes) != 1 || team.Teams.Nodes[0] == nil || team.Teams.Nodes[0].ID == "" || !strings.EqualFold(team.Teams.Nodes[0].Key, teamKey) {
		return model.Snapshot{}, failure("not_found", "Configured Linear team was not found or is not accessible")
	}
	teamID := team.Teams.Nodes[0].ID

	items := []model.Item{}
	seenCursor, seenID := map[string]bool{}, map[string]bool{}
	var after any
	for {
		var data struct {
			Issues *struct {
				Nodes    []*linearIssue `json:"nodes"`
				PageInfo *pageInfo      `json:"pageInfo"`
			} `json:"issues"`
		}
		if err := s.linearGraphQL(ctx, linearIssuesQuery, map[string]any{"teamId": teamID, "after": after}, &data); err != nil {
			return model.Snapshot{}, err
		}
		if data.Issues == nil || data.Issues.Nodes == nil {
			return model.Snapshot{}, invalidResponse()
		}
		next, err := data.Issues.PageInfo.next()
		if err != nil {
			return model.Snapshot{}, err
		}
		for _, native := range data.Issues.Nodes {
			if native == nil || native.Trashed == nil {
				return model.Snapshot{}, invalidResponse()
			}
			if seenID[native.ID] {
				return model.Snapshot{}, invalidResponse()
			}
			seenID[native.ID] = true
			// Trashed issues are pending deletion, not work items in the team.
			if *native.Trashed {
				continue
			}
			item, err := s.linearItem(org.ID, native)
			if err != nil {
				return model.Snapshot{}, err
			}
			items = append(items, item)
		}
		if next == "" {
			break
		}
		if seenCursor[next] || len(data.Issues.Nodes) == 0 {
			return model.Snapshot{}, invalidResponse()
		}
		seenCursor[next] = true
		after = next
	}
	return s.snapshot(items), nil
}

func (s *fetchState) linearItem(orgID string, native *linearIssue) (model.Item, error) {
	if native.ID == "" || native.Identifier == "" || strings.TrimSpace(native.Title) == "" || native.State == nil || native.State.Name == "" || native.State.Type == "" || native.Labels == nil || native.Labels.Nodes == nil || native.CreatedAt == nil || native.CreatedAt.IsZero() || native.UpdatedAt == nil || native.UpdatedAt.IsZero() {
		return model.Item{}, invalidResponse()
	}
	more, err := native.Labels.PageInfo.next()
	if err != nil {
		return model.Item{}, err
	}
	if more != "" {
		// Truncating labels would silently drop priority/filter hints.
		return model.Item{}, failure("invalid_response", "Linear issue has more labels than lazytodo reads per issue; previous data retained")
	}
	u, err := url.Parse(native.URL)
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, linearAuthority) || u.User != nil {
		return model.Item{}, invalidResponse()
	}
	item := model.Item{
		Origin: model.Origin{Provider: "linear", Authority: linearAuthority, Namespace: orgID, ID: native.ID},
		Scope:  s.cfg.Scope, DisplayRef: native.Identifier, Title: native.Title, URL: u.String(),
		NativeType: "Issue", NativeState: native.State.Name, State: linearState(native.State.Type),
		Labels: []model.Label{}, Assignees: []model.Actor{}, Delegates: []model.Actor{},
		CreatedAt: native.CreatedAt, UpdatedAt: native.UpdatedAt,
	}
	if native.Description != nil {
		item.Body = *native.Description
	}
	for _, label := range native.Labels.Nodes {
		if label == nil || label.ID == "" || label.Name == "" {
			return model.Item{}, invalidResponse()
		}
		item.Labels = append(item.Labels, model.Label{ID: label.ID, Name: label.Name, Color: label.Color})
	}
	// Linear separates human responsibility (assignee) from agent delegation.
	if native.Assignee != nil {
		actor, err := linearActor(native.Assignee)
		if err != nil {
			return model.Item{}, err
		}
		item.Assignees = append(item.Assignees, actor)
	}
	if native.Delegate != nil {
		actor, err := linearActor(native.Delegate)
		if err != nil {
			return model.Item{}, err
		}
		item.Delegates = append(item.Delegates, actor)
	}
	return item, nil
}

func linearActor(u *linearUser) (model.Actor, error) {
	name := u.DisplayName
	if name == "" {
		name = u.Name
	}
	if u.ID == "" || name == "" {
		return model.Actor{}, invalidResponse()
	}
	kind := "User"
	if u.App {
		kind = "App"
	}
	return model.Actor{ID: u.ID, Name: name, Kind: kind}, nil
}

// linearState maps WorkflowState.type; the native state name is preserved.
func linearState(kind string) string {
	switch kind {
	case "triage", "backlog", "unstarted", "started":
		return "open"
	case "completed", "canceled", "duplicate":
		return "closed"
	default:
		return "unknown"
	}
}

func (s *fetchState) linearGraphQL(ctx context.Context, query string, variables map[string]any, out any) error {
	payload, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return invalidResponse()
	}
	header := http.Header{}
	header.Set("Content-Type", "application/json")
	header.Set("Accept", "application/json")
	header.Set("User-Agent", "lazytodo")
	// Personal API keys are sent without a Bearer prefix.
	header.Set("Authorization", s.token)
	body, headers, status, err := s.request(ctx, http.MethodPost, linearEndpoint, header, payload)
	if err != nil {
		return err
	}
	var envelope struct {
		Data   json.RawMessage   `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	decodeErr := json.Unmarshal(body, &envelope)
	if decodeErr == nil && len(envelope.Errors) > 0 {
		return linearFailure(status, headers, envelope.Errors)
	}
	if status < 200 || status >= 300 {
		return linearHTTPFailure(status, headers)
	}
	if decodeErr != nil || len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return invalidResponse()
	}
	if json.Unmarshal(envelope.Data, out) != nil {
		return invalidResponse()
	}
	return nil
}

func linearHTTPFailure(status int, headers http.Header) error {
	e, _ := httpFailure(status, headers).(*FetchError)
	switch e.Code {
	case "authentication":
		e.Message = "Linear authentication failed; check the API key in the token_env variable"
	case "access_denied":
		e.Message = "Linear access denied; verify the API key can read the configured team"
	case "rate_limited":
		linearRetry(e, headers)
	}
	return e
}

// linearFailure classifies GraphQL errors by code only. Provider messages may
// contain private titles or queries and are never echoed.
func linearFailure(status int, headers http.Header, providerErrors []json.RawMessage) error {
	for _, raw := range providerErrors {
		var parsed struct {
			Extensions struct {
				Code string `json:"code"`
			} `json:"extensions"`
		}
		_ = json.Unmarshal(raw, &parsed)
		switch strings.ToUpper(parsed.Extensions.Code) {
		case "RATELIMITED":
			e := failure("rate_limited", "Source rate limit reached; retry later")
			linearRetry(e, headers)
			return e
		case "AUTHENTICATION_ERROR":
			return failure("authentication", "Linear authentication failed; check the API key in the token_env variable")
		case "FORBIDDEN":
			return failure("access_denied", "Linear access denied; verify the API key can read the configured team")
		case "GRAPHQL_VALIDATION_FAILED":
			return failure("unsupported_schema", "Linear does not support the required issue schema; previous data retained")
		}
		text := strings.ToLower(string(raw))
		if strings.Contains(text, "cannot query field") || strings.Contains(text, "unknown argument") || strings.Contains(text, "unknown type") {
			return failure("unsupported_schema", "Linear does not support the required issue schema; previous data retained")
		}
	}
	if status == 401 || status == 403 || status == 429 {
		return linearHTTPFailure(status, headers)
	}
	return failure("graphql_error", "Linear returned GraphQL errors; previous data retained")
}

// linearRetry falls back to Linear's millisecond reset headers when no
// Retry-After was provided.
func linearRetry(e *FetchError, headers http.Header) {
	if seconds, err := strconv.ParseInt(headers.Get("Retry-After"), 10, 32); err == nil && seconds >= 0 {
		e.RetryAfter = time.Now().Add(time.Duration(seconds) * time.Second)
		return
	}
	if !e.RetryAfter.IsZero() {
		return
	}
	for _, name := range []string{"X-RateLimit-Requests-Reset", "X-RateLimit-Complexity-Reset"} {
		if ms, err := strconv.ParseInt(headers.Get(name), 10, 64); err == nil && ms > 0 {
			if reset := time.UnixMilli(ms); reset.After(e.RetryAfter) {
				e.RetryAfter = reset
			}
		}
	}
}

// envToken reads only the configured variable, and only during an explicit
// fetch. Query/show/bare commands never reach this path.
func envToken(name, provider string) (string, error) {
	token := strings.TrimSpace(os.Getenv(name))
	if token == "" || strings.ContainsAny(token, "\r\n") {
		return "", failure("auth_missing", provider+" API token is missing; export the variable named by token_env ("+name+")")
	}
	return token, nil
}
