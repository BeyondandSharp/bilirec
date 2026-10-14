package rest

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"
	"time"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/extractors"
)

// sessionCookieName returns the JWT cookie name for a browser Origin, or jwtTokenKey when origin is empty/invalid.
func sessionCookieName(origin string) string {
	normalized := normalizeBrowserOrigin(origin)
	if normalized == "" {
		return jwtTokenKey
	}
	sum := sha256.Sum256([]byte(normalized))
	return "jwt_" + hex.EncodeToString(sum[:8])
}

func sessionCookieNameForRequest(c fiber.Ctx) string {
	return sessionCookieName(c.Get("Origin"))
}

func normalizeBrowserOrigin(origin string) string {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return ""
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return ""
	}
	return scheme + "://" + parsed.Host
}

func sessionCookieAttributes(cfg *config.Config, expires time.Time, value string, name string) *fiber.Cookie {
	return &fiber.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		Expires:  expires,
		HTTPOnly: cfg.ProductionMode,
		Secure:   cfg.ProductionMode,
		SameSite: "None",
	}
}

func setSessionCookie(c fiber.Ctx, cfg *config.Config, token string, expires time.Time) {
	c.Cookie(sessionCookieAttributes(cfg, expires, token, sessionCookieNameForRequest(c)))
}

func clearSessionCookie(c fiber.Ctx, cfg *config.Config) {
	c.Cookie(sessionCookieAttributes(cfg, time.Now().Add(-time.Hour), "", sessionCookieNameForRequest(c)))
}

func sessionJWTFromCookie(c fiber.Ctx) (string, error) {
	name := sessionCookieNameForRequest(c)
	token := c.Cookies(name)
	if token == "" {
		return "", extractors.ErrNotFound
	}
	return token, nil
}

func sessionJWTExtractor() extractors.Extractor {
	return extractors.Extractor{
		Extract: sessionJWTFromCookie,
		Source:  extractors.SourceCustom,
	}
}
