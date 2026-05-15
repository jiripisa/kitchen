// Package github fetches metadata about pull requests from GitHub via the
// gh CLI. We shell out to `gh` rather than calling the REST API directly so
// authentication piggybacks on the user's existing `gh auth login` — no
// token plumbing on our side.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// PR is the subset of PR metadata kitchen surfaces in the UI.
type PR struct {
	Number int
	URL    string
}

// Index maps "effective slug" (the canonical slug produced by
// finforce/actions-base@main from a git ref) to PR metadata.
type Index map[string]PR

// FetchIndex shells out to `gh pr list` for one repo and returns an index
// keyed by the slug derived from each PR's head ref. Returns an empty index
// (not an error) when gh is unavailable or unauthenticated, so callers can
// degrade gracefully.
func FetchIndex(ctx context.Context, owner, repo string) (Index, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return Index{}, nil
	}
	cmd := exec.CommandContext(ctx, "gh", "pr", "list",
		"--repo", owner+"/"+repo,
		"--state", "open",
		"--limit", "200",
		"--json", "number,headRefName,url",
	)
	out, err := cmd.Output()
	if err != nil {
		// Auth failure, network error, etc. — soft-fail.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return Index{}, fmt.Errorf("gh pr list %s/%s: %s", owner, repo, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return Index{}, fmt.Errorf("gh pr list %s/%s: %w", owner, repo, err)
	}

	var rows []struct {
		Number      int    `json:"number"`
		HeadRefName string `json:"headRefName"`
		URL         string `json:"url"`
	}
	if err := json.Unmarshal(out, &rows); err != nil {
		return Index{}, fmt.Errorf("parse gh output for %s/%s: %w", owner, repo, err)
	}

	idx := make(Index, len(rows))
	for _, r := range rows {
		slug := EffectiveSlug(r.HeadRefName)
		if slug == "" {
			continue
		}
		idx[slug] = PR{Number: r.Number, URL: r.URL}
	}
	return idx, nil
}

// EffectiveSlug reproduces the slug algorithm used by finforce/actions-base@main
// to derive a deployment SUFFIX from a git ref:
//
//  1. lowercase
//  2. replace any character outside [a-z0-9-] with '-'
//  3. truncate to 45 characters
//  4. strip trailing dashes
//
// Keeping it in lockstep with upstream is critical — that's how we link a
// deployment back to the PR that spawned it.
func EffectiveSlug(ref string) string {
	ref = strings.TrimPrefix(ref, "refs/heads/")
	ref = strings.TrimPrefix(ref, "refs/tags/")
	ref = strings.ToLower(ref)

	var b strings.Builder
	b.Grow(len(ref))
	for _, r := range ref {
		switch {
		case r >= 'a' && r <= 'z',
			r >= '0' && r <= '9',
			r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	s := b.String()
	if len(s) > 45 {
		s = s[:45]
	}
	return strings.TrimRight(s, "-")
}
