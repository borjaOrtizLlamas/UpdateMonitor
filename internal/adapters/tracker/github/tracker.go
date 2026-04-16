// Package github implements ports.VersionTracker for GitHub repositories.
// It uses the GitHub REST API v3. A token is optional but recommended to
// avoid hitting the 60 req/hr unauthenticated rate limit.
package github

import (
	"context"
	"fmt"
	"net/http"

	gogithub "github.com/google/go-github/v63/github"
	"golang.org/x/oauth2"

	"github.com/bortizllamas/updatemonitor/internal/domain"
)

// Tracker fetches version information from GitHub.
type Tracker struct {
	client *gogithub.Client
}

// New creates a GitHub tracker. token may be empty for public repos.
func New(token string) *Tracker {
	var httpClient *http.Client
	if token != "" {
		ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
		httpClient = oauth2.NewClient(context.Background(), ts)
	}
	return &Tracker{client: gogithub.NewClient(httpClient)}
}

// Platform satisfies ports.VersionTracker.
func (t *Tracker) Platform() domain.Platform {
	return domain.PlatformGitHub
}

// LatestVersion returns the most recent release or tag for the repository.
// It prefers Releases (which carry full metadata) over raw git tags.
func (t *Tracker) LatestVersion(ctx context.Context, owner, repo string) (*domain.VersionInfo, error) {
	// 1. Try the "latest" release endpoint first (ignores pre-releases and drafts).
	release, resp, err := t.client.Repositories.GetLatestRelease(ctx, owner, repo)
	if err == nil {
		return &domain.VersionInfo{
			Tag:        release.GetTagName(),
			PublishedAt: release.GetPublishedAt().Time,
			ReleaseURL: release.GetHTMLURL(),
			Body:       release.GetBody(),
		}, nil
	}
	if resp != nil && resp.StatusCode != http.StatusNotFound {
		return nil, fmt.Errorf("github releases API: %w", err)
	}

	// 2. Fall back to the most recent git tag.
	tags, _, err := t.client.Repositories.ListTags(ctx, owner, repo,
		&gogithub.ListOptions{PerPage: 1})
	if err != nil {
		return nil, fmt.Errorf("github tags API: %w", err)
	}
	if len(tags) == 0 {
		return nil, fmt.Errorf("no tags or releases found for %s/%s", owner, repo)
	}

	tag := tags[0]
	return &domain.VersionInfo{
		Tag: tag.GetName(),
	}, nil
}

// Changelog returns the body of the GitHub Release between two tags, or a
// comparison URL if no release body is available.
func (t *Tracker) Changelog(ctx context.Context, owner, repo, fromTag, toTag string) (string, error) {
	// Fetch the release whose tag is toTag to get its notes.
	release, resp, err := t.client.Repositories.GetReleaseByTag(ctx, owner, repo, toTag)
	if err == nil && release.GetBody() != "" {
		return release.GetBody(), nil
	}
	if resp != nil && resp.StatusCode != http.StatusNotFound && err != nil {
		return "", fmt.Errorf("github release by tag: %w", err)
	}

	// Fall back: compare the two tags and list commits.
	cmp, _, err := t.client.Repositories.CompareCommits(ctx, owner, repo,
		fromTag, toTag, &gogithub.ListOptions{PerPage: 30})
	if err != nil {
		return "", fmt.Errorf("github compare: %w", err)
	}

	var out string
	for _, c := range cmp.Commits {
		msg := c.GetCommit().GetMessage()
		sha := c.GetSHA()
		if len(sha) > 7 {
			sha = sha[:7]
		}
		out += fmt.Sprintf("- %s %s\n", sha, firstLine(msg))
	}
	return out, nil
}

func firstLine(s string) string {
	for i, c := range s {
		if c == '\n' {
			return s[:i]
		}
	}
	return s
}
