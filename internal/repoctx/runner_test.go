package repoctx

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

// Only the test binary is used for subprocess failure/output/deadline fixtures.
func TestRunnerFixtureProcess(t *testing.T) {
	mode := ""
	for n, arg := range os.Args {
		if arg == "--repoctx-fixture" && n+1 < len(os.Args) {
			mode = os.Args[n+1]
		}
	}
	switch mode {
	case "stdout":
		_, _ = os.Stdout.Write(bytes.Repeat([]byte("PRIVATE"), 2048))
	case "stderr":
		_, _ = os.Stderr.Write(bytes.Repeat([]byte("PRIVATE"), 2048))
	case "combined":
		_, _ = os.Stdout.Write(bytes.Repeat([]byte("a"), 800))
		_, _ = os.Stderr.Write(bytes.Repeat([]byte("b"), 800))
	case "wait":
		time.Sleep(10 * time.Second)
	case "environment":
		input, _ := io.ReadAll(os.Stdin)
		env := map[string]string{"stdin": string(input)}
		for _, key := range []string{"GIT_OPTIONAL_LOCKS", "GIT_TERMINAL_PROMPT", "GIT_DIR", "GIT_WORK_TREE", "GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_0", "GIT_CONFIG_VALUE_0", "GIT_TRACE", "GIT_TRACE2", "GIT_TRACE2_EVENT", "GIT_TRACE2_PERF", "GIT_PAGER", "LC_ALL"} {
			env[key] = os.Getenv(key)
		}
		_ = json.NewEncoder(os.Stdout).Encode(env)
	case "exit":
		fmt.Fprint(os.Stdout, "PRIVATE stdout")
		fmt.Fprint(os.Stderr, "PRIVATE stderr")
		os.Exit(7)
	default:
		return
	}
	os.Exit(0)
}

func helperArgs(mode string) []string {
	return []string{"-test.run=^TestRunnerFixtureProcess$", "--", "--repoctx-fixture", mode}
}

func TestRunnerBoundsBothStreams(t *testing.T) {
	for _, mode := range []string{"stdout", "stderr", "combined"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			out, err := runCommand(ctx, os.Args[0], helperArgs(mode), 1024)
			if !errors.Is(err, errOutputLimit) || out != "" {
				t.Fatalf("output not bounded/discarded: size=%d, err=%v", len(out), err)
			}
		})
	}
}

func TestRunnerCancellation(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		want := context.Canceled
		if deadline {
			cancel()
			ctx, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
			want = context.DeadlineExceeded
		} else {
			time.AfterFunc(100*time.Millisecond, cancel)
		}
		started := time.Now()
		out, err := runCommand(ctx, os.Args[0], helperArgs("wait"), 1024)
		cancel()
		if !errors.Is(err, want) || out != "" || time.Since(started) > 2*time.Second {
			t.Fatalf("lost cancellation: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := runCommand(ctx, "must-never-start", nil, 1024)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("started command after cancellation: %v", err)
	}
}

func TestRunnerEnvironment(t *testing.T) {
	for _, key := range []string{"GIT_OPTIONAL_LOCKS", "GIT_TERMINAL_PROMPT", "GIT_DIR", "GIT_WORK_TREE", "GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_0", "GIT_CONFIG_VALUE_0", "GIT_TRACE", "GIT_TRACE2", "GIT_TRACE2_EVENT", "GIT_TRACE2_PERF", "GIT_PAGER", "LC_ALL"} {
		t.Setenv(key, "unsafe-inherited-value")
	}
	out, err := runCommand(context.Background(), os.Args[0], helperArgs("environment"), 4096)
	if err != nil {
		t.Fatal(err)
	}
	var env map[string]string
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatal(err)
	}
	for key, value := range env {
		want := ""
		switch key {
		case "GIT_OPTIONAL_LOCKS", "GIT_TERMINAL_PROMPT", "GIT_TRACE2", "GIT_TRACE2_EVENT", "GIT_TRACE2_PERF":
			want = "0"
		case "LC_ALL":
			want = "C"
		}
		if value != want {
			t.Fatalf("unsafe environment %s=%q", key, value)
		}
	}
}

func TestRunnerFailureNeverLeaksOutput(t *testing.T) {
	result, err := detect(context.Background(), "/fixture", nil, func(ctx context.Context, _ string, _ ...string) (string, error) {
		return runCommand(ctx, os.Args[0], helperArgs("exit"), 1024)
	})
	if strings.Contains(fmt.Sprint(result, err), "PRIVATE") {
		t.Fatal("subprocess output leaked")
	}
}

func TestDetectMissingGitExecutable(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	result, err := Detect(context.Background(), t.TempDir(), nil)
	if err != nil || len(result.Targets) != 0 || result.Preferred != -1 || result.Reason == "" {
		t.Fatalf("missing git should be normal: %#v, %v", result, err)
	}
}
