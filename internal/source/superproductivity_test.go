package source

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"syscall"
	"testing"

	"lazytodo/internal/config"
)

// Fictional token and tasks; no real Super Productivity profile is contacted.
const fakeSPToken = "sp-FICTIONAL-token"

func spConfig() config.Source {
	return config.Source{ID: "sp", Kind: "super-productivity", TokenEnv: "LAZYTODO_TEST_SP_TOKEN"}
}

func spOK(t *testing.T, data any) *http.Response {
	return lnResponse(200, encoded(t, map[string]any{"ok": true, "data": data}))
}

func spTaskFixture(id string) map[string]any {
	return map[string]any{
		"id": id, "title": "Write PRIVATE plan", "notes": "PRIVATE notes", "isDone": false, "projectId": "proj-1",
		"tagIds": []any{"tag-1", "tag-gone"}, "parentId": nil, "subTaskIds": []any{}, "created": 1704103200000, "updated": 1704189600000,
		"timeEstimate": 0, "timeSpent": 0,
	}
}

// spFetcher routes fixed paths to fixtures, asserting loopback-only, authenticated reads.
func spFetcher(t *testing.T, routes map[string]*http.Response) (*Fetcher, *[]string) {
	t.Helper()
	t.Setenv("LAZYTODO_TEST_SP_TOKEN", fakeSPToken)
	var seen []string
	return &Fetcher{HTTP: httpFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Scheme != "http" || r.URL.Host != "127.0.0.1:3876" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL)
		}
		path := r.URL.RequestURI()
		seen = append(seen, path)
		auth := r.Header.Get("Authorization")
		if path == "/health" && auth != "" {
			t.Fatal("token sent to the unauthenticated health route")
		}
		if path != "/health" && auth != "Bearer "+fakeSPToken {
			t.Fatalf("missing bearer token on %s", path)
		}
		resp, ok := routes[path]
		if !ok {
			t.Fatalf("unexpected path %s", path)
		}
		return resp, nil
	})}, &seen
}

func spRoutes(t *testing.T, tasks ...any) map[string]*http.Response {
	return map[string]*http.Response{
		"/health":                            spOK(t, map[string]any{"server": "up", "rendererReady": true}),
		"/projects":                          spOK(t, []any{map[string]any{"id": "proj-1", "title": "Home"}}),
		"/tags":                              spOK(t, []any{map[string]any{"id": "tag-1", "title": "errand", "color": "#00ff00"}}),
		"/tasks?source=all&includeDone=true": spOK(t, tasks),
	}
}

func TestSuperProductivityTasks(t *testing.T) {
	done := spTaskFixture("t2")
	done["isDone"], done["doneOn"], done["wontDoOn"], done["parentId"], done["projectId"] = true, 1704200000000, 1704200000000, "t1", nil
	delete(done, "updated")
	done["modified"] = 1704300000000
	f, seen := spFetcher(t, spRoutes(t, spTaskFixture("t1"), done))
	snapshot, err := f.Fetch(context.Background(), spConfig())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(*seen, " ") != "/health /projects /tags /tasks?source=all&includeDone=true" {
		t.Fatalf("request order: %v", *seen)
	}
	if len(snapshot.Items) != 2 || snapshot.Scope != "local" || !snapshot.Complete {
		t.Fatalf("snapshot: %#v", snapshot)
	}
	a, b := snapshot.Items[0], snapshot.Items[1]
	if a.Origin.Provider != "super-productivity" || a.Origin.Authority != "127.0.0.1:3876" || a.Origin.ID != "t1" || a.DisplayRef != "Home" || a.URL != "" {
		t.Fatalf("identity: %#v", a)
	}
	if a.State != "open" || a.NativeState != "todo" || b.State != "closed" || b.NativeState != "won't do" || b.NativeType != "Subtask" || b.DisplayRef != "Super Productivity" {
		t.Fatalf("state mapping: %#v %#v", a, b)
	}
	if len(a.Labels) != 2 || a.Labels[0].Name != "errand" || a.Labels[0].Color != "#00ff00" || a.Labels[1].Name != "tag-gone" {
		t.Fatalf("labels must keep unknown tag IDs: %#v", a.Labels)
	}
	if a.Assignees != nil || a.Delegates != nil {
		t.Fatalf("single-user source must not invent actors: %#v", a)
	}
	if a.CreatedAt == nil || a.UpdatedAt == nil || b.UpdatedAt == nil || b.UpdatedAt.UnixMilli() != 1704300000000 || a.Body != "PRIVATE notes" {
		t.Fatalf("times/body: %#v %#v", a, b)
	}
}

