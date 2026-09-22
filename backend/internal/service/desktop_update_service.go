package service

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/mod/semver"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	desktopUpdateStorageSettingKey = "desktop_update_storage_config"
	desktopUpdatePublicBaseURL     = "https://updata.yingzo.art"
	desktopUpdateDefaultPrefix     = "desktop-updates"
)

type DesktopUpdateStorageConfig struct {
	Backend       string                `json:"backend"`
	LocalDir      string                `json:"local_dir"`
	PublicBaseURL string                `json:"public_base_url"`
	R2            DesktopUpdateR2Config `json:"r2"`
}

type DesktopUpdateR2Config struct {
	Endpoint        string `json:"endpoint"`
	Region          string `json:"region"`
	Bucket          string `json:"bucket"`
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key,omitempty"`
	Prefix          string `json:"prefix"`
	CustomDomain    string `json:"custom_domain"`
	ForcePathStyle  bool   `json:"force_path_style"`
}

type DesktopUpdateStorageView struct {
	Backend                   string                `json:"backend"`
	LocalDir                  string                `json:"local_dir"`
	EffectiveLocalDir         string                `json:"effective_local_dir"`
	PublicBaseURL             string                `json:"public_base_url"`
	SecretAccessKeyConfigured bool                  `json:"secret_access_key_configured"`
	R2                        DesktopUpdateR2Config `json:"r2"`
}

type DesktopRelease struct {
	ID                  string     `json:"id"`
	Version             string     `json:"version"`
	Platform            string     `json:"platform"`
	Arch                string     `json:"arch"`
	Status              string     `json:"status"`
	PackageFilename     string     `json:"package_filename"`
	StorageBackend      string     `json:"storage_backend"`
	PackageSize         int64      `json:"package_size_bytes"`
	SHA256              string     `json:"sha256"`
	SHA512              string     `json:"sha512"`
	ReleaseNotes        string     `json:"release_notes"`
	DownloadURL         string     `json:"download_url"`
	MetadataURL         string     `json:"metadata_url"`
	InstallerFilename   string     `json:"installer_filename,omitempty"`
	InstallerSize       int64      `json:"installer_size_bytes,omitempty"`
	InstallerSHA256     string     `json:"installer_sha256,omitempty"`
	InstallerSHA512     string     `json:"-"`
	InstallerURL        string     `json:"installer_url,omitempty"`
	StorageKey          string     `json:"-"`
	MetadataKey         string     `json:"-"`
	InstallerStorageKey string     `json:"-"`
	CreatedAt           time.Time  `json:"created_at"`
	PublishedAt         *time.Time `json:"published_at,omitempty"`
}

type DesktopUpdateCheck struct {
	Update             bool   `json:"update"`
	Version            string `json:"version,omitempty"`
	Notes              string `json:"notes,omitempty"`
	DownloadURL        string `json:"download_url,omitempty"`
	SHA256             string `json:"sha256,omitempty"`
	SizeBytes          int64  `json:"size_bytes,omitempty"`
	InstallerURL       string `json:"installer_url,omitempty"`
	InstallerSizeBytes int64  `json:"installer_size_bytes,omitempty"`
}

type DesktopReleaseInput struct {
	Version           string
	Platform          string
	Arch              string
	ReleaseNotes      string
	Filename          string
	InstallerFilename string
}

type DesktopUpdateService struct {
	db           *sql.DB
	settingRepo  SettingRepository
	encryptor    SecretEncryptor
	storeFactory BackupObjectStoreFactory
	defaultDir   string
	uploadQueue  *DesktopUploadQueue
}

func NewDesktopUpdateService(db *sql.DB, settingRepo SettingRepository, encryptor SecretEncryptor, storeFactory BackupObjectStoreFactory, cfg *config.Config) *DesktopUpdateService {
	dataDir := "./data"
	if cfg != nil && strings.TrimSpace(cfg.Pricing.DataDir) != "" {
		dataDir = cfg.Pricing.DataDir
	}
	absolute, err := filepath.Abs(filepath.Join(dataDir, "desktop-updates"))
	if err == nil {
		dataDir = absolute
	}
	service := &DesktopUpdateService{db: db, settingRepo: settingRepo, encryptor: encryptor, storeFactory: storeFactory, defaultDir: dataDir}
	service.uploadQueue = newDesktopUploadQueue(filepath.Join(dataDir, ".uploads"), func(ctx context.Context, input DesktopReleaseInput, filePath, installerPath string, actorID int64) (*DesktopRelease, error) {
		return service.CreateFromFiles(ctx, input, filePath, installerPath, actorID)
	})
	service.uploadQueue.start()
	return service
}

func (s *DesktopUpdateService) GetStorage(ctx context.Context) (*DesktopUpdateStorageView, error) {
	cfg, err := s.loadStorage(ctx)
	if err != nil {
		return nil, err
	}
	view := DesktopUpdateStorageView{
		Backend:                   cfg.Backend,
		LocalDir:                  cfg.LocalDir,
		EffectiveLocalDir:         s.effectiveLocalDir(cfg),
		PublicBaseURL:             effectivePublicBaseURL(cfg),
		SecretAccessKeyConfigured: strings.TrimSpace(cfg.R2.SecretAccessKey) != "",
		R2:                        cfg.R2,
	}
	view.R2.SecretAccessKey = ""
	return &view, nil
}

