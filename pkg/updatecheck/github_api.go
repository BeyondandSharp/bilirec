package updatecheck

import (
	"net/url"
	"os"
	"strings"
)

// Default update-check target: our own fork instead of the upstream repo
// (docs/CONFIG-V2.md §8).
const (
	DefaultOwner      = "BeyondandSharp"
	DefaultRepository = "bilirec"
	// DefaultRepo is the default "owner/repository" pair.
	DefaultRepo = DefaultOwner + "/" + DefaultRepository
	// DefaultReleaseURL is the human-facing release link in /version and startup banners.
	DefaultReleaseURL = "https://github.com/BeyondandSharp/bilirec/releases/latest"

	defaultGitHubAPIBase = "https://api.github.com/"
)

// Environment keys managed by the config center (docs/CONFIG-V2.md §8).
// The GitHub API override keeps GITHUB_API_URL working for backward
// compatibility; UPDATE_CHECK_API_URL, when set, takes precedence.
const (
	EnvCheckUpdate           = "CHECK_UPDATE"
	EnvUpdateCheckRepo       = "UPDATE_CHECK_REPO"
	EnvUpdateCheckReleaseURL = "UPDATE_CHECK_RELEASE_URL"
	EnvUpdateCheckAPIURL     = "UPDATE_CHECK_API_URL"
	EnvGitHubAPIURL          = "GITHUB_API_URL"
)

// NormalizeGitHubAPIBaseURL trims the configured GitHub API base URL and
// ensures a trailing slash. It does not rewrite proxy URL shape (prefix vs
// host mirror); use the full go-github BaseURL your provider documents.
// Empty input means official API.
func NormalizeGitHubAPIBaseURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if !strings.HasSuffix(s, "/") {
		s += "/"
	}
	return s
}

// githubAPIBaseURL returns the go-github BaseURL override, or "" for the
// official GitHub API. UPDATE_CHECK_API_URL is the preferred alias;
// GITHUB_API_URL stays supported for compatibility.
func githubAPIBaseURL() string {
	raw := strings.TrimSpace(os.Getenv(EnvUpdateCheckAPIURL))
	if raw == "" {
		raw = os.Getenv(EnvGitHubAPIURL)
	}
	normalized := NormalizeGitHubAPIBaseURL(raw)
	if normalized == "" || strings.EqualFold(normalized, defaultGitHubAPIBase) {
		return ""
	}
	return normalized
}

// resolveRepo returns the owner/repository to query for release tags. It
// honors UPDATE_CHECK_REPO ("owner/repo" or a github.com repository URL) and
// keeps the default when the value is unset. Malformed values warn and use the
// default so a bad setting never breaks /version.
func resolveRepo() (owner string, repository string) {
	raw := strings.TrimSpace(os.Getenv(EnvUpdateCheckRepo))
	if raw == "" {
		return DefaultOwner, DefaultRepository
	}
	if owner, repository, ok := parseRepo(raw); ok {
		return owner, repository
	}
	log.Warnf("忽略无效的 %s：%q（应为 owner/repo）", EnvUpdateCheckRepo, raw)
	return DefaultOwner, DefaultRepository
}

// parseRepo parses "owner/repo" or "https://github.com/owner/repo" forms.
func parseRepo(raw string) (owner string, repository string, ok bool) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimSuffix(raw, "/")
	raw = strings.TrimSuffix(raw, ".git")

	var parts []string
	if strings.Contains(raw, "://") {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Host == "" || !strings.EqualFold(parsed.Hostname(), "github.com") {
			return "", "", false
		}
		parts = strings.Split(strings.Trim(parsed.Path, "/"), "/")
	} else {
		parts = strings.Split(raw, "/")
	}

	if len(parts) != 2 {
		return "", "", false
	}
	owner = strings.TrimSpace(parts[0])
	repository = strings.TrimSpace(parts[1])
	if owner == "" || repository == "" ||
		strings.ContainsAny(owner, " \t\\") || strings.ContainsAny(repository, " \t\\") {
		return "", "", false
	}
	return owner, repository, true
}

// resolveReleaseURL returns the configured (or default) human-facing release
// page URL used in /version responses and startup banners. Invalid values fall
// back to the default.
func resolveReleaseURL() string {
	raw := strings.TrimSpace(os.Getenv(EnvUpdateCheckReleaseURL))
	if raw == "" {
		return DefaultReleaseURL
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		log.Warnf("忽略无效的 %s：%q", EnvUpdateCheckReleaseURL, raw)
		return DefaultReleaseURL
	}
	return raw
}
