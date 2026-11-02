package source

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

const discoveryOld = "2024-01-01T10:00:00Z"
const discoveryRecent = "2024-01-03T10:00:00Z"

func discoveryRecord(kind string, id int, scope, updated string) map[string]any {
	if kind == "github" {
		return map[string]any{"id": id, "full_name": scope, "updated_at": updated, "private": false, "archived": false, "has_issues": true}
	}
	return map[string]any{"id": id, "path_with_namespace": scope, "last_activity_at": updated, "visibility": "public", "archived": false, "issues_enabled": true}
}

func discoveryEndpoint(kind string, page int) string {
	if kind == "github" {
		return fmt.Sprintf("user/repos?affiliation=owner,collaborator,organization_member&sort=updated&direction=desc&per_page=100&page=%d", page)
	}
	return fmt.Sprintf("projects?membership=true&archived=false&order_by=last_activity_at&sort=desc&per_page=100&page=%d", page)
}

func discoveryUser(kind string) Result {
	if kind == "github" {
		return response(`{"login":"fixture-user"}`)
	}
	return response(`{"username":"fixture-user"}`)
}

type discoveryStep struct {
	endpoint string
	result   Result
	err      error
}

func discoveryFetcher(t *testing.T, kind, host string, steps ...discoveryStep) *Fetcher {
	t.Helper()
	calls := 0
	t.Cleanup(func() {
		if calls != len(steps) {
			t.Errorf("wanted %d requests, got %d", len(steps), calls)
		}
	})
	return &Fetcher{Runner: runnerFunc(func(_ context.Context, name string, args []string, input []byte) (Result, error) {
		t.Helper()
		if calls >= len(steps) {
			t.Fatalf("unexpected extra request: %s %v", name, args)
		}
		step := steps[calls]
		calls++
		command := "gh"
		if kind == "gitlab" {
			command = "glab"
		}
		want := []string{"api", "--hostname", host, "--method", "GET", "--include", step.endpoint}
		if name != command || !reflect.DeepEqual(args, want) || len(input) != 0 {
			t.Fatalf("unsafe or unexpected request: %s %#v, input %q", name, args, input)
		}
		return step.result, step.err
	})}
}

func requireDiscoveryFailure(t *testing.T, result Discovery, err error, code string) {
	t.Helper()
	var fetchErr *FetchError
	if !errors.As(err, &fetchErr) || fetchErr.Code != code {
		t.Fatalf("want %s, got %v", code, err)
	}
	if strings.Contains(err.Error(), "PRIVATE") {
		t.Fatalf("provider text leaked: %v", err)
	}
	if !result.Truncated {
		t.Fatalf("failed discovery marked complete: %#v", result)
	}
}

