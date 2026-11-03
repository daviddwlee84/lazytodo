package source

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"lazytodo/internal/model"
)

// Super Productivity's local REST API is an opt-in desktop (Electron) listener
// on a fixed loopback port. The token is only ever sent to this address.
const spAuthority = "127.0.0.1:3876"
const spBase = "http://" + spAuthority
const spScope = "local"

type spTask struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Notes     *string  `json:"notes"`
	IsDone    *bool    `json:"isDone"`
	ProjectID *string  `json:"projectId"`
	TagIDs    []string `json:"tagIds"`
	ParentID  *string  `json:"parentId"`
	Created   *int64   `json:"created"`
	Updated   *int64   `json:"updated"`
	Modified  *int64   `json:"modified"`
	DoneOn    *int64   `json:"doneOn"`
	WontDoOn  *int64   `json:"wontDoOn"`
}

type spNamed struct {
	ID    string  `json:"id"`
	Title string  `json:"title"`
	Color *string `json:"color"`
}

func spUnavailable() error {
	return failure("unavailable", "Super Productivity desktop app is not running or its local REST API is disabled; previous data retained")
}

func (s *fetchState) superProductivity(ctx context.Context) (model.Snapshot, error) {
	var health struct {
		RendererReady *bool `json:"rendererReady"`
	}
	if err := s.spGet(ctx, "/health", false, &health); err != nil {
		return model.Snapshot{}, err
	}
	if health.RendererReady == nil || !*health.RendererReady {
		return model.Snapshot{}, spUnavailable()
	}
	var projects, tags []*spNamed
	if err := s.spGet(ctx, "/projects", true, &projects); err != nil {
		return model.Snapshot{}, err
	}
	if err := s.spGet(ctx, "/tags", true, &tags); err != nil {
		return model.Snapshot{}, err
	}
	projectTitle, tagByID := map[string]string{}, map[string]*spNamed{}
	for _, p := range projects {
		if p == nil || p.ID == "" || strings.TrimSpace(p.Title) == "" {
			return model.Snapshot{}, invalidResponse()
		}
		projectTitle[p.ID] = p.Title
	}
	for _, t := range tags {
		if t == nil || t.ID == "" || strings.TrimSpace(t.Title) == "" {
			return model.Snapshot{}, invalidResponse()
		}
		tagByID[t.ID] = t
	}
	// One request covers active, done and archived tasks; the API does not page.
	var tasks []*spTask
	if err := s.spGet(ctx, "/tasks?source=all&includeDone=true", true, &tasks); err != nil {
		return model.Snapshot{}, err
	}
	if tasks == nil {
		return model.Snapshot{}, invalidResponse()
	}
	items := make([]model.Item, 0, len(tasks))
	seen := map[string]bool{}
	for _, task := range tasks {
		if task == nil || seen[task.ID] {
			return model.Snapshot{}, invalidResponse()
		}
		seen[task.ID] = true
		item, err := s.spItem(task, projectTitle, tagByID)
		if err != nil {
			return model.Snapshot{}, err
		}
		items = append(items, item)
	}
	return s.snapshot(items), nil
}

func (s *fetchState) spItem(task *spTask, projectTitle map[string]string, tagByID map[string]*spNamed) (model.Item, error) {
	if task.ID == "" || strings.TrimSpace(task.Title) == "" || task.IsDone == nil || task.TagIDs == nil || task.Created == nil || *task.Created <= 0 {
		return model.Item{}, invalidResponse()
	}
	created := time.UnixMilli(*task.Created).UTC()
	item := model.Item{
		Origin: model.Origin{Provider: "super-productivity", Authority: spAuthority, Namespace: "tasks", ID: task.ID},
		Scope:  s.cfg.Scope, DisplayRef: "Super Productivity", Title: task.Title,
		NativeType: "Task", NativeState: "todo", State: "open",
		Labels: []model.Label{}, CreatedAt: &created,
	}
	if task.ParentID != nil && *task.ParentID != "" {
		item.NativeType = "Subtask"
	}
	if task.ProjectID != nil && *task.ProjectID != "" {
		// The project title is display/search context. An unknown project ID
		// is shown as such, never silently dropped.
		if title, ok := projectTitle[*task.ProjectID]; ok {
			item.DisplayRef = title
		} else {
			item.DisplayRef = "project " + *task.ProjectID
		}
	}
	if *task.IsDone {
		item.State, item.NativeState = "closed", "done"
		if task.WontDoOn != nil && task.DoneOn != nil && *task.WontDoOn == *task.DoneOn {
			item.NativeState = "won't do"
		}
	}
	if task.Notes != nil {
		item.Body = *task.Notes
	}
	for _, updated := range []*int64{task.Updated, task.Modified} {
		if updated != nil && *updated > 0 {
			t := time.UnixMilli(*updated).UTC()
			item.UpdatedAt = &t
			break
		}
	}
	for _, id := range task.TagIDs {
		if id == "" {
			return model.Item{}, invalidResponse()
		}
		label := model.Label{ID: id, Name: id}
		if tag, ok := tagByID[id]; ok {
			label.Name = tag.Title
			if tag.Color != nil {
				label.Color = *tag.Color
			}
		}
		item.Labels = append(item.Labels, label)
	}
	// Super Productivity is single-user: assignees/delegates stay unknown (nil).
	return item, nil
}

// spGet reads one fixed path from the loopback API and unwraps {ok,data}.
func (s *fetchState) spGet(ctx context.Context, path string, auth bool, out any) error {
	header := http.Header{}
	header.Set("Accept", "application/json")
	header.Set("User-Agent", "lazytodo")
	if auth {
		header.Set("Authorization", "Bearer "+s.token)
	}
	body, headers, status, err := s.request(ctx, http.MethodGet, spBase+path, header, nil)
	if err != nil {
		return err
	}
	var envelope struct {
		OK    *bool           `json:"ok"`
		Data  json.RawMessage `json:"data"`
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeErr := json.Unmarshal(body, &envelope)
	if status < 200 || status >= 300 {
		code := ""
		if decodeErr == nil && envelope.Error != nil {
			code = envelope.Error.Code
		}
		switch {
		case status == 401 || code == "UNAUTHORIZED":
			return failure("authentication", "Super Productivity rejected the token; copy it from Settings → Misc → Access Token into the token_env variable")
		case code == "NOT_FOUND":
			return failure("unsupported_schema", "Super Productivity does not provide the required local REST API routes; update the desktop app")
		case status == 503 || status == 504:
			return spUnavailable()
		}
		return httpFailure(status, headers)
	}
	if decodeErr != nil || envelope.OK == nil || !*envelope.OK || len(envelope.Data) == 0 {
		return invalidResponse()
	}
	if json.Unmarshal(envelope.Data, out) != nil {
		return invalidResponse()
	}
	return nil
}
