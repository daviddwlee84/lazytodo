package search

import (
	"fmt"
	"strings"
	"unicode"

	"lazytodo/internal/model"
)

type Query struct {
	Ref     string // Internal exact lookup; not part of the user search grammar.
	Terms   []string
	Sources []string
	States  []string
	Labels  []string
	Pinned  *bool
}

func Parse(input string) (Query, error) {
	var q Query
	var tokens []string
	var filters []bool
	var token strings.Builder
	quoted, escaped, started, hasFilter := false, false, false, false
	for _, r := range input {
		if escaped {
			token.WriteRune(r)
			escaped = false
			started = true
			continue
		}
		if r == '\\' {
			escaped = true
			started = true
			continue
		}
		if r == '"' {
			quoted = !quoted
			started = true
			continue
		}
		if unicode.IsSpace(r) && !quoted {
			if started {
				tokens = append(tokens, token.String())
				filters = append(filters, hasFilter)
				token.Reset()
				started, hasFilter = false, false
			}
			continue
		}
		if r == ':' && !quoted {
			hasFilter = true
		}
		token.WriteRune(r)
		started = true
	}
	if quoted || escaped {
		return q, fmt.Errorf("unfinished quote or escape in query")
	}
	if started {
		tokens = append(tokens, token.String())
		filters = append(filters, hasFilter)
	}
	for n, t := range tokens {
		if t == "" {
			return q, fmt.Errorf("empty search term")
		}
		key, value, filter := strings.Cut(t, ":")
		if !filter || !filters[n] {
			q.Terms = append(q.Terms, model.Normalize(t))
			continue
		}
		if value == "" {
			return q, fmt.Errorf("%s filter needs a value", key)
		}
		switch key {
		case "source":
			q.Sources = append(q.Sources, value)
		case "state":
			if value != "open" && value != "closed" && value != "unknown" {
				return q, fmt.Errorf("state must be open, closed or unknown")
			}
			q.States = append(q.States, value)
		case "label":
			q.Labels = append(q.Labels, value)
		case "pinned":
			if q.Pinned != nil {
				return q, fmt.Errorf("pinned filter may appear only once")
			}
			if value != "true" && value != "false" {
				return q, fmt.Errorf("pinned must be true or false")
			}
			v := value == "true"
			q.Pinned = &v
		default:
			return q, fmt.Errorf("unknown filter %q (use source, state, label, pinned)", key)
		}
	}
	return q, nil
}

func Text(i model.Item) string {
	parts := []string{i.Title, i.Body, i.DisplayRef, i.Scope}
	for _, l := range i.Labels {
		parts = append(parts, l.Name)
	}
	return model.Normalize(strings.Join(parts, "\n"))
}
func (q Query) Match(i model.Item) bool {
	if q.Pinned != nil && *q.Pinned != i.Pinned {
		return false
	}
	for _, v := range q.Sources {
		if i.SourceID != v {
			return false
		}
	}
	for _, v := range q.States {
		if i.State != v {
			return false
		}
	}
	for _, v := range q.Labels {
		found := false
		for _, l := range i.Labels {
			if l.Name == v {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	text := Text(i)
	for _, v := range q.Terms {
		if !strings.Contains(text, v) {
			return false
		}
	}
	return true
}
func (q Query) Rank(i model.Item) int {
	n := 0
	title := model.Normalize(i.Title)
	ref := model.Normalize(i.DisplayRef)
	for _, t := range q.Terms {
		if strings.Contains(title, t) || strings.Contains(ref, t) {
			n++
		}
	}
	return n
}
