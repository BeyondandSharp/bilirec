package rest

import (
	"time"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"
)

type loginRequest struct {
	User string `json:"user" form:"user"`
	Pass string `json:"pass" form:"pass"`
}

// Login
// @Summary Login and get JWT token
// @Tags auth
// @Accept json
// @Produce json
// @Param credentials body loginRequest true "Login credentials"
// @Success 200 {string} string "Login successful; JWT token is set in cookie"
// @Failure 400 {string} string "Bad request"
// @Failure 401 {string} string "Unauthorized"
// @Router /login [post]
func loginHandler(cfg *config.Config) fiber.Handler {
	return func(c fiber.Ctx) error {

		var req loginRequest
		if err := c.Bind().All(&req); err != nil {
			return fiber.ErrBadRequest
		}

		user := req.User
		pass := req.Pass

		role := RetrieveRole(cfg, user, pass)
		if role == "" {
			return fiber.ErrUnauthorized
		}

		expire := time.Now().Add(time.Hour * 72)

		claims := jwt.MapClaims{
			"name": user,
			"iat":  time.Now().Unix(),
			"exp":  expire.Unix(),
			"iss":  sessionJwtIssuer,
			"role": role,
		}

		token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		t, err := token.SignedString([]byte(cfg.JwtSecret))
		if err != nil {
			return fiber.ErrInternalServerError
		}

		setSessionCookie(c, cfg, t, expire)

		return c.JSON(fiber.Map{
			"user": user,
			"role": role,
		})
	}
}

// Logout
// @Summary Logout and clear JWT cookie
// @Tags auth
// @Accept json
// @Produce json
// @Success 200 {string} string "Logout successful"
// @Router /logout [post]
func logoutHandler(cfg *config.Config) fiber.Handler {
	return func(c fiber.Ctx) error {
		clearSessionCookie(c, cfg)
		return c.SendStatus(fiber.StatusOK)
	}
}
