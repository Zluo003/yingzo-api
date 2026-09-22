package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	settingKeyFileStorageConfig = "file_storage_config"
	fileStorageSchemaVersion    = 1
	defaultFileRetentionHours   = 24
	defaultFileDailyMaxCount    = int64(100)
	defaultFileDailyMaxBytes    = int64(2 << 30)

	// TemporaryAssetPurposeReference 是下游上传的参考素材；
	// TemporaryAssetPurposeGenerated 是上游回捞的生成产物。两者各有独立的容量预算。
	TemporaryAssetPurposeReference = "reference"
	TemporaryAssetPurposeGenerated = "generated"

	// 容量水位：写入会让占用超过"上限 × (1 - 冗余比例)"时就开始提前清理，而不是等
	// 撞到硬上限。留出冗余空间，写入才不会因为容量刚好用尽而失败。
	defaultFileCapacityReservePercent = 10
	maxFileCapacityReservePercent     = 50
	// maxFileResultRetentionHours 产物的保存时长上限放到一年：交付物通常比参考素材
	// 存得久得多，但也不能无限增长。
	maxFileResultRetentionHours = 8760
)

// FileStorageConfig 是临时素材库的设置。参考素材与生成产物各有独立的保存时长与容量
// 预算，避免客户端上传的参考素材把已经交付给下游的产物挤掉。
type FileStorageConfig struct {
	SchemaVersion int    `json:"schema_version"`
	Backend       string `json:"backend"`
	// LocalDir 是本地素材根目录（绝对路径）。留空时用默认目录（数据目录下的
	// agent-assets）。Docker 部署时它应当指向从宿主机 bind mount 进来的真实目录，
	// 而不是容器内的匿名卷；脚本部署时它就是一个普通宿主机目录。
	//
	// S3 后端也会用到它：产物先写本地再上传对象存储，因此该目录始终需要可写。
	LocalDir       string `json:"local_dir"`
	PublicBaseURL  string `json:"public_base_url"`
	RetentionHours int    `json:"retention_hours"`
	DailyMaxCount  int64  `json:"daily_max_count"`
	DailyMaxBytes  int64  `json:"daily_max_bytes"`
	// MaxTotalBytes 是参考素材的总容量上限（0 表示不限制）。容量不足时按"最早失效
	// 优先"提前驱逐未租用的素材，而不是直接拒绝新上传。
	MaxTotalBytes int64 `json:"max_total_bytes"`
	// ResultRetentionHours 是生成产物的保存时长，独立于参考素材的 RetentionHours。
	ResultRetentionHours int `json:"result_retention_hours"`
	// ResultMaxTotalBytes 是生成产物的总容量上限（0 表示不限制），与参考素材各自独立预算。
	ResultMaxTotalBytes int64 `json:"result_max_total_bytes"`
	// ResultDailyMaxCount / ResultDailyMaxBytes 是生成产物自己的 24 小时配额（按凭据）。
	// 默认 0 = 不限制：生成量不该因为配额把已经付费的结果挡在门外；需要防刷时再填值。
	// 参考素材用的是另一套 DailyMaxCount / DailyMaxBytes，两者互不占用。
	ResultDailyMaxCount int64 `json:"result_daily_max_count"`
	ResultDailyMaxBytes int64 `json:"result_daily_max_bytes"`
	// CapacityReservePercent 是容量冗余比例（0-50），对参考素材与生成产物各自的上限
	// 分别生效：占用超过 上限×(1-冗余) 就开始按最早失效优先清理。
	//
	// 用指针是为了区分"没配置过"（nil，升级上来的老配置，按默认 10% 补齐）和"显式配
	// 成 0%"（不留冗余，占用到上限才开始清理）——两者行为不同，不能被默认值吃掉。
	CapacityReservePercent *int           `json:"capacity_reserve_percent"`
	S3                     BackupS3Config `json:"s3"`
}

type FileStorageUsage struct {
	ActiveFiles         int64 `json:"active_files"`
	ActiveBytes         int64 `json:"active_bytes"`
	LocalFiles          int64 `json:"local_files"`
	S3Files             int64 `json:"s3_files"`
	ExpiringWithin1Hour int64 `json:"expiring_within_1_hour"`
	// 按类别拆分，便于对照各自的容量预算。
	ReferenceFiles int64 `json:"reference_files"`
	ReferenceBytes int64 `json:"reference_bytes"`
	GeneratedFiles int64 `json:"generated_files"`
	GeneratedBytes int64 `json:"generated_bytes"`
}

