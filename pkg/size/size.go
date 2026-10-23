// SPDX-License-Identifier: GPL-3.0-only

// Package size parses human-readable storage sizes used by settings and
// environment variables. Binary units are used: KiB=1024 bytes.
package size

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Units are the supported binary storage units in ascending order.
var Units = []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}

var factors = map[string]int64{
	"B":   1,
	"KIB": 1 << 10,
	"MIB": 1 << 20,
	"GIB": 1 << 30,
	"TIB": 1 << 40,
	"PIB": 1 << 50,
}

var pattern = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)\s*([A-Za-z]+)?$`)

// Parse parses raw into bytes. A bare number uses defaultUnit (for example
// "MiB" for legacy MB-valued settings). Suffixed values use their unit.
func Parse(raw, defaultUnit string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("size is empty")
	}
	match := pattern.FindStringSubmatch(raw)
	if match == nil {
		return 0, fmt.Errorf("invalid size %q", raw)
	}
	number, err := strconv.ParseFloat(match[1], 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || number < 0 {
		return 0, fmt.Errorf("invalid size %q", raw)
	}
	unit := strings.ToUpper(match[2])
	if unit == "" {
		unit = strings.ToUpper(defaultUnit)
	}
	factor, ok := factors[unit]
	if !ok {
		return 0, fmt.Errorf("unsupported size unit %q", match[2])
	}
	value := number * float64(factor)
	if value > math.MaxInt64 {
		return 0, fmt.Errorf("size out of range %q", raw)
	}
	return int64(math.Round(value)), nil
}

// ParseBytes parses raw with bare numbers interpreted as bytes.
func ParseBytes(raw string) (int64, error) {
	return Parse(raw, "B")
}

// ParseMiB parses raw with bare numbers interpreted as MiB and returns an
// integer MiB value. Tiny non-zero values are rounded up to 1 MiB so callers
// using integer-MiB APIs do not silently disable the setting.
func ParseMiB(raw string) (int, error) {
	bytes, err := Parse(raw, "MiB")
	if err != nil {
		return 0, err
	}
	if bytes == 0 {
		return 0, nil
	}
	mib := int64(math.Round(float64(bytes) / float64(1<<20)))
	if mib == 0 {
		mib = 1
	}
	return int(mib), nil
}

// FormatBytes renders bytes using the largest unit that still has an exact
// integer value; otherwise it falls back to the largest unit that can express
// the value without losing more than three decimals.
func FormatBytes(bytes int64) string {
	for i := len(Units) - 1; i >= 0; i-- {
		factor := factors[strings.ToUpper(Units[i])]
		if bytes%factor == 0 {
			return fmt.Sprintf("%d %s", bytes/factor, Units[i])
		}
	}
	for i := len(Units) - 1; i >= 0; i-- {
		factor := factors[strings.ToUpper(Units[i])]
		value := float64(bytes) / float64(factor)
		if value >= 1 {
			return strconv.FormatFloat(math.Round(value*1000)/1000, 'f', -1, 64) + " " + Units[i]
		}
	}
	return fmt.Sprintf("%d B", bytes)
}
