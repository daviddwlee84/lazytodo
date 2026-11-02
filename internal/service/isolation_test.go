package service

import (
	"context"
	"errors"
	"testing"

	"lazytodo/internal/config"
	"lazytodo/internal/model"
)

func TestBackoffDoesNotPreventOtherSourcesRefreshing(t *testing.T) {
	s, f := setup(t)
	fetchOK(t, s)
	f.errs["a"] = errors.New("offline")
	if _, e := s.Fetch(context.Background(), "a", false); e == nil {
		t.Fatal("expected failure")
	}
	s.Config.Sources = append(s.Config.Sources, config.Source{ID: "b", Kind: "snapshot", Path: "/fixture/b", Scope: "org/repo"})
	f.snapshots["b"] = model.Snapshot{SchemaVersion: 1, Scope: "org/repo", ObservedAt: s.Now(), Complete: true, Items: []model.Item{item("3", "healthy source")}}
	if _, e := s.Fetch(context.Background(), "", false); e == nil {
		t.Fatal("backoff must remain visible")
	}
	r, e := s.Query(context.Background(), QueryRequest{Text: "source:b"})
	if e != nil || r.Total != 1 || r.Items[0].Title != "healthy source" {
		t.Fatal("backoff suppressed independent source", r, e)
	}
}
