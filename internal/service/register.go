package service

import (
	"context"

	"lazytodo/internal/config"
)

// RegisterSource explicitly saves a native source but never mutates this
// Service: callers may still have reads/fetches using its configuration snapshot.
// Fetch is separate so a TUI can queue it after an already-running refresh.
func (s *Service) RegisterSource(ctx context.Context, target config.Source) (*Service, config.SourceSave, string, error) {
	draft, err := config.ReadSourceDraft(s.Paths.Config)
	if err != nil {
		return nil, config.SourceSave{}, "", err
	}
	plan, err := draft.PlanSources([]config.Source{target})
	if err != nil {
		return nil, config.SourceSave{}, "", err
	}
	var registered config.Source
	if len(plan.Added) > 0 {
		registered = plan.Added[0]
	} else if len(plan.Skipped) > 0 {
		registered = plan.Skipped[0]
	}
	if registered.Disabled {
		return nil, config.SourceSave{}, "", Fail("source_disabled", "This repository is configured but disabled; enable it explicitly with config edit.")
	}
	result, err := plan.Save(ctx)
	if err != nil {
		return nil, result, "", err
	}
	next := *s
	next.Config = s.Config
	next.Config.Sources = append(append([]config.Source(nil), draft.Config.Sources...), plan.Added...)
	return &next, result, registered.ID, nil
}
