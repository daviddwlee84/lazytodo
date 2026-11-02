package repoctx

import (
	"strings"
	"testing"

	"lazytodo/internal/config"
)

func TestParseRemote(t *testing.T) {
	enterprise := []config.Source{
		{Kind: "github", Host: "code.example", Disabled: true},
		{Kind: "gitlab", Host: "forge.example:8443"},
		{Kind: "gitlab", Host: "forge.example:8443", Scope: "another/project"},
	}
	tests := []struct {
		name, raw, kind, host, scope string
	}{
		{"https credentials", "https://someone:PRIVATE@GitHub.com/Owner/Repo.git/?token=PRIVATE#PRIVATE", "github", "github.com", "Owner/Repo"},
		{"http port", "http://github.com:8080/owner/repo", "github", "github.com:8080", "owner/repo"},
		{"trailing dot", "https://GITHUB.COM./owner/repo.git", "github", "github.com", "owner/repo"},
		{"ssh port", "ssh://git:PRIVATE@github.com:2222/owner/repo.git", "github", "github.com", "owner/repo"},
		{"scp", "git@github.com:owner/repo.git", "github", "github.com", "owner/repo"},
		{"scp no user", "github.com:owner/repo", "github", "github.com", "owner/repo"},
		{"scp query", "PRIVATE@github.com:owner/repo.git/?token=PRIVATE", "github", "github.com", "owner/repo"},
		{"gitlab groups", "ssh://git@gitlab.com:2222/group/subgroup/project.git", "gitlab", "gitlab.com", "group/subgroup/project"},
		{"gitlab https", "https://gitlab.com/group/sub/project.git/", "gitlab", "gitlab.com", "group/sub/project"},
		{"disabled enterprise", "https://code.example/org/repo.git", "github", "code.example", "org/repo"},
		{"enterprise ssh", "git@code.example:org/repo", "github", "code.example", "org/repo"},
		{"enterprise API port", "https://forge.example:8443/group/sub/repo", "gitlab", "forge.example:8443", "group/sub/repo"},
		{"enterprise SSH port", "ssh://git@forge.example:2222/group/repo", "gitlab", "forge.example:8443", "group/repo"},
		{"enterprise scp port mapping", "git@forge.example:group/repo.git", "gitlab", "forge.example:8443", "group/repo"},
		{"no HTTP port guessing", "https://forge.example/group/repo", "", "", ""},
		{"unknown GitLab looking host", "https://gitlab.unknown.example/group/repo", "", "", ""},
		{"unknown GitHub looking host", "https://private.github.com/owner/repo", "", "", ""},
		{"SSH alias", "git@work-github:owner/repo.git", "", "", ""},
		{"SSH URL alias", "ssh://git@work-github/owner/repo.git", "", "", ""},
		{"local absolute", "/tmp/owner/repo.git", "", "", ""},
		{"local relative", "../owner/repo.git", "", "", ""},
		{"file URL", "file://github.com/owner/repo.git", "", "", ""},
		{"ext helper", "ext::sh -c PRIVATE", "", "", ""},
		{"remote helper", "https::github.com/owner/repo", "", "", ""},
		{"unsupported transport", "git://github.com/owner/repo", "", "", ""},
		{"missing host", "https:///owner/repo", "", "", ""},
		{"opaque URL", "https:github.com/owner/repo", "", "", ""},
		{"invalid port", "ssh://git@github.com:nope/owner/repo", "", "", ""},
		{"empty port", "https://github.com:/owner/repo", "", "", ""},
		{"large port", "https://github.com:999999/owner/repo", "", "", ""},
		{"invalid host", "https://github..com/owner/repo", "", "", ""},
		{"host confusion", "https://github.com@unknown.example/owner/repo", "", "", ""},
		{"missing scope", "https://github.com/owner", "", "", ""},
		{"nested github scope", "https://github.com/owner/sub/repo", "", "", ""},
		{"empty segment", "https://gitlab.com/group//repo", "", "", ""},
		{"leading double slash", "https://github.com//owner/repo", "", "", ""},
		{"traversal", "https://gitlab.com/group/../repo", "", "", ""},
		{"escaped traversal", "https://gitlab.com/group/%2E%2E/repo", "", "", ""},
		{"escaped separator", "https://github.com/owner%2frepo", "", "", ""},
		{"escaped question", "https://github.com/owner/repo%3fPRIVATE", "", "", ""},
		{"bad escape", "https://github.com/owner/repo%zz", "", "", ""},
		{"empty git suffix", "https://github.com/owner/.git", "", "", ""},
		{"space", "https://github.com/owner/re po", "", "", ""},
		{"escaped space", "https://github.com/owner/re%20po", "", "", ""},
		{"backslash", "https://github.com/owner/repo\\evil", "", "", ""},
		{"terminal control", "https://github.com/owner/\x1b[31mrepo", "", "", ""},
		{"encoded control", "https://github.com/owner/repo%1b", "", "", ""},
		{"newline", "https://github.com/owner/repo\n", "", "", ""},
		{"bidi control", "https://github.com/owner/repo" + string(rune(0x202e)), "", "", ""},
		{"oversized", "https://github.com/owner/" + strings.Repeat("a", 8192), "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseRemote(tt.raw, enterprise)
			want := Target{Kind: tt.kind, Host: tt.host, Scope: tt.scope}
			if ok != (tt.kind != "") || got != want {
				t.Fatalf("got %#v, %v; want %#v", got, ok, want)
			}
			if strings.Contains(got.Kind+got.Host+got.Scope+got.Remote, "PRIVATE") {
				t.Fatal("credentials leaked into identity")
			}
		})
	}
}

func TestCustomHostMapping(t *testing.T) {
	tests := []struct {
		name, raw string
		sources   []config.Source
		want      Target
	}{
		{"exact host wins", "ssh://git@forge.example:2222/team/repo", []config.Source{
			{Kind: "github", Host: "FORGE.EXAMPLE."},
			{Kind: "gitlab", Host: "forge.example:8443"},
		}, Target{Kind: "github", Host: "forge.example", Scope: "team/repo"}},
		{"ambiguous kinds", "git@forge.example:team/repo", []config.Source{
			{Kind: "github", Host: "forge.example"},
			{Kind: "gitlab", Host: "forge.example"},
		}, Target{}},
		{"ambiguous API ports", "ssh://git@forge.example:8443/team/repo", []config.Source{
			{Kind: "gitlab", Host: "forge.example:8443"},
			{Kind: "gitlab", Host: "forge.example:9443"},
		}, Target{}},
		{"HTTP exact API port", "https://forge.example:8443/team/repo", []config.Source{
			{Kind: "gitlab", Host: "forge.example:8443"},
			{Kind: "github", Host: "forge.example:9443"},
		}, Target{Kind: "gitlab", Host: "forge.example:8443", Scope: "team/repo"}},
		{"unknown kinds ignored", "git@forge.example:team/repo", []config.Source{
			{Kind: "snapshot", Host: "forge.example"},
		}, Target{}},
		{"invalid configured authority ignored", "git@forge.example:team/repo", []config.Source{
			{Kind: "gitlab", Host: "https://forge.example"},
		}, Target{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseRemote(tt.raw, tt.sources)
			if got != tt.want || ok != (tt.want.Kind != "") {
				t.Fatalf("got %#v, %v; want %#v", got, ok, tt.want)
			}
		})
	}
}
