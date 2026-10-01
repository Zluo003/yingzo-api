package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/mediaprobe"
	"github.com/google/uuid"
)

func (p *TemporaryAssetPublisher) PublishGeneratedMusic(
	ctx context.Context,
	owner TemporaryAssetOwner,
	fallbackPublicBaseURL string,
	upstreamURL string,
	assetID uuid.UUID,
) (string, error) {
	if p == nil || p.db == nil || p.fileStorage == nil {
		return "", errors.New("temporary asset publisher is unavailable")
	}
	if owner.UserID <= 0 || owner.APIKeyID <= 0 || owner.GroupID <= 0 {
		return "", errors.New("temporary asset owner is invalid")
	}

	runtime, err := p.fileStorage.Runtime(ctx)
	if err != nil {
		return "", fmt.Errorf("load temporary asset storage: %w", err)
	}
	publicBaseURL, err := p.fileStorage.EffectivePublicBaseURL(ctx, fallbackPublicBaseURL)
	if err != nil {
		return "", fmt.Errorf("resolve temporary asset public URL: %w", err)
	}

	var filename string
	existingErr := p.db.QueryRowContext(ctx, `SELECT original_filename FROM temporary_assets WHERE id=$1 AND user_id=$2 AND api_key_id=$3 AND purpose='generated' AND deleted_at IS NULL AND expires_at>NOW()`, assetID, owner.UserID, owner.APIKeyID).Scan(&filename)
	if existingErr == nil {
		return strings.TrimRight(publicBaseURL, "/") + "/media/" + assetID.String() + "/asset" + filepath.Ext(filename), nil
	}
	if !errors.Is(existingErr, sql.ErrNoRows) {
		return "", existingErr
	}
	if err := validateGeneratedVideoURL(ctx, upstreamURL, p.allowPrivateVideoURLs); err != nil {
		return "", err
	}
	client := p.videoHTTPClient
	if client == nil {
		client = newGeneratedVideoHTTPClient()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(upstreamURL), nil)
	if err != nil {
		return "", errors.New("generated audio URL is invalid")
	}
	req.Header.Set("Accept", "audio/*,application/octet-stream;q=0.8")
	req.Header.Set("User-Agent", "Yingzo-Music-Result-Publisher/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return "", errors.New("download generated audio failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("download generated audio returned HTTP %d", resp.StatusCode)
	}

	maxBytes := int64(200 << 20)
	if resp.ContentLength > maxBytes {
		return "", errors.New("generated audio exceeds the temporary asset size limit")
	}

	id := assetID
	localRoot, err := p.fileStorage.GeneratedLocalPath(ctx, runtime.Config.Generated)
	if err != nil {
		return "", fmt.Errorf("resolve local asset directory: %w", err)
	}
	assetDir := filepath.Join(localRoot, id.String())
	if err := os.MkdirAll(assetDir, 0o700); err != nil { //nolint:gosec // G703: configured storage root plus a generated UUID, never a caller-supplied path.
		return "", fmt.Errorf("create temporary asset directory: %w", err)
	}
	cleanupLocal := true
	defer func() {
		if cleanupLocal {
			_ = os.RemoveAll(assetDir) //nolint:gosec // G703: removes only the generated UUID's directory after an unsuccessful upload.
		}
	}()

	localPath := filepath.Join(assetDir, "object")
	temporaryPath := localPath + ".tmp"
	temporary, err := os.OpenFile(temporaryPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600) //nolint:gosec // G703: fixed object.tmp filename inside this task's deterministic UUID staging directory.
	if err != nil {
		return "", fmt.Errorf("create temporary asset file: %w", err)
	}
	hasher := sha256.New()
	sizeBytes, copyErr := io.Copy(io.MultiWriter(temporary, hasher), io.LimitReader(resp.Body, maxBytes+1))
	if copyErr == nil {
		copyErr = temporary.Sync()
	}
	closeErr := temporary.Close()
	if copyErr != nil {
		return "", errors.New("download generated audio failed")
	}
	if closeErr != nil {
		return "", fmt.Errorf("close temporary asset file: %w", closeErr)
	}
	if sizeBytes == 0 {
		return "", errors.New("generated audio payload is empty")
	}
	if sizeBytes > maxBytes {
		return "", errors.New("generated audio exceeds the temporary asset size limit")
	}

	mimeType, extension, err := inspectGeneratedMusicFile(ctx, temporaryPath)
	if err != nil {
		return "", err
	}
	if err := os.Rename(temporaryPath, localPath); err != nil { //nolint:gosec // G703: fixed object.tmp and object names in the same generated UUID's staging directory.
		return "", fmt.Errorf("publish temporary asset file: %w", err)
	}

	if err := p.enforceResultDailyQuota(ctx, owner, runtime.Config, sizeBytes); err != nil {
		return "", err
	}
	// 总容量上限：产物同样占容量，先按"最早失效优先"驱逐未租用素材腾空间。
	if _, capacityErr := p.fileStorage.EnforceTemporaryAssetCapacity(ctx, TemporaryAssetPurposeGenerated, sizeBytes); capacityErr != nil {
		return "", capacityErr
	}

	// Keep a stable media identity; the media endpoint resolves its storage version.
	backend, storageKey, err := p.fileStorage.storeGeneratedFile(ctx, runtime.Config.Generated, id.String(), localPath, mimeType)
	if err != nil {
		_ = os.RemoveAll(assetDir) //nolint:gosec // G703: configured storage root plus a generated UUID, never a caller-supplied path.
		return "", fmt.Errorf("store generated output: %w", err)
	}
	stored := false
	defer func() {
		if backend == "s3" {
			_ = os.RemoveAll(assetDir) //nolint:gosec // G703: configured storage root plus a generated UUID; removes only this upload's staging directory.
			if !stored {
				_ = runtime.Store.Delete(context.WithoutCancel(ctx), storageKey)
			}
		}
	}()

	token, err := generatedAssetRandomToken(32)
	if err != nil {
		return "", fmt.Errorf("generate temporary asset token: %w", err)
	}
	storageVersion := ""
	if runtime.Config.Generated != nil {
		storageVersion, err = p.fileStorage.generatedVersion(ctx, runtime.Config.Generated)
		if err != nil {
			return "", err
		}
	}
	metadata, err := json.Marshal(map[string]any{
		"storage_version":       storageVersion,
		"probe":                 "ffprobe",
		"provider_url_rehosted": true,
		"source":                "generated_audio",
	})
	if err != nil {
		return "", fmt.Errorf("encode temporary asset metadata: %w", err)
	}

	expiresAt := time.Now().UTC().Add(time.Duration(runtime.Config.ResultRetentionHours) * time.Hour)
	_, err = p.db.ExecContext(ctx, `
		INSERT INTO temporary_assets(
			id,user_id,api_key_id,group_id,public_token_hash,storage_backend,
			storage_key,original_filename,media_type,mime_type,size_bytes,sha256,
			metadata,expires_at,purpose
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
	`, id, owner.UserID, owner.APIKeyID, owner.GroupID, generatedAssetHashToken(token),
		backend, storageKey, "generated-audio"+extension, "audio", mimeType, sizeBytes,
		hex.EncodeToString(hasher.Sum(nil)), metadata, expiresAt, TemporaryAssetPurposeGenerated)
	if err != nil {
		return "", fmt.Errorf("record temporary asset: %w", err)
	}

	cleanupLocal = false
	stored = true
	return strings.TrimRight(publicBaseURL, "/") + "/media/" + id.String() + "/asset" + extension, nil
}

