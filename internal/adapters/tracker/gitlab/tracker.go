// Package gitlab implements ports.VersionTracker for GitLab repositories.
// It uses the GitLab REST API v4. A token is optional for public repos.
package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/bortizllamas/updatemonitor/internal/domain"
)

// Tracker fetches version information from GitLab.
type Tracker struct {
	baseURL string
	token   string
	http    *http.Client
}

// New creates a GitLab tracker. baseURL defaults to https://gitlab.com.
func New(baseURL, token string) *Tracker {
	if baseURL == "" {
		baseURL = "https://gitlab.com"
	}
	return &Tracker{
		baseURL: baseURL,
		token:   token,
		http:    &http.Client{Timeout: 15 * time.Second},
	}
}

// Platform satisfies ports.VersionTracker.
func (t *Tracker) Platform() domain.Platform {
	return domain.PlatformGitLab
}

// LatestVersion returns the most recent release or tag.
func (t *Tracker) LatestVersion(ctx context.Context, owner, repo string) (*domain.VersionInfo, error) {
	projectID := url.PathEscape(owner + "/" + repo)

	// 1. Try releases endpoint.
	var releases []struct {
		TagName     string    `json:"tag_name"`
		ReleasedAt  time.Time `json:"released_at"`
		Description string    `json:"description"`
		Links       struct {
			Self string `json:"self"`
		} `json:"_links"`
	}
	if err := t.get(ctx, fmt.Sprintf("/api/v4/projects/%s/releases?per_page=1", projectID), &releases); err == nil && len(releases) > 0 {
		r := releases[0]
		return &domain.VersionInfo{
			Tag:        r.TagName,
			PublishedAt: r.ReleasedAt,
			ReleaseURL: r.Links.Self,
			Body:       r.Description,
		}, nil
	}

	// 2. Fall back to repository tags.
	var tags []struct {
		Name   string `json:"name"`
		Commit struct {
			CreatedAt time.Time `json:"created_at"`
		} `json:"commit"`
	}
	if err := t.get(ctx, fmt.Sprintf("/api/v4/projects/%s/repository/tags?per_page=1&order_by=version", projectID), &tags); err != nil {
		return nil, fmt.Errorf("gitlab tags API: %w", err)
	}
	if len(tags) == 0 {
		return nil, fmt.Errorf("no tags or releases found for %s/%s", owner, repo)
	}

	tag := tags[0]
	return &domain.VersionInfo{
		Tag:        tag.Name,
		PublishedAt: tag.Commit.CreatedAt,
	}, nil
}

// Changelog returns the release notes for toTag or a list of commits between tags.
func (t *Tracker) Changelog(ctx context.Context, owner, repo, fromTag, toTag string) (string, error) {
	projectID := url.PathEscape(owner + "/" + repo)

	// Try the release description first.
	var release struct {
		Description string `json:"description"`
	}
	path := fmt.Sprintf("/api/v4/projects/%s/releases/%s", projectID, url.PathEscape(toTag))
	if err := t.get(ctx, path, &release); err == nil && release.Description != "" {
		return release.Description, nil
	}

	// Fall back: compare commits.
	var commits []struct {
		ID      string `json:"id"`
		Title   string `json:"title"`
	}
	comparePath := fmt.Sprintf("/api/v4/projects/%s/repository/compare?from=%s&to=%s",
		projectID, url.QueryEscape(fromTag), url.QueryEscape(toTag))
	var cmp struct {
		Commits []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"commits"`
	}
	if err := t.get(ctx, comparePath, &cmp); err != nil {
		return "", fmt.Errorf("gitlab compare: %w", err)
	}
	commits = cmp.Commits

	var out string
	for _, c := range commits {
		sha := c.ID
		if len(sha) > 7 {
			sha = sha[:7]
		}
		out += fmt.Sprintf("- %s %s\n", sha, c.Title)
	}
	return out, nil
}

// get executes a GET request against the GitLab API and JSON-decodes the body.
func (t *Tracker) get(ctx context.Context, path string, dest any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.baseURL+path, nil)
	if err != nil {
		return err
	}
	if t.token != "" {
		req.Header.Set("PRIVATE-TOKEN", t.token)
	}

	resp, err := t.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("gitlab API %s: %d %s", path, resp.StatusCode, string(body))
	}

	return json.NewDecoder(resp.Body).Decode(dest)
}