func (s *DesktopUpdateService) UpdateStorage(ctx context.Context, input DesktopUpdateStorageConfig) (*DesktopUpdateStorageView, error) {
	current, _ := s.loadStorage(ctx)
	if strings.TrimSpace(input.R2.SecretAccessKey) == "" {
		input.R2.SecretAccessKey = current.R2.SecretAccessKey
	}
	normalized, err := normalizeDesktopUpdateStorage(input, s.defaultDir)
	if err != nil {
		return nil, infraerrors.BadRequest("DESKTOP_UPDATE_STORAGE_INVALID", err.Error())
	}
	if normalized.Backend == "local" {
		if err := os.MkdirAll(s.effectiveLocalDir(normalized), 0o750); err != nil {
			return nil, infraerrors.BadRequest("DESKTOP_UPDATE_LOCAL_DIR_UNAVAILABLE", err.Error())
		}
	}
	if normalized.R2.SecretAccessKey != "" {
		if s.encryptor == nil {
			return nil, errors.New("desktop update secret encryptor is unavailable")
		}
		normalized.R2.SecretAccessKey, err = s.encryptor.Encrypt(normalized.R2.SecretAccessKey)
		if err != nil {
			return nil, fmt.Errorf("encrypt desktop update secret: %w", err)
		}
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return nil, err
	}
	if s.settingRepo == nil {
		return nil, errors.New("desktop update setting repository is unavailable")
	}
	if err := s.settingRepo.Set(ctx, desktopUpdateStorageSettingKey, string(raw)); err != nil {
		return nil, err
	}
	return s.GetStorage(ctx)
}

// TestStorage checks that the configured storage is reachable without saving
// the supplied credentials or other settings. This keeps a transient S3/R2
// connectivity error from preventing an administrator from saving a config
// that can be corrected or tested again later.
func (s *DesktopUpdateService) TestStorage(ctx context.Context, input DesktopUpdateStorageConfig) error {
	current, _ := s.loadStorage(ctx)
	if strings.TrimSpace(input.R2.SecretAccessKey) == "" {
		input.R2.SecretAccessKey = current.R2.SecretAccessKey
	}
	normalized, err := normalizeDesktopUpdateStorage(input, s.defaultDir)
	if err != nil {
		return infraerrors.BadRequest("DESKTOP_UPDATE_STORAGE_INVALID", err.Error())
	}
	if normalized.Backend == "local" {
		if err := os.MkdirAll(s.effectiveLocalDir(normalized), 0o750); err != nil {
			return infraerrors.BadRequest("DESKTOP_UPDATE_LOCAL_DIR_UNAVAILABLE", err.Error())
		}
		return nil
	}
	if s.storeFactory == nil {
		return errors.New("desktop update object storage is unavailable")
	}
	store, err := s.storeForConfig(ctx, normalized)
	if err != nil {
		return infraerrors.BadRequest("DESKTOP_UPDATE_R2_INVALID", err.Error())
	}
	if err := store.HeadBucket(ctx); err != nil {
		return infraerrors.BadRequest("DESKTOP_UPDATE_R2_UNAVAILABLE", err.Error())
	}
	return nil
}