func TestDiscoverGitHubMembershipMetadata(t *testing.T) {
	archived := discoveryRecord("github", 2, "org/archive", discoveryRecent)
	archived["archived"] = true
	noIssues := discoveryRecord("github", 3, "org/disabled", discoveryRecent)
	noIssues["has_issues"] = false
	private := discoveryRecord("github", 4, "org/private", discoveryOld)
	private["private"] = true
	f := discoveryFetcher(t, "github", "github.example",
		discoveryStep{"user", discoveryUser("github"), nil},
		discoveryStep{discoveryEndpoint("github", 1), response(encoded(t, []any{
			discoveryRecord("github", 9007199254740993, "org/z", discoveryOld), archived, noIssues, private,
		}), `Link: <https://untrusted.invalid/other-endpoint?sort=wrong&page=3>; rel="next"`), nil},
		discoveryStep{discoveryEndpoint("github", 3), response(encoded(t, []any{
			discoveryRecord("github", 9007199254740993, "org/z", discoveryRecent),
			discoveryRecord("github", 5, "org/a", discoveryRecent), private,
		}), `Link: <https://github.example/user/repos?page=1>; rel="prev"`), nil},
	)
	result, err := f.Discover(context.Background(), Target{Kind: "github", Host: "GitHub.Example."}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if result.Target != (Target{Kind: "github", Host: "github.example"}) || result.Account != "fixture-user" || result.Order != "GitHub repository updated_at" || result.Truncated || result.Scanned != 7 {
		t.Fatalf("bad discovery metadata: %#v", result)
	}
	if len(result.Candidates) != 3 || result.Candidates[0].Scope != "org/a" || result.Candidates[1].Scope != "org/z" || result.Candidates[2].Scope != "org/private" || !result.Candidates[2].Private {
		t.Fatalf("filtering, deduplication, or ordering failed: %#v", result.Candidates)
	}
	for _, candidate := range result.Candidates {
		if candidate.Kind != "github" || candidate.Host != "github.example" || candidate.UpdatedAt.IsZero() {
			t.Fatalf("lost native metadata: %#v", candidate)
		}
	}
	if result.Candidates[1].UpdatedAt.Format(time.RFC3339) != discoveryRecent {
		t.Fatal("duplicate retained older metadata")
	}
}

func TestDiscoverGitLabMembershipMetadata(t *testing.T) {
	archived := discoveryRecord("gitlab", 2, "group/archived", discoveryRecent)
	archived["archived"] = true
	noIssues := discoveryRecord("gitlab", 3, "group/disabled", discoveryRecent)
	noIssues["issues_enabled"] = false
	internal := discoveryRecord("gitlab", 4, "group/sub/internal", discoveryRecent)
	internal["visibility"] = "internal"
	delete(internal, "issues_enabled")
	private := discoveryRecord("gitlab", 5, "group/private", discoveryRecent)
	private["visibility"] = "private"
	private["issues_enabled"] = nil // Unavailable is not an explicit disabled value.
	f := discoveryFetcher(t, "gitlab", "gitlab.example:8443",
		discoveryStep{"user", discoveryUser("gitlab"), nil},
		discoveryStep{discoveryEndpoint("gitlab", 1), response(encoded(t, []any{
			discoveryRecord("gitlab", 1, "group/public", discoveryOld), archived, noIssues,
		}), "X-Next-Page: 2"), nil},
		discoveryStep{discoveryEndpoint("gitlab", 2), response(encoded(t, []any{internal}), `Link: <https://untrusted.invalid/private?page=4>; rel="next"`), nil},
		discoveryStep{discoveryEndpoint("gitlab", 4), response(encoded(t, []any{private, internal}), "X-Next-Page:"), nil},
	)
	result, err := f.Discover(context.Background(), Target{Kind: "gitlab", Host: "GitLab.Example:8443"}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if result.Account != "fixture-user" || result.Order != "GitLab project last_activity_at" || result.Scanned != 6 || result.Truncated || result.Target.Host != "gitlab.example:8443" {
		t.Fatalf("bad discovery metadata: %#v", result)
	}
	if len(result.Candidates) != 3 || result.Candidates[0].Scope != "group/private" || result.Candidates[1].Scope != "group/sub/internal" || result.Candidates[2].Scope != "group/public" {
		t.Fatalf("bad sorted candidates: %#v", result.Candidates)
	}
	if !result.Candidates[0].Private || !result.Candidates[1].Private || result.Candidates[2].Private {
		t.Fatalf("incorrect visibility: %#v", result.Candidates)
	}
}

func TestDiscoverGitLabHeaderlessFullPage(t *testing.T) {
	for _, lastBody := range []string{"[]", encoded(t, []any{discoveryRecord("gitlab", 101, "group/last", discoveryOld)})} {
		t.Run(lastBody[:1]+fmt.Sprint(len(lastBody)), func(t *testing.T) {
			records := make([]any, 100)
			for i := range records {
				records[i] = discoveryRecord("gitlab", i+1, fmt.Sprintf("group/project-%03d", i), discoveryRecent)
			}
			f := discoveryFetcher(t, "gitlab", "gitlab.com",
				discoveryStep{"user", discoveryUser("gitlab"), nil},
				discoveryStep{discoveryEndpoint("gitlab", 1), response(encoded(t, records)), nil},
				discoveryStep{discoveryEndpoint("gitlab", 2), response(lastBody), nil},
			)
			result, err := f.Discover(context.Background(), Target{Kind: "gitlab"}, 1000)
			want := 100
			if lastBody != "[]" {
				want++
			}
			if err != nil || result.Truncated || result.Scanned != want || len(result.Candidates) != want {
				t.Fatalf("headerless pagination: %#v %v", result, err)
			}
		})
	}
}

func TestDiscoverCutoffs(t *testing.T) {
	for _, kind := range []string{"github", "gitlab"} {
		t.Run(kind+"/eligible-limit", func(t *testing.T) {
			filtered := discoveryRecord(kind, 1, "org/archived", discoveryRecent)
			filtered["archived"] = true
			duplicate := discoveryRecord(kind, 2, "org/one", discoveryRecent)
			f := discoveryFetcher(t, kind, kind+".com",
				discoveryStep{"user", discoveryUser(kind), nil},
				discoveryStep{discoveryEndpoint(kind, 1), response(encoded(t, []any{filtered, duplicate, duplicate, discoveryRecord(kind, 3, "org/two", discoveryOld), discoveryRecord(kind, 4, "org/unvisited", discoveryOld)}), `Link: <https://example.invalid/path?page=2>; rel="next"`), nil},
			)
			result, err := f.Discover(context.Background(), Target{Kind: kind}, 2)
			if err != nil || !result.Truncated || result.Scanned != 4 || len(result.Candidates) != 2 {
				t.Fatalf("eligible cutoff: %#v %v", result, err)
			}
		})
		t.Run(kind+"/scan-limit", func(t *testing.T) {
			steps := []discoveryStep{{"user", discoveryUser(kind), nil}}
			for page := 1; page <= 20; page++ {
				records := make([]any, 100)
				for i := range records {
					record := discoveryRecord(kind, (page-1)*100+i+1, fmt.Sprintf("org/record-%d-%d", page, i), discoveryRecent)
					record["archived"] = true
					records[i] = record
				}
				steps = append(steps, discoveryStep{discoveryEndpoint(kind, page), response(encoded(t, records), fmt.Sprintf(`Link: <https://example.invalid/path?page=%d>; rel="next"`, page+1)), nil})
			}
			f := discoveryFetcher(t, kind, kind+".com", steps...)
			result, err := f.Discover(context.Background(), Target{Kind: kind}, 1000)
			if err != nil || !result.Truncated || result.Scanned != 2000 || len(result.Candidates) != 0 {
				t.Fatalf("scan cutoff: %#v %v", result, err)
			}
		})
	}
}

func TestDiscoverEmptyAndExactLimit(t *testing.T) {
	for _, kind := range []string{"github", "gitlab"} {
		for _, empty := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/empty=%v", kind, empty), func(t *testing.T) {
				body := "[]"
				if !empty {
					body = encoded(t, []any{discoveryRecord(kind, 1, "group/project", discoveryRecent)})
				}
				f := discoveryFetcher(t, kind, kind+".com",
					discoveryStep{"user", discoveryUser(kind), nil},
					discoveryStep{discoveryEndpoint(kind, 1), response(body), nil},
				)
				result, err := f.Discover(context.Background(), Target{Kind: kind}, 1)
				if err != nil || result.Truncated == empty || result.Candidates == nil || result.Account != "fixture-user" {
					t.Fatalf("empty/exact limit: %#v %v", result, err)
				}
			})
		}
	}
}

