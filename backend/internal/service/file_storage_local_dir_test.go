package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// localDirSettingRepo 只提供素材库配置读写，其余方法不应被调用。
type localDirSettingRepo struct {
	SettingRepository
	value string
}

func (r *localDirSettingRepo) GetValue(context.Context, string) (string, error) {
	return r.value, nil
}

func (r *localDirSettingRepo) Set(_ context.Context, _ string, value string) error {
	r.value = value
	return nil
}

func newLocalDirStorage(t *testing.T, dataDir, configured string) (*FileStorageService, *localDirSettingRepo) {
	t.Helper()
	repo := &localDirSettingRepo{}
	if configured != "" {
		payload, err := json.Marshal(FileStorageConfig{
			SchemaVersion:  1,
			Backend:        "local",
			LocalDir:       configured,
			RetentionHours: 24,
			DailyMaxCount:  100,
			DailyMaxBytes:  1 << 30,
		})
		require.NoError(t, err)
		repo.value = string(payload)
	}
	return NewFileStorageService(nil, repo, nil, nil, &config.Config{
		Pricing: config.PricingConfig{DataDir: dataDir},
	}), repo
}

// 本地素材目录必须是绝对路径，且不能指向系统目录。
func TestNormalizeFileStorageLocalDir(t *testing.T) {
	for _, tc := range []struct {
		name    string
		raw     string
		want    string
		wantErr string
	}{
		{name: "absolute path", raw: "/data/yingzo-assets", want: "/data/yingzo-assets"},
		{name: "trailing slash is cleaned", raw: "/data/yingzo-assets/", want: "/data/yingzo-assets"},
		{name: "inner dot segments are cleaned", raw: "/data/./assets/../agent-assets", want: "/data/agent-assets"},
		{name: "surrounding spaces are trimmed", raw: "  /data/assets  ", want: "/data/assets"},
		{name: "relative path is rejected", raw: "data/agent-assets", wantErr: "absolute"},
		{name: "parent relative path is rejected", raw: "../assets", wantErr: "absolute"},
		{name: "filesystem root is rejected", raw: "/", wantErr: "filesystem root"},
		{name: "system directory is rejected", raw: "/etc", wantErr: "system directory"},
		{name: "system directory below root is rejected", raw: "/usr/", wantErr: "system directory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeFileStorageLocalDir(tc.raw)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

// 没配置 local_dir 时用默认目录；配置了就用配置值，并按需创建。
func TestEffectiveLocalPathUsesConfiguredDirectory(t *testing.T) {
	base := t.TempDir()
	defaultDir := filepath.Join(base, "data", "agent-assets")
	configured := filepath.Join(base, "host-mounted", "agent-assets")

	service, _ := newLocalDirStorage(t, filepath.Join(base, "data"), "")
	require.Equal(t, defaultDir, service.LocalPath())
	got, err := service.EffectiveLocalPath(context.Background())
	require.NoError(t, err)
	require.Equal(t, defaultDir, got)
	require.DirExists(t, got, "默认目录应被按需创建")

	// 配置的目录必须已经存在：服务不替管理员创建宿主机目录。
	service, _ = newLocalDirStorage(t, filepath.Join(base, "data"), configured)
	_, err = service.EffectiveLocalPath(context.Background())
	require.ErrorContains(t, err, "does not exist")
	require.NoDirExists(t, configured, "服务不应创建配置的目录")

	require.NoError(t, os.MkdirAll(configured, 0o700))
	got, err = service.EffectiveLocalPath(context.Background())
	require.NoError(t, err)
	require.Equal(t, configured, got)
}

// 配置的目录不存在时必须明确报错并指向正确做法，而不是替用户创建（自动创建会在宿主机
// 目录没挂进来时把素材写进容器可写层，容器一重建就丢）。
func TestUpdateSettingsRejectsMissingLocalDir(t *testing.T) {
	base := t.TempDir()
	missing := filepath.Join(base, "not-created-yet")

	service, repo := newLocalDirStorage(t, filepath.Join(base, "data"), "")
	_, err := service.UpdateSettings(context.Background(), FileStorageConfig{
		SchemaVersion:  1,
		Backend:        "local",
		LocalDir:       missing,
		RetentionHours: 24,
		DailyMaxCount:  100,
		DailyMaxBytes:  1 << 20,
	})
	require.ErrorContains(t, err, "does not exist")
	require.NoDirExists(t, missing)
	require.NotContains(t, repo.value, missing, "失败的配置不应被持久化")

	// 存在但指向文件而不是目录，同样拒绝。
	filePath := filepath.Join(base, "a-file")
	require.NoError(t, os.WriteFile(filePath, []byte("x"), 0o600))
	_, err = service.UpdateSettings(context.Background(), FileStorageConfig{
		SchemaVersion:  1,
		Backend:        "local",
		LocalDir:       filePath,
		RetentionHours: 24,
		DailyMaxCount:  100,
		DailyMaxBytes:  1 << 20,
	})
	require.ErrorContains(t, err, "not a directory")
}

// 保存设置时必须真实验证目录可写：宿主机目录没挂进来 / 权限不对要当场报错，
// 而不是等用户上传素材时才失败。
func TestUpdateSettingsRejectsUnwritableLocalDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("以 root 运行时权限位不生效")
	}
	base := t.TempDir()
	readOnly := filepath.Join(base, "readonly")
	require.NoError(t, os.Mkdir(readOnly, 0o500))

	service, _ := newLocalDirStorage(t, base, "")
	_, err := service.UpdateSettings(context.Background(), FileStorageConfig{
		SchemaVersion:  1,
		Backend:        "local",
		LocalDir:       readOnly,
		RetentionHours: 24,
		DailyMaxCount:  100,
		DailyMaxBytes:  1 << 20,
	})
	require.ErrorContains(t, err, "local_dir")
}

// S3 后端下本地目录是中转区，同样必须可写——否则生成完才发现发布失败。
func TestUpdateSettingsVerifiesLocalDirForS3Backend(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("以 root 运行时权限位不生效")
	}
	base := t.TempDir()
	readOnly := filepath.Join(base, "readonly-s3")
	require.NoError(t, os.Mkdir(readOnly, 0o500))

	service, _ := newLocalDirStorage(t, base, "")
	_, err := service.UpdateSettings(context.Background(), FileStorageConfig{
		SchemaVersion:  1,
		Backend:        "s3",
		LocalDir:       readOnly,
		RetentionHours: 24,
		DailyMaxCount:  100,
		DailyMaxBytes:  1 << 20,
		S3:             BackupS3Config{Bucket: "b", AccessKeyID: "k", SecretAccessKey: "s", Region: "auto", Prefix: "model-assets/"},
	})
	require.ErrorContains(t, err, "local_dir")
}

