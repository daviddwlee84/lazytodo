package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"lazytodo/internal/config"
	"lazytodo/internal/model"
)

type runnerFunc func(context.Context, string, []string, []byte) (Result, error)

func (f runnerFunc) Run(ctx context.Context, name string, args []string, input []byte) (Result, error) {
	return f(ctx, name, args, input)
}
func response(body string, headers ...string) Result {
	return Result{Stdout: []byte("HTTP/2.0 200 OK\r\n" + strings.Join(headers, "\r\n") + func() string {
		if len(headers) > 0 {
			return "\r\n"
		}
		return ""
	}() + "\r\n" + body)}
}
func encoded(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
func requireFailure(t *testing.T, s model.Snapshot, err error, code string) {
	t.Helper()
	var fetchErr *FetchError
	if !errors.As(err, &fetchErr) || fetchErr.Code != code {
		t.Fatalf("want %s, got %v", code, err)
	}
	if s.Complete || s.Items != nil {
		t.Fatalf("failed fetch leaked a partial snapshot: %#v", s)
	}
	if strings.Contains(err.Error(), "PRIVATE") {
		t.Fatalf("private provider data leaked: %v", err)
	}
}
func ghIssue(id, number int) map[string]any {
	return map[string]any{
		"id": id, "number": number, "title": "Preserve native issue", "body": "PRIVATE body\noriginal", "html_url": fmt.Sprintf("https://github.example/org/repo/issues/%d", number), "state": "open",
		"labels":     []any{map[string]any{"id": 11, "name": "needs review", "color": "ff00ff"}},
		"assignees":  []any{map[string]any{"id": 12, "login": "person", "type": "User"}},
		"created_at": "2024-01-01T10:00:00Z", "updated_at": "2024-01-02T10:00:00Z",
	}
}
func ghConfig() config.Source {
	return config.Source{ID: "gh", Kind: "github", Host: "GitHub.Example", Scope: "org/repo"}
}
func glConfig() config.Source {
	return config.Source{ID: "gl", Kind: "gitlab", Host: "GitLab.Example", Scope: "org/sub/project"}
}
func page(next string) map[string]any {
	var cursor any
	if next != "" {
		cursor = next
	}
	return map[string]any{"hasNextPage": next != "", "endCursor": cursor}
}
func label(id, title string) map[string]any {
	return map[string]any{"id": id, "title": title, "color": "#123456"}
}
func actor(id, username string) map[string]any { return map[string]any{"id": id, "username": username} }
func labelWidget(nodes []any, next string) map[string]any {
	return map[string]any{"__typename": "WorkItemWidgetLabels", "labels": map[string]any{"nodes": nodes, "pageInfo": page(next)}}
}
func assigneeWidget(nodes []any, next string) map[string]any {
	return map[string]any{"__typename": "WorkItemWidgetAssignees", "assignees": map[string]any{"nodes": nodes, "pageInfo": page(next)}}
}
func glItem(id, iid int, widgets ...any) map[string]any {
	if widgets == nil {
		widgets = []any{}
	}
	return map[string]any{"id": fmt.Sprintf("gid://gitlab/WorkItem/%d", id), "iid": fmt.Sprint(iid), "title": "Original task", "state": "CLOSED", "description": "PRIVATE original description", "workItemType": map[string]any{"name": "Task"}, "widgets": widgets, "createdAt": "2024-01-01T10:00:00Z", "updatedAt": "2024-01-02T10:00:00Z"}
}
func projectPage(t *testing.T, nodes []any, next string) string {
	return encoded(t, map[string]any{"data": map[string]any{"project": map[string]any{"workItems": map[string]any{"nodes": nodes, "pageInfo": page(next)}}}})
}
func widgetPage(t *testing.T, id string, widgets ...any) string {
	return encoded(t, map[string]any{"data": map[string]any{"workItem": map[string]any{"id": id, "widgets": widgets}}})
}

func TestGitHubPaginationAndPRExclusion(t *testing.T) {
	calls := 0
	f := &Fetcher{Runner: runnerFunc(func(ctx context.Context, name string, args []string, input []byte) (Result, error) {
		calls++
		want := []string{"api", "--hostname", "github.example", "--method", "GET", "--include", fmt.Sprintf("repos/org/repo/issues?state=all&per_page=100&page=%d", calls)}
		if name != "gh" || !reflect.DeepEqual(args, want) || len(input) != 0 {
			t.Fatalf("unsafe or unexpected request: %s %#v", name, args)
		}
		if calls == 1 {
			return response(encoded(t, []any{ghIssue(9007199254740993, 1), map[string]any{"pull_request": map[string]any{"url": "PRIVATE"}}}), `Link: <https://api.github.example/repos/org/repo/issues?state=all&per_page=100&page=2>; rel="next"`), nil
		}
		if calls == 2 {
			return response(encoded(t, []any{ghIssue(200, 2)}), `Link: <https://api.github.example/repos/org/repo/issues?page=1>; rel="prev"`), nil
		}
		t.Fatal("unexpected extra page")
		return Result{}, nil
	})}
	snapshot, err := f.Fetch(context.Background(), ghConfig())
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || !snapshot.Complete || snapshot.SchemaVersion != 1 || snapshot.Scope != "org/repo" || snapshot.ObservedAt.IsZero() || len(snapshot.Items) != 2 {
		t.Fatalf("bad complete snapshot %#v", snapshot)
	}
	item := snapshot.Items[0]
	if item.Origin != (model.Origin{Provider: "github", Authority: "github.example", Namespace: "issues", ID: "9007199254740993"}) || item.Ref != item.Origin.Ref() {
		t.Fatalf("lost global identity: %#v", item)
	}
	if item.Body != "PRIVATE body\noriginal" || item.URL != "https://github.example/org/repo/issues/1" || item.NativeState != "open" || item.NativeType != "Issue" || item.CreatedAt == nil || item.UpdatedAt == nil || len(item.Labels) != 1 || item.Labels[0].Name != "needs review" || len(item.Assignees) != 1 || item.SourceID != "gh" || !item.Available {
		t.Fatalf("lost native fields: %#v", item)
	}
}

func TestGitHubLaterPageFailure(t *testing.T) {
	calls := 0
	f := &Fetcher{Runner: runnerFunc(func(context.Context, string, []string, []byte) (Result, error) {
		calls++
		if calls == 1 {
			return response(encoded(t, []any{ghIssue(1, 1)}), `Link: <https://github.example/repos/org/repo/issues?page=2>; rel="next"`), nil
		}
		return Result{Stdout: []byte("HTTP/2 403 Forbidden\r\nX-RateLimit-Remaining: 0\r\nX-RateLimit-Reset: 2000000000\r\n\r\nPRIVATE"), Stderr: []byte("PRIVATE token")}, errors.New("PRIVATE command error")
	})}
	snapshot, err := f.Fetch(context.Background(), ghConfig())
	requireFailure(t, snapshot, err, "rate_limited")
	var e *FetchError
	errors.As(err, &e)
	if calls != 2 || !e.RetryTime().Equal(time.Unix(2000000000, 0)) {
		t.Fatalf("retry metadata lost: %#v", e)
	}
}

func TestGitLabItemAndWidgetPagination(t *testing.T) {
	calls := 0
	globalID := "gid://gitlab/WorkItem/123"
	f := &Fetcher{Runner: runnerFunc(func(ctx context.Context, name string, args []string, input []byte) (Result, error) {
		calls++
		want := []string{"api", "--hostname", "gitlab.example", "--method", "POST", "--include", "graphql", "--input", "-"}
		if name != "glab" || !reflect.DeepEqual(args, want) {
			t.Fatalf("unexpected command %s %#v", name, args)
		}
		var request struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if json.Unmarshal(input, &request) != nil || strings.Contains(request.Query, "mutation") || strings.Contains(request.Query, "org/sub/project") || strings.Contains(request.Query, "webUrl") {
			t.Fatalf("unsafe GraphQL payload: %s", input)
		}
		switch calls {
		case 1:
			if request.Query != workItemsQuery || request.Variables["fullPath"] != "org/sub/project" || request.Variables["after"] != nil {
				t.Fatalf("bad initial variables: %#v", request)
			}
			item := glItem(123, 7, labelWidget([]any{label("label-1", "Priority::High")}, "labels-cursor"), assigneeWidget([]any{actor("actor-1", "one")}, "assignees-cursor"))
			return response(projectPage(t, []any{item}, "items-cursor")), nil
		case 2:
			if request.Query != labelsQuery || request.Variables["id"] != globalID || request.Variables["after"] != "labels-cursor" {
				t.Fatalf("bad labels variables: %#v", request)
			}
			return response(widgetPage(t, globalID, labelWidget([]any{label("label-2", "kind:task")}, ""), map[string]any{"__typename": "WorkItemWidgetAssignees"})), nil
		case 3:
			if request.Query != assigneesQuery || request.Variables["id"] != globalID || request.Variables["after"] != "assignees-cursor" {
				t.Fatalf("bad assignee variables: %#v", request)
			}
			return response(widgetPage(t, globalID, assigneeWidget([]any{actor("actor-2", "two")}, ""), map[string]any{"__typename": "WorkItemWidgetLabels"})), nil
		case 4:
			if request.Query != workItemsQuery || request.Variables["fullPath"] != "org/sub/project" || request.Variables["after"] != "items-cursor" {
				t.Fatalf("bad next-page variables: %#v", request)
			}
			return response(projectPage(t, []any{glItem(124, 8)}, "")), nil
		default:
			t.Fatal("unexpected extra page")
			return Result{}, nil
		}
	})}
	snapshot, err := f.Fetch(context.Background(), glConfig())
	if err != nil {
		t.Fatal(err)
	}
	if calls != 4 || !snapshot.Complete || len(snapshot.Items) != 2 {
		t.Fatalf("bad snapshot %#v", snapshot)
	}
	item := snapshot.Items[0]
	if item.Origin != (model.Origin{Provider: "gitlab", Authority: "gitlab.example", Namespace: "work_items", ID: globalID}) || item.Ref != item.Origin.Ref() || item.URL != "https://gitlab.example/org/sub/project/-/work_items/7" {
		t.Fatalf("bad identity or URL %#v", item)
	}
	if len(item.Labels) != 2 || item.Labels[1].Name != "kind:task" || item.Labels[0].Color != "#123456" || len(item.Assignees) != 2 || item.Assignees[1].Name != "two" || item.Body != "PRIVATE original description" || item.NativeType != "Task" || item.NativeState != "CLOSED" || item.State != "closed" {
		t.Fatalf("lost native widget fields %#v", item)
	}
}

func TestGitLabLaterFailures(t *testing.T) {
	for _, kind := range []string{"items", "labels", "assignees"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			f := &Fetcher{Runner: runnerFunc(func(context.Context, string, []string, []byte) (Result, error) {
				calls++
				if calls == 1 {
					item := glItem(1, 1)
					next := "items-cursor"
					if kind == "labels" {
						item = glItem(1, 1, labelWidget([]any{label("l1", "one")}, "more"))
						next = ""
					}
					if kind == "assignees" {
						item = glItem(1, 1, assigneeWidget([]any{actor("a1", "one")}, "more"))
						next = ""
					}
					return response(projectPage(t, []any{item}, next)), nil
				}
				return Result{Stderr: []byte("PRIVATE command failure")}, errors.New("PRIVATE")
			})}
			snapshot, err := f.Fetch(context.Background(), glConfig())
			requireFailure(t, snapshot, err, "command_failed")
			if calls != 2 {
				t.Fatalf("wanted later-page failure, got %d calls", calls)
			}
		})
	}
}

