package service

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/captcha"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

var (
	ErrImageCaptchaRequired      = infraerrors.BadRequest("IMAGE_CAPTCHA_REQUIRED", "image captcha is required")
	ErrImageCaptchaInvalid       = infraerrors.BadRequest("IMAGE_CAPTCHA_INVALID", "image captcha is incorrect or has expired")
	ErrImageCaptchaDisabled      = infraerrors.NotFound("IMAGE_CAPTCHA_DISABLED", "image captcha is not enabled")
	ErrImageCaptchaNotConfigured = infraerrors.ServiceUnavailable("IMAGE_CAPTCHA_NOT_CONFIGURED", "image captcha service is not configured")
)

const (
	// ImageCaptchaTTL 是一张图形验证码的有效期。
	ImageCaptchaTTL = 5 * time.Minute

	imageCaptchaIDBytes    = 16
	imageCaptchaMaxCodeLen = 16
)

// ImageCaptchaStore 保存图形验证码答案摘要。TakeImageCaptcha 必须原子地取出并删除摘要：
// 同一张验证码无论答对答错都只能校验一次，从根本上杜绝对单张验证码的穷举。
type ImageCaptchaStore interface {
	SaveImageCaptcha(ctx context.Context, id, answer string, ttl time.Duration) error
	TakeImageCaptcha(ctx context.Context, id string) (answer string, found bool, err error)
}

// ImageCaptchaChallenge 是下发给前端的一张验证码。
type ImageCaptchaChallenge struct {
	ID        string
	PNG       []byte
	ExpiresIn time.Duration
}

// ImageCaptchaService 自研图形验证码：出图、下发与一次性校验。
type ImageCaptchaService struct {
	store    ImageCaptchaStore
	settings *SettingService
	generate func() (*captcha.Challenge, error)
}

func NewImageCaptchaService(store ImageCaptchaStore, settingService *SettingService) *ImageCaptchaService {
	return &ImageCaptchaService{
		store:    store,
		settings: settingService,
		generate: func() (*captcha.Challenge, error) {
			return captcha.Generate(captcha.Options{})
		},
	}
}

// Issue 生成并下发一张验证码。仅在后台启用图形验证码时可用，
// 避免未启用时匿名请求无谓地占用 Redis。
func (s *ImageCaptchaService) Issue(ctx context.Context) (*ImageCaptchaChallenge, error) {
	if s == nil || s.store == nil || s.generate == nil {
		return nil, ErrImageCaptchaNotConfigured
	}
	if s.settings == nil || !s.settings.IsImageCaptchaEnabled(ctx) {
		return nil, ErrImageCaptchaDisabled
	}
	challenge, err := s.generate()
	if err != nil {
		return nil, ErrServiceUnavailable.WithCause(err)
	}
	id, err := randomHexString(imageCaptchaIDBytes)
	if err != nil {
		return nil, ErrServiceUnavailable.WithCause(err)
	}
	if err := s.store.SaveImageCaptcha(ctx, id, imageCaptchaDigest(id, challenge.Answer), ImageCaptchaTTL); err != nil {
		return nil, ErrServiceUnavailable.WithCause(err)
	}
	return &ImageCaptchaChallenge{ID: id, PNG: challenge.PNG, ExpiresIn: ImageCaptchaTTL}, nil
}

// Verify 校验并消费验证码：先原子取出答案再比对，答错同样作废。
func (s *ImageCaptchaService) Verify(ctx context.Context, id, code string) error {
	if s == nil || s.store == nil {
		return ErrImageCaptchaNotConfigured
	}
	id = strings.TrimSpace(id)
	code = normalizeImageCaptchaCode(code)
	if id == "" || code == "" {
		return ErrImageCaptchaRequired
	}
	if !isImageCaptchaID(id) || len(code) > imageCaptchaMaxCodeLen {
		return ErrImageCaptchaInvalid
	}
	answer, found, err := s.store.TakeImageCaptcha(ctx, id)
	if err != nil {
		logger.LegacyPrintf("service.image_captcha", "[ImageCaptcha] Failed to take captcha: %v", err)
		return ErrServiceUnavailable
	}
	if !found || subtle.ConstantTimeCompare([]byte(answer), []byte(imageCaptchaDigest(id, code))) != 1 {
		return ErrImageCaptchaInvalid
	}
	return nil
}

func imageCaptchaDigest(id, code string) string {
	sum := sha256.Sum256([]byte(id + "\x00" + normalizeImageCaptchaCode(code)))
	return hex.EncodeToString(sum[:])
}

// normalizeImageCaptchaCode 去掉所有空白并转大写：验证码大小写不敏感，
// 用户输入时多打的空格也不应导致失败。
func normalizeImageCaptchaCode(code string) string {
	return strings.ToUpper(strings.Join(strings.Fields(code), ""))
}

func isImageCaptchaID(id string) bool {
	if len(id) != imageCaptchaIDBytes*2 {
		return false
	}
	for _, c := range id {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
