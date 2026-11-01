// SPDX-License-Identifier: GPL-3.0-only

package config

// Test-only read-only config constructors.
//
// Upstream v0.4.0 removed these helpers from production config code (they exist
// only so unit tests in other packages can build a *GlobalReadOnly without booting
// the fx graph). The fork still uses them across ~11 test files, so they are kept
// in this fork-owned file instead of being reintroduced into global.go. Keeping
// them out of global.go avoids a recurring merge conflict with upstream removals.
//
// See docs/DECISIONS.md D022.

// NewGlobalReadOnlyForTest builds read-only config for unit tests in other packages.
func NewGlobalReadOnlyForTest(dropFilePageCache bool, liveStreamWriterColdCacheReleaseSecs int) *GlobalReadOnly {
	return &GlobalReadOnly{config: &Config{
		dropFilePageCache:                      dropFilePageCache,
		liveStreamWriterColdCacheReleasePeriod: liveStreamWriterColdCacheReleaseSecs,
	}}
}

// NewGlobalReadOnlyWithAuthForTest builds read-only config with REST auth and
// viewer RBAC enabled for unit tests (hashes must be bcrypt strings).
func NewGlobalReadOnlyWithAuthForTest(username, passwordHash, viewerUsername, viewerPasswordHash, jwtSecret string) *GlobalReadOnly {
	return &GlobalReadOnly{config: &Config{
		Username:           username,
		PasswordHash:       passwordHash,
		ViewerUsername:     viewerUsername,
		ViewerPasswordHash: viewerPasswordHash,
		JwtSecret:          jwtSecret,
	}}
}

// NewGlobalReadOnlyWithDanmakuOverflowForTest builds read-only config with a
// specific DANMAKU_OVERFLOW_POLICY for unit tests.
func NewGlobalReadOnlyWithDanmakuOverflowForTest(policy string) *GlobalReadOnly {
	return &GlobalReadOnly{config: &Config{DanmakuOverflowPolicy: policy}}
}
