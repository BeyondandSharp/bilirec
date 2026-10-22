// SPDX-License-Identifier: GPL-3.0-only

package rest

import (
	"net/url"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/modules/configfile"
	"github.com/gofiber/fiber/v3/middleware/cors"
)

const defaultFrontendOrigin = "https://app.bilirec.org"

type corsOrigins struct {
	mu    sync.RWMutex
	allow map[string]struct{}
}

func newCORSOrigins(cfg *config.Config) *corsOrigins {
	o := &corsOrigins{}
	o.setConfig(cfg)
	return o
}

func (o *corsOrigins) setConfig(cfg *config.Config) {
	values := []string(nil)
	production := false
	if cfg != nil {
		values = cfg.FrontendURLs
		production = cfg.ProductionMode
	}
	o.set(values, production)
}

func (o *corsOrigins) set(values []string, production bool) {
	allow := make(map[string]struct{}, len(values)+2)
	for _, value := range values {
		if origin, ok := normalizeOrigin(value); ok {
			allow[origin] = struct{}{}
		}
	}
	if !production {
		allow["http://localhost:3000"] = struct{}{}
		allow["http://127.0.0.1:3000"] = struct{}{}
	}
	o.mu.Lock()
	o.allow = allow
	o.mu.Unlock()
}

func normalizeOrigin(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", false
	}
	return parsed.Scheme + "://" + parsed.Host, true
}

func (o *corsOrigins) allows(origin string) bool {
	if o == nil {
		return false
	}
	normalized, ok := normalizeOrigin(origin)
	if !ok {
		return false
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	_, allowed := o.allow[normalized]
	return allowed
}

var activeCORSOrigins atomic.Pointer[corsOrigins]

func dynamicCORSConfig() cors.Config {
	return cors.Config{
		AllowOriginsFunc: func(origin string) bool {
			return activeCORSOrigins.Load().allows(origin)
		},
		AllowCredentials:    true,
		AllowPrivateNetwork: true,
	}
}

// BindConfigStore makes FRONTEND_URLS / FRONTEND_URL changes effective without
// a restart. It is invoked from bootstrap because the Fiber app provider does
// not otherwise expose its origin set.
func BindConfigStore(cfg *config.Config, store *configfile.Store) {
	if store == nil {
		return
	}
	update := func() {
		primary, primarySource := store.EffectiveValue("FRONTEND_URLS", "")
		if primarySource == configfile.SourceDefault {
			primary = ""
		}
		alias, aliasSource := store.EffectiveValue("FRONTEND_URL", "")
		if aliasSource == configfile.SourceDefault {
			alias = ""
		}
		raw := primary
		if raw == "" {
			raw = alias
		}
		if raw == "" {
			raw = defaultFrontendOrigin
		}
		values := splitOrigins(raw)
		production := false
		if cfg != nil {
			production = cfg.ProductionMode
		}
		activeCORSOrigins.Load().set(values, production)
	}
	store.OnConfigChange(func(change configfile.ConfigChange) {
		if change.Key != "FRONTEND_URLS" && change.Key != "FRONTEND_URL" {
			return
		}
		update()
	})
}

func splitOrigins(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
