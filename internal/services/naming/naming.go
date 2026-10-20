// SPDX-License-Identifier: GPL-3.0-or-later

// Package naming renders recording file paths from configurable templates
// (docs/FEATURES-V2.md §3.2, amended: the template is a PATH template relative
// to OUTPUT_DIR and "/" creates subdirectories).
package naming

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DefaultTemplate is the contract default used when a non-empty template is
// missing. An empty setting keeps the legacy v0.x naming instead (so defaults
// remain byte-identical to previous releases).
const DefaultTemplate = "{title}-{year}{month}{day}_{hour}{minute}{second}"

const (
	// maxTemplateBytes is the raw template length cap (§3.2).
	maxTemplateBytes = 256
	// maxSegmentBytes caps each rendered path segment (§3.2).
	maxSegmentBytes = 200
	// maxTotalBytes caps the rendered relative path (§3.2).
	maxTotalBytes = 512
	// maxTitleRunes is the hard cap on a substituted title (§3.2).
	maxTitleRunes = 80
)

// Vars are the template variables for one recording segment.
type Vars struct {
	RoomID  int
	ShortID int
	Uname   string
	Title   string
	Time    time.Time
	Segment int
}

// nowFunc is overridable in tests for deterministic collision timestamps.
var nowFunc = time.Now

// ValidatePathTemplate validates the structural rules of a path template
// without rendering it: empty means "use legacy naming" and is valid; otherwise
// the template must not be absolute, must not contain "\", control characters,
// ".", ".." or empty segments, must be <=256 bytes and each literal segment
// must be <=200 bytes.
func ValidatePathTemplate(template string) error {
	if strings.TrimSpace(template) == "" {
		return nil
	}
	if len(template) > maxTemplateBytes {
		return fmt.Errorf("模板不能超过 %d 字节", maxTemplateBytes)
	}
	if strings.HasPrefix(template, "/") {
		return errors.New("不能使用绝对路径（以 / 开头）")
	}
	if strings.ContainsRune(template, '\\') {
		return errors.New("不能包含反斜杠 \\")
	}
	for _, r := range template {
		if r < 0x20 || r == 0x7f {
			return errors.New("不能包含控制字符")
		}
	}
	segments := strings.Split(template, "/")
	for idx, segment := range segments {
		if segment == "" {
			return fmt.Errorf("第 %d 个路径段为空（不能使用 //、结尾 / 或以 / 开头）", idx+1)
		}
		if segment == "." || segment == ".." {
			return fmt.Errorf("不允许 . 或 .. 路径段")
		}
		if len(segment) > maxSegmentBytes {
			return fmt.Errorf("第 %d 个路径段不能超过 %d 字节", idx+1, maxSegmentBytes)
		}
	}
	return nil
}

// RenderPath expands a path template into a clean slash-separated relative path
// under OUTPUT_DIR. Variable values are sanitized and cannot introduce path
// separators; literal segments are sanitized the same way. The result is
// validated against the §3.2 rules (no absolute/../backslash/control/empty
// segments, segment <=200B, total <=512B).
func RenderPath(template string, vars Vars) (string, error) {
	template = strings.TrimSpace(template)
	if template == "" {
		return "", errors.New("路径模板为空")
	}
	if err := ValidatePathTemplate(template); err != nil {
		return "", err
	}
	if vars.Time.IsZero() {
		vars.Time = nowFunc()
	}

	// Translate strftime directives before substitution so "%" inside a value
	// is never interpreted as a directive.
	rendered := translateStrftime(template, vars.Time)
	rendered = substituteVars(rendered, vars)

	segments := strings.Split(rendered, "/")
	out := make([]string, 0, len(segments))
	total := 0
	for idx, segment := range segments {
		if segment == "." || segment == ".." {
			return "", errors.New("不允许 . 或 .. 路径段")
		}
		if strings.ContainsRune(segment, '\\') {
			return "", errors.New("不能包含反斜杠 \\")
		}
		clean := sanitizeValue(segment, 0)
		if clean == "" {
			return "", fmt.Errorf("第 %d 个路径段渲染结果为空", idx+1)
		}
		if len(clean) > maxSegmentBytes {
			return "", fmt.Errorf("第 %d 个路径段超过 %d 字节", idx+1, maxSegmentBytes)
		}
		total += len(clean)
		if idx > 0 {
			total++ // separator
		}
		if total > maxTotalBytes {
			return "", fmt.Errorf("路径总长度超过 %d 字节", maxTotalBytes)
		}
		out = append(out, clean)
	}
	return strings.Join(out, "/"), nil
}

