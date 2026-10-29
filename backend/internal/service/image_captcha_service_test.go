//go:build unit

package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/captcha"
	"github.com/stretchr/testify/require"
)

type memoryImageCaptchaStore struct {
	mu        sync.Mutex
	answers   map[string]string
	ttls      map[string]time.Duration
	saveErr   error
	takeErr   error
	takeCalls int
}

func newMemoryImageCaptchaStore() *memoryImageCaptchaStore {
	return &memoryImageCaptchaStore{answers: map[string]string{}, ttls: map[string]time.Duration{}}
}

func (s *memoryImageCaptchaStore) SaveImageCaptcha(_ context.Context, id, answer string, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.saveErr != nil {
		return s.saveErr
	}
	s.answers[id] = answer
	s.ttls[id] = ttl
	return nil
}

func (s *memoryImageCaptchaStore) TakeImageCaptcha(_ context.Context, id string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.takeCalls++
	if s.takeErr != nil {
		return "", false, s.takeErr
	}
	answer, ok := s.answers[id]
	delete(s.answers, id)
	return answer, ok, nil
}

func newImageCaptchaServiceForTest(store ImageCaptchaStore, enabled bool, answer string) *ImageCaptchaService {
	settingService := NewSettingService(&settingRepoStub{values: map[string]string{
		SettingKeyImageCaptchaEnabled: map[bool]string{true: "true", false: "false"}[enabled],
	}}, &config.Config{})
	svc := NewImageCaptchaService(store, settingService)
	svc.generate = func() (*captcha.Challenge, error) {
		return &captcha.Challenge{Answer: answer, PNG: []byte("png-bytes")}, nil
	}
	return svc
}

func TestImageCaptchaIssueRequiresEnabledSetting(t *testing.T) {
	store := newMemoryImageCaptchaStore()
	svc := newImageCaptchaServiceForTest(store, false, "AB34")

	_, err := svc.Issue(context.Background())

	require.ErrorIs(t, err, ErrImageCaptchaDisabled)
	require.Empty(t, store.answers, "a disabled captcha must not occupy the store")
}

func TestImageCaptchaIssueStoresOnlyDigest(t *testing.T) {
	store := newMemoryImageCaptchaStore()
	svc := newImageCaptchaServiceForTest(store, true, "ab34")

	challenge, err := svc.Issue(context.Background())

	require.NoError(t, err)
	require.True(t, isImageCaptchaID(challenge.ID))
	require.Equal(t, []byte("png-bytes"), challenge.PNG)
	require.Equal(t, ImageCaptchaTTL, challenge.ExpiresIn)
	require.Equal(t, imageCaptchaDigest(challenge.ID, "AB34"), store.answers[challenge.ID])
	require.NotContains(t, store.answers[challenge.ID], "AB34")
	require.Equal(t, ImageCaptchaTTL, store.ttls[challenge.ID])
}

func TestImageCaptchaVerifyIsCaseInsensitiveAndSingleUse(t *testing.T) {
	store := newMemoryImageCaptchaStore()
	svc := newImageCaptchaServiceForTest(store, true, "AB34")
	challenge, err := svc.Issue(context.Background())
	require.NoError(t, err)

	require.NoError(t, svc.Verify(context.Background(), challenge.ID, " a b 3 4 "))
	require.ErrorIs(t, svc.Verify(context.Background(), challenge.ID, "AB34"), ErrImageCaptchaInvalid,
		"a solved captcha cannot be replayed")
}

func TestImageCaptchaWrongAnswerBurnsTheChallenge(t *testing.T) {
	store := newMemoryImageCaptchaStore()
	svc := newImageCaptchaServiceForTest(store, true, "AB34")
	challenge, err := svc.Issue(context.Background())
	require.NoError(t, err)

	require.ErrorIs(t, svc.Verify(context.Background(), challenge.ID, "XXXX"), ErrImageCaptchaInvalid)
	require.ErrorIs(t, svc.Verify(context.Background(), challenge.ID, "AB34"), ErrImageCaptchaInvalid,
		"one wrong guess must invalidate the captcha so it cannot be brute forced")
}

