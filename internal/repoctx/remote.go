package repoctx

import (
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"lazytodo/internal/config"
)

func parseRemote(raw string, sources []config.Source) (Target, bool) {
	if len(raw) > 8192 || !safeText(raw) || strings.ContainsAny(raw, " \\") {
		return Target{}, false
	}
	var authority, path string
	ssh := false
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil || u.Opaque != "" || u.Host == "" {
			return Target{}, false
		}
		switch strings.ToLower(u.Scheme) {
		case "https", "http":
		case "ssh":
			ssh = true
		default:
			return Target{}, false
		}
		// Escaped separators can change the repository path's meaning.
		escaped := strings.ToLower(u.EscapedPath())
		if strings.Contains(escaped, "%2f") || strings.Contains(escaped, "%5c") {
			return Target{}, false
		}
		authority, path = u.Host, strings.TrimPrefix(u.Path, "/")
	} else {
		// scp-like syntax is data only: never consult ssh -G or resolve aliases.
		var ok bool
		authority, path, ok = strings.Cut(raw, ":")
		if !ok || strings.ContainsAny(authority, "/?#") {
			return Target{}, false
		}
		if user, host, found := strings.Cut(authority, "@"); found {
			if user == "" || strings.ContainsAny(host, "@") {
				return Target{}, false
			}
			authority = host
		}
		path, _, _ = strings.Cut(path, "?")
		path, _, _ = strings.Cut(path, "#")
		ssh = true
	}
	host, hostname, ok := canonicalHost(authority)
	if !ok {
		return Target{}, false
	}
	if ssh {
		host = hostname // An SSH transport port is never an API authority port.
	}
	kind, host, ok := forgeHost(host, hostname, ssh, sources)
	if !ok {
		return Target{}, false
	}
	scope := strings.TrimSuffix(strings.TrimRight(path, "/"), ".git")
	parts := strings.Split(scope, "/")
	if len(parts) < 2 || kind == "github" && len(parts) != 2 {
		return Target{}, false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return Target{}, false
		}
		for _, c := range part {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("._-", c)) {
				return Target{}, false
			}
		}
	}
	return Target{Kind: kind, Host: host, Scope: scope}, true
}

// canonicalHost intentionally accepts only hostname authorities supported by
// config.Source; malformed ports, IP literals in brackets, and URLs are rejected.
func canonicalHost(authority string) (host, hostname string, ok bool) {
	hostname, port, hasPort := strings.Cut(strings.ToLower(authority), ":")
	hostname = strings.TrimSuffix(hostname, ".")
	if hostname == "" || len(hostname) > 253 {
		return "", "", false
	}
	for _, label := range strings.Split(hostname, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", "", false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return "", "", false
			}
		}
	}
	host = hostname
	if hasPort {
		for _, c := range port {
			if c < '0' || c > '9' {
				return "", "", false
			}
		}
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", "", false
		}
		host += ":" + port
	}
	return host, hostname, true
}

func forgeHost(host, hostname string, ssh bool, sources []config.Source) (string, string, bool) {
	switch hostname {
	case "github.com":
		return "github", host, true
	case "gitlab.com":
		return "gitlab", host, true
	}
	// Exact authority wins. SSH alone may use a unique configured API authority
	// with the same hostname (e.g. forge.example:8443); HTTP cannot guess a port.
	for _, exact := range []bool{true, false} {
		if !exact && !ssh {
			break
		}
		var kind, matched string
		for _, source := range sources {
			if source.Kind != "github" && source.Kind != "gitlab" {
				continue
			}
			candidate, name, ok := canonicalHost(source.Host)
			if !ok || exact && candidate != host || !exact && name != hostname {
				continue
			}
			if kind != "" && (kind != source.Kind || matched != candidate) {
				return "", "", false
			}
			kind, matched = source.Kind, candidate
		}
		if kind != "" {
			return kind, matched, true
		}
	}
	return "", "", false
}

func safeText(value string) bool {
	if !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return false
		}
	}
	return true
}
