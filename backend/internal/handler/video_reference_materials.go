package handler

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
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// videoReferenceMaterialLimits 参考素材单文件上限，按内容项类型区分。取值与
// mediaPolicies 中同类媒体的上限一致，保证下载/解码出来的素材一定能入库。
var videoReferenceMaterialLimits = map[string]int64{
	"image_url": 30 << 20,
	"video_url": 200 << 20,
	"audio_url": 15 << 20,
}

// referenceMaterialDownloader 把外部参考素材 URL 拉到本机临时文件。
// 抽象成接口便于测试替换网络访问。
type referenceMaterialDownloader interface {
	Fetch(ctx context.Context, rawURL string, dst *os.File, maxBytes int64) (*service.ReferenceMaterialFetchResult, error)
}

// videoReferenceMaterialError 让参考素材解析失败能以既有的视频错误响应格式返回。
type videoReferenceMaterialError struct {
	status  int
	code    string
	message string
}

func (e *videoReferenceMaterialError) Error() string {
	if e == nil {
		return "reference material error"
	}
	return e.message
}

func videoReferenceMaterialFailure(status int, code, message string) error {
	return &videoReferenceMaterialError{status: status, code: code, message: message}
}

// platformReferenceMaterialRef 指向平台素材库里的一条素材。
type platformReferenceMaterialRef struct {
	byID    uuid.UUID
	byToken string
}

// resolveVideoReferenceMaterials 把请求体里的参考素材统一收敛成平台自己的公网
// URL，并用平台探测出的时长覆盖下游传入的 duration_seconds。
//
// 三类入参：
//   - base64 data URI：解码落盘后转存，换成平台公网 URL；
//   - 外部公网 URL：下载到本机、由可信探测拿到时长后转存，换成平台公网 URL；
//   - 平台素材 URL（multipart 已上传，或直接引用素材库）：URL 保持不变，时长直接
//     读素材行里保存的探测结果，不重复下载。
//
// 返回 changed=false 表示请求体没有任何改动，调用方应沿用原始请求体。
func (h *VideoHandler) resolveVideoReferenceMaterials(c *gin.Context, apiKey *service.APIKey, raw map[string]any) ([]byte, bool, error) {
	if h == nil || h.agentHandler == nil || h.agentHandler.db == nil || raw == nil {
		return nil, false, nil
	}
	content, ok := raw["content"].([]any)
	if !ok || len(content) == 0 {
		return nil, false, nil
	}
	ownHosts := h.referenceMaterialOwnHosts(c)
	customAccess := service.S3CustomAccess{}
	if h.agentHandler != nil && h.agentHandler.fileStorage != nil {
		customAccess = h.agentHandler.fileStorage.EffectiveS3CustomAccess(c.Request.Context())
	}
	// 同一个素材被多条内容项引用时只下载与入库一次：map 的 key 复用请求体里已有的
	// 字符串，不额外复制内存。时长仍按内容条数分别计费。
	stored := make(map[string]*temporaryAssetUploadResult, len(content))
	changed := false
	for _, value := range content {
		item, ok := value.(map[string]any)
		if !ok {
			continue
		}
		urlField := videoReferenceMaterialURLField(strings.TrimSpace(stringValue(item["type"])))
		urlObject, _ := item[urlField].(map[string]any)
		rawURL := ""
		if urlObject != nil {
			rawURL = strings.TrimSpace(stringValue(urlObject["url"]))
		}

		// 平台素材库里的地址：URL 不动，时长以素材行里的探测结果为准。
		if urlField != "" && urlObject != nil {
			if ref, isPlatformAsset := parsePlatformReferenceMaterialURL(rawURL, ownHosts, customAccess); isPlatformAsset {
				duration, probed, err := h.platformReferenceMaterialDuration(c.Request.Context(), apiKey, ref)
				if err != nil {
					return nil, false, err
				}
				if strings.EqualFold(urlField, "video_url") {
					if !probed {
						return nil, false, videoReferenceMaterialFailure(http.StatusBadRequest,
							"reference_video_duration_unavailable", "Reference video duration is unavailable")
					}
					if setContentItemDurationSeconds(item, duration) {
						changed = true
					}
					continue
				}
				if clearContentItemDurationSeconds(item) {
					changed = true
				}
				continue
			}
		}

		// 其余情况：下游声明的时长一律丢弃，计费时长只能由平台探测得出。
		if clearContentItemDurationSeconds(item) {
			changed = true
		}
		if urlField == "" || urlObject == nil || rawURL == "" {
			continue
		}
		result, cached := stored[rawURL]
		if !cached {
			uploaded, uploadErr := h.storeVideoReferenceMaterial(c, apiKey, rawURL, urlField)
			if uploadErr != nil {
				return nil, false, uploadErr
			}
			result = uploaded
			stored[rawURL] = result
		}
		urlObject["url"] = result.URL
		changed = true
		if strings.EqualFold(urlField, "video_url") {
			if result.Metadata.DurationSeconds <= 0 {
				return nil, false, videoReferenceMaterialFailure(http.StatusBadRequest,
					"reference_video_duration_unavailable", "Reference video duration is unavailable")
			}
			setContentItemDurationSeconds(item, result.Metadata.DurationSeconds)
		}
	}
	if !changed {
		return nil, false, nil
	}
	resolved, err := json.Marshal(raw)
	if err != nil {
		return nil, false, videoReferenceMaterialFailure(http.StatusBadRequest,
			"invalid_video_request", "Failed to encode rewritten video request")
	}
	return resolved, true, nil
}

