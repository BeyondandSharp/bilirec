package rest

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/utils"
	jwtmiddleware "github.com/gofiber/contrib/v3/jwt"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/extractors"
	jwtlib "github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

const (
	testOriginApp    = "https://app.bilirec.org"
	testOriginHinome = "https://hinome.bilirec.org"
)

func testAuthConfig(t *testing.T) *config.Config {
	adminHash, err := bcrypt.GenerateFromPassword([]byte("adminpass"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	viewerHash, err := bcrypt.GenerateFromPassword([]byte("viewerpass"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	return &config.Config{
		Username:           "admin",
		PasswordHash:       string(adminHash),
		ViewerUsername:     "viewer",
		ViewerPasswordHash: string(viewerHash),
		JwtSecret:          "test-jwt-secret",
		ProductionMode:     false,
	}
}

func mintSessionJWT(cfg *config.Config, user, role string) string {
	expire := time.Now().Add(time.Hour)
	claims := jwtlib.MapClaims{
		"name": user,
		"iat":  time.Now().Unix(),
		"exp":  expire.Unix(),
		"iss":  sessionJwtIssuer,
		"role": role,
	}
	token := jwtlib.NewWithClaims(jwtlib.SigningMethodHS256, claims)
	t, err := token.SignedString([]byte(cfg.JwtSecret))
	if err != nil {
		panic(err)
	}
	return t
}

// adminRoleFromJWT mirrors AdminOnly when RBAC is on; used here to assert JWT role after per-Origin cookie selection.
func adminRoleFromJWT(c fiber.Ctx) error {
	claims := utils.ToJwtClaims(c)
	if claims == nil {
		return fiber.NewError(fiber.StatusForbidden, "没有权限")
	}
	role, ok := utils.GetClaimString(claims, "role")
	if !ok || role != string(RoleAdmin) {
		return fiber.NewError(fiber.StatusForbidden, "没有权限")
	}
	return c.Next()
}

func newAuthTestApp(cfg *config.Config) *fiber.App {
	app := fiber.New()
	app.Post("/login", loginHandler(cfg))
	app.Post("/logout", logoutHandler(cfg))
	app.Use(jwtmiddleware.New(jwtmiddleware.Config{
		Extractor:  sessionJWTExtractor(),
		SigningKey: jwtmiddleware.SigningKey{Key: []byte(cfg.JwtSecret)},
		SuccessHandler: func(c fiber.Ctx) error {
			token := jwtmiddleware.FromContext(c)
			if token == nil {
				return c.Status(fiber.StatusUnauthorized).SendString("JWT 无效或已过期")
			}
			claims, ok := token.Claims.(jwtlib.MapClaims)
			iss, _ := claims["iss"].(string)
			if !ok || iss != sessionJwtIssuer {
				return c.Status(fiber.StatusUnauthorized).SendString("JWT 无效或已过期")
			}
			return c.Next()
		},
		ErrorHandler: func(c fiber.Ctx, err error) error {
			if errors.Is(err, extractors.ErrNotFound) {
				return c.Status(fiber.StatusUnauthorized).SendString("missing")
			}
			return c.Status(fiber.StatusUnauthorized).SendString(err.Error())
		},
	}))
	app.Get("/record/list", func(c fiber.Ctx) error {
		return c.JSON([]int{})
	})
	app.Delete("/files/batch", adminRoleFromJWT, func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})
	return app
}

func loginWithOrigin(t *testing.T, app *fiber.App, origin, user, pass string) *http.Response {
	body, err := json.Marshal(map[string]string{"user": user, "pass": pass})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func cookieNameFromSetCookie(setCookie string) string {
	part := strings.SplitN(setCookie, ";", 2)[0]
	name, _, ok := strings.Cut(part, "=")
	if !ok {
		return ""
	}
	return name
}

func TestLogin_sessionCookieNamesDifferByOrigin(t *testing.T) {
	t.Parallel()

	cfg := testAuthConfig(t)
	app := newAuthTestApp(cfg)

	respApp := loginWithOrigin(t, app, testOriginApp, "admin", "adminpass")
	if respApp.StatusCode != http.StatusOK {
		t.Fatalf("app login: %d", respApp.StatusCode)
	}
	respHinome := loginWithOrigin(t, app, testOriginHinome, "viewer", "viewerpass")
	if respHinome.StatusCode != http.StatusOK {
		t.Fatalf("hinome login: %d", respHinome.StatusCode)
	}

	appCookies := respApp.Header["Set-Cookie"]
	hinomeCookies := respHinome.Header["Set-Cookie"]
	if len(appCookies) == 0 || len(hinomeCookies) == 0 {
		t.Fatal("expected Set-Cookie")
	}
	appName := cookieNameFromSetCookie(appCookies[0])
	hinomeName := cookieNameFromSetCookie(hinomeCookies[0])
	if appName == hinomeName {
		t.Fatalf("cookie names should differ: %q vs %q", appName, hinomeName)
	}
	if appName == jwtTokenKey || hinomeName == jwtTokenKey {
		t.Fatalf("browser origins must not use legacy name %q", jwtTokenKey)
	}
	if appName != sessionCookieName(testOriginApp) || hinomeName != sessionCookieName(testOriginHinome) {
		t.Fatalf("unexpected names: %q %q", appName, hinomeName)
	}
}

func TestLogin_withoutOriginUsesLegacyCookieName(t *testing.T) {
	t.Parallel()

	cfg := testAuthConfig(t)
	app := newAuthTestApp(cfg)

	resp := loginWithOrigin(t, app, "", "admin", "adminpass")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login: %d", resp.StatusCode)
	}
	setCookies := resp.Header["Set-Cookie"]
	if len(setCookies) == 0 {
		t.Fatal("expected Set-Cookie")
	}
	if cookieNameFromSetCookie(setCookies[0]) != jwtTokenKey {
		t.Fatalf("expected %q, got %q", jwtTokenKey, setCookies[0])
	}
}

func TestSessionJWT_adminOriginIgnoresViewerAndLegacyCookies(t *testing.T) {
	t.Parallel()

	cfg := testAuthConfig(t)
	app := newAuthTestApp(cfg)
	adminToken := mintSessionJWT(cfg, "admin", string(RoleAdmin))
	viewerToken := mintSessionJWT(cfg, "viewer", string(RoleViewer))

	appCookie := sessionCookieName(testOriginApp)
	hinomeCookie := sessionCookieName(testOriginHinome)

	req := httptest.NewRequest(http.MethodDelete, "/files/batch", nil)
	req.Header.Set("Origin", testOriginApp)
	req.Header.Set("Cookie", strings.Join([]string{
		appCookie + "=" + adminToken,
		hinomeCookie + "=" + viewerToken,
		jwtTokenKey + "=" + viewerToken,
	}, "; "))

	resp, err := app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
}

func TestSessionJWT_viewerOriginCannotDelete(t *testing.T) {
	t.Parallel()

	cfg := testAuthConfig(t)
	app := newAuthTestApp(cfg)
	viewerToken := mintSessionJWT(cfg, "viewer", string(RoleViewer))
	hinomeCookie := sessionCookieName(testOriginHinome)

	req := httptest.NewRequest(http.MethodDelete, "/files/batch", nil)
	req.Header.Set("Origin", testOriginHinome)
	req.Header.Set("Cookie", hinomeCookie+"="+viewerToken)

	resp, err := app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 403, got %d: %s", resp.StatusCode, body)
	}
}

func TestSessionJWT_wrongOriginCookieReturnsUnauthorized(t *testing.T) {
	t.Parallel()

	cfg := testAuthConfig(t)
	app := newAuthTestApp(cfg)
	viewerToken := mintSessionJWT(cfg, "viewer", string(RoleViewer))
	hinomeCookie := sessionCookieName(testOriginHinome)

	req := httptest.NewRequest(http.MethodGet, "/record/list", nil)
	req.Header.Set("Origin", testOriginApp)
	req.Header.Set("Cookie", hinomeCookie+"="+viewerToken)

	resp, err := app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 401, got %d: %s", resp.StatusCode, body)
	}
}