func TestDiscoverSafeFailuresAndPartialResults(t *testing.T) {
	failures := []struct {
		name, code string
		result     Result
		err        error
	}{
		{"auth", "authentication", Result{Stdout: []byte("HTTP/2 401 Unauthorized\r\n\r\nPRIVATE account details"), Stderr: []byte("PRIVATE token")}, errors.New("PRIVATE command error")},
		{"missing-command", "command_failed", Result{Stderr: []byte("PRIVATE path")}, &exec.Error{Name: "PRIVATE executable", Err: exec.ErrNotFound}},
		{"rate-limit", "rate_limited", Result{Stdout: []byte("HTTP/2 429 Too Many Requests\r\nRetry-After: 60\r\n\r\nPRIVATE"), Stderr: []byte("PRIVATE")}, errors.New("PRIVATE")},
		{"output-limit", "size_limit", Result{Stderr: []byte("PRIVATE")}, errOutputLimit},
	}
	for _, kind := range []string{"github", "gitlab"} {
		for _, partial := range []bool{false, true} {
			for _, tc := range failures {
				t.Run(fmt.Sprintf("%s/partial=%v/%s", kind, partial, tc.name), func(t *testing.T) {
					steps := []discoveryStep{{"user", tc.result, tc.err}}
					if partial {
						steps = []discoveryStep{
							{"user", discoveryUser(kind), nil},
							{discoveryEndpoint(kind, 1), response(encoded(t, []any{
								discoveryRecord(kind, 1, "group/old", discoveryOld), discoveryRecord(kind, 2, "group/recent", discoveryRecent),
							}), `Link: <https://example.invalid/path?page=2>; rel="next"`), nil},
							{discoveryEndpoint(kind, 2), tc.result, tc.err},
						}
					}
					f := discoveryFetcher(t, kind, kind+".com", steps...)
					result, err := f.Discover(context.Background(), Target{Kind: kind}, 100)
					requireDiscoveryFailure(t, result, err, tc.code)
					if partial {
						if result.Account != "fixture-user" || len(result.Candidates) != 2 || result.Candidates[0].Scope != "group/recent" || result.Scanned != 2 {
							t.Fatalf("partial choices lost: %#v", result)
						}
					} else if result.Account != "" || len(result.Candidates) != 0 || result.Scanned != 0 {
						t.Fatalf("failed authentication leaked choices: %#v", result)
					}
					if tc.code == "rate_limited" {
						var fetchErr *FetchError
						errors.As(err, &fetchErr)
						if fetchErr.RetryTime().Before(time.Now()) {
							t.Fatal("retry metadata lost")
						}
					}
				})
			}
		}
	}
}