// videoReferenceMaterialURLField 返回内容项承载素材地址的字段名，非素材项返回空。
func videoReferenceMaterialURLField(itemType string) string {
	switch strings.ToLower(strings.TrimSpace(itemType)) {
	case "image_url":
		return "image_url"
	case "video_url":
		return "video_url"
	case "audio_url":
		return "audio_url"
	default:
		return ""
	}
}

// setContentItemDurationSeconds 写入平台探测出的时长，返回是否真的改动了请求体。
func setContentItemDurationSeconds(item map[string]any, seconds float64) bool {
	if current, exists := item["duration_seconds"]; exists {
		if value, ok := current.(float64); ok && value == seconds {
			return false
		}
	}
	item["duration_seconds"] = seconds
	return true
}

// clearContentItemDurationSeconds 删除下游传入的时长声明，返回是否真的删掉了。
func clearContentItemDurationSeconds(item map[string]any) bool {
	if _, exists := item["duration_seconds"]; !exists {
		return false
	}
	delete(item, "duration_seconds")
	return true
}

// referenceMaterialOwnHosts 收集平台自身的 host：本次请求 origin 与素材库配置的
// 公网 URL，以及 S3 自定义域名直读域名。命中这些 host 的参考素材说明已经在平台素材
// 库里，不需要再下载转存。
//
// 只认这几个来源，不认请求的 Host 头：Host 由下游控制，拿它当"平台自身"会让
// 第三方地址被误判成平台素材。代价是两者都取不到时（既没配公网 URL、origin 又
// 不是 HTTPS/localhost），multipart 刚上传的素材会被当成外部地址再转存一份。
func (h *VideoHandler) referenceMaterialOwnHosts(c *gin.Context) map[string]bool {
	hosts := make(map[string]bool, 3)
	add := func(raw string) {
		parsed, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || parsed.Host == "" {
			return
		}
		hosts[strings.ToLower(parsed.Hostname())] = true
	}
	origin, err := requestPublicOrigin(c)
	if err == nil {
		add(origin)
	}
	if h.agentHandler != nil && h.agentHandler.fileStorage != nil {
		if base, err := h.agentHandler.fileStorage.EffectivePublicBaseURL(c.Request.Context(), origin); err == nil {
			add(base)
		}
		if access := h.agentHandler.fileStorage.EffectiveS3CustomAccess(c.Request.Context()); access.Enabled() {
			add(access.Base)
		}
	}
	return hosts
}

// parsePlatformReferenceMaterialURL 识别平台素材的公网读取地址：
//   - 平台代理：/media/<uuid>/<filename> 与 /temporary-assets/<token>；
//   - 自定义域名直读：https://{access.Base}{access.Prefix}<uuid>（access 启用时）。
//
// 只有 host 属于平台自身（ownHosts）或自定义域名时才认作平台记录，避免第三方站点
// 的同形路径被当成平台记录。
func parsePlatformReferenceMaterialURL(rawURL string, ownHosts map[string]bool, access service.S3CustomAccess) (platformReferenceMaterialRef, bool) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" || len(ownHosts) == 0 {
		return platformReferenceMaterialRef{}, false
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		return platformReferenceMaterialRef{}, false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return platformReferenceMaterialRef{}, false
	}
	host := strings.ToLower(parsed.Hostname())
	if !ownHosts[host] && !(access.Enabled() && strings.EqualFold(access.Host(), host)) {
		return platformReferenceMaterialRef{}, false
	}
	ref, ok := service.ParseTemporaryAssetRef(trimmed, access.Host(), access.Prefix)
	if !ok {
		return platformReferenceMaterialRef{}, false
	}
	if ref.ID != uuid.Nil {
		return platformReferenceMaterialRef{byID: ref.ID}, true
	}
	return platformReferenceMaterialRef{byToken: ref.Token}, true
}

