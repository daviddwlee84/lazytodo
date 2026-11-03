package source

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
)

// HTTPDoer permits fixture-only tests of token-authenticated sources that have
// no native CLI. Implementations must not follow redirects.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// defaultHTTP refuses redirects: every request keeps the fixed, explicitly
// configured endpoint, and a credential is never replayed to another host.
var defaultHTTP HTTPDoer = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// post sends one fixed request under the same aggregate page/byte budget as
// CLI reads. The caller supplies a fixed endpoint; the body is never surfaced
// in errors.
func (s *fetchState) post(ctx context.Context, endpoint string, header http.Header, payload []byte) ([]byte, http.Header, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, 0, err
	}
	if s.pages >= maxPages {
		return nil, nil, 0, failure("page_limit", "Source exceeded the pagination safety limit; previous data retained")
	}
	if s.bytes >= maxBytes {
		return nil, nil, 0, failure("size_limit", "Source exceeded the 64 MiB read limit; previous data retained")
	}
	s.pages++
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, nil, 0, failure("invalid_config", "Source request could not be constructed")
	}
	req.Header = header.Clone()
	doer := s.http
	if doer == nil {
		doer = defaultHTTP
	}
	resp, err := doer.Do(req)
	if ctxErr := ctx.Err(); ctxErr != nil {
		if resp != nil {
			resp.Body.Close()
		}
		return nil, nil, 0, ctxErr
	}
	if err != nil {
		return nil, nil, 0, failure("network", "Source API request failed; verify network access; previous data retained")
	}
	defer resp.Body.Close()
	remaining := maxBytes - s.bytes
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(remaining)+1))
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, nil, 0, ctxErr
	}
	if len(body) > remaining {
		return nil, nil, 0, failure("size_limit", "Source exceeded the 64 MiB read limit; previous data retained")
	}
	s.bytes += len(body)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, nil, 0, failure("network", "Source API response was interrupted; previous data retained")
	}
	return body, resp.Header, resp.StatusCode, nil
}