func TestImageCaptchaVerifyRejectsMissingOrMalformedInput(t *testing.T) {
	store := newMemoryImageCaptchaStore()
	svc := newImageCaptchaServiceForTest(store, true, "AB34")

	require.ErrorIs(t, svc.Verify(context.Background(), "", "AB34"), ErrImageCaptchaRequired)
	require.ErrorIs(t, svc.Verify(context.Background(), "0123456789abcdef0123456789abcdef", "   "), ErrImageCaptchaRequired)
	require.ErrorIs(t, svc.Verify(context.Background(), "not-a-captcha-id", "AB34"), ErrImageCaptchaInvalid)
	require.ErrorIs(t, svc.Verify(context.Background(), "0123456789abcdef0123456789abcdef", "ABCDEFGHIJKLMNOPQRS"), ErrImageCaptchaInvalid)
	require.Zero(t, store.takeCalls, "malformed input must be rejected before touching the store")
}

func TestImageCaptchaStoreFailures(t *testing.T) {
	store := newMemoryImageCaptchaStore()
	store.saveErr = errors.New("redis down")
	svc := newImageCaptchaServiceForTest(store, true, "AB34")
	_, err := svc.Issue(context.Background())
	require.ErrorIs(t, err, ErrServiceUnavailable)

	store.saveErr = nil
	store.takeErr = errors.New("redis down")
	err = svc.Verify(context.Background(), "0123456789abcdef0123456789abcdef", "AB34")
	require.ErrorIs(t, err, ErrServiceUnavailable)
}

func newAuthServiceForImageCaptchaTest(settings map[string]string, store ImageCaptchaStore, answer string) *AuthService {
	cfg := &config.Config{Server: config.ServerConfig{Mode: "release"}}
	settingService := NewSettingService(&settingRepoStub{values: settings}, cfg)
	svc := NewAuthService(nil, &userRepoStub{}, nil, nil, cfg, settingService, nil, nil, nil, nil, nil, nil, nil)
	imageService := NewImageCaptchaService(store, settingService)
	imageService.generate = func() (*captcha.Challenge, error) {
		return &captcha.Challenge{Answer: answer, PNG: []byte("png")}, nil
	}
	svc.SetImageCaptchaService(imageService)
	return svc
}

func TestVerifyCaptchaUsesImageCaptchaWhenEnabled(t *testing.T) {
	store := newMemoryImageCaptchaStore()
	svc := newAuthServiceForImageCaptchaTest(map[string]string{SettingKeyImageCaptchaEnabled: "true"}, store, "K7MP")

	require.ErrorIs(t, svc.VerifyCaptcha(context.Background(), CaptchaProof{}, "203.0.113.10"), ErrImageCaptchaRequired)

	challenge, err := svc.IssueImageCaptcha(context.Background())
	require.NoError(t, err)
	err = svc.VerifyCaptcha(context.Background(), CaptchaProof{ImageCaptchaID: challenge.ID, ImageCaptchaCode: "k7mp"}, "203.0.113.10")
	require.NoError(t, err)
}

func TestVerifyCaptchaRejectsImageCaptchaCombinedWithAnotherProvider(t *testing.T) {
	store := newMemoryImageCaptchaStore()
	svc := newAuthServiceForImageCaptchaTest(map[string]string{
		SettingKeyImageCaptchaEnabled: "true",
		SettingKeyTurnstileEnabled:    "true",
	}, store, "K7MP")

	err := svc.VerifyCaptcha(context.Background(), CaptchaProof{ImageCaptchaID: "x", ImageCaptchaCode: "y"}, "203.0.113.10")

	require.ErrorIs(t, err, ErrCaptchaProviderConflict)
	require.Zero(t, store.takeCalls)
}

func TestVerifyCaptchaForRegisterSkipsImageCaptchaAfterEmailCode(t *testing.T) {
	// 邮箱验证注册时，图形验证码已在发送邮箱验证码时校验并消费。
	store := newMemoryImageCaptchaStore()
	svc := newAuthServiceForImageCaptchaTest(map[string]string{
		SettingKeyImageCaptchaEnabled: "true",
		SettingKeyEmailVerifyEnabled:  "true",
	}, store, "K7MP")

	require.NoError(t, svc.VerifyCaptchaForRegister(context.Background(), CaptchaProof{}, "203.0.113.10", "123456"))
	require.ErrorIs(t, svc.VerifyCaptchaForRegister(context.Background(), CaptchaProof{}, "203.0.113.10", ""), ErrImageCaptchaRequired)
}

func TestIssueImageCaptchaWithoutServiceIsNotConfigured(t *testing.T) {
	svc := NewAuthService(nil, &userRepoStub{}, nil, nil, &config.Config{}, nil, nil, nil, nil, nil, nil, nil, nil)
	_, err := svc.IssueImageCaptcha(context.Background())
	require.ErrorIs(t, err, ErrImageCaptchaNotConfigured)
}
