package repoctx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"lazytodo/internal/config"
)

func fixtureRunner(t *testing.T, remotes map[string]string, upstream string) gitRunner {
	t.Helper()
	return func(ctx context.Context, cwd string, args ...string) (string, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("inspection has no overall deadline")
		}
		switch strings.Join(args, " ") {
		case "rev-parse --show-toplevel":
			return "/fixture/root\n", nil
		case "remote":
			names := make([]string, 0, len(remotes))
			for name := range remotes {
				names = append(names, name)
			}
			sort.Strings(names)
			if len(names) == 0 {
				return "", nil
			}
			return strings.Join(names, "\n") + "\n", nil
		case "symbolic-ref --quiet HEAD":
			return "refs/heads/main\n", nil
		case "for-each-ref --count=1 --format=%(upstream:remotename) -- refs/heads/main":
			return upstream + "\n", nil
		}
		if len(args) == 4 && reflect.DeepEqual(args[:3], []string{"remote", "get-url", "--"}) {
			if raw, ok := remotes[args[3]]; ok {
				return raw + "\n", nil
			}
		}
		t.Fatalf("unexpected inspection command: %q", args)
		return "", errors.New("unexpected command")
	}
}

func TestDetectionSelection(t *testing.T) {
	const first = "https://github.com/team/first.git"
	const second = "https://gitlab.com/group/second.git"
	tests := []struct {
		name, upstream string
		remotes        map[string]string
		count          int
		preferred      string
	}{
		{"upstream before origin", "fork", map[string]string{"origin": first, "fork": second}, 2, "fork"},
		{"origin before other", "", map[string]string{"a": second, "origin": first}, 2, "origin"},
		{"unsupported upstream falls back", "fork", map[string]string{"origin": first, "fork": "git@unknown.example:group/repo"}, 1, "origin"},
		{"only recognized target", "", map[string]string{"local": "/tmp/repo", "mine": first}, 1, "mine"},
		{"ambiguous", "", map[string]string{"a": first, "b": second}, 2, ""},
		{"unsupported origin", "", map[string]string{"origin": "ext::PRIVATE", "a": first, "b": second}, 2, ""},
		{"deduplicate identities", "", map[string]string{"a": first, "b": "git@github.com:TEAM/FIRST.git"}, 1, "a"},
		{"duplicate keeps upstream", "z", map[string]string{"origin": first, "a": second, "z": "git@github.com:team/first"}, 2, "z"},
		{"duplicate keeps origin", "", map[string]string{"a": first, "origin": "git@github.com:team/first"}, 1, "origin"},
		{"all unsupported", "", map[string]string{"origin": "https://PRIVATE@unknown.example/group/repo?PRIVATE"}, 0, ""},
		{"no remotes", "", map[string]string{}, 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := detect(context.Background(), "/fixture/nested", nil, fixtureRunner(t, tt.remotes, tt.upstream))
			if err != nil {
				t.Fatal(err)
			}
			if result.Root != "/fixture/root" || len(result.Targets) != tt.count {
				t.Fatalf("unexpected result: %#v", result)
			}
			if tt.preferred == "" {
				if result.Preferred != -1 || result.Reason == "" {
					t.Fatalf("missing no-preference explanation: %#v", result)
				}
			} else if result.Preferred < 0 || result.Preferred >= len(result.Targets) || result.Targets[result.Preferred].Remote != tt.preferred {
				t.Fatalf("wanted preferred %s: %#v", tt.preferred, result)
			}
			if strings.Contains(fmt.Sprint(result), "PRIVATE") {
				t.Fatal("raw remote leaked")
			}
		})
	}
}