func (s *DesktopUpdateService) List(ctx context.Context) ([]DesktopRelease, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, version, platform, arch, status, package_filename, storage_backend, storage_key, metadata_key, package_size_bytes, sha256, sha512, release_notes, download_url, metadata_url, installer_filename, installer_storage_key, installer_size_bytes, installer_sha256, installer_sha512, installer_download_url, created_at, published_at FROM yingzo_desktop_releases WHERE deleted_at IS NULL ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]DesktopRelease, 0)
	for rows.Next() {
		item, err := scanDesktopRelease(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *DesktopUpdateService) CreateFromFile(ctx context.Context, input DesktopReleaseInput, filePath string, actorID int64) (*DesktopRelease, error) {
	return s.CreateFromFiles(ctx, input, filePath, "", actorID)
}

// CreateFromFiles stores the updater artifact and, for macOS, an optional DMG
// used for first-time installs. The updater artifact remains the ZIP package
// consumed by electron-updater.
func (s *DesktopUpdateService) CreateFromFiles(ctx context.Context, input DesktopReleaseInput, filePath, installerPath string, actorID int64) (*DesktopRelease, error) {
	input.Version = strings.TrimSpace(input.Version)
	input.Version = strings.TrimPrefix(input.Version, "v")
	input.Platform = strings.TrimSpace(input.Platform)
	input.Arch = strings.TrimSpace(input.Arch)
	input.Filename = strings.TrimSpace(input.Filename)
	input.InstallerFilename = strings.TrimSpace(input.InstallerFilename)
	if err := validateDesktopReleaseInput(input); err != nil {
		return nil, infraerrors.BadRequest("DESKTOP_UPDATE_INPUT_INVALID", err.Error())
	}
	info, err := os.Stat(filePath)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
		return nil, infraerrors.BadRequest("DESKTOP_UPDATE_PACKAGE_INVALID", "软件包为空或不可读取")
	}
	if input.Platform == "win32" && !strings.HasSuffix(strings.ToLower(input.Filename), ".exe") {
		return nil, infraerrors.BadRequest("DESKTOP_UPDATE_PACKAGE_INVALID", "Windows 更新包必须是 .exe")
	}
	if input.Platform == "darwin" && !strings.HasSuffix(strings.ToLower(input.Filename), ".zip") {
		return nil, infraerrors.BadRequest("DESKTOP_UPDATE_PACKAGE_INVALID", "macOS 自动更新包必须是 .zip")
	}
	if installerPath != "" && input.Platform == "darwin" && !strings.HasSuffix(strings.ToLower(input.InstallerFilename), ".dmg") {
		return nil, infraerrors.BadRequest("DESKTOP_UPDATE_INSTALLER_INVALID", "macOS 首次安装包必须是 .dmg")
	}
	sha256Hex, sha512Base64, err := hashFile(filePath)
	if err != nil {
		return nil, err
	}
	cfg, err := s.loadStorage(ctx)
	if err != nil {
		return nil, err
	}
	id := uuid.New()
	filename := filepath.Base(strings.ReplaceAll(input.Filename, "\\", "/"))
	storageKey := path.Join(strings.Trim(cfg.R2.Prefix, "/"), input.Version, input.Platform, input.Arch, filename)
	if cfg.Backend == "local" {
		storageKey = path.Join(input.Version, input.Platform, input.Arch, filename)
	}
	metadataKey := storageKey + ".yml"
	if err := s.storePackage(ctx, cfg, storageKey, filePath); err != nil {
		return nil, err
	}
	installerFilename := filename
	installerStorageKey := storageKey
	installerSize := info.Size()
	installerSHA256 := sha256Hex
	installerSHA512 := sha512Base64
	if installerPath != "" {
		installerInfo, statErr := os.Stat(installerPath)
		if statErr != nil || !installerInfo.Mode().IsRegular() || installerInfo.Size() <= 0 {
			_ = s.deleteObject(ctx, cfg, storageKey)
			return nil, infraerrors.BadRequest("DESKTOP_UPDATE_INSTALLER_INVALID", "首次安装包为空或不可读取")
		}
		installerFilename = filepath.Base(strings.ReplaceAll(input.InstallerFilename, "\\", "/"))
		installerSHA256, installerSHA512, err = hashFile(installerPath)
		if err != nil {
			_ = s.deleteObject(ctx, cfg, storageKey)
			return nil, err
		}
		installerStorageKey = path.Join(strings.Trim(cfg.R2.Prefix, "/"), input.Version, input.Platform, input.Arch, "installer", installerFilename)
		if cfg.Backend == "local" {
			installerStorageKey = path.Join(input.Version, input.Platform, input.Arch, "installer", installerFilename)
		}
		if err := s.storePackage(ctx, cfg, installerStorageKey, installerPath); err != nil {
			_ = s.deleteObject(ctx, cfg, storageKey)
			return nil, err
		}
		installerSize = installerInfo.Size()
	}
	base := effectivePublicBaseURL(cfg)
	if cfg.Backend == "r2" {
		// R2 packages are either served from the configured custom domain or
		// through the fixed public update service, which can issue a presigned
		// URL. The service domain is independent from the R2 object domain.
		base = desktopUpdatePublicBaseURL
	}
	downloadURL := fmt.Sprintf("%s/v1/updates/download/%s", strings.TrimRight(base, "/"), id.String())
	if cfg.Backend == "r2" && hasCustomDesktopUpdateDomain(cfg) {
		downloadURL = r2CustomDomain(cfg) + "/" + escapedDesktopUpdateKey(storageKey)
	}
	installerURL := fmt.Sprintf("%s/v1/updates/download/%s?artifact=installer", strings.TrimRight(base, "/"), id.String())
	if cfg.Backend == "r2" && hasCustomDesktopUpdateDomain(cfg) {
		installerURL = r2CustomDomain(cfg) + "/" + escapedDesktopUpdateKey(installerStorageKey)
	}
	metadataURL := fmt.Sprintf("%s/v1/updates/metadata/%s", strings.TrimRight(desktopUpdatePublicBaseURL, "/"), id.String())
	_, err = s.db.ExecContext(ctx, `INSERT INTO yingzo_desktop_releases (id,version,platform,arch,status,package_filename,storage_backend,storage_key,metadata_key,package_size_bytes,sha256,sha512,release_notes,download_url,metadata_url,installer_filename,installer_storage_key,installer_size_bytes,installer_sha256,installer_sha512,installer_download_url,created_by) VALUES ($1,$2,$3,$4,'draft',$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)`, id, input.Version, input.Platform, input.Arch, filename, cfg.Backend, storageKey, metadataKey, info.Size(), sha256Hex, sha512Base64, input.ReleaseNotes, downloadURL, metadataURL, installerFilename, installerStorageKey, installerSize, installerSHA256, installerSHA512, installerURL, actorID)
	if err != nil {
		_ = s.deleteObject(ctx, cfg, storageKey)
		if installerStorageKey != storageKey {
			_ = s.deleteObject(ctx, cfg, installerStorageKey)
		}
		return nil, err
	}
	item, err := s.getByID(ctx, id.String())
	if err != nil {
		return nil, err
	}
	metadata := generateDesktopUpdateMetadata(item)
	if err := s.storeMetadata(ctx, cfg, metadataKey, metadata); err != nil {
		_ = s.deleteObject(ctx, cfg, storageKey)
		if installerStorageKey != storageKey {
			_ = s.deleteObject(ctx, cfg, installerStorageKey)
		}
		_, _ = s.db.ExecContext(ctx, `DELETE FROM yingzo_desktop_releases WHERE id=$1`, id)
		return nil, err
	}
	return item, nil
}

