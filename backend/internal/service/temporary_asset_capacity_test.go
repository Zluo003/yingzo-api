package service

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// capacityTestDB 返回一个永不真正连接的 *sql.DB：只用于验证"未配置容量上限时
// 完全不动数据库"这类纯策略分支，一旦真的查库就会报错。
func capacityTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", "postgres://unused:unused@127.0.0.1:1/unused?sslmode=disable")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// 默认配置不设总容量上限：素材库不应因为一次升级就开始驱逐用户文件。
func TestEnforceTemporaryAssetCapacityIsUnlimitedByDefault(t *testing.T) {
	service := &FileStorageService{db: capacityTestDB(t)}

	evicted, err := service.EnforceTemporaryAssetCapacity(context.Background(), TemporaryAssetPurposeReference, 1<<30)
	require.NoError(t, err)
	require.Zero(t, evicted)
}

func TestEnforceTemporaryAssetCapacityWithoutDatabaseIsNoOp(t *testing.T) {
	service := &FileStorageService{}

	evicted, err := service.EnforceTemporaryAssetCapacity(context.Background(), TemporaryAssetPurposeReference, 1<<30)
	require.NoError(t, err)
	require.Zero(t, evicted)
}

func TestDefaultFileStorageConfigLeavesTotalCapacityUnlimited(t *testing.T) {
	config, err := normalizeFileStorageConfig(defaultFileStorageConfig())
	require.NoError(t, err)
	require.Zero(t, config.MaxTotalBytes)
}

func TestNormalizeFileStorageConfigValidatesTotalCapacity(t *testing.T) {
	base := defaultFileStorageConfig()

	negative := base
	negative.MaxTotalBytes = -1
	_, err := normalizeFileStorageConfig(negative)
	require.ErrorContains(t, err, "max_total_bytes")

	tooLarge := base
	tooLarge.MaxTotalBytes = 1<<50 + 1
	_, err = normalizeFileStorageConfig(tooLarge)
	require.ErrorContains(t, err, "max_total_bytes")

	bounded := base
	bounded.MaxTotalBytes = 1 << 30
	normalized, err := normalizeFileStorageConfig(bounded)
	require.NoError(t, err)
	require.Equal(t, int64(1<<30), normalized.MaxTotalBytes)
}

// 升级场景：老配置里没有产物相关字段，读出来必须回落到默认值而不是报错。
func TestNormalizeFileStorageConfigFillsResultDefaultsForLegacyConfig(t *testing.T) {
	legacy := FileStorageConfig{
		SchemaVersion:  1,
		Backend:        "local",
		PublicBaseURL:  "https://api.example.com",
		RetentionHours: 24,
		DailyMaxCount:  100,
		DailyMaxBytes:  1 << 30,
	}

	normalized, err := normalizeFileStorageConfig(legacy)
	require.NoError(t, err)
	require.Equal(t, defaultFileRetentionHours, normalized.ResultRetentionHours)
	require.Zero(t, normalized.ResultMaxTotalBytes, "未配置时产物容量仍是不限制")
	require.Equal(t, defaultFileCapacityReservePercent, normalized.EffectiveCapacityReservePercent(),
		"老配置没有冗余水位字段，应补齐默认值而不是判为非法")
}

func TestNormalizeFileStorageConfigValidatesResultSettings(t *testing.T) {
	base := defaultFileStorageConfig()

	tooLong := base
	tooLong.ResultRetentionHours = maxFileResultRetentionHours + 1
	_, err := normalizeFileStorageConfig(tooLong)
	require.ErrorContains(t, err, "result_retention_hours")

	negative := base
	negative.ResultMaxTotalBytes = -1
	_, err = normalizeFileStorageConfig(negative)
	require.ErrorContains(t, err, "result_max_total_bytes")

	tooMuchReserve := base
	tooMuchReserve.CapacityReservePercent = fileStorageReservePercentPtr(maxFileCapacityReservePercent + 1)
	_, err = normalizeFileStorageConfig(tooMuchReserve)
	require.ErrorContains(t, err, "capacity_reserve_percent")

	negativeReserve := base
	negativeReserve.CapacityReservePercent = fileStorageReservePercentPtr(-1)
	_, err = normalizeFileStorageConfig(negativeReserve)
	require.ErrorContains(t, err, "capacity_reserve_percent")

	explicit := base
	explicit.CapacityReservePercent = fileStorageReservePercentPtr(25)
	normalized, err := normalizeFileStorageConfig(explicit)
	require.NoError(t, err)
	require.Equal(t, 25, normalized.EffectiveCapacityReservePercent())

	// 显式 0 表示不留冗余：不能被默认值悄悄改成 10%。
	noReserve := base
	noReserve.CapacityReservePercent = fileStorageReservePercentPtr(0)
	normalized, err = normalizeFileStorageConfig(noReserve)
	require.NoError(t, err)
	require.Zero(t, normalized.EffectiveCapacityReservePercent())
}

