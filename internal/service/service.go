package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"
	"lazytodo/internal/config"
	"lazytodo/internal/model"
	"lazytodo/internal/search"
	"lazytodo/internal/store"
)

func (s *Service) statuses(cached map[string]store.CachedSource) []model.SourceStatus {
	out := make([]model.SourceStatus, 0, len(s.Config.Active()))
	for _, src := range s.Config.Active() {
		status := model.SourceStatus{ID: src.ID, Kind: src.Kind, Scope: src.Scope, State: "unknown"}
		if c, ok := cached[src.ID]; ok && c.Fingerprint == src.Fingerprint() {
			status = c.Status
		}
		switch {
		case status.Error != "":
			status.State = "failed"
		case status.ObservedAt == nil || !status.Complete:
			status.State = "unknown"
		default:
			ttl := src.Interval(s.Config.Interval())
			if ttl == 0 {
				ttl = 5 * time.Minute
			}
			if s.Now().Sub(*status.ObservedAt) > ttl {
				status.State = "stale"
			} else {
				status.State = "fresh"
			}
		}
		out = append(out, status)
	}
	return out
}
func (s *Service) Sources(ctx context.Context) ([]model.SourceStatus, error) {
	r, e := store.Read(ctx, s.Paths.Cache, nil, search.Query{})
	return s.statuses(r.Sources), e
}

func (s *Service) Query(ctx context.Context, req QueryRequest) (QueryResult, error) {
	out := QueryResult{Items: []model.Item{}}
	if req.Limit == 0 {
		req.Limit = 50
	}
	if req.Limit < 1 || req.Limit > 1000 || req.Offset < 0 {
		return out, Fail("usage", "limit must be 1..1000 and offset nonnegative")
	}
	q, err := search.Parse(req.Text)
	if err != nil {
		return out, Fail("usage", err.Error())
	}
	for _, id := range q.Sources {
		found := false
		for _, c := range s.Config.Active() {
			if c.ID == id {
				found = true
			}
		}
		if !found {
			return out, Fail("usage", "unknown or disabled source: "+id)
		}
	}
	r, err := store.Read(ctx, s.Paths.Cache, s.Config.Active(), q)
	if err != nil {
		return out, err
	}
	pins, err := store.Pins(ctx, s.Paths.Data)
	if err != nil {
		return out, err
	}
	out.Sources = s.statuses(r.Sources)
	// Canonical source observations and pins, not changing wall-clock age, bind pages.
	var revisionParts []string
	for _, c := range s.Config.Active() {
		revisionParts = append(revisionParts, c.ID+":"+c.Fingerprint())
		if v, ok := r.Sources[c.ID]; ok && v.Fingerprint == c.Fingerprint() {
			// Observation identity only: attempt/retry/error metadata changes on a
			// failed refresh without changing any cached row.
			observed := ""
			if v.Status.ObservedAt != nil {
				observed = v.Status.ObservedAt.UTC().Format(time.RFC3339Nano)
			}
			b, _ := json.Marshal([]any{v.Status.Revision, observed, v.Status.Count, v.Status.Complete})
			revisionParts = append(revisionParts, c.ID+"="+string(b))
		}
	}
	for ref := range pins {
		revisionParts = append(revisionParts, "pin:"+ref)
	}
	sort.Strings(revisionParts)
	out.Revision = model.Digest(strings.Join(revisionParts, "\n"))
	if req.Revision != "" && req.Revision != out.Revision {
		return out, Fail("revision_changed", "cache or pins changed; restart pagination at offset 0")
	}
	var items []model.Item
	for _, i := range r.Items {
		_, i.Pinned = pins[i.Ref]
		// SQL already applied text/source/state/label predicates.
		if q.Pinned == nil || *q.Pinned == i.Pinned {
			items = append(items, i)
		}
	}
	// Orphan pins remain discoverable only in an explicitly pinned query.
	if q.Pinned != nil && *q.Pinned {
		for ref, i := range pins {
			if !r.AvailableRefs[ref] {
				i.Pinned = true
				i.Available = false
				i.State = "unknown"
				i.NativeState = ""
				if q.Match(i) {
					items = append(items, i)
				}
			}
		}
	}
	ranks := make(map[string]int, len(items))
	if len(q.Terms) > 0 {
		for _, i := range items {
			ranks[i.Ref] = q.Rank(i)
		}
	}
	sort.Slice(items, func(a, b int) bool {
		ia, ib := items[a], items[b]
		ra, rb := ranks[ia.Ref], ranks[ib.Ref]
		if ra != rb {
			return ra > rb
		}
		var ta, tb time.Time
		if ia.UpdatedAt != nil {
			ta = *ia.UpdatedAt
		}
		if ib.UpdatedAt != nil {
			tb = *ib.UpdatedAt
		}
		if !ta.Equal(tb) {
			return ta.After(tb)
		}
		return ia.Ref < ib.Ref
	})
	out.Total = len(items)
	out.Offset = req.Offset
	out.Limit = req.Limit
	start := min(req.Offset, len(items))
	end := start + min(req.Limit, len(items)-start)
	for _, i := range items[start:end] {
		if !req.IncludeBody {
			i.Body = ""
		}
		out.Items = append(out.Items, i)
	}
	out.Returned = len(out.Items)
	out.Truncated = end < len(items)
	return out, nil
}