func (s *DesktopUpdateService) Publish(ctx context.Context, id string, actorID int64) (*DesktopRelease, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return nil, infraerrors.BadRequest("DESKTOP_UPDATE_ID_INVALID", "版本 ID 无效")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var platform, arch, status string
	if err := tx.QueryRowContext(ctx, `SELECT platform, arch, status FROM yingzo_desktop_releases WHERE id=$1 AND deleted_at IS NULL`, parsed).Scan(&platform, &arch, &status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, infraerrors.NotFound("DESKTOP_UPDATE_NOT_FOUND", "版本不存在")
		}
		return nil, err
	}
	if status == "published" {
		return s.getByID(ctx, id)
	}
	if status != "draft" && status != "superseded" {
		return nil, infraerrors.Conflict("DESKTOP_UPDATE_NOT_PUBLISHABLE", "当前版本状态不能发布")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE yingzo_desktop_releases SET status='superseded', updated_at=NOW() WHERE platform=$1 AND arch=$2 AND status='published' AND deleted_at IS NULL`, platform, arch); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE yingzo_desktop_releases SET status='published', published_by=$2, published_at=NOW(), updated_at=NOW() WHERE id=$1`, parsed, actorID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.getByID(ctx, id)
}

func (s *DesktopUpdateService) Delete(ctx context.Context, id string) error {
	item, err := s.getByID(ctx, id)
	if err != nil {
		return err
	}
	if item.Status == "published" {
		return infraerrors.Conflict("DESKTOP_UPDATE_PUBLISHED", "已发布版本必须先被新版本替代")
	}
	cfg, err := s.loadStorage(ctx)
	if err != nil {
		return err
	}
	if err := s.deleteObject(ctx, cfg, item.StorageKey); err != nil {
		return err
	}
	if err := s.deleteObject(ctx, cfg, item.MetadataKey); err != nil {
		return err
	}
	if item.InstallerStorageKey != "" && item.InstallerStorageKey != item.StorageKey {
		if err := s.deleteObject(ctx, cfg, item.InstallerStorageKey); err != nil {
			return err
		}
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM yingzo_desktop_releases WHERE id=$1`, id)
	return err
}

func (s *DesktopUpdateService) Check(ctx context.Context, currentVersion, platform, arch string) (DesktopUpdateCheck, error) {
	if platform != "win32" && platform != "darwin" {
		return DesktopUpdateCheck{}, infraerrors.BadRequest("DESKTOP_UPDATE_PLATFORM_INVALID", "不支持的平台")
	}
	if arch != "" && arch != "x64" && arch != "arm64" && arch != "universal" {
		return DesktopUpdateCheck{}, infraerrors.BadRequest("DESKTOP_UPDATE_ARCH_INVALID", "不支持的 CPU 架构")
	}
	if platform == "win32" && arch != "" && arch != "x64" {
		return DesktopUpdateCheck{}, infraerrors.BadRequest("DESKTOP_UPDATE_ARCH_INVALID", "Windows 更新包仅支持 x64")
	}
	item, err := s.latest(ctx, platform, arch)
	if err != nil {
		return DesktopUpdateCheck{}, err
	}
	if item == nil || !versionGreater(item.Version, currentVersion) {
		return DesktopUpdateCheck{Update: false}, nil
	}
	if err := s.refreshReleaseDownloadURL(ctx, item); err != nil {
		return DesktopUpdateCheck{}, err
	}
	return DesktopUpdateCheck{Update: true, Version: item.Version, Notes: item.ReleaseNotes, DownloadURL: item.DownloadURL, SHA256: item.SHA256, SizeBytes: item.PackageSize, InstallerURL: item.InstallerURL, InstallerSizeBytes: item.InstallerSize}, nil
}

func (s *DesktopUpdateService) Metadata(ctx context.Context, platform, arch string) ([]byte, error) {
	item, err := s.latest(ctx, platform, arch)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, infraerrors.NotFound("DESKTOP_UPDATE_NOT_FOUND", "没有已发布的升级包")
	}
	if err := s.refreshReleaseDownloadURL(ctx, item); err != nil {
		return nil, err
	}
	return generateDesktopUpdateMetadata(item), nil
}

func (s *DesktopUpdateService) MetadataByID(ctx context.Context, id string) ([]byte, error) {
	item, err := s.getByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if item.Status != "published" {
		return nil, infraerrors.NotFound("DESKTOP_UPDATE_NOT_FOUND", "没有已发布的升级包")
	}
	if err := s.refreshReleaseDownloadURL(ctx, item); err != nil {
		return nil, err
	}
	return generateDesktopUpdateMetadata(item), nil
}

func (s *DesktopUpdateService) LocalPackagePath(ctx context.Context, id string, artifact ...string) (string, bool, error) {
	item, err := s.getByID(ctx, id)
	if err != nil {
		return "", false, err
	}
	if item.Status != "published" {
		return "", false, infraerrors.NotFound("DESKTOP_UPDATE_NOT_FOUND", "没有已发布的升级包")
	}
	if item.StorageBackend != "local" {
		return "", false, nil
	}
	cfg, err := s.loadStorage(ctx)
	if err != nil {
		return "", false, err
	}
	key, _, err := desktopReleaseArtifact(item, artifact...)
	if err != nil {
		return "", false, err
	}
	dest, err := safeDesktopUpdatePath(s.effectiveLocalDir(cfg), key)
	if err != nil {
		return "", false, fmt.Errorf("desktop update path invalid: %w", err)
	}
	return dest, true, nil
}

func (s *DesktopUpdateService) RedirectURL(ctx context.Context, id string, artifact ...string) (string, error) {
	item, err := s.getByID(ctx, id)
	if err != nil {
		return "", err
	}
	if item.Status != "published" {
		return "", infraerrors.NotFound("DESKTOP_UPDATE_NOT_FOUND", "没有已发布的升级包")
	}
	key, _, err := desktopReleaseArtifact(item, artifact...)
	if err != nil {
		return "", err
	}
	if item.StorageBackend == "r2" {
		cfg, err := s.loadStorage(ctx)
		if err != nil {
			return "", err
		}
		if hasCustomDesktopUpdateDomain(cfg) {
			return r2CustomDomain(cfg) + "/" + escapedDesktopUpdateKey(key), nil
		}
		store, err := s.storeForConfig(ctx, cfg)
		if err != nil {
			return "", err
		}
		return store.PresignURL(ctx, key, 15*time.Minute)
	}
	return "", nil
}

func (s *DesktopUpdateService) refreshReleaseDownloadURL(ctx context.Context, item *DesktopRelease) error {
	if item == nil || item.StorageBackend != "r2" {
		return nil
	}
	cfg, err := s.loadStorage(ctx)
	if err != nil {
		return err
	}
	if hasCustomDesktopUpdateDomain(cfg) {
		item.DownloadURL = r2CustomDomain(cfg) + "/" + escapedDesktopUpdateKey(item.StorageKey)
		if item.InstallerStorageKey != "" {
			item.InstallerURL = r2CustomDomain(cfg) + "/" + escapedDesktopUpdateKey(item.InstallerStorageKey)
		}
	} else {
		item.DownloadURL = fmt.Sprintf("%s/v1/updates/download/%s", desktopUpdatePublicBaseURL, item.ID)
		if item.InstallerStorageKey != "" {
			item.InstallerURL = fmt.Sprintf("%s/v1/updates/download/%s?artifact=installer", desktopUpdatePublicBaseURL, item.ID)
		}
	}
	return nil
}

func (s *DesktopUpdateService) OpenPackage(ctx context.Context, id string, artifact ...string) (io.ReadCloser, *DesktopRelease, error) {
	item, err := s.getByID(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if item.Status != "published" {
		return nil, nil, infraerrors.NotFound("DESKTOP_UPDATE_NOT_FOUND", "没有已发布的升级包")
	}
	key, filename, err := desktopReleaseArtifact(item, artifact...)
	if err != nil {
		return nil, nil, err
	}
	if item.StorageBackend == "local" {
		cfg, err := s.loadStorage(ctx)
		if err != nil {
			return nil, nil, err
		}
		packagePath, err := safeDesktopUpdatePath(s.effectiveLocalDir(cfg), key)
		if err != nil {
			return nil, nil, fmt.Errorf("desktop update path invalid: %w", err)
		}
		file, err := os.Open(packagePath)
		if err != nil {
			return nil, nil, err
		}
		item.PackageFilename = filename
		return file, item, nil
	}
	cfg, err := s.loadStorage(ctx)
	if err != nil {
		return nil, nil, err
	}
	store, err := s.storeForConfig(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	body, err := store.Download(ctx, key)
	if err != nil {
		return nil, nil, err
	}
	item.PackageFilename = filename
	return body, item, nil
}

func desktopReleaseArtifact(item *DesktopRelease, artifact ...string) (string, string, error) {
	if item == nil {
		return "", "", infraerrors.NotFound("DESKTOP_UPDATE_NOT_FOUND", "没有已发布的升级包")
	}
	if len(artifact) > 0 && strings.TrimSpace(artifact[0]) == "installer" {
		if item.InstallerStorageKey == "" || item.InstallerFilename == "" {
			return "", "", infraerrors.NotFound("DESKTOP_UPDATE_INSTALLER_NOT_FOUND", "没有可用的首次安装包")
		}
		return item.InstallerStorageKey, item.InstallerFilename, nil
	}
	return item.StorageKey, item.PackageFilename, nil
}

type desktopReleaseScanner interface{ Scan(dest ...any) error }

func scanDesktopRelease(scanner desktopReleaseScanner) (DesktopRelease, error) {
	var item DesktopRelease
	var published sql.NullTime
	var idText string
	if err := scanner.Scan(&idText, &item.Version, &item.Platform, &item.Arch, &item.Status, &item.PackageFilename, &item.StorageBackend, &item.StorageKey, &item.MetadataKey, &item.PackageSize, &item.SHA256, &item.SHA512, &item.ReleaseNotes, &item.DownloadURL, &item.MetadataURL, &item.InstallerFilename, &item.InstallerStorageKey, &item.InstallerSize, &item.InstallerSHA256, &item.InstallerSHA512, &item.InstallerURL, &item.CreatedAt, &published); err != nil {
		return item, err
	}
	item.ID = idText
	if published.Valid {
		t := published.Time
		item.PublishedAt = &t
	}
	return item, nil
}

func (s *DesktopUpdateService) getByID(ctx context.Context, id string) (*DesktopRelease, error) {
	parsed, err := uuid.Parse(strings.TrimSpace(id))
	if err != nil {
		return nil, infraerrors.BadRequest("DESKTOP_UPDATE_ID_INVALID", "版本 ID 无效")
	}
	row := s.db.QueryRowContext(ctx, `SELECT id, version, platform, arch, status, package_filename, storage_backend, storage_key, metadata_key, package_size_bytes, sha256, sha512, release_notes, download_url, metadata_url, installer_filename, installer_storage_key, installer_size_bytes, installer_sha256, installer_sha512, installer_download_url, created_at, published_at FROM yingzo_desktop_releases WHERE id=$1 AND deleted_at IS NULL`, parsed)
	item, err := scanDesktopRelease(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, infraerrors.NotFound("DESKTOP_UPDATE_NOT_FOUND", "版本不存在")
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *DesktopUpdateService) latest(ctx context.Context, platform, arch string) (*DesktopRelease, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, version, platform, arch, status, package_filename, storage_backend, storage_key, metadata_key, package_size_bytes, sha256, sha512, release_notes, download_url, metadata_url, installer_filename, installer_storage_key, installer_size_bytes, installer_sha256, installer_sha512, installer_download_url, created_at, published_at FROM yingzo_desktop_releases WHERE platform=$1 AND status='published' AND deleted_at IS NULL AND ($2='' OR arch=$2 OR arch='universal') ORDER BY CASE WHEN $2<>'' AND arch=$2 THEN 0 WHEN arch='universal' THEN 1 ELSE 2 END, published_at DESC`, platform, arch)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var best *DesktopRelease
	for rows.Next() {
		item, err := scanDesktopRelease(rows)
		if err != nil {
			return nil, err
		}
		if best == nil || versionGreater(item.Version, best.Version) || (item.Version == best.Version && ((arch != "" && item.Arch == arch && best.Arch != arch) || (arch == "" && item.Arch == "universal" && best.Arch != "universal"))) {
			candidate := item
			best = &candidate
		}
	}
	return best, rows.Err()
}

