package source

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"lazytodo/internal/model"
)

// Queries are fixed application text. Scope, IDs and opaque cursors travel only
// as JSON variables on stdin, never through shell interpolation or query text.
const workItemsQuery = `query ReadProjectWorkItems($fullPath: ID!, $after: String) {
 project(fullPath: $fullPath) {
  workItems(first: 100, after: $after, state: all) {
   nodes {
    id iid title state description createdAt updatedAt
    workItemType { name }
    widgets {
     __typename
     ... on WorkItemWidgetLabels {
      labels(first: 100) { nodes { id title color } pageInfo { hasNextPage endCursor } }
     }
     ... on WorkItemWidgetAssignees {
      assignees(first: 100) { nodes { id username } pageInfo { hasNextPage endCursor } }
     }
    }
   }
   pageInfo { hasNextPage endCursor }
  }
 }
}`
const labelsQuery = `query ReadWorkItemLabels($id: WorkItemID!, $after: String) {
 workItem(id: $id) {
  id widgets {
   __typename
   ... on WorkItemWidgetLabels {
    labels(first: 100, after: $after) { nodes { id title color } pageInfo { hasNextPage endCursor } }
   }
  }
 }
}`
const assigneesQuery = `query ReadWorkItemAssignees($id: WorkItemID!, $after: String) {
 workItem(id: $id) {
  id widgets {
   __typename
   ... on WorkItemWidgetAssignees {
    assignees(first: 100, after: $after) { nodes { id username } pageInfo { hasNextPage endCursor } }
   }
  }
 }
}`

type pageInfo struct {
	HasNextPage *bool   `json:"hasNextPage"`
	EndCursor   *string `json:"endCursor"`
}

func (p *pageInfo) next() (string, error) {
	if p == nil || p.HasNextPage == nil {
		return "", invalidResponse()
	}
	if !*p.HasNextPage {
		return "", nil
	}
	if p.EndCursor == nil || *p.EndCursor == "" {
		return "", invalidResponse()
	}
	return *p.EndCursor, nil
}

type gitlabLabel struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Color string `json:"color"`
}
type gitlabActor struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}
type labelConnection struct {
	Nodes    []*gitlabLabel `json:"nodes"`
	PageInfo *pageInfo      `json:"pageInfo"`
}
type actorConnection struct {
	Nodes    []*gitlabActor `json:"nodes"`
	PageInfo *pageInfo      `json:"pageInfo"`
}
type workItemWidget struct {
	Type      string           `json:"__typename"`
	Labels    *labelConnection `json:"labels"`
	Assignees *actorConnection `json:"assignees"`
}
type workItem struct {
	ID           string  `json:"id"`
	IID          string  `json:"iid"`
	Title        string  `json:"title"`
	State        string  `json:"state"`
	Description  *string `json:"description"`
	WorkItemType *struct {
		Name string `json:"name"`
	} `json:"workItemType"`
	Widgets   []*workItemWidget `json:"widgets"`
	CreatedAt *time.Time        `json:"createdAt"`
	UpdatedAt *time.Time        `json:"updatedAt"`
}
type workItemConnection struct {
	Nodes    []*workItem `json:"nodes"`
	PageInfo *pageInfo   `json:"pageInfo"`
}

