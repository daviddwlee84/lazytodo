package model

import "strings"

// PriorityLabel is a presentation hint, never a cross-source rank or native
// priority field. A non-nil per-source list overrides conventions; [] disables.
func PriorityLabel(name string, configured *[]string) bool {
	if configured != nil {
		for _, value := range *configured {
			if name == value {
				return true
			}
		}
		return false
	}
	lower := strings.ToLower(name)
	if len(lower) == 2 && lower[0] == 'p' && lower[1] >= '0' && lower[1] <= '4' {
		return true
	}
	for _, prefix := range []string{"priority::", "priority:", "priority/", "priority-"} {
		if strings.HasPrefix(lower, prefix) {
			return strings.TrimSpace(name[len(prefix):]) != ""
		}
	}
	return false
}

// LabelQuery preserves the exact label; the query parser only unescapes quotes
// and backslashes, not JSON escape sequences. Control text is not put in input.
func LabelQuery(name string) (string, bool) {
	if name == "" || Plain(name) != name || strings.ContainsAny(name, "\n\r\t") {
		return "", false
	}
	escaped := strings.ReplaceAll(strings.ReplaceAll(name, `\`, `\\`), `"`, `\"`)
	return `label:"` + escaped + `"`, true
}
