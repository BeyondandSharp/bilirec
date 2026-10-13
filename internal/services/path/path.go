package path

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bilirec/bilirec/pkg/logger"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/pkg/signeddownload"
	"github.com/bilirec/bilirec/utils"
)

var log = logger.Named("path")

var (
	ErrFileNotFound    = fmt.Errorf("文件不存在")
	ErrInvalidFilePath = fmt.Errorf("无效文件路径")
	ErrAccessDenied    = fmt.Errorf("访问被拒绝")
	ErrTokenExpired    = fmt.Errorf("token 已过期")
	ErrInvalidToken    = fmt.Errorf("无效 token")
)

// Presigned purposes for the media routes. The token itself is route agnostic
// (file path + exp); the purpose only picks which route the URL points at.
const (
	PurposeDownload = "download"
	PurposePlayback = "playback"
	PurposeShare    = "share"
)

const (
	PresignedRouteDownload = "/files/download/"
	PresignedRoutePlayback = "/files/playback/"
)

type Service struct {
	cfg       *config.Config
	presigner *signeddownload.Client
}

func NewService(cfg *config.Config) *Service {
	return &Service{
		cfg:       cfg,
		presigner: signeddownload.NewClient([]byte(cfg.JwtSecret)),
	}
}

func (s *Service) GeneratePresignedMediaURL(fullPath, route string, expireAfter time.Duration) (string, error) {
	url, _, err := s.MintPresignedMediaURL(fullPath, route, expireAfter)
	return url, err
}

// MintPresignedMediaURL signs a new JWT and returns the full media URL plus the
// token (for in-process pools that should not parse the URL back out).
func (s *Service) MintPresignedMediaURL(fullPath, route string, expireAfter time.Duration) (url, token string, err error) {
	relPath, err := s.GetRelativePath(fullPath)
	if err != nil {
		return "", "", err
	}
	token, err = s.presigner.GenerateDownloadToken(relPath, time.Now().Add(expireAfter).Unix())
	if err != nil {
		return "", "", err
	}
	url, err = s.PresignedMediaURL(fullPath, route, token)
	if err != nil {
		return "", "", err
	}
	return url, token, nil
}

// PresignedMediaURL builds a media URL for an existing presigned JWT.
func (s *Service) PresignedMediaURL(fullPath, route, token string) (string, error) {
	relPath, err := s.GetRelativePath(fullPath)
	if err != nil {
		return "", err
	}

	routeAndPath := route
	segments := strings.Split(filepath.ToSlash(filepath.Clean(relPath)), "/")
	for i, seg := range segments {
		segments[i] = url.PathEscape(seg)
	}
	routeAndPath = route + strings.Join(segments, "/")

	fallbackURL := fmt.Sprintf("%s?presigned=%s", routeAndPath, token)
	baseURL := strings.TrimSpace(s.cfg.PublicBaseUrl)
	if baseURL == "" {
		log.Warn("PUBLIC_BASE_URL 为空，返回相对预签名 URL")
		return fallbackURL, nil
	}

	if !utils.IsValidAbsoluteHTTPURL(baseURL) {
		log.Warnf("PUBLIC_BASE_URL 无效：%q，返回相对预签名 URL", s.cfg.PublicBaseUrl)
		return fallbackURL, nil
	}

	return fmt.Sprintf("%s%s?presigned=%s", strings.TrimRight(baseURL, "/"), routeAndPath, token), nil
}

// InspectPresignedToken validates a JWT and returns the bound relative path and expiry.
func (s *Service) InspectPresignedToken(token string) (relPath string, expiresAt time.Time, err error) {
	claim, err := s.verifyPresignedToken(token)
	if err != nil {
		return "", time.Time{}, err
	}
	return claim.FilePath, time.Unix(claim.Exp, 0), nil
}

func (s *Service) ParsePresignedURL(fullURL string) (string, error) {
	token, err := s.presignedQueryToken(fullURL)
	if err != nil {
		return "", err
	}
	relPath, _, err := s.InspectPresignedToken(token)
	return relPath, err
}

func (s *Service) ParsePresignedURLToken(token string) (string, error) {
	relPath, _, err := s.InspectPresignedToken(token)
	return relPath, err
}

func (s *Service) ValidatePath(path string) (string, error) {
	baseAbs, err := filepath.Abs(s.cfg.OutputDir)
	if err != nil {
		log.Errorf("%s 的基础路径无效：%v", s.cfg.OutputDir, err)
		return "", ErrInvalidFilePath
	}

	fullPath := filepath.Join(baseAbs, path)
	fullPath = filepath.Clean(fullPath)

	fullPathAbs, err := filepath.Abs(fullPath)
	if err != nil {
		log.Errorf("路径 %s 无效：%v", fullPath, err)
		return "", ErrInvalidFilePath
	}

	if !strings.HasPrefix(fullPathAbs, baseAbs+string(os.PathSeparator)) &&
		fullPathAbs != baseAbs {
		log.Errorf("检测到路径穿越：%s", fullPath)
		return "", ErrAccessDenied
	}

	return fullPathAbs, nil
}

func (s *Service) GetRelativePath(fullPath string) (string, error) {
	baseAbs, err := filepath.Abs(s.cfg.OutputDir)
	if err != nil {
		return "", err
	}

	// Convert fullPath to absolute path first to handle relative paths
	fullPathAbs, err := filepath.Abs(fullPath)
	if err != nil {
		return "", err
	}

	rel, err := filepath.Rel(baseAbs, fullPathAbs)
	if err != nil {
		return "", err
	}
	if rel == "." {
		rel = ""
	}
	return rel, nil
}

func (s *Service) verifyPresignedToken(token string) (*signeddownload.DownloadTokenClaims, error) {
	claim, err := s.presigner.ParseDownloadToken(token)
	if err != nil {
		return nil, ErrInvalidToken
	}
	if time.Now().Unix() > claim.Exp {
		return nil, ErrTokenExpired
	}
	return claim, nil
}

func (s *Service) presignedQueryToken(fullURL string) (string, error) {
	u, err := url.Parse(fullURL)
	if err != nil {
		return "", err
	}
	token := u.Query().Get("presigned")
	if token == "" {
		return "", ErrInvalidToken
	}
	return token, nil
}