// platformReferenceMaterialDuration 读取平台素材行里保存的探测时长。
// probed=false 表示素材存在但没有视频时长（例如图片素材）。
//
// 查询按 API Key、用户与分组收窄，与素材解析接口的凭据隔离保持一致：参考时长
// 参与计费，不能从别的凭据上传的素材上取。
func (h *VideoHandler) platformReferenceMaterialDuration(ctx context.Context, apiKey *service.APIKey, ref platformReferenceMaterialRef) (float64, bool, error) {
	const selection = `SELECT metadata FROM temporary_assets
		WHERE api_key_id=$1 AND user_id=$2 AND group_id IS NOT DISTINCT FROM $3
			AND deleted_at IS NULL AND GREATEST(expires_at,lease_until)>NOW() AND `
	if apiKey == nil {
		return 0, false, videoReferenceMaterialFailure(http.StatusUnauthorized,
			"invalid_api_key", "Invalid API key")
	}
	var row *sql.Row
	if ref.byID != uuid.Nil {
		row = h.agentHandler.db.QueryRowContext(ctx, selection+`id=$4`, apiKey.ID, apiKey.UserID, apiKey.GroupID, ref.byID)
	} else {
		row = h.agentHandler.db.QueryRowContext(ctx, selection+`public_token_hash=$4`, apiKey.ID, apiKey.UserID, apiKey.GroupID, hashToken(ref.byToken))
	}
	var metadata []byte
	err := row.Scan(&metadata)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, videoReferenceMaterialFailure(http.StatusBadRequest,
			"reference_material_unavailable", "Reference material is unavailable or has expired")
	}
	if err != nil {
		return 0, false, videoReferenceMaterialFailure(http.StatusServiceUnavailable,
			"reference_material_lookup_failed", "Reference material storage is unavailable")
	}
	var record struct {
		DurationSeconds float64 `json:"duration_seconds"`
	}
	if len(metadata) > 0 {
		if err := json.Unmarshal(metadata, &record); err != nil {
			return 0, false, nil
		}
	}
	return record.DurationSeconds, record.DurationSeconds > 0, nil
}

// storeVideoReferenceMaterial 把一份参考素材落到平台素材库，返回它的公网 URL
// 与可信探测结果（含时长）。base64 data URI 直接解码落盘，外部 URL 走 SSRF 安全
// 下载；两者都复用素材上传路径完成校验、探测、配额与入库。
func (h *VideoHandler) storeVideoReferenceMaterial(c *gin.Context, apiKey *service.APIKey, rawURL string, itemType string) (*temporaryAssetUploadResult, error) {
	limit, ok := videoReferenceMaterialLimits[itemType]
	if !ok {
		return nil, videoReferenceMaterialFailure(http.StatusBadRequest,
			"unsupported_reference_material", "Unsupported reference material type")
	}
	root, err := h.agentHandler.assetLocalDir(c.Request.Context())
	if err != nil {
		return nil, videoReferenceMaterialFailure(http.StatusServiceUnavailable,
			"media_upload_unavailable", "Reference material storage is unavailable")
	}
	spool, err := os.CreateTemp(root, "reference-*.spool")
	if err != nil {
		return nil, videoReferenceMaterialFailure(http.StatusServiceUnavailable,
			"media_upload_unavailable", "Reference material storage is unavailable")
	}
	spoolPath := spool.Name()
	defer func() {
		_ = spool.Close()
		_ = os.Remove(spoolPath)
	}()

	var (
		size   int64
		digest string
	)
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(rawURL)), "data:") {
		decoded, err := decodeInlineReferenceMaterial(rawURL, limit)
		if err != nil {
			return nil, videoReferenceMaterialFailure(http.StatusBadRequest, "invalid_reference_material", err.Error())
		}
		if _, err := spool.Write(decoded); err != nil {
			return nil, videoReferenceMaterialFailure(http.StatusServiceUnavailable,
				"media_upload_unavailable", "Reference material storage is unavailable")
		}
		sum := sha256.Sum256(decoded)
		size, digest = int64(len(decoded)), hex.EncodeToString(sum[:])
	} else {
		fetched, err := h.referenceMaterialFetcher.Fetch(c.Request.Context(), rawURL, spool, limit)
		if err != nil {
			return nil, videoReferenceMaterialFailure(http.StatusBadRequest,
				"reference_material_download_failed", err.Error())
		}
		size, digest = fetched.Size, fetched.SHA256
	}

	contentType, err := sniffReferenceMaterialType(spoolPath)
	if err != nil {
		return nil, videoReferenceMaterialFailure(http.StatusBadRequest, "unsupported_reference_material", err.Error())
	}
	// 既有上传路径的媒体嗅探是"先读 512 字节再回绕"，要求传入的文件停在起始位置；
	// 上面的写入/下载都把游标留在了末尾，这里必须显式回绕。
	if _, err := spool.Seek(0, io.SeekStart); err != nil {
		return nil, videoReferenceMaterialFailure(http.StatusServiceUnavailable,
			"media_upload_unavailable", "Reference material storage is unavailable")
	}
	// 既有上传路径按 multipart 文件头判定媒体类型、扩展名与体积，这里用可信探测
	// 结果补齐同样的元数据，避免绕过它的白名单与体积校验。
	header := &multipart.FileHeader{
		Filename: "reference" + referenceMaterialExtension(contentType),
		Size:     size,
		Header:   textproto.MIMEHeader{"Content-Type": []string{contentType}},
	}
	result, err := h.agentHandler.storeTemporaryAssetPart(c, apiKey, spool, header, digest)
	if err != nil {
		if uploadErr, ok := err.(*temporaryAssetUploadError); ok {
			message := uploadErr.message
			if message == "" {
				message = uploadErr.code
			}
			return nil, videoReferenceMaterialFailure(uploadErr.status, uploadErr.code, message)
		}
		return nil, videoReferenceMaterialFailure(http.StatusInternalServerError,
			"media_upload_failed", "Failed to store reference material")
	}
	return result, nil
}