func inspectGeneratedMusicFile(ctx context.Context, path string) (string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	binary, err := mediaprobe.Ensure(ctx)
	if err != nil {
		return "", "", err
	}
	output, err := exec.CommandContext(ctx, binary, "-v", "error", "-protocol_whitelist", "file,pipe", "-show_entries", "stream=codec_type:format=format_name", "-of", "json", path).Output()
	if err != nil {
		return "", "", errors.New("generated audio cannot be decoded")
	}
	var probe struct {
		Streams []struct {
			Type string `json:"codec_type"`
		} `json:"streams"`
		Format struct {
			Name string `json:"format_name"`
		} `json:"format"`
	}
	if json.Unmarshal(output, &probe) != nil {
		return "", "", errors.New("invalid audio probe")
	}
	audio := false
	for _, s := range probe.Streams {
		if s.Type == "audio" {
			audio = true
		}
	}
	if !audio {
		return "", "", errors.New("generated output has no audio stream")
	}
	switch probe.Format.Name {
	case "mp3":
		return "audio/mpeg", ".mp3", nil
	case "wav":
		return "audio/wav", ".wav", nil
	}
	if strings.Contains(probe.Format.Name, "m4a") {
		for _, s := range probe.Streams {
			if s.Type == "video" {
				return "", "", errors.New("generated output contains video")
			}
		}
		return "audio/mp4", ".m4a", nil
	}
	return "", "", errors.New("generated output must be MP3, M4A or WAV")
}
func (p *TemporaryAssetPublisher) PublishGeneratedMusicCover(ctx context.Context, owner TemporaryAssetOwner, base, upstream string, id uuid.UUID) (string, error) {
	if p == nil || p.db == nil || p.fileStorage == nil {
		return "", errors.New("temporary asset publisher is unavailable")
	}
	var filename string
	err := p.db.QueryRowContext(ctx, `SELECT original_filename FROM temporary_assets WHERE id=$1 AND user_id=$2 AND api_key_id=$3 AND purpose='generated' AND deleted_at IS NULL AND expires_at>NOW()`, id, owner.UserID, owner.APIKeyID).Scan(&filename)
	if err == nil {
		publicBase, e := p.fileStorage.EffectivePublicBaseURL(ctx, base)
		if e != nil {
			return "", e
		}
		return strings.TrimRight(publicBase, "/") + "/media/" + id.String() + "/asset" + filepath.Ext(filename), nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if err := validateGeneratedVideoURL(ctx, upstream, p.allowPrivateVideoURLs); err != nil {
		return "", err
	}
	// The image publisher uses this deterministic identity to recover completed uploads.
	ctx = context.WithValue(ctx, generatedAssetIDKey{}, id)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, upstream, nil)
	if err != nil {
		return "", err
	}
	client := p.videoHTTPClient
	if client == nil {
		client = newGeneratedVideoHTTPClient()
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return "", errors.New("cover download failed")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPublishedGeneratedImageBytes+1))
	if err != nil {
		return "", err
	}
	if int64(len(body)) > maxPublishedGeneratedImageBytes {
		return "", errors.New("cover is too large")
	}
	return p.PublishGeneratedImage(ctx, owner, base, base64.StdEncoding.EncodeToString(body), "")
}
