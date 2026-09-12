package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// referenceMaterialDownloadTimeout 参考素材下载超时。参考视频单文件上限
// 200 MiB，比生成产物回捞需要更长的窗口。
const referenceMaterialDownloadTimeout = 5 * time.Minute

// referenceMaterialMaxRedirects 参考素材下载允许的重定向跳数上限。
const referenceMaterialMaxRedirects = 5

// ReferenceMaterialFetchResult 描述一次参考素材下载的结果。
type ReferenceMaterialFetchResult struct {
	Size         int64
	SHA256       string
	UpstreamType string
}

// ReferenceMaterialFetcher 把下游提交的参考素材 URL 拉到本机，交给素材上传路径
// 探测并把素材转存到平台自己的公网 URL。
//
// 与生成产物回捞共用同一套 SSRF 防护：只接受 HTTP(S)、拒绝私网/回环地址与 URL
// fragment、逐跳重新校验重定向，并使用 safeDialContext 阻断 DNS rebinding。
type ReferenceMaterialFetcher struct {
	client *http.Client
}

// NewReferenceMaterialFetcher 构造参考素材下载器。
func NewReferenceMaterialFetcher() *ReferenceMaterialFetcher {
	client := newSSRFSafeHTTPClient(referenceMaterialDownloadTimeout)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= referenceMaterialMaxRedirects {
			return errors.New("reference material download exceeded the redirect limit")
		}
		return validateGeneratedVideoURL(req.Context(), req.URL.String(), false)
	}
	return &ReferenceMaterialFetcher{client: client}
}

// Fetch 把 rawURL 的内容写入 dst，最多 maxBytes 字节。
//
// dst 由调用方创建并负责关闭：调用方需要让下载落盘位置与素材目录处于同一文件
// 系统，后续才能用 rename 接管而不是再拷贝一份。返回的 SHA256 可以直接作为素材
// 摘要传入上传路径，跳过二次计算。
func (f *ReferenceMaterialFetcher) Fetch(ctx context.Context, rawURL string, dst *os.File, maxBytes int64) (*ReferenceMaterialFetchResult, error) {
	if f == nil || f.client == nil {
		return nil, errors.New("reference material fetcher is unavailable")
	}
	if dst == nil {
		return nil, errors.New("reference material spool is unavailable")
	}
	if maxBytes <= 0 {
		return nil, errors.New("reference material size limit is invalid")
	}
	if err := validateGeneratedVideoURL(ctx, rawURL, false); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(rawURL), nil)
	if err != nil {
		return nil, errors.New("reference material URL is invalid")
	}
	req.Header.Set("Accept", "image/*,video/*,audio/*,application/octet-stream;q=0.8")
	req.Header.Set("User-Agent", "Sub2API-Reference-Material/1.0")
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, errors.New("download reference material failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("download reference material returned HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxBytes {
		return nil, errors.New("reference material exceeds the size limit")
	}
	hasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(dst, hasher), io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, errors.New("download reference material failed")
	}
	if written > maxBytes {
		return nil, errors.New("reference material exceeds the size limit")
	}
	if written == 0 {
		return nil, errors.New("reference material is empty")
	}
	if err := dst.Sync(); err != nil {
		return nil, fmt.Errorf("flush reference material spool: %w", err)
	}
	return &ReferenceMaterialFetchResult{
		Size:         written,
		SHA256:       hex.EncodeToString(hasher.Sum(nil)),
		UpstreamType: strings.TrimSpace(resp.Header.Get("Content-Type")),
	}, nil
}
