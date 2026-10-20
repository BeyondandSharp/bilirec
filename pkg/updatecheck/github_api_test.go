package updatecheck

import "testing"

func TestNormalizeGitHubAPIBaseURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"official", "https://api.github.com", defaultGitHubAPIBase},
		{"prefix full", "https://gh-proxy.com/https://api.github.com/", "https://gh-proxy.com/https://api.github.com/"},
		{"host mirror", "https://mirror.example.com", "https://mirror.example.com/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeGitHubAPIBaseURL(tt.in)
			if got != tt.want {
				t.Fatalf("NormalizeGitHubAPIBaseURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestGithubAPIBaseURLFromEnv(t *testing.T) {
	t.Setenv(EnvUpdateCheckAPIURL, "")
	t.Setenv(EnvGitHubAPIURL, "")
	if githubAPIBaseURL() != "" {
		t.Fatal("expected empty base when env unset")
	}

	t.Setenv(EnvGitHubAPIURL, "https://api.github.com/")
	if githubAPIBaseURL() != "" {
		t.Fatal("expected empty base for official URL")
	}

	t.Setenv(EnvGitHubAPIURL, "https://gh-proxy.org/https://api.github.com/")
	got := githubAPIBaseURL()
	if got != "https://gh-proxy.org/https://api.github.com/" {
		t.Fatalf("unexpected base: %q", got)
	}
}

func TestGithubAPIBaseURLUpdateCheckAlias(t *testing.T) {
	t.Setenv(EnvGitHubAPIURL, "https://legacy-proxy.example.com/")
	t.Setenv(EnvUpdateCheckAPIURL, "")
	if got := githubAPIBaseURL(); got != "https://legacy-proxy.example.com/" {
		t.Fatalf("GITHUB_API_URL fallback = %q", got)
	}

	// UPDATE_CHECK_API_URL wins when both are set.
	t.Setenv(EnvUpdateCheckAPIURL, "https://alias-proxy.example.com")
	if got := githubAPIBaseURL(); got != "https://alias-proxy.example.com/" {
		t.Fatalf("alias = %q", got)
	}

	// Alias set to the official API must also disable the override.
	t.Setenv(EnvUpdateCheckAPIURL, "https://api.github.com")
	if got := githubAPIBaseURL(); got != "" {
		t.Fatalf("official alias base = %q, want empty", got)
	}
}

func TestResolveRepo(t *testing.T) {
	t.Setenv(EnvUpdateCheckRepo, "")
	owner, repository := resolveRepo()
	if owner != DefaultOwner || repository != DefaultRepository {
		t.Fatalf("default repo = %s/%s", owner, repository)
	}

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"owner slash repo", "SomeOwner/some-repo", "SomeOwner/some-repo"},
		{"whitespace", "  SomeOwner/some-repo  ", "SomeOwner/some-repo"},
		{"github url", "https://github.com/SomeOwner/some-repo", "SomeOwner/some-repo"},
		{"github url trailing slash", "https://github.com/SomeOwner/some-repo/", "SomeOwner/some-repo"},
		{"github url dot git", "https://github.com/SomeOwner/some-repo.git", "SomeOwner/some-repo"},
		{"invalid single name", "just-a-name", ""},
		{"invalid extra segment", "a/b/c", ""},
		{"invalid non github url", "https://gitlab.com/a/b", ""},
		{"invalid empty repo", "owner/", ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(EnvUpdateCheckRepo, test.in)
			owner, repository := resolveRepo()
			if test.want == "" {
				if owner != DefaultOwner || repository != DefaultRepository {
					t.Fatalf("invalid repo %q must fall back to default, got %s/%s", test.in, owner, repository)
				}
				return
			}
			if got := owner + "/" + repository; got != test.want {
				t.Fatalf("resolveRepo(%q) = %q, want %q", test.in, got, test.want)
			}
		})
	}
}

func TestResolveReleaseURL(t *testing.T) {
	t.Setenv(EnvUpdateCheckReleaseURL, "")
	if got := resolveReleaseURL(); got != DefaultReleaseURL {
		t.Fatalf("default release URL = %q", got)
	}

	t.Setenv(EnvUpdateCheckReleaseURL, "  https://example.com/releases/latest  ")
	if got := resolveReleaseURL(); got != "https://example.com/releases/latest" {
		t.Fatalf("custom release URL = %q", got)
	}

	for _, invalid := range []string{"not-a-url", "ftp://example.com/releases", "https://"} {
		t.Setenv(EnvUpdateCheckReleaseURL, invalid)
		if got := resolveReleaseURL(); got != DefaultReleaseURL {
			t.Fatalf("invalid release URL %q must fall back to default, got %q", invalid, got)
		}
	}
}