// 水位线 = 上限 × (1 - 冗余比例)：配 100G、10% 冗余时，超过 90G 就该开始清理。
func TestCapacityEvictionThreshold(t *testing.T) {
	for _, tc := range []struct {
		name           string
		maxBytes       int64
		reservePercent int64
		want           int64
	}{
		{name: "100G with 10% reserve", maxBytes: 100 << 30, reservePercent: 10, want: 90 << 30},
		{name: "no reserve uses the hard cap", maxBytes: 1000, reservePercent: 0, want: 1000},
		{name: "half reserve", maxBytes: 1000, reservePercent: 50, want: 500},
		{name: "reserve above the allowed maximum is clamped", maxBytes: 1000, reservePercent: 99, want: 500},
		{name: "negative reserve is treated as none", maxBytes: 1000, reservePercent: -5, want: 1000},
		{name: "unlimited capacity has no threshold", maxBytes: 0, reservePercent: 10, want: 0},
		{name: "tiny capacity keeps a positive threshold", maxBytes: 3, reservePercent: 50, want: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, capacityEvictionThreshold(tc.maxBytes, tc.reservePercent))
		})
	}
}

func TestNormalizeTemporaryAssetPurposeFallsBackToReference(t *testing.T) {
	require.Equal(t, TemporaryAssetPurposeGenerated, normalizeTemporaryAssetPurpose(TemporaryAssetPurposeGenerated))
	require.Equal(t, TemporaryAssetPurposeReference, normalizeTemporaryAssetPurpose(TemporaryAssetPurposeReference))
	require.Equal(t, TemporaryAssetPurposeReference, normalizeTemporaryAssetPurpose(""))
	require.Equal(t, TemporaryAssetPurposeReference, normalizeTemporaryAssetPurpose("something-else"))
}

// 产物的每日配额默认 0 = 不限制：产物是已付费的交付物，不该被配额挡住。
func TestNormalizeFileStorageConfigResultDailyQuotaDefaultsToUnlimited(t *testing.T) {
	normalized, err := normalizeFileStorageConfig(FileStorageConfig{
		SchemaVersion:  1,
		Backend:        "local",
		PublicBaseURL:  "https://api.example.com",
		RetentionHours: 24,
		DailyMaxCount:  100,
		DailyMaxBytes:  1 << 30,
	})
	require.NoError(t, err)
	require.Zero(t, normalized.ResultDailyMaxCount)
	require.Zero(t, normalized.ResultDailyMaxBytes)

	negativeCount := defaultFileStorageConfig()
	negativeCount.ResultDailyMaxCount = -1
	_, err = normalizeFileStorageConfig(negativeCount)
	require.ErrorContains(t, err, "result_daily_max_count")

	tooMany := defaultFileStorageConfig()
	tooMany.ResultDailyMaxCount = 1_000_001
	_, err = normalizeFileStorageConfig(tooMany)
	require.ErrorContains(t, err, "result_daily_max_count")

	negativeBytes := defaultFileStorageConfig()
	negativeBytes.ResultDailyMaxBytes = -1
	_, err = normalizeFileStorageConfig(negativeBytes)
	require.ErrorContains(t, err, "result_daily_max_bytes")

	configured := defaultFileStorageConfig()
	configured.ResultDailyMaxCount = 500
	configured.ResultDailyMaxBytes = 8 << 30
	normalized, err = normalizeFileStorageConfig(configured)
	require.NoError(t, err)
	require.EqualValues(t, 500, normalized.ResultDailyMaxCount)
	require.EqualValues(t, 8<<30, normalized.ResultDailyMaxBytes)
}