func TestGitLabRejectsPartialErrorsAndUnsupportedSchema(t *testing.T) {
	for _, tc := range []struct{ name, message, code string }{
		{"partial", "PRIVATE lookup error", "graphql_error"},
		{"schema", "Field 'workItems' doesn't exist on type 'Project' PRIVATE", "unsupported_schema"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{"data": map[string]any{"project": map[string]any{"workItems": map[string]any{"nodes": []any{glItem(1, 1)}, "pageInfo": page("")}}}, "errors": []any{map[string]any{"message": tc.message}}}
			calls := 0
			f := &Fetcher{Runner: runnerFunc(func(context.Context, string, []string, []byte) (Result, error) {
				calls++
				return response(encoded(t, body)), nil
			})}
			snapshot, err := f.Fetch(context.Background(), glConfig())
			requireFailure(t, snapshot, err, tc.code)
			if calls != 1 {
				t.Fatal("must not fall back to Issues")
			}
		})
	}
}

func TestGitLabErrorsWithUnsuccessfulCLIExit(t *testing.T) {
	for _, status := range []int{200, 400} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			body := `{"data":null,"errors":[{"message":"PRIVATE field missing","extensions":{"code":"undefinedField"}}]}`
			f := &Fetcher{Runner: runnerFunc(func(context.Context, string, []string, []byte) (Result, error) {
				return Result{Stdout: []byte(fmt.Sprintf("HTTP/2 %d Status\r\n\r\n%s", status, body)), Stderr: []byte("PRIVATE")}, errors.New("PRIVATE")
			})}
			snapshot, err := f.Fetch(context.Background(), glConfig())
			requireFailure(t, snapshot, err, "unsupported_schema")
		})
	}
}

