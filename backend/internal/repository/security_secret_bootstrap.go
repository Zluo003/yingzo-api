package repository

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/securitysecret"
	"github.com/Wei-Shaw/sub2api/internal/config"
)

const (
	securitySecretKeyJWT              = "jwt_secret"
	securitySecretKeySecretEncryption = "secret_encryption_key"
	securitySecretReadRetryMax        = 5
	securitySecretReadRetryWait       = 10 * time.Millisecond
)

var readRandomBytes = rand.Read

func ensureBootstrapSecrets(ctx context.Context, client *ent.Client, cfg *config.Config) error {
	if client == nil {
		return fmt.Errorf("nil ent client")
	}
	if cfg == nil {
		return fmt.Errorf("nil config")
	}

	if err := ensureJWTSecret(ctx, client, cfg); err != nil {
		return err
	}
	return ensureSecretEncryptionKey(ctx, client, cfg)
}

func ensureJWTSecret(ctx context.Context, client *ent.Client, cfg *config.Config) error {
	cfg.JWT.Secret = strings.TrimSpace(cfg.JWT.Secret)
	if cfg.JWT.Secret != "" {
		storedSecret, err := createSecuritySecretIfAbsent(ctx, client, securitySecretKeyJWT, cfg.JWT.Secret)
		if err != nil {
			return fmt.Errorf("persist jwt secret: %w", err)
		}
		if storedSecret != cfg.JWT.Secret {
			log.Println("Warning: configured JWT secret mismatches persisted value; using persisted secret for cross-instance consistency.")
		}
		cfg.JWT.Secret = storedSecret
		return nil
	}

	secret, created, err := getOrCreateGeneratedSecuritySecret(ctx, client, securitySecretKeyJWT, 32)
	if err != nil {
		return fmt.Errorf("ensure jwt secret: %w", err)
	}
	cfg.JWT.Secret = secret

	if created {
		log.Println("Warning: JWT secret auto-generated and persisted to database. Consider rotating to a managed secret for production.")
	}
	return nil
}

// ensureSecretEncryptionKey 保证秘密加密密钥（cfg.Totp.EncryptionKey）在重启、
// 升级和多实例间保持一致。该密钥用于 AES 加密落库的敏感配置（S3 备份密钥、
// TOTP 密钥、支付配置、插件凭据等），历史上一旦未显式配置 TOTP_ENCRYPTION_KEY
// 就每次启动随机生成，导致所有持久化秘密在重启后无法解密，备份 S3 配置等写入
// 被迫拒绝（#4524）。现在与 jwt_secret 相同：首次启动生成一次并持久化到
// security_secrets 表，之后每次启动复用；显式配置的环境变量值会作为种子写入
// （与库里已有值冲突时以库内值为准）。密钥持久化后 EncryptionKeyConfigured
// 置为 true，解除各处持久化秘密的写入限制。
func ensureSecretEncryptionKey(ctx context.Context, client *ent.Client, cfg *config.Config) error {
	cfg.Totp.EncryptionKey = strings.TrimSpace(cfg.Totp.EncryptionKey)
	if cfg.Totp.EncryptionKey != "" {
		if err := validateSecretEncryptionKey(cfg.Totp.EncryptionKey); err != nil {
			return err
		}
		storedKey, err := createSecuritySecretIfAbsent(ctx, client, securitySecretKeySecretEncryption, cfg.Totp.EncryptionKey)
		if err != nil {
			return fmt.Errorf("persist secret encryption key: %w", err)
		}
		if err := validateSecretEncryptionKey(storedKey); err != nil {
			return err
		}
		if storedKey != cfg.Totp.EncryptionKey {
			log.Println("Warning: configured TOTP encryption key mismatches persisted value; using persisted key so previously encrypted secrets stay decryptable.")
		}
		cfg.Totp.EncryptionKey = storedKey
		cfg.Totp.EncryptionKeyConfigured = true
		return nil
	}

	key, created, err := getOrCreateGeneratedSecuritySecret(ctx, client, securitySecretKeySecretEncryption, 32)
	if err != nil {
		return fmt.Errorf("ensure secret encryption key: %w", err)
	}
	if err := validateSecretEncryptionKey(key); err != nil {
		return err
	}
	cfg.Totp.EncryptionKey = key
	cfg.Totp.EncryptionKeyConfigured = true
	if created {
		log.Println("Warning: secret encryption key auto-generated and persisted to database; set TOTP_ENCRYPTION_KEY to manage it explicitly.")
	}
	return nil
}

