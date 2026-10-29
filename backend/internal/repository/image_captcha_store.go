package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const imageCaptchaKeyPrefix = "captcha:image:"

type imageCaptchaStore struct {
	rdb *redis.Client
}

// NewImageCaptchaStore 基于 Redis 保存图形验证码答案摘要。
func NewImageCaptchaStore(rdb *redis.Client) service.ImageCaptchaStore {
	return &imageCaptchaStore{rdb: rdb}
}

func (s *imageCaptchaStore) SaveImageCaptcha(ctx context.Context, id, answer string, ttl time.Duration) error {
	return s.rdb.Set(ctx, imageCaptchaKeyPrefix+id, answer, ttl).Err()
}

// TakeImageCaptcha 在 MULTI/EXEC 中先 GET 再 DEL，原子取出并作废答案
// （不依赖 Redis 6.2+ 的 GETDEL，兼容旧版本）。
func (s *imageCaptchaStore) TakeImageCaptcha(ctx context.Context, id string) (string, bool, error) {
	key := imageCaptchaKeyPrefix + id
	pipe := s.rdb.TxPipeline()
	get := pipe.Get(ctx, key)
	pipe.Del(ctx, key)
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return "", false, err
	}
	answer, err := get.Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return answer, true, nil
}
