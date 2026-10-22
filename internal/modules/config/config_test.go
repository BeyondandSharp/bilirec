package config

import (
	"os"
	"strings"
	"testing"
)

func TestParseLocalLogsOverflow(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{"", "drop"},
		{"drop", "drop"},
		{"DROP", "drop"},
		{"block", "block"},
		{" BLOCK ", "block"},
		{"unknown", "drop"},
	}
	for _, tt := range tests {
		if got := parseLocalLogsOverflow(tt.raw); got != tt.want {
			t.Errorf("parseLocalLogsOverflow(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

func TestParseFrontendURLs(t *testing.T) {
	t.Setenv("FRONTEND_URLS", "")
	t.Setenv("FRONTEND_URL", "")

	got, err := parseFrontendURLs()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != defaultFrontendOrigin {
		t.Fatalf("default: got %v", got)
	}
}

func TestParseFrontendURLs_primarySingle(t *testing.T) {
	t.Setenv("FRONTEND_URLS", "https://app.bilirec.org/")
	t.Setenv("FRONTEND_URL", "")

	got, err := parseFrontendURLs()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "https://app.bilirec.org" {
		t.Fatalf("got %v", got)
	}
}

func TestParseFrontendURLs_primaryMultiple(t *testing.T) {
	t.Setenv("FRONTEND_URLS", "https://app.bilirec.org, https://bilirec.example.com")
	t.Setenv("FRONTEND_URL", "")

	got, err := parseFrontendURLs()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"https://app.bilirec.org", "https://bilirec.example.com"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestParseFrontendURLs_aliasOnly(t *testing.T) {
	t.Setenv("FRONTEND_URLS", "")
	t.Setenv("FRONTEND_URL", "https://legacy.example.com")

	got, err := parseFrontendURLs()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "https://legacy.example.com" {
		t.Fatalf("got %v", got)
	}
}

func TestParseFrontendURLs_primaryWinsOverAlias(t *testing.T) {
	t.Setenv("FRONTEND_URLS", "https://primary.example.com")
	t.Setenv("FRONTEND_URL", "https://ignored.example.com")

	got, err := parseFrontendURLs()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "https://primary.example.com" {
		t.Fatalf("got %v", got)
	}
}

func TestParseFrontendURLs_dedupe(t *testing.T) {
	t.Setenv("FRONTEND_URLS", "https://app.bilirec.org,https://app.bilirec.org/foo")
	t.Setenv("FRONTEND_URL", "")

	got, err := parseFrontendURLs()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "https://app.bilirec.org" {
		t.Fatalf("got %v", got)
	}
}

func TestParseFrontendURLs_invalid(t *testing.T) {
	t.Setenv("FRONTEND_URLS", "not-a-url")
	t.Setenv("FRONTEND_URL", "")

	_, err := parseFrontendURLs()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseMaxRetrySeconds(t *testing.T) {
	cases := []struct {
		name    string
		seconds string
		minutes string
		want    int
	}{
		{name: "default", want: 30},
		{name: "seconds wins", seconds: "45", minutes: "10", want: 45},
		{name: "legacy minutes alias", minutes: "2", want: 120},
		{name: "zero disables", seconds: "0", want: 0},
		{name: "negative clamps", seconds: "-5", want: 0},
		{name: "empty seconds falls back to legacy", seconds: "", minutes: "1", want: 60},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MAX_RETRY_SECONDS", tc.seconds)
			t.Setenv("MAX_RETRY_MINUTES", tc.minutes)
			if got := parseMaxRetrySeconds(); got != tc.want {
				t.Fatalf("parseMaxRetrySeconds() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestParseLocalLogsSplit(t *testing.T) {
	cases := map[string]bool{
		"":        true,
		"true":    true,
		"false":   false,
		" FALSE ": false,
		"0":       true,
		"1":       true,
	}
	for raw, want := range cases {
		if got := parseLocalLogsSplit(raw); got != want {
			t.Errorf("parseLocalLogsSplit(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestParseLocalLogsGroups(t *testing.T) {
	old, had := os.LookupEnv("LOCAL_LOGS_GROUPS")
	t.Cleanup(func() {
		if had {
			_ = os.Setenv("LOCAL_LOGS_GROUPS", old)
		} else {
			_ = os.Unsetenv("LOCAL_LOGS_GROUPS")
		}
	})

	_ = os.Unsetenv("LOCAL_LOGS_GROUPS")
	if got := parseLocalLogsGroups(); got != nil {
		t.Fatalf("missing key should mean all groups, got %#v", got)
	}
	t.Setenv("LOCAL_LOGS_GROUPS", "")
	if got := parseLocalLogsGroups(); got == nil || len(got) != 0 {
		t.Fatalf("empty value should mean no groups, got %#v", got)
	}
	t.Setenv("LOCAL_LOGS_GROUPS", "ACCESS, live, unknown, live")
	if got := strings.Join(parseLocalLogsGroups(), ","); got != "access,live" {
		t.Fatalf("parsed groups = %q", got)
	}
}
