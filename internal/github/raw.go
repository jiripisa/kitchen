package github

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// FetchRawFile pulls the raw contents of a file from a GitHub repository at
// a specific ref. Uses `gh api` so authentication piggybacks on the user's
// existing `gh auth` session — no token plumbing on our side.
//
// Returns ErrGHUnavailable when the `gh` CLI is not installed or not
// authenticated; callers can decide whether to surface a friendly error.
func FetchRawFile(ctx context.Context, owner, repo, ref, path string) ([]byte, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return nil, ErrGHUnavailable
	}
	endpoint := fmt.Sprintf("repos/%s/%s/contents/%s", owner, repo, path)
	if ref != "" {
		endpoint += "?ref=" + ref
	}
	// The `application/vnd.github.raw` accept header tells GitHub to return
	// the file body directly instead of the JSON envelope with a base64
	// `content` field.
	cmd := exec.CommandContext(ctx, "gh", "api",
		"-H", "Accept: application/vnd.github.raw",
		endpoint,
	)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("gh api %s/%s@%s/%s: %s", owner, repo, ref, path, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("gh api %s/%s@%s/%s: %w", owner, repo, ref, path, err)
	}
	return out, nil
}

// CurrentUser returns the GitHub login of the currently-authenticated `gh`
// user. Empty string + nil error when `gh` is unavailable so callers can
// degrade gracefully without checking for a specific error.
func CurrentUser(ctx context.Context) (string, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return "", nil
	}
	cmd := exec.CommandContext(ctx, "gh", "api", "user", "--jq", ".login")
	out, err := cmd.Output()
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(string(out)), nil
}

// ErrGHUnavailable is returned when callers strictly need `gh` but it isn't
// installed or authenticated. Wrap with %w so callers can errors.Is it.
var ErrGHUnavailable = errors.New("gh CLI is not available; install it and run `gh auth login`")
