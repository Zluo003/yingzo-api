package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

// DesktopUploadChunkSize is deliberately small enough that a slow chunk upload
// finishes before a reverse proxy's write timeout. The total package transfer
// is asynchronous and never occupies the original HTTP request.
const DesktopUploadChunkSize int64 = 256 << 10

const (
	desktopUploadMaxFileSize = 2 << 30
	desktopUploadRetention   = 7 * 24 * time.Hour
	desktopUploadQueueLimit  = 8
)

type DesktopUploadInput struct {
	Version           string `json:"version"`
	Platform          string `json:"platform"`
	Arch              string `json:"arch"`
	ReleaseNotes      string `json:"release_notes"`
	Filename          string `json:"filename"`
	InstallerFilename string `json:"installer_filename"`
	PackageSize       int64  `json:"package_size"`
	InstallerSize     int64  `json:"installer_size"`
}

func (input DesktopUploadInput) releaseInput() DesktopReleaseInput {
	return DesktopReleaseInput{
		Version:           input.Version,
		Platform:          input.Platform,
		Arch:              input.Arch,
		ReleaseNotes:      input.ReleaseNotes,
		Filename:          input.Filename,
		InstallerFilename: input.InstallerFilename,
	}
}

type DesktopUpload struct {
	ID                string    `json:"id"`
	Version           string    `json:"version"`
	Platform          string    `json:"platform"`
	Arch              string    `json:"arch"`
	ReleaseNotes      string    `json:"release_notes"`
	Filename          string    `json:"filename"`
	InstallerFilename string    `json:"installer_filename,omitempty"`
	PackageSize       int64     `json:"package_size"`
	InstallerSize     int64     `json:"installer_size"`
	Status            string    `json:"status"`
	PackageReceived   int64     `json:"package_received"`
	InstallerReceived int64     `json:"installer_received"`
	Error             string    `json:"error,omitempty"`
	ReleaseID         string    `json:"release_id,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	ActorID           int64     `json:"-"`
	ChunkSize         int64     `json:"chunk_size"`
}

type desktopUploadJob struct {
	DesktopUpload
	ActorID    int64 `json:"actor_id"`
	RetryCount int   `json:"retry_count"`
}

type desktopUploadProcessor func(context.Context, DesktopReleaseInput, string, string, int64) (*DesktopRelease, error)

type DesktopUploadQueue struct {
	root    string
	process desktopUploadProcessor
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	wake    chan struct{}
	done    chan struct{}
	once    sync.Once
}

func newDesktopUploadQueue(root string, process desktopUploadProcessor) *DesktopUploadQueue {
	ctx, cancel := context.WithCancel(context.Background())
	return &DesktopUploadQueue{
		root: root, process: process, ctx: ctx, cancel: cancel,
		wake: make(chan struct{}, 1), done: make(chan struct{}),
	}
}

func (q *DesktopUploadQueue) start() { q.once.Do(func() { go q.run() }) }

func (q *DesktopUploadQueue) stop() {
	q.cancel()
	q.start()
	<-q.done
}

func (q *DesktopUploadQueue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *DesktopUploadQueue) dir(id string) (string, error) {
	parsed, err := uuid.Parse(id)
	if err != nil || parsed.String() != id {
		return "", infraerrors.BadRequest("DESKTOP_UPLOAD_ID_INVALID", "上传任务 ID 无效")
	}
	return filepath.Join(q.root, id), nil
}

func (q *DesktopUploadQueue) read(id string) (*desktopUploadJob, error) {
	dir, err := q.dir(id)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, "job.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, infraerrors.NotFound("DESKTOP_UPLOAD_NOT_FOUND", "上传任务不存在或已过期，请重新上传")
	}
	if err != nil {
		return nil, err
	}
	var job desktopUploadJob
	if err := json.Unmarshal(raw, &job); err != nil {
		return nil, err
	}
	if job.ID != id {
		return nil, errors.New("upload manifest ID mismatch")
	}
	return &job, nil
}

func (q *DesktopUploadQueue) save(job *desktopUploadJob) error {
	dir, err := q.dir(job.ID)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(job)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "job.json")
	tmp := filepath.Join(dir, "job.tmp")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(raw); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (q *DesktopUploadQueue) list() ([]desktopUploadJob, error) {
	entries, err := os.ReadDir(q.root)
	if errors.Is(err, os.ErrNotExist) {
		return []desktopUploadJob{}, nil
	}
	if err != nil {
		return nil, err
	}
	jobs := make([]desktopUploadJob, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		job, err := q.read(entry.Name())
		if err != nil {
			continue
		}
		jobs = append(jobs, *job)
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].CreatedAt.Before(jobs[j].CreatedAt) })
	return jobs, nil
}

func uploadView(job *desktopUploadJob) *DesktopUpload {
	if job == nil {
		return nil
	}
	view := job.DesktopUpload
	return &view
}

func (job *desktopUploadJob) releaseInput() DesktopReleaseInput {
	return DesktopReleaseInput{
		Version:           job.Version,
		Platform:          job.Platform,
		Arch:              job.Arch,
		ReleaseNotes:      job.ReleaseNotes,
		Filename:          job.Filename,
		InstallerFilename: job.InstallerFilename,
	}
}

func (q *DesktopUploadQueue) List() ([]DesktopUpload, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	jobs, err := q.list()
	if err != nil {
		return nil, err
	}
	items := make([]DesktopUpload, 0, len(jobs))
	for i := range jobs {
		items = append(items, jobs[i].DesktopUpload)
	}
	return items, nil
}

func (q *DesktopUploadQueue) Get(id string) (*DesktopUpload, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	job, err := q.read(id)
	if err != nil {
		return nil, err
	}
	return uploadView(job), nil
}

func (q *DesktopUploadQueue) Create(input DesktopUploadInput, actorID int64) (*DesktopUpload, error) {
	input.Version = strings.TrimPrefix(strings.TrimSpace(input.Version), "v")
	input.Platform = strings.TrimSpace(input.Platform)
	input.Arch = strings.TrimSpace(input.Arch)
	input.Filename = strings.TrimSpace(input.Filename)
	input.InstallerFilename = strings.TrimSpace(input.InstallerFilename)
	releaseInput := input.releaseInput()
	if err := validateDesktopReleaseInput(releaseInput); err != nil {
		return nil, infraerrors.BadRequest("DESKTOP_UPDATE_INPUT_INVALID", err.Error())
	}
	if input.Platform == "win32" && !strings.HasSuffix(strings.ToLower(input.Filename), ".exe") {
		return nil, infraerrors.BadRequest("DESKTOP_UPDATE_PACKAGE_INVALID", "Windows 更新包必须是 .exe")
	}
	if input.Platform == "darwin" && !strings.HasSuffix(strings.ToLower(input.Filename), ".zip") {
		return nil, infraerrors.BadRequest("DESKTOP_UPDATE_PACKAGE_INVALID", "macOS 自动更新包必须是 .zip")
	}
	if input.Platform == "darwin" && input.InstallerSize <= 0 {
		return nil, infraerrors.BadRequest("DESKTOP_UPDATE_INSTALLER_INVALID", "macOS 首次安装包不能为空")
	}
	if input.InstallerSize > 0 && !strings.HasSuffix(strings.ToLower(input.InstallerFilename), ".dmg") {
		return nil, infraerrors.BadRequest("DESKTOP_UPDATE_INSTALLER_INVALID", "macOS 首次安装包必须是 .dmg")
	}
	if input.PackageSize <= 0 || input.PackageSize > desktopUploadMaxFileSize || input.InstallerSize < 0 || input.InstallerSize > desktopUploadMaxFileSize {
		return nil, infraerrors.BadRequest("DESKTOP_UPLOAD_SIZE_INVALID", "软件包不能为空，单个文件不能超过 2 GiB")
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	jobs, err := q.list()
	if err != nil {
		return nil, err
	}
	active := 0
	for _, job := range jobs {
		if job.Status != "completed" && job.Status != "failed" {
			active++
		}
	}
	if active >= desktopUploadQueueLimit {
		return nil, infraerrors.Conflict("DESKTOP_UPLOAD_QUEUE_FULL", "最多保留 8 个未完成上传，请先完成或删除旧任务")
	}
	now := time.Now().UTC()
	job := &desktopUploadJob{DesktopUpload: DesktopUpload{
		ID: uuid.NewString(), Version: input.Version, Platform: input.Platform, Arch: input.Arch,
		ReleaseNotes: input.ReleaseNotes, Filename: input.Filename, InstallerFilename: input.InstallerFilename,
		PackageSize: input.PackageSize, InstallerSize: input.InstallerSize, Status: "receiving",
		CreatedAt: now, UpdatedAt: now, ChunkSize: DesktopUploadChunkSize,
	}, ActorID: actorID}
	dir, err := q.dir(job.ID)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	if err := q.save(job); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	q.start()
	return uploadView(job), nil
}

func (q *DesktopUploadQueue) Append(id, artifact string, offset int64, body io.Reader) (*DesktopUpload, error) {
	if artifact != "package" && artifact != "installer" {
		return nil, infraerrors.BadRequest("DESKTOP_UPLOAD_ARTIFACT_INVALID", "文件类型无效")
	}
	if offset < 0 {
		return nil, infraerrors.BadRequest("DESKTOP_UPLOAD_OFFSET_INVALID", "分片位置无效")
	}
	data, err := io.ReadAll(io.LimitReader(body, DesktopUploadChunkSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || int64(len(data)) > DesktopUploadChunkSize {
		return nil, infraerrors.BadRequest("DESKTOP_UPLOAD_CHUNK_INVALID", "分片不能为空或超过 256 KiB")
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	job, err := q.read(id)
	if err != nil {
		return nil, err
	}
	if job.Status != "receiving" {
		return nil, infraerrors.Conflict("DESKTOP_UPLOAD_NOT_RECEIVING", "上传任务已进入后台处理")
	}
	received, total := &job.PackageReceived, job.PackageSize
	if artifact == "installer" {
		received, total = &job.InstallerReceived, job.InstallerSize
	}
	if total <= 0 {
		return nil, infraerrors.BadRequest("DESKTOP_UPLOAD_ARTIFACT_INVALID", "该任务不包含此文件")
	}
	if offset > *received || offset > total-int64(len(data)) {
		return nil, infraerrors.Conflict("DESKTOP_UPLOAD_OFFSET_INVALID", "分片位置与服务器不一致，请继续上传")
	}
	dir, err := q.dir(id)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, artifact)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	if offset < *received {
		if offset+int64(len(data)) > *received {
			return nil, infraerrors.Conflict("DESKTOP_UPLOAD_OFFSET_INVALID", "分片与已保存数据重叠")
		}
		existing := make([]byte, len(data))
		if _, err := f.ReadAt(existing, offset); err != nil {
			return nil, err
		}
		if !bytes.Equal(data, existing) {
			return nil, infraerrors.Conflict("DESKTOP_UPLOAD_CHUNK_MISMATCH", "重传文件内容不一致，请重新创建上传任务")
		}
		return uploadView(job), nil
	}
	if err := f.Truncate(*received); err != nil {
		return nil, err
	}
	if _, err := f.WriteAt(data, offset); err != nil {
		return nil, err
	}
	if err := f.Sync(); err != nil {
		return nil, err
	}
	*received += int64(len(data))
	job.UpdatedAt = time.Now().UTC()
	if err := q.save(job); err != nil {
		return nil, err
	}
	return uploadView(job), nil
}

func (q *DesktopUploadQueue) Complete(id string) (*DesktopUpload, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	job, err := q.read(id)
	if err != nil {
		return nil, err
	}
	if job.Status == "queued" || job.Status == "uploading" || job.Status == "completed" {
		return uploadView(job), nil
	}
	if job.Status == "failed" {
		return nil, infraerrors.Conflict("DESKTOP_UPLOAD_FAILED", "上传任务处理失败，请点击重试")
	}
	if job.PackageReceived != job.PackageSize || job.InstallerReceived != job.InstallerSize {
		return nil, infraerrors.Conflict("DESKTOP_UPLOAD_INCOMPLETE", "文件尚未完整上传至服务器")
	}
	job.Status = "queued"
	job.Error = ""
	job.UpdatedAt = time.Now().UTC()
	if err := q.save(job); err != nil {
		return nil, err
	}
	q.start()
	q.signal()
	return uploadView(job), nil
}

func (q *DesktopUploadQueue) Retry(id string) (*DesktopUpload, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	job, err := q.read(id)
	if err != nil {
		return nil, err
	}
	if job.Status != "failed" {
		return uploadView(job), nil
	}
	job.Status = "queued"
	job.Error = ""
	job.UpdatedAt = time.Now().UTC()
	if err := q.save(job); err != nil {
		return nil, err
	}
	q.start()
	q.signal()
	return uploadView(job), nil
}

func (q *DesktopUploadQueue) Delete(id string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	job, err := q.read(id)
	if err != nil {
		return err
	}
	if job.Status == "uploading" {
		return infraerrors.Conflict("DESKTOP_UPLOAD_BUSY", "任务正在后台处理，请稍后再删除")
	}
	dir, err := q.dir(id)
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

func (q *DesktopUploadQueue) run() {
	defer close(q.done)
	q.mu.Lock()
	jobs, err := q.list()
	if err != nil {
		slog.Error("read desktop upload queue", "error", err)
	} else {
		for i := range jobs {
			if jobs[i].Status == "uploading" {
				jobs[i].Status = "queued"
				if err := q.save(&jobs[i]); err != nil {
					slog.Error("recover desktop upload", "error", err)
				}
			}
		}
	}
	q.mu.Unlock()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		if q.ctx.Err() != nil {
			return
		}
		if q.processNext() {
			continue
		}
		select {
		case <-q.ctx.Done():
			return
		case <-q.wake:
		case <-ticker.C:
		}
	}
}

func (q *DesktopUploadQueue) processNext() bool {
	q.mu.Lock()
	jobs, err := q.list()
	if err != nil {
		q.mu.Unlock()
		return false
	}
	var next *desktopUploadJob
	for i := range jobs {
		job := jobs[i]
		if job.Status != "queued" && job.Status != "uploading" && time.Since(job.UpdatedAt) > desktopUploadRetention {
			dir, _ := q.dir(job.ID)
			_ = os.RemoveAll(dir)
			continue
		}
		if next == nil && job.Status == "queued" {
			next = &jobs[i]
		}
	}
	if next == nil {
		q.mu.Unlock()
		return false
	}
	next.Status = "uploading"
	next.UpdatedAt = time.Now().UTC()
	if err := q.save(next); err != nil {
		q.mu.Unlock()
		return false
	}
	q.mu.Unlock()

	dir, _ := q.dir(next.ID)
	installerPath := ""
	if next.InstallerSize > 0 {
		installerPath = filepath.Join(dir, "installer")
	}
	var item *DesktopRelease
	var processErr error
	for attempt := 0; attempt < 3; attempt++ {
		ctx, cancel := context.WithTimeout(q.ctx, 30*time.Minute)
		item, processErr = q.process(ctx, next.releaseInput(), filepath.Join(dir, "package"), installerPath, next.ActorID)
		cancel()
		if processErr == nil || q.ctx.Err() != nil {
			break
		}
		next.RetryCount++
		select {
		case <-q.ctx.Done():
		case <-time.After(time.Duration(attempt+1) * 10 * time.Second):
		}
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	next.UpdatedAt = time.Now().UTC()
	if processErr != nil {
		if q.ctx.Err() != nil {
			next.Status = "queued"
			next.Error = ""
		} else {
			next.Status = "failed"
			next.Error = fmt.Sprintf("后台处理失败：%v", processErr)
		}
	} else {
		next.Status = "completed"
		next.ReleaseID = item.ID
		next.Error = ""
		_ = os.Remove(filepath.Join(dir, "package"))
		_ = os.Remove(filepath.Join(dir, "installer"))
	}
	if err := q.save(next); err != nil {
		slog.Error("save desktop upload result", "error", err)
	}
	return true
}

func (s *DesktopUpdateService) CreateUpload(_ context.Context, input DesktopUploadInput, actorID int64) (*DesktopUpload, error) {
	if s.uploadQueue == nil {
		return nil, errors.New("desktop upload queue is unavailable")
	}
	return s.uploadQueue.Create(input, actorID)
}

func (s *DesktopUpdateService) AppendUpload(_ context.Context, id, artifact string, offset int64, body io.Reader) (*DesktopUpload, error) {
	if s.uploadQueue == nil {
		return nil, errors.New("desktop upload queue is unavailable")
	}
	return s.uploadQueue.Append(id, artifact, offset, body)
}

func (s *DesktopUpdateService) GetUpload(_ context.Context, id string) (*DesktopUpload, error) {
	if s.uploadQueue == nil {
		return nil, errors.New("desktop upload queue is unavailable")
	}
	return s.uploadQueue.Get(id)
}

func (s *DesktopUpdateService) ListUploads(_ context.Context) ([]DesktopUpload, error) {
	if s.uploadQueue == nil {
		return nil, errors.New("desktop upload queue is unavailable")
	}
	return s.uploadQueue.List()
}

func (s *DesktopUpdateService) CompleteUpload(_ context.Context, id string) (*DesktopUpload, error) {
	if s.uploadQueue == nil {
		return nil, errors.New("desktop upload queue is unavailable")
	}
	return s.uploadQueue.Complete(id)
}

func (s *DesktopUpdateService) RetryUpload(_ context.Context, id string) (*DesktopUpload, error) {
	if s.uploadQueue == nil {
		return nil, errors.New("desktop upload queue is unavailable")
	}
	return s.uploadQueue.Retry(id)
}

func (s *DesktopUpdateService) DeleteUpload(_ context.Context, id string) error {
	if s.uploadQueue == nil {
		return errors.New("desktop upload queue is unavailable")
	}
	return s.uploadQueue.Delete(id)
}