func TestGitLabCollectionKnowledge(t *testing.T) {
	for _, tc := range []struct {
		name              string
		labels, assignees bool
	}{
		{"no widgets", false, false}, {"labels only", true, false}, {"assignees only", false, true}, {"both widgets", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			widgets := []any{}
			if tc.labels {
				widgets = append(widgets, labelWidget([]any{}, ""))
			}
			if tc.assignees {
				widgets = append(widgets, assigneeWidget([]any{}, ""))
			}
			f := &Fetcher{Runner: runnerFunc(func(_ context.Context, _ string, _ []string, input []byte) (Result, error) {
				var request struct {
					Query string `json:"query"`
				}
				if json.Unmarshal(input, &request) != nil || !strings.Contains(request.Query, "state: all") {
					t.Fatal("WorkItems query must explicitly include closed items")
				}
				return response(projectPage(t, []any{glItem(1, 1, widgets...)}, "")), nil
			})}
			snapshot, err := f.Fetch(context.Background(), glConfig())
			if err != nil {
				t.Fatal(err)
			}
			item := snapshot.Items[0]
			if (item.Labels != nil) != tc.labels || (item.Assignees != nil) != tc.assignees || item.Delegates != nil {
				t.Fatalf("unknown collection coerced to empty: %#v", item)
			}
			if len(item.Labels) != 0 || len(item.Assignees) != 0 {
				t.Fatal("fixture connections should be confirmed empty")
			}
		})
	}
}

