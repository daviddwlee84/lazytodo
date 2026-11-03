package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lazytodo/internal/config"
)

// Fictional workspace and token; no real Linear account is contacted.
const fakeLinearToken = "lin_api_FICTIONAL"

type httpFunc func(*http.Request) (*http.Response, error)

func (f httpFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

func lnConfig() config.Source {
	return config.Source{ID: "ln", Kind: "linear", Scope: "acme/ENG", TokenEnv: "LAZYTODO_TEST_LINEAR_KEY"}
}

func lnResponse(status int, body string, headers ...string) *http.Response {
	h := http.Header{}
	for _, kv := range headers {
		k, v, _ := strings.Cut(kv, ": ")
		h.Add(k, v)
	}
	return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader(body))}
}

func lnTeam(t *testing.T) string {
	return encoded(t, map[string]any{"data": map[string]any{
		"viewer": map[string]any{"organization": map[string]any{"id": "org-1", "urlKey": "acme"}},
		"teams":  map[string]any{"nodes": []any{map[string]any{"id": "team-1", "key": "ENG"}}},
	}})
}

func lnIssue(n int) map[string]any {
	return map[string]any{
		"id": fmt.Sprintf("issue-%d", n), "identifier": fmt.Sprintf("ENG-%d", n), "title": "Preserve native issue",
		"description": "PRIVATE body", "url": fmt.Sprintf("https://linear.app/acme/issue/ENG-%d/x", n),
		"createdAt": "2024-01-01T10:00:00Z", "updatedAt": "2024-01-02T10:00:00Z", "archivedAt": nil, "trashed": false,
		"state":    map[string]any{"name": "In Review", "type": "started"},
		"labels":   map[string]any{"nodes": []any{map[string]any{"id": "lbl-1", "name": "P1", "color": "#ff0000"}}, "pageInfo": map[string]any{"hasNextPage": false}},
		"assignee": map[string]any{"id": "user-1", "name": "Pat Person", "displayName": "pat", "app": false},
		"delegate": map[string]any{"id": "agent-1", "name": "Helper Agent", "displayName": "helper", "app": true},
	}
}

func lnPage(t *testing.T, nodes []any, next string) string {
	return encoded(t, map[string]any{"data": map[string]any{"issues": map[string]any{"nodes": nodes, "pageInfo": page(next)}}})
}

// lnFetcher answers the team preflight, then issue pages in order.
func lnFetcher(t *testing.T, pages ...string) (*Fetcher, *int) {
	t.Helper()
	t.Setenv("LAZYTODO_TEST_LINEAR_KEY", fakeLinearToken)
	calls := 0
	return &Fetcher{HTTP: httpFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.String() != linearEndpoint {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL)
		}
		if r.Header.Get("Authorization") != fakeLinearToken {
			t.Fatalf("authorization header not sent as a raw personal key")
		}
		var payload struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		calls++
		if calls == 1 {
			if !strings.Contains(payload.Query, "LazytodoTeam") || payload.Variables["key"] != "ENG" {
				t.Fatalf("first request must resolve the team: %#v", payload)
			}
			return lnResponse(200, lnTeam(t)), nil
		}
		if payload.Variables["teamId"] != "team-1" || !strings.Contains(payload.Query, "includeArchived: true") {
			t.Fatalf("issue request lost team scope or archive coverage: %#v", payload)
		}
		if calls-2 >= len(pages) {
			t.Fatal("too many issue requests")
		}
		return lnResponse(200, pages[calls-2]), nil
	})}, &calls
}