// ResolvePathInRoot renders a path template and resolves a non-colliding file
// path under rootDir:
//   - parent directories are created with MkdirAll(0755);
//   - the final basename gets the legacy "-<segment>" suffix for segment > 0;
//   - collisions append -1..-999 (segment 0) and finally a timestamp;
//   - the resolved absolute path is verified to stay inside rootDir.
func ResolvePathInRoot(rootDir, template, ext string, vars Vars) (string, error) {
	rel, err := RenderPath(template, vars)
	if err != nil {
		return "", err
	}
	rootAbs, err := filepath.Abs(rootDir)
	if err != nil {
		return "", err
	}
	relDir, relBase := path.Split(rel)
	relDir = strings.TrimSuffix(relDir, "/")
	dirAbs := rootAbs
	if relDir != "" {
		dirAbs = filepath.Join(rootAbs, filepath.FromSlash(relDir))
	}
	if err := os.MkdirAll(dirAbs, 0o755); err != nil {
		return "", err
	}
	full, err := UniquePath(dirAbs, relBase, ext, vars.Segment)
	if err != nil {
		return "", err
	}
	if !isWithinRoot(rootAbs, full) {
		return "", fmt.Errorf("模板路径越界：%s", rel)
	}
	// Preserve the caller's root style: a relative OUTPUT_DIR yields a
	// relative path (legacy style), an absolute one stays absolute.
	if filepath.IsAbs(rootDir) {
		return full, nil
	}
	relToRoot, err := filepath.Rel(rootAbs, full)
	if err != nil {
		return full, nil
	}
	return filepath.Join(rootDir, relToRoot), nil
}

// isWithinRoot reports whether full is inside root (defense in depth on top of
// the structural template validation).
func isWithinRoot(root, full string) bool {
	rel, err := filepath.Rel(root, full)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// UniquePath resolves a non-colliding path for base+ext inside dir. For
// segment > 0 the legacy "-<segment>" suffix is appended to the basename.
func UniquePath(dir, base, ext string, segment int) (string, error) {
	if segment > 0 {
		base = fmt.Sprintf("%s-%d", base, segment)
	}
	candidate := filepath.Join(dir, base+ext)
	if !pathExists(candidate) {
		return candidate, nil
	}
	if segment == 0 {
		for i := 1; i <= 999; i++ {
			candidate = filepath.Join(dir, fmt.Sprintf("%s-%d%s", base, i, ext))
			if !pathExists(candidate) {
				return candidate, nil
			}
		}
	}
	stamp := strconv.FormatInt(nowFunc().UnixNano(), 10)
	return filepath.Join(dir, fmt.Sprintf("%s-%s%s", base, stamp, ext)), nil
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// substituteVars replaces known {name} tokens with sanitized values; unknown
// tokens are kept literally.
func substituteVars(template string, vars Vars) string {
	replacements := []struct {
		token string
		value string
	}{
		{"{roomid}", strconv.Itoa(vars.RoomID)},
		{"{shortid}", strconv.Itoa(vars.ShortID)},
		{"{uname}", sanitizeValue(vars.Uname, 0)},
		{"{title}", sanitizeValue(vars.Title, maxTitleRunes)},
		{"{year}", vars.Time.Format("2006")},
		{"{month}", vars.Time.Format("01")},
		{"{day}", vars.Time.Format("02")},
		{"{hour}", vars.Time.Format("15")},
		{"{minute}", vars.Time.Format("04")},
		{"{second}", vars.Time.Format("05")},
		{"{date}", vars.Time.Format("20060102")},
		{"{time}", vars.Time.Format("150405")},
		{"{segment}", strconv.Itoa(vars.Segment)},
	}
	for _, replacement := range replacements {
		template = strings.ReplaceAll(template, replacement.token, replacement.value)
	}
	return template
}

// translateStrftime expands the supported strftime directives for the given
// time; any other character (including unknown %-directives) is kept literally.
func translateStrftime(template string, now time.Time) string {
	layouts := map[byte]string{
		'Y': "2006",
		'm': "01",
		'd': "02",
		'H': "15",
		'M': "04",
		'S': "05",
	}
	var b strings.Builder
	for i := 0; i < len(template); i++ {
		if template[i] != '%' || i+1 >= len(template) {
			b.WriteByte(template[i])
			continue
		}
		layout, ok := layouts[template[i+1]]
		if !ok {
			b.WriteByte('%')
			b.WriteByte(template[i+1])
			i++
			continue
		}
		b.WriteString(now.Format(layout))
		i++
	}
	return b.String()
}

// sanitizeValue removes path separators, reserved characters and control
// characters, trims leading/trailing dots and spaces, and optionally truncates
// to maxRunes runes (0 = no rune cap).
func sanitizeValue(value string, maxRunes int) string {
	if maxRunes > 0 {
		runes := []rune(value)
		if len(runes) > maxRunes {
			value = string(runes[:maxRunes])
		}
	}
	var b strings.Builder
	for _, r := range value {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			continue
		}
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	return strings.Trim(b.String(), " .")
}
