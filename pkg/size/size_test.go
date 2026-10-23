package size

import "testing"

func TestParse(t *testing.T) {
	cases := map[string]int64{
		"0":       0,
		"1024":    1024,
		"1 KiB":   1024,
		"1.5 MiB": 1572864,
		"2GiB":    2 << 30,
		"1 TiB":   1 << 40,
		"1 PiB":   1 << 50,
	}
	for raw, want := range cases {
		got, err := Parse(raw, "B")
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %d, %v; want %d", raw, got, err, want)
		}
	}
	if _, err := Parse("nope", "B"); err == nil {
		t.Fatal("invalid size should fail")
	}
}

func TestParseMiB(t *testing.T) {
	cases := map[string]int{
		"20":      20,
		"20 MiB":  20,
		"2 GiB":   2048,
		"512 KiB": 1,
	}
	for raw, want := range cases {
		got, err := ParseMiB(raw)
		if err != nil || got != want {
			t.Errorf("ParseMiB(%q) = %d, %v; want %d", raw, got, err, want)
		}
	}
}