func TestLinearPaginationAssigneeDelegateAndStates(t *testing.T) {
	second := lnIssue(2)
	second["state"] = map[string]any{"name": "Won't do", "type": "canceled"}
	second["archivedAt"] = "2024-02-01T00:00:00Z"
	second["assignee"], second["delegate"], second["description"] = nil, nil, nil
	trashed := lnIssue(3)
	trashed["trashed"] = true
	f, calls := lnFetcher(t, lnPage(t, []any{lnIssue(1)}, "c1"), lnPage(t, []any{second, trashed}, ""))
	snapshot, err := f.Fetch(context.Background(), lnConfig())
	if err != nil {
		t.Fatal(err)
	}
	if *calls != 3 || len(snapshot.Items) != 2 || !snapshot.Complete || snapshot.Scope != "acme/ENG" {
		t.Fatalf("pagination: calls=%d %#v", *calls, snapshot)
	}
	a, b := snapshot.Items[0], snapshot.Items[1]
	if a.Origin.Provider != "linear" || a.Origin.Authority != "linear.app" || a.Origin.Namespace != "org-1" || a.Origin.ID != "issue-1" || a.DisplayRef != "ENG-1" || a.SourceID != "ln" {
		t.Fatalf("identity: %#v", a)
	}
	if a.State != "open" || a.NativeState != "In Review" || b.State != "closed" || b.NativeState != "Won't do" {
		t.Fatalf("state mapping lost native spelling: %#v %#v", a, b)
	}
	if len(a.Assignees) != 1 || a.Assignees[0].Name != "pat" || a.Assignees[0].Kind != "User" || len(a.Delegates) != 1 || a.Delegates[0].Name != "helper" || a.Delegates[0].Kind != "App" {
		t.Fatalf("assignee and delegate must stay separate: %#v", a)
	}
	if b.Assignees == nil || b.Delegates == nil || len(b.Assignees)+len(b.Delegates) != 0 || b.Body != "" {
		t.Fatalf("null actors must be known-empty: %#v", b)
	}
	if len(a.Labels) != 1 || a.Labels[0].Name != "P1" || a.Labels[0].Color != "#ff0000" {
		t.Fatalf("labels: %#v", a.Labels)
	}
}

func TestLinearFailuresRetainNothing(t *testing.T) {
	moreLabels := lnIssue(1)
	moreLabels["labels"] = map[string]any{"nodes": []any{}, "pageInfo": map[string]any{"hasNextPage": true}}
	badURL := lnIssue(1)
	badURL["url"] = "https://evil.example/acme/issue/ENG-1"
	noTrash := lnIssue(1)
	delete(noTrash, "trashed")
	for name, tc := range map[string]struct {
		pages []string
		code  string
	}{
		"label overflow":   {[]string{lnPage(t, []any{moreLabels}, "")}, "invalid_response"},
		"foreign url":      {[]string{lnPage(t, []any{badURL}, "")}, "invalid_response"},
		"unknown trashed":  {[]string{lnPage(t, []any{noTrash}, "")}, "invalid_response"},
		"repeated cursor":  {[]string{lnPage(t, []any{lnIssue(1)}, "same"), lnPage(t, []any{lnIssue(2)}, "same")}, "invalid_response"},
		"duplicate id":     {[]string{lnPage(t, []any{lnIssue(1)}, "c1"), lnPage(t, []any{lnIssue(1)}, "")}, "invalid_response"},
		"null issues":      {[]string{`{"data":{"issues":null}}`}, "invalid_response"},
		"later page error": {[]string{lnPage(t, []any{lnIssue(1)}, "c1"), `{"data":null,"errors":[{"message":"PRIVATE boom"}]}`}, "graphql_error"},
		"schema":           {[]string{`{"errors":[{"message":"Cannot query field \"delegate\" on type \"Issue\"","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`}, "unsupported_schema"},
	} {
		t.Run(name, func(t *testing.T) {
			f, _ := lnFetcher(t, tc.pages...)
			snapshot, err := f.Fetch(context.Background(), lnConfig())
			requireFailure(t, snapshot, err, tc.code)
		})
	}
}