type FileStorageSettings struct {
	FileStorageConfig
	Source                    string           `json:"source"`
	LocalPath                 string           `json:"local_path"`
	SecretAccessKeyConfigured bool             `json:"secret_access_key_configured"`
	Usage                     FileStorageUsage `json:"usage"`
	// MountedHostDir 是容器内素材目录对应的宿主机目录，由部署时注入的
	// AGENT_ASSETS_HOST_DIR 提供。它只用于在设置页把"容器内路径 ← 宿主机目录"的映射
	// 显示出来，避免管理员把宿主机路径填进 local_dir。取不到时为空。
	MountedHostDir string `json:"mounted_host_dir"`
}

type FileStorageRuntime struct {
	Config FileStorageConfig
	Store  BackupObjectStore
}

type FileStorageService struct {
	db               *sql.DB
	settingRepo      SettingRepository
	encryptor        SecretEncryptor
	storeFactory     BackupObjectStoreFactory
	defaultLocalPath string

	storeMu          sync.Mutex
	storeFingerprint string
	store            BackupObjectStore
}

func NewFileStorageService(
	db *sql.DB,
	settingRepo SettingRepository,
	encryptor SecretEncryptor,
	storeFactory BackupObjectStoreFactory,
	cfg *config.Config,
) *FileStorageService {
	// 默认目录统一取绝对路径：data_dir 可能是相对路径，而素材路径会写进数据库并用于
	// 跨进程读取，不能依赖进程当前工作目录。
	localPath := filepath.Join(cfg.Pricing.DataDir, "agent-assets")
	// 部署时用 AGENT_ASSETS_HOST_DIR 把宿主机目录按“同路径”挂进容器：那个路径就是本
	// 部署的素材默认根目录。必须认它，否则面板留空时会写进容器可写层——看着成功，
	// 容器一重建就全丢。未设置时才回落到数据目录下的 agent-assets。
	if mounted := strings.TrimSpace(os.Getenv("AGENT_ASSETS_HOST_DIR")); mounted != "" && filepath.IsAbs(mounted) {
		localPath = mounted
	} else if absolute, err := filepath.Abs(localPath); err == nil {
		localPath = absolute
	}
	// #nosec G703 -- 与 EffectiveLocalPath 同源：路径来自配置或数据目录，非用户输入。
	_ = os.MkdirAll(localPath, 0700)
	return &FileStorageService{
		db:               db,
		settingRepo:      settingRepo,
		encryptor:        encryptor,
		storeFactory:     storeFactory,
		defaultLocalPath: localPath,
	}
}

// LocalPath 返回构造时确定的默认素材目录。它是兜底值：可配置的 LocalDir 优先，
// 运行时请用 EffectiveLocalPath。
func (s *FileStorageService) LocalPath() string {
	if s == nil {
		return ""
	}
	return s.defaultLocalPath
}

// EffectiveLocalPath 返回当前生效的本地素材根目录：配置里指定了就用它，否则用默认目录。
//
// 配置的目录必须已经存在：它是管理员在宿主机上准备好、再挂进容器的真实目录，服务不应
// 替它创建。自动创建反而危险——宿主目录没挂进来时，mkdir 会"成功地"在容器可写层里造出
// 一个空目录，素材看着写成功了，容器一重建就全没了。默认目录在应用自己的数据目录里，
// 仍然按需创建。
func (s *FileStorageService) EffectiveLocalPath(ctx context.Context) (string, error) {
	if s == nil {
		return "", errors.New("file storage service is unavailable")
	}
	dir := s.defaultLocalPath
	configured := ""
	if cfg, _, err := s.loadEffectiveConfig(ctx); err != nil {
		return "", err
	} else if configured = strings.TrimSpace(cfg.LocalDir); configured != "" {
		dir = configured
	}
	if dir == "" {
		return "", errors.New("local asset directory is not configured")
	}
	if configured != "" {
		if err := requireExistingDirectory(dir); err != nil {
			return "", err
		}
		return dir, nil
	}
	// #nosec G703 -- 目录不是用户输入：它来自管理员在设置页配置的 local_dir，
	// 已通过 normalizeFileStorageLocalDir 校验（必须是绝对路径、拒绝系统目录），
	// 保存时还会做真实写入探针；这里只是按需创建该目录。
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("create local asset directory: %w", err)
	}
	return dir, nil
}