func TestDetectionRemoteCap(t *testing.T) {
	remotes := map[string]string{}
	for n := 0; n < maxRemotes+5; n++ {
		remotes[fmt.Sprintf("r%02d", n)] = "https://unknown.example/team/repo"
	}
	remotes["r00"] = "https://github.com/team/repo"
	for _, prefer := range []bool{false, true} {
		upstream := ""
		if prefer {
			upstream = "z-upstream"
			remotes[upstream] = "https://gitlab.com/group/repo"
		}
		base := fixtureRunner(t, remotes, upstream)
		calls := 0
		runner := func(ctx context.Context, cwd string, args ...string) (string, error) {
			if len(args) == 4 && args[1] == "get-url" {
				calls++
			}
			return base(ctx, cwd, args...)
		}
		result, err := detect(context.Background(), "/fixture", nil, runner)
		if err != nil || calls != maxRemotes {
			t.Fatalf("cap not enforced: calls=%d, err=%v", calls, err)
		}
		if !prefer && (result.Preferred != -1 || result.Reason == "") {
			t.Fatalf("incomplete inventory claimed unique preference: %#v", result)
		}
		if prefer && (result.Preferred < 0 || result.Targets[result.Preferred].Remote != upstream) {
			t.Fatalf("upstream lost at remote cap: %#v", result)
		}
	}
}

func TestDetectionCancellationAndFailures(t *testing.T) {
	t.Run("missing git", func(t *testing.T) {
		runner := func(context.Context, string, ...string) (string, error) {
			return "", &exec.Error{Name: "git", Err: exec.ErrNotFound}
		}
		result, err := detect(context.Background(), "/fixture", nil, runner)
		if err != nil || result.Preferred != -1 || len(result.Targets) != 0 || result.Reason == "" {
			t.Fatalf("missing Git should be normal: %#v, %v", result, err)
		}
	})
	t.Run("cancel before starting", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := detect(ctx, "/fixture", nil, func(context.Context, string, ...string) (string, error) {
			t.Fatal("cancelled detector launched Git")
			return "", nil
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("lost cancellation: %v", err)
		}
	})
	t.Run("caller timeout", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		result, err := detect(ctx, "/fixture", nil, func(ctx context.Context, _ string, _ ...string) (string, error) {
			<-ctx.Done()
			return "PRIVATE", ctx.Err()
		})
		if !errors.Is(err, context.DeadlineExceeded) || result.Preferred != -1 || len(result.Targets) != 0 {
			t.Fatalf("lost deadline: %#v, %v", result, err)
		}
	})
	t.Run("overall timeout", func(t *testing.T) {
		started := time.Now()
		_, err := detect(context.Background(), "/fixture", nil, func(ctx context.Context, _ string, _ ...string) (string, error) {
			deadline, _ := ctx.Deadline()
			if left := time.Until(deadline); left > detectionTimeout || left < time.Second {
				t.Fatalf("unexpected overall budget %s", left)
			}
			<-ctx.Done()
			return "", ctx.Err()
		})
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 3*time.Second {
			t.Fatalf("overall timeout not enforced: %v", err)
		}
	})
	t.Run("sanitized failure", func(t *testing.T) {
		result, err := detect(context.Background(), "/fixture", nil, func(context.Context, string, ...string) (string, error) {
			return "PRIVATE", errors.New("https://PRIVATE@unknown.example/team/repo")
		})
		if !errors.Is(err, errInspect) || strings.Contains(fmt.Sprint(result, err), "PRIVATE") {
			t.Fatalf("unsafe failure: %#v, %v", result, err)
		}
	})
}

func isolatedGit(t *testing.T) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("Git unavailable for disposable repository tests")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	return git
}

func gitFixture(t *testing.T, git, cwd string, args ...string) string {
	t.Helper()
	fixed := []string{"-c", "core.hooksPath=" + os.DevNull, "-c", "commit.gpgsign=false", "-C", cwd}
	cmd := exec.Command(git, append(fixed, args...)...)
	cmd.Env = gitEnvironment(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fixture git %v: %v (%s)", args, err, out)
	}
	return strings.TrimSuffix(string(out), "\n")
}