func TestLinearPreflightAndHTTPFailures(t *testing.T) {
	t.Setenv("LAZYTODO_TEST_LINEAR_KEY", fakeLinearToken)
	for name, tc := range map[string]struct {
		resp  *http.Response
		code  string
		retry bool
	}{
		"other workspace": {lnResponse(200, `{"data":{"viewer":{"organization":{"id":"org-2","urlKey":"other"}},"teams":{"nodes":[{"id":"t","key":"ENG"}]}}}`), "scope_mismatch", false},
		"team missing":    {lnResponse(200, `{"data":{"viewer":{"organization":{"id":"org-1","urlKey":"acme"}},"teams":{"nodes":[]}}}`), "not_found", false},
		"unauthorized":    {lnResponse(401, `{"errors":[{"message":"PRIVATE","extensions":{"code":"AUTHENTICATION_ERROR"}}]}`), "authentication", false},
		"forbidden":       {lnResponse(403, `{}`), "access_denied", false},
		"rate limited":    {lnResponse(429, `{"errors":[{"extensions":{"code":"RATELIMITED"}}]}`, "Retry-After: 30"), "rate_limited", true},
		"rate reset ms":   {lnResponse(400, `{"errors":[{"extensions":{"code":"RATELIMITED"}}]}`, fmt.Sprintf("X-RateLimit-Requests-Reset: %d", time.Now().Add(time.Hour).UnixMilli())), "rate_limited", true},
		"redirect":        {lnResponse(302, ``, "Location: https://evil.example/"), "http_error", false},
		"malformed":       {lnResponse(200, `not json`), "invalid_response", false},
	} {
		t.Run(name, func(t *testing.T) {
			f := &Fetcher{HTTP: httpFunc(func(*http.Request) (*http.Response, error) { return tc.resp, nil })}
			snapshot, err := f.Fetch(context.Background(), lnConfig())
			requireFailure(t, snapshot, err, tc.code)
			var e *FetchError
			if errors.As(err, &e) && tc.retry != !e.RetryAfter.IsZero() {
				t.Fatalf("retry hint mismatch: %v", e.RetryAfter)
			}
		})
	}
}

func TestLinearTokenAndConfigGuards(t *testing.T) {
	never := &Fetcher{HTTP: httpFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("no request may be sent without a valid token and scope")
		return nil, nil
	})}
	t.Setenv("LAZYTODO_TEST_LINEAR_KEY", "")
	snapshot, err := never.Fetch(context.Background(), lnConfig())
	requireFailure(t, snapshot, err, "auth_missing")
	if strings.Contains(err.Error(), fakeLinearToken) {
		t.Fatal("token leaked")
	}
	t.Setenv("LAZYTODO_TEST_LINEAR_KEY", fakeLinearToken)
	for _, cfg := range []config.Source{
		{Kind: "linear", Scope: "ENG", TokenEnv: "X"},
		{Kind: "linear", Scope: "acme/ENG"},
		{Kind: "linear", Scope: "acme/ENG", TokenEnv: "LAZYTODO_TEST_LINEAR_KEY", Host: "evil.example"},
	} {
		snapshot, err := never.Fetch(context.Background(), cfg)
		requireFailure(t, snapshot, err, "invalid_config")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := never.Fetch(ctx, lnConfig()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestHTTPBudgetsAndDefaultRedirectRefusal(t *testing.T) {
	var e *FetchError
	s := &fetchState{pages: maxPages}
	if _, _, _, err := s.request(context.Background(), http.MethodPost, linearEndpoint, http.Header{}, nil); !errors.As(err, &e) || e.Code != "page_limit" {
		t.Fatalf("missing page limit: %v", err)
	}
	s = &fetchState{bytes: maxBytes - 4, http: httpFunc(func(*http.Request) (*http.Response, error) {
		return lnResponse(200, "0123456789"), nil
	})}
	if _, _, _, err := s.request(context.Background(), http.MethodPost, linearEndpoint, http.Header{}, nil); !errors.As(err, &e) || e.Code != "size_limit" {
		t.Fatalf("missing size limit: %v", err)
	}
	followed := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { followed = true }))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer origin.Close()
	s = &fetchState{}
	_, _, status, err := s.request(context.Background(), http.MethodPost, origin.URL, http.Header{"Authorization": {fakeLinearToken}}, nil)
	if err != nil || status != http.StatusFound || followed {
		t.Fatalf("default client followed a redirect: status=%d err=%v followed=%v", status, err, followed)
	}
}