// requireExistingDirectory 校验配置的素材目录确实存在且是目录，不做任何创建。
func requireExistingDirectory(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("local_dir %q does not exist inside the service; create it where the service runs "+
				"(with Docker: create it on the host and bind-mount it, then keep this field empty or point it at "+
				"the in-container mount path)", dir)
		}
		return fmt.Errorf("local_dir %q cannot be inspected: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("local_dir %q is not a directory", dir)
	}
	return nil
}

// EffectiveLocalPathOrDefault 与 EffectiveLocalPath 相同，但取不到时回落到默认目录，
// 供展示场景（设置页）使用。
func (s *FileStorageService) EffectiveLocalPathOrDefault(ctx context.Context) string {
	dir, err := s.EffectiveLocalPath(ctx)
	if err != nil || dir == "" {
		return s.LocalPath()
	}
	return dir
}

func (s *FileStorageService) GetSettings(ctx context.Context) (*FileStorageSettings, error) {
	cfg, source, err := s.loadEffectiveConfig(ctx)
	if err != nil {
		return nil, err
	}
	secretConfigured := strings.TrimSpace(cfg.S3.SecretAccessKey) != ""
	cfg.S3.SecretAccessKey = ""
	usage, err := s.Usage(ctx)
	if err != nil {
		return nil, err
	}
	return &FileStorageSettings{
		FileStorageConfig:         cfg,
		Source:                    source,
		LocalPath:                 s.EffectiveLocalPathOrDefault(ctx),
		SecretAccessKeyConfigured: secretConfigured,
		Usage:                     usage,
		MountedHostDir:            strings.TrimSpace(os.Getenv("AGENT_ASSETS_HOST_DIR")),
	}, nil
}

func (s *FileStorageService) UpdateSettings(ctx context.Context, input FileStorageConfig) (*FileStorageSettings, error) {
	current, _, _ := s.loadEffectiveConfig(ctx)
	if strings.TrimSpace(input.S3.SecretAccessKey) == "" {
		input.S3.SecretAccessKey = current.S3.SecretAccessKey
	}
	cfg, err := normalizeFileStorageConfig(input)
	if err != nil {
		return nil, infraerrors.BadRequest("FILE_STORAGE_CONFIG_INVALID", err.Error())
	}
	if err := s.verifyLocalDir(cfg); err != nil {
		return nil, infraerrors.BadRequest("FILE_STORAGE_LOCAL_DIR_UNAVAILABLE", err.Error())
	}
	if err := s.testConfig(ctx, cfg); err != nil {
		return nil, infraerrors.BadRequest("FILE_STORAGE_CONNECTION_FAILED", err.Error())
	}

	stored := cfg
	if stored.S3.SecretAccessKey != "" {
		if s.encryptor == nil {
			return nil, errors.New("file storage secret encryptor is unavailable")
		}
		stored.S3.SecretAccessKey, err = s.encryptor.Encrypt(stored.S3.SecretAccessKey)
		if err != nil {
			return nil, fmt.Errorf("encrypt file storage secret: %w", err)
		}
	}
	payload, err := json.Marshal(stored)
	if err != nil {
		return nil, fmt.Errorf("marshal file storage config: %w", err)
	}
	if s.settingRepo == nil {
		return nil, errors.New("file storage setting repository is unavailable")
	}
	if err := s.settingRepo.Set(ctx, settingKeyFileStorageConfig, string(payload)); err != nil {
		return nil, fmt.Errorf("save file storage config: %w", err)
	}
	s.invalidateStore()
	return s.GetSettings(ctx)
}

func (s *FileStorageService) TestSettings(ctx context.Context, input FileStorageConfig) error {
	current, _, _ := s.loadEffectiveConfig(ctx)
	if strings.TrimSpace(input.S3.SecretAccessKey) == "" {
		input.S3.SecretAccessKey = current.S3.SecretAccessKey
	}
	cfg, err := normalizeFileStorageConfig(input)
	if err != nil {
		return infraerrors.BadRequest("FILE_STORAGE_CONFIG_INVALID", err.Error())
	}
	if err := s.verifyLocalDir(cfg); err != nil {
		return infraerrors.BadRequest("FILE_STORAGE_LOCAL_DIR_UNAVAILABLE", err.Error())
	}
	if err := s.testConfig(ctx, cfg); err != nil {
		return infraerrors.BadRequest("FILE_STORAGE_CONNECTION_FAILED", err.Error())
	}
	return nil
}

