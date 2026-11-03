// Package source reads complete source snapshots. It never writes to a provider.
package source

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"lazytodo/internal/config"
	"lazytodo/internal/model"
)

const maxBytes = 64 << 20
const maxPages = 10000

// FetchError contains only safe, application-owned diagnostics. Provider output
// and filesystem paths are deliberately never included in Message.
type FetchError struct {
	Code       string
	Message    string
	RetryAfter time.Time
}

func (e *FetchError) Error() string            { return e.Message }
func (e *FetchError) RetryTime() time.Time     { return e.RetryAfter }
func failure(code, message string) *FetchError { return &FetchError{Code: code, Message: message} }
func invalidResponse() error {
	return failure("invalid_response", "Source returned malformed or incomplete data; previous data retained")
}

// Fetcher is safe for concurrent Fetch calls if its injected Runner/HTTP are too.
// A nil Runner uses the bounded, noninteractive command runner; a nil HTTP uses
// a client that refuses redirects.
type Fetcher struct {
	Runner Runner
	HTTP   HTTPDoer
}

func New() *Fetcher { return &Fetcher{Runner: commandRunner{limit: maxBytes}} }

type fetchState struct {
	runner   Runner
	http     HTTPDoer
	token    string
	cfg      config.Source
	observed time.Time
	bytes    int
	pages    int
}

