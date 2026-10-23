package settings

import (
	"fmt"

	"github.com/bilirec/bilirec/pkg/size"
)

func parseSizeValue(value, unit string) (int64, error) {
	if unit == "" {
		unit = "B"
	}
	value = stringsTrim(value)
	if value == "" {
		return 0, fmt.Errorf("empty size")
	}
	return size.Parse(value, unit)
}

func stringsTrim(value string) string {
	for len(value) > 0 && (value[0] == ' ' || value[0] == '\t' || value[0] == '\n' || value[0] == '\r') {
		value = value[1:]
	}
	for len(value) > 0 {
		last := value[len(value)-1]
		if last != ' ' && last != '\t' && last != '\n' && last != '\r' {
			break
		}
		value = value[:len(value)-1]
	}
	return value
}
