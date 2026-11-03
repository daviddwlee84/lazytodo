package source

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"lazytodo/internal/model"
)

type githubIssue struct {
	ID          json.Number     `json:"id"`
	Number      json.Number     `json:"number"`
	Title       string          `json:"title"`
	Body        *string         `json:"body"`
	HTMLURL     string          `json:"html_url"`
	State       string          `json:"state"`
	PullRequest json.RawMessage `json:"pull_request"`
	Labels      []*struct {
		ID    json.Number `json:"id"`
		Name  string      `json:"name"`
		Color string      `json:"color"`
	} `json:"labels"`
	Assignees []*struct {
		ID    json.Number `json:"id"`
		Login string      `json:"login"`
		Type  string      `json:"type"`
	} `json:"assignees"`
	CreatedAt *time.Time `json:"created_at"`
	UpdatedAt *time.Time `json:"updated_at"`
}

func (s *fetchState) github(ctx context.Context) (model.Snapshot, error) {
	items := []model.Item{}
	parts := strings.Split(s.cfg.Scope, "/")
	endpoint := "repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1]) + "/issues"
	for page := 1; ; {
		// Oldest-first: issues created mid-fetch append to the last page instead of
		// shifting a boundary item onto the next page (a duplicate identity).
		path := endpoint + "?state=all&sort=created&direction=asc&per_page=100&page=" + strconv.Itoa(page)
		body, headers, err := s.api(ctx, "gh", []string{"api", "--hostname", s.cfg.Host, "--method", "GET", "--include", path}, nil)
		if err != nil {
			return model.Snapshot{}, err
		}
		var issues []*githubIssue
		if json.Unmarshal(body, &issues) != nil || issues == nil {
			return model.Snapshot{}, invalidResponse()
		}
		for _, issue := range issues {
			if issue == nil {
				return model.Snapshot{}, invalidResponse()
			}
			if len(issue.PullRequest) > 0 && !bytes.Equal(bytes.TrimSpace(issue.PullRequest), []byte("null")) {
				continue
			}
			item, err := s.githubItem(issue)
			if err != nil {
				return model.Snapshot{}, err
			}
			items = append(items, item)
		}
		next, err := githubNext(headers, page)
		if err != nil {
			return model.Snapshot{}, err
		}
		if next == 0 {
			break
		}
		page = next
	}
	return s.snapshot(items), nil
}

func (s *fetchState) githubItem(issue *githubIssue) (model.Item, error) {
	if !positiveID(issue.ID.String()) || !positiveID(issue.Number.String()) || strings.TrimSpace(issue.Title) == "" || issue.State == "" || issue.Labels == nil || issue.Assignees == nil || issue.CreatedAt == nil || issue.CreatedAt.IsZero() || issue.UpdatedAt == nil || issue.UpdatedAt.IsZero() {
		return model.Item{}, invalidResponse()
	}
	u, err := url.Parse(issue.HTMLURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return model.Item{}, invalidResponse()
	}
	item := model.Item{
		Origin: model.Origin{Provider: "github", Authority: s.cfg.Host, Namespace: "issues", ID: issue.ID.String()},
		Scope:  s.cfg.Scope, DisplayRef: s.cfg.Scope + "#" + issue.Number.String(), Title: issue.Title, URL: issue.HTMLURL,
		NativeType: "Issue", NativeState: issue.State, State: normalizedState(issue.State),
		Labels: []model.Label{}, Assignees: []model.Actor{}, CreatedAt: issue.CreatedAt, UpdatedAt: issue.UpdatedAt,
	}
	if issue.Body != nil {
		item.Body = *issue.Body
	}
	for _, label := range issue.Labels {
		if label == nil || !positiveID(label.ID.String()) || label.Name == "" {
			return model.Item{}, invalidResponse()
		}
		item.Labels = append(item.Labels, model.Label{ID: label.ID.String(), Name: label.Name, Color: label.Color})
	}
	for _, actor := range issue.Assignees {
		if actor == nil || !positiveID(actor.ID.String()) || actor.Login == "" {
			return model.Item{}, invalidResponse()
		}
		item.Assignees = append(item.Assignees, model.Actor{ID: actor.ID.String(), Name: actor.Login, Kind: actor.Type})
	}
	return item, nil
}

// Only the next page number is taken from Link. Never execute a server-supplied
// endpoint or host: every request retains the explicitly configured scope.
func githubNext(headers http.Header, current int) (int, error) {
	next := 0
	for _, value := range headers.Values("Link") {
		for _, link := range strings.Split(value, ",") {
			fields := strings.Split(strings.TrimSpace(link), ";")
			if len(fields) < 2 {
				return 0, invalidResponse()
			}
			isNext := false
			for _, field := range fields[1:] {
				key, value, ok := strings.Cut(strings.TrimSpace(field), "=")
				if ok && key == "rel" {
					for _, rel := range strings.Fields(strings.Trim(value, "\"")) {
						if rel == "next" {
							isNext = true
						}
					}
				}
			}
			if !isNext {
				continue
			}
			target := strings.TrimSpace(fields[0])
			if !strings.HasPrefix(target, "<") || !strings.HasSuffix(target, ">") || next != 0 {
				return 0, invalidResponse()
			}
			parsed, err := url.Parse(target[1 : len(target)-1])
			if err != nil {
				return 0, invalidResponse()
			}
			page, err := strconv.Atoi(parsed.Query().Get("page"))
			if err != nil || page <= current {
				return 0, invalidResponse()
			}
			if page > maxPages {
				return 0, failure("page_limit", "Source exceeded the pagination safety limit; previous data retained")
			}
			next = page
		}
	}
	return next, nil
}
