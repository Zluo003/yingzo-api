package admin

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type FileStorageHandler struct {
	service *service.FileStorageService
}

func NewFileStorageHandler(fileStorageService *service.FileStorageService) *FileStorageHandler {
	return &FileStorageHandler{service: fileStorageService}
}

type fileStorageSettingsResponse struct {
	*service.FileStorageSettings
	EffectivePublicBaseURL string `json:"effective_public_base_url"`
}

func (h *FileStorageHandler) GetSettings(c *gin.Context) {
	h.writeSettings(c, nil)
}

func (h *FileStorageHandler) UpdateSettings(c *gin.Context) {
	var input service.FileStorageConfig
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	settings, err := h.service.UpdateSettings(c.Request.Context(), input)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	h.writeSettings(c, settings)
}

func (h *FileStorageHandler) TestSettings(c *gin.Context) {
	var input service.FileStorageConfig
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if err := h.service.TestSettings(c.Request.Context(), input); err != nil {
		response.Success(c, gin.H{"ok": false, "backend": input.Backend, "message": err.Error()})
		return
	}
	response.Success(c, gin.H{"ok": true, "backend": input.Backend, "message": "connection successful"})
}

func (h *FileStorageHandler) writeSettings(c *gin.Context, settings *service.FileStorageSettings) {
	var err error
	if settings == nil {
		settings, err = h.service.GetSettings(c.Request.Context())
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
	}
	effective, err := h.service.EffectivePublicBaseURL(c.Request.Context(), fileStorageRequestOrigin(c))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, fileStorageSettingsResponse{FileStorageSettings: settings, EffectivePublicBaseURL: effective})
}

func fileStorageRequestOrigin(c *gin.Context) string {
	scheme := "https"
	if c.Request.TLS == nil {
		scheme = "http"
	}
	if forwarded := strings.TrimSpace(strings.Split(c.GetHeader("X-Forwarded-Proto"), ",")[0]); forwarded == "http" || forwarded == "https" {
		scheme = forwarded
	}
	return scheme + "://" + c.Request.Host
}
