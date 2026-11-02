package service

import "lazytodo/internal/config"

// ReloadConfig returns a new service snapshot. In-flight operations keep their
// original immutable configuration; explicit launch flags still outrank the file.
func (s *Service) ReloadConfig() (*Service, error) {
	cfg, err := config.LoadForReload(s.Paths.Config, s.ColorOverride, s.IntervalOverride)
	if err != nil {
		return nil, err
	}
	next := *s
	next.Config = cfg
	return &next, nil
}
