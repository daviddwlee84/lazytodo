package source

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

// Result holds bounded child output; adapters never expose it in errors.
type Result struct{ Stdout, Stderr []byte }

// Runner permits fixture-only tests without invoking authenticated provider CLIs.
type Runner interface {
	Run(context.Context, string, []string, []byte) (Result, error)
}

var errOutputLimit = errors.New("command output exceeded limit")

type commandRunner struct{ limit int }

func (r commandRunner) Run(ctx context.Context, name string, args []string, input []byte) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	limit := r.limit
	if limit <= 0 || limit > maxBytes {
		limit = maxBytes
	}
	budget := &outputBudget{remaining: limit, cancel: cancel}
	stdout, stderr := &boundedOutput{budget: budget}, &boundedOutput{budget: budget}
	cmd := exec.CommandContext(childCtx, name, args...)
	cmd.Env = childEnvironment(os.Environ())
	cmd.Stdin = bytes.NewReader(input)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	result := Result{Stdout: stdout.buffer.Bytes(), Stderr: stderr.buffer.Bytes()}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if budget.exceeded {
		return result, errOutputLimit
	}
	return result, err
}

type outputBudget struct {
	mu        sync.Mutex
	remaining int
	exceeded  bool
	cancel    context.CancelFunc
}
type boundedOutput struct {
	budget *outputBudget
	buffer bytes.Buffer
}

func (w *boundedOutput) Write(p []byte) (int, error) {
	w.budget.mu.Lock()
	defer w.budget.mu.Unlock()
	count := len(p)
	if count > w.budget.remaining {
		count = w.budget.remaining
		w.buffer.Write(p[:count])
		w.budget.remaining = 0
		w.budget.exceeded = true
		w.budget.cancel()
		return count, errOutputLimit
	}
	w.buffer.Write(p)
	w.budget.remaining -= count
	return count, nil
}

func childEnvironment(env []string) []string {
	fixed := map[string]string{
		"GH_PROMPT_DISABLED": "1", "GH_NO_UPDATE_NOTIFIER": "1", "GH_NO_EXTENSION_UPDATE_NOTIFIER": "1",
		"GH_PAGER": "cat", "GLAB_PAGER": "cat", "PAGER": "cat", "GIT_PAGER": "cat",
		"GLAB_CHECK_UPDATE": "false", "GLAB_SEND_TELEMETRY": "false", "NO_PROMPT": "true", "CI": "true",
		"TERM": "dumb", "NO_COLOR": "1", "CLICOLOR": "0", "CLICOLOR_FORCE": "0",
	}
	result := make([]string, 0, len(env)+len(fixed))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if _, replace := fixed[key]; replace || key == "GH_FORCE_TTY" || key == "GLAB_FORCE_TTY" {
			continue
		}
		result = append(result, entry)
	}
	for key, value := range fixed {
		result = append(result, key+"="+value)
	}
	return result
}