func TestGitHubEmptyCollectionsAndUnknownDelegates(t *testing.T) {
	issue := ghIssue(1, 1)
	issue["labels"] = []any{}
	issue["assignees"] = []any{}
	f := &Fetcher{Runner: runnerFunc(func(context.Context, string, []string, []byte) (Result, error) {
		return response(encoded(t, []any{issue})), nil
	})}
	snapshot, err := f.Fetch(context.Background(), ghConfig())
	if err != nil {
		t.Fatal(err)
	}
	item := snapshot.Items[0]
	if item.Labels == nil || item.Assignees == nil || len(item.Labels) != 0 || len(item.Assignees) != 0 || item.Delegates != nil {
		t.Fatalf("collection knowledge lost: %#v", item)
	}
}

func TestSnapshotPreservesCollectionKnowledge(t *testing.T) {
	for _, known := range []bool{false, true} {
		t.Run(fmt.Sprintf("known=%v", known), func(t *testing.T) {
			item := model.Item{Origin: model.Origin{Provider: "fixture", Authority: "local", Namespace: "tasks", ID: "1"}, Title: "Imported", NativeType: "task", NativeState: "open", State: "open"}
			if known {
				item.Labels = []model.Label{}
				item.Assignees = []model.Actor{}
				item.Delegates = []model.Actor{}
			}
			original := model.Snapshot{SchemaVersion: 1, Scope: "explicit", ObservedAt: time.Now().UTC(), Complete: true, Items: []model.Item{item}}
			path := filepath.Join(t.TempDir(), "snapshot.json")
			if err := os.WriteFile(path, []byte(encoded(t, original)), 0600); err != nil {
				t.Fatal(err)
			}
			snapshot, err := New().Fetch(context.Background(), config.Source{ID: "file", Kind: "snapshot", Path: path})
			if err != nil {
				t.Fatal(err)
			}
			got := snapshot.Items[0]
			if !reflect.DeepEqual(got.Labels, item.Labels) || !reflect.DeepEqual(got.Assignees, item.Assignees) || !reflect.DeepEqual(got.Delegates, item.Delegates) {
				t.Fatalf("snapshot null/empty distinction lost: %#v", got)
			}
		})
	}
}