func (s *Service) Show(ctx context.Context, ref string) (model.Item, error) {
	if _, err := model.ParseRef(ref); err != nil {
		return model.Item{}, Fail("usage", err.Error())
	}
	r, err := store.Read(ctx, s.Paths.Cache, s.Config.Active(), search.Query{Ref: ref})
	if err != nil {
		return model.Item{}, err
	}
	pins, err := store.Pins(ctx, s.Paths.Data)
	if err != nil {
		return model.Item{}, err
	}
	for _, i := range r.Items {
		if i.Ref == ref {
			_, i.Pinned = pins[ref]
			return i, nil
		}
	}
	if i, ok := pins[ref]; ok {
		return i, nil
	}
	return model.Item{}, Fail("not_found", "item is not in the current cache; fetch its configured source first")
}
func (s *Service) Pin(ctx context.Context, ref string, add bool) error {
	if _, err := model.ParseRef(ref); err != nil {
		return Fail("usage", err.Error())
	}
	if !add {
		return store.Pin(ctx, s.Paths.Data, model.Item{Ref: ref}, false)
	}
	i, err := s.Show(ctx, ref)
	if err != nil {
		return err
	}
	if !i.Available {
		return Fail("unavailable", "cannot add a pin without a current cached item")
	}
	return store.Pin(ctx, s.Paths.Data, i, true)
}

