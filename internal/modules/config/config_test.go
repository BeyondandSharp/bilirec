package config

import (
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