func TestMalformedProviderResponses(t *testing.T) {
	for _, kind := range []string{"github", "gitlab"} {
		bodies := []string{"null", "{}", "PRIVATE invalid JSON"}
		if kind == "github" {
			bodies = append(bodies, "[null]", "[{}]")
			for _, field := range []string{"id", "number", "title", "state", "labels", "assignees", "created_at", "updated_at", "html_url"} {
				item := ghIssue(1, 1)
				delete(item, field)
				bodies = append(bodies, encoded(t, []any{item}))
				item = ghIssue(1, 1)
				item[field] = nil
				bodies = append(bodies, encoded(t, []any{item}))
			}
		} else {
			bodies = append(bodies, `{"data":null}`, `{"data":{"project":null}}`, `{"data":{"project":{"workItems":null}}}`, projectPage(t, nil, ""), projectPage(t, []any{nil}, ""))
			for _, field := range []string{"id", "iid", "title", "state", "workItemType", "widgets", "createdAt", "updatedAt"} {
				item := glItem(1, 1)
				delete(item, field)
				bodies = append(bodies, projectPage(t, []any{item}, ""))
				item = glItem(1, 1)
				item[field] = nil
				bodies = append(bodies, projectPage(t, []any{item}, ""))
			}
			bodies = append(bodies, projectPage(t, []any{glItem(1, 1, map[string]any{"__typename": "WorkItemWidgetLabels", "labels": nil})}, ""))
			bodies = append(bodies, projectPage(t, []any{glItem(1, 1, labelWidget(nil, ""))}, ""))
		}
		for n, body := range bodies {
			t.Run(fmt.Sprintf("%s/%d", kind, n), func(t *testing.T) {
				f := &Fetcher{Runner: runnerFunc(func(context.Context, string, []string, []byte) (Result, error) { return response(body), nil })}
				cfg := ghConfig()
				if kind == "gitlab" {
					cfg = glConfig()
				}
				snapshot, err := f.Fetch(context.Background(), cfg)
				requireFailure(t, snapshot, err, "invalid_response")
			})
		}
	}
}