func (s *fetchState) graphQL(ctx context.Context, query string, variables map[string]any, target any) error {
	input, err := json.Marshal(struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}{query, variables})
	if err != nil {
		return invalidResponse()
	}
	body, _, err := s.api(ctx, "glab", []string{"api", "--hostname", s.cfg.Host, "--method", "POST", "--include", "graphql", "--input", "-"}, input)
	if err != nil {
		return err
	}
	var envelope struct {
		Data   json.RawMessage   `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return invalidResponse()
	}
	if len(envelope.Errors) > 0 {
		return graphQLFailure(envelope.Errors)
	}
	if len(envelope.Data) == 0 || bytes.Equal(bytes.TrimSpace(envelope.Data), []byte("null")) || json.Unmarshal(envelope.Data, target) != nil {
		return invalidResponse()
	}
	return nil
}

func graphQLFailure(providerErrors []json.RawMessage) error {
	// Recognize schema errors only for a safe actionable diagnostic. Never echo
	// messages, which may contain private titles, queries or credentials.
	for _, raw := range providerErrors {
		text := strings.ToLower(string(raw))
		if strings.Contains(text, "undefinedfield") || strings.Contains(text, "unknown type") || strings.Contains(text, "doesn't exist on type") || strings.Contains(text, "unknown argument") || strings.Contains(text, "isn't a defined input type") || strings.Contains(text, "cannot query field") {
			return failure("unsupported_schema", "GitLab does not support the required WorkItems schema; no Issues fallback was used")
		}
	}
	return failure("graphql_error", "GitLab returned GraphQL errors; previous data retained")
}

func (s *fetchState) gitlab(ctx context.Context) (model.Snapshot, error) {
	items := []model.Item{}
	var after any
	seen := map[string]bool{}
	for {
		var data struct {
			Project *struct {
				WorkItems *workItemConnection `json:"workItems"`
			} `json:"project"`
		}
		err := s.graphQL(ctx, workItemsQuery, map[string]any{"fullPath": s.cfg.Scope, "after": after}, &data)
		if err != nil {
			return model.Snapshot{}, err
		}
		if data.Project == nil || data.Project.WorkItems == nil || data.Project.WorkItems.Nodes == nil {
			return model.Snapshot{}, invalidResponse()
		}
		connection := data.Project.WorkItems
		next, err := connection.PageInfo.next()
		if err != nil {
			return model.Snapshot{}, err
		}
		for _, native := range connection.Nodes {
			item, err := s.gitlabItem(ctx, native)
			if err != nil {
				return model.Snapshot{}, err
			}
			items = append(items, item)
		}
		if next == "" {
			break
		}
		if seen[next] || len(connection.Nodes) == 0 {
			return model.Snapshot{}, invalidResponse()
		}
		seen[next] = true
		after = next
	}
	return s.snapshot(items), nil
}

func (s *fetchState) gitlabItem(ctx context.Context, native *workItem) (model.Item, error) {
	if native == nil || !strings.HasPrefix(native.ID, "gid://gitlab/WorkItem/") || !positiveID(strings.TrimPrefix(native.ID, "gid://gitlab/WorkItem/")) || !positiveID(native.IID) || strings.TrimSpace(native.Title) == "" || native.State == "" || native.WorkItemType == nil || native.WorkItemType.Name == "" || native.Widgets == nil || native.CreatedAt == nil || native.CreatedAt.IsZero() || native.UpdatedAt == nil || native.UpdatedAt.IsZero() {
		return model.Item{}, invalidResponse()
	}
	u := url.URL{Scheme: "https", Host: s.cfg.Host, Path: "/" + s.cfg.Scope + "/-/work_items/" + native.IID}
	item := model.Item{
		Origin: model.Origin{Provider: "gitlab", Authority: s.cfg.Host, Namespace: "work_items", ID: native.ID},
		Scope:  s.cfg.Scope, DisplayRef: s.cfg.Scope + "#" + native.IID, Title: native.Title, URL: u.String(),
		NativeType: native.WorkItemType.Name, NativeState: native.State, State: normalizedState(native.State),
		CreatedAt: native.CreatedAt, UpdatedAt: native.UpdatedAt,
	}
	if native.Description != nil {
		item.Body = *native.Description
	}
	labels, assignees, err := findWidgets(native.Widgets, "")
	if err != nil {
		return model.Item{}, err
	}
	if labels != nil {
		item.Labels = []model.Label{}
		if err := s.gitlabLabels(ctx, native.ID, labels, &item); err != nil {
			return model.Item{}, err
		}
	}
	if assignees != nil {
		item.Assignees = []model.Actor{}
		if err := s.gitlabAssignees(ctx, native.ID, assignees, &item); err != nil {
			return model.Item{}, err
		}
	}
	return item, nil
}

func findWidgets(widgets []*workItemWidget, wanted string) (*labelConnection, *actorConnection, error) {
	if widgets == nil {
		return nil, nil, invalidResponse()
	}
	var labels *labelConnection
	var assignees *actorConnection
	for _, widget := range widgets {
		if widget == nil || widget.Type == "" {
			return nil, nil, invalidResponse()
		}
		if wanted != "" && widget.Type != wanted {
			continue
		}
		switch widget.Type {
		case "WorkItemWidgetLabels":
			if labels != nil || widget.Labels == nil {
				return nil, nil, invalidResponse()
			}
			labels = widget.Labels
		case "WorkItemWidgetAssignees":
			if assignees != nil || widget.Assignees == nil {
				return nil, nil, invalidResponse()
			}
			assignees = widget.Assignees
		}
	}
	return labels, assignees, nil
}

func (s *fetchState) nextWidgets(ctx context.Context, id, after, query string) ([]*workItemWidget, error) {
	var data struct {
		WorkItem *struct {
			ID      string            `json:"id"`
			Widgets []*workItemWidget `json:"widgets"`
		} `json:"workItem"`
	}
	if err := s.graphQL(ctx, query, map[string]any{"id": id, "after": after}, &data); err != nil {
		return nil, err
	}
	if data.WorkItem == nil || data.WorkItem.ID != id || data.WorkItem.Widgets == nil {
		return nil, invalidResponse()
	}
	return data.WorkItem.Widgets, nil
}

func (s *fetchState) gitlabLabels(ctx context.Context, id string, connection *labelConnection, item *model.Item) error {
	cursors, ids := map[string]bool{}, map[string]bool{}
	for {
		if connection == nil || connection.Nodes == nil {
			return invalidResponse()
		}
		next, err := connection.PageInfo.next()
		if err != nil {
			return err
		}
		for _, label := range connection.Nodes {
			if label == nil || label.ID == "" || label.Title == "" || ids[label.ID] {
				return invalidResponse()
			}
			ids[label.ID] = true
			item.Labels = append(item.Labels, model.Label{ID: label.ID, Name: label.Title, Color: label.Color})
		}
		if next == "" {
			return nil
		}
		if cursors[next] || len(connection.Nodes) == 0 {
			return invalidResponse()
		}
		cursors[next] = true
		widgets, err := s.nextWidgets(ctx, id, next, labelsQuery)
		if err != nil {
			return err
		}
		connection, _, err = findWidgets(widgets, "WorkItemWidgetLabels")
		if err != nil {
			return err
		}
	}
}

func (s *fetchState) gitlabAssignees(ctx context.Context, id string, connection *actorConnection, item *model.Item) error {
	cursors, ids := map[string]bool{}, map[string]bool{}
	for {
		if connection == nil || connection.Nodes == nil {
			return invalidResponse()
		}
		next, err := connection.PageInfo.next()
		if err != nil {
			return err
		}
		for _, actor := range connection.Nodes {
			if actor == nil || actor.ID == "" || actor.Username == "" || ids[actor.ID] {
				return invalidResponse()
			}
			ids[actor.ID] = true
			item.Assignees = append(item.Assignees, model.Actor{ID: actor.ID, Name: actor.Username, Kind: "User"})
		}
		if next == "" {
			return nil
		}
		if cursors[next] || len(connection.Nodes) == 0 {
			return invalidResponse()
		}
		cursors[next] = true
		widgets, err := s.nextWidgets(ctx, id, next, assigneesQuery)
		if err != nil {
			return err
		}
		_, connection, err = findWidgets(widgets, "WorkItemWidgetAssignees")
		if err != nil {
			return err
		}
	}
}
