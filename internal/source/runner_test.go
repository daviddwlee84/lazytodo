package source

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// The only real subprocess used by tests is this fixture in the test binary.
// It never invokes gh/glab, reads credentials, or makes network calls.
func TestRunnerProcess(t *testing.T) {
	mode := ""
	for n, arg := range os.Args {
		if arg == "--" && n+1 < len(os.Args) {
			mode = os.Args[n+1]
			break
		}
	}
	switch mode {
	case "stdout":
		_, _ = os.Stdout.Write(bytes.Repeat([]byte("PRIVATE"), 2048))
		os.Exit(0)
	case "stderr":
		_, _ = os.Stderr.Write(bytes.Repeat([]byte("PRIVATE"), 2048))
		os.Exit(0)
	case "combined":
		_, _ = os.Stdout.Write(bytes.Repeat([]byte("a"), 800))
		_, _ = os.Stderr.Write(bytes.Repeat([]byte("b"), 800))
		os.Exit(0)
	case "wait":
		time.Sleep(10 * time.Second)
		os.Exit(0)
	case "environment":
		input, _ := io.ReadAll(os.Stdin)
		env := map[string]string{"stdin": string(input)}
		for _, key := range []string{"GH_PROMPT_DISABLED", "GH_NO_UPDATE_NOTIFIER", "GH_NO_EXTENSION_UPDATE_NOTIFIER", "GH_PAGER", "GLAB_PAGER", "PAGER", "GLAB_CHECK_UPDATE", "NO_PROMPT", "CI", "GH_FORCE_TTY", "GLAB_FORCE_TTY"} {
			env[key] = os.Getenv(key)
		}
		_ = json.NewEncoder(os.Stdout).Encode(env)
		os.Exit(0)
	case "exit":
		fmt.Fprint(os.Stderr, "PRIVATE token")
		os.Exit(7)
	}
}

func helperArgs(mode string) []string { return []string{"-test.run=^TestRunnerProcess$", "--", mode} }

func TestCommandRunnerBoundsBothStreams(t *testing.T) {
	for _, mode := range []string{"stdout", "stderr", "combined"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			result, err := (commandRunner{limit: 1024}).Run(ctx, os.Args[0], helperArgs(mode), nil)
			if !errors.Is(err, errOutputLimit) {
				t.Fatalf("want bounded output failure, got %v", err)
			}
			if len(result.Stdout)+len(result.Stderr) > 1024 {
				t.Fatalf("runner retained too much output: %d", len(result.Stdout)+len(result.Stderr))
			}
		})
	}
}

func TestCommandRunnerCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := (commandRunner{limit: 1024}).Run(ctx, os.Args[0], helperArgs("wait"), nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline lost: %v", err)
	}
	if time.Since(started) > 2*time.Second {
		t.Fatal("child ignored caller deadline")
	}
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	_, err = (commandRunner{}).Run(ctx, "must-never-start", nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled runner started process: %v", err)
	}
}

func TestCommandRunnerNoninteractiveEnvironment(t *testing.T) {
	t.Setenv("GH_PAGER", "unsafe-pager")
	t.Setenv("GLAB_PAGER", "unsafe-pager")
	t.Setenv("GH_FORCE_TTY", "1")
	t.Setenv("GLAB_FORCE_TTY", "1")
	result, err := (commandRunner{limit: 4096}).Run(context.Background(), os.Args[0], helperArgs("environment"), []byte("fixture input"))
	if err != nil {
		t.Fatal(err)
	}
	var env map[string]string
	if err := json.Unmarshal(result.Stdout, &env); err != nil {
		t.Fatalf("bad environment fixture: %v", err)
	}
	expected := map[string]string{"GH_PROMPT_DISABLED": "1", "GH_NO_UPDATE_NOTIFIER": "1", "GH_NO_EXTENSION_UPDATE_NOTIFIER": "1", "GH_PAGER": "cat", "GLAB_PAGER": "cat", "PAGER": "cat", "GLAB_CHECK_UPDATE": "false", "NO_PROMPT": "true", "CI": "true", "GH_FORCE_TTY": "", "GLAB_FORCE_TTY": "", "stdin": "fixture input"}
	for key, value := range expected {
		if env[key] != value {
			t.Fatalf("%s: wanted %q got %q", key, value, env[key])
		}
	}
}

func TestCommandFailureDoesNotExposeOutput(t *testing.T) {
	f := &Fetcher{Runner: runnerFunc(func(ctx context.Context, _ string, _ []string, _ []byte) (Result, error) {
		return (commandRunner{limit: 4096}).Run(ctx, os.Args[0], helperArgs("exit"), nil)
	})}
	snapshot, err := f.Fetch(context.Background(), ghConfig())
	requireFailure(t, snapshot, err, "command_failed")
	if strings.Contains(err.Error(), "exit status") {
		t.Fatalf("raw command error exposed: %v", err)
	}
}