// verifyLocalDir 校验本地素材目录可创建可写。
//
// S3 后端同样要校验：产物会先落到本地目录作为中转，再上传到对象存储，目录不可写照样
// 会让发布失败。与其等到用户生成完才发现，不如保存设置时就报错。
func (s *FileStorageService) verifyLocalDir(cfg FileStorageConfig) error {
	if dir := strings.TrimSpace(cfg.LocalDir); dir != "" {
		return verifyConfiguredLocalDir(dir)
	}
	if s.defaultLocalPath == "" {
		return errors.New("local_dir is not configured")
	}
	return verifyDefaultLocalDir(s.defaultLocalPath)
}

func (s *FileStorageService) Runtime(ctx context.Context) (*FileStorageRuntime, error) {
	cfg, _, err := s.loadEffectiveConfig(ctx)
	if err != nil {
		return nil, err
	}
	var store BackupObjectStore
	if cfg.S3.IsConfigured() {
		store, err = s.storeForConfig(ctx, cfg.S3)
		if err != nil {
			return nil, err
		}
	}
	return &FileStorageRuntime{Config: cfg, Store: store}, nil
}

func (s *FileStorageService) EffectivePublicBaseURL(ctx context.Context, fallback string) (string, error) {
	cfg, _, err := s.loadEffectiveConfig(ctx)
	if err != nil {
		return "", err
	}
	if cfg.PublicBaseURL != "" {
		return cfg.PublicBaseURL, nil
	}
	return normalizeFileStoragePublicBaseURL(fallback)
}