func TestDiscoverRejectsMalformedUser(t *testing.T) {
	for _, kind := range []string{"github", "gitlab"} {
		field := "login"
		if kind == "gitlab" {
			field = "username"
		}
		bodies := []string{"null", "{}", "[]", "PRIVATE invalid JSON", fmt.Sprintf(`{%q:null}`, field), fmt.Sprintf(`{%q:42}`, field), fmt.Sprintf(`{%q:""}`, field), fmt.Sprintf(`{%q:"PRIVATE\u001b[0m"}`, field), fmt.Sprintf(`{%q:"PRIVATE name"}`, field)}
		for i, body := range bodies {
			t.Run(fmt.Sprintf("%s/%d", kind, i), func(t *testing.T) {
				f := discoveryFetcher(t, kind, kind+".com", discoveryStep{"user", response(body), nil})
				result, err := f.Discover(context.Background(), Target{Kind: kind}, 100)
				requireDiscoveryFailure(t, result, err, "invalid_response")
				if result.Account != "" || len(result.Candidates) != 0 {
					t.Fatalf("malformed identity leaked: %#v", result)
				}
			})
		}
	}
}

func TestDiscoverRejectsMalformedRecordsPreservingPartial(t *testing.T) {
	for _, kind := range []string{"github", "gitlab"} {
		bodies := []string{"null", "{}", "PRIVATE invalid JSON", "[null]", "[{}]"}
		fields := []string{"id", "full_name", "updated_at", "private", "archived", "has_issues"}
		if kind == "gitlab" {
			fields = []string{"id", "path_with_namespace", "last_activity_at", "visibility", "archived"}
		}
		for _, field := range fields {
			for _, missing := range []bool{true, false} {
				record := discoveryRecord(kind, 1, "group/project", discoveryRecent)
				if missing {
					delete(record, field)
				} else {
					record[field] = nil
				}
				bodies = append(bodies, encoded(t, []any{record}))
			}
		}
		for _, id := range []any{0, -1, 1.5, "PRIVATE", "1e5"} {
			record := discoveryRecord(kind, 1, "group/project", discoveryRecent)
			record["id"] = id
			bodies = append(bodies, encoded(t, []any{record}))
		}
		for _, updated := range []string{"PRIVATE", "0001-01-01T00:00:00Z", ""} {
			bodies = append(bodies, encoded(t, []any{discoveryRecord(kind, 1, "group/project", updated)}))
		}
		for _, scope := range []string{"repo", "group/../project", "group/{repo}", "group/repo?PRIVATE", "group/repo\nPRIVATE", "https://PRIVATE/repo", "group/./repo", "group/re po", "group//repo"} {
			bodies = append(bodies, encoded(t, []any{discoveryRecord(kind, 1, scope, discoveryRecent)}))
		}
		if kind == "github" {
			bodies = append(bodies, encoded(t, []any{discoveryRecord(kind, 1, "group/sub/repo", discoveryRecent)}))
		} else {
			record := discoveryRecord(kind, 1, "group/project", discoveryRecent)
			record["visibility"] = "PRIVATE unknown visibility"
			bodies = append(bodies, encoded(t, []any{record}))
		}
		for i, body := range bodies {
			t.Run(fmt.Sprintf("%s/%d", kind, i), func(t *testing.T) {
				f := discoveryFetcher(t, kind, kind+".com",
					discoveryStep{"user", discoveryUser(kind), nil},
					discoveryStep{discoveryEndpoint(kind, 1), response(encoded(t, []any{discoveryRecord(kind, 10, "group/known", discoveryOld)}), `Link: <https://example.invalid/path?page=2>; rel="next"`), nil},
					discoveryStep{discoveryEndpoint(kind, 2), response(body), nil},
				)
				result, err := f.Discover(context.Background(), Target{Kind: kind}, 100)
				requireDiscoveryFailure(t, result, err, "invalid_response")
				if len(result.Candidates) != 1 || result.Candidates[0].Scope != "group/known" || result.Account != "fixture-user" {
					t.Fatalf("known choices lost: %#v", result)
				}
			})
		}
	}
}