func (s *DesktopUpdateService) loadStorage(ctx context.Context) (DesktopUpdateStorageConfig, error) {
	cfg := DesktopUpdateStorageConfig{Backend: "local", LocalDir: s.defaultDir, PublicBaseURL: desktopUpdatePublicBaseURL, R2: DesktopUpdateR2Config{Region: "auto", Prefix: desktopUpdateDefaultPrefix}}
	if s.settingRepo == nil {
		return cfg, nil
	}
	raw, err := s.settingRepo.GetValue(ctx, desktopUpdateStorageSettingKey)
	if err != nil || strings.TrimSpace(raw) == "" {
		return cfg, nil
	}
	var stored DesktopUpdateStorageConfig
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return cfg, err
	}
	if stored.R2.SecretAccessKey != "" && s.encryptor != nil {
		stored.R2.SecretAccessKey, err = s.encryptor.Decrypt(stored.R2.SecretAccessKey)
		if err != nil {
			return cfg, err
		}
	}
	// Older configurations used public_base_url for the R2 custom domain.
	// Move that value to the explicit R2 field in memory so existing setups
	// continue to serve their objects while the next save persists the split.
	if stored.Backend == "r2" && strings.TrimSpace(stored.R2.CustomDomain) == "" && isNonDefaultDesktopUpdatePublicBaseURL(stored.PublicBaseURL) {
		stored.R2.CustomDomain = stored.PublicBaseURL
		stored.PublicBaseURL = desktopUpdatePublicBaseURL
	}
	return normalizeDesktopUpdateStorage(stored, s.defaultDir)
}

