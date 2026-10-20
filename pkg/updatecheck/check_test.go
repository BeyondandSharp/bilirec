package updatecheck

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// resetUpdateCheckState isolates a test from the package-level cache and the
// build-time injected version, and clears every update-check env key so tests
// never depend on (or leak into) the ambient environment.
func resetUpdateCheckState(t *testing.T) {
	t.Helper()

	oldVersion, oldCache := currentVersionInjected, cached
	currentVersionInjected = ""
	cacheMu.Lock()
	cached = Result{}
	cacheMu.Unlock()
	t.Cleanup(func() {
		currentVersionInjected = oldVersion
		cacheMu.Lock()
		cached = oldCache
		cacheMu.Unlock()
	})

	for _, key := range []string{
		EnvCheckUpdate,
		EnvUpdateCheckRepo,
		EnvUpdateCheckReleaseURL,
		EnvUpdateCheckAPIURL,
		EnvGitHubAPIURL,
		"GOLATEST_DISABLE",
	} {
		t.Setenv(key, "")
	}
}

func setInjectedVersion(t *testing.T, version string) {
	t.Helper()
	currentVersionInjected = version
}

// fakeGitHubAPI starts an httptest server and points the update check at it
// through GITHUB_API_URL.
func fakeGitHubAPI(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	t.Setenv(EnvGitHubAPIURL, server.URL)
	return server
}

// writeTags answers a GitHub "list tags" request with the given tag names.
func writeTags(w http.ResponseWriter, names ...string) {
	entries := make([]string, 0, len(names))
	for _, name := range names {
		entries = append(entries, `{"name":"`+name+`","commit":{"sha":"deadbeef"}}`)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte("[" + strings.Join(entries, ",") + "]"))
}

func tagsPath(owner, repository string) string {
	return "/repos/" + owner + "/" + repository + "/tags"
}

func githubTagsHandler(t *testing.T, tags ...string) (http.HandlerFunc, *atomic.Int64) {
	t.Helper()
	var calls atomic.Int64
	return func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != tagsPath(DefaultOwner, DefaultRepository) {
			t.Errorf("unexpected request path %q", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		writeTags(w, tags...)
	}, &calls
}

func TestCheckDefaultsToOurRepo(t *testing.T) {
	resetUpdateCheckState(t)
	setInjectedVersion(t, "v0.1.0")

	handler, calls := githubTagsHandler(t, "v0.4.0", "v0.1.0")
	fakeGitHubAPI(t, handler)

	res, err := Check()
	if err != nil {
		t.Fatalf("Check() error: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected exactly one API call, got %d", calls.Load())
	}
	if res.Current != "v0.1.0" {
		t.Fatalf("current = %q", res.Current)
	}
	if res.Latest != "v0.4.0" {
		t.Fatalf("latest = %q, want v0.4.0", res.Latest)
	}
	if !res.Outdated || !res.Checked {
		t.Fatalf("expected outdated+checked, got %+v", res)
	}
	if res.URL != DefaultReleaseURL {
		t.Fatalf("release URL = %q, want %q", res.URL, DefaultReleaseURL)
	}
	if res.Error != "" || res.ErrorCode != "" {
		t.Fatalf("unexpected error fields: %+v", res)
	}
	if got := Cached(); got != res {
		t.Fatalf("cache = %+v, want %+v", got, res)
	}
}

func TestCheckLatestWithoutVPrefix(t *testing.T) {
	resetUpdateCheckState(t)
	setInjectedVersion(t, "v0.2.0")

	fakeGitHubAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != tagsPath(DefaultOwner, DefaultRepository) {
			t.Errorf("unexpected request path %q", r.URL.Path)
		}
		writeTags(w, "0.2.0", "0.1.0")
	})

	res, err := Check()
	if err != nil {
		t.Fatalf("Check() error: %v", err)
	}
	if res.Latest != "v0.2.0" {
		t.Fatalf("latest = %q, want v0.2.0", res.Latest)
	}
	if res.Outdated {
		t.Fatalf("same version must not be outdated: %+v", res)
	}
}

