//go:build unit

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newImageCaptchaStoreForTest(t *testing.T) (*imageCaptchaStore, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return &imageCaptchaStore{rdb: rdb}, mr
}

func TestImageCaptchaStoreTakeIsSingleUse(t *testing.T) {
	store, mr := newImageCaptchaStoreForTest(t)
	ctx := context.Background()

	require.NoError(t, store.SaveImageCaptcha(ctx, "abc", "K7MP", time.Minute))
	require.True(t, mr.Exists(imageCaptchaKeyPrefix+"abc"))
	require.Equal(t, time.Minute, mr.TTL(imageCaptchaKeyPrefix+"abc"))

	answer, found, err := store.TakeImageCaptcha(ctx, "abc")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "K7MP", answer)
	require.False(t, mr.Exists(imageCaptchaKeyPrefix+"abc"), "taking a captcha must delete it")

	_, found, err = store.TakeImageCaptcha(ctx, "abc")
	require.NoError(t, err)
	require.False(t, found)
}

func TestImageCaptchaStoreMissingAndExpired(t *testing.T) {
	store, mr := newImageCaptchaStoreForTest(t)
	ctx := context.Background()

	_, found, err := store.TakeImageCaptcha(ctx, "missing")
	require.NoError(t, err)
	require.False(t, found)

	require.NoError(t, store.SaveImageCaptcha(ctx, "short", "AB34", time.Second))
	mr.FastForward(2 * time.Second)
	_, found, err = store.TakeImageCaptcha(ctx, "short")
	require.NoError(t, err)
	require.False(t, found, "an expired captcha must not verify")
}