func normalizeDesktopUpdateStorage(cfg DesktopUpdateStorageConfig, defaultDir string) (DesktopUpdateStorageConfig, error) {
	cfg.Backend = strings.ToLower(strings.TrimSpace(cfg.Backend))
	if cfg.Backend == "" {
		cfg.Backend = "local"
	}
	if cfg.Backend != "local" && cfg.Backend != "r2" {
		return cfg, errors.New("backend must be local or r2")
	}
	if strings.TrimSpace(cfg.LocalDir) == "" {
		cfg.LocalDir = defaultDir
	}
	if !filepath.IsAbs(cfg.LocalDir) {
		return cfg, errors.New("local_dir must be an absolute path")
	}
	if filepath.Clean(cfg.LocalDir) == string(filepath.Separator) {
		return cfg, errors.New("local_dir cannot be the filesystem root")
	}
	if strings.TrimSpace(cfg.PublicBaseURL) == "" {
		cfg.PublicBaseURL = desktopUpdatePublicBaseURL
	}
	cfg.PublicBaseURL = strings.TrimRight(strings.TrimSpace(cfg.PublicBaseURL), "/")
	parsedPublicURL, err := url.Parse(cfg.PublicBaseURL)
	if err != nil || parsedPublicURL.Host == "" || (parsedPublicURL.Scheme != "http" && parsedPublicURL.Scheme != "https") {
		return cfg, errors.New("public_base_url must be an http or https URL")
	}
	cfg.R2.Region = strings.TrimSpace(cfg.R2.Region)
	if cfg.R2.Region == "" {
		cfg.R2.Region = "auto"
	}
	cfg.R2.Prefix = strings.Trim(strings.TrimSpace(cfg.R2.Prefix), "/")
	if cfg.R2.Prefix == "" {
		cfg.R2.Prefix = desktopUpdateDefaultPrefix
	}
	cfg.R2.CustomDomain = strings.TrimRight(strings.TrimSpace(cfg.R2.CustomDomain), "/")
	if cfg.R2.CustomDomain != "" {
		parsedCustomDomain, err := url.Parse(cfg.R2.CustomDomain)
		if err != nil || parsedCustomDomain.Host == "" || (parsedCustomDomain.Scheme != "http" && parsedCustomDomain.Scheme != "https") || parsedCustomDomain.RawQuery != "" || parsedCustomDomain.Fragment != "" {
			return cfg, errors.New("r2.custom_domain must be an http or https URL without query or fragment")
		}
	}
	if cfg.Backend == "r2" && (cfg.R2.Endpoint == "" || cfg.R2.Bucket == "" || cfg.R2.AccessKeyID == "" || cfg.R2.SecretAccessKey == "") {
		return cfg, errors.New("R2 endpoint, bucket, access key and secret key are required")
	}
	return cfg, nil
}