func TestCheckCustomRepo(t *testing.T) {
	tests := []struct {
		name string
		env  string
	}{
		{name: "owner slash repo", env: "SomeOwner/some-repo"},
		{name: "github url", env: "https://github.com/SomeOwner/some-repo"},
		{name: "github url trailing slash dot git", env: "https://github.com/SomeOwner/some-repo.git/"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resetUpdateCheckState(t)
			setInjectedVersion(t, "v0.1.0")
			t.Setenv(EnvUpdateCheckRepo, test.env)

			fakeGitHubAPI(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tagsPath("SomeOwner", "some-repo") {
					t.Errorf("unexpected request path %q", r.URL.Path)
					http.NotFound(w, r)
					return
				}
				writeTags(w, "v0.9.0")
			})

			res, err := Check()
			if err != nil {
				t.Fatalf("Check() error: %v", err)
			}
			if res.Latest != "v0.9.0" || !res.Outdated {
				t.Fatalf("unexpected result: %+v", res)
			}
		})
	}
}

func TestCheckInvalidRepoFallsBackToDefault(t *testing.T) {
	resetUpdateCheckState(t)
	setInjectedVersion(t, "v0.1.0")
	t.Setenv(EnvUpdateCheckRepo, "not-a-repo")

	handler, calls := githubTagsHandler(t, "v0.1.0")
	fakeGitHubAPI(t, handler)

	res, err := Check()
	if err != nil {
		t.Fatalf("Check() error: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected one API call, got %d", calls.Load())
	}
	if res.Latest != "v0.1.0" {
		t.Fatalf("latest = %q", res.Latest)
	}
}

func TestCheckCustomReleaseURL(t *testing.T) {
	const customURL = "https://example.com/our/releases/latest"

	t.Run("success result", func(t *testing.T) {
		resetUpdateCheckState(t)
		setInjectedVersion(t, "v0.1.0")
		t.Setenv(EnvUpdateCheckReleaseURL, customURL)

		handler, _ := githubTagsHandler(t, "v0.2.0")
		fakeGitHubAPI(t, handler)

		res, err := Check()
		if err != nil {
			t.Fatalf("Check() error: %v", err)
		}
		if res.URL != customURL {
			t.Fatalf("release URL = %q, want %q", res.URL, customURL)
		}
	})

	t.Run("failure result", func(t *testing.T) {
		resetUpdateCheckState(t)
		setInjectedVersion(t, "v0.1.0")
		t.Setenv(EnvUpdateCheckReleaseURL, customURL)

		fakeGitHubAPI(t, func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		})

		res, err := Check()
		if err == nil {
			t.Fatal("expected error")
		}
		if res.URL != customURL {
			t.Fatalf("release URL = %q, want %q", res.URL, customURL)
		}
	})

	t.Run("no injected version", func(t *testing.T) {
		resetUpdateCheckState(t)
		t.Setenv(EnvUpdateCheckReleaseURL, customURL)

		fakeGitHubAPI(t, func(w http.ResponseWriter, r *http.Request) {
			t.Errorf("must not request the API without an injected version")
			http.NotFound(w, r)
		})

		res, err := Check()
		if err != nil {
			t.Fatalf("Check() error: %v", err)
		}
		if res.URL != customURL || res.Checked {
			t.Fatalf("unexpected result: %+v", res)
		}
	})

	t.Run("invalid falls back to default", func(t *testing.T) {
		resetUpdateCheckState(t)
		setInjectedVersion(t, "v0.1.0")
		t.Setenv(EnvUpdateCheckReleaseURL, "not-a-url")

		handler, _ := githubTagsHandler(t, "v0.1.0")
		fakeGitHubAPI(t, handler)

		res, err := Check()
		if err != nil {
			t.Fatalf("Check() error: %v", err)
		}
		if res.URL != DefaultReleaseURL {
			t.Fatalf("release URL = %q, want default", res.URL)
		}
	})
}