// decodeInlineReferenceMaterial 解析 base64 data URI。非 base64 的 data URI
// （例如纯文本）一律拒绝，避免把非媒体内容当参考素材下载。
func decodeInlineReferenceMaterial(rawURL string, maxBytes int64) ([]byte, error) {
	trimmed := strings.TrimSpace(rawURL)
	comma := strings.Index(trimmed, ",")
	if comma < 0 {
		return nil, errors.New("inline reference material is malformed")
	}
	metadata := trimmed[len("data:"):comma]
	payload := strings.TrimSpace(trimmed[comma+1:])
	isBase64 := false
	for _, part := range strings.Split(metadata, ";") {
		if strings.EqualFold(strings.TrimSpace(part), "base64") {
			isBase64 = true
			break
		}
	}
	if !isBase64 {
		return nil, errors.New("inline reference material must be base64 encoded")
	}
	// base64 长度先于解码校验，避免为一个超大声明分配内存。
	if int64(len(payload))/4*3 > maxBytes+3 {
		return nil, errors.New("reference material exceeds the size limit")
	}
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(payload, "="))
		if err != nil {
			return nil, errors.New("inline reference material is not valid base64")
		}
	}
	if int64(len(decoded)) > maxBytes {
		return nil, errors.New("reference material exceeds the size limit")
	}
	if len(decoded) == 0 {
		return nil, errors.New("reference material is empty")
	}
	return decoded, nil
}

// sniffReferenceMaterialType 用可信探测判定素材真实类型，返回值必须落在素材
// 白名单里；既有的上传路径会用同一个探测器复核，两次判定结果一致才能入库。
func sniffReferenceMaterialType(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("reference material is unavailable")
	}
	defer func() { _ = file.Close() }()
	buffer := make([]byte, 512)
	n, err := file.Read(buffer)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", errors.New("reference material is unavailable")
	}
	detected := detectMediaMIME(buffer[:n])
	if referenceMaterialExtension(detected) == "" {
		return "", fmt.Errorf("unsupported reference material type %q", detected)
	}
	return detected, nil
}

// referenceMaterialExtension 返回 mediaPolicies 白名单里对应的扩展名；
// 返回空表示该类型不能作为参考素材存储。
func referenceMaterialExtension(contentType string) string {
	switch contentType {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "image/bmp":
		return ".bmp"
	case "image/tiff":
		return ".tif"
	case "image/heic":
		return ".heic"
	case "image/heif":
		return ".heif"
	case "video/mp4":
		return ".mp4"
	case "video/quicktime":
		return ".mov"
	case "audio/wav", "audio/x-wav":
		return ".wav"
	case "audio/mpeg":
		return ".mp3"
	default:
		return ""
	}
}
