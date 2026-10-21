// SPDX-License-Identifier: GPL-3.0-only

package configfile

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sort"

	"github.com/joho/godotenv"
)

const (
	configFileMode  os.FileMode = 0o644
	secretsFileMode os.FileMode = 0o600
)

// ErrUnknownManagedKey is returned when the API tries to persist a bootstrap
// or unknown key.
var ErrUnknownManagedKey = errors.New("未知的配置键")

// readDotEnv parses a .env file into a flat map (empty when absent).
func readDotEnv(path string) (map[string]string, error) {
	if path == "" {
		return nil, nil
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return godotenv.Read(path)
}

// writeConfig persists config.json atomically with mode 0644.
func (s *Store) writeConfig() error {
	s.mu.RLock()
	values := make(map[string]string, len(s.configValues))
	for key, value := range s.configValues {
		values[key] = value
	}
	path := s.opts.ConfigPath
	s.mu.RUnlock()
	return writeFlatJSON(path, values, configFileMode)
}

// writeSecrets persists secrets.json atomically with mode 0600, tightening
// permissions (with a warning) when the existing file is too broad.
func (s *Store) writeSecrets() error {
	s.mu.RLock()
	values := make(map[string]string, len(s.secretsValues))
	for key, value := range s.secretsValues {
		values[key] = value
	}
	path := s.opts.SecretsPath
	s.mu.RUnlock()
	return writeFlatJSON(path, values, secretsFileMode)
}

// writeFlatJSON writes a flat JSON object atomically (temp file + rename).
func writeFlatJSON(path string, values map[string]string, mode os.FileMode) error {
	if path == "" {
		return errors.New("empty path")
	}
	if info, err := os.Stat(path); err == nil {
		if info.Mode().Perm() != mode {
			if mode == secretsFileMode {
				log.Printf("configfile: secrets file %s has mode %#o (expected 0600), tightening", path, info.Mode().Perm())
			}
			_ = os.Chmod(path, mode)
		}
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".configfile-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()
	payload, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	if _, err := tmp.Write(payload); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// SetConfig persists non-secret managed values from config.json. An empty value
// in clear deletes those keys. Unknown keys already in the file are preserved.
// Values are marked restart-required unless they are hot-applied feature keys.
func (s *Store) SetConfig(values map[string]string, clear []string) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	for key, value := range values {
		if key == "" || IsBootstrapKey(key) {
			s.mu.Unlock()
			return ErrUnknownManagedKey
		}
		if IsSecretKey(key) {
			s.mu.Unlock()
			return errors.New("密钥只能通过 /config/secrets 保存")
		}
		s.configValues[key] = value
		if !IsFeatureKey(key) {
			s.restartRequired[key] = struct{}{}
		}
	}
	for _, key := range clear {
		delete(s.configValues, key)
		if !IsFeatureKey(key) {
			s.restartRequired[key] = struct{}{}
		}
	}
	s.mu.Unlock()
	return s.writeConfig()
}

// SetSecrets persists secret values from secrets.json.
func (s *Store) SetSecrets(values map[string]string, clear []string) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	for key, value := range values {
		if !IsSecretKey(key) {
			s.mu.Unlock()
			return ErrUnknownManagedKey
		}
		s.secretsValues[key] = value
		s.restartRequired[key] = struct{}{}
	}
	for _, key := range clear {
		if !IsSecretKey(key) {
			s.mu.Unlock()
			return ErrUnknownManagedKey
		}
		delete(s.secretsValues, key)
		s.restartRequired[key] = struct{}{}
	}
	s.mu.Unlock()
	return s.writeSecrets()
}

// ImportMissingConfig writes config.json values only for keys that are not
// already stored in the file (settings.db migration, idempotent).
func (s *Store) ImportMissingConfig(values map[string]string) error {
	if s == nil || len(values) == 0 {
		return nil
	}
	s.mu.Lock()
	changed := false
	for key, value := range values {
		if IsBootstrapKey(key) || IsSecretKey(key) {
			continue
		}
		if _, exists := s.configValues[key]; exists {
			continue
		}
		s.configValues[key] = value
		changed = true
	}
	s.mu.Unlock()
	if !changed {
		return nil
	}
	return s.writeConfig()
}

// ResetConfig removes the given managed keys from config.json (unknown keys are
// preserved) and marks non-feature keys restart-required.
func (s *Store) ResetConfig(keys []string) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	for _, key := range keys {
		if IsBootstrapKey(key) {
			s.mu.Unlock()
			return ErrUnknownManagedKey
		}
		delete(s.configValues, key)
		if !IsFeatureKey(key) {
			s.restartRequired[key] = struct{}{}
		}
	}
	s.mu.Unlock()
	return s.writeConfig()
}

// ResetSecrets removes all secret values (or the given keys when provided).
func (s *Store) ResetSecrets(keys []string) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if len(keys) == 0 {
		for key := range s.secretsValues {
			delete(s.secretsValues, key)
			s.restartRequired[key] = struct{}{}
		}
	} else {
		for _, key := range keys {
			if !IsSecretKey(key) {
				s.mu.Unlock()
				return ErrUnknownManagedKey
			}
			delete(s.secretsValues, key)
			s.restartRequired[key] = struct{}{}
		}
	}
	s.mu.Unlock()
	return s.writeSecrets()
}

// SecretKeysPresent returns the secret keys currently stored in secrets.json.
func (s *Store) SecretKeysPresent() []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]string, 0, len(s.secretsValues))
	for key := range s.secretsValues {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// ConfigKeys returns all keys stored in config.json.
func (s *Store) ConfigKeys() []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]string, 0, len(s.configValues))
	for key := range s.configValues {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// EffectiveValue resolves the effective (precedence-applied) value for a key.
// The loader-applied process environment is consulted as well, so values from
// config.json/secrets.json/.env are reported with their real source.
func (s *Store) EffectiveValue(key, defaultValue string) (string, Source) {
	if s == nil {
		return defaultValue, SourceDefault
	}
	if value, source := s.effectiveValue(key, defaultValue); source != SourceDefault {
		return value, source
	}
	// The loader writes file/dotenv values into the process environment; fall
	// back to it for keys that were applied before /config started.
	s.mu.RLock()
	locked := false
	if _, ok := s.systemEnv[key]; ok {
		locked = true
	}
	s.mu.RUnlock()
	if value, ok := os.LookupEnv(key); ok && value != "" {
		if locked {
			return value, SourceEnv
		}
	}
	return defaultValue, SourceDefault
}
