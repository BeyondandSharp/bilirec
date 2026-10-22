// SPDX-License-Identifier: GPL-3.0-only

// Package configfile implements the unified configuration center introduced by
// docs/CONFIG-V2.md: bootstrap of a flat JSON config file, a separate secrets
// file, .env fallbacks, precedence tracking and atomic persistence.
//
// The loader runs before logger.Init, so warnings go to the standard logger.
package configfile

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Bootstrap and test-only keys are never managed by /config or /settings.
var BootstrapKeys = []string{
	"CONFIG_FILE", "SECRETS_FILE", "ENV_FILE", "PUID", "PGID",
}

// SecretKeys are the only keys stored in secrets.json and exposed (masked) by
// /config/secrets. They never appear in the /config schema.
var SecretKeys = []string{
	"PASSWORD", "VIEWER_PASSWORD", "JWT_SECRET", "CLOUDCONVERT_API_KEY", "FRP_TOKEN", "NOTIFY_SSE_TOKEN",
}

// HotApplyKeys can be updated through /config and applied to runtime services
// without restarting the process.
var HotApplyKeys = []string{
	"WEBHOOK_URLS",
	"FRONTEND_URLS",
	"FRONTEND_URL",
	"LOCAL_LOGS_GROUPS",
}

// FeatureKeys are the FEATURES-V2 keys owned by /settings.
var FeatureKeys = []string{
	"SEGMENT_MODE", "SEGMENT_MINUTES", "SEGMENT_SIZE_MB", "SEGMENT_BY_TITLE",
	"MIN_FILE_SIZE_MB", "FILENAME_TEMPLATE", "TITLE_FILTER_PATTERNS", "SAVE_STREAM_COVER",
	"DANMAKU_RECORD_TYPES", "DANMAKU_TRANSPORT", "FLV_WRITE_METADATA", "FLV_SPLIT_ON_SCRIPT_TAG",
	"FLV_DISABLE_SPLIT_ON_ANNEXB", "BILI_LIVE_API_HOST", "BILI_LIVE_API_FALLBACK_HOST",
	"BILI_CDN", "BILI_CDN_FALLBACK", "BILI_FORCE_SOURCE", "BILI_REPLACE_CN01",
}

var (
	secretKeySet  = toSet(SecretKeys)
	bootstrapSet  = toSet(BootstrapKeys)
	featureKeySet = toSet(FeatureKeys)
	hotApplySet   = toSet(HotApplyKeys)
)

func toSet(keys []string) map[string]struct{} {
	set := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		set[key] = struct{}{}
	}
	return set
}

// IsSecretKey reports whether key belongs to secrets.json.
func IsSecretKey(key string) bool {
	_, ok := secretKeySet[key]
	return ok
}

// IsFeatureKey reports whether key is a FEATURES-V2 /settings key.
func IsFeatureKey(key string) bool {
	_, ok := featureKeySet[key]
	return ok
}

// IsHotApplyKey reports whether key can be applied without a process restart.
func IsHotApplyKey(key string) bool {
	_, ok := hotApplySet[key]
	return ok
}

// IsBootstrapKey reports whether key is bootstrap/test-only.
func IsBootstrapKey(key string) bool {
	_, ok := bootstrapSet[key]
	return ok
}

// Source records where the effective value of a key came from.
type Source string

const (
	SourceEnv      Source = "env"
	SourceFile     Source = "file"
	SourceSecret   Source = "secret-file"
	SourceDotEnv   Source = "dotenv"
	SourceDefault  Source = "default"
	defaultEnvFile        = ".env"
)

// Options are the resolved paths for the config center.
type Options struct {
	ConfigPath  string
	SecretsPath string
	EnvPath     string
}

// ResolveOptions resolves CONFIG_FILE / SECRETS_FILE / ENV_FILE from the system
// environment, applying the Docker/native defaults from docs/CONFIG-V2.md §1.
func ResolveOptions() Options {
	opts := Options{}
	if value := strings.TrimSpace(os.Getenv("CONFIG_FILE")); value != "" {
		opts.ConfigPath = value
	} else if _, err := os.Stat("/.dockerenv"); err == nil {
		opts.ConfigPath = "/app/config/config.json"
	} else {
		opts.ConfigPath = "config.json"
	}
	if value := strings.TrimSpace(os.Getenv("SECRETS_FILE")); value != "" {
		opts.SecretsPath = value
	} else if _, err := os.Stat("/.dockerenv"); err == nil {
		opts.SecretsPath = "/app/config/secrets.json"
	} else {
		opts.SecretsPath = "secrets.json"
	}
	opts.EnvPath = strings.TrimSpace(os.Getenv("ENV_FILE"))
	return opts
}

// Store holds the loaded files, source tracking and restart bookkeeping.
type Store struct {
	mu sync.RWMutex

	opts Options

	configValues  map[string]string // config.json (unknown keys preserved)
	secretsValues map[string]string // secrets.json

	dotEnv map[string]string

	// systemEnv is the set of keys present in the real environment before the
	// loader applied file values. Those keys are env-locked (highest precedence).
	systemEnv map[string]struct{}

	sources         map[string]Source
	restartRequired map[string]struct{}

	changeMu        sync.RWMutex
	changeListeners []func(ConfigChange)
}