func TestCheckAPIURLOverride(t *testing.T) {
	t.Run("GITHUB_API_URL", func(t *testing.T) {
		resetUpdateCheckState(t)
		setInjectedVersion(t, "v0.1.0")

		handler, calls := githubTagsHandler(t, "v0.1.0")
		fakeGitHubAPI(t, handler)

		if _, err := Check(); err != nil {
			t.Fatalf("Check() error: %v", err)
		}
		if calls.Load() != 1 {
			t.Fatalf("expected one API call, got %d", calls.Load())
		}
	})

	t.Run("UPDATE_CHECK_API_URL alias takes precedence", func(t *testing.T) {
		resetUpdateCheckState(t)
		setInjectedVersion(t, "v0.1.0")

		aliasCalls := 0
		alias := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			aliasCalls++
			if r.URL.Path != tagsPath(DefaultOwner, DefaultRepository) {
				t.Errorf("unexpected alias request path %q", r.URL.Path)
			}
			writeTags(w, "v0.1.0")
		}))
		t.Cleanup(alias.Close)

		legacyCalls := 0
		legacy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			legacyCalls++
			http.Error(w, "legacy API must not be used", http.StatusInternalServerError)
		}))
		t.Cleanup(legacy.Close)

		t.Setenv(EnvUpdateCheckAPIURL, alias.URL)
		t.Setenv(EnvGitHubAPIURL, legacy.URL)

		if _, err := Check(); err != nil {
			t.Fatalf("Check() error: %v", err)
		}
		if aliasCalls != 1 {
			t.Fatalf("alias API calls = %d, want 1", aliasCalls)
		}
		if legacyCalls != 0 {
			t.Fatalf("legacy API calls = %d, want 0", legacyCalls)
		}
	})
}

func TestCheckRateLimit(t *testing.T) {
	resetUpdateCheckState(t)
	setInjectedVersion(t, "v0.1.0")

	fakeGitHubAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Limit", "60")
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(65*time.Second).Unix(), 10))
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"API rate limit exceeded for 203.0.113.1."}`))
	})

	res, err := Check()
	if err == nil {
		t.Fatal("expected rate limit error")
	}
	if res.ErrorCode != ErrorCodeGitHubRateLimit {
		t.Fatalf("error code = %q, want %q (err=%v)", res.ErrorCode, ErrorCodeGitHubRateLimit, err)
	}
	if res.RetryAfterSecs < 60 || res.RetryAfterSecs > 70 {
		t.Fatalf("retry_after_secs = %d, want ~65", res.RetryAfterSecs)
	}
	if res.Error == "" || strings.Contains(res.Error, "api.github.com") {
		t.Fatalf("unexpected message: %q", res.Error)
	}
	if res.URL != DefaultReleaseURL {
		t.Fatalf("release URL = %q", res.URL)
	}
	if got := Cached(); got.ErrorCode != ErrorCodeGitHubRateLimit {
		t.Fatalf("cache not updated: %+v", got)
	}
}

func TestCheckUnreachable(t *testing.T) {
	resetUpdateCheckState(t)
	setInjectedVersion(t, "v0.1.0")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	serverURL := server.URL
	server.Close()
	t.Setenv(EnvGitHubAPIURL, serverURL)

	res, err := Check()
	if err == nil {
		t.Fatal("expected unreachable error")
	}
	if res.ErrorCode != ErrorCodeGitHubUnreachable {
		t.Fatalf("error code = %q, want %q (err=%v)", res.ErrorCode, ErrorCodeGitHubUnreachable, err)
	}
	if res.URL != DefaultReleaseURL {
		t.Fatalf("release URL = %q", res.URL)
	}
}

func TestCheckGitHubServerError(t *testing.T) {
	resetUpdateCheckState(t)
	setInjectedVersion(t, "v0.1.0")

	fakeGitHubAPI(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})

	res, err := Check()
	if err == nil {
		t.Fatal("expected error")
	}
	if res.ErrorCode != ErrorCodeGitHubError {
		t.Fatalf("error code = %q, want %q (err=%v)", res.ErrorCode, ErrorCodeGitHubError, err)
	}
}

func TestCheckNoComparableTags(t *testing.T) {
	resetUpdateCheckState(t)
	setInjectedVersion(t, "v0.1.0")

	fakeGitHubAPI(t, func(w http.ResponseWriter, r *http.Request) {
		writeTags(w, "nightly", "latest")
	})

	res, err := Check()
	if err == nil {
		t.Fatal("expected error when no tag parses as semver")
	}
	if res.ErrorCode != ErrorCodeGitHubError {
		t.Fatalf("error code = %q, want %q", res.ErrorCode, ErrorCodeGitHubError)
	}
}