var hostname = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.-]*(?::[0-9]+)?$`)

func (f *Fetcher) Fetch(ctx context.Context, cfg config.Source) (model.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return model.Snapshot{}, err
	}
	var snapshot model.Snapshot
	var err error
	switch cfg.Kind {
	case "snapshot":
		snapshot, err = readSnapshot(ctx, cfg)
	case "linear":
		if cfg.Host == "" {
			cfg.Host = linearAuthority
		}
		cfg.Host = strings.ToLower(strings.TrimSuffix(cfg.Host, "."))
		if cfg.Host != linearAuthority || !config.LinearScope.MatchString(cfg.Scope) || cfg.TokenEnv == "" {
			return snapshot, failure("invalid_config", "Linear source requires scope workspace-url-key/TEAMKEY and token_env")
		}
		token, tokenErr := envToken(cfg.TokenEnv, "Linear")
		if tokenErr != nil {
			return snapshot, tokenErr
		}
		state := &fetchState{http: f.HTTP, token: token, cfg: cfg, observed: time.Now().UTC()}
		snapshot, err = state.linear(ctx)
	case "super-productivity":
		if cfg.Host == "" {
			cfg.Host = spAuthority
		}
		if cfg.Scope == "" {
			cfg.Scope = spScope
		}
		if cfg.Host != spAuthority || cfg.Scope != spScope || cfg.TokenEnv == "" {
			return snapshot, failure("invalid_config", "Super Productivity source requires host 127.0.0.1:3876, scope local and token_env")
		}
		token, tokenErr := envToken(cfg.TokenEnv, "Super Productivity")
		if tokenErr != nil {
			return snapshot, tokenErr
		}
		doer := f.HTTP
		if doer == nil {
			doer = loopbackHTTP
		}
		state := &fetchState{http: doer, token: token, cfg: cfg, observed: time.Now().UTC()}
		snapshot, err = state.superProductivity(ctx)
	case "github", "gitlab":
		if cfg.Host == "" {
			cfg.Host = cfg.Kind + ".com"
		}
		cfg.Host = strings.ToLower(strings.TrimSuffix(cfg.Host, "."))
		if !hostname.MatchString(cfg.Host) || !validScope(cfg.Kind, cfg.Scope) {
			return snapshot, failure("invalid_config", "Source requires an explicit hostname and full repository or project path")
		}
		runner := f.Runner
		if runner == nil {
			runner = commandRunner{limit: maxBytes}
		}
		state := &fetchState{runner: runner, cfg: cfg, observed: time.Now().UTC()}
		if cfg.Kind == "github" {
			snapshot, err = state.github(ctx)
		} else {
			snapshot, err = state.gitlab(ctx)
		}
	default:
		return snapshot, failure("unsupported_source", "Unsupported source kind")
	}
	if err != nil {
		return model.Snapshot{}, err
	}
	if err := ctx.Err(); err != nil {
		return model.Snapshot{}, err
	}
	if err := snapshot.Validate(time.Now()); err != nil {
		return model.Snapshot{}, failure("invalid_snapshot", "Source snapshot failed validation; previous data retained")
	}
	if cfg.Kind == "snapshot" && cfg.Scope != "" && cfg.Scope != snapshot.Scope {
		return model.Snapshot{}, failure("scope_mismatch", "Snapshot scope does not match the configured scope")
	}
	for n := range snapshot.Items {
		snapshot.Items[n].SourceID = cfg.ID
	}
	return snapshot, nil
}

func validScope(kind, scope string) bool {
	parts := strings.Split(scope, "/")
	if len(parts) < 2 || (kind == "github" && len(parts) != 2) {
		return false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "?#\\\x00\r\n{}: \t") {
			return false
		}
	}
	return true
}

func (s *fetchState) snapshot(items []model.Item) model.Snapshot {
	return model.Snapshot{SchemaVersion: model.SchemaVersion, Scope: s.cfg.Scope, ObservedAt: s.observed, Complete: true, Items: items}
}

func readSnapshot(ctx context.Context, cfg config.Source) (model.Snapshot, error) {
	var snapshot model.Snapshot
	if !filepath.IsAbs(cfg.Path) {
		return snapshot, failure("invalid_config", "Snapshot requires an explicit absolute file path")
	}
	info, err := os.Stat(cfg.Path)
	if err != nil || !info.Mode().IsRegular() {
		return snapshot, failure("snapshot_read", "Cannot read the configured snapshot file")
	}
	if info.Size() > maxBytes {
		return snapshot, failure("size_limit", "Snapshot exceeds the 64 MiB limit")
	}
	file, err := os.Open(cfg.Path)
	if err != nil {
		return snapshot, failure("snapshot_read", "Cannot read the configured snapshot file")
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return snapshot, failure("snapshot_read", "Snapshot must be a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if ctx.Err() != nil {
		return snapshot, ctx.Err()
	}
	if err != nil {
		return snapshot, failure("snapshot_read", "Cannot read the configured snapshot file")
	}
	if len(data) > maxBytes {
		return snapshot, failure("size_limit", "Snapshot exceeds the 64 MiB limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&snapshot) != nil || decoder.Decode(new(any)) != io.EOF {
		return model.Snapshot{}, failure("invalid_snapshot", "Snapshot is not a valid snapshot document")
	}
	return snapshot, nil
}

// api expects --include output, retaining headers for pagination and retry hints.
// The budget spans ALL item/widget pages, not merely an individual child process.
func (s *fetchState) api(ctx context.Context, command string, args []string, input []byte) ([]byte, http.Header, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if s.pages >= maxPages {
		return nil, nil, failure("page_limit", "Source exceeded the pagination safety limit; previous data retained")
	}
	s.pages++
	result, runErr := s.runner.Run(ctx, command, args, input)
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	size := len(result.Stdout) + len(result.Stderr)
	if size > maxBytes-s.bytes || errors.Is(runErr, errOutputLimit) {
		return nil, nil, failure("size_limit", "Source exceeded the 64 MiB read limit; previous data retained")
	}
	s.bytes += size
	body, headers, status, parseErr := parseResponse(result.Stdout)
	// Some CLI versions exit unsuccessfully for a valid GraphQL error body.
	// Preserve its safe classification without accepting any partial result.
	if command == "glab" && parseErr == nil && (status == 400 || (status >= 200 && status < 300 && runErr != nil)) {
		var envelope struct {
			Errors []json.RawMessage `json:"errors"`
		}
		if json.Unmarshal(body, &envelope) == nil && len(envelope.Errors) > 0 {
			return nil, nil, graphQLFailure(envelope.Errors)
		}
	}
	if parseErr == nil && (status < 200 || status >= 300) {
		return nil, nil, httpFailure(status, headers)
	}
	if runErr != nil {
		return nil, nil, failure("command_failed", "Source CLI failed; verify installation, authentication and read access")
	}
	if parseErr != nil {
		return nil, nil, invalidResponse()
	}
	return body, headers, nil
}

func parseResponse(data []byte) ([]byte, http.Header, int, error) {
	reader := bufio.NewReader(bytes.NewReader(data))
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, nil, 0, err
	}
	parts := strings.Fields(line)
	if len(parts) < 2 || !strings.HasPrefix(parts[0], "HTTP/") {
		return nil, nil, 0, invalidResponse()
	}
	status, err := strconv.Atoi(parts[1])
	if err != nil || status < 100 || status > 599 {
		return nil, nil, 0, invalidResponse()
	}
	headers, err := textproto.NewReader(reader).ReadMIMEHeader()
	if err != nil {
		return nil, nil, 0, err
	}
	body, err := io.ReadAll(reader)
	return body, http.Header(headers), status, err
}

func httpFailure(status int, headers http.Header) error {
	e := failure("http_error", "Source API request failed; previous data retained")
	switch status {
	case 401:
		e.Code = "authentication"
		e.Message = "Source authentication failed; authenticate with its CLI"
	case 403:
		e.Code = "access_denied"
		e.Message = "Source access denied; verify CLI credentials and read access"
	case 404:
		e.Code = "not_found"
		e.Message = "Configured source was not found or is not accessible"
	case 429:
		e.Code = "rate_limited"
		e.Message = "Source rate limit reached; retry later"
	}
	retry := headers.Get("Retry-After")
	if seconds, err := strconv.ParseInt(retry, 10, 32); err == nil && seconds >= 0 {
		e.RetryAfter = time.Now().Add(time.Duration(seconds) * time.Second)
	} else if date, err := http.ParseTime(retry); err == nil {
		e.RetryAfter = date
	}
	if (status == 403 || status == 429) && headers.Get("X-RateLimit-Remaining") == "0" {
		e.Code = "rate_limited"
		e.Message = "Source rate limit reached; retry later"
		if reset, err := strconv.ParseInt(headers.Get("X-RateLimit-Reset"), 10, 64); err == nil && reset > 0 && e.RetryAfter.IsZero() {
			e.RetryAfter = time.Unix(reset, 0)
		}
	}
	return e
}

func positiveID(id string) bool {
	if id == "" || id[0] == '0' {
		return false
	}
	for _, r := range id {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func normalizedState(state string) string {
	switch strings.ToLower(state) {
	case "open", "opened":
		return "open"
	case "closed":
		return "closed"
	default:
		return "unknown"
	}
}
