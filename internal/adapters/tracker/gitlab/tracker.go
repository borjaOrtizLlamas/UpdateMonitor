// Package gitlab implements ports.VersionTracker for GitLab repositories.
package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
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
	log     *slog.Logger
}

// New creates a GitLab tracker. baseURL defaults to https://gitlab.com.
func New(baseURL, token string, log *slog.Logger) *Tracker {
	if baseURL == "" {
		baseURL = "https://gitlab.com"
	}
	if token != "" {
		log.Debug("gitlab: using authenticated client (token set)", "base_url", baseURL)
	} else {
		log.Debug("gitlab: no token set — public repos only", "base_url", baseURL)
	}
	return &Tracker{
		baseURL: baseURL,
		token:   token,
		http:    &http.Client{Timeout: 15 * time.Second},
		log:     log,
	}
}

// Platform satisfies ports.VersionTracker.
func (t *Tracker) Platform() domain.Platform { return domain.PlatformGitLab }

// LatestVersion returns the most recent release or tag.
func (t *Tracker) LatestVersion(ctx context.Context, owner, repo string) (*domain.VersionInfo, error) {
	t.log.Debug("gitlab: LatestVersion called", "owner", owner, "repo", repo)
	projectID := url.PathEscape(owner + "/" + repo)

	var releases []struct {
		TagName     string    `json:"tag_name"`
		ReleasedAt  time.Time `json:"released_at"`
		Description string    `json:"description"`
		Links       struct {
			Self string `json:"self"`
		} `json:"_links"`
	}
	relPath := fmt.Sprintf("/api/v4/projects/%s/releases?per_page=1", projectID)
	if err := t.get(ctx, relPath, &releases); err == nil && len(releases) > 0 {
		r := releases[0]
		t.log.Debug("gitlab: latest release found", "owner", owner, "repo", repo, "tag", r.TagName)
		return &domain.VersionInfo{
			Tag:         r.TagName,
			PublishedAt: r.ReleasedAt,
			ReleaseURL:  r.Links.Self,
			Body:        r.Description,
		}, nil
	}
	t.log.Debug("gitlab: no releases found, falling back to tags", "owner", owner, "repo", repo)

	var tags []struct {
		Name   string `json:"name"`
		Commit struct {
			CreatedAt time.Time `json:"created_at"`
		} `json:"commit"`
	}
	tagsPath := fmt.Sprintf("/api/v4/projects/%s/repository/tags?per_page=1&order_by=version", projectID)
	if err := t.get(ctx, tagsPath, &tags); err != nil {
		t.log.Error("gitlab: tags API error", "owner", owner, "repo", repo, "err", err)
		return nil, fmt.Errorf("gitlab tags API: %w", err)
	}
	if len(tags) == 0 {
		t.log.Warn("gitlab: no tags or releases found", "owner", owner, "repo", repo)
		return nil, fmt.Errorf("no tags or releases found for %s/%s", owner, repo)
	}

	t.log.Debug("gitlab: latest tag found", "owner", owner, "repo", repo, "tag", tags[0].Name)
	return &domain.VersionInfo{
		Tag:         tags[0].Name,
		PublishedAt: tags[0].Commit.CreatedAt,
	}, nil
}

