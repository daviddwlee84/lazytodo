package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/flock"
	"lazytodo/internal/config"
	"lazytodo/internal/model"
)

type fakeFetcher struct {
	mu        sync.Mutex
	snapshots map[string]model.Snapshot
	errs      map[string]error
	calls     int
}

func (f *fakeFetcher) Fetch(ctx context.Context, s config.Source) (model.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if err := ctx.Err(); err != nil {
		return model.Snapshot{}, err
	}
	return f.snapshots[s.ID], f.errs[s.ID]
}
func item(id, title string) model.Item {
	return model.Item{Origin: model.Origin{Provider: "example", Authority: "test", Namespace: "store", ID: id}, Title: title, DisplayRef: "#" + id, State: "open", NativeState: "opened", Labels: []model.Label{{Name: "needs review"}}, Assignees: []model.Actor{}}
}
func setup(t *testing.T) (*Service, *fakeFetcher) {
	t.Helper()
	dir := t.TempDir()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	c := config.Config{RefreshInterval: "5m", Color: "never", Sources: []config.Source{{ID: "a", Kind: "snapshot", Path: "/fixture/a", Scope: "org/repo"}}}
	p := config.Paths{Cache: filepath.Join(dir, "cache", "cache.sqlite"), Data: filepath.Join(dir, "data", "local.sqlite"), State: filepath.Join(dir, "state")}
	f := &fakeFetcher{snapshots: map[string]model.Snapshot{"a": {SchemaVersion: 1, Scope: "org/repo", ObservedAt: now, Complete: true, Items: []model.Item{item("1", "登入逾時 👩🏽‍💻"), item("2", "Retry login")}}}, errs: map[string]error{}}
	s := New(c, p, f)
	s.Now = func() time.Time { return now }
	return s, f
}
func fetchOK(t *testing.T, s *Service) {
	t.Helper()
	if _, e := s.Fetch(context.Background(), "", false); e != nil {
		t.Fatal(e)
	}
}