func TestDiscoverRejectsInvalidPagination(t *testing.T) {
	for _, tc := range []struct {
		kind, code string
		headers    []string
	}{
		{"github", "invalid_response", []string{`Link: <https://example.invalid/path?page=1>; rel="next"`}},
		{"github", "invalid_response", []string{`Link: PRIVATE broken`}},
		{"github", "page_limit", []string{`Link: <https://example.invalid/path?page=10001>; rel="next"`}},
		{"gitlab", "invalid_response", []string{"X-Next-Page: 1"}},
		{"gitlab", "invalid_response", []string{"X-Next-Page: PRIVATE"}},
		{"gitlab", "invalid_response", []string{"X-Next-Page: 0"}},
		{"gitlab", "invalid_response", []string{"X-Next-Page: 2", "X-Next-Page: 3"}},
		{"gitlab", "invalid_response", []string{"X-Next-Page: 2", `Link: <https://example.invalid/path?page=3>; rel="next"`}},
		{"gitlab", "page_limit", []string{"X-Next-Page: 10001"}},
	} {
		t.Run(tc.kind+"/"+strings.Join(tc.headers, ","), func(t *testing.T) {
			f := discoveryFetcher(t, tc.kind, tc.kind+".com",
				discoveryStep{"user", discoveryUser(tc.kind), nil},
				discoveryStep{discoveryEndpoint(tc.kind, 1), response(encoded(t, []any{discoveryRecord(tc.kind, 1, "group/known", discoveryOld)}), tc.headers...), nil},
			)
			result, err := f.Discover(context.Background(), Target{Kind: tc.kind}, 100)
			requireDiscoveryFailure(t, result, err, tc.code)
			if len(result.Candidates) != 1 {
				t.Fatal("pagination error discarded validated choices")
			}
		})
	}
}

func TestDiscoverCancellation(t *testing.T) {
	for _, cancelAt := range []int{0, 1, 3} {
		t.Run(fmt.Sprint(cancelAt), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if cancelAt == 0 {
				cancel()
			}
			calls := 0
			f := &Fetcher{Runner: runnerFunc(func(_ context.Context, _ string, _ []string, _ []byte) (Result, error) {
				calls++
				if calls == cancelAt {
					cancel()
					return Result{Stderr: []byte("PRIVATE")}, errors.New("PRIVATE cancellation")
				}
				if calls == 1 {
					return discoveryUser("github"), nil
				}
				return response(encoded(t, []any{discoveryRecord("github", 1, "group/known", discoveryOld)}), `Link: <https://example.invalid/path?page=2>; rel="next"`), nil
			})}
			result, err := f.Discover(ctx, Target{Kind: "github"}, 100)
			if !errors.Is(err, context.Canceled) || !result.Truncated || calls != cancelAt {
				t.Fatalf("cancellation lost: %#v %v, %d calls", result, err, calls)
			}
			want := 0
			if cancelAt == 3 {
				want = 1
			}
			if len(result.Candidates) != want {
				t.Fatalf("partial choices lost on cancellation: %#v", result)
			}
		})
	}
}