func TestSuperProductivityFailures(t *testing.T) {
	noDone := spTaskFixture("t1")
	delete(noDone, "isDone")
	for name, tc := range map[string]struct {
		patch map[string]*http.Response
		code  string
	}{
		"renderer not ready": {map[string]*http.Response{"/health": spOK(t, map[string]any{"server": "up", "rendererReady": false})}, "unavailable"},
		"bad token":          {map[string]*http.Response{"/projects": lnResponse(401, `{"ok":false,"error":{"code":"UNAUTHORIZED","message":"PRIVATE"}}`)}, "authentication"},
		"old app":            {map[string]*http.Response{"/tags": lnResponse(404, `{"ok":false,"error":{"code":"NOT_FOUND","message":"x"}}`)}, "unsupported_schema"},
		"renderer timeout":   {map[string]*http.Response{"/tasks?source=all&includeDone=true": lnResponse(504, `{"ok":false,"error":{"code":"INTERNAL_ERROR","message":"x"}}`)}, "unavailable"},
		"not ok":             {map[string]*http.Response{"/tasks?source=all&includeDone=true": lnResponse(200, `{"ok":false,"data":[]}`)}, "invalid_response"},
		"null tasks":         {map[string]*http.Response{"/tasks?source=all&includeDone=true": spOK(t, nil)}, "invalid_response"},
		"duplicate":          {map[string]*http.Response{"/tasks?source=all&includeDone=true": spOK(t, []any{spTaskFixture("t1"), spTaskFixture("t1")})}, "invalid_response"},
		"unknown isDone":     {map[string]*http.Response{"/tasks?source=all&includeDone=true": spOK(t, []any{noDone})}, "invalid_response"},
	} {
		t.Run(name, func(t *testing.T) {
			routes := spRoutes(t, spTaskFixture("t1"))
			for k, v := range tc.patch {
				routes[k] = v
			}
			f, _ := spFetcher(t, routes)
			snapshot, err := f.Fetch(context.Background(), spConfig())
			requireFailure(t, snapshot, err, tc.code)
		})
	}
}

func TestSuperProductivityClosedAppAndGuards(t *testing.T) {
	t.Setenv("LAZYTODO_TEST_SP_TOKEN", fakeSPToken)
	refused := &Fetcher{HTTP: httpFunc(func(*http.Request) (*http.Response, error) {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
	})}
	snapshot, err := refused.Fetch(context.Background(), spConfig())
	requireFailure(t, snapshot, err, "unavailable")

	never := &Fetcher{HTTP: httpFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("no request may be sent")
		return nil, nil
	})}
	for _, cfg := range []config.Source{
		{Kind: "super-productivity", Host: "localhost:3876", TokenEnv: "LAZYTODO_TEST_SP_TOKEN"},
		{Kind: "super-productivity", Host: "evil.example:3876", TokenEnv: "LAZYTODO_TEST_SP_TOKEN"},
		{Kind: "super-productivity", Scope: "proj-1", TokenEnv: "LAZYTODO_TEST_SP_TOKEN"},
		{Kind: "super-productivity"},
	} {
		snapshot, err := never.Fetch(context.Background(), cfg)
		requireFailure(t, snapshot, err, "invalid_config")
	}
	t.Setenv("LAZYTODO_TEST_SP_TOKEN", "")
	snapshot, err = never.Fetch(context.Background(), spConfig())
	requireFailure(t, snapshot, err, "auth_missing")
	var e *FetchError
	if !errors.As(err, &e) || strings.Contains(e.Message, fakeSPToken) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoopbackClientIgnoresProxy(t *testing.T) {
	client, ok := loopbackHTTP.(*http.Client)
	if !ok {
		t.Fatal("loopback client type changed")
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.Proxy != nil || client.CheckRedirect == nil {
		t.Fatal("loopback client must not use a proxy or follow redirects")
	}
}