// 保存一个合法的宿主机目录后，设置页报告的就是它，且立刻生效。
func TestUpdateSettingsAcceptsHostMountedLocalDir(t *testing.T) {
	base := t.TempDir()
	configured := filepath.Join(base, "mnt", "agent-assets")
	// 管理员先在宿主机上建好目录（Docker 部署下这就是挂载点）。
	require.NoError(t, os.MkdirAll(configured, 0o700))

	service, repo := newLocalDirStorage(t, filepath.Join(base, "data"), "")
	settings, err := service.UpdateSettings(context.Background(), FileStorageConfig{
		SchemaVersion:  1,
		Backend:        "local",
		LocalDir:       configured,
		RetentionHours: 24,
		DailyMaxCount:  100,
		DailyMaxBytes:  1 << 20,
	})
	require.NoError(t, err)
	require.Equal(t, configured, settings.LocalPath, "设置页应显示生效目录")
	require.Contains(t, repo.value, configured, "配置应被持久化")

	got, err := service.EffectiveLocalPath(context.Background())
	require.NoError(t, err)
	require.Equal(t, configured, got)
}

// local_dir 也支持环境变量配置，供脚本/Compose 部署使用。
func TestFileStorageLocalDirFromEnvironment(t *testing.T) {
	t.Setenv("FILE_SERVICE_LOCAL_DIR", "/mnt/host/agent-assets")
	cfg, _ := fileStorageConfigFromEnvironment()
	require.Equal(t, "/mnt/host/agent-assets", cfg.LocalDir)
}

// 部署时用 AGENT_ASSETS_HOST_DIR 把宿主目录按同路径挂进容器：它必须成为默认素材目录，
// 否则面板留空时会写进容器可写层，容器一重建素材就没了。
func TestDefaultLocalPathHonorsMountedHostDir(t *testing.T) {
	base := t.TempDir()
	mounted := filepath.Join(base, "mounted-assets")
	require.NoError(t, os.MkdirAll(mounted, 0o700))
	t.Setenv("AGENT_ASSETS_HOST_DIR", mounted)

	service, _ := newLocalDirStorage(t, filepath.Join(base, "data"), "")
	require.Equal(t, mounted, service.LocalPath(), "默认目录就是挂进来的宿主目录")

	got, err := service.EffectiveLocalPath(context.Background())
	require.NoError(t, err)
	require.Equal(t, mounted, got)

	// 相对路径不是合法的部署注入值，忽略它并回落到数据目录。
	t.Setenv("AGENT_ASSETS_HOST_DIR", "relative/path")
	service, _ = newLocalDirStorage(t, filepath.Join(base, "data"), "")
	require.Equal(t, filepath.Join(base, "data", "agent-assets"), service.LocalPath())
}