// validateSecretEncryptionKey 校验密钥值能被 AESEncryptor 使用：64 位 hex，
// 解码后恰好 32 字节（AES-256）。在启动阶段提前失败，避免服务起来后加密器报错。
func validateSecretEncryptionKey(value string) error {
	raw, err := hex.DecodeString(value)
	if err != nil {
		return fmt.Errorf("secret encryption key must be hex-encoded: %w", err)
	}
	if len(raw) != 32 {
		return fmt.Errorf("secret encryption key must decode to 32 bytes (64 hex chars), got %d bytes", len(raw))
	}
	return nil
}

func getOrCreateGeneratedSecuritySecret(ctx context.Context, client *ent.Client, key string, byteLength int) (string, bool, error) {
	existing, err := client.SecuritySecret.Query().Where(securitysecret.KeyEQ(key)).Only(ctx)
	if err == nil {
		value := strings.TrimSpace(existing.Value)
		if len([]byte(value)) < 32 {
			return "", false, fmt.Errorf("stored secret %q must be at least 32 bytes", key)
		}
		return value, false, nil
	}
	if !ent.IsNotFound(err) {
		return "", false, err
	}

	generated, err := generateHexSecret(byteLength)
	if err != nil {
		return "", false, err
	}

	if err := client.SecuritySecret.Create().
		SetKey(key).
		SetValue(generated).
		OnConflictColumns(securitysecret.FieldKey).
		DoNothing().
		Exec(ctx); err != nil {
		if !isSQLNoRowsError(err) {
			return "", false, err
		}
	}

	stored, err := querySecuritySecretWithRetry(ctx, client, key)
	if err != nil {
		return "", false, err
	}
	value := strings.TrimSpace(stored.Value)
	if len([]byte(value)) < 32 {
		return "", false, fmt.Errorf("stored secret %q must be at least 32 bytes", key)
	}
	return value, value == generated, nil
}

func createSecuritySecretIfAbsent(ctx context.Context, client *ent.Client, key, value string) (string, error) {
	value = strings.TrimSpace(value)
	if len([]byte(value)) < 32 {
		return "", fmt.Errorf("secret %q must be at least 32 bytes", key)
	}

	if err := client.SecuritySecret.Create().
		SetKey(key).
		SetValue(value).
		OnConflictColumns(securitysecret.FieldKey).
		DoNothing().
		Exec(ctx); err != nil {
		if !isSQLNoRowsError(err) {
			return "", err
		}
	}

	stored, err := querySecuritySecretWithRetry(ctx, client, key)
	if err != nil {
		return "", err
	}
	storedValue := strings.TrimSpace(stored.Value)
	if len([]byte(storedValue)) < 32 {
		return "", fmt.Errorf("stored secret %q must be at least 32 bytes", key)
	}
	return storedValue, nil
}

func querySecuritySecretWithRetry(ctx context.Context, client *ent.Client, key string) (*ent.SecuritySecret, error) {
	var lastErr error
	for attempt := 0; attempt <= securitySecretReadRetryMax; attempt++ {
		stored, err := client.SecuritySecret.Query().Where(securitysecret.KeyEQ(key)).Only(ctx)
		if err == nil {
			return stored, nil
		}
		if !isSecretNotFoundError(err) {
			return nil, err
		}
		lastErr = err
		if attempt == securitySecretReadRetryMax {
			break
		}

		timer := time.NewTimer(securitySecretReadRetryWait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, lastErr
}

func isSecretNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	return ent.IsNotFound(err) || isSQLNoRowsError(err)
}

func isSQLNoRowsError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, sql.ErrNoRows) || strings.Contains(err.Error(), "no rows in result set")
}

func generateHexSecret(byteLength int) (string, error) {
	if byteLength <= 0 {
		byteLength = 32
	}
	buf := make([]byte, byteLength)
	if _, err := readRandomBytes(buf); err != nil {
		return "", fmt.Errorf("generate random secret: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
