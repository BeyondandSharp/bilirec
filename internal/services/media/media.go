package media

import (
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/bilirec/bilirec/internal/services/path"
	"github.com/bilirec/bilirec/pkg/logger"
	"github.com/puzpuzpuz/xsync/v4"
)

var log = logger.Named("media")

var (
	ErrMissingToken   = errors.New("缺少预签名 token")
	ErrPathMismatch   = errors.New("预签名 token 与请求路径不符")
	ErrInvalidPurpose = errors.New("无效的 purpose")
)

// renewMargin is how close to expiry a pooled URL is considered stale.
// Playback renewal runs 5 minutes before expiry; handing back a URL inside
// that window would make the renewal a no-op, so re-sign instead.
const renewMargin = 5 * time.Minute

type Service struct {
	pathSvc *path.Service
	pool    *xsync.Map[string, string] // route + fullPath -> presigned JWT
}

func NewService(pathSvc *path.Service) *Service {
	return &Service{
		pathSvc: pathSvc,
		pool:    xsync.NewMap[string, string](),
	}
}

// PresignedURL returns a presigned media URL for purpose. Download and
// playback URLs are pooled per route and path: the same file keeps the same
// URL until it is within renewMargin of expiring, so repeated requests share
// one cache key on the CDN instead of minting a fresh token each time.
// Share URLs are always freshly signed and never touch the pool, so a
// short-lived share link can neither reuse nor overwrite a pooled
// playback/download URL. expiresIn is the remaining lifetime of the returned
// URL in seconds.
func (s *Service) PresignedURL(fullPath, purpose string, expireAfter time.Duration) (url string, expiresIn int64, err error) {
	route, err := routeFor(purpose)
	if err != nil {
		return "", 0, err
	}

	if purpose == path.PurposeShare {
		url, err = s.pathSvc.GeneratePresignedMediaURL(fullPath, route, expireAfter)
		if err != nil {
			return "", 0, err
		}
		return url, int64(expireAfter.Seconds()), nil
	}

	key := route + "\x00" + fullPath
	if token, ok := s.pool.Load(key); ok {
		_, expiresAt, err := s.pathSvc.InspectPresignedToken(token)
		if err != nil {
			s.pool.Delete(key)
			log.Debugf("%s 的预签名 token 无效或已过期，重新签发", fullPath)
		} else {
			remaining := time.Until(expiresAt)
			if remaining > renewMargin {
				url, err = s.pathSvc.PresignedMediaURL(fullPath, route, token)
				if err != nil {
					return "", 0, err
				}
				return url, int64(remaining.Seconds()), nil
			}
			s.pool.Delete(key)
			log.Debugf("%s 的预签名 URL 即将过期，重新签发", fullPath)
		}
	}
	url, token, err := s.pathSvc.MintPresignedMediaURL(fullPath, route, expireAfter)
	if err != nil {
		return "", 0, err
	}
	s.pool.Store(key, token)
	return url, int64(expireAfter.Seconds()), nil
}

// AuthorizeToken validates the presigned token of a media route request
// (/files/playback, /files/download): it must be present, well-formed,
// unexpired, and bound to reqPath. Returned errors are ErrMissingToken,
// path.ErrTokenExpired, path.ErrInvalidToken, or ErrPathMismatch.
func (s *Service) AuthorizeToken(token, reqPath string) error {
	if token == "" {
		return ErrMissingToken
	}
	tokenPath, err := s.pathSvc.ParsePresignedURLToken(token)
	if err != nil {
		return err
	}
	if normalizeRelPath(tokenPath) != normalizeRelPath(reqPath) {
		return ErrPathMismatch
	}
	return nil
}

// routeFor maps a presigned purpose to its media route. Share URLs ride the
// download route since they are downloaded as attachments.
func routeFor(purpose string) (string, error) {
	switch purpose {
	case path.PurposePlayback:
		return path.PresignedRoutePlayback, nil
	case path.PurposeDownload, path.PurposeShare:
		return path.PresignedRouteDownload, nil
	default:
		return "", ErrInvalidPurpose
	}
}

// normalizeRelPath makes token paths and request paths comparable across
// Windows/Unix separators and redundant separators.
func normalizeRelPath(p string) string {
	return filepath.ToSlash(filepath.Clean(strings.TrimPrefix(p, "/")))
}