func (s *Service) Fetch(ctx context.Context, sourceID string, dueOnly bool) (FetchResult, error) {
	result := FetchResult{Sources: []model.SourceStatus{}}
	active := s.Config.Active()
	if sourceID != "" {
		found := false
		for _, c := range active {
			if c.ID == sourceID {
				found = true
			}
		}
		if !found {
			return result, Fail("usage", "unknown or disabled source: "+sourceID)
		}
	}
	if len(active) == 0 {
		return result, nil
	}
	if s.Fetcher == nil {
		return result, Fail("unavailable", "no source fetcher configured")
	}
	if err := os.MkdirAll(s.Paths.State, 0700); err != nil {
		return result, err
	}
	lock := flock.New(filepath.Join(s.Paths.State, "fetch.lock"), flock.SetPermissions(0600))
	ok, err := lock.TryLock()
	if err != nil {
		return result, err
	}
	if !ok {
		return result, Fail("busy", "another fetch is running; cached queries remain available")
	}
	defer lock.Unlock()
	old, err := s.Sources(ctx)
	if err != nil {
		return result, err
	}
	byID := map[string]model.SourceStatus{}
	for _, st := range old {
		byID[st.ID] = st
	}
	var jobs []config.Source
	var backoffs []string
	for _, src := range active {
		if sourceID != "" && sourceID != src.ID {
			continue
		}
		st := byID[src.ID]
		now := s.Now()
		if st.RetryAfter != nil && now.Before(*st.RetryAfter) {
			if !dueOnly {
				backoffs = append(backoffs, src.ID)
			}
			continue
		}
		if dueOnly {
			interval := src.Interval(s.Config.Interval())
			if interval == 0 {
				continue
			}
			if st.LastAttempt != nil && now.Sub(*st.LastAttempt) < interval {
				continue
			}
		}
		jobs = append(jobs, src)
	}
	if len(jobs) == 0 && len(backoffs) > 0 {
		return FetchResult{Sources: old}, Fail("backoff", "sources in retry backoff: "+strings.Join(backoffs, ", "))
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 2)
	var mu sync.Mutex
	failures := append([]string(nil), backoffs...)
	for _, src := range jobs {
		src := src
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			now := s.Now()
			st := byID[src.ID]
			st.LastAttempt = &now
			child, cancel := context.WithTimeout(ctx, 2*time.Minute)
			snap, e := s.Fetcher.Fetch(child, src)
			cancel()
			if e == nil {
				e = snap.Validate(s.Now())
			}
			if e == nil && src.Scope != "" && snap.Scope != src.Scope {
				e = errors.New("snapshot scope differs from configured source")
			}
			if e == nil {
				stamp := s.Now()
				st.Scope = snap.Scope
				st.ObservedAt = &snap.ObservedAt
				st.LastSuccess = &stamp
				st.Error = ""
				st.RetryAfter = nil
				st.Complete = true
				st.Count = len(snap.Items)
				b, _ := json.Marshal(snap)
				st.Revision = model.Digest(string(b))
				st.State = "fresh"
				// SQLite writes serialize; do not make competing transactions contend mid-upgrade.
				mu.Lock()
				e = store.WriteSnapshot(ctx, s.Paths.Cache, src, snap, st, active)
				mu.Unlock()
			}
			if e != nil {
				if ctx.Err() != nil {
					return
				}
				// Preserve last-good completeness/times when this attempt was not committed.
				st = byID[src.ID]
				st.LastAttempt = &now
				st.Error = model.Plain(e.Error())
				st.State = "failed"
				retry := s.Now().Add(time.Minute)
				var limited interface{ RetryTime() time.Time }
				if errors.As(e, &limited) && limited.RetryTime().After(retry) {
					retry = limited.RetryTime()
				}
				st.RetryAfter = &retry
				mu.Lock()
				writeErr := store.WriteStatus(ctx, s.Paths.Cache, src, st)
				failures = append(failures, src.ID)
				if writeErr != nil {
					failures = append(failures, "cache write failed")
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	finalCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	result.Sources, err = s.Sources(finalCtx)
	if err != nil {
		return result, err
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		return result, Fail("fetch_failed", "refresh failed for: "+strings.Join(failures, ", ")+"; previous complete snapshots retained")
	}
	return result, nil
}

func SourceURL(i model.Item) (string, error) {
	if !i.Available {
		return "", Fail("unavailable", "item is not in the current source snapshot")
	}
	u, err := url.Parse(i.URL)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return "", Fail("unsafe_url", "item has no permitted HTTP(S) source URL")
	}
	if (i.Origin.Provider == "github" || i.Origin.Provider == "gitlab") && !strings.EqualFold(u.Host, i.Origin.Authority) {
		return "", Fail("unsafe_url", "URL host differs from the source authority")
	}
	return u.String(), nil
}
func (s *Service) Open(ctx context.Context, ref string) error {
	i, err := s.Show(ctx, ref)
	if err != nil {
		return err
	}
	target, err := SourceURL(i)
	if err != nil {
		return err
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "open", target)
	case "linux":
		cmd = exec.CommandContext(ctx, "xdg-open", target)
	default:
		return Fail("unsupported", "native opening is supported on macOS and Linux")
	}
	if err = cmd.Run(); err != nil {
		return fmt.Errorf("could not open source URL with the system browser")
	}
	return nil
}