func TestProviderIdentityDoesNotDependOnScope(t *testing.T) {
	for _, kind := range []string{"github", "gitlab"} {
		t.Run(kind, func(t *testing.T) {
			cfg := ghConfig()
			body := encoded(t, []any{ghIssue(123, 1)})
			if kind == "gitlab" {
				cfg = glConfig()
				body = projectPage(t, []any{glItem(123, 1)}, "")
			}
			f := &Fetcher{Runner: runnerFunc(func(context.Context, string, []string, []byte) (Result, error) { return response(body), nil })}
			before, err := f.Fetch(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			cfg.Scope = "other/project"
			after, err := f.Fetch(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			if before.Items[0].Ref != after.Items[0].Ref {
				t.Fatal("canonical identity changed with project scope")
			}
		})
	}
}

func TestEmptySourcesProduceCompleteArrays(t *testing.T) {
	for _, kind := range []string{"github", "gitlab"} {
		t.Run(kind, func(t *testing.T) {
			cfg := ghConfig()
			body := "[]"
			if kind == "gitlab" {
				cfg = glConfig()
				body = projectPage(t, []any{}, "")
			}
			f := &Fetcher{Runner: runnerFunc(func(context.Context, string, []string, []byte) (Result, error) { return response(body), nil })}
			snapshot, err := f.Fetch(context.Background(), cfg)
			if err != nil || !snapshot.Complete || snapshot.Items == nil || len(snapshot.Items) != 0 {
				t.Fatalf("empty source invalid: %#v %v", snapshot, err)
			}
		})
	}
}

func TestPaginationGuards(t *testing.T) {
	for _, link := range []string{`<https://host/path?page=1>; rel="next"`, `<https://host/path?page=wat>; rel="next"`, `broken`, `<https://host/path?page=2>; rel="next", <https://host/path?page=3>; rel="next"`} {
		if _, err := githubNext(http.Header{"Link": []string{link}}, 1); err == nil {
			t.Fatalf("accepted bad link: %s", link)
		}
	}
	for _, p := range []*pageInfo{nil, {}, {HasNextPage: func() *bool { v := true; return &v }()}} {
		if _, err := p.next(); err == nil {
			t.Fatal("accepted missing cursor data")
		}
	}
	calls := 0
	f := &Fetcher{Runner: runnerFunc(func(context.Context, string, []string, []byte) (Result, error) {
		calls++
		return response(projectPage(t, []any{glItem(calls, calls)}, "same-cursor")), nil
	})}
	snapshot, err := f.Fetch(context.Background(), glConfig())
	requireFailure(t, snapshot, err, "invalid_response")
	if calls != 2 {
		t.Fatalf("cursor loop not stopped: %d", calls)
	}
	never := runnerFunc(func(context.Context, string, []string, []byte) (Result, error) {
		t.Fatal("runner must not be called after page limit")
		return Result{}, nil
	})
	s := &fetchState{runner: never, pages: maxPages}
	_, _, err = s.api(context.Background(), "gh", nil, nil)
	var e *FetchError
	if !errors.As(err, &e) || e.Code != "page_limit" {
		t.Fatalf("missing page safety error: %v", err)
	}
	s = &fetchState{bytes: maxBytes, runner: runnerFunc(func(context.Context, string, []string, []byte) (Result, error) { return response("[]"), nil })}
	_, _, err = s.api(context.Background(), "gh", nil, nil)
	if !errors.As(err, &e) || e.Code != "size_limit" {
		t.Fatalf("missing aggregate size safety error: %v", err)
	}
}

func TestSnapshotValidation(t *testing.T) {
	valid := model.Snapshot{SchemaVersion: 1, Scope: "explicit", ObservedAt: time.Now().UTC(), Complete: true, Items: []model.Item{{Origin: model.Origin{Provider: "beads", Authority: "local-fixture", Namespace: "tasks", ID: "global-1"}, Title: "Imported", NativeType: "task", NativeState: "open", State: "open"}}}
	path := filepath.Join(t.TempDir(), "snapshot.json")
	cfg := config.Source{ID: "file", Kind: "snapshot", Path: path, Scope: "explicit"}
	never := runnerFunc(func(context.Context, string, []string, []byte) (Result, error) {
		t.Fatal("snapshot must not run a CLI or auto-export")
		return Result{}, nil
	})
	f := &Fetcher{Runner: never}
	write := func(text string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(encoded(t, valid))
	snapshot, err := f.Fetch(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Items[0].Origin != valid.Items[0].Origin || snapshot.Items[0].SourceID != "file" || snapshot.Items[0].Ref == "" {
		t.Fatalf("snapshot origin not preserved: %#v", snapshot)
	}
	cfg.Scope = ""
	if _, err := f.Fetch(context.Background(), cfg); err != nil {
		t.Fatalf("optional scope rejected: %v", err)
	}
	cfg.Scope = "mismatch"
	snapshot, err = f.Fetch(context.Background(), cfg)
	requireFailure(t, snapshot, err, "scope_mismatch")
	cfg.Scope = "explicit"
	for _, mutate := range []func(*model.Snapshot){
		func(s *model.Snapshot) { s.SchemaVersion = 2 }, func(s *model.Snapshot) { s.Complete = false }, func(s *model.Snapshot) { s.Items = nil }, func(s *model.Snapshot) { s.ObservedAt = time.Time{} }, func(s *model.Snapshot) { s.ObservedAt = time.Now().Add(time.Hour) }, func(s *model.Snapshot) { s.Scope = "" },
	} {
		bad := valid
		mutate(&bad)
		write(encoded(t, bad))
		snapshot, err = f.Fetch(context.Background(), config.Source{Kind: "snapshot", Path: path})
		requireFailure(t, snapshot, err, "invalid_snapshot")
	}
	for _, body := range []string{"null", "{}", "PRIVATE", encoded(t, valid) + " {}", strings.Replace(encoded(t, valid), `"schema_version":1`, `"schema_version":1,"unknown":"PRIVATE"`, 1)} {
		write(body)
		snapshot, err = f.Fetch(context.Background(), cfg)
		requireFailure(t, snapshot, err, "invalid_snapshot")
	}
	if err := os.Truncate(path, maxBytes+1); err != nil {
		t.Fatal(err)
	}
	snapshot, err = f.Fetch(context.Background(), cfg)
	requireFailure(t, snapshot, err, "size_limit")
	cfg.Path = t.TempDir()
	snapshot, err = f.Fetch(context.Background(), cfg)
	requireFailure(t, snapshot, err, "snapshot_read")
	cfg.Path = "relative.json"
	snapshot, err = f.Fetch(context.Background(), cfg)
	requireFailure(t, snapshot, err, "invalid_config")
}

func TestCancellationAndExplicitConfiguration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := &Fetcher{Runner: runnerFunc(func(context.Context, string, []string, []byte) (Result, error) {
		t.Fatal("cancelled fetch started command")
		return Result{}, nil
	})}
	if _, err := f.Fetch(ctx, ghConfig()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	for _, cfg := range []config.Source{
		{Kind: "bd"}, {Kind: "td"}, {Kind: "github", Scope: "repo"}, {Kind: "github", Scope: "{owner}/{repo}"}, {Kind: "gitlab", Scope: "group/project", Host: "https://gitlab.com"}, {Kind: "gitlab", Scope: "group/../project"},
	} {
		snapshot, err := f.Fetch(context.Background(), cfg)
		code := "invalid_config"
		if cfg.Kind == "bd" || cfg.Kind == "td" {
			code = "unsupported_source"
		}
		requireFailure(t, snapshot, err, code)
	}
}