// ConfigChange describes one effective config value change after a successful
// /config write. Present=false means the key was removed.
type ConfigChange struct {
	Key     string
	Value   string
	Present bool
}

// OnConfigChange registers a listener invoked after config values are persisted.
// Listeners are called synchronously after the store lock is released.
func (s *Store) OnConfigChange(fn func(ConfigChange)) {
	if s == nil || fn == nil {
		return
	}
	s.changeMu.Lock()
	s.changeListeners = append(s.changeListeners, fn)
	s.changeMu.Unlock()
}

func (s *Store) notifyConfigChanges(changes []ConfigChange) {
	if s == nil || len(changes) == 0 {
		return
	}
	s.changeMu.RLock()
	listeners := append([]func(ConfigChange){}, s.changeListeners...)
	s.changeMu.RUnlock()
	for _, change := range changes {
		for _, fn := range listeners {
			func() {
				defer func() {
					if recovered := recover(); recovered != nil {
						log.Printf("configfile: config change listener panic: %v", recovered)
					}
				}()
				fn(change)
			}()
		}
	}
}

// LoadStore loads the files and .env chain without applying values to the
// process environment. It is used directly by tests.
func LoadStore(opts Options) (*Store, error) {
	store := &Store{
		opts:            opts,
		configValues:    make(map[string]string),
		secretsValues:   make(map[string]string),
		dotEnv:          make(map[string]string),
		systemEnv:       make(map[string]struct{}),
		sources:         make(map[string]Source),
		restartRequired: make(map[string]struct{}),
	}
	for _, key := range os.Environ() {
		if idx := strings.IndexByte(key, '='); idx > 0 {
			store.systemEnv[key[:idx]] = struct{}{}
		}
	}
	if err := store.loadFiles(); err != nil {
		return nil, err
	}
	store.loadDotEnv()
	return store, nil
}

// LoadAndApply loads the config center, runs startup migration, applies the
// precedence chain (system env > config > secrets > dotenv > default) by
// setting only keys absent from the system environment, and returns the store.
func LoadAndApply() (*Store, error) {
	opts := ResolveOptions()
	for _, path := range []string{opts.ConfigPath, opts.SecretsPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			log.Printf("configfile: cannot create directory for %s: %v", path, err)
		}
	}
	store, err := LoadStore(opts)
	if err != nil {
		return nil, err
	}
	if err := store.migrate(); err != nil {
		log.Printf("configfile: startup migration failed: %v", err)
	}
	store.apply()
	return store, nil
}

// ConfigPath returns the resolved config.json path.
func (s *Store) ConfigPath() string {
	if s == nil {
		return ""
	}
	return s.opts.ConfigPath
}

// SecretsPath returns the resolved secrets.json path.
func (s *Store) SecretsPath() string {
	if s == nil {
		return ""
	}
	return s.opts.SecretsPath
}

// EnvPath returns the resolved .env path (may be empty when none was used).
func (s *Store) EnvPath() string {
	if s == nil {
		return ""
	}
	return s.opts.EnvPath
}

// Source returns the source of the effective value for key.
func (s *Store) Source(key string) Source {
	if s == nil {
		return SourceDefault
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.systemEnv[key]; ok {
		return SourceEnv
	}
	if _, ok := s.sources[key]; ok {
		return s.sources[key]
	}
	if _, ok := s.dotEnv[key]; ok {
		return SourceDotEnv
	}
	if _, ok := s.configValues[key]; ok {
		return SourceFile
	}
	if _, ok := s.secretsValues[key]; ok {
		return SourceSecret
	}
	return SourceDefault
}

// IsEnvLocked reports whether key is provided by the real environment and can
// therefore not be overridden by the config API.
func (s *Store) IsEnvLocked(key string) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.systemEnv[key]
	return ok
}

