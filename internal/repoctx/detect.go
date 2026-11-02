// Package repoctx recognizes configured forge identities from a local checkout.
// Detection is read-only: it does not contact remotes or persist any state.
package repoctx

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"lazytodo/internal/config"
)

type Target struct {
	Kind   string
	Host   string
	Scope  string
	Remote string
}

type Result struct {
	Root      string
	Targets   []Target
	Preferred int // -1 when there is no unique preferred recognized remote.
	Reason    string
}

const (
	detectionTimeout = 2 * time.Second
	maxRemotes       = 16
)

var errInspect = errors.New("could not inspect Git repository")

type gitRunner func(context.Context, string, ...string) (string, error)

// Detect inspects cwd (including nested directories and linked worktrees).
// sources supplies the only known custom forge authorities, including disabled
// sources. Unsupported repositories and missing Git are ordinary empty results;
// cancellation and inspection failures return errors without subprocess output.
func Detect(ctx context.Context, cwd string, sources []config.Source) (Result, error) {
	return detect(ctx, cwd, sources, runGit)
}

func detect(ctx context.Context, cwd string, sources []config.Source, run gitRunner) (result Result, err error) {
	ctx, cancel := context.WithTimeout(ctx, detectionTimeout)
	defer cancel()
	result = Result{Targets: []Target{}, Preferred: -1}
	defer func() {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		if err != nil {
			// An interrupted scan must not look like a complete selection.
			result.Targets = []Target{}
			result.Preferred = -1
			result.Reason = "Repository detection could not complete; configure a source manually."
		}
	}()
	if err = ctx.Err(); err != nil {
		return result, err
	}
	root, e := run(ctx, cwd, "rev-parse", "--show-toplevel")
	if e != nil {
		if errors.Is(e, exec.ErrNotFound) {
			result.Reason = "Git is unavailable; configure a source manually."
			return result, nil
		}
		var exit *exec.ExitError
		if errors.As(e, &exit) {
			result.Reason = "Current directory is not a Git working tree."
			return result, nil
		}
		return result, inspectionError(e)
	}
	root = strings.TrimSuffix(root, "\n")
	if !filepath.IsAbs(root) || !safeText(root) {
		return result, errInspect
	}
	result.Root = root

	out, e := run(ctx, cwd, "remote")
	if e != nil {
		return result, inspectionError(e)
	}
	if out == "" {
		result.Reason = "This repository has no remotes; configure a source manually."
		return result, nil
	}
	remotes := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	upstream, e := upstreamRemote(ctx, cwd, run)
	if e != nil {
		return result, e
	}
	// Prioritize explicit preferences even when the repository exceeds our cap.
	ordered := make([]string, 0, maxRemotes)
	seen := map[string]bool{}
	add := func(remote string) {
		if !seen[remote] && validRemote(remote) {
			seen[remote] = true
			ordered = append(ordered, remote)
		}
	}
	for _, preferred := range []string{upstream, "origin"} {
		for _, remote := range remotes {
			if remote == preferred {
				add(remote)
			}
		}
	}
	for _, remote := range remotes {
		add(remote)
	}
	incomplete := len(ordered) != len(remotes) || len(ordered) > maxRemotes
	if len(ordered) > maxRemotes {
		ordered = ordered[:maxRemotes]
	}
	identities := map[string]int{}
	byRemote := map[string]int{}
	for _, remote := range ordered {
		if e := ctx.Err(); e != nil {
			return result, e
		}
		// Default get-url returns the fetch URL (not pushurl), and safely expands
		// insteadOf without invoking a transport, SSH configuration, or helper.
		raw, e := run(ctx, cwd, "remote", "get-url", "--", remote)
		if e != nil {
			return result, inspectionError(e)
		}
		target, ok := parseRemote(strings.TrimSuffix(raw, "\n"), sources)
		if !ok {
			continue
		}
		target.Remote = remote
		key := config.SourceKey(config.Source{Kind: target.Kind, Host: target.Host, Scope: target.Scope})
		index, exists := identities[key]
		if !exists {
			index = len(result.Targets)
			identities[key] = index
			result.Targets = append(result.Targets, target)
		}
		byRemote[remote] = index
	}
	for _, remote := range []string{upstream, "origin"} {
		if index, exists := byRemote[remote]; exists {
			result.Preferred = index
			result.Targets[index].Remote = remote
			return result, nil
		}
	}
	switch {
	case incomplete:
		result.Reason = "Not all remotes could be inspected; choose a target or configure a source manually."
	case len(result.Targets) == 1:
		result.Preferred = 0
	case len(result.Targets) == 0:
		result.Reason = "No supported remote was recognized; configure a source manually for custom hosts or SSH aliases."
	default:
		result.Reason = "Multiple repository targets were recognized; choose a target."
	}
	return result, nil
}

func upstreamRemote(ctx context.Context, cwd string, run gitRunner) (string, error) {
	ref, err := run(ctx, cwd, "symbolic-ref", "--quiet", "HEAD")
	var exit *exec.ExitError
	if errors.As(err, &exit) { // Detached HEAD has no branch upstream.
		return "", nil
	}
	if err != nil {
		return "", inspectionError(err)
	}
	ref = strings.TrimSuffix(ref, "\n")
	if !strings.HasPrefix(ref, "refs/heads/") || !safeText(ref) {
		return "", errInspect
	}
	remote, err := run(ctx, cwd, "for-each-ref", "--count=1", "--format=%(upstream:remotename)", "--", ref)
	if err != nil {
		return "", inspectionError(err)
	}
	remote = strings.TrimSuffix(remote, "\n")
	if !validRemote(remote) {
		return "", nil
	}
	return remote, nil
}

func validRemote(remote string) bool {
	if len(remote) == 0 || len(remote) > 256 || remote[0] == '-' || remote == "." {
		return false
	}
	for _, c := range remote {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("._-/", c)) {
			return false
		}
	}
	return true
}

func inspectionError(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		return context.DeadlineExceeded
	case errors.Is(err, errOutputLimit):
		return errOutputLimit
	default:
		return errInspect
	}
}