// Changelog returns the release notes for toTag or a list of commits between tags.
func (t *Tracker) Changelog(ctx context.Context, owner, repo, fromTag, toTag string) (string, error) {
	t.log.Debug("gitlab: Changelog called", "owner", owner, "repo", repo, "from", fromTag, "to", toTag)
	projectID := url.PathEscape(owner + "/" + repo)

	var release struct {
		Description string `json:"description"`
	}
	path := fmt.Sprintf("/api/v4/projects/%s/releases/%s", projectID, url.PathEscape(toTag))
	if err := t.get(ctx, path, &release); err == nil && release.Description != "" {
		t.log.Debug("gitlab: changelog from release body", "tag", toTag, "bytes", len(release.Description))
		return release.Description, nil
	}
	t.log.Debug("gitlab: no release body, comparing commits", "from", fromTag, "to", toTag)

	comparePath := fmt.Sprintf("/api/v4/projects/%s/repository/compare?from=%s&to=%s",
		projectID, url.QueryEscape(fromTag), url.QueryEscape(toTag))
	var cmp struct {
		Commits []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"commits"`
	}
	if err := t.get(ctx, comparePath, &cmp); err != nil {
		t.log.Error("gitlab: compare API error", "from", fromTag, "to", toTag, "err", err)
		return "", fmt.Errorf("gitlab compare: %w", err)
	}

	t.log.Debug("gitlab: commits in range", "from", fromTag, "to", toTag, "count", len(cmp.Commits))
	var out string
	for _, c := range cmp.Commits {
		sha := c.ID
		if len(sha) > 7 {
			sha = sha[:7]
		}
		out += fmt.Sprintf("- %s %s\n", sha, c.Title)
	}
	return out, nil
}

// AllChanges returns every release body and commit message between fromTag
// (exclusive) and toTag (inclusive).
func (t *Tracker) AllChanges(ctx context.Context, owner, repo, fromTag, toTag string) ([]domain.ReleaseInfo, error) {
	t.log.Debug("gitlab: AllChanges called", "owner", owner, "repo", repo, "from", fromTag, "to", toTag)
	projectID := url.PathEscape(owner + "/" + repo)

	var releases []struct {
		TagName     string `json:"tag_name"`
		Description string `json:"description"`
	}
	_ = t.get(ctx, fmt.Sprintf("/api/v4/projects/%s/releases?per_page=100", projectID), &releases)
	t.log.Debug("gitlab: releases fetched for CVE scan", "count", len(releases))

	majorPrefix := majorVersionPrefix(fromTag)
	t.log.Debug("gitlab: AllChanges version filter", "major_prefix", majorPrefix)

	var infos []domain.ReleaseInfo
	collecting := false
	for _, r := range releases {
		if r.TagName == toTag {
			collecting = true
		}
		if !collecting {
			continue
		}
		if r.TagName == fromTag {
			t.log.Debug("gitlab: AllChanges reached fromTag, stopping", "tag", r.TagName)
			break
		}
		if majorPrefix != "" && !strings.HasPrefix(r.TagName, majorPrefix) {
			t.log.Debug("gitlab: AllChanges skipping release — different version family",
				"tag", r.TagName, "expected_prefix", majorPrefix)
			continue
		}
		t.log.Debug("gitlab: including release in CVE scan", "tag", r.TagName, "body_bytes", len(r.Description))
		infos = append(infos, domain.ReleaseInfo{
			Tag:  r.TagName,
			Body: r.Description,
		})
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
		t.log.Warn("gitlab: compare failed for AllChanges — skipping commits",
			"from", fromTag, "to", toTag, "err", err)
	} else {
		msgs := make([]string, 0, len(cmp.Commits))
		for _, c := range cmp.Commits {
			msgs = append(msgs, c.Title)
		}
		t.log.Debug("gitlab: commits collected for CVE scan", "count", len(msgs))
		infos = append(infos, domain.ReleaseInfo{
			Tag:     toTag,
			Commits: msgs,
		})
	}

	return infos, nil
}

// get executes a GET request against the GitLab API and JSON-decodes the body.
func (t *Tracker) get(ctx context.Context, path string, dest any) error {
	fullURL := t.baseURL + path
	t.log.Debug("gitlab: GET request", "url", fullURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return err
	}
	if t.token != "" {
		req.Header.Set("PRIVATE-TOKEN", t.token)
	}

	resp, err := t.http.Do(req)
	if err != nil {
		t.log.Error("gitlab: HTTP request failed", "url", fullURL, "err", err)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		t.log.Error("gitlab: API returned error status",
			"url", fullURL, "status", resp.StatusCode, "body", string(body))
		return fmt.Errorf("gitlab API %s: %d %s", path, resp.StatusCode, string(body))
	}

	t.log.Debug("gitlab: GET success", "url", fullURL, "status", resp.StatusCode)
	return json.NewDecoder(resp.Body).Decode(dest)
}
