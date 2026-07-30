package workspace

import (
	"path/filepath"
	"strings"
)

// Label returns the checkout folder name for logs (e.g. devops__test-go-1e805d884ce4),
// without host-specific cache roots like /Users/... or /data/workspaces.
func Label(dir string) string {
	if dir == "" {
		return "."
	}
	return filepath.Base(filepath.Clean(dir))
}

// RedactPaths replaces absolute workspace paths in text with Label(dir).
func RedactPaths(dir, text string) string {
	if dir == "" || text == "" {
		return text
	}
	clean := filepath.Clean(dir)
	out := strings.ReplaceAll(text, clean, Label(clean))
	if abs, err := filepath.Abs(clean); err == nil && abs != clean {
		out = strings.ReplaceAll(out, abs, Label(clean))
	}
	return out
}
