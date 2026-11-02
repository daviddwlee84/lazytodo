package repoctx

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const maxOutput = 64 << 10

var errOutputLimit = errors.New("Git inspection output exceeded its limit")

func runGit(ctx context.Context, cwd string, args ...string) (string, error) {
	fixed := []string{"--no-pager", "-c", "core.fsmonitor=false", "-c", "color.ui=false", "-C", cwd}
	return runCommand(ctx, "git", append(fixed, args...), maxOutput)
}

// runCommand is separated only so tests can run a fixture binary, never a shell.
func runCommand(ctx context.Context, name string, args []string, limit int) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	if limit <= 0 || limit > maxOutput {
		limit = maxOutput
	}
	budget := &outputBudget{remaining: limit, cancel: cancel}
	stdout := &boundedOutput{budget: budget, retain: true}
	cmd := exec.CommandContext(child, name, args...)
	cmd.Env = gitEnvironment(os.Environ())
	cmd.Stdout = stdout
	cmd.Stderr = &boundedOutput{budget: budget} // Count, but never retain stderr.
	cmd.WaitDelay = 100 * time.Millisecond
	err := cmd.Run()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if budget.exceeded {
		return "", errOutputLimit
	}
	if err != nil {
		return "", err
	}
	return stdout.buffer.String(), nil
}

type outputBudget struct {
	mu        sync.Mutex
	remaining int
	exceeded  bool
	cancel    context.CancelFunc
}

type boundedOutput struct {
	budget *outputBudget
	retain bool
	buffer bytes.Buffer
}

func (w *boundedOutput) Write(p []byte) (int, error) {
	w.budget.mu.Lock()
	defer w.budget.mu.Unlock()
	n := min(len(p), w.budget.remaining)
	if w.retain {
		w.buffer.Write(p[:n])
	}
	w.budget.remaining -= n
	if n != len(p) {
		w.budget.exceeded = true
		w.budget.cancel()
		return n, errOutputLimit
	}
	return n, nil
}

func gitEnvironment(env []string) []string {
	result := make([]string, 0, len(env)+3)
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		// Honor explicit Git config-file isolation, but discard inherited repo
		// overrides, injected config, tracing (which can write files), and pagers.
		if strings.HasPrefix(key, "GIT_") && key != "GIT_CONFIG_NOSYSTEM" && key != "GIT_CONFIG_SYSTEM" && key != "GIT_CONFIG_GLOBAL" {
			continue
		}
		if key != "LC_ALL" {
			result = append(result, entry)
		}
	}
	return append(result, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C",
		"GIT_TRACE2=0", "GIT_TRACE2_EVENT=0", "GIT_TRACE2_PERF=0")
}
