package service

import (
	"context"
	"time"

	"lazytodo/internal/config"
	"lazytodo/internal/model"
)

type Fetcher interface {
	Fetch(context.Context, config.Source) (model.Snapshot, error)
}
type Service struct {
	Config           config.Config
	Paths            config.Paths
	Fetcher          Fetcher
	Now              func() time.Time
	ColorOverride    *string
	IntervalOverride *string
}

func New(c config.Config, p config.Paths, f Fetcher) *Service {
	return &Service{Config: c, Paths: p, Fetcher: f, Now: time.Now}
}

type QueryRequest struct {
	Text        string
	Limit       int
	Offset      int
	Revision    string
	IncludeBody bool
}
type QueryResult struct {
	Items     []model.Item         `json:"items"`
	Sources   []model.SourceStatus `json:"sources"`
	Total     int                  `json:"total"`
	Returned  int                  `json:"returned"`
	Offset    int                  `json:"offset"`
	Limit     int                  `json:"limit"`
	Truncated bool                 `json:"truncated"`
	Revision  string               `json:"revision"`
}
type FetchResult struct {
	Sources []model.SourceStatus `json:"sources"`
}
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string        { return e.Message }
func Fail(code, message string) error { return &Error{code, message} }