func newFixtureRepo(t *testing.T, git string) string {
	t.Helper()
	root := t.TempDir()
	gitFixture(t, git, root, "init", "--template=", "-b", "main")
	gitFixture(t, git, root, "config", "user.name", "Fixture")
	gitFixture(t, git, root, "config", "user.email", "fixture@example.invalid")
	gitFixture(t, git, root, "commit", "--allow-empty", "-m", "fixture")
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestDetectDisposableRepositories(t *testing.T) {
	git := isolatedGit(t)
	t.Run("non repository", func(t *testing.T) {
		result, err := Detect(context.Background(), t.TempDir(), nil)
		if err != nil || result.Root != "" || len(result.Targets) != 0 || result.Preferred != -1 || result.Reason == "" {
			t.Fatalf("non-repository should be normal: %#v, %v", result, err)
		}
	})
	t.Run("no remotes", func(t *testing.T) {
		root := newFixtureRepo(t, git)
		result, err := Detect(context.Background(), root, nil)
		if err != nil || result.Root != root || len(result.Targets) != 0 || result.Preferred != -1 || result.Reason == "" {
			t.Fatalf("no remotes should be normal: %#v, %v", result, err)
		}
	})
	t.Run("nested and worktree", func(t *testing.T) {
		root := newFixtureRepo(t, git)
		gitFixture(t, git, root, "config", "remote.origin.url", "https://github.com/team/original.git")
		gitFixture(t, git, root, "config", "remote.fork.url", "ssh://git@gitlab.com:2222/group/fork.git")
		gitFixture(t, git, root, "config", "remote.fork.fetch", "+refs/heads/*:refs/remotes/fork/*")
		gitFixture(t, git, root, "config", "branch.main.remote", "fork")
		gitFixture(t, git, root, "config", "branch.main.merge", "refs/heads/main")
		nested := filepath.Join(root, "nested", "deep")
		if err := os.MkdirAll(nested, 0700); err != nil {
			t.Fatal(err)
		}
		before := gitFixture(t, git, root, "status", "--porcelain=v1")
		result, err := Detect(context.Background(), nested, nil)
		if err != nil || result.Root != root || result.Preferred < 0 || result.Targets[result.Preferred].Remote != "fork" {
			t.Fatalf("nested checkout/upstream: %#v, %v", result, err)
		}
		if after := gitFixture(t, git, root, "status", "--porcelain=v1"); after != before {
			t.Fatal("detector changed disposable repository status")
		}
		worktree := filepath.Join(t.TempDir(), "linked")
		gitFixture(t, git, root, "worktree", "add", "-b", "linked", worktree, "HEAD")
		worktree, err = filepath.EvalSymlinks(worktree)
		if err != nil {
			t.Fatal(err)
		}
		nested = filepath.Join(worktree, "nested")
		if err := os.Mkdir(nested, 0700); err != nil {
			t.Fatal(err)
		}
		result, err = Detect(context.Background(), nested, nil)
		if err != nil || result.Root != worktree || result.Preferred < 0 || result.Targets[result.Preferred].Remote != "origin" {
			t.Fatalf("linked worktree root/origin: %#v, %v", result, err)
		}
		gitFixture(t, git, worktree, "checkout", "--detach")
		result, err = Detect(context.Background(), nested, nil)
		if err != nil || result.Preferred < 0 || result.Targets[result.Preferred].Remote != "origin" {
			t.Fatalf("detached head: %#v, %v", result, err)
		}
	})
	t.Run("insteadOf and fetch only", func(t *testing.T) {
		root := newFixtureRepo(t, git)
		gitFixture(t, git, root, "config", "url.https://github.com/team/.insteadOf", "fixture:")
		gitFixture(t, git, root, "config", "remote.origin.url", "fixture:repo.git")
		gitFixture(t, git, root, "config", "--add", "remote.origin.url", "https://gitlab.com/group/not-fetched")
		gitFixture(t, git, root, "config", "remote.origin.pushurl", "ext::never-run-PRIVATE")
		gitFixture(t, git, root, "config", "url.https://gitlab.com/group/.pushInsteadOf", "fixture:")
		result, err := Detect(context.Background(), root, nil)
		want := Target{Kind: "github", Host: "github.com", Scope: "team/repo", Remote: "origin"}
		if err != nil || len(result.Targets) != 1 || result.Targets[0] != want {
			t.Fatalf("fetch URL expansion: %#v, %v", result, err)
		}
	})
	t.Run("unborn branch", func(t *testing.T) {
		root := t.TempDir()
		gitFixture(t, git, root, "init", "--template=", "-b", "main")
		gitFixture(t, git, root, "config", "remote.origin.url", "https://github.com/team/repo")
		result, err := Detect(context.Background(), root, []config.Source{})
		if err != nil || result.Preferred < 0 {
			t.Fatalf("unborn branch failed: %#v, %v", result, err)
		}
	})
}
