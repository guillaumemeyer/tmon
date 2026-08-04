package proc

import "strings"

// CWDShort returns the last two path components of cwd ("/a/b/c/d" -> "c/d"),
// matching the display format of the bash plugin.
func CWDShort(cwd string) string {
	trimmed := strings.Trim(cwd, "/")
	if trimmed == "" {
		return "/"
	}
	parts := strings.Split(trimmed, "/")
	if len(parts) > 2 {
		parts = parts[len(parts)-2:]
	}
	return strings.Join(parts, "/")
}