func TestDiscoverDeadline(t *testing.T) {
	for _, timeout := range []time.Duration{0, 5 * time.Second, time.Minute} {
		t.Run(timeout.String(), func(t *testing.T) {
			ctx := context.Background()
			if timeout != 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, timeout)
				defer cancel()
			}
			started := time.Now()
			var requestDeadline time.Time
			calls := 0
			f := &Fetcher{Runner: runnerFunc(func(child context.Context, _ string, _ []string, _ []byte) (Result, error) {
				deadline, ok := child.Deadline()
				if !ok {
					t.Fatal("discovery subprocess has no deadline")
				}
				if callerDeadline, ok := ctx.Deadline(); ok && timeout < discoveryTimeout {
					if !deadline.Equal(callerDeadline) {
						t.Fatal("earlier caller deadline was not retained")
					}
				} else if deadline.Before(started.Add(discoveryTimeout)) || deadline.After(time.Now().Add(discoveryTimeout)) {
					t.Fatalf("unexpected discovery timeout: %v", deadline.Sub(started))
				}
				if !requestDeadline.IsZero() && !requestDeadline.Equal(deadline) {
					t.Fatal("deadline reset between metadata requests")
				}
				requestDeadline = deadline
				calls++
				if calls == 1 {
					return discoveryUser("github"), nil
				}
				return response("[]"), nil
			})}
			if _, err := f.Discover(ctx, Target{Kind: "github"}, 100); err != nil {
				t.Fatal(err)
			}
			if calls != 2 {
				t.Fatalf("wanted two bounded requests, got %d", calls)
			}
		})
	}
	t.Run("expired", func(t *testing.T) {
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
		f := &Fetcher{Runner: runnerFunc(func(context.Context, string, []string, []byte) (Result, error) {
			t.Fatal("expired discovery invoked runner")
			return Result{}, nil
		})}
		result, err := f.Discover(ctx, Target{Kind: "github"}, 100)
		if !errors.Is(err, context.DeadlineExceeded) || !result.Truncated {
			t.Fatalf("deadline lost: %#v %v", result, err)
		}
	})
}

func TestDiscoverValidatesBeforeInvocation(t *testing.T) {
	for _, tc := range []struct {
		target Target
		limit  int
		code   string
	}{
		{Target{}, 100, "unsupported_source"},
		{Target{Kind: "snapshot"}, 100, "unsupported_source"},
		{Target{Kind: "GitHub"}, 100, "unsupported_source"},
		{Target{Kind: "github"}, 0, "invalid_config"},
		{Target{Kind: "gitlab"}, -1, "invalid_config"},
		{Target{Kind: "github"}, 1001, "invalid_config"},
		{Target{Kind: "github", Host: "https://PRIVATE"}, 100, "invalid_config"},
		{Target{Kind: "gitlab", Host: "PRIVATE/path"}, 100, "invalid_config"},
		{Target{Kind: "github", Host: "PRIVATE?query"}, 100, "invalid_config"},
		{Target{Kind: "github", Host: "PRIVATE#fragment"}, 100, "invalid_config"},
		{Target{Kind: "gitlab", Host: "PRIVATE@host"}, 100, "invalid_config"},
		{Target{Kind: "github", Host: "{hostname}"}, 100, "invalid_config"},
		{Target{Kind: "github", Host: "--PRIVATE"}, 100, "invalid_config"},
		{Target{Kind: "gitlab", Host: " "}, 100, "invalid_config"},
		{Target{Kind: "gitlab", Host: "host\nPRIVATE"}, 100, "invalid_config"},
	} {
		t.Run(fmt.Sprintf("%s/%s/%d", tc.target.Kind, tc.target.Host, tc.limit), func(t *testing.T) {
			f := &Fetcher{Runner: runnerFunc(func(context.Context, string, []string, []byte) (Result, error) {
				t.Fatal("invalid discovery invoked runner")
				return Result{}, nil
			})}
			result, err := f.Discover(context.Background(), tc.target, tc.limit)
			requireDiscoveryFailure(t, result, err, tc.code)
		})
	}
}

func TestDiscoverSharesOverallByteBudget(t *testing.T) {
	// The same stderr buffer counts on every call without allocating 64 MiB per
	// response. Crossing the cumulative budget must preserve the earlier page.
	padding := make([]byte, maxBytes/3)
	user := discoveryUser("github")
	user.Stderr = padding
	firstPage := response(encoded(t, []any{discoveryRecord("github", 1, "group/known", discoveryOld)}), `Link: <https://example.invalid/path?page=2>; rel="next"`)
	firstPage.Stderr = padding
	lastPage := response("[]")
	lastPage.Stderr = padding
	f := discoveryFetcher(t, "github", "github.com",
		discoveryStep{"user", user, nil},
		discoveryStep{discoveryEndpoint("github", 1), firstPage, nil},
		discoveryStep{discoveryEndpoint("github", 2), lastPage, nil},
	)
	result, err := f.Discover(context.Background(), Target{Kind: "github"}, 100)
	requireDiscoveryFailure(t, result, err, "size_limit")
	if len(result.Candidates) != 1 || result.Scanned != 1 {
		t.Fatalf("byte budget lost partial choices: %#v", result)
	}
}