// EnvLockedKeys returns all env-locked keys known to the loader.
func (s *Store) EnvLockedKeys() []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]string, 0, len(s.systemEnv))
	for key := range s.systemEnv {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// ConfigValue returns the value stored in config.json (not the effective one).
func (s *Store) ConfigValue(key string) (string, bool) {
	if s == nil {
		return "", false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.configValues[key]
	return value, ok
}

// SecretIsSet reports whether a secret key has a stored value.
func (s *Store) SecretIsSet(key string) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.secretsValues[key]
	return ok
}

// ConfigOverrides returns the stored config.json values for the given managed
// keys (only keys present in the file).
func (s *Store) ConfigOverrides(keys []string) map[string]string {
	if s == nil {
		return map[string]string{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := s.configValues[key]; ok {
			out[key] = value
		}
	}
	return out
}

// MarkRestartRequired records keys changed at runtime that need a restart.
func (s *Store) MarkRestartRequired(keys []string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, key := range keys {
		if key == "" {
			continue
		}
		s.restartRequired[key] = struct{}{}
	}
}

// RestartRequired returns the sorted set of keys pending a restart.
func (s *Store) RestartRequired() []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]string, 0, len(s.restartRequired))
	for key := range s.restartRequired {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// apply sets file/dotenv values for keys absent from the real environment.
func (s *Store) apply() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, value := range s.configValues {
		if _, locked := s.systemEnv[key]; locked || IsSecretKey(key) {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		_ = os.Setenv(key, value)
		s.sources[key] = SourceFile
	}
	for key, value := range s.secretsValues {
		if _, locked := s.systemEnv[key]; locked {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		_ = os.Setenv(key, value)
		s.sources[key] = SourceSecret
	}
	for key, value := range s.dotEnv {
		if _, locked := s.systemEnv[key]; locked {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		_ = os.Setenv(key, value)
		s.sources[key] = SourceDotEnv
	}
}

// effectiveValue resolves the effective value for key following precedence.
func (s *Store) effectiveValue(key, defaultValue string) (string, Source) {
	if s != nil {
		s.mu.RLock()
		locked := false
		if _, ok := s.systemEnv[key]; ok {
			locked = true
		}
		configValue, inConfig := s.configValues[key]
		secretValue, inSecrets := s.secretsValues[key]
		dotEnvValue, inDotEnv := s.dotEnv[key]
		s.mu.RUnlock()
		if locked {
			if value, ok := os.LookupEnv(key); ok {
				return value, SourceEnv
			}
		}
		if IsSecretKey(key) {
			if inSecrets {
				return secretValue, SourceSecret
			}
		} else if inConfig {
			return configValue, SourceFile
		}
		if inDotEnv {
			return dotEnvValue, SourceDotEnv
		}
		if value, ok := os.LookupEnv(key); ok && value != "" {
			return value, SourceEnv
		}
	}
	return defaultValue, SourceDefault
}

// loadFiles parses config.json and secrets.json. Malformed files are warned
// about and treated as empty; unknown keys are preserved.
func (s *Store) loadFiles() error {
	configValues, err := readFlatJSON(s.opts.ConfigPath)
	if err != nil {
		log.Printf("configfile: config file %s is malformed, ignoring it: %v", s.opts.ConfigPath, err)
		configValues = map[string]string{}
	}
	secretsValues, err := readFlatJSON(s.opts.SecretsPath)
	if err != nil {
		log.Printf("configfile: secrets file %s is malformed, secret keys from it are not applied: %v", s.opts.SecretsPath, err)
		secretsValues = map[string]string{}
	}
	s.configValues = configValues
	s.secretsValues = secretsValues
	return nil
}

// migrate moves secret keys from config.json into secrets.json (idempotent).
func (s *Store) migrate() error {
	s.mu.Lock()
	moved := false
	for _, key := range SecretKeys {
		value, ok := s.configValues[key]
		if !ok {
			continue
		}
		delete(s.configValues, key)
		moved = true
		if _, exists := s.secretsValues[key]; !exists {
			s.secretsValues[key] = value
		}
	}
	s.mu.Unlock()
	if !moved {
		return nil
	}
	if err := s.writeConfig(); err != nil {
		return err
	}
	return s.writeSecrets()
}

// loadDotEnv loads the .env chain: ENV_FILE -> ./.env.local -> ./.env -> exe .env.
// Earlier files take precedence and never overwrite already collected keys.
func (s *Store) loadDotEnv() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, path := range s.dotEnvCandidates() {
		values, err := readDotEnv(path)
		if err != nil || len(values) == 0 {
			continue
		}
		for key, value := range values {
			if _, exists := s.dotEnv[key]; !exists {
				s.dotEnv[key] = value
			}
		}
	}
}

func (s *Store) dotEnvCandidates() []string {
	candidates := make([]string, 0, 4)
	if s.opts.EnvPath != "" {
		candidates = append(candidates, s.opts.EnvPath)
	}
	candidates = append(candidates, ".env.local", defaultEnvFile)
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), defaultEnvFile))
	}
	return candidates
}

func readFlatJSON(path string) (map[string]string, error) {
	values := make(map[string]string)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return values, nil
		}
		return values, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return values, nil
	}
	raw := make(map[string]json.RawMessage)
	if err := json.Unmarshal(data, &raw); err != nil {
		return values, err
	}
	for key, value := range raw {
		if string(value) == "null" {
			continue
		}
		var str string
		if err := json.Unmarshal(value, &str); err == nil {
			values[key] = str
			continue
		}
		var num json.Number
		if err := json.Unmarshal(value, &num); err == nil {
			values[key] = num.String()
			continue
		}
		var b bool
		if err := json.Unmarshal(value, &b); err == nil {
			values[key] = fmt.Sprintf("%t", b)
			continue
		}
		log.Printf("configfile: ignoring non-scalar value for %s in %s", key, path)
	}
	return values, nil
}
