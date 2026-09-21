package admin

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type DesktopUpdateHandler struct {
	service *service.DesktopUpdateService
}

func NewDesktopUpdateHandler(updateService *service.DesktopUpdateService) *DesktopUpdateHandler {
	return &DesktopUpdateHandler{service: updateService}
}

func (h *DesktopUpdateHandler) List(c *gin.Context) {
	items, err := h.service.List(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, items)
}

func (h *DesktopUpdateHandler) Storage(c *gin.Context) {
	settings, err := h.service.GetStorage(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

func (h *DesktopUpdateHandler) UpdateStorage(c *gin.Context) {
	var input service.DesktopUpdateStorageConfig
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	settings, err := h.service.UpdateStorage(c.Request.Context(), input)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

func (h *DesktopUpdateHandler) Upload(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not found in context")
		return
	}
	version := strings.TrimSpace(c.PostForm("version"))
	platform := strings.TrimSpace(c.PostForm("platform"))
	arch := strings.TrimSpace(c.PostForm("arch"))
	notes := c.PostForm("release_notes")
	file, header, err := c.Request.FormFile("package")
	if err != nil {
		response.BadRequest(c, "package is required")
		return
	}
	defer file.Close()
	tmp, err := os.CreateTemp("", "yingzo-desktop-update-*")
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.ReadFrom(file); err != nil {
		_ = tmp.Close()
		response.ErrorFrom(c, err)
		return
	}
	if err := tmp.Close(); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	item, err := h.service.CreateFromFile(c.Request.Context(), service.DesktopReleaseInput{Version: version, Platform: platform, Arch: arch, ReleaseNotes: notes, Filename: header.Filename}, tmpPath, subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, item)
}

func (h *DesktopUpdateHandler) Publish(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not found in context")
		return
	}
	item, err := h.service.Publish(c.Request.Context(), c.Param("id"), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, item)
}

func (h *DesktopUpdateHandler) Delete(c *gin.Context) {
	if err := h.service.Delete(c.Request.Context(), c.Param("id")); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

// PublicCheck is served by the dedicated 8081 update listener.
func (h *DesktopUpdateHandler) PublicCheck(c *gin.Context) {
	result, err := h.service.Check(c.Request.Context(), c.Query("version"), c.Query("platform"), c.Query("arch"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *DesktopUpdateHandler) PublicMetadata(c *gin.Context) {
	var data []byte
	var err error
	if id := strings.TrimSpace(c.Param("id")); id != "" {
		data, err = h.service.MetadataByID(c.Request.Context(), id)
	} else {
		platform := "win32"
		if strings.HasSuffix(c.Request.URL.Path, "latest-mac.yml") {
			platform = "darwin"
		}
		data, err = h.service.Metadata(c.Request.Context(), platform, c.Query("arch"))
	}
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Data(http.StatusOK, "text/yaml; charset=utf-8", data)
}

func (h *DesktopUpdateHandler) PublicDownload(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	redirect, err := h.service.RedirectURL(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if redirect != "" {
		c.Redirect(http.StatusFound, redirect)
		return
	}
	path, local, err := h.service.LocalPackagePath(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if local {
		c.Header("Content-Disposition", `attachment; filename="`+strings.ReplaceAll(filepath.Base(path), `"`, "")+`"`)
		c.File(path)
		return
	}
	body, item, err := h.service.OpenPackage(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	defer body.Close()
	c.Header("Content-Disposition", `attachment; filename="`+strings.ReplaceAll(item.PackageFilename, `"`, "")+`"`)
	c.DataFromReader(http.StatusOK, item.PackageSize, "application/octet-stream", body, nil)
}
