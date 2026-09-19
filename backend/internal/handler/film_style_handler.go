package handler

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "golang.org/x/image/webp"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const maxFilmStylePreviewBytes = 20 << 20

type filmStyleTemplate struct {
	ID         string    `json:"id"`
	Category   string    `json:"category"`
	Name       string    `json:"name"`
	Prompt     string    `json:"prompt"`
	StorageKey string    `json:"-"`
	MimeType   string    `json:"mimeType"`
	SHA256     string    `json:"sha256"`
	Width      int       `json:"width"`
	Height     int       `json:"height"`
	Revision   int       `json:"revision"`
	SortOrder  int       `json:"sortOrder"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

type filmStylePreview struct {
	MimeType string `json:"mimeType"`
	SHA256   string `json:"sha256"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}

func (h *AgentHandler) styleTemplatesDir() string {
	if h == nil || h.styleDir == "" {
		return ""
	}
	return h.styleDir
}

// withinStyleDir 报告 path 是否严格位于 base 目录内部（含路径清洗，拒绝
// 绝对路径与 .. 上跳），用于把持久化 storage_key 约束在样式目录之内。
func withinStyleDir(base, path string) bool {
	if base == "" || path == "" {
		return false
	}
	cleanBase := filepath.Clean(base)
	cleanPath := filepath.Clean(path)
	rel, err := filepath.Rel(cleanBase, cleanPath)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (h *AgentHandler) queryFilmStyles(includeArchived bool) ([]filmStyleTemplate, error) {
	if h == nil || h.db == nil {
		return nil, fmt.Errorf("film style storage is unavailable")
	}
	where := "WHERE status='active'"
	if includeArchived {
		where = ""
	}
	rows, err := h.db.Query(`SELECT id,category,name,prompt,storage_key,mime_type,sha256,width,height,revision,sort_order,status,created_at,updated_at FROM film_style_templates ` + where + ` ORDER BY category,sort_order,updated_at,id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	styles := make([]filmStyleTemplate, 0)
	for rows.Next() {
		var item filmStyleTemplate
		if err := rows.Scan(&item.ID, &item.Category, &item.Name, &item.Prompt, &item.StorageKey, &item.MimeType, &item.SHA256, &item.Width, &item.Height, &item.Revision, &item.SortOrder, &item.Status, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		styles = append(styles, item)
	}
	return styles, rows.Err()
}

func filmStyleCatalogETag(items []filmStyleTemplate) string {
	payload, _ := json.Marshal(items)
	digest := sha256.Sum256(payload)
	return `"` + hex.EncodeToString(digest[:]) + `"`
}

func (h *AgentHandler) ListFilmStyleTemplates(c *gin.Context) {
	items, err := h.queryFilmStyles(false)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "api_error", "message": err.Error()}})
		return
	}
	etag := filmStyleCatalogETag(items)
	c.Header("ETag", etag)
	c.Header("Cache-Control", "private, max-age=60")
	if strings.TrimSpace(c.GetHeader("If-None-Match")) == etag {
		c.Status(http.StatusNotModified)
		return
	}
	data := make([]gin.H, 0, len(items))
	for _, item := range items {
		data = append(data, gin.H{
			"id": item.ID, "category": item.Category, "name": item.Name,
			"prompt": item.Prompt, "revision": item.Revision,
			"preview":   filmStylePreview{MimeType: item.MimeType, SHA256: item.SHA256, Width: item.Width, Height: item.Height},
			"updatedAt": item.UpdatedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"catalogRevision": etag, "items": data})
}

func (h *AgentHandler) ServeFilmStylePreview(c *gin.Context) {
	if h == nil || h.db == nil {
		c.Status(http.StatusServiceUnavailable)
		return
	}
	var item filmStyleTemplate
	err := h.db.QueryRow(`SELECT id,category,name,prompt,storage_key,mime_type,sha256,width,height,revision,sort_order,status,created_at,updated_at FROM film_style_templates WHERE id=$1 AND status='active'`, c.Param("id")).Scan(&item.ID, &item.Category, &item.Name, &item.Prompt, &item.StorageKey, &item.MimeType, &item.SHA256, &item.Width, &item.Height, &item.Revision, &item.SortOrder, &item.Status, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	root := filepath.Clean(h.styleTemplatesDir())
	path := filepath.Join(root, filepath.Clean(item.StorageKey))
	if root == "" || (path != root && !strings.HasPrefix(path, root+string(os.PathSeparator))) {
		c.Status(http.StatusNotFound)
		return
	}
	file, err := os.Open(path)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	defer func() { _ = file.Close() }()
	stat, err := file.Stat()
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	c.Header("ETag", `"`+item.SHA256+`"`)
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	c.Header("Content-Type", item.MimeType)
	http.ServeContent(c.Writer, c.Request, filepath.Base(path), stat.ModTime(), file)
}

func (h *AgentHandler) AdminListFilmStyleTemplates(c *gin.Context) {
	items, err := h.queryFilmStyles(true)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (h *AgentHandler) AdminCreateFilmStyleTemplate(c *gin.Context) {
	category := strings.TrimSpace(c.PostForm("category"))
	name := strings.TrimSpace(c.PostForm("name"))
	prompt := strings.TrimSpace(c.PostForm("prompt"))
	if category != "realistic" && category != "3d" && category != "2d" || name == "" || prompt == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "category, name and prompt are required"}})
		return
	}
	file, header, err := c.Request.FormFile("preview")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "preview image is required"}})
		return
	}
	defer func() { _ = file.Close() }()
	item, err := h.saveFilmStyleTemplate(c, "", category, name, prompt, 0, file, header.Filename)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}
	c.JSON(http.StatusCreated, item)
}

func (h *AgentHandler) AdminUpdateFilmStyleTemplate(c *gin.Context) {
	var current filmStyleTemplate
	err := h.db.QueryRow(`SELECT id,category,name,prompt,storage_key,mime_type,sha256,width,height,revision,sort_order,status,created_at,updated_at FROM film_style_templates WHERE id=$1`, c.Param("id")).Scan(&current.ID, &current.Category, &current.Name, &current.Prompt, &current.StorageKey, &current.MimeType, &current.SHA256, &current.Width, &current.Height, &current.Revision, &current.SortOrder, &current.Status, &current.CreatedAt, &current.UpdatedAt)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	category, name, prompt := strings.TrimSpace(c.PostForm("category")), strings.TrimSpace(c.PostForm("name")), strings.TrimSpace(c.PostForm("prompt"))
	if category == "" {
		category = current.Category
	}
	if name == "" {
		name = current.Name
	}
	if prompt == "" {
		prompt = current.Prompt
	}
	var file io.Reader
	var filename string
	if upload, header, uploadErr := c.Request.FormFile("preview"); uploadErr == nil {
		defer func() { _ = upload.Close() }()
		file, filename = upload, header.Filename
	}
	item, err := h.saveFilmStyleTemplate(c, current.ID, category, name, prompt, current.Revision, file, filename)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}
	c.JSON(http.StatusOK, item)
}

func (h *AgentHandler) AdminDeleteFilmStyleTemplate(c *gin.Context) {
	result, err := h.db.Exec(`UPDATE film_style_templates SET status='archived',updated_at=NOW() WHERE id=$1`, c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}
	if n, _ := result.RowsAffected(); n == 0 {
		c.Status(http.StatusNotFound)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *AgentHandler) saveFilmStyleTemplate(c *gin.Context, id, category, name, prompt string, revision int, file io.Reader, filename string) (filmStyleTemplate, error) {
	if category != "realistic" && category != "3d" && category != "2d" {
		return filmStyleTemplate{}, fmt.Errorf("invalid category")
	}
	if name == "" || prompt == "" {
		return filmStyleTemplate{}, fmt.Errorf("name and prompt are required")
	}
	if h.styleTemplatesDir() == "" {
		return filmStyleTemplate{}, fmt.Errorf("style library is unavailable")
	}
	if id == "" {
		id = "style_" + uuid.NewString()
	}
	if revision <= 0 {
		revision = 1
	} else {
		revision++
	}
	var width, height int
	var mimeType, sha string
	var storageKey string
	if file != nil {
		data, err := io.ReadAll(io.LimitReader(file, maxFilmStylePreviewBytes+1))
		if err != nil || len(data) == 0 || len(data) > maxFilmStylePreviewBytes {
			return filmStyleTemplate{}, fmt.Errorf("preview image is too large")
		}
		config, format, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || config.Width <= 0 || config.Height <= 0 {
			return filmStyleTemplate{}, fmt.Errorf("invalid preview image")
		}
		if format != "jpeg" && format != "png" && format != "webp" && format != "gif" {
			return filmStyleTemplate{}, fmt.Errorf("unsupported preview image")
		}
		width, height, mimeType = config.Width, config.Height, "image/"+format
		if format == "jpeg" {
			mimeType = "image/jpeg"
		}
		digest := sha256.Sum256(data)
		sha = hex.EncodeToString(digest[:])
		ext := format
		if candidate := strings.TrimPrefix(strings.ToLower(filepath.Ext(filename)), "."); candidate != "" {
			if candidate == "jpg" {
				candidate = "jpeg"
			}
			if candidate == "jpeg" || candidate == "png" || candidate == "webp" || candidate == "gif" {
				ext = candidate
			}
		}
		storageKey = filepath.ToSlash(filepath.Join(id, strconv.Itoa(revision), "preview."+ext))
		path := filepath.Join(h.styleTemplatesDir(), storageKey)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return filmStyleTemplate{}, err
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			return filmStyleTemplate{}, err
		}
	} else {
		var current filmStyleTemplate
		if err := h.db.QueryRow(`SELECT id,category,name,prompt,storage_key,mime_type,sha256,width,height,revision,sort_order,status,created_at,updated_at FROM film_style_templates WHERE id=$1`, id).Scan(&current.ID, &current.Category, &current.Name, &current.Prompt, &current.StorageKey, &current.MimeType, &current.SHA256, &current.Width, &current.Height, &current.Revision, &current.SortOrder, &current.Status, &current.CreatedAt, &current.UpdatedAt); err != nil {
			return filmStyleTemplate{}, err
		}
		width, height, mimeType, sha = current.Width, current.Height, current.MimeType, current.SHA256
		ext := strings.TrimPrefix(filepath.Ext(current.StorageKey), ".")
		if ext == "" {
			ext = "png"
		}
		storageKey = filepath.ToSlash(filepath.Join(id, strconv.Itoa(revision), "preview."+ext))
		base := h.styleTemplatesDir()
		oldPath := filepath.Join(base, filepath.FromSlash(current.StorageKey))
		newPath := filepath.Join(base, filepath.FromSlash(storageKey))
		// storage_key 理论上只由服务端生成（id/revision/preview.ext），这里仍然
		// 校验解析结果不逃出样式目录，防止任何持久化数据被篡改后越界读写。
		if !withinStyleDir(base, oldPath) || !withinStyleDir(base, newPath) {
			return filmStyleTemplate{}, fmt.Errorf("preview storage key escapes the style directory")
		}
		//nolint:gosec // storage key 由服务端生成且已通过 withinStyleDir 包含性校验
		if data, readErr := os.ReadFile(oldPath); readErr == nil {
			if err := os.MkdirAll(filepath.Dir(newPath), 0700); err != nil {
				return filmStyleTemplate{}, err
			}
			if err := os.WriteFile(newPath, data, 0600); err != nil {
				return filmStyleTemplate{}, err
			}
		} else {
			return filmStyleTemplate{}, fmt.Errorf("existing preview is unavailable")
		}
	}
	var item filmStyleTemplate
	err := h.db.QueryRow(`INSERT INTO film_style_templates(id,category,name,prompt,storage_key,mime_type,sha256,width,height,revision,sort_order,status) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,COALESCE((SELECT sort_order FROM film_style_templates WHERE id=$1),0),'active') ON CONFLICT(id) DO UPDATE SET category=EXCLUDED.category,name=EXCLUDED.name,prompt=EXCLUDED.prompt,storage_key=EXCLUDED.storage_key,mime_type=EXCLUDED.mime_type,sha256=EXCLUDED.sha256,width=EXCLUDED.width,height=EXCLUDED.height,revision=EXCLUDED.revision,status='active',updated_at=NOW() RETURNING id,category,name,prompt,storage_key,mime_type,sha256,width,height,revision,sort_order,status,created_at,updated_at`, id, category, name, prompt, storageKey, mimeType, sha, width, height, revision).Scan(&item.ID, &item.Category, &item.Name, &item.Prompt, &item.StorageKey, &item.MimeType, &item.SHA256, &item.Width, &item.Height, &item.Revision, &item.SortOrder, &item.Status, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}
