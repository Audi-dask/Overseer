package workspace

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Prepare materializes a shallow checkout of commitSHA so OCR agent tools
// (file_read / code_search / git diff) can run against a real git tree.
// All paths are resolved to absolute form because git commands run with
// cmd.Dir set, which would otherwise re-anchor relative targets.
func Prepare(ctx context.Context, baseURL, fullName, token, commitSHA, cacheRoot string) (dir string, cleanup func(), err error) {
	commitSHA = strings.TrimSpace(commitSHA)
	if commitSHA == "" {
		return "", nil, fmt.Errorf("commit sha required for workspace")
	}
	if cacheRoot == "" {
		cacheRoot = filepath.Join(os.TempDir(), "overseer-workspaces")
	}
	cacheRoot, err = filepath.Abs(cacheRoot)
	if err != nil {
		return "", nil, err
	}
	if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
		return "", nil, err
	}

	safe := strings.ReplaceAll(strings.Trim(fullName, "/"), "/", "__")
	dir = filepath.Join(cacheRoot, fmt.Sprintf("%s-%s", safe, short(commitSHA)))
	cleanup = func() { _ = os.RemoveAll(dir) }

	// Reuse an existing checkout when it already holds the commit.
	if fi, statErr := os.Stat(filepath.Join(dir, ".git")); statErr == nil && fi.IsDir() {
		if git(ctx, dir, "cat-file", "-e", commitSHA+"^{commit}") == nil {
			if git(ctx, dir, "checkout", "--force", commitSHA) == nil {
				return dir, cleanup, nil
			}
		}
		_ = os.RemoveAll(dir)
	}

	cloneURL, err := httpsCloneURL(baseURL, fullName, token)
	if err != nil {
		return "", nil, err
	}

	tmp := filepath.Join(cacheRoot, fmt.Sprintf(".%s-%d.tmp", safe, time.Now().UnixNano()))
	defer func() { _ = os.RemoveAll(tmp) }()
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return "", nil, err
	}
	if err := git(ctx, tmp, "init", "--quiet"); err != nil {
		return "", nil, fmt.Errorf("git init: %w", err)
	}
	if err := git(ctx, tmp, "remote", "add", "origin", cloneURL); err != nil {
		return "", nil, fmt.Errorf("git remote add: %w", err)
	}

	// Fetching the SHA directly needs uploadpack.allowReachableSHA1InWant on the
	// server; fall back to branch + merge-request refs when it is disabled.
	fetchErr := git(ctx, tmp, "fetch", "--depth", "50", "origin", commitSHA)
	if fetchErr != nil {
		alt := git(ctx, tmp, "fetch", "--depth", "50", "origin",
			"+refs/heads/*:refs/remotes/origin/*",
			"+refs/merge-requests/*/head:refs/remotes/origin/mr/*")
		if alt != nil {
			return "", nil, fmt.Errorf("git fetch %s: %v (refs fallback: %v)", short(commitSHA), fetchErr, alt)
		}
	}
	if err := git(ctx, tmp, "-c", "advice.detachedHead=false", "checkout", "--force", commitSHA); err != nil {
		return "", nil, fmt.Errorf("git checkout %s: %w", short(commitSHA), err)
	}

	_ = os.RemoveAll(dir)
	if err := os.Rename(tmp, dir); err != nil {
		return "", nil, err
	}
	return dir, cleanup, nil
}

func httpsCloneURL(baseURL, fullName, token string) (string, error) {
	u, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil {
		return "", err
	}
	if u.Scheme == "" {
		u.Scheme = "https"
	}
	u.User = url.UserPassword("oauth2", token)
	u.Path = "/" + strings.Trim(fullName, "/") + ".git"
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func git(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=echo",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := redact(strings.TrimSpace(string(out)))
		if dir != "" {
			msg = RedactPaths(dir, msg)
		}
		return fmt.Errorf("%w: %s", err, msg)
	}
	return nil
}

// redact strips basic-auth credentials that git echoes back in error output.
func redact(s string) string {
	if i := strings.Index(s, "oauth2:"); i >= 0 {
		if j := strings.Index(s[i:], "@"); j > 0 {
			return s[:i] + "oauth2:***" + s[i+j:]
		}
	}
	return s
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