func TestCacheOnlyUnknownAndNoDirectories(t *testing.T) {
	s, f := setup(t)
	r, e := s.Query(context.Background(), QueryRequest{})
	if e != nil {
		t.Fatal(e)
	}
	if r.Total != 0 || r.Sources[0].State != "unknown" {
		t.Fatalf("unexpected %#v", r)
	}
	if f.calls != 0 {
		t.Fatal("query fetched")
	}
	if _, e = os.Stat(filepath.Dir(s.Paths.Cache)); !os.IsNotExist(e) {
		t.Fatal("query initialized cache")
	}
}
func TestFetchQueryPinsAndLossOfCache(t *testing.T) {
	s, _ := setup(t)
	fetchOK(t, s)
	ctx := context.Background()
	r, e := s.Query(ctx, QueryRequest{Text: `登入 label:"needs review"`})
	if e != nil || r.Total != 1 {
		t.Fatalf("%+v %v", r, e)
	}
	ref := r.Items[0].Ref
	if e = s.Pin(ctx, ref, true); e != nil {
		t.Fatal(e)
	}
	r, e = s.Query(ctx, QueryRequest{Text: "pinned:true"})
	if e != nil || r.Total != 1 || !r.Items[0].Available {
		t.Fatal(r, e)
	}
	if e = os.RemoveAll(filepath.Dir(s.Paths.Cache)); e != nil {
		t.Fatal(e)
	}
	r, e = s.Query(ctx, QueryRequest{Text: "pinned:true"})
	if e != nil || r.Total != 1 || r.Items[0].Available || r.Items[0].State != "unknown" {
		t.Fatal(r, e)
	}
	if r.Items[0].Body != "" {
		t.Fatal("durable pin retained body")
	}
	r, e = s.Query(ctx, QueryRequest{})
	if e != nil || r.Total != 0 {
		t.Fatal("orphan appeared in All", r, e)
	}
}
func TestFailedFetchRetainsButEmptyClears(t *testing.T) {
	s, f := setup(t)
	fetchOK(t, s)
	ctx := context.Background()
	f.errs["a"] = errors.New("fixture transport unavailable")
	if _, e := s.Fetch(ctx, "", false); e == nil {
		t.Fatal("failure hidden")
	}
	r, e := s.Query(ctx, QueryRequest{})
	if e != nil || r.Total != 2 || r.Sources[0].State != "failed" {
		t.Fatal(r, e)
	}
	delete(f.errs, "a")
	future := s.Now().Add(10 * time.Minute)
	s.Now = func() time.Time { return future }
	sn := f.snapshots["a"]
	sn.Items = []model.Item{}
	f.snapshots["a"] = sn
	fetchOK(t, s)
	r, e = s.Query(ctx, QueryRequest{})
	if e != nil || r.Total != 0 || !r.Sources[0].Complete {
		t.Fatal(r, e)
	}
}
func TestScopeChangeFailureDoesNotExposeOldItems(t *testing.T) {
	s, f := setup(t)
	fetchOK(t, s)
	s.Config.Sources[0].Scope = "different/repo"
	f.errs["a"] = errors.New("unavailable")
	if _, e := s.Fetch(context.Background(), "", false); e == nil {
		t.Fatal("expected failure")
	}
	r, e := s.Query(context.Background(), QueryRequest{})
	if e != nil || r.Total != 0 || r.Sources[0].Complete {
		t.Fatal("old target leaked", r, e)
	}
}
func TestPinnedFilterDoesNotInventOrphan(t *testing.T) {
	s, _ := setup(t)
	fetchOK(t, s)
	ctx := context.Background()
	r, _ := s.Query(ctx, QueryRequest{})
	if e := s.Pin(ctx, r.Items[0].Ref, true); e != nil {
		t.Fatal(e)
	}
	r, e := s.Query(ctx, QueryRequest{Text: "pinned:true state:unknown"})
	if e != nil || r.Total != 0 {
		t.Fatal("filtered-out current row became orphan", r, e)
	}
}
func TestPaginationRevisionAndPinMutation(t *testing.T) {
	s, _ := setup(t)
	fetchOK(t, s)
	ctx := context.Background()
	r, e := s.Query(ctx, QueryRequest{Limit: 1})
	if e != nil || r.Total != 2 || !r.Truncated {
		t.Fatal(r, e)
	}
	if e = s.Pin(ctx, r.Items[0].Ref, true); e != nil {
		t.Fatal(e)
	}
	_, e = s.Query(ctx, QueryRequest{Limit: 1, Offset: 1, Revision: r.Revision})
	var se *Error
	if !errors.As(e, &se) || se.Code != "revision_changed" {
		t.Fatal(e)
	}
}
func TestIncompleteAndOverlapKeepLastGood(t *testing.T) {
	s, f := setup(t)
	fetchOK(t, s)
	sn := f.snapshots["a"]
	sn.Complete = false
	sn.Items = nil
	f.snapshots["a"] = sn
	if _, e := s.Fetch(context.Background(), "", false); e == nil {
		t.Fatal("incomplete accepted")
	}
	r, _ := s.Query(context.Background(), QueryRequest{})
	if r.Total != 2 {
		t.Fatal(r)
	}
	// A second source claiming the same canonical IDs must not overwrite A.
	s.Config.Sources = append(s.Config.Sources, config.Source{ID: "b", Kind: "snapshot", Path: "/fixture/b", Scope: "org/repo"})
	sn.Complete = true
	sn.Items = []model.Item{item("1", "impostor")}
	f.snapshots["b"] = sn
	if _, e := s.Fetch(context.Background(), "b", false); e == nil {
		t.Fatal("overlap accepted")
	}
	r, _ = s.Query(context.Background(), QueryRequest{})
	if r.Total != 2 {
		t.Fatal(r)
	}
}
func TestAutomaticIntervalZeroAndLock(t *testing.T) {
	s, f := setup(t)
	s.Config.RefreshInterval = "0s"
	if _, e := s.Fetch(context.Background(), "", true); e != nil {
		t.Fatal(e)
	}
	if f.calls != 0 {
		t.Fatal("disabled auto fetched")
	}
	lock := flock.New(filepath.Join(s.Paths.State, "fetch.lock"))
	if e := lock.Lock(); e != nil {
		t.Fatal(e)
	}
	defer lock.Unlock()
	_, e := s.Fetch(context.Background(), "", false)
	var se *Error
	if !errors.As(e, &se) || se.Code != "busy" {
		t.Fatal(e)
	}
}
func TestSnapshotAgeNotRefreshTimeAndSourceAlias(t *testing.T) {
	s, f := setup(t)
	sn := f.snapshots["a"]
	sn.ObservedAt = sn.ObservedAt.Add(-24 * time.Hour)
	f.snapshots["a"] = sn
	fetchOK(t, s)
	r, _ := s.Query(context.Background(), QueryRequest{})
	if r.Sources[0].State != "stale" {
		t.Fatal(r.Sources)
	}
	ref := r.Items[0].Ref
	if e := s.Pin(context.Background(), ref, true); e != nil {
		t.Fatal(e)
	}
	s.Config.Sources[0].ID = "renamed"
	f.snapshots["renamed"] = sn
	fetchOK(t, s)
	r, _ = s.Query(context.Background(), QueryRequest{Text: "pinned:true"})
	if r.Total != 1 || r.Items[0].Ref != ref || r.Items[0].SourceID != "renamed" {
		t.Fatal(r)
	}
}
func TestSourceURL(t *testing.T) {
	i := item("1", "test")
	i.Available = true
	i.Origin.Provider = "github"
	i.Origin.Authority = "github.com"
	for _, u := range []string{"file:///tmp/x", "javascript:alert(1)", "https://evil.test/x", "https://user:secret@github.com/x"} {
		i.URL = u
		if _, e := SourceURL(i); e == nil {
			t.Fatal("accepted", u)
		}
	}
	i.URL = "https://github.com/o/r/issues/1"
	if _, e := SourceURL(i); e != nil {
		t.Fatal(e)
	}
}

func BenchmarkQuery10k(b *testing.B) {
	dir := b.TempDir()
	now := time.Now()
	src := config.Source{ID: "bench", Kind: "snapshot", Path: "/fictional", Scope: "org/bench"}
	items := make([]model.Item, 10000)
	for n := range items {
		items[n] = item(fmtID(n), "搜尋 benchmark issue")
		items[n].Scope = src.Scope
	}
	f := &fakeFetcher{snapshots: map[string]model.Snapshot{"bench": {SchemaVersion: 1, Scope: src.Scope, ObservedAt: now, Complete: true, Items: items}}, errs: map[string]error{}}
	s := New(config.Config{RefreshInterval: "5m", Sources: []config.Source{src}}, config.Paths{Cache: filepath.Join(dir, "cache.db"), Data: filepath.Join(dir, "pins.db"), State: filepath.Join(dir, "state")}, f)
	if _, e := s.Fetch(context.Background(), "", false); e != nil {
		b.Fatal(e)
	}
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		if _, e := s.Query(context.Background(), QueryRequest{Text: "搜尋", Limit: 50}); e != nil {
			b.Fatal(e)
		}
	}
}
func fmtID(n int) string { return strconv.Itoa(n) }