func (s *DesktopUpdateService) effectiveLocalDir(cfg DesktopUpdateStorageConfig) string {
	if strings.TrimSpace(cfg.LocalDir) != "" {
		return cfg.LocalDir
	}
	return s.defaultDir
}
func effectivePublicBaseURL(cfg DesktopUpdateStorageConfig) string {
	if strings.TrimSpace(cfg.PublicBaseURL) != "" {
		return cfg.PublicBaseURL
	}
	return desktopUpdatePublicBaseURL
}

func r2CustomDomain(cfg DesktopUpdateStorageConfig) string {
	return strings.TrimRight(strings.TrimSpace(cfg.R2.CustomDomain), "/")
}

func hasCustomDesktopUpdateDomain(cfg DesktopUpdateStorageConfig) bool {
	return r2CustomDomain(cfg) != ""
}

func isNonDefaultDesktopUpdatePublicBaseURL(value string) bool {
	base := strings.TrimRight(strings.TrimSpace(value), "/")
	return base != "" && base != desktopUpdatePublicBaseURL
}

func escapedDesktopUpdateKey(key string) string {
	if err := validateDesktopUpdateKey(key); err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(key, "/"), "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

func (s *DesktopUpdateService) storeForConfig(ctx context.Context, cfg DesktopUpdateStorageConfig) (BackupObjectStore, error) {
	if s.storeFactory == nil {
		return nil, errors.New("object storage is unavailable")
	}
	return s.storeFactory(ctx, &BackupS3Config{Endpoint: cfg.R2.Endpoint, Region: cfg.R2.Region, Bucket: cfg.R2.Bucket, AccessKeyID: cfg.R2.AccessKeyID, SecretAccessKey: cfg.R2.SecretAccessKey, Prefix: cfg.R2.Prefix, ForcePathStyle: cfg.R2.ForcePathStyle})
}

func (s *DesktopUpdateService) storePackage(ctx context.Context, cfg DesktopUpdateStorageConfig, key, filePath string) error {
	if cfg.Backend == "r2" {
		store, err := s.storeForConfig(ctx, cfg)
		if err != nil {
			return err
		}
		_, err = store.UploadFile(ctx, key, filePath, "application/octet-stream")
		return err
	}
	dest, err := safeDesktopUpdatePath(s.effectiveLocalDir(cfg), key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return err
	}
	in, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	_, err = io.Copy(out, in)
	return err
}

func (s *DesktopUpdateService) storeMetadata(ctx context.Context, cfg DesktopUpdateStorageConfig, key string, data []byte) error {
	if cfg.Backend == "r2" {
		store, err := s.storeForConfig(ctx, cfg)
		if err != nil {
			return err
		}
		_, err = store.Upload(ctx, key, strings.NewReader(string(data)), "text/yaml; charset=utf-8")
		return err
	}
	dest, err := safeDesktopUpdatePath(s.effectiveLocalDir(cfg), key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return err
	}
	return os.WriteFile(dest, data, 0o640)
}

func (s *DesktopUpdateService) deleteObject(ctx context.Context, cfg DesktopUpdateStorageConfig, key string) error {
	if cfg.Backend == "r2" {
		store, err := s.storeForConfig(ctx, cfg)
		if err != nil {
			return err
		}
		return store.Delete(ctx, key)
	}
	dest, err := safeDesktopUpdatePath(s.effectiveLocalDir(cfg), key)
	if err != nil {
		return err
	}
	err = os.Remove(dest)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func validateDesktopReleaseInput(input DesktopReleaseInput) error {
	version := strings.TrimSpace(input.Version)
	if !semver.IsValid("v" + strings.TrimPrefix(version, "v")) {
		return errors.New("version must be a valid semver")
	}
	if input.Platform != "win32" && input.Platform != "darwin" {
		return errors.New("platform must be win32 or darwin")
	}
	if input.Arch != "x64" && input.Arch != "arm64" && input.Arch != "universal" {
		return errors.New("arch must be x64, arm64 or universal")
	}
	if input.Platform == "win32" && input.Arch != "x64" {
		return errors.New("windows currently supports x64 only")
	}
	if strings.TrimSpace(input.Filename) == "" {
		return errors.New("package filename is required")
	}
	if input.Platform == "darwin" && input.Arch == "universal" {
		return nil
	}
	return nil
}

func safeDesktopUpdatePath(root, key string) (string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if err := validateDesktopUpdateKey(key); err != nil {
		return "", err
	}
	cleanKey := filepath.Clean(filepath.FromSlash(key))
	if cleanKey == "." || cleanKey == string(filepath.Separator) || filepath.IsAbs(cleanKey) {
		return "", errors.New("storage key escapes update directory")
	}
	dest := filepath.Join(rootAbs, cleanKey)
	if dest != rootAbs && !strings.HasPrefix(dest, rootAbs+string(filepath.Separator)) {
		return "", errors.New("storage key escapes update directory")
	}
	return dest, nil
}

func validateDesktopUpdateKey(key string) error {
	normalized := strings.ReplaceAll(strings.TrimSpace(key), "\\", "/")
	if normalized == "" || strings.HasPrefix(normalized, "/") {
		return errors.New("storage key must be relative")
	}
	for _, segment := range strings.Split(normalized, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return errors.New("storage key contains an unsafe path segment")
		}
	}
	return nil
}

func versionGreater(candidate, current string) bool {
	candidate = "v" + strings.TrimPrefix(strings.TrimSpace(candidate), "v")
	current = "v" + strings.TrimPrefix(strings.TrimSpace(current), "v")
	return semver.IsValid(candidate) && semver.IsValid(current) && semver.Compare(candidate, current) > 0
}

func hashFile(filePath string) (string, string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = f.Close() }()
	h1, h2 := sha256.New(), sha512.New()
	if _, err := io.Copy(io.MultiWriter(h1, h2), f); err != nil {
		return "", "", err
	}
	return hex.EncodeToString(h1.Sum(nil)), base64.StdEncoding.EncodeToString(h2.Sum(nil)), nil
}

func generateDesktopUpdateMetadata(item *DesktopRelease) []byte {
	if item == nil {
		return nil
	}
	return []byte("version: " + strconv.Quote(item.Version) + "\n" +
		"files:\n  - url: " + strconv.Quote(item.DownloadURL) + "\n    sha512: " + strconv.Quote(item.SHA512) + "\n    size: " + strconv.FormatInt(item.PackageSize, 10) + "\n" +
		"path: " + strconv.Quote(item.PackageFilename) + "\n" +
		"sha512: " + strconv.Quote(item.SHA512) + "\n" +
		"releaseNotes: " + strconv.Quote(item.ReleaseNotes) + "\n" +
		"releaseDate: " + strconv.Quote(item.CreatedAt.UTC().Format(time.RFC3339)) + "\n")
}
