package firewall

import (
	"path/filepath"
	"strings"
)

// FilterDiff drops file hunks whose path matches gitignore-like rules.
func FilterDiff(diff, rules string) string {
	patterns := parseRules(rules)
	if len(patterns) == 0 {
		return diff
	}
	parts := splitDiffFiles(diff)
	var keep []string
	for _, p := range parts {
		if matched(p.path, patterns) {
			continue
		}
		keep = append(keep, p.body)
	}
	return strings.Join(keep, "")
}

// IsExcluded reports whether a repository-relative path is blocked by the
// configured gitignore-like firewall rules. Later negation rules can re-include
// a path, matching FilterDiff's behavior.
func IsExcluded(path, rules string) bool {
	return matched(path, parseRules(rules))
}

type rule struct {
	negate  bool
	pattern string
}

func parseRules(raw string) []rule {
	var out []rule
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		neg := false
		if strings.HasPrefix(line, "!") {
			neg = true
			line = line[1:]
		}
		out = append(out, rule{negate: neg, pattern: line})
	}
	return out
}

type filePart struct {
	path string
	body string
}

func splitDiffFiles(diff string) []filePart {
	if strings.TrimSpace(diff) == "" {
		return nil
	}
	// Normalize and split on unified-diff file headers.
	chunks := strings.Split(diff, "\n--- a/")
	var parts []filePart
	for i, chunk := range chunks {
		if strings.TrimSpace(chunk) == "" {
			continue
		}
		body := chunk
		if i > 0 {
			body = "--- a/" + chunk
		} else if !strings.HasPrefix(body, "--- a/") {
			// first chunk may already include header
		}
		path := ""
		for _, line := range strings.Split(body, "\n") {
			if strings.HasPrefix(line, "+++ b/") {
				path = strings.TrimPrefix(line, "+++ b/")
				break
			}
			if strings.HasPrefix(line, "--- a/") && path == "" {
				path = strings.TrimPrefix(line, "--- a/")
			}
		}
		if !strings.HasSuffix(body, "\n") && i < len(chunks)-1 {
			body += "\n"
		}
		parts = append(parts, filePart{path: path, body: body})
	}
	return parts
}

func matched(path string, rules []rule) bool {
	path = strings.TrimPrefix(path, "/")
	ignored := false
	for _, r := range rules {
		ok := matchPattern(r.pattern, path)
		if !ok {
			continue
		}
		if r.negate {
			ignored = false
		} else {
			ignored = true
		}
	}
	return ignored
}

func matchPattern(pattern, path string) bool {
	pattern = strings.TrimPrefix(pattern, "/")
	path = strings.TrimPrefix(path, "/")
	if strings.HasSuffix(pattern, "/") {
		prefix := strings.TrimSuffix(pattern, "/")
		return path == prefix || strings.HasPrefix(path, prefix+"/")
	}
	if strings.HasPrefix(pattern, "**/") {
		suf := strings.TrimPrefix(pattern, "**/")
		return path == suf || strings.HasSuffix(path, "/"+suf) || filepath.Base(path) == suf
	}
	if strings.Contains(pattern, "**") {
		p := strings.ReplaceAll(pattern, "**", "*")
		ok, _ := filepath.Match(p, path)
		if ok {
			return true
		}
		ok, _ = filepath.Match(p, filepath.Base(path))
		return ok
	}
	ok, _ := filepath.Match(pattern, path)
	if ok {
		return true
	}
	ok, _ = filepath.Match(pattern, filepath.Base(path))
	if ok {
		return true
	}
	if !strings.ContainsAny(pattern, "*?[") {
		return path == pattern || strings.HasPrefix(path, pattern+"/") || strings.Contains(path, "/"+pattern+"/") || strings.HasSuffix(path, "/"+pattern)
	}
	return false
}