func (s *FileStorageService) Usage(ctx context.Context) (FileStorageUsage, error) {
	if s.db == nil {
		return FileStorageUsage{}, nil
	}
	var usage FileStorageUsage
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(size_bytes),0),
			COUNT(*) FILTER (WHERE storage_backend='local'),
			COUNT(*) FILTER (WHERE storage_backend='s3'),
			COUNT(*) FILTER (WHERE expires_at<=NOW()+INTERVAL '1 hour'),
			COUNT(*) FILTER (WHERE purpose='`+TemporaryAssetPurposeReference+`'),
			COALESCE(SUM(size_bytes) FILTER (WHERE purpose='`+TemporaryAssetPurposeReference+`'),0),
			COUNT(*) FILTER (WHERE purpose='`+TemporaryAssetPurposeGenerated+`'),
			COALESCE(SUM(size_bytes) FILTER (WHERE purpose='`+TemporaryAssetPurposeGenerated+`'),0)
		FROM temporary_assets
		WHERE deleted_at IS NULL AND expires_at>NOW()
	`).Scan(
		&usage.ActiveFiles, &usage.ActiveBytes, &usage.LocalFiles, &usage.S3Files, &usage.ExpiringWithin1Hour,
		&usage.ReferenceFiles, &usage.ReferenceBytes, &usage.GeneratedFiles, &usage.GeneratedBytes,
	)
	return usage, err
}

func (s *FileStorageService) loadEffectiveConfig(ctx context.Context) (FileStorageConfig, string, error) {
	if s.settingRepo != nil {
		raw, err := s.settingRepo.GetValue(ctx, settingKeyFileStorageConfig)
		if err == nil && strings.TrimSpace(raw) != "" {
			var stored FileStorageConfig
			if err := json.Unmarshal([]byte(raw), &stored); err != nil {
				return FileStorageConfig{}, "", fmt.Errorf("decode file storage config: %w", err)
			}
			if stored.S3.SecretAccessKey != "" {
				if s.encryptor == nil {
					return FileStorageConfig{}, "", errors.New("file storage secret encryptor is unavailable")
				}
				stored.S3.SecretAccessKey, err = s.encryptor.Decrypt(stored.S3.SecretAccessKey)
				if err != nil {
					return FileStorageConfig{}, "", fmt.Errorf("decrypt file storage secret: %w", err)
				}
			}
			normalized, err := normalizeFileStorageConfig(stored)
			if err != nil {
				return FileStorageConfig{}, "", fmt.Errorf("stored file storage config is invalid: %w", err)
			}
			return normalized, "database", nil
		}
	}
	cfg, source := fileStorageConfigFromEnvironment()
	normalized, err := normalizeFileStorageConfig(cfg)
	return normalized, source, err
}

func fileStorageConfigFromEnvironment() (FileStorageConfig, string) {
	cfg := defaultFileStorageConfig()
	source := "default"
	read := func(keys ...string) string {
		for _, key := range keys {
			if value := strings.TrimSpace(os.Getenv(key)); value != "" {
				source = "environment"
				return value
			}
		}
		return ""
	}
	cfg.PublicBaseURL = read("FILE_SERVICE_PUBLIC_BASE_URL", "AGENT_ASSETS_PUBLIC_BASE_URL")
	cfg.LocalDir = read("FILE_SERVICE_LOCAL_DIR", "AGENT_ASSETS_LOCAL_DIR")
	if value := read("FILE_SERVICE_RETENTION_HOURS"); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			cfg.RetentionHours = parsed
		}
	}
	if value := read("FILE_SERVICE_DAILY_MAX_COUNT", "AGENT_ASSETS_DAILY_MAX_COUNT"); value != "" {
		if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
			cfg.DailyMaxCount = parsed
		}
	}
	if value := read("FILE_SERVICE_DAILY_MAX_BYTES", "AGENT_ASSETS_DAILY_MAX_BYTES"); value != "" {
		if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
			cfg.DailyMaxBytes = parsed
		}
	}
	if value := read("FILE_SERVICE_MAX_TOTAL_BYTES", "AGENT_ASSETS_MAX_TOTAL_BYTES"); value != "" {
		if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
			cfg.MaxTotalBytes = parsed
		}
	}
	if value := read("FILE_SERVICE_RESULT_RETENTION_HOURS"); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			cfg.ResultRetentionHours = parsed
		}
	}
	if value := read("FILE_SERVICE_RESULT_MAX_TOTAL_BYTES"); value != "" {
		if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
			cfg.ResultMaxTotalBytes = parsed
		}
	}
	if value := read("FILE_SERVICE_RESULT_DAILY_MAX_COUNT"); value != "" {
		if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
			cfg.ResultDailyMaxCount = parsed
		}
	}
	if value := read("FILE_SERVICE_RESULT_DAILY_MAX_BYTES"); value != "" {
		if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
			cfg.ResultDailyMaxBytes = parsed
		}
	}
	if value := read("FILE_SERVICE_CAPACITY_RESERVE_PERCENT"); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			cfg.CapacityReservePercent = fileStorageReservePercentPtr(parsed)
		}
	}
	cfg.S3 = BackupS3Config{
		Endpoint:        read("FILE_SERVICE_S3_ENDPOINT", "AGENT_ASSETS_S3_ENDPOINT"),
		Region:          read("FILE_SERVICE_S3_REGION", "AGENT_ASSETS_S3_REGION"),
		Bucket:          read("FILE_SERVICE_S3_BUCKET", "AGENT_ASSETS_S3_BUCKET"),
		AccessKeyID:     read("FILE_SERVICE_S3_ACCESS_KEY_ID", "AGENT_ASSETS_S3_ACCESS_KEY_ID"),
		SecretAccessKey: read("FILE_SERVICE_S3_SECRET_ACCESS_KEY", "AGENT_ASSETS_S3_SECRET_ACCESS_KEY"),
		Prefix:          read("FILE_SERVICE_S3_PREFIX"),
		CustomDomain:    read("FILE_SERVICE_S3_CUSTOM_DOMAIN", "AGENT_ASSETS_S3_CUSTOM_DOMAIN"),
		ForcePathStyle:  strings.EqualFold(read("FILE_SERVICE_S3_FORCE_PATH_STYLE", "AGENT_ASSETS_S3_FORCE_PATH_STYLE"), "true"),
	}
	if cfg.S3.Bucket != "" {
		cfg.Backend = "s3"
	}
	return cfg, source
}

// fileStorageReservePercentPtr 返回冗余比例的指针，便于区分"未设置"与"显式 0"。
func fileStorageReservePercentPtr(percent int) *int {
	return &percent
}

// EffectiveCapacityReservePercent 返回实际生效的冗余比例（未设置时为默认值）。
func (c FileStorageConfig) EffectiveCapacityReservePercent() int {
	if c.CapacityReservePercent == nil {
		return defaultFileCapacityReservePercent
	}
	return *c.CapacityReservePercent
}

func defaultFileStorageConfig() FileStorageConfig {
	return FileStorageConfig{
		SchemaVersion:          fileStorageSchemaVersion,
		Backend:                "local",
		RetentionHours:         defaultFileRetentionHours,
		DailyMaxCount:          defaultFileDailyMaxCount,
		DailyMaxBytes:          defaultFileDailyMaxBytes,
		ResultRetentionHours:   defaultFileRetentionHours,
		CapacityReservePercent: fileStorageReservePercentPtr(defaultFileCapacityReservePercent),
		S3: BackupS3Config{
			Region: "auto",
			Prefix: "model-assets/",
		},
	}
}

func normalizeFileStorageConfig(input FileStorageConfig) (FileStorageConfig, error) {
	if input.SchemaVersion == 0 {
		input.SchemaVersion = fileStorageSchemaVersion
	}
	if input.SchemaVersion != fileStorageSchemaVersion {
		return FileStorageConfig{}, fmt.Errorf("unsupported schema_version %d", input.SchemaVersion)
	}
	input.Backend = strings.ToLower(strings.TrimSpace(input.Backend))
	if input.Backend == "" {
		input.Backend = "local"
	}
	if input.Backend != "local" && input.Backend != "s3" {
		return FileStorageConfig{}, errors.New("backend must be local or s3")
	}
	if strings.TrimSpace(input.LocalDir) != "" {
		normalized, err := normalizeFileStorageLocalDir(input.LocalDir)
		if err != nil {
			return FileStorageConfig{}, err
		}
		input.LocalDir = normalized
	}
	if strings.TrimSpace(input.PublicBaseURL) != "" {
		normalized, err := normalizeFileStoragePublicBaseURL(input.PublicBaseURL)
		if err != nil {
			return FileStorageConfig{}, err
		}
		input.PublicBaseURL = normalized
	}
	if input.RetentionHours < 1 || input.RetentionHours > 720 {
		return FileStorageConfig{}, errors.New("retention_hours must be between 1 and 720")
	}
	if input.DailyMaxCount < 1 || input.DailyMaxCount > 1_000_000 {
		return FileStorageConfig{}, errors.New("daily_max_count must be between 1 and 1000000")
	}
	if input.DailyMaxBytes < 1 || input.DailyMaxBytes > 1<<50 {
		return FileStorageConfig{}, errors.New("daily_max_bytes is outside the supported range")
	}
	if input.MaxTotalBytes < 0 || input.MaxTotalBytes > 1<<50 {
		return FileStorageConfig{}, errors.New("max_total_bytes is outside the supported range")
	}
	// 老配置里没有产物相关字段：零值一律回落到默认值，不能让升级后的既有部署读不出配置。
	if input.ResultRetentionHours == 0 {
		input.ResultRetentionHours = defaultFileRetentionHours
	}
	if input.ResultRetentionHours < 1 || input.ResultRetentionHours > maxFileResultRetentionHours {
		return FileStorageConfig{}, fmt.Errorf("result_retention_hours must be between 1 and %d", maxFileResultRetentionHours)
	}
	if input.ResultMaxTotalBytes < 0 || input.ResultMaxTotalBytes > 1<<50 {
		return FileStorageConfig{}, errors.New("result_max_total_bytes is outside the supported range")
	}
	if input.ResultDailyMaxCount < 0 || input.ResultDailyMaxCount > 1_000_000 {
		return FileStorageConfig{}, errors.New("result_daily_max_count must be between 0 and 1000000")
	}
	if input.ResultDailyMaxBytes < 0 || input.ResultDailyMaxBytes > 1<<50 {
		return FileStorageConfig{}, errors.New("result_daily_max_bytes is outside the supported range")
	}
	// nil 来自更早版本的配置（那时还没有冗余水位），按默认值补齐而不是判为非法；
	// 显式配置的 0 是合法值，表示不留冗余。
	if input.CapacityReservePercent == nil {
		input.CapacityReservePercent = fileStorageReservePercentPtr(defaultFileCapacityReservePercent)
	}
	if *input.CapacityReservePercent < 0 || *input.CapacityReservePercent > maxFileCapacityReservePercent {
		return FileStorageConfig{}, fmt.Errorf("capacity_reserve_percent must be between 0 and %d", maxFileCapacityReservePercent)
	}
	input.S3.Endpoint = strings.TrimSpace(input.S3.Endpoint)
	input.S3.Region = strings.TrimSpace(input.S3.Region)
	if input.S3.Region == "" {
		input.S3.Region = "auto"
	}
	input.S3.Bucket = strings.TrimSpace(input.S3.Bucket)
	input.S3.AccessKeyID = strings.TrimSpace(input.S3.AccessKeyID)
	input.S3.SecretAccessKey = strings.TrimSpace(input.S3.SecretAccessKey)
	prefix, err := normalizeFileStoragePrefix(input.S3.Prefix)
	if err != nil {
		return FileStorageConfig{}, err
	}
	input.S3.Prefix = prefix
	if input.S3.CustomDomain != "" {
		normalized, err := normalizeFileStorageCustomDomain(input.S3.CustomDomain)
		if err != nil {
			return FileStorageConfig{}, err
		}
		input.S3.CustomDomain = normalized
	}
	if input.S3.Endpoint != "" {
		parsed, err := url.Parse(input.S3.Endpoint)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
			return FileStorageConfig{}, errors.New("S3 endpoint must contain only an HTTP(S) scheme and host")
		}
		input.S3.Endpoint = strings.TrimRight(parsed.String(), "/")
	}
	if input.Backend == "s3" && !input.S3.IsConfigured() {
		return FileStorageConfig{}, errors.New("S3 backend requires bucket, access key ID, and secret access key")
	}
	return input, nil
}

// blockedFileStorageDirs 是明确拒绝的目录：把素材根目录指到这些地方只会破坏系统，
// 没有任何正当用途。
var blockedFileStorageDirs = map[string]bool{
	"/": true, "/bin": true, "/sbin": true, "/lib": true, "/lib64": true,
	"/usr": true, "/etc": true, "/proc": true, "/sys": true, "/dev": true,
	"/boot": true, "/root": true, "/var": true,
}

// normalizeFileStorageLocalDir 校验本地素材目录：必须是绝对路径、不能是系统目录。
// 相对路径一律拒绝——容器与脚本部署里相对路径的含义不同，配置出来必然是错的。
func normalizeFileStorageLocalDir(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if !filepath.IsAbs(trimmed) {
		return "", errors.New("local_dir must be an absolute path")
	}
	cleaned := filepath.Clean(trimmed)
	if cleaned == string(filepath.Separator) {
		return "", errors.New("local_dir must not be the filesystem root")
	}
	if strings.ContainsRune(cleaned, 0) {
		return "", errors.New("local_dir is invalid")
	}
	if blockedFileStorageDirs[cleaned] {
		return "", fmt.Errorf("local_dir must not be a system directory: %s", cleaned)
	}
	return cleaned, nil
}

// localDirContainerHint 解释了最常见的误配：把宿主机的路径填进了面板。后端（尤其是
// 容器里的后端）看到的是自己的文件系统，宿主机路径必须先挂进来、并填写容器内的路径。
const localDirContainerHint = "note: this path is resolved inside the service process; " +
	"with Docker the host directory must be bind-mounted first and this field must hold the " +
	"in-container path (default /app/data/agent-assets, host side set via AGENT_ASSETS_HOST_DIR)"

// verifyConfiguredLocalDir 校验管理员配置的素材目录：必须已存在，且能写进去。
// 不创建目录——目录是管理员在宿主机上准备好的，服务只负责使用和校验。
func verifyConfiguredLocalDir(dir string) error {
	if err := requireExistingDirectory(dir); err != nil {
		return err
	}
	return probeLocalDirWritable(dir)
}

// verifyDefaultLocalDir 校验默认目录（应用数据目录内），允许按需创建。
func verifyDefaultLocalDir(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("cannot create local_dir %q: %w", dir, err)
	}
	return probeLocalDirWritable(dir)
}

// probeLocalDirWritable 写一个探针文件，确保配置生效后素材一定写得进去（权限不对时
// 保存设置就该失败，而不是等到用户上传才炸）。
func probeLocalDirWritable(dir string) error {
	file, err := os.CreateTemp(dir, ".file-service-probe-")
	if err != nil {
		return fmt.Errorf("local_dir %q is not writable: %w; %s", dir, err, localDirContainerHint)
	}
	name := file.Name()
	if closeErr := file.Close(); closeErr != nil {
		_ = os.Remove(name)
		return fmt.Errorf("local_dir %q is not writable: %w; %s", dir, closeErr, localDirContainerHint)
	}
	return os.Remove(name)
}

func normalizeFileStoragePublicBaseURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New("public_base_url must contain only an HTTP(S) scheme and host")
	}
	if parsed.Scheme != "https" && parsed.Hostname() != "localhost" && parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "::1" {
		return "", errors.New("public_base_url must use HTTPS outside local development")
	}
	parsed.Path = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

func normalizeFileStoragePrefix(raw string) (string, error) {
	prefix := strings.Trim(strings.TrimSpace(raw), "/")
	if prefix == "" {
		return "model-assets/", nil
	}
	for _, segment := range strings.Split(prefix, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", errors.New("S3 prefix contains an unsafe path segment")
		}
	}
	return prefix + "/", nil
}

// normalizeFileStorageCustomDomain 校验 S3 自定义域名：只允许 scheme+host（可带端口），
// 不允许路径、查询与用户信息；本地开发之外必须是 HTTPS。素材 URL 会以它为前缀直接
// 拼接对象 key，任何多余部分都会让拼出来的地址不可用。
func normalizeFileStorageCustomDomain(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New("S3 custom domain must contain only an HTTP(S) scheme and host")
	}
	if parsed.Scheme != "https" && parsed.Hostname() != "localhost" && parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "::1" {
		return "", errors.New("S3 custom domain must use HTTPS outside local development")
	}
	parsed.Path = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

// S3CustomAccess 描述 S3 自定义域名直读访问的形状：素材 URL = Base + Prefix + 素材 ID。
type S3CustomAccess struct {
	// Base 是自定义域名基址（https://host，无尾斜杠）；空表示未启用直读。
	Base string
	// Prefix 是对象前缀（归一化后带尾斜杠）。
	Prefix string
}

// Enabled 报告自定义域名直读是否已启用。
func (a S3CustomAccess) Enabled() bool { return a.Base != "" }

// Host 返回自定义域名基址的 hostname（小写）；基址为空或非法时返回空串。
func (a S3CustomAccess) Host() string {
	parsed, err := url.Parse(a.Base)
	if err != nil || parsed.Host == "" {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}

// EffectiveS3CustomAccess 返回当前生效的自定义域名直读访问。仅 S3 后端且显式配置了
// 自定义域名时非空；参考素材的上传响应会用它构造对象存储直读地址。
func (s *FileStorageService) EffectiveS3CustomAccess(ctx context.Context) S3CustomAccess {
	if s == nil {
		return S3CustomAccess{}
	}
	cfg, _, err := s.loadEffectiveConfig(ctx)
	if err != nil || cfg.Backend != "s3" {
		return S3CustomAccess{}
	}
	base := cfg.S3.CustomAssetBase()
	if base == "" {
		return S3CustomAccess{}
	}
	return S3CustomAccess{Base: base, Prefix: cfg.S3.Prefix}
}

func (s *FileStorageService) testConfig(ctx context.Context, cfg FileStorageConfig) error {
	if cfg.Backend == "local" {
		// 用待保存配置里的目录（而不是构造时的默认目录）验证可写性：管理员改的就是它。
		if err := s.verifyLocalDir(cfg); err != nil {
			return err
		}
		dir := strings.TrimSpace(cfg.LocalDir)
		if dir == "" {
			dir = s.defaultLocalPath
		}
		file, err := os.CreateTemp(dir, ".file-service-test-")
		if err != nil {
			return fmt.Errorf("write local storage directory: %w", err)
		}
		name := file.Name()
		if closeErr := file.Close(); closeErr != nil {
			_ = os.Remove(name)
			return fmt.Errorf("close local storage test file: %w", closeErr)
		}
		return os.Remove(name)
	}
	if s.storeFactory == nil {
		return errors.New("file storage object store factory is unavailable")
	}
	store, err := s.storeFactory(ctx, &cfg.S3)
	if err != nil {
		return fmt.Errorf("initialize S3 storage: %w", err)
	}
	if err := store.HeadBucket(ctx); err != nil {
		return err
	}
	return nil
}

func (s *FileStorageService) storeForConfig(ctx context.Context, cfg BackupS3Config) (BackupObjectStore, error) {
	fingerprintPayload, _ := json.Marshal(cfg)
	digest := sha256.Sum256(fingerprintPayload)
	fingerprint := hex.EncodeToString(digest[:])
	s.storeMu.Lock()
	defer s.storeMu.Unlock()
	if s.store != nil && s.storeFingerprint == fingerprint {
		return s.store, nil
	}
	if s.storeFactory == nil {
		return nil, errors.New("file storage object store factory is unavailable")
	}
	store, err := s.storeFactory(ctx, &cfg)
	if err != nil {
		return nil, fmt.Errorf("initialize file storage S3 client: %w", err)
	}
	s.store = store
	s.storeFingerprint = fingerprint
	return store, nil
}

func (s *FileStorageService) invalidateStore() {
	s.storeMu.Lock()
	s.store = nil
	s.storeFingerprint = ""
	s.storeMu.Unlock()
}
