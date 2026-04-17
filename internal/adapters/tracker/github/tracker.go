// Package github implements ports.VersionTracker for GitHub repositories.
package github

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	gogithub "github.com/google/go-github/v63/github"
	"golang.org/x/oauth2"

	"github.com/bortizllamas/updatemonitor/internal/domain"
)

// Tracker fetches version information from GitHub.
type Tracker struct {
	client *gogithub.Client
	log    *slog.Logger
}

// New creates a GitHub tracker. token may be empty for public repos.
func New(token string, log *slog.Logger) *Tracker {
	var httpClient *http.Client
	if token != "" {
		log.Debug("github: using authenticated client (token set)")
		ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
		httpClient = oauth2.NewClient(context.Background(), ts)
	} else {
		log.Warn("github: no token set — rate limit is 60 req/hr (unauthenticated)")
	}
	return &Tracker{client: gogithub.NewClient(httpClient), log: log}
}

// Platform satisfies ports.VersionTracker.
func (t *Tracker) Platform() domain.Platform { return domain.PlatformGitHub }

// LatestVersion returns the most recent release or tag for the repository.
func (t *Tracker) LatestVersion(ctx context.Context, owner, repo string) (*domain.VersionInfo, error) {
	t.log.Debug("github: LatestVersion called", "owner", owner, "repo", repo)

	release, resp, err := t.client.Repositories.GetLatestRelease(ctx, owner, repo)
	if err == nil {
		info := &domain.VersionInfo{
			Tag:         release.GetTagName(),
			PublishedAt: release.GetPublishedAt().Time,
			ReleaseURL:  release.GetHTMLURL(),
			Body:        release.GetBody(),
		}
		t.log.Debug("github: latest release found", "owner", owner, "repo", repo, "tag", info.Tag)
		return info, nil
	}
	if resp != nil && resp.StatusCode != http.StatusNotFound {
		t.log.Error("github: GetLatestRelease API error",
			"owner", owner, "repo", repo, "status", resp.StatusCode, "err", err)
		return nil, fmt.Errorf("github releases API: %w", err)
	}
	t.log.Debug("github: no release found, falling back to tags", "owner", owner, "repo", repo)

	tags, _, err := t.client.Repositories.ListTags(ctx, owner, repo,
		&gogithub.ListOptions{PerPage: 1})
	if err != nil {
		t.log.Error("github: ListTags API error", "owner", owner, "repo", repo, "err", err)
		return nil, fmt.Errorf("github tags API: %w", err)
	}
	if len(tags) == 0 {
		t.log.Warn("github: no tags or releases found", "owner", owner, "repo", repo)
		return nil, fmt.Errorf("no tags or releases found for %s/%s", owner, repo)
	}

	tag := tags[0]
	t.log.Debug("github: latest tag found", "owner", owner, "repo", repo, "tag", tag.GetName())
	return &domain.VersionInfo{Tag: tag.GetName()}, nil
}

// Changelog returns the body of the GitHub Release between two tags.
func (t *Tracker) Changelog(ctx context.Context, owner, repo, fromTag, toTag string) (string, error) {
	t.log.Debug("github: Changelog called", "owner", owner, "repo", repo, "from", fromTag, "to", toTag)

	release, resp, err := t.client.Repositories.GetReleaseByTag(ctx, owner, repo, toTag)
	if err == nil && release.GetBody() != "" {
		t.log.Debug("github: changelog from release body", "tag", toTag, "bytes", len(release.GetBody()))
		return release.GetBody(), nil
	}
	if resp != nil && resp.StatusCode != http.StatusNotFound && err != nil {
		t.log.Error("github: GetReleaseByTag error", "tag", toTag, "err", err)
		return "", fmt.Errorf("github release by tag: %w", err)
	}
	t.log.Debug("github: no release body, comparing commits", "from", fromTag, "to", toTag)

	cmp, _, err := t.client.Repositories.CompareCommits(ctx, owner, repo,
		fromTag, toTag, &gogithub.ListOptions{PerPage: 30})
	if err != nil {
		t.log.Error("github: CompareCommits error", "from", fromTag, "to", toTag, "err", err)
		return "", fmt.Errorf("github compare: %w", err)
	}

	t.log.Debug("github: commits in range", "from", fromTag, "to", toTag, "count", len(cmp.Commits))
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

// AllChanges returns every release body and commit message between fromTag
// (exclusive) and toTag (inclusive).
func (t *Tracker) AllChanges(ctx context.Context, owner, repo, fromTag, toTag string) ([]domain.ReleaseInfo, error) {
	t.log.Debug("github: AllChanges called", "owner", owner, "repo", repo, "from", fromTag, "to", toTag)

	releases, _, err := t.client.Repositories.ListReleases(ctx, owner, repo,
		&gogithub.ListOptions{PerPage: 100})
	if err != nil {
		t.log.Error("github: ListReleases error", "owner", owner, "repo", repo, "err", err)
		return nil, fmt.Errorf("github list releases: %w", err)
	}
	t.log.Debug("github: releases fetched", "total", len(releases))

	// Only scan releases from the same major version family as the tracked range.
	// This prevents pulling in CVEs from unrelated branches (e.g. v2.x when tracking v3.x).
	majorPrefix := majorVersionPrefix(fromTag)
	t.log.Debug("github: AllChanges version filter", "major_prefix", majorPrefix)

	var infos []domain.ReleaseInfo
	collecting := false
	for _, r := range releases {
		tag := r.GetTagName()
		if tag == toTag {
			collecting = true
		}
		if !collecting {
			continue
		}
		if tag == fromTag {
			t.log.Debug("github: AllChanges reached fromTag, stopping", "tag", tag)
			break
		}
		if majorPrefix != "" && !strings.HasPrefix(tag, majorPrefix) {
			t.log.Debug("github: AllChanges skipping release — different version family",
				"tag", tag, "expected_prefix", majorPrefix)
			continue
		}
		t.log.Debug("github: including release in CVE scan", "tag", tag, "body_bytes", len(r.GetBody()))
		infos = append(infos, domain.ReleaseInfo{
			Tag:  tag,
			Body: r.GetBody(),
		})
	}
	t.log.Debug("github: releases collected for CVE scan", "count", len(infos))

	cmp, _, err := t.client.Repositories.CompareCommits(ctx, owner, repo, fromTag, toTag,
		&gogithub.ListOptions{PerPage: 100})
	if err != nil {
		t.log.Warn("github: CompareCommits failed for AllChanges — skipping commits",
			"from", fromTag, "to", toTag, "err", err)
	} else {
		msgs := make([]string, 0, len(cmp.Commits))
		for _, c := range cmp.Commits {
			msgs = append(msgs, c.GetCommit().GetMessage())
		}
		t.log.Debug("github: commits collected for CVE scan", "count", len(msgs))
		infos = append(infos, domain.ReleaseInfo{
			Tag:     toTag,
			Commits: msgs,
		})
	}

	return infos, nil
}

// majorVersionPrefix extracts the major version prefix from a semver tag.
// e.g. "v3.6.0" → "v3.", "v2.11.32" → "v2.", "1.0.0" → "1."
// Returns "" if the tag doesn't look like semver.
func majorVersionPrefix(tag string) string {
	s := strings.TrimPrefix(tag, "v")
	dot := strings.IndexByte(s, '.')
	if dot == -1 {
		return ""
	}
	if strings.HasPrefix(tag, "v") {
		return "v" + s[:dot] + "."
	}
	return s[:dot] + "."
}

func firstLine(s string) string {
	for i, c := range s {
		if c == '\n' {
			return s[:i]
		}
	}
	return s
}
